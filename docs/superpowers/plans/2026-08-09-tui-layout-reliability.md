# TUI Layout Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Corvus TUI fit its terminal grid reliably, keep modals usable on short screens, and remove the persistent composer/user-message gray background blocks.

**Architecture:** Treat `transcriptContentWidth` and bottom-region row counting as frame contracts. Renderers consume the content width instead of raw terminal width; the bottom frame derives all visible rows from shared helpers. Modal input owns paste routing, while transcript/user-message styling remains foreground-only.

**Tech Stack:** Go, Bubble Tea v2, Lipgloss v2, existing `internal/cli` render helpers and Go tests.

---

### Task 1: Make transcript rendering consume the viewport content width

**Files:**
- Modify: `internal/cli/transcript.go`
- Modify: `internal/cli/chat_tui.go`
- Test: `internal/cli/transcript_test.go`
- Test: `internal/cli/separators_test.go`

- [ ] **Step 1: Write failing regression tests**

```go
func TestAltScreenTranscriptSourcesFitContentWidth(t *testing.T) {
    m := newTestChatTUI()
    m.width, m.nativeScrollback = 40, false
    m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: strings.Repeat("x", 38)})
    for _, line := range wrapBlock(m.transcript[0], transcriptContentWidth(m.width, false)) {
        if got := visibleWidth(ansi.Strip(line)); got > 39 { t.Fatalf("row width = %d", got) }
    }
}
```

- [ ] **Step 2: Run the focused tests and confirm the source-width test fails on the current renderer.**

Run: `go test ./internal/cli -run 'TestAltScreenTranscriptSourcesFitContentWidth|TestTurnDoneEmitsSeparatorAfterTools' -count=1`

Expected: the user source or separator exceeds 39 cells / adds a continuation row.

- [ ] **Step 3: Render every width-dependent transcript source with `contentWidth` and store separators as semantic sources that re-render after resize.**

```go
contentWidth := transcriptContentWidth(terminalWidth, m.nativeScrollback)
return renderUserBubble(source.raw, contentWidth, source.planMode, current)
```

Use an explicit separator source or regenerate fixed separator strings in `reflowTranscript`; do not retain a raw separator rendered at the old width.

- [ ] **Step 4: Re-run focused tests and package tests.**

Run: `go test ./internal/cli -run 'TestAltScreenTranscriptSourcesFitContentWidth|TestTurnDoneEmitsSeparatorAfterTools|TestTranscriptResize' -count=1`

Expected: PASS.

### Task 2: Unify bottom-frame height accounting and release transient raises

**Files:**
- Modify: `internal/cli/chat_tui.go`
- Test: `internal/cli/chat_tui_test.go`
- Test: `internal/cli/composer_raise_scroll_test.go`

- [ ] **Step 1: Write failing tests for queued feedback, wrapped working text, and closing completion.**

```go
func TestQueuedFeedbackFrameFitsHeight(t *testing.T) {
    m := sizedRunningTUI(t, 40, 14)
    m.pendingInterject = []string{"queued feedback"}
    assertFrameFits(t, m)
}

func TestCompletionCloseReleasesRaisedRows(t *testing.T) {
    m := completionTUI(t, 60, 12)
    m = updateKey(t, m, tea.KeyEsc)
    if m.composerRaisedRows != 0 { t.Fatalf("raised rows = %d", m.composerRaisedRows) }
}
```

- [ ] **Step 2: Run the focused tests and confirm they fail.**

Run: `go test ./internal/cli -run 'TestQueuedFeedbackFrameFitsHeight|TestCompletionCloseReleasesRaisedRows' -count=1`

Expected: the frame has too many rows or `composerRaisedRows` remains nonzero.

- [ ] **Step 3: Add one shared bottom-row helper for wrapped working text and queue indicator; remove the transient completion raise on close.**

```go
func (m chatTUI) rowsAboveComposer(width int) int {
    rows := lineCount(m.runningWorkingLine(...), width)
    rows += lineCount(m.renderQueueIndicator(), width)
    return rows
}
```

Use that helper in `bottomRows`, `View`, and cursor-origin calculation. Keep only active persistent panels in `composerRaisedRows`.

- [ ] **Step 4: Re-run focused tests and all CLI tests.**

Run: `go test ./internal/cli -run 'TestQueuedFeedbackFrameFitsHeight|TestCompletionCloseReleasesRaisedRows|TestStatusLineRenderedHeightMatchesBudget' -count=1`

Expected: PASS.

### Task 3: Make manager and approval panels responsive

**Files:**
- Modify: `internal/cli/skill_picker_view.go`
- Modify: `internal/cli/mcp_manager_view.go`
- Modify: `internal/cli/chat_tui.go`
- Modify: `internal/cli/selection_row.go`
- Test: `internal/cli/skill_picker_test.go`
- Test: `internal/cli/chat_tui_test.go`

