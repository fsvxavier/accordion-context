// Shared helpers for the node:test suites: a fake plugin server, the vector
// loader and the subset matcher used by the conformance replay.

import { readFileSync, readdirSync } from "node:fs";
import { createServer } from "node:http";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const TOKEN = "0123456789abcdef0123456789abcdef0123";
export const PLUGIN_PREFIX = "/__acd-ctx/plugin";
export const VECTOR_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "../../../conformance/vectors");

/**
 * A loopback server that answers with handler(req) -> [status, jsonValue].
 * req has method, path, headers and body (a string).
 */
export async function startFakeServer(handler) {
  const server = createServer((req, res) => {
    let body = "";
    req.setEncoding("utf8");
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      Promise.resolve(handler({ method: req.method, path: req.url, headers: req.headers, body }))
        .then(([status, value]) => {
          const payload = value === undefined || value === null ? "" : JSON.stringify(value);
          res.writeHead(status, { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(payload) });
          res.end(payload);
        });
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${server.address().port}`;
  return {
    url,
    close: () => new Promise((r) => { server.closeAllConnections(); server.close(() => r()); }),
  };
}

export function loadSuites(dir = VECTOR_DIR) {
  return readdirSync(dir)
    .filter((f) => f.endsWith(".json"))
    .sort()
    .map((f) => JSON.parse(readFileSync(join(dir, f), "utf8")));
}

/** Replace {conv} in every string of a JSON value. */
export function substitute(value, conv) {
  if (typeof value === "string") return value.split("{conv}").join(conv);
  if (Array.isArray(value)) return value.map((v) => substitute(v, conv));
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, substitute(v, conv)]));
  }
  return value;
}

/** Return null when actual contains expected, else a reason string. "$any" matches any present value. */
export function match(expected, actual, at = "$") {
  if (expected === "$any") return actual === null || actual === undefined ? `${at}: value is null or missing` : null;
  if (Array.isArray(expected)) {
    if (!Array.isArray(actual)) return `${at}: want array, got ${typeof actual}`;
    if (expected.length !== actual.length) return `${at}: want ${expected.length} items, got ${actual.length}`;
    for (let i = 0; i < expected.length; i++) {
      const r = match(expected[i], actual[i], `${at}[${i}]`);
      if (r) return r;
    }
    return null;
  }
  if (expected && typeof expected === "object") {
    if (!actual || typeof actual !== "object" || Array.isArray(actual)) return `${at}: want object`;
    for (const [k, ev] of Object.entries(expected)) {
      if (!(k in actual)) return `${at}: missing "${k}"`;
      const r = match(ev, actual[k], `${at}.${k}`);
      if (r) return r;
    }
    return null;
  }
  return expected === actual ? null : `${at}: want ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`;
}

/** The fake server answers "$any" fields with a placeholder. */
export function placeholder(value) {
  if (value === "$any") return "x";
  if (Array.isArray(value)) return value.map(placeholder);
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, placeholder(v)]));
  return value;
}

function authOK(auth, header) {
  if (auth === true || auth === undefined) return header === `Bearer ${TOKEN}`;
  if (auth === false) return header === undefined;
  if (auth === "wrong") return typeof header === "string" && header.startsWith("Bearer ") && header !== `Bearer ${TOKEN}`;
  return false;
}

/**
 * Fake server that checks each request against the next expected step of a
 * vector and answers with that step's expected response. Mismatches are kept
 * in mismatches and checked by the test.
 */
export async function startReplayServer(steps, conv) {
  const mismatches = [];
  let index = 0;
  const srv = await startFakeServer(({ method, path, headers, body }) => {
    if (index >= steps.length) {
      mismatches.push(`unexpected request ${method} ${path}`);
      return [500, { error: "unexpected request" }];
    }
    const i = index++;
    const step = steps[i];
    const req = step.request;
    const want = `${PLUGIN_PREFIX}/${substitute(req.path, conv)}`;
    if (method !== req.method || path !== want) mismatches.push(`step ${i}: got ${method} ${path}, want ${req.method} ${want}`);
    if (!authOK(req.auth, headers.authorization)) mismatches.push(`step ${i}: auth header ${headers.authorization} does not match ${req.auth}`);
    if ("raw" in req) {
      if (body !== req.raw) mismatches.push(`step ${i}: raw body ${JSON.stringify(body)}`);
    } else if ("body" in req) {
      let got;
      try { got = JSON.parse(body); } catch { got = undefined; }
      if (JSON.stringify(got) !== JSON.stringify(substitute(req.body, conv))) mismatches.push(`step ${i}: body does not match`);
    } else if (body !== "") {
      mismatches.push(`step ${i}: unexpected body ${JSON.stringify(body)}`);
    }
    const exp = step.expect;
    let value = placeholder(exp.json);
    if (exp.keys) {
      value = { ...(value || {}) };
      for (const k of exp.keys) if (!(k in value)) value[k] = "x";
    }
    return [exp.status, value];
  });
  return { url: srv.url, mismatches, served: () => index, close: srv.close };
}
