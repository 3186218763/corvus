# TUI Render Animation & Art Polish (P1.5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship fluidity fixes (no ClearScreen-on-scroll by default, incremental transcript wrap, single-pass bottom panels), a reduced-motion gate with smooth scroll and fixed-width elapsed, and a curated art set (branding, density, neutral table code cells, color discipline) — all TDD with benchmarks.

**Architecture:** Keep Bubble Tea `chatTUI`. (1) env helpers `motionEnabled()`/`scrollRepaintEnabled()`; (2) per-block wrapped-line cache (`blockLineCounts`) replacing whole-transcript re-wrap; (3) `bottomPanels` cache refreshed once inside the Update wrapper; (4) `smoothScroll` 150ms ease-out ticker; (5) small md/theme/banner art changes. Order: fluidity → motion → art.

**Tech Stack:** Go 1.25, Bubble Tea v2.0.7 (cell-diff renderer, DEC 2026 auto), Lip Gloss v2.0.4, bubbles v2.1.0 viewport, `x/ansi`.

**Spec:** `docs/superpowers/specs/2026-08-06-tui-render-animation-design.md` (P1.5; three review fixes applied).
**Out of this plan:** P2 Tasks, P3 statusline/palette registry, scrollback virtualization, shimmer unless the spike go/no-go passes.

---

## Opening 1: Transcript mutation inventory (required by spec §4.2)

All paths that change `m.transcript` and how the cache tracks them:

| Mutation | Location | Cache action |
|----------|----------|--------------|
| `appendTranscriptBlock` (commitLine etc.) | transcript.go:50 | wrapper `appendWrappedBlocks(prevLines, w)` on block-count growth |
| `setTranscriptBlock` (live reasoning) | transcript.go:66 | marks dirty (Task 3 change) → wrapper `rewrapBlock(idx)` |
| Direct write `m.transcript[m.toolStreamIdx]` (tool stream tail) | chat_tui.go:2202 | → `setLiveBlock` (Task 3 change) |
| Direct write `m.transcript[idx]` (collapseToolOutput) | chat_tui.go:2318/2331 | → `setLiveBlock` |
| Direct write `m.transcript[m.toolStreamIdx]` (tickToolRunning) | chat_tui.go:2452 | → `setLiveBlock` |
| `removeTranscriptBlock` | transcript.go:74 | → `removeWrappedBlock` (Task 3 change) |
| `truncateTranscriptBlocks` | transcript.go:83 | → `truncateWrappedBlocks` (Task 3 change) |
| `reflowTranscript` (width change) | transcript.go:214 | wrapper `rebuildWrappedLines` (full) |
| `clearTranscriptDisplay` | chat_tui.go:1826 | reset `wrappedLines`, `blockLineCounts`, `liveDirtyIdx` |
| Any other `transcriptDirty = true` writer (branch.go:142, clear_confirm.go:65, chat_tui.go:1401/2285/2399/2491/2531/2552/3779/4063) | — | wrapper fallback: empty `liveDirtyIdx` ⇒ full `rebuildWrappedLines` (rare, safe) |

## Opening 2: Test seams (required by spec §7)

- **Env:** tests use `t.Setenv("REASONIX_REDUCE_MOTION", "1")` / `"REASONIX_TUI_SCROLL_REPAINT"`; helpers read env per call.
- **Synthetic ticks:** smooth-scroll tests construct `smoothScrollTickMsg{now: ...}` directly (no wall-clock sleeps).
- **Nil-cmd gating:** `workingCmds()` returns `nil` spinner cmd when motion is off — tests assert nil, never execute ticks.
- **Panel hook:** `chatTUI.panelRenderHook func(string)` (nil in prod) lets tests count renders.
- **Viewport fixture:** tests needing scrolling build a real viewport: `vp := viewport.New(viewport.WithWidth(80)); vp.SetContent(...); vp.SetHeight(20)`.

## Global constraints

- TDD per task: failing test → run → implement → run → commit. One commit per task.
- Do not change Esc semantics, double-Esc 600ms, double-Ctrl+C 1500ms, completion Ctrl+P/N, draft preservation.
- i18n: key labels stay literal English; fmt changes touch all three locales in the same task.
- `View`, `computeStatusLineCount`, `bottomRows`, `hideComposer`, `transcriptHeight` stay in lockstep (panel cache read by all).
- Benchmarks are reports, not assertions; `go test ./internal/cli/ -count=1` must stay green after every task.
- Commit prefixes: `feat(cli):`, `test(cli):`, `style(cli):`, `docs:`.

## File map

| File | Responsibility |
|------|----------------|
| `internal/cli/motion.go` (new) | `motionEnabled`, `scrollRepaintEnabled`, `workingCmds`/`workingBatch` |
| `internal/cli/motion_test.go` (new) | env helpers + working batch gating |
| `internal/cli/chat_tui.go` | `scrollRepaint` field + wrapper branch; panel refresh; smooth-scroll wiring; `setLiveBlock` call sites; elapsed call sites; branding |
| `internal/cli/chat_tui_test.go` | ClearScreen test families rewritten; panel hook test |
| `internal/cli/transcript.go` | `wrapBlock`, cache ops, `setTranscriptBlock` dirty-mark, `setLiveBlock`, remove/truncate cache sync |
| `internal/cli/transcript_test.go` | cache property test |
| `internal/cli/bottom_panels.go` (new) | `bottomPanels` struct + `renderBottomPanels` + hook |
| `internal/cli/status_footer.go` | `formatElapsedFixed` |
| `internal/cli/status_footer_test.go` | elapsed tests |
| `internal/cli/smooth_scroll.go` (new) | state machine + tick + `startSmoothScroll` |
| `internal/cli/smooth_scroll_test.go` (new) | animation tests |
| `internal/cli/md.go` | `inTable` flag + neutral CodeSpan |
| `internal/cli/md_test.go` | table code span test |
| `internal/cli/style.go` | `muted()` helper |
| `internal/cli/diffview.go` | delete dead color constants |
| `internal/cli/diffview_test.go` | use theme slots |
| `internal/cli/color_discipline_test.go` (new) | go/ast SGR scanner |
| `internal/cli/bench_test.go` | keep; add `BenchmarkAppendBlock` |
| `internal/i18n/{i18n,messages_en,messages_zh,messages_zh_tw}.go` | elapsed fmt `%s` + comments |
| `README.md`, `README.zh-CN.md` | env vars doc |
| `cmd/spike-shimmer/main.go` (new, temporary) | shimmer A/B spike |

---

### Task 1: Motion + repaint env helpers

**Files:**
- Create: `internal/cli/motion.go`
- Test: `internal/cli/motion_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cli

import "testing"

func TestMotionEnvHelpers(t *testing.T) {
	t.Setenv("REASONIX_REDUCE_MOTION", "1")
	if !motionEnabled() {
		t.Fatal("motionEnabled should be true with REASONIX_REDUCE_MOTION=1")
	}
	t.Setenv("REASONIX_REDUCE_MOTION", "0")
	if motionEnabled() {
		t.Fatal("motionEnabled should be false with REASONIX_REDUCE_MOTION=0")
	}
	t.Setenv("REASONIX_REDUCE_MOTION", "")
	if motionEnabled() {
		t.Fatal("motionEnabled should be false when unset")
	}
	t.Setenv("REASONIX_TUI_SCROLL_REPAINT", "1")
	if !scrollRepaintEnabled() {
		t.Fatal("scrollRepaintEnabled should be true with REASONIX_TUI_SCROLL_REPAINT=1")
	}
}

func TestWorkingCmdsGatesSpinnerTick(t *testing.T) {
	m := newTestChatTUI()
	t.Setenv("REASONIX_REDUCE_MOTION", "1")
	if _, sp := m.workingCmds(); sp != nil {
		t.Fatal("spinner tick must be suppressed when reduced motion is on")
	}
	t.Setenv("REASONIX_REDUCE_MOTION", "0")
	if _, sp := m.workingCmds(); sp == nil {
		t.Fatal("spinner tick must be scheduled when motion is on")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/ -run 'TestMotionEnvHelpers|TestWorkingCmdsGatesSpinnerTick' -v
```

Expected: FAIL — undefined `motionEnabled`/`scrollRepaintEnabled`/`workingCmds`.

- [ ] **Step 3: Implement**

`internal/cli/motion.go`:

