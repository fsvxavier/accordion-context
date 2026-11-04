// Replays the shared vectors in conformance/vectors (see the Go runner for the format).
// Without ACD_CTX_CONFORMANCE_URL and ACD_CTX_CONFORMANCE_TOKEN, each vector runs
// against a fake server that checks every request and answers with the vector's
// expected response. With both set, the vectors run against that live server.

import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { describe, it } from "node:test";

import { PluginClient, PluginError } from "../index.mjs";
import { TOKEN, loadSuites, match, startReplayServer, substitute } from "./support.mjs";

async function runSteps(client, steps, conv) {
  for (let i = 0; i < steps.length; i++) {
    const { request: req, expect: exp } = steps[i];
    const opts = { auth: req.auth ?? true };
    if ("raw" in req) opts.raw = req.raw;
    else if ("body" in req) opts.body = substitute(req.body, conv);
    const { status, body } = await client.request(req.method, substitute(req.path, conv), opts);
    assert.equal(status, exp.status, `step ${i}: ${req.method} ${req.path}`);
    for (const key of exp.keys ?? []) {
      assert.ok(body && typeof body === "object" && key in body, `step ${i}: missing key "${key}"`);
    }
    if ("json" in exp) {
      const reason = match(exp.json, body);
      assert.equal(reason, null, `step ${i}: ${reason}`);
    }
  }
}

describe("conformance vectors", () => {
  const live = process.env.ACD_CTX_CONFORMANCE_URL && process.env.ACD_CTX_CONFORMANCE_TOKEN;
  const suites = loadSuites();
  it("finds vector files", () => {
    assert.ok(suites.length > 0, "no vector files found");
  });

  for (const suite of suites) {
    describe(suite.suite, () => {
      for (const vec of suite.vectors) {
        it(vec.name, async () => {
          if (live) {
            const conv = `conf-${vec.name}-${randomBytes(4).toString("hex")}`;
            const client = new PluginClient({
              baseUrl: process.env.ACD_CTX_CONFORMANCE_URL,
              token: process.env.ACD_CTX_CONFORMANCE_TOKEN,
              timeoutMs: 10_000,
            });
            await runSteps(client, vec.steps, conv);
            return;
          }
          const conv = `conf-${vec.name}`;
          const server = await startReplayServer(vec.steps, conv);
          try {
            const client = new PluginClient({ baseUrl: server.url, token: TOKEN, timeoutMs: 5000 });
            await runSteps(client, vec.steps, conv);
            assert.deepEqual(server.mismatches, []);
            assert.equal(server.served(), vec.steps.length);
          } catch (err) {
            if (err instanceof PluginError) assert.fail(`plugin call failed: ${err.message}`);
            throw err;
          } finally {
            await server.close();
          }
        });
      }
    });
  }
});
