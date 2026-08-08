# TUI Codex-Parity Polish — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Match the approved mockup (`docs/tui-codex-parity-mockup.html`) and design spec: full-line diff tints, lettered selection rows for all pickers (approval keeps y/a/p/n semantics), cool-surface composer tint.

**Architecture:** Three focused `internal/cli` changes: (1) `diffBar` paints full-width bg with NBSP pad; (2) shared `selectionRow`/`selectionPanel` replace `rowLine`; (3) cool mix targets for `inputBoxTintFromBackground` + darker cool fallback.

**Tech Stack:** Go, lipgloss/bubbletea, existing theme SGR helpers.

**Acceptance mockup:** `docs/tui-codex-parity-mockup.html`  
**Design spec:** `docs/superpowers/specs/2026-08-08-tui-codex-parity-polish-design.md`

## Global Constraints

- Diff: full-line bg including indent/gutter; NBSP pad; trailing ansiReset; width = transcriptContentWidth
- Selectors: display a–z; migrate all rowLine call sites; approval y/a/p/n semantic priority first
- Composer: cool target mix not pure white; fallback `#2b3344`/237
- No slash completion restyle

---

### Task 1: Diff full-line background

**Files:** `internal/cli/diffview.go`, `internal/cli/chat_tui.go` (width arg), `internal/cli/diffview_test.go`

- [ ] Rewrite `diffBar` to arm bg from start of row, pad with NBSP to width, end with ansiReset
- [ ] Pass `transcriptContentWidth` into `diffBlock` from chat_tui
- [ ] Tests: full width, NBSP, trailing reset, bg before gutter
- [ ] Commit

### Task 2: Unified lettered selection

**Files:** new `selection_row.go` + tests; migrate all `rowLine` sites; approval keys in `chat_tui.go` / chooser

- [ ] `selectionLetter`, `selectionRow`, `selectionPanel`; remove `rowLine`
- [ ] Wire all pickers; approval hints + key priority
- [ ] Tests for letter map and approval key order
- [ ] Commit

### Task 3: Cool composer tint

**Files:** `theme.go`, `theme_test.go`

- [ ] Cool mix targets + fallback `#2b3344`/237
- [ ] Update fixture pins
- [ ] Commit

### Task 4: Acceptance

- [ ] Focused tests green
- [ ] Mockup checklist: full-line diff, a/b/c selectors, cool input on purple
