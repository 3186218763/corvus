# ADR-0002: Single width authority (x/ansi); uniseg only for segmentation

- Date: 2026-08-16
- Status: Accepted

## Context

Width measurement lived in three implementations across four libraries: cli's
`visibleWidth` (x/ansi, 21 files), agent's same-named `visibleWidth`
(go-runewidth plus a hand-rolled SGR strip), and the composer mixing
`uniseg.StringWidth` with `runewidth.RuneWidth`. A probe (preserved as
`TestVisibleWidthTerminalCellPins` in `internal/cli`) showed they disagree on
real terminal cells: go-runewidth answers 1 column for flags (🇺🇳) and keycaps
(1️⃣) where terminals render 2 — so agent's streamed markdown redraw
undercounted rows and left stale output. Two ad-hoc rune-count truncators
additionally split grapheme clusters mid-character.

## Decision

1. `charmbracelet/x/ansi` is the single width authority. Every width
   measurement goes through it — directly or via a package's thin
   `visibleWidth` wrapper (e.g. `internal/cli/box.go`, `internal/agent/width.go`).
2. `rivo/uniseg` is imported only for grapheme *segmentation* (cluster
   iteration, cursor stepping). `uniseg.StringWidth` is forbidden.
3. Do not add another width library. go-runewidth remains an **indirect**
   dependency (x/ansi itself builds on it) but must not be imported directly:
   width semantics come from x/ansi's grapheme handling, not raw rune tables.
4. Text truncation goes through the `internal/textutil` grapheme helpers —
   never a hand-rolled rune slice.

## Consequences

- Swapping or adding a width library is a semantic change.
  `TestVisibleWidthTerminalCellPins` is the acceptance table; reopen this ADR
  before adjusting its expectations to match a different library.
