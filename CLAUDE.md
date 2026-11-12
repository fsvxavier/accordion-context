# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Accordion Context is a Go proxy between a coding agent and a model API. The session's model writes summaries of older message ranges (`compress`), and the system decides when to ask for them and how to rebuild the visible window. Compaction is incremental, reversible (originals are hidden, never deleted) and hierarchical (tiers T1→T2→T3). SQLite is the source of truth.

The design spec is [compactacao-contexto-go-sqlite.md](compactacao-contexto-go-sqlite.md) (Portuguese, v0.1). It is the source of truth: read the relevant section (§N) before changing behavior. Module: `github.com/fsvxavier/accordion-context`, `go 1.27.1`.

Prefix shell commands with rtk if installed (see ~/.claude/RTK.md).

## Commands

```sh
go build -o /dev/null ./cmd/acd-ctx           # compile check; `go build ./cmd/acd-ctx` writes ./acd-ctx, which .gitignore does not cover
go vet ./...
gofmt -l .                                  # must print nothing (CI gate)
go test ./...                               # includes e2e (~30s)
go test $(go list ./... | grep -v /e2e$)    # everything except e2e
go test -race -count=1 ./...
go test ./internal/kernel -run TestName     # single test
CGO_ENABLED=0 GOOS=darwin go vet ./...      # cross vet; also GOOS=windows
```

Fuzz targets (one package and one target per `-fuzz` run):

```sh
grep -rn "func Fuzz" --include=*_test.go .
go test -run='^$' -fuzz='^FuzzDecodeFrame$' -fuzztime=30s ./internal/plugin
```

End-to-end (`e2e/`, runs the real binary against fake upstreams, ~30s):

```sh
go test ./e2e
go test ./e2e -run TestE2EAnthropicMainPath -args -update   # rewrite golden trace
```

The golden update writes `testdata/traces/e2e_basic.golden.json`. Review its diff before committing. Only the Anthropic scenario is golden (`golden: true`). That file is an event list, not a `replay` input: `replay` takes a `trace.jsonl` from `/__acd-ctx/sessions/{id}/trace.jsonl`.

Conformance vectors (Go runner) and the client middleware:

```sh
go test -count=1 -v ./conformance/...
cd middleware/python && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -v
cd middleware/node && node --test
```

Compatibility matrix: `go run ./scripts/gen-compat-matrix > docs/compatibility.md`. CI diffs the output against the committed file, so regenerate it whenever a launcher profile or codec changes.

Docker: `docker build --build-arg VERSION=ci-local -t acd-ctx:local .` (CI also runs a health smoke test). See `docs/docker.md`.

## Architecture

Package map:

