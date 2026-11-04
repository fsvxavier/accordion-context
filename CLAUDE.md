# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Accordion Context is a Go proxy between a coding agent and a model API. The session's model writes summaries of older message ranges (`compress`), and the system decides when to ask for them and how to rebuild the visible window. Compaction is incremental, reversible (originals are hidden, never deleted) and hierarchical (tiers T1→T2→T3). SQLite is the source of truth.

The design spec is [compactacao-contexto-go-sqlite.md](compactacao-contexto-go-sqlite.md) (Portuguese, v0.1). It is the source of truth: read the relevant section (§N) before changing behavior. Module: `github.com/fsvxavier/accordion-context`, `go 1.27.1`.

Prefix shell commands with rtk if installed (see ~/.claude/RTK.md).

## Commands

```sh
go build ./cmd/acd-ctx                      # binary: acd-ctx
go vet ./...
gofmt -l .                                  # must print nothing (CI gate)
go test ./...                               # includes e2e (~30s)
go test $(go list ./... | grep -v /e2e$)    # everything except e2e
go test -race -count=1 ./...
go test ./internal/kernel -run TestName     # single test
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

The golden update writes `testdata/traces/e2e_basic.golden.json`. Review its diff before committing. Only the Anthropic scenario is golden (`golden: true`).

Conformance vectors (Go runner) and the client middleware:

```sh
go test -count=1 -v ./conformance/...
cd middleware/python && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -v
cd middleware/node && node --test
```

Compatibility matrix: `go run ./scripts/gen-compat-matrix > docs/compatibility.md`. CI diffs the output against the committed file, so regenerate it whenever a launcher profile or codec changes.

## Architecture

Package map:

- `cmd/acd-ctx`: entry point. `internal/cli` dispatches `claude`, `codex`, `gemini`, `opencode` (launchers), `serve`, `project`, `config`, `attach`, `doctor`, `run`, `mcp`, `help`.
- `internal/proxy`: HTTP proxy. `protocol.go` defines the `protocol` interface (`Detect`, `Meta`, `Decode`, `InjectTools`, `AppendNudge`, `ParseTool`, `Live`, `Read`, `Continue`). `adapter_{anthropic,openai,responses,google}.go` implement it. `proxy.go` holds the single server-side loop, written against the interface. `route.go` handles the `/p/<id>/` and `/ctx/<upstream>/` prefixes.
- `internal/wire/{anthropic,openai,responses,google}`: the four codecs. Each decodes a request into `kernel.Message`s, encodes a window back, and parses tool arguments. Fuzzed.
- `internal/session`: one conversation's turn orchestration (plan, compress, decompress, recall). It is the only writer, through `store`.
- `internal/kernel`: pure decisions. `trigger.go` (`DecideNudge`, §8), `tiers.go`, `render.go`, `recall.go`, `crush.go`, `cachecost.go`, `tokens.go`, `types.go`.
- `internal/store`: SQLite (`modernc.org/sqlite`, WAL, FTS5). `schema.sql`, `invariants.go`, `window.go`, `projects.go`.
- `internal/config`: layered configuration (`load.go`), key table with a `local` flag (`config.go`), decimal handling (`decimal.go`).
- `internal/launcher` (per-client profiles), `internal/mitm` (CA, leaf certs, server), `internal/attach`, `internal/doctor`, `internal/plugin` (HTTP JSON-RPC plugin protocol and MCP server), `internal/project` (root detection and project scopes).
- `pkg/acdctx`: public Go client library.
- `conformance/`: plugin vectors, replayed by Go, Python and Node.
- `middleware/python/acd_ctx`, `middleware/node/index.mjs`: plugin clients (stdlib only).
- `e2e/`: black-box tests of the binary. `testdata/traces/`: golden event sequences.

Request flow: client → `proxy` (route, detect the dialect) → `wire` decode into `kernel.Message`s → `session`/`store` record the ledger → `kernel` decides the window and nudge → `proxy` injects the internal `compress`/`decompress` tools and sends upstream → `Read` takes the complete answer (only Anthropic streams live, through `Live`) → internal calls are applied by `session` → `Continue` loops the round → `wire` encodes the window back to the client.

Rules:

- `internal/kernel` is pure: no I/O, and no imports from `store`, `wire` or `session`. Check with `go list -deps ./internal/kernel | grep accordion`; it must print only the kernel package.
- Only `store` writes SQL. The only `DELETE` statements in non-test code are in `store/projects.go` (`PurgeProject`, which removes one project in a single transaction and is the only hard-delete path).
- Money, ratios and token math use `github.com/cockroachdb/apd/v3` (34-digit, half-even), not `float64`.
- A compression is confirmed only by a `compression_events` row, never by model text. Strip fake "📦 Compressed…" markers from model output.
- The first user message is pinned, the recent zone stays visible, and tool_call/tool_result pairs are atomic.
- The compression trigger has one spec. Change the §8.3 decision table and `kernel/trigger_test.go` together.

Invariants (§17.1), where they are enforced:

- I1, I2, I3, I6, I7: SQL checks in `store/invariants.go`. `checkInvariants` runs them inside every compress transaction; `Store.CheckInvariants` runs them on demand. Each query must return zero rows.
- I5 (`sum_tokens < src_tokens`): schema CHECK, plus validation in `kernel/tiers.go`.
- I4 (first user message visible) and I9 (system prompt byte-identical): checked by the property tests in `session/property_test.go`.
- I8 (window never exceeds the limit after `emergencyTruncate`): **not implemented yet**. `kernel/render.go` says so.

Configuration:

- Precedence, lowest to highest: defaults, global file (`$XDG_CONFIG_HOME/acd-ctx/config.yaml`), `projects` overrides in the global file, local `<root>/.acd-ctx/config.yaml`, `ACD_CTX_*` env vars, flags.
- The local file may set only keys marked `local` in `config.go` (trigger, compress, ccr, recall, storage, `window.*`). `projects` is global-only. `upstream.*`, `credentials.*`, `listen.*` and `tools.external_summary.*` are rejected in every file.
- `acd-ctx config show --effective` prints every key with its value and source.
- The proxy reloads config per project root at most every 30 seconds.

## Gotchas

- **Concurrent git commits.** When several agents commit at once, `.git/index.lock` errors happen. Wait and retry. Never delete the lock while a git process may still be running.
- **Go version.** CI reads the Go version from the `go` line in `go.mod`, so the local toolchain must be at least 1.27.1.
- **Throwaway HOME.** Tests that touch config or data must set `HOME`, `XDG_CONFIG_HOME` and `XDG_DATA_HOME` to a temp dir. The e2e harness (`serveEnv`) strips inherited `HOME` and `ACD_CTX_*`, and launcher tests use `t.Setenv`. Do this for any new test that loads config.
- **No real provider traffic in tests.** The default `serve` upstream is `https://api.anthropic.com` (`cmd/acd-ctx/main.go`), and launcher profiles hardcode `api.anthropic.com` and `api.openai.com`. Point tests at `httptest` servers through `--upstream` or the `/ctx/<url>/` prefix.
- **Refs are cache-sensitive.** `kernel.RefLabel` gives each raw message a `[mNNNNN]` label that depends only on its ledger position. Keep it byte-stable: the provider's prompt cache depends on the rendered prefix not changing between turns. Do not add per-turn data to it.
- **Python tests leave `__pycache__`.** Set `PYTHONDONTWRITEBYTECODE=1`, or delete the directories before committing; `.gitignore` does not cover them.
- **e2e window.** The scenarios reach the 128K window through the `gpt-4o` model-name prefix. `window.context_limit` exists now, but the scenarios do not use it. `TestConfigCannotSetContextWindow` is still skipped.
- **Not implemented yet:** I8, `attach --system-proxy` and `--transparent`, the plugin `compact` endpoint, and `search_context` in the plugin and MCP companion.

## Implementation status

Phase 1 of the plan (§21) covers `store`, `kernel` basics and `project`. The spec's phases and acceptance criteria are in §21. The proxy, launchers, `attach`, `doctor`, `mcp`, the plugin protocol with its Python and Node clients, and `pkg/acdctx` are implemented and pass `go test ./...`. None of it is validated against live provider APIs or real client binaries (see README Status).
