// Unit tests for the Node client against a fake plugin server (node:test).

import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, describe, it } from "node:test";

import { PLUGIN_PREFIX, PluginClient, PluginError, compactMessages, message, runTool } from "../index.mjs";
import { TOKEN, startFakeServer } from "./support.mjs";

// routes maps "METHOD /path" (without the prefix, query stripped) to fn(body) -> [status, value].
function router(routes) {
  return async ({ method, path, headers, body }) => {
    if (headers.authorization !== `Bearer ${TOKEN}`) return [401, { error: "missing or invalid token" }];
    const bare = path.split("?")[0].slice(PLUGIN_PREFIX.length);
    const key = `${method} ${bare}`;
    if (!routes[key]) return [404, { error: `no route ${key}` }];
    return routes[key](body ? JSON.parse(body) : undefined, path);
  };
}

describe("PluginClient", () => {
  const servers = [];
  after(async () => {
    for (const s of servers) await s.close();
  });

  async function serve(routes) {
    const s = await startFakeServer(router(routes));
    servers.push(s);
    return new PluginClient({ baseUrl: s.url, token: TOKEN, conversationId: "conv-1" });
  }

  it("reads the manifest", async () => {
    const c = await serve({ "GET /manifest": () => [200, { protocol: "acd-ctx-plugin", protocolVersion: 1, tools: [] }] });
    const m = await c.manifest();
    assert.equal(m.protocol, "acd-ctx-plugin");
    assert.equal(m.protocolVersion, 1);
  });

  it("throws PluginError with the status on a rejected token", async () => {
    const c = await serve({ "GET /manifest": () => [200, {}] });
    const bad = new PluginClient({ baseUrl: c.baseUrl, token: "wrong-token-that-is-long-enough-to-construct" });
    await assert.rejects(() => bad.manifest(), (err) => err instanceof PluginError && err.status === 401);
  });

  it("request reports the status without throwing", async () => {
    const c = await serve({ "GET /manifest": () => [200, { ok: true }] });
    const r1 = await c.request("GET", "/manifest", { auth: "wrong" });
    assert.equal(r1.status, 401);
    const r2 = await c.request("GET", "/manifest", { auth: false });
    assert.equal(r2.status, 401);
  });

  it("sends the runtime-info body", async () => {
    let seen;
    const c = await serve({
      "POST /runtime-info": (body) => {
        seen = body;
        return [200, { sessionId: "plg-1", created: true }];
      },
    });
    const out = await c.runtimeInfo({ model: "m1", contextWindow: 1000, maxOutputTokens: 50, cwd: "/w" });
    assert.equal(out.sessionId, "plg-1");
    assert.deepEqual(seen, { conversationId: "conv-1", model: "m1", contextWindow: 1000, maxOutputTokens: 50, cwd: "/w" });
  });

  it("turn sends messages and returns the plan", async () => {
    let seen;
    const c = await serve({
      "POST /turn": (body) => {
        seen = body;
        return [200, { ingested: body.messages.length, decision: { nudge: true }, visible: body.messages, hints: [], lowWater: 0 }];
      },
    });
    const plan = await c.turn([message("user", "hello"), message("assistant", "hi", "text", { tokens: 3 })], 500);
    assert.equal(seen.tokenCount, 500);
    assert.equal(seen.messages[1].tokens, 3);
    assert.equal(plan.decision.nudge, true);
    assert.equal(plan.ingested, 2);
  });

  it("runTool returns the result and surfaces refusals", async () => {
    const c = await serve({
      "POST /tool": (body) => {
        if (body.name === "retrieve") return [200, { result: { ref: body.arguments.ref, text: "alpha" } }];
        return [422, { error: "range 0: covers 3 tokens" }];
      },
    });
    assert.deepEqual(await c.runTool("retrieve", { ref: "m00001" }), { ref: "m00001", text: "alpha" });
    await assert.rejects(() => c.runTool("compress", { ranges: [] }), (err) => err.status === 422 && err.message.includes("covers 3 tokens"));
  });

  it("status sends conversationId as a query parameter", async () => {
    const c = await serve({
      "GET /status": (_b, path) => {
        assert.equal(new URL(path, "http://x").searchParams.get("conversationId"), "conv-1");
        return [200, { messages: 2 }];
      },
    });
    assert.deepEqual(await c.status(), { messages: 2 });
  });

  it("compact reports 501 until forced compaction exists", async () => {
    const c = await serve({ "POST /compact": () => [501, { error: "compact is not implemented yet" }] });
    await assert.rejects(() => c.compact(), (err) => err.status === 501);
  });

  it("requires a conversation id", async () => {
    const c = new PluginClient({ baseUrl: "http://127.0.0.1:1", token: TOKEN });
    await assert.rejects(() => c.turn([]), /conversationId is required/);
  });

  it("maps an unreachable server to status 0", async () => {
    const c = new PluginClient({ baseUrl: "http://127.0.0.1:1", token: TOKEN, conversationId: "c", timeoutMs: 2000 });
    await assert.rejects(() => c.manifest(), (err) => err instanceof PluginError && err.status === 0);
  });

  it("trims a trailing slash from baseUrl", () => {
    assert.equal(new PluginClient({ baseUrl: "http://127.0.0.1:9/", token: TOKEN }).baseUrl, "http://127.0.0.1:9");
  });
});

describe("helpers", () => {
  const servers = [];
  after(async () => {
    for (const s of servers) await s.close();
  });

  it("compactMessages and runTool use the given client", async () => {
    const s = await startFakeServer(
      router({
        "POST /turn": (body) => [200, { ingested: 1, visible: body.messages, decision: { nudge: false }, hints: [], lowWater: 0 }],
        "POST /tool": () => [200, { result: { messages: 1, activeBlocks: [] } }],
      }),
    );
    servers.push(s);
    const client = new PluginClient({ baseUrl: s.url, token: TOKEN });
    const plan = await compactMessages([message("user", "x")], 10, { client, conversationId: "c9" });
    assert.equal(plan.visible[0].text, "x");
    const status = await runTool("ctx_status", {}, { client, conversationId: "c9" });
    assert.equal(status.messages, 1);
  });

  it("fromEnv reads the token file and the environment", () => {
    const dir = mkdtempSync(join(tmpdir(), "acd-ctx-node-"));
    const path = join(dir, "plugin.token");
    writeFileSync(path, `${TOKEN}\n`);
    const saved = { ...process.env };
    try {
      process.env.ACD_CTX_URL = "http://127.0.0.1:9999";
      process.env.ACD_CTX_TOKEN_FILE = path;
      process.env.ACD_CTX_CONVERSATION = "from-env";
      delete process.env.ACD_CTX_TOKEN;
      const c = PluginClient.fromEnv();
      assert.equal(c.token, TOKEN);
      assert.equal(c.baseUrl, "http://127.0.0.1:9999");
      assert.equal(c.conversationId, "from-env");
    } finally {
      process.env = saved;
    }
  });

  it("exports the protocol prefix", () => {
    assert.equal(PLUGIN_PREFIX, "/__acd-ctx/plugin");
  });
});