- `cmd/acd-ctx`: entry point. `serve` and `version` are handled here; `internal/cli` handles the rest.
- `internal/cli`: dispatches `claude`, `codex`, `gemini`, `opencode` (launchers), `project`, `config`, `attach`, `doctor`, `run`, `replay`, `backup`, `restore`, `clean`, `mcp`, `healthcheck` and `help`.
- `internal/proxy`: HTTP proxy. `protocol.go` defines the `protocol` interface (`Detect`, `Meta`, `Decode`, `InjectTools`, `AppendNudge`, `ParseTool`, `Live`, `Read`, `Continue`, `ClientCompaction`) and the four internal tool names. `adapter_{anthropic,openai,responses,google}.go` implement it. `proxy.go` holds the single server-side loop, written against the interface. `recall_tools.go` runs `search_context` and `retrieve`. `route.go` handles the `/p/<id>/` and `/ctx/<upstream>/` prefixes. `ws.go` and `adapter_responses_ws.go` are the Responses WebSocket mode. `obs.go` serves `/__acd-ctx/metrics` and the session endpoints. `epoch.go` drops in-memory sessions when the epoch of any open store changes. `stores.go` picks the store of a project: with `Config.OpenStore` set (as `serve` does), a `storage: local` project named by `X-Acd-Ctx-Project-Dir` gets its own `<root>/.acd-ctx/acd-ctx.db`; everything else uses `Config.Store`. Code that has only a session id uses `storeOf(sid)`, never `cfg.Store` directly. `listen.go` holds the non-loopback guard. `compaction_rules.go` holds the client compaction text rules (`strong` and `all`), and `client_compaction.go` holds the detection, the unchanged passthrough, the `client_compaction` event and the `client_compaction_parent` link. Each adapter's `ClientCompaction` reads its own signal (headers, the `compaction_trigger` item, the Gemini shape; OpenAI chat has none).
- `internal/wire/{anthropic,openai,responses,google}`: the four codecs. Each decodes a request into `kernel.Message`s, encodes a window back, and injects the four tool definitions (`InjectTools`). Fuzzed. `internal/wire/toolargs` is the shared parser for `search_context` and `retrieve` arguments. The Responses codec keeps input item types it does not model as opaque `KindOpaque` messages, sent back unchanged.
- `internal/session`: one conversation's turn orchestration (plan, compress, decompress, recall, search and retrieve in `search.go` with the hit, snippet and token caps, export, emergency truncation). It is the only writer, through `store`.
- `internal/kernel`: pure decisions. `trigger.go` (`DecideNudge`, §8), `tiers.go`, `render.go`, `recall.go`, `crush.go`, `cachecost.go`, `tokens.go`, `truncate.go` (I8), `framing.go` (`compress.framing_check`), `types.go`.
- `internal/store`: SQLite (`modernc.org/sqlite`, WAL, FTS5). `schema.sql`, `invariants.go`, `window.go`, `projects.go`, `purge.go` (the only hard delete), `search.go` (`search_context`), `backup.go` (`VACUUM INTO`, `InspectFile`, restore import), `contents_verify.go` (content hashes), `migrate.go` (schema v4: the `opaque` message kind; store epoch). `Open` falls back to `journal_mode = DELETE` when WAL cannot open (WSL DrvFs, network mounts).
- `internal/backup`: verified backup and restore, the `--docker` runners (`docker.go`). Hostile-file checks come from `store.InspectFile`.
- `internal/obs`: Prometheus metrics (`acd_ctx_*`), status reports, trace export (`anonymize`).
- `internal/config`: layered configuration (`load.go`), key table with a `local` flag (`config.go`), decimal handling (`decimal.go`).
- `internal/project` (root and ID detection), `internal/launcher` (per-client profiles, temp configs, `merge_from` for json, yaml, toml, env and ini), `internal/attach` (degree ladder, system degrees), `internal/sysproxy` (system-proxy backends, nftables and iptables transparent backends, IPv6 redirect, `watchdog*.go` helper, state files, `--restore`), `internal/mitm` (CA, leaf certs, server), `internal/plugin` (HTTP JSON-RPC plugin, MCP server, `epoch.go`), `internal/doctor`.
- `pkg/acdctx`: public Go client library.
- `conformance/`: plugin vectors, replayed by Go, Python and Node.
- `middleware/python/acd_ctx`, `middleware/node/index.mjs`: plugin clients (stdlib only).
- `e2e/`: black-box tests of the binary. `testdata/traces/`: golden event sequences.
- `docs/`: `compatibility.md` (generated), `docker.md` (container install, backup, security), `release.md` (tags, release and image workflows), `integrations.md` (research and status of other clients and gateways; the README table is derived from it).
- Release: `.goreleaser.yaml` and `.github/workflows/release.yml`. The workflow runs on a `v*` tag or by hand. Its GoReleaser and image jobs run only for a tag.

Request flow: client → `proxy` (route, detect the dialect) → `wire` decode into `kernel.Message`s → `session`/`store` record the ledger → `kernel` decides the window and nudge → `proxy` injects the four internal tools (`compress`, `decompress`, `search_context`, `retrieve`) and sends upstream → `Read` takes the complete answer (only Anthropic streams live, through `Live`) → internal calls are applied by `session` → `Continue` loops the round → `wire` encodes the window back to the client.

Rules:

- `internal/kernel` is pure: no I/O, and no imports from `store`, `wire` or `session`. Check with `go list -deps ./internal/kernel | grep accordion`; it must print only the kernel package.
- Only `store` writes SQL. `DELETE FROM` appears only in `store/purge.go` (`PurgeProject` and the `clean` purge core), in one transaction per purge. `TestNoDeleteOutsidePurge` (`internal/store/purge_test.go`) greps every non-test `.go` and `.sql` file and fails on any other `DELETE FROM`. `PRAGMA journal_mode = DELETE` is not a delete statement and is allowed.
- A purge increments `store_epoch` in the same transaction. `proxy` and `plugin` compare the epoch and drop their in-memory sessions when it changes. `restore --mode replace` purges through the same core.
- Money, ratios and token math use `github.com/cockroachdb/apd/v3` (34-digit, half-even), not `float64`.
- A compression is confirmed only by a `compression_events` row, never by model text. Strip fake "📦 Compressed…" markers from model output.
- The first user message is pinned, the recent zone stays visible, and tool_call/tool_result pairs are atomic.
- The compression trigger has one spec. Change the §8.3 decision table and `kernel/trigger_test.go` together.
- Restores never copy a file over the live database. A backup is copied to a private file, checked by `store.InspectFile`, and imported in one transaction.
- A client's own compaction call (header, item, path or text rule) is forwarded unchanged by `passthrough`: no ingest, no tools, no nudge. It counts as outcome `client_compaction` and in `acd_ctx_client_compactions_total{protocol,signal}`, whose signal labels are a closed set, never built from request content.

