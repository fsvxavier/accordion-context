# Accordion Context (`acd-ctx`)

**English** | [Português (Brasil)](README.pt-BR.md) | [Español (España)](README.es.md)

Accordion Context is a local proxy that keeps long coding-agent sessions inside a limited context window. The model summarizes older parts of the conversation, the summaries replace the original messages in the window, and the originals stay in a SQLite database so they can be recovered.

> **Status: early development (pre-1.0).** The proxy (with its four internal tools), the launchers, the `attach` ladder (including the opt-in system-proxy and transparent degrees), backup, restore, clean, the observability endpoints, the plugin protocol with its clients, and `pkg/acdctx` are implemented and pass `go test ./...`. No release has been tagged. Nothing has been validated against the live provider APIs or the real client binaries. See [Status](#status).

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
- Named invariants (I1 to I9) are checked by SQL checks, property tests and, for the window size (I8), an emergency truncation.
- Cache cost is modeled explicitly, and a cache gate can defer a nudge.

## How it works

1. The agent's request goes to the proxy, which records each message in the session with a stable reference (for example `m00451`).
2. The trigger decides whether compaction is due. The cache gate can postpone the nudge when a rewrite would break the provider's prompt cache at a high cost.
3. When due, the proxy adds a nudge that lists the compressible ranges. The model answers with a `compress` call: ranges plus a summary.
4. The proxy validates the call and writes the block and the hidden ranges in one transaction.
5. The next window shows the summary in place of the folded messages. The originals remain in SQLite.
6. The proxy adds four internal tools to every request, in this order: `compress`, `decompress`, `search_context` and `retrieve`. When the model calls `decompress`, `search_context` or `retrieve`, the proxy answers from the database. Internal calls run inside the proxy (up to 4 rounds per request by default) and do not reach the client.
7. Any failure in the compaction path is fail-open: the original request is forwarded unchanged.

## Supported protocols

The proxy recognizes four API dialects on `POST`:

| Dialect | Endpoint recognized by the proxy | Notes |
|---|---|---|
| Anthropic Messages | `/v1/messages` | Streaming is relayed as it arrives. |
| OpenAI Chat Completions | `/v1/chat/completions` | Each upstream round is read in full before it is sent. |
| OpenAI Responses | `/v1/responses` | HTTP requests are stateless: `previous_response_id` or `conversation` requests are forwarded unchanged. WebSocket upgrades are compacted per `response.create` frame. Input items of other types, such as `compaction` or `local_shell_call`, are kept as opaque items and sent back unchanged instead of failing the request. |
| Google Gemini | `.../models/<model>:generateContent`, and `:streamGenerateContent` with `alt=sse` | Streams are buffered per round. Without `alt=sse` the request is forwarded unchanged. |

Other paths, and other methods, are passed through to the upstream without compaction.

## Client compaction commands

Coding clients also compact their own history: `/compact` in Claude Code, Codex, OpenCode, Kilo Code and Cline, `/compress` in Gemini CLI, and automatic compaction near the limit. Each one sends a request that asks the model to summarize the transcript. acd-ctx recognises that request and forwards it unchanged: no ingest, no injected tools and no nudge, so the client summarizes its real transcript.

The request is counted as outcome `client_compaction` and in the metric `acd_ctx_client_compactions_total{protocol,signal}`, where `signal` names the rule that matched. It is logged at INFO, not as a fail-open warning. If the session already exists, a `client_compaction` event is recorded on it. Within one hour of the compaction, the next new session of the same project, or the next request that carries `x-claude-code-context-compacted`, gets a `client_compaction_parent` event that links it to the compacted session. No message is copied into the new session; the old messages stay in SQLite and can be found with `search_context` at project scope.

| Client | Signal | Default | Confidence |
|---|---|---|---|
| Claude Code | Header `x-claude-code-compaction` (`auto`, `manual` or `reactive`), or `x-claude-code-request-class: compaction` | On. The `claude` launcher sets `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1`; other setups must set it themselves | High. The header is documented. No text rule, because the prompt text is not published |
| Codex, OpenAI and Azure providers, HTTP and WebSocket | Last input item is `compaction_trigger` | On | High in the source; no wire capture |
| Codex, legacy builds | `POST /v1/responses/compact` | On | High for those builds |
| Codex, other providers (local compaction) | Last user message starts with "You are performing a CONTEXT CHECKPOINT COMPACTION." | `strong` | Medium to high. A custom `compact_prompt` is missed |
| Gemini CLI | Non-streaming `generateContent` with a system instruction, no `tools` and no `generationConfig` (the shape). The system text "distilling chat history into a structured XML <state_snapshot>" is also matched, with the shape | On | High for the text. The shape alone is structural and can match a plain SDK call with a system instruction, no tools and no `generationConfig` |
| OpenCode v1 and Kilo Code (`compaction.txt` prompt) | System text "You are a context summarization agent. You are given a conversation between a user and an agent.", and a last user message containing "Create a new anchored summary from the conversation history in the <conversation> tags above" | `strong` | High |
| Kilo Code, Kilo gateway provider | Header `x-kilocode-mode: compaction` | On | High. Also set on Kilo's plan hand-over summary, which is benign |
| OpenCode v2 | Last user message starts with "You MUST summarize the conversation above into a structured summary", or with "Update the existing checkpoint in the conversation above" | `all` only | Medium to high. A user who quotes the text is a false positive |
| Cline SDK | System text "Summarize the provided coding session into a concise continuation note with detailed next steps.", and a last user message starting with "Summarize this session for continuation. Be concise and factual." | `strong` | High |
| Cline classic | Last user message contains `<explicit_instructions type="summarize_task">` or `type="condense"` | `strong` | High. In the classic client this is an ordinary turn, so the whole turn is forwarded unchanged |
| Copilot CLI, Cursor, Windsurf | None | n/a | Not detected |

`compaction.text_rules` (global file only) selects the text rules: `strong` (default) enables the rules marked `strong` above, `all` also enables the OpenCode v2 rules, and `off` disables every text rule. The header, item, path, Kilo header and Gemini shape signals do not depend on it.

Every rule text was read from the clients' source code at the commits listed in [docs/compaction-commands.md](docs/compaction-commands.md). None has been checked on the wire or against a real client, and a client release can change any of them. OpenCode v2 and Cline are detected by text only.

Known limits:
- Codex `custom_tool_call`, `local_shell_call` and every other input item type the codec does not model are opaque barriers: kept in place, sent back unchanged, never compressed, and no compression range may cover one.
- The Gemini shape rule can match a plain SDK call that has a system instruction, no `tools` and no `generationConfig`.
- The parent link goes to any new session of the project within one hour. It is kept in memory only, so a restart drops it.
- If the session does not exist yet, the request is counted and logged, but no event is recorded.

## Installation

Requirements: Go 1.27.1 or later (the `go` directive in `go.mod`). No C compiler is needed: SQLite comes from `modernc.org/sqlite`, which is pure Go.

Build from a clone (verified, also with `CGO_ENABLED=0`):

```sh
git clone https://github.com/fsvxavier/accordion-context.git
cd accordion-context
go build -o acd-ctx ./cmd/acd-ctx
./acd-ctx help
```

Install from the module path with `go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@<commit>`. No version tag exists yet, so `@latest` does not resolve, and `@vX.Y.Z` works only after the first tag is pushed. The `version` value is `dev` unless the binary is built with `-ldflags "-X main.version=<tag>"`. Release archives are built by GoReleaser only when a `v*` tag is pushed; see [docs/release.md](docs/release.md).

## Docker

The image is a static, non-root build (distroless, uid 65532) with the data in the `acd-ctx-data` volume. Compose publishes the port on `127.0.0.1` only:

```sh
ACD_CTX_VERSION=$(acd-ctx version | awk '{print $2}') docker compose up -d --build
docker exec acd-ctx /acd-ctx healthcheck --port 8787   # exit 0 when healthy
```

Build the image with the same version as the host binary: the host launcher reuses a running proxy only when its version matches. See [docs/docker.md](docs/docker.md) for configuration, backup, upgrade and security notes.

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
| `--listen` | `127.0.0.1:8787` | Must be loopback, unless `--allow-non-loopback` and `--allowed-hosts` are both set (see [Security notes](#security-notes)). |
| `--upstream` | `https://api.anthropic.com` | Upstream used for requests without a `/ctx/<url>/` prefix. |
| `--allow` | origin of `--upstream` | Comma-separated origins reachable through `/ctx/<url>/`. Env `ACD_CTX_ALLOW`. |
| `--allowed-hosts` | loopback names | Host header values the guard accepts; replaces the defaults. Env `ACD_CTX_ALLOWED_HOSTS`. |
| `--db` | `~/.local/share/acd-ctx/acd-ctx.db` | SQLite database path. |
| `--config` | user config path | Global configuration file. |
| `--markers` | `go.mod,package.json,pyproject.toml,composer.json,Cargo.toml` | Project root markers. |
| `--plugin-token-file` | `~/.local/share/acd-ctx/plugin.token` | Receives the per-run plugin bearer token (mode `0600`). A non-default port uses `plugin-<port>.token`. If it cannot be written, the plugin protocol is disabled and the proxy keeps running. |

## Integration modes

| Mode | Command | Status | Notes |
|---|---|---|---|
| **Base URL** | `acd-ctx serve` plus the client's base-URL variable | Implemented | Verified with HTTP requests and stand-in clients. Real clients not run. |
| **Launcher** | `acd-ctx claude`, `codex`, `gemini`, `opencode`, `run -- <cmd>` | Implemented for claude, codex, gemini, opencode | `run` accepts only commands with a known profile. `opencode` launches through a temp config (`OPENCODE_CONFIG`). |
| **Attach** | `acd-ctx attach [--profiles f] [--mitm-domain h] -- <cmd>` | Implemented | Probes each degree before starting the client and uses the first that works: base-URL env, base-URL flag, temp config, console (any command), MITM. |
| **MITM** | the `mitm` degree of `attach` | Implemented | Only allowlisted hosts are terminated. Others are tunnelled untouched. The CA is per install and is not added to the system trust store. |
| **System proxy** | `attach --system-proxy --yes` | Implemented; not validated on real desktops | Points the desktop HTTPS proxy at acd-ctx for the run. Backends: GNOME, KDE, macOS, Windows. Machine-wide change: needs `--yes`. |
| **Transparent** | `attach --transparent --yes --apply` | Implemented for Linux (nftables, or iptables when nft is absent) | Redirects TCP 443 of one uid (MITM domains by default) to the local listener, over IPv4 and, when `[::1]` is free on the redirect port, IPv6 (with nft, or with iptables when `ip6tables` is present). Needs root or CAP_NET_ADMIN. Without `--apply` only the rules are printed. A helper process restores the redirect if acd-ctx exits. |
| **Plugin** | `acd-ctx serve`, under `/__acd-ctx/plugin/` | Implemented | Bearer token per run. Tools: `compress`, `decompress`, `retrieve`, `search_context`, `ctx_status`. See [Plugin protocol and middleware](#plugin-protocol-and-middleware). |
| **Middleware** | `middleware/python`, `middleware/node` | Implemented | Dependency-free clients of the plugin protocol, tested against the conformance vectors. |
| **MCP companion** | `acd-ctx mcp` (stdio) | Implemented | Tools: `decompress`, `retrieve`, `search_context`, `ctx_status`. |
| **Go library** | `pkg/acdctx` | Implemented | `Open` and `Client` (Turn, Compress, Decompress, Recall, Close), `NewTransport` (an `http.RoundTripper`) and `Handler`. May change before 1.0. |
| **Doctor** | `acd-ctx doctor [--json] [client]` | Implemented | Checks the install and the integration. Exit code 0 for PASS, 3 for WARN, 1 for FAIL. |

`attach --profiles file.yaml` adds profiles that are matched before the built-in ones. A profile sets `match`, `upstream`, `base_url_env` or `base_url_flag` (with `flag_position`), a `temp_config` (`file`, `format`, `template`, `merge_from`, `key`; `merge_from` reads an existing file of format `json`, `yaml`, `toml`, `env` or `ini` and sets one dotted `key`; only the temporary copy is written, never the source, and json, yaml and toml are re-marshalled, so comments in the merged copy are lost), `env`, `detect` (`binaries`, `config_paths`, `version_regex`), `integration` (`prefer`, `base_url_env`, `https_proxy`, `ca_env`, `domains`), `model_info` and `quirks`.

## Integrations with other clients and gateways

Client status comes from [docs/integrations.md](docs/integrations.md), which lists the sources. Nothing here has been run against a real client or gateway.

| Client | How to integrate | Status |
|---|---|---|
| Claude Code CLI | Built-in launcher `acd-ctx claude`, which sets `ANTHROPIC_BASE_URL` and `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1`. | Implemented; not run against the real CLI. |
| Claude Code IDE chat (VS Code) | Manual: set `ANTHROPIC_BASE_URL` in `claudeCode.environmentVariables`, or in the `env` block of `~/.claude/settings.json`. JetBrains path not verified. | Documented; unverified. |
| OpenCode | Launcher `acd-ctx opencode`, which writes a temp config and points `OPENCODE_CONFIG` at it. `opencode-openai` is reached through a link or wrapper with that name. | Implemented; unverified with OpenCode. |
| Kilo Code (VS Code extension) | Manual: Settings, Providers, Custom provider. Paste `http://127.0.0.1:8787/p/<id>/ctx/<upstream>` as the Base URL. | Documented; unverified. Kilo CLI has no profile. |
| GitHub Copilot Chat (VS Code) | Manual BYOK custom endpoint in `chatLanguageModels.json`. Chat only; the model must support tool calling. An organization policy can disable BYOK. | Documented; unverified. |
| GitHub Copilot CLI | No profile. The BYOK `providers.json` format is not confirmed. | Unverified. |
| Cline (VS Code extension) | Manual: OpenAI Compatible or Anthropic provider, with the custom base URL field. | Documented; unverified. Cline CLI unverified. |
| Cursor, Windsurf | Not supported: no confirmed mechanism for a local base URL. | Not supported. |

### Gateways

freellmapi, Bifrost and OmniRoute can run behind acd-ctx. Use a client profile with the gateway as upstream through `/ctx/`, and add the gateway origin to `ACD_CTX_ALLOW`, for example `ACD_CTX_ALLOW=http://127.0.0.1:20128`. Default ports: freellmapi 3001, Bifrost 8080, OmniRoute 20128.

- **Bifrost** drops `anthropic-beta` unless it is on the header allowlist. Without it, beta features are dropped or rejected and Claude Code can fail. Add it in Bifrost's client settings.
- **OmniRoute** semantic cache and compression rewrite requests. That conflicts with acd-ctx's byte-stable prompt prefix, so turn them off for traffic that goes through acd-ctx.
- **freellmapi** failover changes the model between turns. That conflicts with window sizing (`window.context_limit`) and with the provider's prompt cache. Use a fixed model, not `auto` or `fusion`.

None of these has been validated against a real client or gateway. Items marked unverified in [docs/integrations.md](docs/integrations.md) remain unverified.

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
| `window.context_limit`, `window.models` | yes | `0` (derived from the model), empty map |
| `compaction.text_rules` | no | `strong` (also `all`, `off`) |

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

`<project>/.acd-ctx/config.yaml` lives inside the repository you are working on, so it may come from a clone you did not write. It can change trigger, compression, CCR, recall, storage and window behavior, but not `upstream.*`, `credentials.*` or `listen.*`. Review it like any other project file.

## Per-project separation

- **Detection.** Walking up from the working directory, the nearest directory that holds `.acd-ctx/`, `.git` or a root marker is the root; within one directory the order is `.acd-ctx/`, then `.git`, then a marker. If none is found, the working directory is the root.
- **Project ID.** A project with `.acd-ctx/project-id` uses that value. Otherwise the ID is the first 16 hex characters of the SHA-256 of the root path.
- **Requests.** The proxy takes the project from the `X-Acd-Ctx-Project-Dir` header (an absolute path of an existing directory), then from the `/p/<id>/` prefix. The conversation comes from `X-Acd-Ctx-Conversation`. Requests with no project signal use the `default` project and log a warning.
- **Storage.** `global` keeps every project in one database. `local` keeps one project in `.acd-ctx/acd-ctx.db`.

Project commands:

```sh
acd-ctx project init [--local-storage]   # create .acd-ctx/ at the project root
acd-ctx project show                     # detected project, root, storage and database
acd-ctx project list                     # projects in the global database
acd-ctx project move <old> <new>         # re-point a moved project's root path
acd-ctx project migrate --to local|global # copy the project's sessions between databases
acd-ctx project purge <id> [--yes]       # delete all data of a project
```

## Environment variables

| Variable | Used by | Meaning |
|---|---|---|
| `ACD_CTX_<KEY>` | config loader | Overrides one configuration key, for example `ACD_CTX_CCR_CRUSH=false`. |
| `ACD_CTX_DB` | CLI commands, `attach`, `doctor` | Global database path. `serve` uses `--db` instead. |
| `ACD_CTX_ALLOW`, `ACD_CTX_ALLOWED_HOSTS`, `ACD_CTX_ALLOW_NON_LOOPBACK` | `serve` | Same as `--allow`, `--allowed-hosts` and `--allow-non-loopback` (`1` or `true`). |
| `ACD_CTX_MAX_RESTORE_BYTES` | `restore` | Size cap for a backup, when `--max-size` is not given. Default 2GiB. |
| `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `HOME` | paths | Base directories. `serve` ignores `XDG_DATA_HOME`. |
| `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GOOGLE_GEMINI_BASE_URL` | your client | Point the client at the proxy in base-URL mode. Set by the launchers. |
| `CLAUDE_CODE_GATEWAY_HINT_HEADERS` | `acd-ctx claude` | Set to `1` for Claude Code, so that it sends the `x-claude-code-*` hint headers that mark its compaction calls. |
| `HTTPS_PROXY`, `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` | `attach` (MITM degree) | Set for that client process only, so it trusts the local CA. |
| `ACD_CTX_URL`, `ACD_CTX_TOKEN`, `ACD_CTX_TOKEN_FILE`, `ACD_CTX_CONVERSATION` | `middleware/` clients | Locate and authenticate against the plugin protocol. |

## Endpoints

Control endpoints (`/__acd-ctx/` refuses any request with an `Origin` header; `metrics` and `sessions/...` accept GET only, `health` and `status` do not check the method):

| Path | Access | Response |
|---|---|---|
| `/__acd-ctx/health` | any allowed Host | `{"ok":true}` |
| `/__acd-ctx/status` | any allowed Host | JSON: `status`, `version`, `uptime_seconds`, `upstream_host`, `context_limit`, `max_loop`, `sessions_loaded`, `projects_seen`, `default_project_requests`, `protocols`. |
| `/__acd-ctx/metrics` | loopback peers only | Prometheus text. Families: `acd_ctx_requests_total`, `acd_ctx_request_duration_seconds`, `acd_ctx_upstream_duration_seconds`, `acd_ctx_context_tokens`, `acd_ctx_compress_total`, `acd_ctx_compress_ratio`, `acd_ctx_cache_read_ratio`, `acd_ctx_fold_cache_cost_tokens`, `acd_ctx_recall_hint_total`, `acd_ctx_token_calib_factor`, `acd_ctx_tool_calls_total{tool,outcome}` (`tool` is one of the four internal tool names or `unknown`; `outcome` is `accepted` or `rejected`), `acd_ctx_client_compactions_total{protocol,signal}` (client compactions forwarded unchanged). Labels are a closed set: no session, project or message IDs. |
| `/__acd-ctx/sessions/{id}/status` | loopback peers only | Counts, trigger state, window size, blocks, open intents and recent decisions (`?decisions=N`). No message or block text. |
| `/__acd-ctx/sessions/{id}/trace.jsonl` | loopback peers only | Session trace in JSON lines. `?anonymize=1` replaces texts with their length and SHA-256. Replay it with `acd-ctx replay`. |

API requests use the protocol paths in [Supported protocols](#supported-protocols), optionally behind `/p/<id>/` and `/ctx/<url>/`.

## Plugin protocol and middleware

The plugin protocol (spec §19.4) lets a program that builds its own prompts ask the proxy for the visible window of a conversation and run the context tools. `acd-ctx serve` mounts it under `/__acd-ctx/plugin/`. Every request needs `Authorization: Bearer <token>`. `serve` creates a new random token on each run and writes it to the file named by `--plugin-token-file` (mode `0600`). The Host and Origin guards of the proxy apply.

| Path (under `/__acd-ctx/plugin/`) | Method | Purpose |
|---|---|---|
| `manifest` | GET | Protocol name and version (`acd-ctx-plugin`, 1), tools and doctrine. |
| `runtime-info` | POST | Declare the conversation, model and context window. |
| `turn` | POST | Send messages and a token count; get the decision, hints and the visible window. |
| `tool` | POST | Run `compress`, `decompress` (modes `context`, `peek`, `file`), `retrieve`, `search_context` or `ctx_status`. |
| `status` | GET | Same data as `ctx_status`, for `?conversationId=`. |
| `compact` | POST | Offers the compressible spans and arms a nudge for the next turn. Unknown conversation: 404. |

`search_context` is a full-text (FTS5) search over the messages and block summaries of the session; all words must match. `decompress` mode `file` writes the block's original text to the project's exports directory and returns the path.

The proxy also injects these four tools into the model's request, in every dialect, on the HTTP paths and on the Responses WebSocket mode: `compress`, `decompress`, `search_context` and `retrieve`. The proxy runs all of them and never sends their calls to the client. Results come back in a `<history>` envelope that tells the model they are past data, not instructions. `search_context` returns at most 20 hits (10 by default), each snippet at most 300 runes. Its `project` scope also searches the other conversations of the same project, but only as snippets; `retrieve` reads only the current conversation. A `retrieve` result is capped at about 32K tokens.

The clients live in `middleware/python` (package `acd_ctx`, standard library only) and `middleware/node` (package `acd-ctx`, ESM, Node.js 18.13 or later, no dependencies). Both read `ACD_CTX_URL` (default `http://127.0.0.1:8787`), `ACD_CTX_TOKEN` or the file in `ACD_CTX_TOKEN_FILE` (default `~/.local/share/acd-ctx/plugin.token`), and `ACD_CTX_CONVERSATION`. On any client error, continue without compression.

`conformance/vectors/*.json` are language-neutral vectors, replayed by `go test ./conformance/...` and by the Python and Node suites. `docs/compatibility.md` is generated by `go run ./scripts/gen-compat-matrix`, and CI fails when it is stale.

## Storage

The database is at schema version 4, tracked by `PRAGMA user_version`. Version 4 adds the message kind `opaque`, which holds a Responses input item that the codec does not model (see [Supported protocols](#supported-protocols)). The migration runs on open, inside one transaction, and a failed upgrade leaves the old version in place. Older builds refuse a version 4 database and report that it is newer than they support.

SQLite normally uses WAL mode. When the file system cannot open the WAL files, the store logs a warning and falls back to the rollback journal (`journal_mode = DELETE`). This happens on WSL drives under `/mnt/<drive>` (DrvFs) and on some network mounts. A stale process that still holds the database in WAL mode blocks opening it, so stop that process first. Prefer a native Linux path for the database, such as one under `~/.local/share`.

## Backup and restore

`acd-ctx backup` writes a verified copy of the global database, or of one project. The copy is made with `VACUUM INTO` into a private temporary directory, checked with the same inspection that `restore` applies, and renamed into place, so the output file is either the old one or the complete new one. The default file name is `acd-ctx-backup-<UTC time>.db` in the working directory; files are written with mode `0600`.

```sh
acd-ctx backup                              # whole global database
acd-ctx backup --project <id|path|.> -o p.db  # one project; a local project copies its own file
```

`acd-ctx restore <file.db>` (or `--stdin`) imports a backup in one transaction. It never copies a file over the live database.

- `--project <id>` restores one project of the backup. `--to local|global` chooses the destination; `local` needs exactly one project and writes the current project's `.acd-ctx/acd-ctx.db`.
- `--mode merge` (default) refuses any ID that already exists in the destination, and writes nothing.
- `--mode replace` purges the target project first. It needs `--yes`.
- `--dry-run` plans the whole import on a scratch copy and rolls it back. It writes nothing and creates no destination file.
- `--max-size` (or `ACD_CTX_MAX_RESTORE_BYTES`) caps the backup size: bytes, or a number with `KiB`, `MiB` or `GiB`. Default 2GiB.
- `--docker <container>` (also on `backup` and `clean`) runs the operation inside the container; the file is read or written on the host.

Hostile or damaged backups are rejected before anything is written. A file is refused when it is not SQLite, when its schema is newer than the binary's, when it holds any table, view, trigger or virtual table that acd-ctx does not create (only the FTS5 `search_idx` is allowed), when `PRAGMA integrity_check` fails, when a stored content does not hash to its key, or when a session breaks an invariant (I1 to I3, I6, I7). The copy is opened with `trusted_schema` off and `query_only` on.

## Cleaning

`acd-ctx clean` deletes conversation data. **It is irreversible.** Without a backup, the deleted sessions, messages, blocks, events and contents are gone. Always run it with `--dry-run` first, and use `--backup` for anything you may need later.

```sh
acd-ctx clean --project . --dry-run                  # what would be deleted, nothing else
acd-ctx clean --project . --backup before.db         # verified backup first, then delete
acd-ctx clean --older-than 30d --vacuum --exports
acd-ctx clean --all --docker acd-ctx --dry-run
```

- Scope: exactly one of `--project <id|path|.>`, `--session <id>`, `--older-than <dur>` (`30d`, `12h`, any Go duration; `--project` may narrow it) or `--all`.
- `--backup <file.db>` writes a verified backup of each database before deleting. If a backup fails, nothing is deleted.
- Confirmation: without `--yes` you must type the word shown: the project ID, the session ID, or `delete` for `--all` and `--older-than`. A non-terminal stdin without `--yes` is refused.
- `--exports` also removes the exported block files of the removed sessions. `--vacuum` compacts the files afterwards.
- `--include-local` also cleans the current directory's local database for `--all`, `--older-than` or `--session` without `--project`.
- Configuration, the MITM CA and the plugin token are never deleted (`--keep-config` is always on).
- A purge bumps the store epoch in the same transaction. A running `serve` or plugin drops its in-memory state for the affected sessions on its next request. `restore --mode replace` does the same.

## Security notes

- **Loopback by default.** `serve` refuses a non-loopback `--listen` unless both `--allow-non-loopback` (or `ACD_CTX_ALLOW_NON_LOOPBACK=1`) and a non-empty `--allowed-hosts` are given. It then logs a `SECURITY` warning. The Docker image uses this path; see [docs/docker.md](docs/docker.md).
- **No authentication on the base-URL listener.** Any local process can send requests to `/ctx/<allowlisted host>/...` on the loopback port (only the MITM `CONNECT` path and the plugin require a token). The proxy forwards the caller's own credentials and never adds its own, so it works as a relay to allowlisted hosts. Run it on a single-user machine.
- **Host and browser guards.** Only allowed `Host` names are served (by default `localhost`, `127.0.0.1`, `::1` and the listen host); others get HTTP 403, which is meant to stop DNS rebinding (tested with forged `Host` headers, not with a real browser). CORS preflights get 403, and `/__acd-ctx/` refuses any request with an `Origin` header. Neither guard is authentication.
- **Upstream allowlist.** A `/ctx/<url>/` target is used only if its origin is in `--allow`. Redirects are returned to the client, not followed.
- **MITM CA.** One CA per install, its key in a `0600` file. Only the allowlisted domains are intercepted, and `attach` passes the CA only to the client process it starts, through environment variables.
- **System proxy and transparent mode.** Both change the machine's network path and need explicit consent (`--yes`). `--system-proxy` changes the desktop proxy settings; `--transparent` needs root or CAP_NET_ADMIN, installs nftables rules (or iptables rules when nft is absent) for one uid, and refuses to run under sudo with a `HOME` that is not the invoking user's, unless the uid and data directory are explicit. The nftables table is created fresh: acd-ctx refuses to adopt an existing table. Both record state files that are checked before they are trusted. Before the first change, a watchdog helper starts; it restores the redirect when acd-ctx dies. If the helper itself is killed with SIGKILL, the redirect stays until `attach --restore` runs, and `doctor` reports it. `attach --restore` run without privilege keeps the transparent state file, because the firewall cannot be read to restore it.
- **Credentials and limits.** Credential headers are forwarded to the upstream and the proxy does not log them (tests cover only that error messages do not leak the query string). A compaction error forwards the request unchanged. Request bodies and WebSocket frames are capped at 32 MiB.

## Development

```sh
go test -race -count=1 ./...                 # mandatory before every commit
go test ./...                                  # full suite
go test ./internal/kernel -run TestName        # one test
go test -fuzz=FuzzParseToolArguments ./internal/wire/openai
go test ./conformance/...                      # plugin protocol vectors
(cd middleware/python && python3 -m unittest discover -s tests)
(cd middleware/node && node --test)
go run ./scripts/gen-compat-matrix             # prints docs/compatibility.md
```

Run `go test -race -count=1 ./...` over the whole module before every commit. It is mandatory: CI runs it, and it catches races that a plain `go test` misses.

`CLAUDE.md` is in English. The design spec `compactacao-contexto-go-sqlite.md` (Portuguese) is the source of truth; §N in code comments refers to it.

CI (`.github/workflows/ci.yml`) runs `gofmt`, `go vet`, `go test -race`, a cross vet for darwin and windows with `CGO_ENABLED=0`, the conformance vectors, the Python and Node client tests, a check that `docs/compatibility.md` is current, and a Docker image build with a health smoke test. The release workflow (`.github/workflows/release.yml`) runs only on a pushed `v*` tag or by hand; see [docs/release.md](docs/release.md).

| Package | Role |
|---|---|
| `internal/kernel` | Pure logic: trigger, tiers, render, recall, CCR and crush, truncation, cache cost. No I/O. |
| `internal/store` | SQLite access (WAL, FTS5), schema, invariants, purge, backup and restore. |
| `internal/session` | Turn orchestration, tools and the compression transaction. The only writer. |
| `internal/proxy` | HTTP server, routing, protocol adapters, WebSocket mode, server-side tool loop, observability endpoints. |
| `internal/wire/...` | Codecs for `anthropic`, `openai`, `responses` and `google`. |
| `internal/config` | Layered configuration and effective-source reporting. |
| `internal/launcher` | Client profiles, process launch with environment variables, temp configs. |
| `internal/attach` | Integration ladder for `attach`: probes, console, MITM and system degrees. |
| `internal/sysproxy` | System-proxy backends and the nftables and iptables transparent backends, with state files, the watchdog helper and restore. |
| `internal/mitm` | Local CA, leaf certificates and scoped TLS interception. |
| `internal/backup` | Verified backup and restore, and the `--docker` runners. |
| `internal/obs` | Prometheus metrics, status reports and trace export. |
| `internal/plugin` | stdio MCP server, HTTP plugin handler, and the store-epoch cache check. |
| `internal/cli`, `internal/doctor` | Command implementations behind `cmd/acd-ctx`; installation and integration checks. |
| `internal/project` | Project root and ID detection. |
| `pkg/acdctx` | Public Go library: client API and HTTP transport and handler. |
| `middleware/python`, `middleware/node` | Plugin protocol clients (no dependencies). |
| `conformance` | Language-neutral plugin vectors and the Go runner. |
| `scripts/gen-compat-matrix` | Generates `docs/compatibility.md`. |
| `.goreleaser.yaml`, `.github/workflows/release.yml` | Release builds and archives, run only for a `v*` tag. |

## Status

Validated by automated tests, with no network access beyond loopback. `go test ./...` passes with `CGO_ENABLED=0`:

- Unit and table tests for the kernel, store, config, project, launcher, attach, sysproxy (with fake runners), MITM, backup, restore, clean, observability and doctor.
- Property tests (`pgregory.net/rapid`) that check the invariants after random compression steps.
- Fuzz targets for the tool-argument parsers of each wire codec, the `compress` handler and the plugin JSON-RPC frames.
- Proxy tests against fake upstreams (`httptest` on loopback) for the four dialects, including streaming and the Responses WebSocket mode. Launchers, `attach` and MITM run with stand-in client binaries.
- End-to-end tests (`e2e/`) run the real binary against a fake upstream for Anthropic Messages and OpenAI Chat Completions: 80 turns past twice a 128K window, folds and a decompress round trip, invariants checked after the run, and a proxy restart mid-run.
- Plugin conformance vectors, replayed by Go, Python and Node.
- Client compaction detection, with synthetic fixtures built from the text of the clients' source code (`compaction_rules_test.go`, `client_compaction_test.go`, `gemini_shape_test.go`).
- Schema v4 migration (`migrate_v4_test.go`), the WAL fallback (`wal_fallback_test.go`) and opaque Responses items (`opaque_test.go`).
- The proxy's internal tools `search_context` and `retrieve`, on the HTTP and WebSocket paths, with the hit, snippet and token caps.
- Dependencies updated with `go get -u`; the suite passes on them.
- The Docker image was built and run once locally in an earlier step. It was not re-verified for this revision.

Not validated:

- The real provider APIs (Anthropic, OpenAI, Google): wire details, streaming edge cases, `usage` values, cache behavior and prices.
- The live OpenAI Responses WebSocket endpoint.
- The real agent clients (Claude Code, Codex, Gemini CLI, OpenCode) running through the proxy, MITM, system proxy or transparent mode.
- The client compaction rules on the real clients and on the wire. Every rule text was read from the clients' source code (see [docs/compaction-commands.md](docs/compaction-commands.md)); none has been captured from a run.
- `attach --system-proxy` and `--transparent`, the watchdog, the nftables and iptables redirects, and the macOS and Windows system-proxy backends, on real systems. The transparent and watchdog paths have only been run against fake runners.
- A GitHub Actions run of the CI workflow (including its Docker job) and of the release workflow: GoReleaser builds, draft releases, and the image push to ghcr.io. `goreleaser check` has not been run.
- Summary quality in long real sessions.

Not implemented:

- A watchdog helper killed with SIGKILL: the redirect stays until `attach --restore` is run by hand. `doctor` reports it.
- IPv6 redirect when `[::1]` is not free on the redirect port: IPv6 then bypasses acd-ctx, and the attach output says so.
- Client compaction detection for Copilot CLI, Cursor and Windsurf: no documented signal, so no rule exists.

## Limitations and risks

- **Lossy compression.** A summary replaces the messages it covers in the window. The model may summarize incorrectly or omit details. Originals are recoverable with `decompress`, but only if the model or you ask for them.
- **Cache cost.** Rewriting an earlier part of the prompt invalidates the provider's prompt cache. The cache gate reduces this cost but does not remove it. Cache prices are provider-specific and must be set by you.
- **Token counts are estimates.** They are calibrated against provider `usage` values but are not exact.
- **Pre-1.0.** No release tag exists yet, and the spec (v0.1) and the library API may change.

## License

License: TBD
