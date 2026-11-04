// Accordion Context client for the plugin protocol (spec section 19.4).
// Built-in fetch and Node.js APIs only; no dependencies.
//
// Every call goes to the proxy's /__acd-ctx/plugin/ endpoints with the instance
// bearer token. Calls that get an HTTP error status throw PluginError; treat
// any failure as "continue without compression" (fail-open, spec section 19.7).

import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export const PLUGIN_PREFIX = "/__acd-ctx/plugin";
export const DEFAULT_URL = "http://127.0.0.1:8787";
const DEFAULT_TIMEOUT_MS = 10_000;

export class PluginError extends Error {
  constructor(status, message, body) {
    super(`acd-ctx plugin: HTTP ${status}: ${message}`);
    this.name = "PluginError";
    this.status = status;
    this.body = body;
  }
}

/** Where serve writes the token when --plugin-token-file is not given. */
export function defaultTokenPath() {
  return join(homedir(), ".local", "share", "acd-ctx", "plugin.token");
}

/** Build one wire message. Extra keys: toolName, toolCallId, tokens. */
export function message(role, text, kind = "text", extra = {}) {
  return { role, kind, text, ...extra };
}

export class PluginClient {
  /**
   * @param {object} opts
   * @param {string} [opts.baseUrl] origin of the proxy, e.g. http://127.0.0.1:8787
   * @param {string} opts.token instance bearer token
   * @param {string} [opts.conversationId] default conversation for calls
   * @param {number} [opts.timeoutMs]
   */
  constructor({ baseUrl = DEFAULT_URL, token, conversationId, timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
    if (!token) throw new Error("token is required");
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.token = token;
    this.conversationId = conversationId;
    this.timeoutMs = timeoutMs;
  }

  /**
   * Build a client from ACD_CTX_URL, ACD_CTX_TOKEN or ACD_CTX_TOKEN_FILE, and
   * ACD_CTX_CONVERSATION. The token file defaults to ~/.local/share/acd-ctx/plugin.token.
   */
  static fromEnv({ conversationId } = {}) {
    const env = process.env;
    let token = (env.ACD_CTX_TOKEN ?? "").trim();
    if (!token) {
      token = readFileSync(env.ACD_CTX_TOKEN_FILE || defaultTokenPath(), "utf8").trim();
    }
    return new PluginClient({
      baseUrl: env.ACD_CTX_URL || DEFAULT_URL,
      token,
      conversationId: conversationId || env.ACD_CTX_CONVERSATION || undefined,
    });
  }

  /**
   * Send one request. Resolves to { status, body } (body is parsed JSON or null)
   * and does not throw for an HTTP status. path is relative to /__acd-ctx/plugin.
   * opts.auth is true (instance token), false (no header) or "wrong".
   * opts.raw, when set, is sent as the body instead of opts.body.
   * Throws PluginError with status 0 when the server cannot be reached.
   */
  async request(method, path, { body, auth = true, raw } = {}) {
    const headers = { Accept: "application/json" };
    let payload;
    if (raw !== undefined) payload = raw;
    else if (body !== undefined) payload = JSON.stringify(body);
    else if (method === "POST") payload = "";
    if (payload !== undefined) headers["Content-Type"] = "application/json";
    if (auth === true) headers.Authorization = `Bearer ${this.token}`;
    else if (auth === "wrong") headers.Authorization = `Bearer ${"x".repeat(40)}`;

    let res;
    try {
      res = await fetch(`${this.baseUrl}${PLUGIN_PREFIX}/${path.replace(/^\/+/, "")}`, {
        method,
        headers,
        body: payload,
        signal: AbortSignal.timeout(this.timeoutMs),
      });
    } catch (err) {
      throw new PluginError(0, `unreachable: ${err.message}`, err);
    }
    const text = await res.text();
    let parsed = null;
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch {
        parsed = null;
      }
    }
    return { status: res.status, body: parsed };
  }

  async #call(method, path, body) {
    const { status, body: parsed } = await this.request(method, path, { body });
    if (status >= 400) {
      const msg = parsed && typeof parsed === "object" ? parsed.error : undefined;
      throw new PluginError(status, msg || "request failed", parsed);
    }
    return parsed;
  }

  #cid(conversationId) {
    const cid = conversationId || this.conversationId;
    if (!cid) throw new Error("conversationId is required");
    return cid;
  }

  /** GET manifest: protocol version, tool specs and doctrine. */
  async manifest() {
    return this.#call("GET", "/manifest");
  }

  /** POST runtime-info. The window and model apply when the session is created. */
  async runtimeInfo({ model = "", contextWindow = 0, maxOutputTokens = 0, cwd = "", conversationId } = {}) {
    return this.#call("POST", "/runtime-info", {
      conversationId: this.#cid(conversationId),
      model,
      contextWindow,
      maxOutputTokens,
      cwd,
    });
  }

  /**
   * POST turn: ingest the transcript and return the plan. The response has
   * ingested, lowWater, decision, hints and visible (the window to send to the model).
   */
  async turn(messages, tokenCount = 0, { conversationId } = {}) {
    return this.#call("POST", "/turn", {
      conversationId: this.#cid(conversationId),
      tokenCount,
      messages: [...messages],
    });
  }

  /** POST tool and return its result. name is one of the manifest tools. */
  async runTool(name, args = {}, { conversationId } = {}) {
    const out = await this.#call("POST", "/tool", {
      conversationId: this.#cid(conversationId),
      name,
      arguments: args,
    });
    return out.result;
  }

  /** GET status for the conversation. */
  async status(conversationId) {
    const query = new URLSearchParams({ conversationId: this.#cid(conversationId) });
    return this.#call("GET", `/status?${query}`);
  }

  /** POST compact: offer the compressible spans and nudge the next turn. */
  async compact(conversationId) {
    return this.#call("POST", "/compact", { conversationId: this.#cid(conversationId) });
  }
}

/**
 * Ingest the transcript and return the turn plan. Use result.visible as the
 * window for the model. If result.decision.nudge is true, ask for a compress call.
 * Without opts.client, PluginClient.fromEnv() is used.
 */
export async function compactMessages(messages, tokenCount = 0, { client, conversationId } = {}) {
  const c = client ?? PluginClient.fromEnv({ conversationId });
  return c.turn(messages, tokenCount, { conversationId });
}

/**
 * Run one tool (compress, decompress, retrieve, ctx_status) and return its result.
 * Without opts.client, PluginClient.fromEnv() is used.
 */
export async function runTool(name, args = {}, { client, conversationId } = {}) {
  const c = client ?? PluginClient.fromEnv({ conversationId });
  return c.runTool(name, args, { conversationId });
}