```go
package cli

import (
	"os"
	"strings"

	"charm.land/bubbletea/v2"
)

// motionEnabled reports whether decorative animation is enabled. Reduced motion
// (REASONIX_REDUCE_MOTION=1) disables spinner motion, smooth scroll, and any
// shimmer. Read on every call so tests observe the current environment.
func motionEnabled() bool {
	v := strings.TrimSpace(os.Getenv("REASONIX_REDUCE_MOTION"))
	return v == "" || v == "0"
}

// scrollRepaintEnabled reports whether the legacy full-screen repaint on every
// viewport scroll is requested (REASONIX_TUI_SCROLL_REPAINT=1).
func scrollRepaintEnabled() bool {
	v := strings.TrimSpace(os.Getenv("REASONIX_TUI_SCROLL_REPAINT"))
	return v != "" && v != "0"
}

// workingCmds returns the commands driving the running-state indicators: the
// elapsed ticker always runs (information), the spinner tick is decorative and
// is suppressed under reduced motion (returns nil).
func (m chatTUI) workingCmds() (elapsedCmd, spinnerCmd tea.Cmd) {
	if !motionEnabled() {
		return elapsedTick(), nil
	}
	return elapsedTick(), m.spinner.Tick
}

// workingBatch wraps workingCmds for tea.Batch call sites.
func (m chatTUI) workingBatch() tea.Cmd {
	el, sp := m.workingCmds()
	if sp != nil {
		return tea.Batch(el, sp)
	}
	return el
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/cli/ -run 'TestMotionEnvHelpers|TestWorkingCmdsGatesSpinnerTick' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/motion.go internal/cli/motion_test.go
git commit -m "feat(cli): reduced-motion and scroll-repaint env helpers"
```

---

### Task 2: Scroll repaint default off

**Files:**
- Modify: `internal/cli/chat_tui.go` (struct fields ~211; `newChatTUI` ~556; Update wrapper ~859)
- Modify: `internal/cli/chat_tui_test.go` (~2917/2921 test families)

- [ ] **Step 1: Write the failing test (rewrite the two ClearScreen families)**

Replace `TestRegularForceGotoBottomScrollJumpRequestsClearScreen` and the trailing assertions of `TestSessionSwitchSuppressesOneClearScreen` (chat_tui_test.go) with:

```go
func TestRegularForceGotoBottomScrollJumpNoClearScreenByDefault(t *testing.T) {
	ctrl := control.New(control.Options{})
	ch := make(chan event.Event, 1)
	adv := func(m chatTUI, msg tea.Msg) (chatTUI, tea.Cmd) {
		n, cmd := m.Update(msg)
		return n.(chatTUI), cmd
	}
	next := func(m chatTUI, msg tea.Msg) chatTUI {
		n, _ := adv(m, msg)
		return n
	}

	cur := next(newChatTUI(ctrl, "", ch, 80), tea.WindowSizeMsg{Width: 80, Height: 8})
	for i := 0; i < 12; i++ {
		cur = next(cur, agentEventMsg(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "line"}))
	}
	cur = next(cur, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if cur.viewport.AtBottom() {
		t.Fatal("wheel-up should break the bottom pin")
	}
	cur.forceGotoBottom = true
	cur.transcriptDirty = false
	cur, cmd := adv(cur, tea.WindowSizeMsg{Width: 80, Height: 8})

	if !cur.viewport.AtBottom() {
		t.Fatalf("forceGotoBottom should scroll without transcript changes, YOffset=%d", cur.viewport.YOffset())
	}
	if cur.forceGotoBottom {
		t.Fatal("forceGotoBottom should be cleared after scrolling")
	}
	if cmd != nil {
		t.Fatal("default scroll jumps must not request ClearScreen")
	}
}

func TestScrollRepaintEnvRestoresClearScreen(t *testing.T) {
	ctrl := control.New(control.Options{})
	ch := make(chan event.Event, 1)
	adv := func(m chatTUI, msg tea.Msg) (chatTUI, tea.Cmd) {
		n, cmd := m.Update(msg)
		return n.(chatTUI), cmd
	}
	next := func(m chatTUI, msg tea.Msg) chatTUI {
		n, _ := adv(m, msg)
		return n
	}

	cur := next(newChatTUI(ctrl, "", ch, 80), tea.WindowSizeMsg{Width: 80, Height: 8})
	for i := 0; i < 12; i++ {
		cur = next(cur, agentEventMsg(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "line"}))
	}
	cur = next(cur, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	cur.scrollRepaint = true
	cur.forceGotoBottom = true
	cur.transcriptDirty = false
	cur, cmd := adv(cur, tea.WindowSizeMsg{Width: 80, Height: 8})

	if cmd == nil {
		t.Fatal("legacy repaint mode must still request ClearScreen on scroll jumps")
	}
	if !cur.viewport.AtBottom() {
		t.Fatal("legacy repaint mode should still land at bottom")
	}
}
```

Keep `TestSessionSwitchSuppressesOneClearScreen` but change its final assertion block ("later scroll jumps must still request ClearScreen") to assert `cmd == nil` (default off) and `sessionSwitch` stays false:

```go
	cur = next(cur, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	cur.forceGotoBottom = true
	cur, cmd = adv(cur, tea.WindowSizeMsg{Width: 80, Height: 8})
	if cmd != nil {
		t.Fatal("later scroll jumps must not request ClearScreen by default")
	}
	if cur.sessionSwitch {
		t.Fatal("sessionSwitch should remain false after the suppressed cycle")
	}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestRegularForceGotoBottomScrollJumpNoClearScreenByDefault|TestScrollRepaintEnvRestoresClearScreen|TestSessionSwitchSuppressesOneClearScreen' -v
```

Expected: FAIL — wrapper still emits ClearScreen unconditionally.

- [ ] **Step 3: Implement**

1. Add field next to `sessionSwitch` (chat_tui.go ~211):

```go
	// scrollRepaint restores the legacy full-screen repaint on every viewport
	// scroll (REASONIX_TUI_SCROLL_REPAINT=1) for terminals that strand stale
	// rows under the cell-diff renderer.
	scrollRepaint bool
```

2. In `newChatTUI`, add to the struct literal:

```go
		scrollRepaint:        scrollRepaintEnabled(),
```

3. In the Update wrapper, change the ClearScreen branch to:

```go
	if cm.viewport.YOffset() != prevYOff && !cm.nativeScrollback && !cm.sessionSwitch && cm.scrollRepaint {
		return cm, tea.Batch(tea.ClearScreen, cmd)
	}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestRegularForceGotoBottomScrollJumpNoClearScreenByDefault|TestScrollRepaintEnvRestoresClearScreen|TestSessionSwitchSuppressesOneClearScreen' -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/chat_tui_test.go
git commit -m "feat(cli): no ClearScreen on scroll by default; env opt-in legacy repaint"
```

---

### Task 3: Incremental wrapped-line cache

**Files:**
- Modify: `internal/cli/transcript.go`
- Modify: `internal/cli/chat_tui.go` (fields, Update wrapper, 4 direct-mutation sites, `clearTranscriptDisplay`)
- Test: `internal/cli/transcript_test.go`
- Modify: `internal/cli/bench_test.go`

- [ ] **Step 1: Write the failing property test**

Add to `internal/cli/transcript_test.go`:

