# AGENTS.md

Agent instructions for working in this repository. Humans see README.md.

## Build and verify

- `make build` builds the static binary; `make check` runs everything CI runs
  (vet, gofmt, catalog freshness, tests, race). Run `make check` before
  finishing a change set.
- Never commit generated docs by hand: `docs/tool-catalog.md` and
  `docs/event-map.md` come from `cmd/corvus-catalog`. Regenerate with
  `make tool-catalog` / `make event-map`; `make verify-catalog` is the gate.
- Tests live beside the code (`_test.go`, in-package or `_test` package
  following the surrounding files). Prefer focused tests for new behavior.

## Repository conventions

- `internal/store` is the single authority for the on-disk persistence layout.
  Never construct a session/sidecar path by hand in another package.
- Session persistence is an append/replace event log; model history is derived
  from it. Anything a model request can show must be reconstructable from the
  log (`CORVUS_SESSION_ASSERT=1` arms the save-time round-trip check).
- New model-visible behavior goes through `internal/tool`'s `Tool` interface
  and its optional capability interfaces (`ReadOnly`, `Previewer`,
  `ConcurrencySafe`, `PlanModeClassifier`, `SnipHinter`). Never add scheduler
  policy by hardcoding tool names in the loop.
- Optional capabilities belong beside the loop spine, not inside it: the
  compaction policy lives in `internal/compaction`, tool-output spilling in
  `internal/spill`. Keep `internal/agent` loop glue thin.
- JSONL appends follow ADR-0006 durability tiers; use
  `fileutil.EnsureTrailingNewline` (open `O_APPEND|O_RDWR`) for best-effort
  diagnostics like the hook audit sidecar.
- Decisions with cross-cutting rationale go in `docs/adr/` (numbered, dated);
  external research and comparison notes go in `docs/notes/`.
- Run `gofmt` (Makefile `fmt` rewrites, `fmt-check` verifies). Do not reformat
  unrelated files.

## Concurrency contracts

- The run loop is the only writer of a `Session`; frontends read via
  `Snapshot()`. Fields guarded by `sessMu` on the Agent (session pointer,
  spill dir) are swapped only between turns.
- A tool returning `ConcurrencySafe(args) == true` promises its body may run
  alongside any sibling call that also returned true and does not mutate
  parent-owned state; results commit in model order regardless of completion
  order. Classifiers must be pure; the scheduler recovers from panics and
  classifies them exclusive.