- [ ] **Step 1: Write failing `36x14` and `40x14` render assertions.**

```go
func TestSkillPickerNarrowFrameFitsAndShowsSelection(t *testing.T) {
    m := skillPickerTUI(t, 36, 14)
    view := ansi.Strip(m.View().Content)
    assertLinesFit(t, view, 36, 14)
    if !strings.Contains(view, "demo") { t.Fatal("selected skill is not visible") }
}
```

- [ ] **Step 2: Run the focused tests and confirm the pre-fix render is clipped.**

Run: `go test ./internal/cli -run 'TestSkillPickerNarrowFrameFitsAndShowsSelection|TestApprovalNarrowFrameFits' -count=1`

Expected: width/height assertion fails.

- [ ] **Step 3: Replace hard minimum widths with viewport-clamped widths and clamp visible list rows to the manager's actual height budget.**

```go
w := max(viewWidth(m.width), 10)
visible := max(1, min(skillDialogMaxRows, availableManagerRows(m)))
```

Truncate labels before adding shortcut hints; when no room remains, omit the hint rather than exceeding the row width.

- [ ] **Step 4: Re-run focused manager tests.**

Run: `go test ./internal/cli -run 'TestSkillPickerNarrowFrameFitsAndShowsSelection|TestApprovalNarrowFrameFits|TestSkillPickerRenderWidthNarrow' -count=1`

Expected: PASS.

### Task 4: Route paste to the active modal and remove gray background blocks

**Files:**
- Modify: `internal/cli/chat_tui.go`
- Modify: `internal/cli/composer_selection.go`
- Modify: `internal/cli/chat_tui.go`
- Test: `internal/cli/composer_selection_test.go`
- Test: `internal/cli/transcript_test.go`

- [ ] **Step 1: Write failing behavior tests.**

```go
func TestModalPasteDoesNotMutateHiddenComposer(t *testing.T) {
    m := approvalTUI(t)
    m.input.SetValue("draft")
    m = updatePaste(t, m, "pasted")
    if got := m.input.Value(); got != "draft" { t.Fatalf("draft = %q", got) }
}

func TestComposerAndUserMessagesDoNotPaintFullRowBackground(t *testing.T) {
    configureCLITheme("dark")
    if strings.Contains(renderComposerField("› hi", 20), bgSGR(activeCLITheme.inputBoxBG)) { t.Fatal("composer background") }
    if strings.Contains(renderUserBubble("hi", 20, false, true), bgSGR(activeCLITheme.userBubbleBG)) { t.Fatal("user background") }
}
```

- [ ] **Step 2: Run focused tests and confirm they fail before the behavior change.**

Run: `go test ./internal/cli -run 'TestModalPasteDoesNotMutateHiddenComposer|TestComposerAndUserMessagesDoNotPaintFullRowBackground' -count=1`

Expected: hidden draft changes and background SGR is present.

- [ ] **Step 3: Let active searchable modals consume paste, suppress paste for non-text modal controls, and make composer/user rendering foreground-only.**

```go
if m.modalOwnsInput() {
    return m.updateModalPaste(msg)
}
```

Keep textarea selection styling, the `›` marker, current-message accent, and history-message faded foreground. Remove background padding rows.

- [ ] **Step 4: Re-run focused tests and the relevant renderer suite.**

Run: `go test ./internal/cli -run 'TestModalPasteDoesNotMutateHiddenComposer|TestComposerAndUserMessagesDoNotPaintFullRowBackground|TestUserBubble|TestComposerField' -count=1`

Expected: PASS.

### Task 5: Repair output/state correctness and remaining narrow-render regressions

**Files:**
- Modify: `internal/cli/chat_tui.go`
- Modify: `internal/cli/md.go`
- Modify: `internal/cli/toolcard.go`
- Modify: `internal/cli/diffview.go`
- Modify: `internal/cli/skill_picker.go`
- Test: matching existing `*_test.go` files

- [ ] **Step 1: Add focused failures for silent native shell outcome, long tool output visibility, narrow table width, CJK tab stops, and detail-wheel bounds.**

- [ ] **Step 2: Change each renderer/state machine only after its own failure is observed.**

- [ ] **Step 3: Add tool-id-scoped live tail state so unrelated events cannot finalize an active tool.**

- [ ] **Step 4: Run all CLI tests.**

Run: `go test ./internal/cli -count=1`

Expected: PASS.

### Task 6: Visual regression pass

**Files:**
- Test/manual evidence only

- [ ] **Step 1: Build the binary and exercise the real TUI at `80x30`, `40x14`, and `36x14`.**

Run: `go build -o /tmp/corvus-tui-audit ./cmd/corvus`

- [ ] **Step 2: Verify `/skills`, approval, completion close, queued feedback, modal paste, resize, and a long tool output.**

- [ ] **Step 3: Run `go test ./internal/cli -count=1` and record any unrelated repository-wide failures separately.**