Invariants (§17.1), where they are enforced:

- I1, I2, I3, I6, I7: SQL checks in `store/invariants.go`. `checkInvariants` runs them inside every compress transaction and inside `InspectFile` for each session of a backup. `Store.CheckInvariants` runs them on demand. Each query must return zero rows.
- I5 (`sum_tokens < src_tokens`): schema CHECK, plus validation in `kernel/tiers.go`.
- I4 (first user message visible) and I9 (system prompt byte-identical): checked by the property tests in `session/property_test.go`.
- I8 (window never exceeds the limit): `session.truncated` renders the window and applies `kernel.EmergencyTruncateWith` when `window.context_limit` or the model's window is known. It records a `truncate` event.

Configuration:

- Precedence, lowest to highest: defaults, global file (`$XDG_CONFIG_HOME/acd-ctx/config.yaml`), `projects` overrides in the global file, local `<root>/.acd-ctx/config.yaml`, `ACD_CTX_*` env vars, flags.
- The local file may set only keys marked `local` in `config.go` (trigger, compress, ccr, recall, storage, `window.*`). `projects` is global-only. `upstream.*`, `credentials.*`, `listen.*` and `tools.external_summary.*` are rejected in every file.
- `acd-ctx config show --effective` prints every key with its value and source.
- The proxy reloads config per project root at most every 30 seconds.
- `compaction.text_rules` (`strong` default, `all`, `off`) is global-only and selects the client compaction text rules. The header, item, path, Kilo and Gemini-shape signals are always on.

## Gotchas

- **Run `go test -race -count=1 ./...` before every commit (mandatory).** CI runs it, and it catches races that plain `go test` and a partial `-race` run on a few packages miss (it caught a stderr buffer shared by the client and the proxy it starts). It takes about 4 minutes.

