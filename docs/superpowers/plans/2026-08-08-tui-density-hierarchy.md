# TUI Density & Hierarchy Implementation Plan

> **For agentic workers:** Execute task-by-task. Steps use checkbox syntax.

**Goal:** Ship Codex-first transcript density: `›`/`•` markers, Explored coalesce, ambient thinking, plain assistant, thin footer, ~2-row composer — match `docs/tui-density-target.html`.

**Architecture:** Render-layer only in `internal/cli`. Pure render helpers in `toolcard.go` / `transcript.go` / `status_footer.go`; coalesce buffer + reasoning path in `chat_tui.go`.

**Tech Stack:** Go, Bubble Tea v2, existing cli theme helpers.

**Spec:** `docs/superpowers/specs/2026-08-08-tui-density-hierarchy-design.md`  
**Acceptance mockup:** `docs/tui-density-target.html`

## Global Constraints

- No agent/tool protocol changes
- Codex palette: cyan tree verbs, green/red outcome bullets, bash highlight; no category `●` rainbow
- Verbose reasoning path preserved
- Custom statusline still replaces footer data band

---

### Task 1: Tool card Codex markers + Explored/Ran/Edited pure render

**Files:** `internal/cli/toolcard.go`, `internal/cli/toolcard_test.go`

- [ ] Pure helpers: `isExploreCoalesceTool`, `exploreLeafFrom`, `exploredCard`, rewrite `toolCard`/`bashToolCard`/`toolDot`
- [ ] Tests for Ran/Edited/Explored markers and cyan tree verbs
- [ ] Commit

### Task 2: Explored coalesce at ToolDispatch

**Files:** `internal/cli/chat_tui.go`, tests

- [ ] Buffer consecutive explore tools into one transcript block
- [ ] Flush on write/exec/user/turn boundary
- [ ] Tests for 3 reads → 1 block; bash breaks merge
- [ ] Commit

### Task 3: Assistant without ◆ Corvus nameplate

**Files:** `internal/cli/transcript.go`, `*_test.go`

- [ ] Plain body / optional dim diamond history only if needed; no live name
- [ ] Update identity tests
- [ ] Commit

### Task 4: Thinking ambient-only (default)

**Files:** `internal/cli/chat_tui.go`, `chat_render_test.go`, `chat_tui_test.go`

- [ ] Default: buffer reasoning, no transcript wall; keep ambient working line
- [ ] Verbose: keep latest full text on commit
- [ ] Update tests
- [ ] Commit

### Task 5: Footer model · path only

**Files:** `internal/cli/status_footer.go`, `status_footer_test.go`

- [ ] Drop Effort/Work/CTX/cache from default row
- [ ] Right side: `model · path` (compact)
- [ ] Update footer tests
- [ ] Commit

### Task 6: Composer ~2 rows + optional › prompt

**Files:** `internal/cli/chat_tui.go`

- [ ] MinHeight/SetHeight 2
- [ ] Commit

### Task 7: Full package test + checklist note

- [ ] `go test ./internal/cli/ -count=1` relevant packages
- [ ] Manual note vs HTML checklist
