# Codex-Style Composer Popup, Cache Hit-Rate & Footer Label Casing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Corvus TUI popups expand below the composer with a held raised position (Codex-style), show the session cache hit rate instead of a token count, and switch footer labels to Title Case.

**Architecture:** Reorder the bottom-region `parts` in `chatTUI.View()` so panels render after the composer; add `composerRaisedRows` (set when a panel is visible while the composer is shown, cleared on submit) that `bottomRows()` and `View()` use to hold the raised space. Replace `renderTurnReceipt` (token count) with `renderCacheHitRate` (session aggregate `hit/(hit+miss)`). Change 3 English i18n labels.

**Tech Stack:** Go 1.25, Bubble Tea v2, Bubblegum/lipgloss (existing TUI), standard `go test` + `make build`.

**Spec:** `docs/superpowers/specs/2026-08-07-codex-style-composer-popup-design.md`

**Baseline:** commit `1fd62bf` (`docs: record Codex-style composer popup design`). All steps run from repo root `/home/miku/dv_project/corvus`.

---

### Task 1: Footer label casing (`Model` / `Effort` / `Work`)

**Files:**
- Modify: `internal/i18n/messages_en.go:51-53`
- Modify: `internal/cli/chat_tui.go:4122` (comment only)
- Test: `internal/cli/status_footer_test.go`, `internal/cli/statusline_test.go`

- [ ] **Step 1: Update the test assertions to the new labels**

In `internal/cli/status_footer_test.go` replace every hardcoded old label:

- Line ~115-119 table `want` list: `tt.labelSGR + "MODEL"` → `tt.labelSGR + "Model"`, `tt.labelSGR + "EFFORT"` → `tt.labelSGR + "Effort"`, `tt.labelSGR + "WORK"` → `tt.labelSGR + "Work"`.
- Line ~218: `footerSecondary("~/project") + " · " + footerInfo("MODEL m")` → `footerInfo("Model m")`.
- Line ~226: `strings.HasSuffix(ansi.Strip(got), "MODEL m")` → `"Model m"`.
- Line ~266: `[]string{"MODEL deepseek-v4-flash", "EFFORT auto", "WORK balanced"}` → `[]string{"Model deepseek-v4-flash", "Effort auto", "Work balanced"}`.
- Line ~281: `session: "MODEL deepseek-v4-flash   EFFORT auto   WORK balanced"` → `"Model deepseek-v4-flash   Effort auto   Work balanced"` (English row only; zh / zh-TW rows unchanged).
- Line ~401: `strings.Contains(plain, "MODEL deepseek-v4-flash   EFFORT auto   WORK balanced")` → same replacement.
- Line ~459-463: `statusFooterIndent+"MODEL deepseek-v4-flash"` → `statusFooterIndent+"Model deepseek-v4-flash"`; `"EFFORT auto   WORK balanced"` → `"Effort auto   Work balanced"`; `strings.Count(strings.TrimLeft(modelRow, " "), "MODEL")` → `"Model"`.
- Line ~530: `strings.Contains(block, "MODEL")` → `strings.Contains(block, "Model")`.

In `internal/cli/statusline_test.go`:
- Line ~421: `"MODEL deepseek-v4-flash   EFFORT auto"` → `"Model deepseek-v4-flash   Effort auto"`.
- Line ~431: `"MODEL deepseek-v4-flash"` → `"Model deepseek-v4-flash"`.
- Line ~447: same as 421.
- Line ~465: `"MODEL deepseek-v4-flash   WORK delivery"` → `"Model deepseek-v4-flash   Work delivery"`.
- Line ~489: `"EFFORT max"` → `"Effort max"`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestStatus|TestStatusline' 2>&1 | head -30`
Expected: FAIL — assertions still find the old all-caps labels.

- [ ] **Step 3: Change the English labels**

In `internal/i18n/messages_en.go` (lines 51-53):

```go
	ChatStatusModelLabel:                   "Model",
	ChatStatusEffortLabel:                  "Effort",
	ChatStatusWorkLabel:                    "Work",
```

Do NOT touch `messages_zh.go` (`模型`/`强度`/`模式`) or `messages_zh_tw.go` (`模型`/`強度`/`模式`).

- [ ] **Step 4: Update the stale comment**

In `internal/cli/chat_tui.go` near line 4122, change:

```go
		// The persistent footer uses an uppercase semantic label. The expanded
		// diagnostic view keeps its sentence-like wording for readability.
```

to:

```go
		// The persistent footer uses a Title Case semantic label. The expanded
		// diagnostic view keeps its sentence-like wording for readability.
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ 2>&1 | tail -5`
Expected: PASS for `internal/cli` package.

- [ ] **Step 6: Commit**