- **Concurrent git commits.** When several agents commit at once, `.git/index.lock` errors happen. Wait and retry. Never delete the lock while a git process may still be running.
- **Offline `go mod tidy`.** `go mod tidy` needs every module in the graph, in the module cache or from the network. Offline (`GOPROXY=off`) it fails, or it can leave `go.sum` incomplete. Do not commit a `go.mod`/`go.sum` change that you could not produce with network access.
- **Cross vet with CGO off.** Run `CGO_ENABLED=0 GOOS=darwin go vet ./...` (and `windows`) after touching `sysproxy`, `attach` or other platform code. CI runs it in the `cross-vet` job, but run it locally first. The SQLite driver is pure Go, so the cross run needs no C cross-toolchain.
- **Releases.** Do not push a `v*` tag or run `goreleaser release` to test a change: the release and image jobs publish. `docs/release.md` has the procedure. `go install ...@vX.Y.Z` works only for a `v*` tag; a tag without the `v` (such as `1.0.0`) is not a module version and does not run the release workflow.
- **Go version.** CI reads the Go version from the `go` line in `go.mod`, so the local toolchain must be at least 1.27.1.
- **Throwaway HOME.** Tests that touch config or data must set `HOME`, `XDG_CONFIG_HOME` and `XDG_DATA_HOME` to a temp dir. The e2e harness (`serveEnv`) strips inherited `HOME` and `ACD_CTX_*`, and launcher tests use `t.Setenv`. Do this for any new test that loads config.
- **Never run network-changing commands in tests.** `sysproxy` and `attach` tests drive fake runners (`fake_test.go`), not `gsettings`, `nft`, `iptables`, `sudo` or a real proxy. A new test must not reach them either. The same applies to hosts: point tests at `httptest` servers through `--upstream` or the `/ctx/<url>/` prefix. No real provider traffic.
- **Refs are cache-sensitive.** `kernel.RefLabel` gives each raw message a `[mNNNNN]` label that depends only on its ledger position. Keep it byte-stable: the provider's prompt cache depends on the rendered prefix not changing between turns. Do not add per-turn data to it.
- **WAL fallback.** `store.Open` falls back to the rollback journal when WAL cannot open (WSL DrvFs under `/mnt/<drive>`, network mounts). A stale process still holding the database in WAL mode blocks opening it. Prefer a native Linux path for the database.
- **Anthropic system messages.** Claude Code sends `role: "system"` messages inside `messages`. The Anthropic codec keeps each one as a single `KindOpaque` message with role user, re-sent byte for byte. Before this, every Claude Code request failed open with `unknown role "system"`. Such an item is *carried* (`kernel.Carried`): a range may cover it (not start or end on it), its tokens do not count as replaced, and `Render` keeps it after the envelope. Treating them as barriers made every compress fail in long Claude Code sessions (one every 10-20 messages).
- **Opaque Responses items.** An unmodelled input item is `KindOpaque` with role assistant, a barrier (`kernel.Barrier`): never summarized, dropped or joined to a block, and a compression range may not cover it (`barrierInside`).
- **Refusals.** A refusal is a nudge the model answered with no `compress` call (spec §8.3). A refused `compress` call is not one: `Session.HandleCompress` clears the count and the pending nudge. A window change (`applyContextLimit`) also clears them, and `trigger.max_refusals` (0 = never suspend) sets the threshold.
- **Intent noise.** `kernel.IntentText` drops harness text before an intent opens: tagged envelopes (attributes allowed, e.g. `<agent-message from=...>`), relay lines and their guidance notes, continued-session summaries, resume requests, and bare acknowledgements ("mergeado", "sim").
- **Claude Code side requests.** `x-claude-code-request-class` `auxiliary` or `side_reply` is passed through with no session (`claudeSideRequest`, outcome `side_request`); otherwise each auto-mode classifier call became the newest session and hid the conversation from `ctx_status`.
- **Context windows.** `builtinWindows` (`internal/proxy/config.go`) gives Claude 5 models 1M; a too-small window makes the proxy truncate every turn (I8) even though upstream accepts the request.
- **Client compaction is unverified on the wire.** The text rules come from the clients' source (`docs/compaction-commands.md`). The `claude` launcher sets `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1`, which the Claude Code header rule needs. Change a rule's text, its `source` note and its tests together.
- **Python tests leave `__pycache__`.** `.gitignore` already covers `__pycache__/` and `*.pyc`; set `PYTHONDONTWRITEBYTECODE=1` anyway to keep the tree clean.
- **e2e window.** The scenarios reach the 128K window through the `gpt-4o` model-name prefix (`e2eWindow`). The `window.*` keys are covered in `internal/proxy/window_test.go`, not in e2e.
- **Transparent-mode limits:** the watchdog helper (`__watchdog`, hidden, dispatched in `cmd/acd-ctx/main.go`) restores the redirect when its owner dies, but a SIGKILL of the helper leaves it in place until `attach --restore`; `doctor` reports it. IPv6 is redirected only when `[::1]` is free on the redirect port. Unprivileged `--restore` keeps the transparent state file.
- **Not implemented yet:** fail-open when the transparent watchdog helper is SIGKILLed (the redirect stays until `attach --restore`), and IPv6 interception when `[::1]` is not free on the redirect port (IPv6 then bypasses acd-ctx), and client compaction detection for Copilot CLI, Cursor and Windsurf (no documented signal).

## Implementation status

Phase 1 of the plan (§21) covers `store`, `kernel` basics and `project`. The spec's phases and acceptance criteria are in §21. The proxy (including the Responses WebSocket mode and the four injected internal tools, `search_context` and `retrieve` included), launchers (with `merge_from` for json, yaml, toml, env and ini), `attach` with the system and transparent degrees (nftables or iptables, IPv4 and IPv6), `backup`, `restore`, `clean`, `replay`, observability, `doctor`, `mcp`, the plugin protocol with its `compact` and `search_context` tools and its Python and Node clients, `pkg/acdctx`, the Docker image, and the release configuration are implemented. `go test ./...` passes with `CGO_ENABLED=0`. Client compaction detection (`compaction_rules.go`, `client_compaction.go`) is implemented with rule texts taken from client source only. None of it is validated against live provider APIs, the live OpenAI Responses WebSocket endpoint, real client binaries, real desktops (transparent and watchdog paths run only with fake runners), or a GitHub Actions or release run (see README Status).
