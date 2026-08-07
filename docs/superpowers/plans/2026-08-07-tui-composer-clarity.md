# TUI Composer Clarity & Bottom-Chrome Slimming — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Corvus TUI bottom chrome minimal: borderless tinted composer, cache-hit-only turn receipt, one-line footer with project path, and compact tool calls.

**Architecture:** Pure `internal/cli` changes. The composer loses its lipgloss border and gets a per-line background painter (`renderComposerField`) that re-arms the tint after every SGR reset the textarea emits. The footer becomes one logical row rendered by `layoutSingleStatusLine` (left status, right `path · model · cache-hit`). Tool completion removes the live output block entirely and re-anchors Ctrl+B expansion to the tool-card transcript source.

**Tech Stack:** Go 1.25 / toolchain 1.26.5, Bubble Tea v2, bubbles/textarea v2, lipgloss v2, x/ansi. Build: `go build ./...`; tests: `go test ./internal/cli/...`.

**Environment note:** Use `go` from `/home/miku/.local/go-linux/bin` (already wired via `~/.local/bin/go`). If `go env GOROOT` shows a `C:\` path, run `export PATH=/home/miku/.local/go-linux/bin:$PATH`.

**Design spec:** `docs/superpowers/specs/2026-08-07-tui-composer-clarity-design.md`

---

## File Map

- `internal/cli/theme.go` — new `inputBoxBG` palette slot; `inputBoxStyle` becomes borderless.
- `internal/cli/composer_selection.go` — background painter `renderComposerField` + helpers.
- `internal/cli/chat_tui.go` — composer wiring, cursor offsets, `composerBorderRows`, `bottomRows`, footer wiring, tool-collapse rework, `toolCardIdx` map.
- `internal/cli/status_footer.go` — cache-only `renderTurnReceipt`, single-line `renderStatusBlock`, `layoutSingleStatusLine`, `statusRightGroup`, `projectPath`/`abbrevHome`.
- `internal/cli/toolcard.go` — `renderToolCardExpanded`.
- `internal/cli/transcript.go` — `transcriptSource.shellID` field + expanded card render in `renderTranscriptSource`.
- `internal/i18n/*` — untouched (`ChatCacheHitLabel` already existed; only the unused `ChatTurnReceiptLabel` is dropped).
- Tests: `theme_test.go`, `composer_selection_test.go`, `chat_tui_test.go`, `chat_render_test.go`, `status_footer_test.go`, `statusline_test.go`, `consecutive_tool_markers_test.go`, `transcript_test.go`.

---

## Task 1: Theme tint slot and borderless composer style

**Files:**
- Modify: `internal/cli/theme.go`
- Test: `internal/cli/theme_test.go`

- [ ] **Step 1: Write the failing test**

Replace the body of `TestComposerBorderAndCursorTrackThemeAccent` in `internal/cli/theme_test.go` (currently around lines 257-287) with:

```go
func TestComposerTintAndCursorFollowTheme(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	for _, theme := range cliThemeStyles {
		t.Run(theme.name, func(t *testing.T) {
			configureCLITheme(theme.name)
			wantTint := activeCLITheme.inputBoxBG
			if wantTint.hex == "" {
				t.Fatalf("%s inputBoxBG must be populated", theme.name)
			}
			if got := inputBoxStyle.GetBackground(); !reflect.DeepEqual(got, color.Color("")) {
				t.Fatalf("inputBoxStyle must not carry a lipgloss background (painter owns the tint), got %v", got)
			}
			want := themeLipColor(activeCLITheme.accent)
			ti := textarea.New()
			applyTextareaTheme(&ti)
			if got := ti.Styles().Cursor.Color; !reflect.DeepEqual(got, want) {
				t.Fatalf("composer cursor color = %v, want theme accent %v", got, want)
			}
		})
	}

	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	if got := activeCLITheme.inputBoxBG; got.hex == "" {
		t.Fatalf("dark theme must keep a tint slot even under NO_COLOR")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestComposerTintAndCursorFollowTheme -count=1`
Expected: FAIL — `cliPalette` has no field `inputBoxBG` (compile error).

- [ ] **Step 3: Implement the palette slot and style change**

In `internal/cli/theme.go`:

1. Add the field to `cliPalette` (after `border cliColor`):

```go
	border          cliColor
	inputBoxBG      cliColor
```

2. In `cliDarkTheme` (after `border: cliColor{"#2a2f3b", 236},`):

```go
		inputBoxBG:     cliColor{"#1c2028", 234},
```

3. In `cliLightTheme` (after `border: cliColor{"#e6ddd0", 253},`):

```go
		inputBoxBG:     cliColor{"#eceff4", 255},
```

4. In `refreshCLIStyles()`, replace the `inputBoxStyle` assignment:

```go
	inputBoxStyle = lipgloss.NewStyle().PaddingLeft(1)
```

(The background is painted per-line by `renderComposerField`; `inputBoxStyle` keeps only width/padding semantics.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestComposerTintAndCursorFollowTheme -count=1`
Expected: PASS

- [ ] **Step 5: Run the theme suite and fix stragglers**

Run: `go test ./internal/cli/ -run 'TestTheme|TestComposer' -count=1`
Expected: PASS. Any test still asserting `inputBoxStyle.GetBorderTopForeground()`/`GetBorderBottomForeground()` must drop those assertions (the border is gone by design).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/theme.go internal/cli/theme_test.go
git commit -m "feat(cli): borderless composer with inputBoxBG theme tint"
```

---

## Task 2: Per-line background painter

**Files:**
- Modify: `internal/cli/composer_selection.go`
- Test: `internal/cli/composer_selection_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/composer_selection_test.go`:

```go
func TestComposerFieldPaintsContinuousBackground(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	bg := composerFieldBackground()
	if bg == "" {
		t.Fatal("composerFieldBackground should be non-empty with color on")
	}
	view := "\x1b[2m❯ \x1b[0mhello\x1b[m"
	got := renderComposerField(view, 12)
	if !strings.HasPrefix(got, bg) {
		t.Fatalf("painted field must open with the background SGR: %q", got)
	}
	if !strings.Contains(got, "\x1b[0m"+bg) {
		t.Fatalf("field must re-arm the background after \\x1b[0m: %q", got)
	}
	if !strings.Contains(got, "\x1b[m"+bg) {
		t.Fatalf("field must re-arm the background after \\x1b[m: %q", got)
	}
	if !strings.Contains(got, bg+"   ") {
		t.Fatalf("right padding must be background-armed: %q", got)
	}
	if w := visibleWidth(ansi.Strip(got)); w != 12 {
		t.Fatalf("painted field visible width = %d, want 12: %q", w, got)
	}
}

func TestComposerFieldPreservesSelectionStyle(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	view := "abc\x1b[7mdef\x1b[0mghi"
	got := renderComposerField(view, 9)
	if !strings.Contains(got, "\x1b[7m") {
		t.Fatalf("selection reverse SGR must survive painting: %q", got)
	}
	if !strings.Contains(got, "\x1b[0m"+composerFieldBackground()) {
		t.Fatalf("background must re-arm after the selection reset: %q", got)
	}
}

func TestComposerFieldRespectsNoColor(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	view := "hello"
	if got := renderComposerField(view, 10); got != view {
		t.Fatalf("NO_COLOR field must pass through unchanged, got %q", got)
	}
	if got := composerFieldBackground(); got != "" {
		t.Fatalf("NO_COLOR composerFieldBackground = %q, want empty", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestComposerField -count=1`
Expected: FAIL — `composerFieldBackground` / `renderComposerField` undefined.

- [ ] **Step 3: Implement the painter**

In `internal/cli/composer_selection.go`, add to the import block:

```go
	"regexp"

	"github.com/charmbracelet/x/ansi"
```

Then append at the end of the file:

```go
// sgrResetRe matches the reset codes the textarea/prompt styling emits
// ("\x1b[0m" and "\x1b[m"). Color-setting SGRs are deliberately not matched so
// selection backgrounds survive painting.
var sgrResetRe = regexp.MustCompile(`\x1b\[0?m`)

// composerFieldBackground returns the SGR that arms the composer field's
// translucent tint, or "" when color is off.
func composerFieldBackground() string {
	if !colorOn() {
		return ""
	}
	return bgSGR(activeCLITheme.inputBoxBG)
}

// rearmFieldBackground re-issues the field background after every reset code
// inside a rendered line so no textarea SGR leaves a hollow cell.
func rearmFieldBackground(s, bg string) string {
	return sgrResetRe.ReplaceAllStringFunc(s, func(m string) string {
		return m + bg
	})
}

// renderComposerField paints the textarea view as a borderless field: every
// line opens with the field background, re-arms it after each reset, and
// right-pads with background-armed spaces to the full box width so the tint
// reads as one continuous block. Pass-through when color is off.
func renderComposerField(view string, width int) string {
	bg := composerFieldBackground()
	if bg == "" || width <= 0 {
		return view
	}
	var out strings.Builder
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(bg)
		out.WriteString(rearmFieldBackground(line, bg))
		if w := visibleWidth(ansi.Strip(line)); w < width {
			out.WriteString(bg + strings.Repeat(" ", width-w))
		}
	}
	return out.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run TestComposerField -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/composer_selection.go internal/cli/composer_selection_test.go
git commit -m "feat(cli): per-line composer field background painter"
```

---

## Task 3: Borderless composer wiring (layout math, badge, cursor)

**Files:**
- Modify: `internal/cli/chat_tui.go`
- Test: `internal/cli/chat_tui_test.go`, `internal/cli/composer_selection_test.go`

- [ ] **Step 1: Write the failing tests**

1. In `internal/cli/chat_tui_test.go`, update `TestTranscriptViewportSizing` (currently asserts `bottomRows == 4` and `transcriptHeight == 20`):

```go
// TestTranscriptViewportSizing proves the viewport tracks the terminal size and
// gets the rows left over after the pinned bottom region (input box + the one
// footer row = 2 with an empty 1-line composer and no Git or telemetry), and is
// fed the committed transcript.
func TestTranscriptViewportSizing(t *testing.T) {
	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)

	m0, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m0.(chatTUI)

	if got := m.bottomRows(); got != 2 {
		t.Fatalf("bottomRows with an empty composer = %d, want 2 (input 1 + footer 1)", got)
	}
	if m.viewport.Width() != 79 {
		t.Errorf("viewport content width = %d, want 79 (terminal 80 - 1 scrollbar column)", m.viewport.Width())
	}
	if want := m.transcriptHeight(); m.viewport.Height() != want || want != 22 {
		t.Errorf("viewport height = %d, transcriptHeight = %d, want 22 (24-2)", m.viewport.Height(), want)
	}
	if m.viewport.TotalLineCount() == 0 {
		t.Errorf("viewport should hold the committed banner after the first resize")
	}
}
```

2. In `internal/cli/chat_tui_test.go`, update the panel-table budget assertion (currently `panelRows+m.input.Height()+2+m.statusLineCount`):

```go
			if got, want := m.bottomRows(), panelRows+m.input.Height()+m.statusLineCount; got != want {
				t.Fatalf("bottomRows with %s = %d, want %d (panel + composer + footer row)", tt.name, got, want)
			}
```

3. Append to `internal/cli/composer_selection_test.go`:

```go
func TestComposerFieldRendersBadgeOnFirstRowOnly(t *testing.T) {
	view := "row0\nrow1\nrow2"
	got := joinModeBadgeLeftOfComposer("AUTO ", view)
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[0], "AUTO ") {
		t.Fatalf("badge must sit on row 0, got %q", lines[0])
	}
	for i, ln := range lines[1:] {
		if !strings.HasPrefix(ln, "     ") {
			t.Fatalf("continuation row %d must carry the badge-width gutter, got %q", i+1, ln)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestTranscriptViewportSizing|TestComposerFieldRendersBadgeOnFirstRowOnly' -count=1`
Expected: FAIL — `bottomRows` still 4; `joinModeBadgeLeftOfComposer` still puts the badge on row 1 for 3+ lines.

- [ ] **Step 3: Implement the wiring**

In `internal/cli/chat_tui.go`:

1. Replace the composer-box construction inside `View()` (currently around lines 2905-2920):

```go
	var box string
	badgeCols := 0
	if !hideComposer {
		badge := m.renderModeBadge(shellMode)
		const badgeGap = " "
		badgeCols = visibleWidth(badge) + visibleWidth(badgeGap)
		// Borderless field: the painter tints the whole box width, so the mode
		// badge is the only chrome left of the ❯ prompt.
		box = joinModeBadgeLeftOfComposer(badge+badgeGap, renderComposerField(m.renderComposerInput(), m.composerBoxWidth(badgeCols)))
	}
```

2. In the native-scrollback cursor block, change the comment and offset:

```go
			if cur := m.composerCursor(); cur != nil {
				// badge column; the borderless field has no padding chrome
				cur.X += badgeCols
				cur.Y += rowsAboveBox
				v.Cursor = cur
			}
```

3. In the alt-screen cursor block, change the comment and offset:

```go
	// Anchor the real terminal cursor at the textarea's insertion point only when
	// the composer is visible. input.Cursor() is relative to the textarea; offset
	// by the viewport height + rows above, then by the mode-badge column (the
	// borderless field adds no border/padding chrome).
	if !hideComposer {
		if cur := m.composerCursor(); cur != nil {
			cur.X += badgeCols
			cur.Y += m.viewport.Height() + rowsAboveBox
			v.Cursor = cur
		}
	}
```

4. Replace `joinModeBadgeLeftOfComposer` (currently around lines 3517-3540):

```go
// joinModeBadgeLeftOfComposer places the mode badge beside the first row of the
// borderless composer field, so the chip shares a line with the ❯ prompt.
// Wrapped continuation rows get a blank left gutter of the same width.
func joinModeBadgeLeftOfComposer(badgeWithGap, box string) string {
	rightLines := strings.Split(box, "\n")
	leftW := visibleWidth(badgeWithGap)
	gutter := strings.Repeat(" ", leftW)
	out := make([]string, len(rightLines))
	for i, r := range rightLines {
		if i == 0 {
			out[i] = badgeWithGap + r
			continue
		}
		out[i] = gutter + r
	}
	return strings.Join(out, "\n")
}
```

5. Change the constant (currently around line 3544):

```go
const (
	composerBorderRows = 0
	minTranscriptRows  = 3
)
```

6. In `bottomRows()` (currently around lines 1965-1988), drop the border rows:

```go
	if !m.hideComposer() {
		rows += m.input.Height()
	}
	if m.statusLineCount > 0 {
		return rows + m.statusLineCount
	}
	return rows + 1 // fallback for tests that don't set statusLineCount
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestTranscriptViewportSizing|TestComposerFieldRendersBadgeOnFirstRowOnly' -count=1`
Expected: PASS

- [ ] **Step 5: Run the broader suite and fix border-row expectations**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS except tests that still pin the old border-row budget. Fix them by removing the `+2` border term from their expected row math (same pattern as Step 1.2). Known candidates: `TestManualNewlineGrowsComposerWithoutHidingFirstLine`, any `bottomRows`-based assertions in `chat_tui_test.go`/`chat_render_test.go`. If a cursor-position test fails, it is because it expected the old `+1` padding offset — update the expectation to the new `badgeCols` math.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/chat_tui_test.go internal/cli/composer_selection_test.go
git commit -m "feat(cli): borderless composer layout, badge and cursor wiring"
```

---

## Task 4: Cache-hit-only turn receipt

**Files:**
- Modify: `internal/i18n/i18n.go`, `internal/i18n/messages_en.go`, `internal/i18n/messages_zh.go`, `internal/i18n/messages_zh_tw.go`
- Modify: `internal/cli/status_footer.go`, `internal/cli/chat_tui.go`
- Test: `internal/cli/status_footer_test.go`, `internal/cli/chat_tui_test.go`, `internal/cli/chat_render_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/cli/status_footer_test.go`, replace `TestTurnReceiptKeepsCompletePerTurnBreakdown`, `TestTurnReceiptFallsBackToDerivedFreshTokensAndWrapsCleanly`, `TestTurnReceiptMarksEstimatedUsage`, and `TestTurnReceiptAdaptsContrastAcrossThemes` with:

```go
func TestTurnReceiptShowsOnlyCacheHit(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	i18n.DetectLanguage("zh")

	u := &provider.Usage{
		PromptTokens: 13_625, CompletionTokens: 392, TotalTokens: 14_017,
		CacheHitTokens: 13_184, CacheMissTokens: 441, ReasoningTokens: 24,
	}
	got := renderTurnReceipt(u)
	for _, want := range []string{"缓存命中", "13.2K"} {
		if !strings.Contains(got, want) {
			t.Fatalf("turn receipt %q missing %q", got, want)
		}
	}
	for _, banned := range []string{"tok", "in ", "out ", "reasoning", "¥", "estimated", "prefix"} {
		if strings.Contains(got, banned) {
			t.Fatalf("turn receipt %q must not contain %q", got, banned)
		}
	}
	if strings.Contains(got, "\033[") {
		t.Fatalf("NO_COLOR turn receipt contains escapes: %q", got)
	}
}

func TestTurnReceiptShowsZeroWhenNoHits(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	i18n.DetectLanguage("en")

	got := renderTurnReceipt(&provider.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120})
	for _, want := range []string{"cached", "0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("zero-hit receipt %q missing %q", got, want)
		}
	}
}

func TestTurnReceiptIgnoresNilUsage(t *testing.T) {
	if got := renderTurnReceipt(nil); got != "" {
		t.Fatalf("nil usage receipt = %q, want empty", got)
	}
}

func TestTurnReceiptAdaptsContrastAcrossThemes(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.ANSI256
	i18n.DetectLanguage("en")

	for _, tt := range []struct {
		mode, labelSGR, valueSGR string
	}{
		{mode: "dark", labelSGR: "\033[38;5;247m", valueSGR: "\033[38;5;252m"},
		{mode: "light", labelSGR: "\033[38;5;243m", valueSGR: "\033[38;5;238m"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			configureCLITheme(tt.mode)
			receipt := renderTurnReceipt(&provider.Usage{
				PromptTokens: 900, CompletionTokens: 100, TotalTokens: 1_000, CacheHitTokens: 900,
			})
			for _, want := range []string{tt.labelSGR + "cached", tt.valueSGR + "900"} {
				if !strings.Contains(receipt, want) {
					t.Fatalf("%s receipt %q missing semantic style %q", tt.mode, receipt, want)
				}
			}
		})
	}
}
```

In `internal/cli/chat_tui_test.go`, update the usage-event table in `TestIngestEventRoutesByKind` (currently around lines 1371-1382):

```go
	// Usage does not commit a scrollback line; it feeds the cache-hit readout.
	for _, tc := range []struct {
		name string
		ev   event.Event
		want string
	}{
		{"usage", event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200, CacheHitTokens: 900, CacheMissTokens: 100}}, "cached 900"},
		{"usage-zero-hit", event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200}}, "cached 0"},
	} {
		m := newTestChatTUI()
		m.ingestEvent(tc.ev)
		if got := *m.pendingCommit; len(got) != 0 {
			t.Errorf("%s: usage must not commit a scrollback line, got %v", tc.name, got)
		}
		if !strings.Contains(ansi.Strip(m.turnReceipt), tc.want) {
			t.Errorf("%s: turn receipt %q missing %q", tc.name, m.turnReceipt, tc.want)
		}
	}
```

In `internal/cli/chat_render_test.go`, update `TestTurnReceiptMovesBelowComposer` — replace the `"TURN"` assertions with `"cached"`:

```go
func TestTurnReceiptMovesBelowComposer(t *testing.T) {
	ctrl := control.New(control.Options{})
	ch := make(chan event.Event, 1)
	m := newChatTUI(ctrl, "", ch, 80)
	m0, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = m0.(chatTUI)
	m.ingestEvent(event.Event{Kind: event.Text, Text: "Answer"})
	m.ingestEvent(event.Event{Kind: event.Message})
	m.ingestEvent(event.Event{Kind: event.Usage, Usage: &provider.Usage{
		PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
	}})

	for _, block := range m.transcript {
		if strings.Contains(ansi.Strip(block), "cached") {
			t.Fatalf("receipt must not stay in the transcript scrollback, got %q", block)
		}
	}
	if !strings.Contains(ansi.Strip(m.turnReceipt), "cached") {
		t.Fatalf("turn receipt not captured, got %q", m.turnReceipt)
	}
	view := m.View().Content
	if !strings.Contains(ansi.Strip(view), "cached") {
		t.Fatalf("View should render the receipt below the composer:\n%s", ansi.Strip(view))
	}
	// The receipt sits after the composer box: it must appear after "❯" input
	// prompt marker in the rendered output.
	boxIdx := strings.LastIndex(view, "❯")
	receiptIdx := strings.Index(view, "cached")
	if boxIdx < 0 || receiptIdx < 0 || receiptIdx < boxIdx {
		t.Fatalf("receipt should render below the composer (box at %d, receipt at %d)", boxIdx, receiptIdx)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestTurnReceipt|TestIngestEventRoutesByKind|TestTurnReceiptMovesBelowComposer' -count=1`
Expected: FAIL — `renderTurnReceipt` still has the old signature/fields; `ChatCacheHitLabel` missing.

- [ ] **Step 3: Implement the receipt and i18n key**

1. `internal/i18n/i18n.go` — add after `ChatTurnReceiptLabel`:

```go
	ChatTurnReceiptLabel                   string // compact per-turn usage receipt attached to the completed assistant response
	ChatCacheHitLabel                      string // cache-hit readout shown after each completed turn
```

2. `internal/i18n/messages_en.go` — after `ChatTurnReceiptLabel: "TURN",`:

```go
	ChatCacheHitLabel:                      "cached",
```

3. `internal/i18n/messages_zh.go` — after `ChatTurnReceiptLabel: "本轮",`:

```go
	ChatCacheHitLabel:                      "缓存命中",
```

4. `internal/i18n/messages_zh_tw.go` — after `ChatTurnReceiptLabel: "本輪",`:

```go
	ChatCacheHitLabel:                      "快取命中",
```

5. `internal/cli/status_footer.go` — replace `renderTurnReceipt`:

```go
// renderTurnReceipt renders the completed turn's cache-hit readout. Only the
// cache-hit segment survives: totals, in/out/reasoning/cost and cache-prefix
// warnings are intentionally dropped for a quiet footer.
func renderTurnReceipt(u *provider.Usage) string {
	if u == nil {
		return ""
	}
	return footerMetric(i18n.M.ChatCacheHitLabel, footerValue(shortTokens(u.CacheHitTokens)))
}
```

6. `internal/cli/chat_tui.go` — update the `event.Usage` case (currently around line 3949):

```go
	case event.Usage:
		if e.Usage != nil {
			m.turnTokens += e.Usage.CompletionTokens
		}
		m.finalizeStreamed()
		m.turnReceipt = renderTurnReceipt(e.Usage)
```

Note: `renderTurnReceipt` no longer takes `e.Pricing`/`e.CacheDiagnostics`; remove the `event.CacheDiagnostics`-driven warning path. `internal/cli/status_footer.go` no longer needs the `provider.Pricing` param — keep the `event` import only if still used elsewhere in that file (it is not after this change; remove it if the compiler complains).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestTurnReceipt|TestIngestEventRoutesByKind|TestTurnReceiptMovesBelowComposer' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/i18n internal/cli/status_footer.go internal/cli/chat_tui.go internal/cli/status_footer_test.go internal/cli/chat_tui_test.go internal/cli/chat_render_test.go
git commit -m "feat(cli): cache-hit-only turn receipt"
```

---

## Task 5: Single-line footer with project path

**Files:**
- Modify: `internal/cli/status_footer.go`, `internal/cli/chat_tui.go`
- Test: `internal/cli/status_footer_test.go`, `internal/cli/chat_tui_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/cli/status_footer_test.go`:

1. Replace `TestStatusFooterDefaultOmitsBalanceGitCache` with:

```go
func TestStatusFooterSingleLineOmitsBalanceGitCacheContext(t *testing.T) {
	i18n.DetectLanguage("en")

	prov := testutil.NewMock("deepseek-v4-flash", testutil.Turn{
		Text: "ok",
		Usage: &provider.Usage{
			CacheHitTokens:   900,
			CacheMissTokens:  100,
			CompletionTokens: 50,
			PromptTokens:     1000,
			TotalTokens:      1050,
		},
	})
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{MaxSteps: 1, ContextWindow: 200_000}, event.Discard)
	if err := exec.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("seed agent usage: %v", err)
	}

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Executor: exec, WorkspaceRoot: "/home/user/project"})
	m.label = "deepseek-v4-flash"
	m.effortLevel = "auto"
	m.runtimeProfile = "full"
	m.balance = "¥12.34"
	m.gitStatus = gitStatus{Repo: "Corvus", Branch: "main", Added: 1}
	m.turnReceipt = renderTurnReceipt(&provider.Usage{TotalTokens: 1050, CacheHitTokens: 900})

	plain := ansi.Strip(m.renderStatusBlock(m.primaryStatusLine(false, false), 100))
	for _, banned := range []string{
		"BAL", "¥12.34", "Corvus", "main",
		i18n.M.ChatStatusCacheLabel, "turn hit", "avg 90",
		i18n.M.ChatStatusContextLabel, i18n.M.ChatStatusJobsLabel, "COMPACT",
	} {
		if strings.Contains(plain, banned) {
			t.Fatalf("single-line footer must omit %q:\n%s", banned, plain)
		}
	}
	for _, want := range []string{"/home/user/project", "MODEL deepseek-v4-flash", "WORK balanced", "cached 900"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("single-line footer missing %q:\n%s", want, plain)
		}
	}
	if strings.Count(plain, "\n") != 0 {
		t.Fatalf("footer must be one row at width 100:\n%s", plain)
	}
}
```

2. Replace the divider test (`TestStatusFooterSemanticPaletteAcrossThemes` divider block around lines 197-213 — the case that asserts `statusFooterDivider`) with a narrow-wrap test:

```go
func TestSingleStatusLineWrapsAtGroupBoundaries(t *testing.T) {
	i18n.DetectLanguage("en")
	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{WorkspaceRoot: "/home/user/project"})
	m.label = "deepseek-v4-flash"
	m.turnReceipt = renderTurnReceipt(&provider.Usage{TotalTokens: 1050, CacheHitTokens: 900})

	primary := m.primaryStatusLine(false, false)
	got := m.renderStatusBlock(primary, 30)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("narrow footer should wrap, got one row: %q", got)
	}
	for i, line := range lines {
		if width := visibleWidth(line); width > 30 {
			t.Fatalf("row %d width = %d, want <= 30: %q", i, width, line)
		}
	}
}

func TestAbbrevHomeShortensHomePrefix(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	for _, tt := range []struct{ in, want string }{
		{"/home/user/project", "~/project"},
		{"/home/user", "~"},
		{"/srv/other", "/srv/other"},
		{"", ""},
	} {
		if got := abbrevHome(tt.in); got != tt.want {
			t.Fatalf("abbrevHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSingleStatusLineRightAlignsWhenItFits(t *testing.T) {
	left := footerHint("idle")
	right := footerSecondary("~/project") + " · " + footerInfo("MODEL m")
	got := layoutSingleStatusLine(left, right, 40)
	if strings.Contains(got, "\n") {
		t.Fatalf("expected one row, got %q", got)
	}
	if width := visibleWidth(got); width != 40 {
		t.Fatalf("row width = %d, want 40: %q", width, got)
	}
	if !strings.HasSuffix(ansi.Strip(got), "MODEL m") {
		t.Fatalf("right group should be right-aligned: %q", ansi.Strip(got))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestStatusFooterSingleLine|TestSingleStatusLine|TestAbbrevHome|TestStatusFooterSemanticPaletteAcrossThemes' -count=1`
Expected: FAIL — `abbrevHome`, `layoutSingleStatusLine`, `statusRightGroup` undefined; old footer renders two rows.

- [ ] **Step 3: Implement the single-line footer**

In `internal/cli/status_footer.go`:

1. Add `"os"` to the import block.

2. Replace `renderStatusBlock` and remove `statusFooterDivider` (keep `layoutStatusSides`, `rightAlignStatusGroup`, `statusTelemetryGroups`, `layoutDataBand`, `renderContextStatusGroups` — they remain covered by other tests but are no longer part of the footer):

```go
// abbrevHome shortens a path under the user's home directory to "~".
func abbrevHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// projectPath returns the workspace root of the current session, falling back
// to the process cwd when no controller (or no configured root) exists.
func (m chatTUI) projectPath() string {
	root := ""
	if m.ctrl != nil {
		root = m.ctrl.WorkspaceRoot()
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	return abbrevHome(root)
}

// statusRightGroup renders the right half of the single footer row: project
// path · model · cache hit. A configured custom statusline replaces all of it
// (existing contract: it owns the data fields).
func (m chatTUI) statusRightGroup(width int) string {
	if m.statuslineCmd != "" && m.statuslineOut != "" {
		return footerHint(ansi.Strip(m.statuslineOut))
	}
	var groups []string
	if path := m.projectPath(); path != "" {
		groups = append(groups, footerSecondary(compactMiddle(path, max(width/3, 12))))
	}
	if model := m.statusModelWorkGroup(max(width-visibleWidth(strings.Join(groups, " · "))-1, 1)); model != "" {
		groups = append(groups, model)
	}
	if m.turnReceipt != "" {
		groups = append(groups, m.turnReceipt)
	}
	return strings.Join(groups, " · ")
}

// layoutSingleStatusLine lays out the one footer row: left status text, right
// data group. When both fit they sit on one row (right group right-aligned);
// otherwise the combined line wraps at " · " group boundaries.
func layoutSingleStatusLine(left, right string, width int) string {
	switch {
	case right == "":
		return wrapStatusGroups(left, width)
	case left == "":
		return wrapStatusGroups(right, width)
	}
	full := left + " · " + right
	if visibleWidth(full) <= width {
		return left + strings.Repeat(" ", width-visibleWidth(full)) + right
	}
	return wrapStatusGroups(full, width)
}

// renderStatusBlock owns the single persistent footer row under the composer.
func (m chatTUI) renderStatusBlock(primary string, width int) string {
	if width <= 0 {
		width = 1
	}
	primary = hideStatusHintWhenKeyNamesCannotFit(primary, width)
	return layoutSingleStatusLine(primary, m.statusRightGroup(width), width)
}
```

3. In `internal/cli/chat_tui.go` `View()` (currently around lines 3002-3008), remove the separate receipt block:

```go
	// The cache-hit readout lives inside the single footer row; there is no
	// separate receipt line under the composer.
```

4. In `internal/cli/chat_tui.go`, replace `computeStatusLineCount` (currently around lines 3430-3460):

```go
// computeStatusLineCount predicts the terminal rows the bottom status region
// occupies: the working (spinner) line while a turn runs, plus the single
// footer row (wrapped at " · " group boundaries). Mirrors View().
func (m chatTUI) computeStatusLineCount(width int) int {
	shellMode := strings.HasPrefix(strings.TrimSpace(m.input.Value()), "!")
	cancelRequested := m.cancelRequested()

	primaryStatus := m.primaryStatusLine(shellMode, cancelRequested)
	statusBlock := m.renderStatusBlock(primaryStatus, width)
	working := m.runningWorkingLine(cancelRequested, false)

	var lines int
	if m.state == tuiRunning {
		lines += strings.Count(wrapStatusLine(working, width), "\n") + 1
	}
	lines += strings.Count(statusBlock, "\n") + 1
	return lines
}
```

5. In `internal/cli/chat_tui.go`, update the constructor default (currently `statusLineCount: 3,` around line 617):

```go
		statusLineCount:      1,
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestStatusFooterSingleLine|TestSingleStatusLine|TestAbbrevHome|TestStatusFooterSemanticPaletteAcrossThemes|TestStatusFooterHeightCountUsesRenderedLayout' -count=1`
Expected: PASS

- [ ] **Step 5: Run the full suite and update wrap-accounting expectations**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS except `TestStatusLineWrapAccounting` / `TestStatusLineRenderedHeightMatchesBudget` (chat_tui_test.go) if their `> 2` assumptions no longer hold. Update those tests: replace the comment "mode+state line and data line" with "single footer row", and keep `statusLineCount > 2` only if the narrow widths actually wrap; otherwise lower the width in the test setup until wrapping is exercised (the height-budget invariants `transcriptHeight()+bottomRows() == height` and `View()` total lines == height must stay).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/status_footer.go internal/cli/chat_tui.go internal/cli/status_footer_test.go internal/cli/chat_tui_test.go
git commit -m "feat(cli): single-line footer with project path and cache hit"
```

---

## Task 6: Compact tool calls (no summaries, card-anchored Ctrl+B)

**Files:**
- Modify: `internal/cli/chat_tui.go`, `internal/cli/transcript.go`, `internal/cli/toolcard.go`
- Test: `internal/cli/chat_render_test.go`, `internal/cli/consecutive_tool_markers_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/cli/chat_render_test.go`:

1. Replace `TestToolProgressStreamsThenCollapses`:

```go
// TestToolProgressStreamsThenCollapses proves a running tool's output streams
// live under its card via the ⎿ connector, then vanishes entirely when the
// result lands — only the ● Verb(arg) card remains (no line-count summary).
func TestToolProgressStreamsThenCollapses(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "b1", Name: "bash", Args: `{"command":"go test ./..."}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "b1", Output: "ok pkg/a\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "b1", Output: "ok pkg/b\n"}})

	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "ok pkg/a") || !strings.Contains(joined, "ok pkg/b") {
		t.Fatalf("live output should be visible while running:\n%s", joined)
	}
	if !strings.Contains(joined, "⎿") {
		t.Fatalf("live output should use the ⎿ connector:\n%s", joined)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "b1", Name: "bash", Output: "ok pkg/a\nok pkg/b\n"}})
	joined = strings.Join(m.transcript, "\n")
	if strings.Contains(joined, "ok pkg/a") {
		t.Fatalf("output must be removed after completion:\n%s", joined)
	}
	if strings.Contains(joined, "lines") {
		t.Fatalf("no line-count summary may remain:\n%s", joined)
	}
	if len(m.transcript) != 1 {
		t.Fatalf("only the card should remain, got %d blocks:\n%s", len(m.transcript), joined)
	}
}
```

2. Replace `TestToolWorkingLineThenClears`:

```go
// TestToolWorkingLineThenClears proves a dispatched tool that streams no output
// (e.g. symbol_context) shows a live "working · Ns" line so it doesn't look
// frozen, and that the line is removed on the result — no "0 lines", no blank
// slot.
func TestToolWorkingLineThenClears(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "c1", Name: "symbol_context", Args: `{"q":"x"}`}})

	m.tickToolRunning() // one elapsed tick fills the placeholder
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "⎿") || !strings.Contains(joined, "working") {
		t.Fatalf("a running tool should show a 'working' progress line:\n%s", joined)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "c1", Name: "symbol_context"}})
	joined = strings.Join(m.transcript, "\n")
	if strings.Contains(joined, "working") {
		t.Fatalf("working line should be removed after the result:\n%s", joined)
	}
	if strings.Contains(joined, "0 lines") || strings.Contains(joined, "-1 lines") {
		t.Fatalf("a no-output tool must not leave a count summary:\n%s", joined)
	}
	if strings.TrimSpace(joined) == "" {
		t.Fatalf("the card itself must remain:\n%q", joined)
	}
	if m.toolStreamIdx != -1 {
		t.Fatalf("tool block should be closed after the result, idx=%d", m.toolStreamIdx)
	}
}
```

3. Replace `TestConsecutiveToolCallsKeepMarkersUnderOwnCard` with a compact-spacing version (keep the regression intent: each card's output is removed on ITS OWN result, not another tool's):

```go
// TestConsecutiveToolCallsStayCompact is the regression test for back-to-back
// Bash tool calls: each tool's live block is removed when ITS OWN result
// lands, no summary rows remain, and the transcript holds only the two cards
// in dispatch order (no blank spacer, no ⎿ markers).
func TestConsecutiveToolCallsStayCompact(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-1", Name: "bash", Args: `{"command":"git status"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-1", Output: "On branch main-v2\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-2", Name: "bash", Args: `{"command":"git branch -a"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-2", Output: "* main-v2\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-1", Output: "nothing to commit\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-1", Name: "bash", Output: "On branch main-v2\nnothing to commit\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-2", Name: "bash", Output: "* main-v2\n"}})

	transcript := m.transcript
	if len(transcript) != 2 {
		t.Fatalf("expected exactly the two cards, got %d blocks:\n%s", len(transcript), strings.Join(transcript, "\n"))
	}
	idx1 := strings.Index(transcript[0], "git status")
	idx2 := strings.Index(transcript[1], "git branch -a")
	if idx1 < 0 || idx2 < 0 {
		t.Fatalf("cards should remain in dispatch order:\n%s", strings.Join(transcript, "\n"))
	}
	joined := strings.Join(transcript, "\n")
	for _, banned := range []string{"⎿", "lines", "On branch", "nothing to commit", "* main-v2"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("compact transcript must not contain %q:\n%s", banned, joined)
		}
	}
	if _, ok := m.shellTranscriptIdx["shell-1"]; !ok {
		t.Fatalf("shell-1 must keep a Ctrl+B anchor on its card")
	}
	if idx := m.shellTranscriptIdx["shell-1"]; idx != 0 {
		t.Fatalf("shell-1 anchor should be the card index 0, got %d", idx)
	}
}
```

4. Replace `TestCollapsedShellHintUsesKeyboardShortcutOnly` with a Ctrl+B round-trip test:

```go
// TestCtrlBTogglesOutputOnTheCardBlock proves Ctrl+B expands the finished
// shell output onto the card block (card + ⎿ output) and collapses back to
// the bare card, surviving a reflow (resize) in the expanded state.
func TestCtrlBTogglesOutputOnTheCardBlock(t *testing.T) {
	m := newTestChatTUI()
	const id = "shell-long"
	lines := make([]string, 4)
	for i := range lines {
		lines[i] = "line"
	}
	output := strings.Join(lines, "\n") + "\n"
	m.shellOutputs[id] = output
	m.transcript = []string{"  ● Bash(cmd)"}
	m.transcriptSources = []transcriptSource{{kind: transcriptSourceToolCard, raw: "bash", aux: `{"command":"cmd"}`, shellID: id}}
	m.shellTranscriptIdx[id] = 0
	m.toolCardIdx = map[string]int{id: 0}

	m.toggleShellOutput()
	got := m.transcript[0]
	if !strings.Contains(got, "line") || !strings.Contains(got, "⎿") {
		t.Fatalf("expanded card should carry the output under the connector, got %q", got)
	}

	// Reflow keeps the expanded state (source-driven render).
	m0, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = m0.(chatTUI)
	if !m.shellExpanded[id] {
		t.Fatalf("expanded state must survive a resize")
	}
	if got := m.transcript[0]; !strings.Contains(got, "line") {
		t.Fatalf("reflowed expanded card lost the output: %q", got)
	}

	m.toggleShellOutput()
	if got := m.transcript[0]; strings.Contains(got, "line") || strings.Contains(got, "⎿") {
		t.Fatalf("collapsed card should be bare, got %q", got)
	}
	if m.shellExpanded[id] {
		t.Fatalf("second toggle must collapse the output")
	}
}
```

5. Replace `TestConsecutiveNonShellToolsDoNotRenderNegativeLineCount`:

```go
// TestConsecutiveNonShellToolsLeaveNoSlots proves back-to-back non-shell tools
// (e.g. read_file) leave only their cards after each result — no count
// summaries, no blank slots, no negative counts.
func TestConsecutiveNonShellToolsLeaveNoSlots(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "read_file-1", Name: "read_file", Args: `{"path":"a.txt"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "read_file-2", Name: "read_file", Args: `{"path":"b.txt"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "read_file-1", Name: "read_file", Output: "a.txt contents"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "read_file-2", Name: "read_file", Output: "b.txt contents"}})

	joined := strings.Join(m.transcript, "\n")
	for _, banned := range []string{"lines", "⎿", "a.txt contents", "b.txt contents"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("transcript must not contain %q:\n%s", banned, joined)
		}
	}
	if len(m.transcript) != 2 {
		t.Fatalf("only the two cards should remain, got %d blocks:\n%s", len(m.transcript), joined)
	}
}
```

6. Update `TestToolProgressTailCap` — append a result and assert the live block is removed:

```go
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "b1", Name: "bash", Output: "lineA\n"}})
	if got := strings.Join(m.transcript, "\n"); strings.Contains(got, "line") {
		t.Fatalf("completed tool output must be removed:\n%s", got)
	}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestToolProgress|TestToolWorkingLine|TestConsecutiveToolCalls|TestCtrlB|TestConsecutiveNonShell|TestToolProgressTailCap' -count=1`
Expected: FAIL — summaries still rendered; `toolCardIdx` undefined; `shellID` field missing.

- [ ] **Step 3: Implement the tool-collapse rework**

1. `internal/cli/transcript.go` — add the field to `transcriptSource`:

```go
type transcriptSource struct {
	kind     transcriptSourceKind
	raw      string
	aux      string
	shellID  string // tool id for expandable tool cards (Ctrl+B)
	planMode bool
	maxLines int
	history  []provider.Message
}
```

2. `internal/cli/transcript.go` — update the tool-card render in `renderTranscriptSource`:

```go
	case transcriptSourceToolCard:
		if source.shellID != "" && m.shellExpanded[source.shellID] {
			return renderToolCardExpanded(source.raw, source.aux, m.shellOutputs[source.shellID], terminalWidth)
		}
		return toolCard(source.raw, source.aux, terminalWidth)
```

3. `internal/cli/toolcard.go` — add `fmt` to the import block, then append:

```go
// renderToolCardExpanded renders the tool card followed by its output (capped
// at shellExpandMaxLines) under the ⎿ connector. Used by Ctrl+B expansion,
// which anchors to the card block itself.
func renderToolCardExpanded(name, args, output string, width int) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	show := min(len(lines), shellExpandMaxLines)
	rendered := make([]string, show)
	for i := 0; i < show; i++ {
		rendered[i] = dim(clampPlain(lines[i], width-len([]rune(connector))))
	}
	if len(lines) > shellExpandMaxLines {
		rendered = append(rendered, dim(fmt.Sprintf("… %d more lines", len(lines)-shellExpandMaxLines)))
	}
	card := toolCard(name, args, width)
	if block := connectorBlock(rendered); block != "" {
		return card + "\n" + block
	}
	return card
}
```

4. `internal/cli/chat_tui.go` — add the card-index field to `chatTUI` (near `shellExpanded`):

```go
	shellExpanded map[string]bool
	// toolCardIdx maps a dispatched tool id to its card block index, so
	// completion can re-anchor Ctrl+B expansion to the card.
	toolCardIdx map[string]int
```

5. `internal/cli/chat_tui.go` — initialize it next to the `shellExpanded` init (around line 608):

```go
		shellExpanded:        make(map[string]bool),
		toolCardIdx:          make(map[string]int),
```

6. `internal/cli/chat_tui.go` — reset it next to the existing reset (around line 1884):

```go
	m.shellExpanded = make(map[string]bool)
	m.toolCardIdx = make(map[string]int)
```

7. `internal/cli/chat_tui.go` — in the `ToolDispatch` case, record the card index and shell id (replace the card commit, currently around lines 3919-3924):

```go
			m.commitTranscriptSource(transcriptSource{
				kind: transcriptSourceToolCard, raw: e.Tool.Name, aux: e.Tool.Args, shellID: e.Tool.ID,
			})
			m.toolCardIdx[e.Tool.ID] = len(m.transcript) - 1
			m.beginToolRunning(e.Tool.ID)
```

8. `internal/cli/chat_tui.go` — remove the `commitSpacer()` call at the top of the `default:` branch (currently around line 3915) and update the `ToolResult` comment; the `default` branch becomes:

```go
		default:
			if block := diffBlock(e.Tool.Name, e.Tool.Args, e.Tool.FileDiff, m.width, m.diffMaxLines); block != nil {
				for _, ln := range block {
					m.commitLine(ln)
				}
				break
			}
			m.commitTranscriptSource(transcriptSource{
				kind: transcriptSourceToolCard, raw: e.Tool.Name, aux: e.Tool.Args, shellID: e.Tool.ID,
			})
			m.toolCardIdx[e.Tool.ID] = len(m.transcript) - 1
			m.beginToolRunning(e.Tool.ID)
```

9. `internal/cli/chat_tui.go` — update the `ToolResult` case comment and call (around lines 3927-3936):

```go
	case event.ToolResult:
		// A successful result is silent (it only feeds the model); a blocked/failed
		// call surfaces a red "⏺ Verb ⊘ <reason>" card. The live output block is
		// removed entirely so only the card line remains (compact scrollback).
		m.collapseToolOutput(e.Tool.ID)
```

10. `internal/cli/chat_tui.go` — replace `collapseToolOutput` (currently around lines 2251-2305) with:

```go
// collapseToolOutput finalises a finished tool's live block: the block is
// removed so only the ● Verb(arg) card remains (compact scrollback), and the
// Ctrl+B anchor moves to the card block. No-op when id isn't streaming.
func (m *chatTUI) collapseToolOutput(id string) {
	if m.nativeScrollback {
		if id == "" || m.toolStreamID != id {
			return
		}
		m.toolStreamIdx = -1
		m.toolStreamID = ""
		m.toolTail = m.toolTail[:0]
		m.toolPartial = ""
		m.toolLineCount = 0
		return
	}
	idx := -1
	if m.toolStreamIdx >= 0 && id != "" && m.toolStreamID == id {
		idx = m.toolStreamIdx
	} else if i, ok := m.shellTranscriptIdx[id]; ok {
		idx = i
	}
	m.toolStreamIdx = -1
	m.toolStreamID = ""
	m.toolTail = m.toolTail[:0]
	m.toolPartial = ""
	m.toolLineCount = 0
	if idx < 0 || idx >= len(m.transcript) {
		return
	}
	// Remove the finished live block; every later recorded index shifts down.
	m.removeTranscriptBlock(idx)
	for tid, i := range m.shellTranscriptIdx {
		if i > idx {
			m.shellTranscriptIdx[tid] = i - 1
		}
	}
	// Re-anchor Ctrl+B expansion on the tool's card block.
	if cardIdx, ok := m.toolCardIdx[id]; ok && cardIdx >= 0 && cardIdx < len(m.transcript) {
		m.shellTranscriptIdx[id] = cardIdx
		m.shellExpanded[id] = false
	}
}
```

11. `internal/cli/chat_tui.go` — delete `collapseShellSlot` (currently around lines 2307-2370) and replace `toggleShellOutput` (currently around lines 2373-2426) with:

```go
// toggleShellOutput expands or collapses the output of the most recent shell
// command. Expansion is anchored to the tool card block: collapsed shows only
// the card; expanded renders the card plus its output under the ⎿ connector.
// Called on Ctrl+B.
func (m *chatTUI) toggleShellOutput() {
	var lastID string
	lastIdx := -1
	for id, idx := range m.shellTranscriptIdx {
		if idx >= 0 && idx < len(m.transcript) && idx > lastIdx {
			lastID = id
			lastIdx = idx
		}
	}
	if lastID == "" || lastIdx < 0 || lastIdx >= len(m.transcriptSources) {
		return
	}
	src := m.transcriptSources[lastIdx]
	if src.kind != transcriptSourceToolCard || src.shellID != lastID {
		return // still-streaming live block or a stale entry
	}
	if strings.TrimSpace(m.shellOutputs[lastID]) == "" {
		return
	}
	m.shellExpanded[lastID] = !m.shellExpanded[lastID]
	m.setLiveBlock(lastIdx, m.renderTranscriptSource(src, m.width, markerNone))
}
```

12. `internal/cli/chat_tui.go` — the `streamToolOutput` unknown-id branch keeps its `commitLine("")` live-slot creation (the block is the streaming canvas and is removed at completion). Update its comment to say so.

13. If the compiler reports `shellPreviewLines` as unused, keep the constant (package-level unused constants are legal); `toolLineCountByID` assignments may stay.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestToolProgress|TestToolWorkingLine|TestConsecutiveToolCalls|TestCtrlB|TestConsecutiveNonShell|TestToolProgressTailCap|TestRepeatedShellCommand' -count=1`
Expected: PASS

- [ ] **Step 5: Update the transcript connector test**

Run: `go test ./internal/cli/ -run 'Transcript|⎿' -count=1`
If a `transcript_test.go` case renders `⎿` for a tool-card source, update it to render through `renderTranscriptSource` with `shellID` + `shellExpanded` (expanded → connector output; collapsed → bare card).

- [ ] **Step 6: Run the full suite**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS. Also run `go build ./...` — Expected: exit 0.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/transcript.go internal/cli/toolcard.go internal/cli/chat_render_test.go internal/cli/consecutive_tool_markers_test.go
git commit -m "feat(cli): compact tool calls with card-anchored Ctrl+B expansion"
```

---

## Final Verification

- [ ] Run: `go build ./...` — exit 0.
- [ ] Run: `go test ./internal/cli/ -count=1` — all pass.
- [ ] Run: `go test ./... -count=1` (full repo) — all pass (or only pre-existing unrelated failures).
- [ ] Manual smoke check (optional, needs a TTY): `go run ./cmd/corvus` in a terminal; verify the composer has a tinted borderless field, the footer is one row showing `路径 · 模型 · 缓存命中`, tool calls stack tightly after completion, and Ctrl+B expands/collapses the last shell output.