```bash
git add internal/i18n/messages_en.go internal/cli/chat_tui.go internal/cli/status_footer_test.go internal/cli/statusline_test.go
git commit -m "style(i18n): Title Case footer labels (Model/Effort/Work)"
```

---

### Task 2: Session cache hit-rate receipt

**Files:**
- Modify: `internal/cli/status_footer.go:56-64` (`renderTurnReceipt` → `renderCacheHitRate`)
- Modify: `internal/cli/chat_tui.go:3782` (Usage event handler)
- Test: `internal/cli/chat_tui_test.go` (~1385), `internal/cli/chat_render_test.go` (`TestTurnReceiptMovesBelowComposer`), `internal/cli/status_footer_test.go` (~620)

- [ ] **Step 1: Write the failing tests**

In `internal/cli/status_footer_test.go` add (replace the `renderTurnReceipt` setup in the test at line ~618):

```go
	m.turnReceipt = renderCacheHitRate(900, 100)
```

and change the `want` list near line ~620 from `"cached 900"` to `"cached 90.00%"`.

Also add a pure-function test at the end of `internal/cli/status_footer_test.go`:

```go
func TestRenderCacheHitRateFormatsSessionRate(t *testing.T) {
	if got := renderCacheHitRate(900, 100); got != "cached 90.00%" {
		t.Fatalf("renderCacheHitRate(900,100) = %q, want cached 90.00%%", got)
	}
	if got := renderCacheHitRate(0, 0); got != "" {
		t.Fatalf("renderCacheHitRate(0,0) = %q, want empty", got)
	}
}
```

In `internal/cli/chat_tui_test.go` replace the usage table near line ~1385 with:

```go
	// Usage does not commit a scrollback line; the receipt derives from the
	// controller's session cache (nil ctrl in these tests → empty receipt).
	for _, tc := range []struct {
		name string
		ev   event.Event
	}{
		{"usage", event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200, CacheHitTokens: 900, CacheMissTokens: 100}}},
		{"usage-zero-hit", event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200}}},
	} {
		m := newTestChatTUI()
		m.ingestEvent(tc.ev)
		if got := *m.pendingCommit; len(got) != 0 {
			t.Errorf("%s: usage must not commit a scrollback line, got %v", tc.name, got)
		}
		if m.turnReceipt != "" {
			t.Errorf("%s: receipt must be empty without session cache data, got %q", tc.name, m.turnReceipt)
		}
	}
	if got := renderCacheHitRate(900, 100); got != "cached 90.00%" {
		t.Errorf("renderCacheHitRate(900,100) = %q, want cached 90.00%%", got)
	}
```

In `internal/cli/chat_render_test.go` (`TestTurnReceiptMovesBelowComposer`, line ~113), after the three `ingestEvent` calls add:

```go
	m.turnReceipt = renderCacheHitRate(900, 100)
```

and change the receipt-content assertion from `strings.Contains(ansi.Strip(m.turnReceipt), "cached")` to `strings.Contains(ansi.Strip(m.turnReceipt), "cached 90.00%")`, and the view assertion from `strings.Contains(ansi.Strip(view), "cached")` to `strings.Contains(ansi.Strip(view), "cached 90.00%")`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRenderCacheHitRate|TestTurnReceipt|TestUsage|TestStatus' 2>&1 | head -40`
Expected: FAIL — `renderTurnReceipt` is undefined / receipt still shows token counts.

- [ ] **Step 3: Implement `renderCacheHitRate`**

In `internal/cli/status_footer.go` replace `renderTurnReceipt` (lines 56-64) with:

```go
// renderCacheHitRate renders the current conversation's prompt-cache hit rate
// (Σhit / Σ(hit+miss) across the whole session), e.g. "cached 87.50%". Hidden
// until the provider reports any cache tokens (denominator 0).
func renderCacheHitRate(hit, miss int) string {
	if hit+miss <= 0 {
		return ""
	}
	return footerMetric(i18n.M.ChatCacheHitLabel, footerValue(cacheRateLabel("%s", hit, hit+miss)))
}
```

(`cacheRateLabel` already lives in `internal/cli/chat_tui.go` and formats `%.2f%%`.)

- [ ] **Step 4: Update the Usage event handler**

In `internal/cli/chat_tui.go` at the `case event.Usage:` handler (~line 3782), replace:

```go
		m.finalizeStreamed()
		m.turnReceipt = renderTurnReceipt(e.Usage)
```

with:

```go
		m.finalizeStreamed()
		m.turnReceipt = ""
		if m.ctrl != nil {
			hit, miss := m.ctrl.SessionCache()
			m.turnReceipt = renderCacheHitRate(hit, miss)
		}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ 2>&1 | tail -5`