```go
package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// mixedBlocks mirrors real transcript content: ANSI-dim tool lines, accent
// tool cards, CJK user bubbles, and plain assistant text.
func mixedBlocks() []string {
	return []string{
		"\x1b[2m  ⎿  " + strings.Repeat("tool output line", 4) + "\x1b[0m",
		"\x1b[38;5;173m● Tool(verb)\x1b[0m",
		"普通文本行 1：中文内容用于宽度与换行测试",
		"\x1b[38;5;245m· \x1b[0m\x1b[38;5;252m" + strings.Repeat("assistant text ", 8) + "\x1b[0m",
		"",
	}
}

func fullWrap(blocks []string, width int) []string {
	return strings.Split(wrapTranscript(strings.Join(blocks, "\n"), width), "\n")
}

func TestWrappedCacheEqualsFullWrapAfterMutations(t *testing.T) {
	const width = 80
	m := newTestChatTUI()
	blocks := mixedBlocks()
	for _, b := range blocks {
		m.appendTranscriptBlock(b, transcriptSource{kind: transcriptSourceFixed})
	}
	m.appendWrappedBlocks(0, width)
	if !reflect.DeepEqual(m.wrappedLines, fullWrap(blocks, width)) {
		t.Fatalf("initial cache mismatch:\n%q\nvs\n%q", m.wrappedLines, fullWrap(blocks, width))
	}

	// Live update of the LAST block (streaming hot path).
	m.setLiveBlock(len(m.transcript)-1, "\x1b[2m  ⎿  new tail line\x1b[0m")
	m.rewrapBlock(len(m.transcript)-1, width)
	blocks[len(blocks)-1] = "\x1b[2m  ⎿  new tail line\x1b[0m"
	if !reflect.DeepEqual(m.wrappedLines, fullWrap(blocks, width)) {
		t.Fatalf("last-block rewrite mismatch")
	}

	// Live update of a MIDDLE block (late tool progress).
	m.setLiveBlock(1, "\x1b[38;5;173m● Tool(verb)\x1b[0m\x1b[0m")
	m.rewrapBlock(1, width)
	blocks[1] = "\x1b[38;5;173m● Tool(verb)\x1b[0m\x1b[0m"
	if !reflect.DeepEqual(m.wrappedLines, fullWrap(blocks, width)) {
		t.Fatalf("middle-block rewrite mismatch")
	}

	// Append two more blocks.
	extra := []string{"extra a", "extra b"}
	for _, b := range extra {
		m.appendTranscriptBlock(b, transcriptSource{kind: transcriptSourceFixed})
	}
	m.appendWrappedBlocks(len(blocks), width)
	blocks = append(blocks, extra...)
	if !reflect.DeepEqual(m.wrappedLines, fullWrap(blocks, width)) {
		t.Fatalf("append mismatch")
	}

	// Truncate to 3 blocks.
	m.truncateWrappedBlocks(3)
	blocks = blocks[:3]
	if !reflect.DeepEqual(m.wrappedLines, fullWrap(blocks, width)) {
		t.Fatalf("truncate mismatch")
	}

	// Remove block 1.
	m.removeWrappedBlock(1)
	blocks = append(blocks[:1], blocks[2:]...)
	if !reflect.DeepEqual(m.wrappedLines, fullWrap(blocks, width)) {
		t.Fatalf("remove mismatch")
	}

	// Rebuild from scratch (width change path).
	m.rebuildWrappedLines(60)
	if !reflect.DeepEqual(m.wrappedLines, fullWrap(blocks, 60)) {
		t.Fatalf("rebuild mismatch")
	}
	// ANSI safety: every wrapped line must carry balanced SGR (no dangling dim).
	for _, ln := range m.wrappedLines {
		if strings.Contains(ln, "\x1b[2m") && !strings.Contains(ln, "\x1b[0m") {
			t.Fatalf("dangling SGR in %q", ln)
		}
	}
}

func TestWrapBlockEquivalence(t *testing.T) {
	blocks := mixedBlocks()
	for _, width := range []int{20, 40, 80} {
		var joined []string
		for _, b := range blocks {
			joined = append(joined, wrapBlock(b, width)...)
		}
		if want := fullWrap(blocks, width); !reflect.DeepEqual(joined, want) {
			t.Fatalf("width %d: per-block wrap != full wrap:\n%q\nvs\n%q", width, joined, want)
		}
	}
	// ANSI must survive wrapping (the reason lipgloss width render is used).
	if got := ansi.Strip(wrapBlock("\x1b[2mdimmed\x1b[0m", 80)[0]); got != "dimmed" {
		t.Fatalf("ANSI stripped wrong: %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestWrappedCacheEqualsFullWrapAfterMutations|TestWrapBlockEquivalence' -v
```

Expected: FAIL — `wrapBlock`, `appendWrappedBlocks`, `rewrapBlock`, `setLiveBlock`, `truncateWrappedBlocks`, `removeWrappedBlock`, `rebuildWrappedLines`, `blockLineCounts` undefined.

- [ ] **Step 3: Implement**

1. `internal/cli/transcript.go` — add cache ops after `wrapTranscript`:

```go
// wrapBlock wraps one SGR-balanced transcript block to width, returning its
// visual lines. Equivalent to wrapping the block as part of the joined
// transcript (lipgloss width render wraps each line independently).
func wrapBlock(rendered string, width int) []string {
	if width <= 0 {
		width = 1
	}
	if rendered == "" {
		return []string{""}
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(rendered), "\n")
}

// appendWrappedBlocks wraps transcript blocks [from, len) into the wrappedLines
// cache. If the cache was cleared while blocks remain, rebuilds from 0.
func (m *chatTUI) appendWrappedBlocks(from, width int) {
	if len(m.wrappedLines) == 0 && from > 0 {
		m.rebuildWrappedLines(width)
		return
	}
	for i := from; i < len(m.transcript); i++ {
		lines := wrapBlock(m.transcript[i], width)
		m.wrappedLines = append(m.wrappedLines, lines...)
		m.blockLineCounts = append(m.blockLineCounts, len(lines))
	}
}

// rewrapBlock re-wraps block i in place. O(block) for the last block (common
// streaming case); O(nBlocks) prefix scan for rare middle-block updates.
func (m *chatTUI) rewrapBlock(i, width int) {
	if i < 0 || i >= len(m.transcript) {
		return
	}
	start := 0
	for j := 0; j < i; j++ {
		start += m.blockLineCounts[j]
	}
	old := m.blockLineCounts[i]
	lines := wrapBlock(m.transcript[i], width)
	if len(lines) == old {
		copy(m.wrappedLines[start:], lines)
		return
	}
	m.wrappedLines = append(m.wrappedLines[:start], append(lines, m.wrappedLines[start+old:]...)...)
	m.blockLineCounts[i] = len(lines)
}

// rebuildWrappedLines rebuilds the whole cache from the current transcript
// (width-change / reflow path).
func (m *chatTUI) rebuildWrappedLines(width int) {
	m.wrappedLines = m.wrappedLines[:0]
	m.blockLineCounts = m.blockLineCounts[:0]
	m.appendWrappedBlocks(0, width)
}

// truncateWrappedBlocks drops the cache to the first length blocks.
func (m *chatTUI) truncateWrappedBlocks(length int) {
	if length >= len(m.blockLineCounts) {
		return
	}
	end := 0
	for j := 0; j < length; j++ {
		end += m.blockLineCounts[j]
	}
	m.wrappedLines = m.wrappedLines[:end]
	m.blockLineCounts = m.blockLineCounts[:length]
}

// removeWrappedBlock removes block i's lines from the cache.
func (m *chatTUI) removeWrappedBlock(i int) {
	if i < 0 || i >= len(m.blockLineCounts) {
		return
	}
	start := 0
	for j := 0; j < i; j++ {
		start += m.blockLineCounts[j]
	}
	n := m.blockLineCounts[i]
	m.wrappedLines = append(m.wrappedLines[:start], m.wrappedLines[start+n:]...)
	m.blockLineCounts = append(m.blockLineCounts[:i], m.blockLineCounts[i+1:]...)
}

// setLiveBlock replaces transcript block idx and marks it for re-wrapping on
// the next Update pass. This is the single setter for live tool/reasoning
// updates (historical direct m.transcript[idx] writers moved here).
func (m *chatTUI) setLiveBlock(idx int, rendered string) {
	if idx < 0 || idx >= len(m.transcript) {
		return
	}
	m.transcript[idx] = rendered
	m.liveDirtyIdx = append(m.liveDirtyIdx, idx)
	m.transcriptDirty = true
}
```

2. `internal/cli/chat_tui.go` — fields next to `wrappedLines` (line ~211):

```go
	wrappedLines      []string // transcript wrapped to viewport width (rendered each frame)
	blockLineCounts   []int    // wrapped line count per transcript block
	liveDirtyIdx      []int    // blocks mutated this Update, re-wrapped by the wrapper
```

3. `internal/cli/transcript.go` — `setTranscriptBlock` gains dirty marking:

```go
func (m *chatTUI) setTranscriptBlock(index int, rendered string, source transcriptSource) {
	if index < 0 || index >= len(m.transcript) {
		return
	}
	m.ensureTranscriptSources()
	m.transcript[index] = rendered
	m.transcriptSources[index] = source
	m.liveDirtyIdx = append(m.liveDirtyIdx, index)
	m.transcriptDirty = true
}
```

4. `internal/cli/transcript.go` — `removeTranscriptBlock` / `truncateTranscriptBlocks` sync the cache:

```go
func (m *chatTUI) removeTranscriptBlock(index int) {
	if index < 0 || index >= len(m.transcript) {
		return
	}
	m.ensureTranscriptSources()
	m.transcript = append(m.transcript[:index], m.transcript[index+1:]...)
	m.transcriptSources = append(m.transcriptSources[:index], m.transcriptSources[index+1:]...)
	m.removeWrappedBlock(index)
}

func (m *chatTUI) truncateTranscriptBlocks(length int) {
	length = min(max(length, 0), len(m.transcript))
	m.ensureTranscriptSources()
	m.transcript = m.transcript[:length]
	m.transcriptSources = m.transcriptSources[:length]
	m.truncateWrappedBlocks(length)
}
```

5. `internal/cli/chat_tui.go` — the 4 direct-mutation sites become `setLiveBlock`:

- ~2202 (tool stream tail): `m.transcript[m.toolStreamIdx] = connectorBlock(lines)` followed by `m.transcriptDirty = true` → replace both with `m.setLiveBlock(m.toolStreamIdx, connectorBlock(lines))`.
- ~2318 (no output): `m.transcript[idx] = ""` → `m.setLiveBlock(idx, "")`.
- ~2331 (preview): `m.transcript[idx] = connectorBlock(preview)` → `m.setLiveBlock(idx, connectorBlock(preview))` (and the else branch's `m.transcript[idx] = connectorBlock(rendered)` likewise).
- ~2452 (`tickToolRunning`): `m.transcript[m.toolStreamIdx] = connectorBlock(...)` + `m.transcriptDirty = true` → `m.setLiveBlock(m.toolStreamIdx, connectorBlock(...))`.

6. `internal/cli/chat_tui.go` — `clearTranscriptDisplay` (line ~1833) adds resets:

```go
	m.wrappedLines = nil
	m.blockLineCounts = nil
	m.liveDirtyIdx = nil
```

7. `internal/cli/chat_tui.go` — Update wrapper: replace the re-feed block (currently ~line 838) with:

```go
	if cm.width != prevWidth {
		cm.reflowTranscript(cm.width)
		cm.rebuildWrappedLines(contentW)
		// Selection coordinates are visual-line based and cannot survive a
		// semantic reflow without selecting unrelated text.
		cm.sel = selection{}
	} else if len(cm.transcript) != prevLines {
		cm.appendWrappedBlocks(prevLines, contentW)
	} else if cm.transcriptDirty {
		if len(cm.liveDirtyIdx) == 0 {
			cm.rebuildWrappedLines(contentW) // dirty without a tracked block: full rebuild
		} else {
			for _, idx := range cm.liveDirtyIdx {
				cm.rewrapBlock(idx, contentW)
			}
		}
	}
	cm.liveDirtyIdx = cm.liveDirtyIdx[:0]
	if cm.width != prevWidth || len(cm.transcript) != prevLines || cm.transcriptDirty {
		cm.viewport.SetContent(strings.Join(cm.wrappedLines, "\n"))
		if wasAtBottom {
			cm.viewport.GotoBottom() // tail-follow: stay pinned to newest output
		} else if cm.width != prevWidth && resizeAnchor.valid {
			cm.viewport.SetYOffset(resizeAnchor.yOffset(cm.transcript, contentW))
		}
	}
	if cm.forceGotoBottom {
		cm.viewport.GotoBottom()
		cm.forceGotoBottom = false
	}
	cm.transcriptDirty = false
```

(Remove the old `wrapped := wrapTranscript(...)` / `cm.wrappedLines = strings.Split(...)` lines.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestWrappedCacheEqualsFullWrapAfterMutations|TestWrapBlockEquivalence' -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS.

- [ ] **Step 5: Add `BenchmarkAppendBlock` to `internal/cli/bench_test.go`**

```go
func BenchmarkAppendBlock(b *testing.B) {
	base := benchTranscriptContent(10000)
	m := newTestChatTUI()
	m.appendTranscriptBlock(base, transcriptSource{kind: transcriptSourceFixed})
	m.appendWrappedBlocks(0, 120)
	newBlock := benchTranscriptContent(1)
	for i := 0; i < b.N; i++ {
		m.appendTranscriptBlock(newBlock, transcriptSource{kind: transcriptSourceFixed})
		from := len(m.blockLineCounts) - 1
		m.appendWrappedBlocks(from, 120)
	}
}
```

Run and record numbers (report only):

```bash
go test ./internal/cli/ -run '^$' -bench 'BenchmarkAppendBlock' -benchmem -count=1
go test ./internal/cli/ -run '^$' -bench 'BenchmarkWrapTranscript/lines=10000' -benchmem -count=1
```

Expected: append-block is orders of magnitude below the 10k-line full wrap (~58ms).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/transcript.go internal/cli/chat_tui.go internal/cli/transcript_test.go internal/cli/bench_test.go
git commit -m "feat(cli): incremental per-block transcript wrap cache"
```

---

### Task 4: Bottom panels single pass

**Files:**
- Create: `internal/cli/bottom_panels.go`
- Modify: `internal/cli/chat_tui.go` (fields, Update wrapper, `bottomRows`, `View`)
- Test: `internal/cli/chat_tui_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/chat_tui_test.go`:

```go
func TestBottomPanelsRenderOncePerEvent(t *testing.T) {
	ctrl := control.New(control.Options{})
	ch := make(chan event.Event, 1)
	m := newChatTUI(ctrl, "", ch, 80)
	m.panelRenderHook = func(name string) { panelRenderCounts[name]++ }
	next := func(msg tea.Msg) chatTUI {
		n, _ := m.Update(msg)
		return n.(chatTUI)
	}
	m = next(tea.WindowSizeMsg{Width: 80, Height: 8})
	for i := 0; i < 3; i++ {
		m = next(agentEventMsg(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "line"}))
	}
	// 1 resize + 3 notices = exactly 4 renders per panel, no more.
	for name, n := range panelRenderCounts {
		if n != 4 {
			t.Fatalf("panel %q rendered %d times across 4 events, want exactly 4", name, n)
		}
	}
	if m.panels.rows != m.bottomRows() {
		t.Fatalf("cached rows %d != bottomRows() %d", m.panels.rows, m.bottomRows())
	}
	if got := m.renderCheatsheet(); got != "" && !strings.Contains(m.View(), got) {
		t.Fatal("View should render the cached cheatsheet")
	}
}

func TestBottomPanelsFallbackWhenInvalid(t *testing.T) {
	m := newTestChatTUI()
	m.panelsValid = false
	rows := m.bottomRows()
	if rows < 0 {
		t.Fatalf("fallback bottomRows should render on demand, got %d", rows)
	}
}
```

Add the shared counter at package scope in `chat_tui_test.go`:

```go
var panelRenderCounts = map[string]int{}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestBottomPanelsRenderOncePerEvent|TestBottomPanelsFallbackWhenInvalid' -v
```

Expected: FAIL — `panelRenderHook`, `panels`, `panelsValid`, `renderBottomPanels` undefined.

- [ ] **Step 3: Implement**

1. `internal/cli/bottom_panels.go`:

```go
package cli

import "strings"

// bottomPanels holds the rendered bottom region so bottomRows() and View()
// share one render pass per event. Refreshed in the Update wrapper.
type bottomPanels struct {
	todo, banner, chooser, rewind, mcpImport, resumePick, quickPick, copyPick, cheatsheet, completion, manager, managerFooter string
	rows int
}

// renderBottomPanels renders every bottom panel once. panelRenderHook (nil in
// production) lets tests count renders per panel name.
func (m chatTUI) renderBottomPanels() bottomPanels {
	var p bottomPanels
	hook := func(name string, s string) string {
		if m.panelRenderHook != nil {
			m.panelRenderHook(name)
		}
		return s
	}
	p.todo = hook("todo", m.renderTodoPanel())
	p.banner = hook("banner", m.renderApprovalBanner())
	p.chooser = hook("chooser", m.renderChooser())
	p.rewind = hook("rewind", m.renderRewind())
	p.mcpImport = hook("mcpImport", m.renderMCPImport())
	p.resumePick = hook("resumePick", m.renderResumePicker())
	p.quickPick = hook("quickPick", m.renderQuickPicker())
	p.copyPick = hook("copyPick", m.renderCopyPicker())
	p.cheatsheet = hook("cheatsheet", m.renderCheatsheet())
	p.completion = hook("completion", m.renderCompletion())
	if m.nativeScrollback {
		p.manager = hook("manager", m.renderMainManager())
		p.managerFooter = hook("managerFooter", m.renderMainManagerFooter())
	}
	for _, s := range []string{p.todo, p.banner, p.chooser, p.rewind, p.mcpImport, p.resumePick, p.quickPick, p.copyPick, p.cheatsheet, p.completion, p.manager, p.managerFooter} {
		if s != "" {
			p.rows += strings.Count(s, "\n") + 1
		}
	}
	return p
}
```

2. `internal/cli/chat_tui.go` — fields:

```go
	panels           bottomPanels
	panelsValid      bool
	panelRenderHook  func(name string) // test seam; nil in production
```

3. `internal/cli/chat_tui.go` — Update wrapper, immediately after `next, cmd := m.update(msg)` and the `cm := next.(chatTUI)` line, insert:

```go
	// Render the bottom region once per event; bottomRows()/View() read it.
	cm.panels = cm.renderBottomPanels()
	cm.panelsValid = true
```

4. `internal/cli/chat_tui.go` — `bottomRows()` (line ~1917) becomes:

```go
func (m chatTUI) bottomRows() int {
	if m.panelsValid {
		return m.panels.rows
	}
	return m.renderBottomPanels().rows
}
```

5. `internal/cli/chat_tui.go` — `View()` panel section (~2870): replace each `if card := m.renderX(); card != ""` block with reads from a local cache copy:

```go
	panels := m.panels
	if !m.panelsValid {
		panels = m.renderBottomPanels()
	}
	var parts []string
	rowsAboveBox := 0 // terminal rows occupied by panels/working line before the composer
	if panels.todo != "" {
		parts = append(parts, panels.todo)
		rowsAboveBox += strings.Count(panels.todo, "\n") + 1
	}
	if panels.banner != "" {
		parts = append(parts, panels.banner)
		rowsAboveBox += strings.Count(panels.banner, "\n") + 1
	}
	if panels.chooser != "" {
		parts = append(parts, panels.chooser)
		rowsAboveBox += strings.Count(panels.chooser, "\n") + 1
	}
	if panels.rewind != "" {
		parts = append(parts, panels.rewind)
		rowsAboveBox += strings.Count(panels.rewind, "\n") + 1
	}
	if panels.mcpImport != "" {
		parts = append(parts, panels.mcpImport)
		rowsAboveBox += strings.Count(panels.mcpImport, "\n") + 1
	}
	if panels.resumePick != "" {
		parts = append(parts, panels.resumePick)
		rowsAboveBox += strings.Count(panels.resumePick, "\n") + 1
	}
	if panels.quickPick != "" {
		parts = append(parts, panels.quickPick)
		rowsAboveBox += strings.Count(panels.quickPick, "\n") + 1
	}
	if panels.copyPick != "" {
		parts = append(parts, panels.copyPick)
		rowsAboveBox += strings.Count(panels.copyPick, "\n") + 1
	}
	if panels.cheatsheet != "" {
		parts = append(parts, panels.cheatsheet)
		rowsAboveBox += strings.Count(panels.cheatsheet, "\n") + 1
	}
	if panels.completion != "" {
		parts = append(parts, panels.completion)
		rowsAboveBox += strings.Count(panels.completion, "\n") + 1
	}
	if m.nativeScrollback && panels.manager != "" {
		parts = append(parts, panels.manager)
		rowsAboveBox += strings.Count(panels.manager, "\n") + 1
	}
```

Also replace the `renderMainManagerFooter()` call in `View` (line ~2950) with `panels.managerFooter` (fallback when `!panelsValid` is covered by the local cache copy above).

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestBottomPanelsRenderOncePerEvent|TestBottomPanelsFallbackWhenInvalid' -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/bottom_panels.go internal/cli/chat_tui.go internal/cli/chat_tui_test.go
git commit -m "feat(cli): single-pass bottom panel cache"
```

---

### Task 5: Fixed-width elapsed

**Files:**
- Modify: `internal/cli/status_footer.go`
- Modify: `internal/cli/chat_tui.go` (6 call sites)
- Modify: `internal/i18n/{i18n,messages_en,messages_zh,messages_zh_tw}.go`
- Test: `internal/cli/status_footer_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/status_footer_test.go`:

```go
func TestFormatElapsedFixed(t *testing.T) {
	for _, tc := range []struct{ sec, want string }{
		{0, "  0"}, {3, "  3"}, {12, " 12"}, {123, "123"}, {999, "999"}, {1000, "999"}, {9999, "999"},
	} {
		if got := formatElapsedFixed(tc.sec); got != tc.want {
			t.Fatalf("formatElapsedFixed(%d) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

func TestWorkingLineElapsedStableWidth(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiRunning
	m.elapsed = 3
	line3 := m.runningWorkingLine(false, false)
	m.elapsed = 12
	line12 := m.runningWorkingLine(false, false)
	// The time segment keeps a stable 4-column width ("  3s" vs " 12s").
	if strings.Contains(line3, " 3s") && strings.Contains(line12, " 3s") {
		t.Fatalf("elapsed width must be fixed: %q vs %q", line3, line12)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestFormatElapsedFixed|TestWorkingLineElapsedStableWidth' -v
```

Expected: FAIL — `formatElapsedFixed` undefined.

- [ ] **Step 3: Implement**

1. `internal/cli/status_footer.go`:

```go
// formatElapsedFixed renders elapsed seconds right-aligned to a stable 3-column
// numeric width (>=1000 clamps to 999); locale fmt strings add the unit so the
// display is a fixed 4 columns and never jitters.
func formatElapsedFixed(sec int) string {
	if sec >= 999 {
		sec = 999
	}
	return fmt.Sprintf("%3d", sec)
}
```

2. `internal/cli/chat_tui.go` — the six call sites (pass `formatElapsedFixed(...)`; fmt strings become `%s`):

- `tickToolRunning` (~2452): `fmt.Sprintf(i18n.M.ChatToolWorkingFmt, frame, formatElapsedFixed(secs))`
- tool stream tail (~2431, the other `ChatToolWorkingFmt` site): same change
- working line `ChatStatusCancellingFmt` (2823): `fmt.Sprintf("  "+i18n.M.ChatStatusCancellingFmt, m.spinner.View(), formatElapsedFixed(m.elapsed))`
- working line `ChatStatusThinkingFmt` (2825): same
- `ChatThoughtForFmt` (2465, 2480): `fmt.Sprintf("  ▎ "+i18n.M.ChatThoughtForFmt, formatElapsedFixed(secs))`

3. `internal/i18n/messages_en.go` (lines ~44–48):

```go
	ChatThoughtForFmt:                      "thought for %ss",
	ChatStatusThinkingFmt:                  "%s thinking… (%ss · Esc cancels)",
	ChatStatusRetryingFmt:                  "%s retrying (%d/%d)… (Esc cancels)", // unchanged
	ChatStatusCancellingFmt:                "%s stopping… (%ss · Ctrl+C exits)",
	ChatToolWorkingFmt:                     "%s working · %ss",
```

4. `internal/i18n/messages_zh.go` (lines ~45–48 + Cancelling):

```go
	ChatThoughtForFmt:                      "思考了 %s 秒",
	ChatStatusThinkingFmt:                  "%s 思考中… (%s 秒 · Esc 取消)",
	ChatStatusRetryingFmt:                  "%s 正在重试 (%d/%d)… (Esc 取消)", // unchanged
	ChatStatusCancellingFmt:                "%s 正在停止… (%s 秒 · Ctrl+C 退出)",
	ChatToolWorkingFmt:                     "%s 运行中 · %s 秒",
```

(Adjust `ChatStatusCancellingFmt`'s zh text to the actual existing wording — change only the `%d 秒` → `%s 秒` verb, never the label text.)

5. `internal/i18n/messages_zh_tw.go` (lines ~41–48): same `%d`→`%s` verb changes for `ChatThoughtForFmt`, `ChatStatusThinkingFmt`, `ChatStatusCancellingFmt`, `ChatToolWorkingFmt`; `ChatStatusRetryingFmt` unchanged.

6. `internal/i18n/i18n.go` (lines ~69–72) comment updates:

```go
	ChatThoughtForFmt string // collapsed reasoning summary, "%s" = fixed-width elapsed seconds
	ChatStatusThinkingFmt string // "%s thinking… (%ss …)" — %s = spinner, %s = fixed-width elapsed
	ChatToolWorkingFmt string // "%s working · %ss" — %s = frame, %s = fixed-width elapsed
	ChatStatusRetryingFmt string // "%s retrying (%d/%d)…" — %s = spinner, %d/%d = attempt/max
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestFormatElapsedFixed|TestWorkingLineElapsedStableWidth' -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS (retry_indicator_test must still pass — RetryingFmt untouched).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/status_footer.go internal/cli/chat_tui.go internal/i18n/ internal/cli/status_footer_test.go
git commit -m "feat(cli): fixed-width elapsed counters"
```

---

### Task 6: Motion gate wiring (spinner sites + tool frames)

**Files:**
- Modify: `internal/cli/chat_tui.go` (spinner tick sites ~1506/3756; `tickToolRunning` ~2442)
- Test: `internal/cli/motion_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/motion_test.go`:

```go
func TestToolFramesFreezeUnderReducedMotion(t *testing.T) {
	m := newTestChatTUI()
	m.transcript = append(m.transcript, "")
	m.toolStreamIdx = 0
	t.Setenv("REASONIX_REDUCE_MOTION", "1")
	m.tickToolRunning()
	frozen := m.transcript[0]
	m.tickToolRunning()
	if m.transcript[0] != frozen {
		t.Fatal("tool working line must not advance frames under reduced motion")
	}
	t.Setenv("REASONIX_REDUCE_MOTION", "0")
	m.tickToolRunning()
	if m.transcript[0] == frozen {
		t.Fatal("tool working line should advance frames when motion is on")
	}
}

func TestWorkingBatchSuppressesSpinner(t *testing.T) {
	m := newTestChatTUI()
	t.Setenv("REASONIX_REDUCE_MOTION", "1")
	if got := m.workingBatch(); got == nil {
		t.Fatal("workingBatch must still return the elapsed ticker")
	}
	t.Setenv("REASONIX_REDUCE_MOTION", "0")
	if got := m.workingBatch(); got == nil {
		t.Fatal("workingBatch must return a batch with motion on")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestToolFramesFreezeUnderReducedMotion|TestWorkingBatchSuppressesSpinner' -v
```

Expected: FAIL — tool frames advance regardless of env; `workingBatch` undefined.

- [ ] **Step 3: Implement**

1. `internal/cli/chat_tui.go` — `tickToolRunning` (~2442): gate only the frame advance:

```go
	if motionEnabled() {
		m.toolStreamFrame++
	}
	frame := toolWorkingFrames[m.toolStreamFrame%len(toolWorkingFrames)]
```

(Keep the rest of the function — seconds still re-render each tick via `setLiveBlock` from Task 3, so elapsed stays live under reduced motion.)

2. `internal/cli/chat_tui.go` — replace the two spinner-tick sites (~1506, ~3756):

Old:
```go
return m, tea.Batch(m.spinner.Tick, elapsedTick())
```
New:
```go
return m, m.workingBatch()
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestToolFramesFreezeUnderReducedMotion|TestWorkingBatchSuppressesSpinner' -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/motion_test.go
git commit -m "feat(cli): reduced-motion gate for spinner and tool frames"
```

---

### Task 7: Smooth scroll interpolation

**Files:**
- Create: `internal/cli/smooth_scroll.go`
- Modify: `internal/cli/chat_tui.go` (update switch handler; PgUp/PgDn keys ~1140; wheel ~906)
- Test: `internal/cli/smooth_scroll_test.go`

- [ ] **Step 1: Write the failing test**

`internal/cli/smooth_scroll_test.go`:

```go
package cli

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
)

func scrollFixture() chatTUI {
	vp := viewport.New(viewport.WithWidth(80))
	var content string
	for i := 0; i < 200; i++ {
		content += "line\n"
	}
	vp.SetContent(content)
	vp.SetHeight(20)
	m := newTestChatTUI()
	m.viewport = vp
	m.scrollRepaint = false
	return m
}

func TestSmoothScrollStartsAndSnaps(t *testing.T) {
	upd := func(m chatTUI, msg tea.Msg) (chatTUI, tea.Cmd) {
		n, cmd := m.update(msg)
		return n.(chatTUI), cmd
	}
	m := scrollFixture()
	next, cmd := m.startSmoothScroll(50)
	if cmd == nil {
		t.Fatal("motion on should start a tick")
	}
	if next.smooth == nil {
		t.Fatal("smooth state should be active")
	}

	// Mid-flight tick.
	mid := time.Unix(0, 0)
	next.smooth.start = mid
	next2, cmd2 := upd(next, smoothScrollTickMsg{now: mid.Add(75 * time.Millisecond)})
	if cmd2 == nil {
		t.Fatal("mid-flight tick should re-arm")
	}
	off := next2.viewport.YOffset()
	if off <= 0 || off >= 50 {
		t.Fatalf("mid-flight offset %d should be strictly between 0 and 50", off)
	}

	// Final snap.
	next3, cmd3 := upd(next2, smoothScrollTickMsg{now: mid.Add(10 * time.Second)})
	if cmd3 != nil {
		t.Fatal("final tick should not re-arm")
	}
	if next3.smooth != nil {
		t.Fatal("smooth state should clear on arrival")
	}
	if next3.viewport.YOffset() != 50 {
		t.Fatalf("final offset = %d, want 50", next3.viewport.YOffset())
	}
}

func TestSmoothScrollInstantWhenMotionOff(t *testing.T) {
	t.Setenv("REASONIX_REDUCE_MOTION", "1")
	m := scrollFixture()
	next, cmd := m.startSmoothScroll(50)
	if cmd != nil {
		t.Fatal("motion off must jump instantly, no tick")
	}
	if next.smooth != nil {
		t.Fatal("no smooth state when motion off")
	}
	if next.viewport.YOffset() != 50 {
		t.Fatalf("offset = %d, want 50", next.viewport.YOffset())
	}
}

func TestSmoothScrollInstantUnderLegacyRepaint(t *testing.T) {
	m := scrollFixture()
	m.scrollRepaint = true
	next, cmd := m.startSmoothScroll(50)
	if cmd != nil {
		t.Fatal("legacy repaint mode must jump instantly")
	}
	if next.viewport.YOffset() != 50 {
		t.Fatalf("offset = %d, want 50", next.viewport.YOffset())
	}
}

func TestSmoothScrollInterruptRestartsFromCurrent(t *testing.T) {
	upd := func(m chatTUI, msg tea.Msg) (chatTUI, tea.Cmd) {
		n, cmd := m.update(msg)
		return n.(chatTUI), cmd
	}
	m := scrollFixture()
	next, _ := m.startSmoothScroll(50)
	mid := time.Unix(0, 0)
	next.smooth.start = mid
	next2, _ := upd(next, smoothScrollTickMsg{now: mid.Add(75 * time.Millisecond)})
	cur := next2.viewport.YOffset()
	next3, _ := next2.startSmoothScroll(100)
	if next3.smooth.from != cur {
		t.Fatalf("interrupt should restart from current offset %d, got %d", cur, next3.smooth.from)
	}
}

func TestSmoothScrollClampsTarget(t *testing.T) {
	upd := func(m chatTUI, msg tea.Msg) (chatTUI, tea.Cmd) {
		n, cmd := m.update(msg)
		return n.(chatTUI), cmd
	}
	m := scrollFixture()
	next, _ := m.startSmoothScroll(1_000_000)
	mid := time.Unix(0, 0)
	next.smooth.start = mid
	next2, _ := upd(next, smoothScrollTickMsg{now: mid.Add(10 * time.Second)})
	if next2.smooth != nil || next2.viewport.YOffset() >= 200 {
		t.Fatalf("target must clamp to content bounds, offset=%d", next2.viewport.YOffset())
	}
}
```

Note: `update` is the unexported message handler returning `tea.Model` — tests in the same package call it through the local `upd` helper with synthetic `smoothScrollTickMsg`, and pin `smooth.start` to a synthetic clock for determinism.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestSmoothScroll' -v
```

Expected: FAIL — `smoothScroll`, `smoothScrollTickMsg`, `startSmoothScroll` undefined.

- [ ] **Step 3: Implement**

1. `internal/cli/smooth_scroll.go`:

```go
package cli

import (
	"math"
	"time"

	"charm.land/bubbletea/v2"
)

const (
	smoothScrollDuration = 150 * time.Millisecond
	smoothScrollTickDur  = 16 * time.Millisecond
)

// smoothScroll is the PgUp/PgDn/wheel interpolation state; nil when idle.
type smoothScroll struct {
	from, to int
	start    time.Time
	dur      time.Duration
}

type smoothScrollTickMsg struct {
	now time.Time
}

func smoothScrollTick() tea.Cmd {
	return tea.Tick(smoothScrollTickDur, func(t time.Time) tea.Msg {
		return smoothScrollTickMsg{now: t}
	})
}

// startSmoothScroll animates the viewport offset to target (150ms ease-out
// cubic). Jumps instantly when reduced motion is on or the legacy repaint mode
// is active; interrupts an in-flight animation from its current offset.
func (m chatTUI) startSmoothScroll(target int) (chatTUI, tea.Cmd) {
	if !motionEnabled() || m.scrollRepaint {
		m.viewport.SetYOffset(target)
		return m, nil
	}
	from := m.viewport.YOffset()
	if from == target {
		return m, nil
	}
	m.smooth = &smoothScroll{from: from, to: target, start: time.Now(), dur: smoothScrollDuration}
	return m, smoothScrollTick()
}

// offsetAt returns the eased offset at time now; done=true when finished.
func (s *smoothScroll) offsetAt(now time.Time) (offset int, done bool) {
	if now.Before(s.start) {
		now = s.start
	}
	t := float64(now.Sub(s.start)) / float64(s.dur)
	if t >= 1 {
		return s.to, true
	}
	eased := 1 - math.Pow(1-t, 3)
	return s.from + int(float64(s.to-s.from)*eased), false
}
```

2. `internal/cli/chat_tui.go` — field next to `sessionSwitch`:

```go
	// smooth is the in-flight scroll interpolation (nil when idle).
	smooth *smoothScroll
```

3. `internal/cli/chat_tui.go` — add a `case smoothScrollTickMsg:` in the main update switch (next to the `spinner.TickMsg` case, ~1770):

```go
	case smoothScrollTickMsg:
		if m.smooth == nil {
			return m, nil
		}
		off, done := m.smooth.offsetAt(msg.now)
		m.viewport.SetYOffset(off)
		if done {
			m.smooth = nil
			return m, nil
		}
		cmds = append(cmds, smoothScrollTick())
```

4. `internal/cli/chat_tui.go` — PgUp/PgDn keys (~1140):

```go
		case "pgup":
			next, sc := m.startSmoothScroll(m.viewport.YOffset() - m.viewport.Height())
			return next, finalize(next, append(cmds, sc))
		case "pgdown":
			next, sc := m.startSmoothScroll(m.viewport.YOffset() + m.viewport.Height())
			return next, finalize(next, append(cmds, sc))
```

(`tea.Batch` ignores nil cmds, so `sc == nil` is safe.)

5. `internal/cli/chat_tui.go` — wheel (~906):

```go
		switch msg.Button {
		case tea.MouseWheelUp:
			next, sc := m.startSmoothScroll(m.viewport.YOffset() - 3)
			return next, sc
		case tea.MouseWheelDown:
			next, sc := m.startSmoothScroll(m.viewport.YOffset() + 3)
			return next, sc
		}
```

(Keep the earlier composer-internal wheel handling untouched; this branch is the transcript fall-through.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestSmoothScroll' -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/smooth_scroll.go internal/cli/chat_tui.go internal/cli/smooth_scroll_test.go
git commit -m "feat(cli): smooth scroll interpolation with reduced-motion gate"
```

---

### Task 8: Width-gated first-screen branding

**Files:**
- Modify: `internal/cli/chat_tui.go` (`renderTUIBanner`, ~4696)
- Test: `internal/cli/chat_render_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/chat_render_test.go`:

```go
func TestRenderTUIBannerWideAndNarrow(t *testing.T) {
	wide := renderTUIBanner("model-x", "", 120)
	if strings.Count(wide, "\n") < 1 {
		t.Fatalf("wide banner should keep the tip line, got %q", wide)
	}
	if !strings.Contains(wide, i18n.M.ChatTip) {
		t.Fatalf("wide banner should contain the tip, got %q", wide)
	}
	narrow := renderTUIBanner("model-x", "", 40)
	if strings.Count(narrow, "\n") != 0 {
		t.Fatalf("narrow banner must be a single line, got %q", narrow)
	}
	if strings.Contains(narrow, i18n.M.ChatTip) {
		t.Fatalf("narrow banner must not contain the tip, got %q", narrow)
	}
	if !strings.Contains(narrow, "reasonix") {
		t.Fatalf("narrow banner should keep the wordmark, got %q", narrow)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run TestRenderTUIBannerWideAndNarrow -v
```

Expected: FAIL — narrow banner still renders two lines.

- [ ] **Step 3: Implement**

Replace `renderTUIBanner` (chat_tui.go ~4696):

```go
func renderTUIBanner(label, missing string, width int) string {
	var b strings.Builder
	if width >= 60 {
		b.WriteString(accent("◆") + " " + bold("reasonix") + "  " + dim("· "+label) + "\n")
		b.WriteString(dim("  "+i18n.M.ChatTip) + "\n")
	} else {
		line := accent("◆") + " " + bold("reasonix") + " " + dim("· "+label)
		b.WriteString(ansi.Truncate(line, width, "…"))
	}
	if missing != "" {
		b.WriteString(wrapForViewport("  ! "+missing, width, activeCLITheme.warn) + "\n")
	}
	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run TestRenderTUIBannerWideAndNarrow -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/chat_render_test.go
git commit -m "feat(cli): width-gated first-screen branding"
```

---

### Task 9: Density audit + regression test

**Files:**
- Modify: `internal/cli/transcript_test.go` (new test) — fixes, if any, land in `md.go`/`toolcard.go`/`chat_tui.go`
- Test: `internal/cli/transcript_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestTranscriptNeverDoubleBlanks(t *testing.T) {
	m := newTestChatTUI()
	m.commitLine("user bubble")
	m.commitSpacer()
	m.commitLine(dim("  ▎ thinking…"))
	m.commitSpacer()
	m.commitLine("  ⎿  tool line")
	m.commitSpacer()
	m.commitLine("assistant answer")
	m.commitSpacer()
	m.commitLine("receipt")
	joined := strings.Join(m.transcript, "\n")
	if strings.Contains(joined, "\n\n\n") {
		t.Fatalf("double blank line detected:\n%q", joined)
	}
	if strings.Count(joined, "\n\n") != 4 {
		t.Fatalf("want exactly one blank line between the 5 blocks, got:\n%q", joined)
	}
}

func TestCommitSpacerNeverDoubleSpaces(t *testing.T) {
	m := newTestChatTUI()
	m.commitLine("a")
	m.commitSpacer()
	m.commitSpacer() // second spacer must be a no-op
	m.commitLine("b")
	if strings.Contains(strings.Join(m.transcript, "\n"), "\n\n\n") {
		t.Fatalf("spacer double-spaced:\n%q", m.transcript)
	}
}
```

- [ ] **Step 2: Run tests to verify they pass (regression pin)**

```bash
go test ./internal/cli/ -run 'TestTranscriptNeverDoubleBlanks|TestCommitSpacerNeverDoubleSpaces' -v
```

Expected: PASS immediately — these pin the existing invariant. Then audit:

- [ ] **Step 3: Audit and fix any inconsistencies**

Check these patterns; fix only actual double blanks / trailing-blank-before-spacer defects, nothing else:
1. `commitSpacer()` already skips when the last block is blank or absent (verified by the test above).
2. `connectorBlock` / `toolCard` / `reasoningBlock` / `renderTurnReceiptBand` — search for outputs ending in `"\n"` (would create an extra blank before the spacer): `rg -n '\\\\n"$|"\\\\n\\+"' internal/cli/toolcard.go internal/cli/chat_tui.go internal/cli/md.go`.
3. `renderAssistantMarkdown` — ensure a trailing blank paragraph is not emitted as an extra empty block.

Fix anything found with the smallest change (trim trailing newline at the block boundary). If nothing is found, record "audit clean" in the commit message — the two tests are the acceptance.

- [ ] **Step 4: Run full tests**

```bash
go test ./internal/cli/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/transcript_test.go [fixed files if any]
git commit -m "test(cli): pin single-blank-line transcript density"
```

---

### Task 10: Neutral table code cells

**Files:**
- Modify: `internal/cli/md.go` (`mdRenderer.inTable`, `collectCells`, `appendInline`)
- Modify: `internal/cli/style.go` (`muted`)
- Test: `internal/cli/md_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/md_test.go`:

```go
func TestTableCodeSpanNeutral(t *testing.T) {
	r := newMarkdownRenderer(80)
	md := "| lang | code |\n| --- | --- |\n| go | `fmt.Println` |\n"
	got := r.Render(md)
	accentEsc := themeFg(activeCLITheme.accent, "")
	mutedEsc := themeFg(activeCLITheme.muted, "")
	if strings.Contains(got, accentEsc+"fmt.Println") {
		t.Fatalf("table code span must not use accent:\n%s", got)
	}
	if !strings.Contains(got, mutedEsc+"fmt.Println") {
		t.Fatalf("table code span should use the muted theme color:\n%s", got)
	}
}

func TestInlineCodeSpanStillAccentOutsideTable(t *testing.T) {
	r := newMarkdownRenderer(80)
	got := r.Render("use `os.Exit` here")
	accentEsc := themeFg(activeCLITheme.accent, "")
	if !strings.Contains(got, accentEsc+"os.Exit") {
		t.Fatalf("inline code outside tables must keep accent:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestTableCodeSpanNeutral|TestInlineCodeSpanStillAccentOutsideTable' -v
```

Expected: FAIL — table code span uses accent.

- [ ] **Step 3: Implement**

1. `internal/cli/style.go` — add next to `accent`:

```go
func muted(s string) string { return themeFg(activeCLITheme.muted, s) }
```

2. `internal/cli/md.go` — field on `mdRenderer`:

```go
	inTable       bool
```

3. `internal/cli/md.go` — `collectCells` sets/restores the flag:

```go
func (r *mdRenderer) collectCells(parent ast.Node, src []byte) []string {
	prev := r.inTable
	r.inTable = true
	defer func() { r.inTable = prev }()
	var out []string
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		if cell, ok := c.(*extast.TableCell); ok {
			out = append(out, strings.TrimSpace(r.collectInline(cell, src)))
		}
	}
	return out
}
```

4. `internal/cli/md.go` — `appendInline` CodeSpan case (~362):

```go
		case *ast.CodeSpan:
			var inner strings.Builder
			r.appendInline(&inner, v, src)
			if r.inTable {
				b.WriteString(muted(inner.String()))
			} else {
				b.WriteString(accent(inner.String()))
			}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestTableCodeSpanNeutral|TestInlineCodeSpanStillAccentOutsideTable' -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/md.go internal/cli/style.go internal/cli/md_test.go
git commit -m "feat(cli): neutral code spans inside table cells"
```

---

### Task 11: Color discipline (delete dead constants + scanner test)

**Files:**
- Modify: `internal/cli/diffview.go` (delete constants ~29–32)
- Modify: `internal/cli/diffview_test.go` (theme slots)
- Create: `internal/cli/color_discipline_test.go`

- [ ] **Step 1: Write the failing test**

`internal/cli/color_discipline_test.go`:

```go
package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// colorCSI matches anchored color-only CSI SGR sequences (ends with 'm').
// Deliberately does NOT match \033[K (erase), \033[3m (italic), \033[1m (bold),
// \033[7m (reverse), or OSC sequences.
var colorCSI = regexp.MustCompile(
	`\x1b\[[34][0-7]m` +
		`|\x1b\[9[0-7]m` +
		`|\x1b\[10[0-7]m` +
		`|\x1b\[38;5;[0-9]+m` +
		`|\x1b\[48;5;[0-9]+m` +
		`|\x1b\[38;2;[0-9]+;[0-9]+;[0-9]+m` +
		`|\x1b\[48;2;[0-9]+;[0-9]+;[0-9]+m`,
)

func TestNoHardcodedColorCodes(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			name == "theme.go" || name == "style.go" {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if m := colorCSI.FindString(s); m != "" {
				t.Errorf("%s: hardcoded color sequence %q in string literal", fset.Position(lit.Pos()), m)
			}
			return true
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/ -run TestNoHardcodedColorCodes -v
```

Expected: FAIL — `diffview.go` still contains `\033[48;5;22m` etc.

- [ ] **Step 3: Implement**

1. `internal/cli/diffview.go` — delete the four constants (lines ~29–32):

```go
	bgDiffAdd = "\033[48;5;22m"
	bgDiffDel = "\033[48;5;52m"
	fgDiffAdd = "\033[1;38;5;46m"
	fgDiffDel = "\033[1;38;5;203m"
```

2. `internal/cli/diffview_test.go` — `TestDiffBarReappliesBackground` uses theme slots:

```go
	line := diffBar('+', "a + b", "x.go", 40, bgSGR(activeCLITheme.diffAddBG), fgSGR(activeCLITheme.success), 12, 3)
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestNoHardcodedColorCodes|TestDiffBarReappliesBackground' -v
go test ./internal/cli/ -count=1
```

Expected: PASS; full package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/diffview.go internal/cli/diffview_test.go internal/cli/color_discipline_test.go
git commit -m "test(cli): forbid hardcoded SGR colors; delete dead diff constants"
```

---

### Task 12 (optional, spike): Shimmer A/B — go/no-go

**Files:**
- Create: `cmd/spike-shimmer/main.go` (temporary; deleted at go/no-go)

- [ ] **Step 1: Build the spike**

```go
package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// A/B spike: working line with a single-pass shimmer sweep vs static.
// Run: go run ./cmd/spike-shimmer   (see both variants for 3s each)
func main() {
	stops := []lipgloss.Color{lipgloss.Color("#858b96"), lipgloss.Color("#d97757")}
	ramp := lipgloss.Blend1D(24, stops...)
	text := "⠋ thinking… ( 12s · Esc cancels)"
	for round := 0; round < 2; round++ {
		start := time.Now()
		for i := 0; i < 120; i++ { // 3s at ~40fps
			var line string
			if round == 0 {
				pos := i % len(ramp)
				line = lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("%s", ramp[pos]))).Render(text)
			} else {
				line = text
			}
			clear := strings.Repeat("\r", len([]rune(line))) + "\x1b[K"
			fmt.Print(clear + line)
			time.Sleep(25 * time.Millisecond)
		}
		fmt.Println("\n--- round", round, "done ---")
		time.Sleep(time.Second)
	}
	_ = start
}
```

(If the `lipgloss.Color(...)` conversion from the ramp needs adjustment, adapt in the spike — it is exploratory, not TDD.)

- [ ] **Step 2: Run and decide**

```bash
go run ./cmd/spike-shimmer
```

- [ ] **Step 3: Record go/no-go and clean up**

- **Go:** keep shimmer in the design — a follow-up task integrates it behind `motionEnabled()`.
- **No-go:** delete `cmd/spike-shimmer/` and note the decision in the final commit.

Either way: `git rm -r cmd/spike-shimmer` (if no-go) and commit `docs: record shimmer spike go/no-go (no-go)` or proceed to integration.

---

### Task 13: Integration polish + docs + full verification

**Files:**
- Modify: `README.md`, `README.zh-CN.md` (env section)
- Modify: `docs/superpowers/plans/2026-08-06-tui-render-animation-p15.md` (this file: coverage table below)
- Test: full suite

- [ ] **Step 1: README env docs**

Add to the environment/configuration section of both READMEs:

```markdown
TUI environment:
- `REASONIX_REDUCE_MOTION=1` — disable decorative animation (spinner motion,
  smooth scroll, tool frame cycling). Elapsed counters still tick.
- `REASONIX_TUI_SCROLL_REPAINT=1` — legacy full-screen repaint on every scroll;
  only for terminals that strand stale rows under the cell-diff renderer
  (disables smooth scroll).
```

- [ ] **Step 2: Full verification**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
cd /home/miku/dv_project/mk_agent
go test ./internal/cli/ -count=1
go test ./internal/cli/ -run '^$' -bench 'BenchmarkAppendBlock|BenchmarkWrapTranscript/lines=10000' -benchmem -count=1
make build
```

Expected: all PASS; build succeeds; benchmark contrast (append vs full wrap) recorded in the plan's coverage table.

- [ ] **Step 3: Manual acceptance (user)**

Verify on the user's terminal (and at least one of Warp / iTerm2 / Windows Terminal / konsole):
1. Streaming a long answer at 5k+ lines feels fluid, no per-token jank.
2. PgUp/PgDn/wheel scroll smoothly; Ctrl+Home/End still jump instantly.
3. `REASONIX_REDUCE_MOTION=1` → static spinner, frozen tool frames, instant scroll, elapsed still ticks.
4. `REASONIX_TUI_SCROLL_REPAINT=1` → no stale rows on the problem terminal, scroll instant.
5. First screen shows the narrow/wide banner per terminal width.
6. Tables with code spans look calmer; diff view colors match the theme.

- [ ] **Step 4: Spec-coverage table (append to this plan)**

| Spec § | Task |
|--------|------|
| §4.1 scroll repaint | 2 |
| §4.2 incremental wrap + mutation inventory | 3 |
| §4.3 bottom panels single pass | 4 |
| §5.1 motion gate (spinner, tool frames) | 1, 6 |
| §5.2 smooth scroll | 7 |
| §5.3 shimmer | 12 (spike, go/no-go) |
| §5.4 fixed-width elapsed | 5 |
| §5.5 branding | 8 |
| §5.6 density | 9 |
| §5.7 table code cells | 10 |
| §5.8 color discipline | 11 |
| §6 benchmarks | 3, 13 |
| §7 docs/env | 13 |

- [ ] **Step 5: Commit**

```bash
git add README.md README.zh-CN.md docs/superpowers/plans/2026-08-06-tui-render-animation-p15.md
git commit -m "docs: P1.5 env vars, plan coverage table"
```

---

## Placeholder / consistency scan

- Single names used everywhere: `motionEnabled`, `scrollRepaintEnabled`, `workingCmds`/`workingBatch`, `setLiveBlock`, `wrapBlock`, `appendWrappedBlocks`, `rewrapBlock`, `rebuildWrappedLines`, `truncateWrappedBlocks`, `removeWrappedBlock`, `blockLineCounts`, `liveDirtyIdx`, `scrollRepaint`, `panels`/`panelsValid`/`panelRenderHook`/`renderBottomPanels`, `formatElapsedFixed`, `smooth`/`smoothScroll`/`smoothScrollTickMsg`/`startSmoothScroll`/`offsetAt`, `inTable`, `muted`.
- Task 5 and Task 6 both touch `tickToolRunning` — Task 5 changes the elapsed argument, Task 6 the frame advance; order is intentional.
- `ChatStatusRetryingFmt` is intentionally never changed (no seconds argument).
- No TBD/TODO; every code step shows complete code.

## Execution notes

- Work on a feature branch or worktree (`using-git-worktrees`) before Task 1.
- If a task's tests reveal a wider refactor need, stop and surface it — do not expand scope silently.
- Do not start P2 Tasks or shimmer integration in this plan.
