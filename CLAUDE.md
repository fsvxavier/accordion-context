# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

Pre-implementation. No Go code, `go.mod`, build, or test setup exists yet. The only real content is the design spec [compactacao-contexto-go-sqlite.md](compactacao-contexto-go-sqlite.md) (Portuguese, v0.1). Treat it as the source of truth and read the relevant section before implementing anything. The section numbers (§N) below refer to it.

Accordion Context reimplements the context-compaction model of [billion-context](https://github.com/ranxianglei/billion-context) (`acp-kernel`) in Go, with SQLite as the source of truth. It runs as a proxy between a coding agent and the model API. The session's model writes summaries of older message ranges, and the system decides when to ask for them and how to rebuild the visible window. Compaction is incremental, reversible (originals are never deleted, only hidden) and hierarchical (tiers T1→T2→T3).

## Naming conventions (§ header table)

- Binary/command: `acd-ctx` (`cmd/acd-ctx`)
- Project-local folder: `.acd-ctx/`; DB file: `acd-ctx.db`
- Env vars `ACD_CTX_*`, HTTP headers `X-Acd-Ctx-*`, internal endpoints `/__acd-ctx/*`, metrics `acd_ctx_*`

## Planned architecture (§4)

Request flow: agent, then `wire/` (protocol adapters: anthropic, openai, responses, google), then `session/` (turn orchestration, tools, compress loop), then `kernel/`. `store/` holds SQLite (WAL, FTS5).

Hard rule: `internal/kernel` is pure (no I/O) and imports nothing from `store`, `wire` or `session`. It takes a `Snapshot` and returns a `TurnPlan` (`ProcessTurn(s Snapshot, cfg Config) TurnPlan`). Only `session` writes, through `store`.

Other packages: `plugin/` (HTTP loopback plugin protocol + MCP server), `launcher/` (per-client launchers, MITM), `project/` (project root detection, config scopes, §18), `crush/` (deterministic shrinking of large tool outputs), `tokens/` (tokenizer calibrated against provider `usage`, §15). Recorded replay traces go in `testdata/traces/*.jsonl`.

## Key design decisions

- SQLite driver: `modernc.org/sqlite` (pure Go, no CGO, FTS5). Open with `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`. One writer per session (per-session mutex). The schema is in §5.2, and the event log is append-only.
- The compression trigger (§8) is one spec, implemented as a pure function (`DecideNudge` in `kernel/trigger.go`). The table tests in `trigger_test.go` are the executable spec: change the §8.3 decision table and the tests together.
- A compression is confirmed only by a `compression_events` row in the DB, never by model text. Strip fake "📦 Compressed…" markers from model output.
- Protected content: first user message is always pinned, the recent zone stays visible, and tool_call/tool_result pairs are atomic.

## Invariants (§17.1)

Behavior is tied to named invariants I1–I9, not to one-off fixes. Examples: no message is covered by more than one active block, active blocks do not overlap, tool pairs are never split, `sum_tokens < src_tokens` (a schema CHECK), and the system prompt is byte-identical across turns. `checkInvariantsTx` runs I1–I3, I6 and I7 inside every compression transaction. Each SQL check must return zero rows.

## Testing strategy (§17.2), once code exists

- Trigger table tests: one case per row of §8.3, plus the boundaries.
- Property tests (`testing/quick` or `pgregory.net/rapid`): I1–I9 must hold after every random step.
- Fuzzing the `compress` argument parser: `go test -fuzz=<FuzzName> ./internal/...`
- Trace replay against golden files.
- `store` integration tests on in-memory SQLite (`file::memory:?cache=shared`).

Standard Go commands apply: `go test ./...` and, for a single test, `go test ./internal/kernel -run TestName`.

## Implementation plan

The phases and their acceptance criteria are in §21. Phase 1 covers `store`, the `kernel` basics and `project`.

<!-- rtk-instructions v2 -->
# RTK (Rust Token Killer) - Token-Optimized Commands

## Golden Rule

**Always prefix commands with `rtk`**. If RTK has a dedicated filter, it uses it. If not, it passes through unchanged. This means RTK is always safe to use.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

## RTK Commands by Workflow

### Build & Compile (80-90% savings)
```bash
rtk cargo build         # Cargo build output
rtk cargo check         # Cargo check output
rtk cargo clippy        # Clippy warnings grouped by file (80%)
rtk tsc                 # TypeScript errors grouped by file/code (83%)
rtk lint                # ESLint/Biome violations grouped (84%)
rtk prettier --check    # Files needing format only (70%)
rtk next build          # Next.js build with route metrics (87%)
```

### Test (60-99% savings)
```bash
rtk cargo test          # Cargo test failures only (90%)
rtk go test             # Go test failures only (90%)
rtk jest                # Jest failures only (99.5%)
rtk vitest              # Vitest failures only (99.5%)
rtk playwright test     # Playwright failures only (94%)
rtk pytest              # Python test failures only (90%)
rtk rake test           # Ruby test failures only (90%)
rtk rspec               # RSpec test failures only (60%)
rtk test <cmd>          # Generic test wrapper - failures only
```

### Git (59-80% savings)
```bash
rtk git status          # Compact status
rtk git log             # Compact log (works with all git flags)
rtk git diff            # Compact diff (80%)
rtk git show            # Compact show (80%)
rtk git add             # Ultra-compact confirmations (59%)
rtk git commit          # Ultra-compact confirmations (59%)
rtk git push            # Ultra-compact confirmations
rtk git pull            # Ultra-compact confirmations
rtk git branch          # Compact branch list
rtk git fetch           # Compact fetch
rtk git stash           # Compact stash
rtk git worktree        # Compact worktree
```

Note: Git passthrough works for ALL subcommands, even those not explicitly listed.

### GitHub (26-87% savings)
```bash
rtk gh pr view <num>    # Compact PR view (87%)
rtk gh pr checks        # Compact PR checks (79%)
rtk gh run list         # Compact workflow runs (82%)
rtk gh issue list       # Compact issue list (80%)
rtk gh api              # Compact API responses (26%)
```

### JavaScript/TypeScript Tooling (70-90% savings)
```bash
rtk pnpm list           # Compact dependency tree (70%)
rtk pnpm outdated       # Compact outdated packages (80%)
rtk pnpm install        # Compact install output (90%)
rtk npm run <script>    # Compact npm script output
rtk npx <cmd>           # Compact npx command output
rtk prisma              # Prisma without ASCII art (88%)
rtk uv run <cmd>        # Compact uv project command output
```

### Files & Search (60-75% savings)
```bash
rtk ls <path>           # Tree format, compact (65%)
rtk read <file>         # Code reading with filtering (60%)
rtk grep <pattern>      # Search grouped by file (75%). Format flags (-c, -l, -L, -o, -Z) run raw.
rtk find <pattern>      # Find grouped by directory (70%)
```

### Analysis & Debug (70-90% savings)
```bash
rtk err <cmd>           # Filter errors only from any command
rtk log <file>          # Deduplicated logs with counts
rtk json <file>         # JSON structure without values
rtk deps                # Dependency overview
rtk env                 # Environment variables compact
rtk summary <cmd>       # Smart summary of command output
rtk diff                # Ultra-compact diffs
```

### Infrastructure (85% savings)
```bash
rtk docker ps           # Compact container list
rtk docker images       # Compact image list
rtk docker logs <c>     # Deduplicated logs
rtk kubectl get         # Compact resource list
rtk kubectl logs        # Deduplicated pod logs
```

### Network (65-70% savings)
```bash
rtk curl <url>          # Compact HTTP responses (70%)
rtk wget <url>          # Compact download output (65%)
```

### Meta Commands
```bash
rtk gain                # View token savings statistics
rtk gain --history      # View command history with savings
rtk discover            # Analyze Claude Code sessions for missed RTK usage
rtk proxy <cmd>         # Run command without filtering (for debugging)
rtk init                # Add RTK instructions to CLAUDE.md
rtk init --global       # Add RTK to ~/.claude/CLAUDE.md
```

## Token Savings Overview

| Category | Commands | Typical Savings |
|----------|----------|-----------------|
| Tests | vitest, playwright, cargo test | 90-99% |
| Build | next, tsc, lint, prettier | 70-87% |
| Git | status, log, diff, add, commit | 59-80% |
| GitHub | gh pr, gh run, gh issue | 26-87% |
| Package Managers | pnpm, npm, npx | 70-90% |
| Files | ls, read, grep, find | 60-75% |
| Infrastructure | docker, kubectl | 85% |
| Network | curl, wget | 65-70% |

Overall average: **60-90% token reduction** on common development operations.
<!-- /rtk-instructions -->