Expected: PASS. Also check no remaining callers: `rg -n "renderTurnReceipt" internal/` → no hits.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/status_footer.go internal/cli/chat_tui.go internal/cli/chat_tui_test.go internal/cli/chat_render_test.go internal/cli/status_footer_test.go
git commit -m "feat(cli): show session cache hit rate in footer receipt"
```

---

### Task 3: Popups below the composer + raised-position state machine

**Files:**
- Modify: `internal/cli/chat_tui.go` (struct field ~388, Update wrapper ~843, `bottomRows` ~1961, `View` ~2805-2890, idle Enter handler ~1476)
- Test: `internal/cli/chat_tui_test.go` (new tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/chat_tui_test.go`:

```go
func completionTestTUI() chatTUI {
	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	m.width = 80
	m.completion.active = true
	m.completion.kind = compSlash
	m.completion.items = []compItem{{label: "/help", hint: "show help"}}
	return m
}

func promptRow(view string) int {
	for i, ln := range strings.Split(view, "\n") {
		if strings.Contains(ln, "❯") {
			return i
		}
	}
	return -1
}

func TestCompletionMenuRendersBelowComposer(t *testing.T) {
	m := completionTestTUI()
	m0, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m0.(chatTUI)

	if m.composerRaisedRows == 0 {
		t.Fatalf("open completion should raise the composer, got composerRaisedRows=0")
	}
	view := ansi.Strip(m.View().Content)
	boxIdx := strings.LastIndex(view, "❯")
	menuIdx := strings.Index(view, "/help")
	if boxIdx < 0 || menuIdx < 0 || menuIdx < boxIdx {
		t.Fatalf("completion menu should render below the composer (box at %d, menu at %d):\n%s", boxIdx, menuIdx, view)
	}
}

func TestComposerStaysRaisedAfterMenuCloses(t *testing.T) {
	m := completionTestTUI()
	m0, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m0.(chatTUI)
	raised := m.composerRaisedRows
	if raised == 0 {
		t.Fatalf("open completion should raise the composer")
	}
	openRow := promptRow(ansi.Strip(m.View().Content))

	// Cancel the menu (Esc). The raised position must be held.
	m.completion = completion{}
	m0, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m0.(chatTUI)

	if m.composerRaisedRows != raised {
		t.Fatalf("composerRaisedRows = %d after cancel, want held %d", m.composerRaisedRows, raised)
	}
	if got := promptRow(ansi.Strip(m.View().Content)); got != openRow {
		t.Fatalf("input row = %d after cancel, want held at %d:\n%s", got, openRow, m.View().Content)
	}
}

func TestComposerDropsToBottomOnSubmit(t *testing.T) {
	r := &blockingTurnRunner{started: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: r, Sink: event.Discard, SessionDir: t.TempDir(), Label: "test"})
	m := newChatTUI(ctrl, "", make(chan event.Event, 8), 80)
	m.composerRaisedRows = 4
	m.input.SetValue("hello")

	m0, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = m0.(chatTUI)

	if m.composerRaisedRows != 0 {
		t.Fatalf("composerRaisedRows = %d after submit, want 0", m.composerRaisedRows)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestCompletionMenuRendersBelowComposer|TestComposerStaysRaisedAfterMenuCloses|TestComposerDropsToBottomOnSubmit' -v 2>&1 | tail -20`
Expected: FAIL — menu renders above the composer; `composerRaisedRows` does not exist yet.

- [ ] **Step 3: Add the state field**

In `internal/cli/chat_tui.go` next to the `completion` field (~line 388):

```go
	// completion is the live autocomplete menu (slash commands; @-refs later).
	completion completion
	// composerRaisedRows holds the height of the last visible bottom panel so
	// the composer stays raised (Codex-style) after a popup closes, until the
	// next submission drops it back to the bottom.
	composerRaisedRows int
```

- [ ] **Step 4: Raise on visible panels (Update wrapper)**

In `internal/cli/chat_tui.go` in the `Update` wrapper, right after `cm.panelsValid = true` (line ~843):

```go
	// Codex-style raise: while the composer is visible, any bottom panel lifts
	// the input; the height is held after the panel closes until submission.
	if !cm.hideComposer() && cm.panels.rows > 0 {
		cm.composerRaisedRows = cm.panels.rows
	}
```

- [ ] **Step 5: Hold the raised space in `bottomRows`**

In `internal/cli/chat_tui.go` `bottomRows()` (line ~1961), after the panels-rows assignment and before `if !m.hideComposer()`:

```go
	// Hold the raised-composer space after a popup closes until the next
	// submission drops the composer back to the bottom (Codex-style).
	if rows == 0 && !m.hideComposer() && m.composerRaisedRows > 0 {
		rows = m.composerRaisedRows
	}
```

