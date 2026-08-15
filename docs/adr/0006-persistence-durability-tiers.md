# ADR-0006: One file-lock implementation; shared torn-tail guard; four deliberate durability tiers for JSONL appends

- Date: 2026-08-16
- Status: Accepted

## Context

The audit (`.scratch/dep-audit/FINDINGS.md` B5/C4/C7) found:

- `workspacelease`'s `lock_unix.go`/`lock_windows.go` were byte-identical to
  `internal/filelock`'s (modulo package clause and `errHeld` vs `ErrHeld`), and
  it also re-implemented the in-process registry, the retry loop (75ms vs
  filelock's 20ms), and the release — three copies of one mechanism.
- Four JSONL appenders with diverging crash behavior: `stats` repaired a torn
  trailing line; `agent` session events fsync rare events and salvage damaged
  tails to a `.damaged` sidecar; `control`'s conflict log appended bare
  (0644, no guard); `autoresearch` appended bare under an `os.Root` jail.
- `memory`'s `MEMORY.md` index rewrite used `os.WriteFile` (O_TRUNC), so a
  crash mid-flush could destroy the index, while `fileutil.AtomicWriteFile`
  existed and was the repo convention.

## Decision

1. **One lock implementation.** `workspacelease` deletes its lock file pair
   and in-process registry and delegates to `filelock.Acquire`. The lease
   keeps its wrapper semantics (acquire timeout, retention grace, re-entrancy,
   leak counting) but no longer owns lock mechanics. Its "busy" WaitNotice
   survives as `filelock.WithWaitHook`, called at most once when an
   acquisition cannot complete immediately. The retry cadence becomes
   filelock's 20ms; release gains the sync.Once double-release guard.
2. **Shared torn-tail guard.** `fileutil.EnsureTrailingNewline(*os.File)`
   (open O_RDWR|O_APPEND) separates a crash-torn line from the next append.
   `stats`, `control`, and `autoresearch` all adopt it. A torn line still
   stays in the file — readers skip it — but it can no longer fuse with the
   next record.
3. **Not** one JSONL package. The four appenders differ deliberately, and a
   unified helper would either flatten those differences or grow an option
   set:
   - `stats`: cross-process `filelock` + boundary repair; no fsync (loss of a
     stats line is acceptable).
   - `agent` session events: conditional fsync for rare replace events;
     truncate-and-salvage repair with a `.events.jsonl.damaged` sidecar,
     coordinated with the event index. Too session-specific to share.
   - `control` conflict log: best-effort by design (errors swallowed,
     diagnostics only) — now 0600 with the boundary guard.
   - `autoresearch`: `os.Root`-jailed appends; files 0644 inside a 0700
     state dir.
4. **Atomic index rewrites.** `memory` uses `fileutil.AtomicWriteFile` for
   `MEMORY.md`, matching every other rewritten-file writer in the repo.

## Consequences

- The lock surface shrinks by one implementation; behavior deltas are the
  20ms retry cadence, the once-guarded release, and error text now wrapping
  `context.DeadlineExceeded` (callers use `errors.Is`, verified in
  `lease_test.go`).
- A crash between two conflict-log or autoresearch appends can still tear a
  line (O_APPEND mid-write), but the next append starts a fresh line and the
  readers' skip behavior covers the remainder.
- Session transcripts (`<id>.jsonl`) are untouched: they were already atomic
  (temp + rename), never O_APPEND.
