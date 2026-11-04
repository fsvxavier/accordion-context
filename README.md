# Accordion Context (`acd-ctx`)

**English** | [Português (Brasil)](README.pt-BR.md) | [Español (España)](README.es.md)

Accordion Context is a local proxy that keeps long coding-agent sessions inside a limited context window. The model summarizes older parts of the conversation, the summaries replace the original messages in the window, and the originals stay in a SQLite database so they can be recovered.

> **Status: early development (pre-1.0).** The proxy, the client launchers, the `project`, `config`, `attach`, `doctor` and `mcp` commands, the plugin HTTP protocol with its Python and Node clients, and the `pkg/acdctx` library are implemented and pass `go test ./...`. Nothing has been validated against the live provider APIs or the real client binaries. See [Status](#status).

## What it is

Coding agents such as Claude Code, Codex, Gemini CLI and OpenCode can run for days, but the model sees a window of 100K to 200K tokens. Accordion Context reimplements the context-compaction model of [billion-context](https://github.com/ranxianglei/billion-context) (`acp-kernel`) in Go, with these properties:

- **Incremental.** Only new content that has already been consumed is folded. The whole history is never rewritten at once.
- **Reversible.** Originals are never deleted. A folded range is hidden from the rendered window and can be recovered.
- **Hierarchical.** Summaries are organized in tiers: T2 summarizes T1 blocks and T3 summarizes T2 blocks.
- **The model writes the summaries.** The system decides when to ask for one, which range to offer, and how to rebuild the window.
- **Proxy plus SQLite as source of truth.** SQLite (WAL mode, FTS5, pure-Go driver `modernc.org/sqlite`) holds the append-only event log, blocks, sessions and cache samples.

Differences from billion-context, as stated in the design spec `compactacao-contexto-go-sqlite.md` (Portuguese, v0.1; §1 to §3):

- Go instead of TypeScript, with a pure `internal/kernel` package that does no I/O.
- The trigger is one decision table, implemented as the pure function `kernel.DecideNudge`. Its table tests are the executable specification.
- A compression counts only when a `compression_events` row exists in the database. Compression-looking text in the model's output is not trusted.
- Named invariants (I1 to I9) are enforced by SQL checks in the store and by property tests.
- Cache cost is modeled explicitly, and a cache gate can defer a nudge.

## How it works

1. The agent's request goes to the proxy, which records each message in the session with a stable reference (for example `m00451`).
2. The trigger decides whether compaction is due. The cache gate can postpone the nudge when a rewrite would break the provider's prompt cache at a high cost.
3. When due, the proxy adds a nudge that lists the compressible ranges. The model answers with a `compress` call: ranges plus a summary.
4. The proxy validates the call and writes the block and the hidden ranges in one transaction.
5. The next window shows the summary in place of the folded messages. The originals remain in SQLite.
6. When the model needs details, it calls `decompress`. Internal `compress` and `decompress` calls run inside the proxy (up to 4 rounds per request by default) and do not reach the client.
7. Any failure in the compaction path is fail-open: the original request is forwarded unchanged.

## Supported protocols

The proxy recognizes four API dialects on `POST`:

| Dialect | Endpoint recognized by the proxy | Notes |
|---|---|---|
| Anthropic Messages | `/v1/messages` | Streaming is relayed as it arrives. |
| OpenAI Chat Completions | `/v1/chat/completions` | Each upstream round is read in full before it is sent. |
| OpenAI Responses | `/v1/responses` | **Stateless only.** Requests with `previous_response_id` or `conversation` are forwarded unchanged. WebSocket mode is not handled. |
| Google Gemini | `.../models/<model>:generateContent`, and `:streamGenerateContent` with `alt=sse` | Streams are buffered per round. Without `alt=sse` the request is forwarded unchanged. |

Other paths, and other methods, are passed through to the upstream without compaction.

## Installation

Requirements: Go 1.27.1 or later (the `go` directive in `go.mod`). No C compiler is needed: SQLite comes from `modernc.org/sqlite`, which is pure Go.

Build from a clone (verified, also with `CGO_ENABLED=0`):

```sh
git clone https://github.com/fsvxavier/accordion-context.git
cd accordion-context
go build -o acd-ctx ./cmd/acd-ctx
./acd-ctx help
```

Install from the module path:
Install from the module path, once a version is tagged:
```sh
go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@latest
```

No version tag exists yet, so `@latest` does not resolve today. Until then, install a specific commit with `go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@<commit>`, or build from source as above. The `version` value is `dev` unless the binary is built with `-ldflags "-X main.version=<tag>"`.

## Quick start

Start the proxy on loopback, forwarding to `api.anthropic.com`:

```sh
acd-ctx serve
curl -s http://127.0.0.1:8787/__acd-ctx/health
# {"ok":true}
```

Launcher mode starts its own proxy on a free loopback port (or reuses a healthy proxy of the same version), sets the client's base URL, and stops the proxy when the client exits:

```sh
acd-ctx claude        # sets ANTHROPIC_BASE_URL for this process only
```

Base-URL mode points an existing proxy at a client yourself. Two path forms exist:

```sh
# Plain form: default upstream, project from the X-Acd-Ctx-Project-Dir header or "default"
ANTHROPIC_BASE_URL=http://127.0.0.1:8787 claude

# Explicit form: project ID in /p/<id>/, upstream in /ctx/<url>/ (must be allowed)
ANTHROPIC_BASE_URL=http://127.0.0.1:8787/p/<project-id>/ctx/https://api.anthropic.com claude

# Another provider needs its origin in --allow. Not yet verified with the real client.
acd-ctx serve --allow https://api.openai.com
OPENAI_BASE_URL=http://127.0.0.1:8787/ctx/https://api.openai.com/v1 codex
```

`serve` flags (all optional):

| Flag | Default | Meaning |
|---|---|---|
| `--listen` | `127.0.0.1:8787` | Must be a loopback address, otherwise the command refuses to start. |
| `--upstream` | `https://api.anthropic.com` | Upstream used for requests without a `/ctx/<url>/` prefix. |
| `--allow` | origin of `--upstream` | Comma-separated origins reachable through `/ctx/<url>/`. |
| `--db` | `~/.local/share/acd-ctx/acd-ctx.db` | SQLite database path. |
| `--config` | user config path | Global configuration file. |
| `--markers` | `go.mod,package.json,pyproject.toml,composer.json,Cargo.toml` | Project root markers. |
| `--plugin-token-file` | `~/.local/share/acd-ctx/plugin.token` | File that receives the per-run plugin bearer token (mode `0600`). If it cannot be written, the plugin protocol is disabled and the proxy keeps running. |

## Integration modes

| Mode | Command | Status | Notes |
|---|---|---|---|
| **Base URL** | `acd-ctx serve` plus the client's base-URL variable | Implemented | Verified with HTTP requests. Real clients not run. |
| **Launcher** | `acd-ctx claude`, `codex`, `gemini`, `opencode`, `run -- <cmd>` | Implemented for claude, codex, gemini | `run` only accepts a command that matches a built-in profile. `opencode` has no base-URL variable and is refused, so use `attach`. Checked with stand-in client binaries. |
| **Attach** | `acd-ctx attach [--mitm-domain <host>] -- <cmd> [args]` | Implemented (base-URL and MITM degrees) | Probes each degree before starting the client and uses the first that works. Any command works in console mode. |
| **MITM** | the `mitm` degree of `attach` | Implemented | Only allowlisted hosts are terminated. Other hosts are tunnelled untouched. The CA is per install and is not added to the system trust store. |
| **System proxy, transparent, temporary config** | `attach --system-proxy` / `--transparent`; temporary base-URL config file (spec degree 4) | Not implemented | The two flags are refused with an error. `attach` never tries degree 4. |
| **Plugin** (HTTP loopback, spec §19.4) | `acd-ctx serve`, under `/__acd-ctx/plugin/` | Implemented | Bearer token created per run. `compact` returns 501 and `search_context` fails (the store has no search API yet). See [Plugin protocol and middleware](#plugin-protocol-and-middleware). |
| **Middleware** | `middleware/python`, `middleware/node` | Implemented | Dependency-free clients of the plugin protocol, tested against the conformance vectors. |
| **MCP companion** | `acd-ctx mcp` (stdio) | Implemented | Tools: `decompress`, `retrieve`, `search_context`, `ctx_status`. `search_context` currently returns an error. |
| **Go library** | `pkg/acdctx` | Implemented | `Open` and `Client` (Turn, Compress, Decompress, Recall), `NewTransport` (an `http.RoundTripper`) and `Handler`. The API may change before 1.0. |
| **Doctor** | `acd-ctx doctor [--json] [client]` | Implemented | Checks the install and the integration. Exit code 0 for PASS, 3 for WARN, 1 for FAIL. |

## Configuration

Values are resolved per key, from lowest to highest precedence:

1. Built-in defaults.
2. Global file: `$XDG_CONFIG_HOME/acd-ctx/config.yaml`, or `~/.config/acd-ctx/config.yaml` on Linux.
3. `projects` overrides in the global file, for each pattern that matches the project root.
4. Local project file: `<project root>/.acd-ctx/config.yaml`.
5. Environment variables `ACD_CTX_*`.
6. Explicit flags.

Example, using real keys (it loads without warnings):

```yaml
# ~/.config/acd-ctx/config.yaml
trigger:
  u_high: 0.75          # start compacting at 75% of the window
  step_base: 50000      # tokens between nudges, before adaptive scaling
compress:
  preserve_recent_messages: 3
ccr:
  crush: true           # shrink very large tool outputs deterministically
storage: global         # or "local": keep the project database in .acd-ctx/
```

The real keys, with their defaults (`config show --effective` prints the same list). The `local` column says whether a project's `.acd-ctx/config.yaml` may set the key:

| Key | Local | Default |
|---|---|---|
| `trigger.u_floor`, `trigger.u_high`, `trigger.u_emerg` | yes | `0.3`, `0.75`, `0.95` |
| `trigger.step_base`, `trigger.step_min`, `trigger.step_max` | yes | `50000`, `30000`, `100000` |
| `trigger.step_read_gain`, `trigger.min_growth_floor`, `trigger.max_refusals`, `trigger.tiers_enabled` | yes | `1`, `20000`, `3`, `true` |
| `cache.ttl`, `cache.margin`, `cache.price_uncached`, `cache.price_cached` | no | `5m`, `1.5`, `0` (not set), `0` (not set) |
| `compress.preserve_recent_messages`, `compress.preserve_recent_tokens` | yes | `3`, `5000` |
| `compress.min_compress_range`, `compress.min_summary_chars`, `compress.max_summary_chars` | yes | `5000`, `50`, `20000` |
| `compress.protected_tools`, `compress.framing_check` | yes | empty list, `false` |
| `compress.user_turn_after_summaries`, `compress.intent_ledger` | yes | `true`, `true` |
| `ccr.threshold_tokens`, `ccr.crush` | yes | `4000`, `true` |
| `recall.assist`, `recall.min_accept_rate` | yes | `true`, `0.05` |
| `storage` | yes | `global` |

Keys with the prefixes `upstream.`, `credentials.`, `listen.` and `tools.external_summary.` are reserved. They are ignored with a warning, in the global and the local file.

Environment variables use the key in upper case, with dots replaced by underscores:

```sh
ACD_CTX_TRIGGER_STEP_BASE=60000 acd-ctx claude
```

Show the effective value and the source of every key:

```sh
acd-ctx config show --effective
```

The proxy reloads configuration for each project root at most every 30 seconds, so most edits do not need a restart.

### Security note on local config

`<project>/.acd-ctx/config.yaml` lives inside the repository you are working on, so it may come from a clone you did not write. It can change trigger, compression, CCR, recall and storage behavior, but not `upstream.*`, `credentials.*` or `listen.*`. Review it like any other project file.

## Per-project separation

- **Detection.** Walking up from the working directory, the first match wins: a `.acd-ctx/` directory, then `.git`, then a root marker. Otherwise the working directory is the root.
- **Project ID.** A project with `.acd-ctx/project-id` uses that value. Otherwise the ID is the first 16 hex characters of the SHA-256 of the root path.
- **Requests.** The proxy takes the project from the `/p/<id>/` prefix, then from the `X-Acd-Ctx-Project-Dir` header. The conversation comes from `X-Acd-Ctx-Conversation`. Requests with no project signal use the `default` project and log a warning.
- **Storage.** `global` keeps every project in one database. `local` keeps one project in `.acd-ctx/acd-ctx.db`.

Project commands:

```sh
acd-ctx project init [--local-storage]   # create .acd-ctx/ at the project root
acd-ctx project show                     # detected project, root, storage and database
acd-ctx project list                     # projects in the global database
acd-ctx project move <old> <new>         # re-point a moved project's root path
acd-ctx project migrate --to local|global
acd-ctx project purge <id> [--yes]       # delete all data of a project
```

## Environment variables

| Variable | Used by | Meaning |
|---|---|---|
| `ACD_CTX_<KEY>` | config loader | Overrides one configuration key, for example `ACD_CTX_CCR_CRUSH=false`. |
| `ACD_CTX_DB` | CLI commands, `attach`, `doctor` | Global database path. `serve` uses `--db` instead. |
| `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `HOME` | paths | Base directories. `serve` ignores `XDG_DATA_HOME`. |
| `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GOOGLE_GEMINI_BASE_URL` | your client | Point the client at the proxy in base-URL mode. Set by the launchers. |
| `HTTPS_PROXY`, `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` | `attach` (MITM degree) | Set for that client process only, so it trusts the local CA. |
| `ACD_CTX_URL`, `ACD_CTX_TOKEN`, `ACD_CTX_TOKEN_FILE`, `ACD_CTX_CONVERSATION` | `middleware/` clients | Locate and authenticate against the plugin protocol. See [Plugin protocol and middleware](#plugin-protocol-and-middleware). |

## Endpoints

Control endpoints:

| Path | Method | Response |
|---|---|---|
| `/__acd-ctx/health` | GET | `{"ok":true}` |
| `/__acd-ctx/status` | GET | JSON with `status`, `version`, `uptime_seconds`, `upstream_host`, `context_limit`, `max_loop`, `sessions_loaded`, `projects_seen`, `default_project_requests` and `protocols`. |

API requests use the protocol paths in [Supported protocols](#supported-protocols), optionally behind `/p/<id>/` and `/ctx/<url>/`. The MCP companion has no HTTP endpoint; it speaks JSON-RPC over stdio.

## Plugin protocol and middleware

The plugin protocol (spec §19.4) lets a program that builds its own prompts ask the proxy for the visible window of a conversation and run the context tools. `acd-ctx serve` mounts it under `/__acd-ctx/plugin/`. Every request needs `Authorization: Bearer <token>`. `serve` creates a new random token on each run and writes it to the file named by `--plugin-token-file` (mode `0600`). The Host and Origin guards of the proxy apply.

| Path (under `/__acd-ctx/plugin/`) | Method | Purpose |
|---|---|---|
| `manifest` | GET | Protocol name and version (`acd-ctx-plugin`, 1), tools and doctrine. |
| `runtime-info` | POST | Declare the conversation, model and context window. |
| `turn` | POST | Send messages and a token count; get the decision, hints and the visible window. |
| `tool` | POST | Run `compress`, `decompress`, `retrieve`, `search_context` or `ctx_status`. |
| `status` | GET | Same data as `ctx_status`, for `?conversationId=`. |
| `compact` | POST | Not implemented: always HTTP 501. |

The clients live in `middleware/python` (package `acd_ctx`, standard library only) and `middleware/node` (package `acd-ctx`, ESM, Node.js 18.13 or later, no dependencies). Both read `ACD_CTX_URL` (default `http://127.0.0.1:8787`), `ACD_CTX_TOKEN` or the file in `ACD_CTX_TOKEN_FILE` (default `~/.local/share/acd-ctx/plugin.token`), and `ACD_CTX_CONVERSATION`. On any client error, continue without compression.

`conformance/vectors/*.json` are language-neutral request and expectation vectors. `go test ./conformance/...` replays them against the real plugin handler. The Python and Node test suites replay the same files against a fake server. Set `ACD_CTX_CONFORMANCE_URL` and `ACD_CTX_CONFORMANCE_TOKEN` to replay against a running `acd-ctx serve`.

`docs/compatibility.md` is the generated compatibility matrix of protocols and client profiles. CI fails when the committed file differs from the output of `go run ./scripts/gen-compat-matrix`.

## Security notes

- **Loopback only.** `serve` refuses a non-loopback `--listen` address. The proxy carries API credentials.
- **Host guard.** Requests are accepted only when the `Host` header names `localhost`, `127.0.0.1`, `::1` or the listen host. Other names get HTTP 403, which blocks DNS rebinding.
- **Browser access.** CORS preflight requests get HTTP 403. Control endpoints under `/__acd-ctx/` refuse any request with an `Origin` header.
- **Plugin token.** Plugin endpoints require a bearer token, compared in constant time. It is regenerated on each `serve` run and stored in a file with mode `0600`.
- **Upstream allowlist.** A `/ctx/<url>/` target is used only if its origin is in `--allow`. Redirects are returned to the client, not followed.
- **Credentials.** Credential headers are forwarded to the upstream. Logs record session IDs, stages and errors, not request headers.
- **Fail-open.** A compaction error forwards the request unchanged.
- **Limits.** Request bodies are capped at 32 MiB. The database directory is created with mode `0700`, and the MITM CA key with `0600`.

## Development

```sh
go test ./...                                  # full suite
go test ./internal/kernel -run TestName        # one test
go test -fuzz=FuzzParseToolArguments ./internal/wire/openai
go test -fuzz=FuzzDecodeFrame ./internal/plugin
go test -race ./...                            # as in CI
go test ./conformance/...                      # plugin protocol vectors
(cd middleware/python && python3 -m unittest discover -s tests)
(cd middleware/node && node --test)
go run ./scripts/gen-compat-matrix             # prints docs/compatibility.md
```

`CLAUDE.md` is in English. The design spec `compactacao-contexto-go-sqlite.md` is in Portuguese and is the source of truth. Section numbers (§N) in code comments refer to it.

CI (`.github/workflows/ci.yml`) runs `gofmt`, `go vet`, `go test -race`, the conformance vectors, the Python and Node client tests, and a check that `docs/compatibility.md` is current.

| Package | Role |
|---|---|
| `internal/kernel` | Pure logic: trigger, tiers, render, recall, CCR and crush, cache cost. No I/O. |
| `internal/store` | SQLite access (WAL, FTS5), schema and invariant checks. |
| `internal/session` | Turn orchestration, tools and the compression transaction. The only writer. |
| `internal/proxy` | HTTP server, routing, protocol adapters, server-side tool loop, fork handling. |
| `internal/wire/...` | Codecs for `anthropic`, `openai`, `responses` and `google`. |
| `internal/config` | Layered configuration and effective-source reporting. |
| `internal/project` | Project root and ID detection. |
| `internal/launcher` | Client profiles and process launch with environment variables. |
| `internal/attach` | Integration ladder for `attach`: probes, console and MITM degrees. |
| `internal/mitm` | Local CA, leaf certificates and scoped TLS interception. |
| `internal/plugin` | stdio MCP server and the HTTP plugin handler mounted by `serve`. |
| `internal/doctor` | Installation and integration checks. |
| `internal/cli` | Command implementations behind `cmd/acd-ctx`. |
| `pkg/acdctx` | Public Go library: client API and HTTP transport and handler. |
| `middleware/python`, `middleware/node` | Plugin protocol clients (no dependencies). |
| `conformance` | Language-neutral plugin vectors and the Go runner. |
| `scripts/gen-compat-matrix` | Generates `docs/compatibility.md`. |

## Status

Validated by automated tests, with no network access beyond loopback:

- Unit and table tests for the kernel, store, config, project, launcher, attach, MITM and doctor.
- Property tests (`pgregory.net/rapid`) that check the invariants after random compression steps.
- Fuzz targets for the tool-argument parsers of each wire codec, the `compress` handler and the plugin JSON-RPC frames.
- Proxy tests against fake upstreams (`httptest` on loopback) for the four dialects, including streaming.
- Plugin conformance vectors, replayed by Go, Python and Node.
- Launchers, `attach` and MITM with stand-in client binaries.

Not validated:

- The real provider APIs (Anthropic, OpenAI, Google): wire details, streaming edge cases, `usage` values, cache behavior and prices.
- The real agent clients (Claude Code, Codex, Gemini CLI, OpenCode) running through the proxy or MITM.
- Summary quality in long real sessions.

## Limitations and risks

- **Lossy compression.** A summary replaces the messages it covers in the window. The model may summarize incorrectly or omit details. Originals are recoverable with `decompress`, but only if the model or you ask for them.
- **Cache cost.** Rewriting an earlier part of the prompt invalidates the provider's prompt cache. The cache gate reduces this cost but does not remove it. Cache prices are provider-specific and must be set by you.
- **Not validated against live APIs or real clients.** See [Status](#status). Tests use fake upstreams and stand-in client binaries.
- **WebSocket not handled.** Responses WebSocket traffic is not compacted.
- **Stateful Responses not compacted.** Requests that chain through `previous_response_id` or `conversation` are forwarded unchanged.
- **Not implemented.** `attach --system-proxy`, `--transparent` and the temporary-config degree; the plugin `compact` endpoint; `search_context` in the plugin and the MCP companion.
- **Token counts are estimates.** They are calibrated against provider `usage` values but are not exact.
- **Pre-1.0.** No release tag exists yet, and the spec (v0.1) and the library API may change.


## License

License: TBD