- [ ] **Step 6: Reorder `View()` so popups render below the composer**

In `internal/cli/chat_tui.go` `View()`, replace the block from `var parts []string` (line ~2812) through the `parts = append(parts, statusBlockStyle...` line (~2870) with:

```go
	var parts []string
	rowsAboveBox := 0 // terminal rows before the composer (working line, manager footer, queue indicator)
	// The working spinner (when running), the manager footer, and the queue
	// indicator render ABOVE the composer; popups render BELOW it (Codex-style:
	// typing "/" raises the input and the menu expands downward).
	if working != "" {
		parts = append(parts, workingStyle.Width(boxW).MaxWidth(boxW).Render(wrapStatusLine(working, boxW)))
		rowsAboveBox++
	}
	if footer := panels.managerFooter; footer != "" {
		parts = append(parts, footer)
		rowsAboveBox += strings.Count(footer, "\n") + 1
	}
	if !hideComposer {
		if qi := m.renderQueueIndicator(); qi != "" {
			parts = append(parts, qi)
			rowsAboveBox += strings.Count(qi, "\n") + 1
		}
		parts = append(parts, box)
	}
	// Popups expand below the composer. While open they raise the input; after
	// dismissal the raised space is held (composerRaisedRows) until the next
	// submission drops it back to the bottom.
	var panelParts []string
	for _, s := range []string{panels.todo, panels.banner, panels.chooser, panels.rewind, panels.mcpImport, panels.resumePick, panels.quickPick, panels.copyPick, panels.cheatsheet, panels.completion} {
		if s != "" {
			panelParts = append(panelParts, s)
		}
	}
	if m.nativeScrollback && panels.manager != "" {
		panelParts = append(panelParts, panels.manager)
	}
	if len(panelParts) > 0 {
		parts = append(parts, panelParts...)
	} else if !hideComposer && m.composerRaisedRows > 0 {
		parts = append(parts, strings.Repeat("\n", m.composerRaisedRows))
	}
	statusBlock := m.renderStatusBlock(primaryStatus, boxW)
	parts = append(parts, statusBlockStyle.Width(boxW).MaxWidth(boxW).Render(statusBlock))
```

The cursor-anchoring code below (`cur.Y += rowsAboveBox`) stays unchanged — it now correctly excludes panel rows because `rowsAboveBox` no longer counts them.

- [ ] **Step 7: Drop on submit (idle Enter handler)**

In `internal/cli/chat_tui.go` idle `case "enter":` handler, immediately after `m.rememberSubmittedInput(line)` (line ~1476):

```go
			// The raised composer drops back to the bottom the moment the user
			// submits anything (memory notes, shell commands, slash commands,
			// or a normal turn).
			m.composerRaisedRows = 0
```

- [ ] **Step 8: Run the targeted tests**

Run: `go test ./internal/cli/ -run 'TestCompletionMenuRendersBelowComposer|TestComposerStaysRaisedAfterMenuCloses|TestComposerDropsToBottomOnSubmit' -v 2>&1 | tail -15`
Expected: PASS for all three.

- [ ] **Step 9: Run the full test suite**

Run: `go test ./... 2>&1 | tail -20`
Expected: PASS across all packages. If an unrelated test asserts the old panel-above-composer order, update that assertion to the new order (search `rg -n "rowsAboveBox|above the composer" internal/cli/*_test.go` first — none exist at baseline).

- [ ] **Step 10: Build and smoke-check**

Run: `make build`
Expected: `bin/corvus` builds. Then run `./bin/corvus` in a terminal: type `/`, confirm the command menu opens below the input and the input rises; press `Esc` and confirm the input stays raised; submit a message and confirm the input returns to the bottom.

- [ ] **Step 11: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/chat_tui_test.go
git commit -m "feat(cli): Codex-style popups below composer with raised-position hold"
```

---

## Self-Review Notes

- Spec 4.1 → Task 3 Steps 6 (reorder) + 5 (bottomRows hold).
- Spec 4.2 → Task 3 Steps 3 (field), 4 (raise), 5 (hold), 7 (drop).
- Spec 4.3 → Task 2 (renderCacheHitRate + Usage handler).
- Spec 4.4 → Task 1 (labels + comment).
- Spec 5 (tests) → Task 1 Step 1, Task 2 Step 1, Task 3 Step 1; manual check Task 3 Step 10.
- Cursor anchoring (spec risk §6): covered by Task 3 Step 6 note — `rowsAboveBox` excludes panels; existing cursor tests in `chat_tui_test.go` keep guarding the offset.
- `provider` import in `status_footer_test.go` becomes unused after removing the `provider.Usage{...}` literal at line ~618 — delete the import if `go test` reports it unused.
