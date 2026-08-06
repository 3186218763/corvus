package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/charmbracelet/x/ansi"

	"corvus/internal/provider"
)

func TestAssistantMarkdownHasIdentityAndIndentedBody(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")

	rendered := renderAssistantMarkdown("A concise answer that wraps across the available width.", 32, true)
	lines := strings.Split(ansi.Strip(rendered), "\n")
	if len(lines) < 4 {
		t.Fatalf("assistant block should contain a header, gap, and wrapped body:\n%s", rendered)
	}
	if lines[0] != "  ◆ Corvus" {
		t.Fatalf("assistant header = %q, want %q", lines[0], "  ◆ Corvus")
	}
	if lines[1] != "" {
		t.Fatalf("assistant header/body separator = %q, want blank row", lines[1])
	}
	for i, line := range lines[2:] {
		if line != "" && !strings.HasPrefix(line, assistantTranscriptIndent) {
			t.Fatalf("assistant body row %d lacks the two-cell gutter: %q", i+2, line)
		}
		if width := visibleWidth(line); width > 32 {
			t.Fatalf("assistant row %d width = %d, want <= 32: %q", i+2, width, line)
		}
	}
}

func TestReplaySectionsKeepAssistantIdentity(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")

	sections := replaySectionsFor([]provider.Message{
		{Role: provider.RoleUser, Content: "Which version?"},
		{Role: provider.RoleAssistant, Content: "Version 1.2.3"},
	}, 48)
	if len(sections) != 2 {
		t.Fatalf("replay sections = %d, want user and assistant", len(sections))
	}
	if plain := ansi.Strip(sections[1]); !strings.HasPrefix(plain, "  ◆\n\n  Version 1.2.3") {
		t.Fatalf("demoted replay should keep a bare diamond and drop the name: %q", plain)
	}
}

func TestReplaySectionsRestoreInterruptedLocalOutput(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")

	sections := replaySectionsFor([]provider.Message{
		{Role: provider.RoleUser, Content: "change config"},
		{
			Role: provider.RoleTool, ToolCallID: provider.LocalOnlyToolID, Name: provider.LocalOnlyToolName,
			LocalOnly: true, Content: "partial answer", ReasoningContent: "checking config",
			ToolCalls:       []provider.ToolCall{{ID: "p1", Name: "write_file"}},
			InterruptedTurn: &provider.InterruptedTurnRecovery{Pending: true},
		},
	}, 64)
	plain := ansi.Strip(strings.Join(sections, ""))
	for _, want := range []string{"change config", "checking config", "partial answer", "Write", "bounded recovery summary"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("replayed interrupted history missing %q:\n%s", want, plain)
		}
	}
}

func TestScrollbarThumb(t *testing.T) {
	if _, size := scrollbarThumb(10, 0, 5); size != 0 {
		t.Errorf("content within viewport should have no thumb, got size %d", size)
	}
	if start, _ := scrollbarThumb(10, 0, 100); start != 0 {
		t.Errorf("at top the thumb starts at row 0, got %d", start)
	}
	const h, total = 10, 100
	if start, size := scrollbarThumb(h, total-h, total); start+size != h {
		t.Errorf("at bottom the thumb reaches the last row: start=%d size=%d h=%d", start, size, h)
	}
}

func TestEdgeScrollDir(t *testing.T) {
	const h = 10
	if got := edgeScrollDir(0, h); got != -1 {
		t.Errorf("top edge dir = %d, want -1", got)
	}
	if got := edgeScrollDir(h-1, h); got != 1 {
		t.Errorf("bottom edge dir = %d, want 1", got)
	}
	if got := edgeScrollDir(h/2, h); got != 0 {
		t.Errorf("middle dir = %d, want 0", got)
	}
}

func TestSelSpan(t *testing.T) {
	start, end, cw := selPos{line: 1, col: 3}, selPos{line: 3, col: 5}, 20
	for _, tc := range []struct {
		idx         int
		wantOK      bool
		wantLo, wHi int
	}{
		{0, false, 0, 0}, // above
		{1, true, 3, cw}, // first line: anchor col → right edge
		{2, true, 0, cw}, // middle line: full width
		{3, true, 0, 5},  // last line: left edge → head col
		{4, false, 0, 0}, // below
	} {
		lo, hi, ok := selSpan(tc.idx, start, end, cw)
		if ok != tc.wantOK || (ok && (lo != tc.wantLo || hi != tc.wHi)) {
			t.Errorf("selSpan(%d) = (%d,%d,%v), want (%d,%d,%v)", tc.idx, lo, hi, ok, tc.wantLo, tc.wHi, tc.wantOK)
		}
	}
}

func TestSelectedTextMultiLine(t *testing.T) {
	m := newTestChatTUI()
	m.wrappedLines = []string{"hello world", "second line", "third row"}
	m.sel = selection{active: true, anchor: selPos{line: 0, col: 6}, head: selPos{line: 2, col: 5}}

	if got, want := m.selectedText(), "world\nsecond line\nthird"; got != want {
		t.Errorf("selectedText() = %q, want %q", got, want)
	}

	// A zero-width selection (plain click) copies nothing.
	m.sel = selection{active: true, anchor: selPos{line: 0, col: 3}, head: selPos{line: 0, col: 3}}
	if got := m.selectedText(); got != "" {
		t.Errorf("empty selection should yield no text, got %q", got)
	}
}

func TestSelectedTextRestoresMathWithoutReusingRawColumns(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.width = 80
	contentWidth := transcriptContentWidth(m.width, m.nativeScrollback)
	m.viewport.SetWidth(contentWidth)
	source := transcriptSource{kind: transcriptSourceMarkdown, raw: `before $\alpha$ after`}
	rendered := m.renderTranscriptSource(source, m.width, currentTranscriptMarkers([]transcriptSource{source})[0])
	m.transcript = []string{rendered}
	m.transcriptSources = []transcriptSource{source}
	m.wrappedLines = strings.Split(wrapTranscript(rendered, contentWidth), "\n")

	lineIndex := -1
	for i, line := range m.wrappedLines {
		if strings.Contains(ansi.Strip(line), "before α after") {
			lineIndex = i
			break
		}
	}
	if lineIndex < 0 {
		t.Fatalf("rendered transcript did not contain the math line:\n%s", ansi.Strip(rendered))
	}

	plain := ansi.Strip(m.wrappedLines[lineIndex])
	formulaByte := strings.Index(plain, "α")
	afterByte := strings.Index(plain, "after")
	if formulaByte < 0 || afterByte < 0 {
		t.Fatalf("math line = %q", plain)
	}
	formulaCol := ansi.StringWidth(plain[:formulaByte])
	afterCol := ansi.StringWidth(plain[:afterByte])

	m.sel = selection{
		active: true,
		anchor: selPos{line: lineIndex, col: formulaCol},
		head:   selPos{line: lineIndex, col: formulaCol + ansi.StringWidth("α")},
	}
	if got, want := m.selectedText(), `$\alpha$`; got != want {
		t.Fatalf("formula selection = %q, want %q", got, want)
	}

	m.sel = selection{
		active: true,
		anchor: selPos{line: lineIndex, col: afterCol},
		head:   selPos{line: lineIndex, col: afterCol + ansi.StringWidth("after")},
	}
	if got, want := m.selectedText(), "after"; got != want {
		t.Fatalf("text after formula = %q, want %q", got, want)
	}
}

func TestSelectedTextRestoresMathFromReplayBundle(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.width = 80
	contentWidth := transcriptContentWidth(m.width, m.nativeScrollback)
	m.viewport.SetWidth(contentWidth)
	source := transcriptSource{
		kind: transcriptSourceReplayBundle,
		history: []provider.Message{
			{Role: provider.RoleAssistant, Content: `before $\alpha$ after`},
			{LocalOnly: true, Content: `local $\beta$ recovery`},
		},
	}
	rendered := m.renderTranscriptSource(source, m.width, currentTranscriptMarkers([]transcriptSource{source})[0])
	m.transcript = []string{rendered}
	m.transcriptSources = []transcriptSource{source}
	m.wrappedLines = strings.Split(wrapTranscript(rendered, contentWidth), "\n")

	lineIndex := -1
	formulaCol := -1
	for i, line := range m.wrappedLines {
		plain := ansi.Strip(line)
		formulaByte := strings.Index(plain, "α")
		if formulaByte < 0 {
			continue
		}
		lineIndex = i
		formulaCol = ansi.StringWidth(plain[:formulaByte])
		break
	}
	if lineIndex < 0 {
		t.Fatalf("rendered replay bundle did not contain the formula:\n%s", ansi.Strip(rendered))
	}

	m.sel = selection{
		active: true,
		anchor: selPos{line: lineIndex, col: formulaCol},
		head:   selPos{line: lineIndex, col: formulaCol + ansi.StringWidth("α")},
	}
	if got, want := m.selectedText(), `$\alpha$`; got != want {
		t.Fatalf("replayed formula selection = %q, want %q", got, want)
	}

	copyLines, ok := m.copyTranscriptLines()
	if !ok {
		t.Fatal("copy rendition diverged from the displayed replay bundle")
	}
	sourcesByID := make(map[string]string)
	for _, line := range copyLines {
		for _, span := range line.math {
			if source, exists := sourcesByID[span.id]; exists && source != span.source {
				t.Fatalf("formula marker %q reused for %q and %q", span.id, source, span.source)
			}
			sourcesByID[span.id] = span.source
		}
	}
	if len(sourcesByID) != 2 {
		t.Fatalf("replay formula markers = %v, want two unique formulas", sourcesByID)
	}
	foundSources := make(map[string]bool)
	for _, source := range sourcesByID {
		foundSources[source] = true
	}
	for _, want := range []string{`$\alpha$`, `$\beta$`} {
		if !foundSources[want] {
			t.Fatalf("replay formula markers = %v, missing %q", sourcesByID, want)
		}
	}
}

func TestSelectedTextPreservesProseAroundMath(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.width = 80
	contentWidth := transcriptContentWidth(m.width, m.nativeScrollback)
	m.viewport.SetWidth(contentWidth)
	source := transcriptSource{kind: transcriptSourceMarkdown, raw: `before $\frac{1}{2}$ after`}
	rendered := m.renderTranscriptSource(source, m.width, currentTranscriptMarkers([]transcriptSource{source})[0])
	m.transcript = []string{rendered}
	m.transcriptSources = []transcriptSource{source}
	m.wrappedLines = strings.Split(wrapTranscript(rendered, contentWidth), "\n")

	for i, line := range m.wrappedLines {
		plain := ansi.Strip(line)
		startByte := strings.Index(plain, "before")
		endByte := strings.Index(plain, " after")
		if startByte < 0 || endByte < 0 {
			continue
		}
		startCol := ansi.StringWidth(plain[:startByte])
		endCol := ansi.StringWidth(plain[:endByte+len(" after")])
		m.sel = selection{
			active: true,
			anchor: selPos{line: i, col: startCol},
			head:   selPos{line: i, col: endCol},
		}
		if got, want := m.selectedText(), `before $\frac{1}{2}$ after`; got != want {
			t.Fatalf("mixed selection = %q, want %q", got, want)
		}
		return
	}
	t.Fatalf("rendered transcript did not contain the expected mixed line:\n%s", ansi.Strip(rendered))
}

func TestSelectedTextRestoresMathWrappedAcrossDisplayLinesOnce(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.width = 10
	contentWidth := transcriptContentWidth(m.width, m.nativeScrollback)
	m.viewport.SetWidth(contentWidth)
	const latex = `\alpha+\beta+\gamma+\delta+\epsilon+\zeta`
	source := transcriptSource{kind: transcriptSourceMarkdown, raw: `$` + latex + `$`}
	rendered := m.renderTranscriptSource(source, m.width, currentTranscriptMarkers([]transcriptSource{source})[0])
	m.transcript = []string{rendered}
	m.transcriptSources = []transcriptSource{source}
	m.wrappedLines = strings.Split(wrapTranscript(rendered, contentWidth), "\n")

	copyLines, ok := m.copyTranscriptLines()
	if !ok {
		t.Fatal("copy rendition diverged from the displayed transcript")
	}
	firstLine, lastLine := -1, -1
	firstCol, lastCol := 0, 0
	for i, line := range copyLines {
		if len(line.math) == 0 {
			continue
		}
		if firstLine < 0 {
			firstLine = i
			firstCol = line.math[0].start
		}
		lastLine = i
		lastCol = line.math[len(line.math)-1].end
	}
	if firstLine < 0 || lastLine <= firstLine {
		t.Fatalf("expected formula to wrap across lines:\n%s", ansi.Strip(rendered))
	}

	m.sel = selection{
		active: true,
		anchor: selPos{line: firstLine, col: firstCol},
		head:   selPos{line: lastLine, col: lastCol},
	}
	if got, want := m.selectedText(), `$`+latex+`$`; got != want {
		t.Fatalf("wrapped formula selection = %q, want %q", got, want)
	}
}

func TestCopyToClipboard(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")
	previous := writeNativeClipboardText
	t.Cleanup(func() { writeNativeClipboardText = previous })

	var written string
	writeNativeClipboardText = func(text string) error {
		written = text
		return nil
	}
	message := copyToClipboard("hello")()
	got, ok := message.(clipboardCopyMsg)
	if !ok {
		t.Fatalf("copyToClipboard returned %T, want clipboardCopyMsg", message)
	}
	if written != "hello" || got.text != "hello" || got.err != nil || got.osc52 {
		t.Fatalf("native clipboard result = %+v, written %q", got, written)
	}

	wantErr := errors.New("clipboard unavailable")
	writeNativeClipboardText = func(string) error { return wantErr }
	got = copyToClipboard("fallback")().(clipboardCopyMsg)
	if !errors.Is(got.err, wantErr) || got.osc52 {
		t.Fatalf("failed native clipboard result = %+v", got)
	}

	t.Setenv("SSH_CONNECTION", "host 22 client 1234")
	writeNativeClipboardText = func(string) error {
		t.Fatal("SSH copy must not write the remote host's native clipboard")
		return nil
	}
	got = copyToClipboard("remote")().(clipboardCopyMsg)
	if !got.osc52 || got.text != "remote" {
		t.Fatalf("SSH clipboard result = %+v, want OSC 52", got)
	}
}

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

	// Truncate to 3 blocks (transcript-level op keeps the cache in sync).
	m.truncateTranscriptBlocks(3)
	blocks = blocks[:3]
	if !reflect.DeepEqual(m.wrappedLines, fullWrap(blocks, width)) {
		t.Fatalf("truncate mismatch")
	}

	// Remove block 1 (transcript-level op keeps the cache in sync).
	m.removeTranscriptBlock(1)
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
	// lipgloss may normalize the reset to "\x1b[m", so accept either form.
	for _, ln := range m.wrappedLines {
		if strings.Contains(ln, "\x1b[2m") && !strings.Contains(ln, "\x1b[0m") && !strings.Contains(ln, "\x1b[m") {
			t.Fatalf("dangling SGR in %q", ln)
		}
	}
}

// TestClearTranscriptDisplayResetsWrapCache proves a display clear drops the
// whole wrap cache together with any pending re-wrap indices, so the next
// append starts from a clean slate instead of re-wrapping stale blocks.
func TestClearTranscriptDisplayResetsWrapCache(t *testing.T) {
	m := newTestChatTUI()
	m.appendTranscriptBlock("block a", transcriptSource{kind: transcriptSourceFixed})
	m.appendTranscriptBlock("block b", transcriptSource{kind: transcriptSourceFixed})
	m.appendWrappedBlocks(0, 80)
	m.setLiveBlock(0, "block a'")
	if len(m.wrappedLines) == 0 || len(m.blockLineCounts) == 0 || len(m.liveDirtyIdx) == 0 {
		t.Fatalf("precondition: wrap cache should be populated and dirty, got wrappedLines=%d blockLineCounts=%d liveDirtyIdx=%v",
			len(m.wrappedLines), len(m.blockLineCounts), m.liveDirtyIdx)
	}
	m.clearTranscriptDisplay()
	if len(m.wrappedLines) != 0 || len(m.blockLineCounts) != 0 || len(m.liveDirtyIdx) != 0 {
		t.Fatalf("clearTranscriptDisplay should reset the wrap cache, got wrappedLines=%d blockLineCounts=%d liveDirtyIdx=%v",
			len(m.wrappedLines), len(m.blockLineCounts), m.liveDirtyIdx)
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
	// Wrapped lines are padded to width like the full wrap; the text content
	// must survive wrapping.
	if got := strings.TrimRight(ansi.Strip(wrapBlock("\x1b[2mdimmed\x1b[0m", 80)[0]), " "); got != "dimmed" {
		t.Fatalf("ANSI stripped wrong: %q", got)
	}
}

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

func TestAssistantMarkdownHistoryDropsName(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	named := renderAssistantMarkdown("Live answer", 48, true)
	if plain := ansi.Strip(named); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("named header should keep the name, got %q", plain)
	}
	history := renderAssistantMarkdown("History answer", 48, false)
	plain := ansi.Strip(history)
	if strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("history header must not carry the name, got %q", plain)
	}
	if !strings.HasPrefix(plain, "  ◆") {
		t.Fatalf("history header should keep the diamond, got %q", plain)
	}
	if !strings.Contains(history, fgSGR(activeCLITheme.faint)) {
		t.Fatalf("history diamond should be faint-colored, got %q", history)
	}
}

func TestUserBubbleFadedHistory(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	current := renderUserBubble("now", 80, false, true)
	if !strings.Contains(current, fgSGR(activeCLITheme.accent)) {
		t.Fatalf("current bubble should use accent SGR, got %q", current)
	}
	faded := renderUserBubble("then", 80, false, false)
	if !strings.Contains(faded, fgSGR(activeCLITheme.userBubbleFaded)) {
		t.Fatalf("history bubble should use userBubbleFaded SGR, got %q", faded)
	}
	if strings.Contains(faded, fgSGR(activeCLITheme.accent)) {
		t.Fatalf("history bubble must not use full accent, got %q", faded)
	}
}

func TestSecondExchangeDemotesFirst(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "first question"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "first answer"})
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("first answer should be named, got %q", plain)
	}
	if !strings.Contains(m.transcript[0], fgSGR(activeCLITheme.accent)) {
		t.Fatalf("first bubble should be current/accent, got %q", m.transcript[0])
	}

	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "second question"})
	if !strings.Contains(m.transcript[0], fgSGR(activeCLITheme.userBubbleFaded)) {
		t.Fatalf("first bubble should be faded after turn 2, got %q", m.transcript[0])
	}
	if strings.Contains(ansi.Strip(m.transcript[1]), "Corvus") {
		t.Fatalf("first answer must lose the name after turn 2, got %q", m.transcript[1])
	}

	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "second answer"})
	if plain := ansi.Strip(m.transcript[3]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("second answer should be named, got %q", plain)
	}
}

// TestNonLiveCommitsKeepMarkers covers the banner (/new, /cls) and tool-card
// commitTranscriptSource call sites: neither may demote the live exchange.
func TestNonLiveCommitsKeepMarkers(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "a"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceBanner})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceToolCard, raw: "bash", aux: `{"command":"ls"}`})
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("banner/tool commits must not demote the live answer, got %q", plain)
	}
	if !strings.Contains(m.transcript[0], fgSGR(activeCLITheme.accent)) {
		t.Fatalf("user bubble should stay current, got %q", m.transcript[0])
	}
}

func TestUnsendRegainsAssistantName(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q1"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "a1"})
	m.bubbleStartIdx = len(m.transcript)
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q2"})
	if plain := ansi.Strip(m.transcript[1]); strings.Contains(plain, "Corvus") {
		t.Fatalf("precondition: a1 should be demoted while q2 is pending, got %q", plain)
	}
	m.truncateTranscriptBlocks(m.bubbleStartIdx)
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("after un-send the previous answer should regain its name, got %q", plain)
	}
}

func TestRemoveLastAnswerRetagsPrevious(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "a1"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "a2"})
	if plain := ansi.Strip(m.transcript[2]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("a2 should be named, got %q", plain)
	}
	m.removeTranscriptBlock(2)
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("after removing a2, a1 should regain the name, got %q", plain)
	}
}

func TestReflowPreservesMarkers(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.width = 80
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "answer"})
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("precondition: answer should be named, got %q", plain)
	}
	m.reflowTranscript(40)
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("reflow must preserve the named marker, got %q", plain)
	}

	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q2"})
	m.reflowTranscript(60)
	if strings.Contains(ansi.Strip(m.transcript[1]), "Corvus") {
		t.Fatalf("reflow must preserve demotion, got %q", m.transcript[1])
	}
	if !strings.Contains(m.transcript[2], fgSGR(activeCLITheme.accent)) {
		t.Fatalf("reflow must preserve the user current marker, got %q", m.transcript[2])
	}
}

func TestReplayBundleInternalLiveness(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	history := []provider.Message{
		{Role: provider.RoleUser, Content: "old question"},
		{Role: provider.RoleAssistant, Content: "old answer"},
		{Role: provider.RoleUser, Content: "latest question"},
		{Role: provider.RoleAssistant, Content: "latest answer"},
	}

	// Live bundle committed through the production path: last internal
	// assistant named, last internal user full accent.
	m := newTestChatTUI()
	m.label = "model-x"
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceReplayBundle, history: history})
	live := strings.Join(m.transcript, "\n")
	if !strings.Contains(ansi.Strip(live), "◆ Corvus\n\n  latest answer") {
		t.Fatalf("live bundle should name the last assistant body, got %q", live)
	}
	if strings.Contains(ansi.Strip(live), "Corvus\n\n  old answer") {
		t.Fatalf("live bundle must not name earlier assistant bodies, got %q", live)
	}
	if !strings.Contains(live, fgSGR(activeCLITheme.accent)+"› latest question") {
		t.Fatalf("live bundle should render the last user full accent, got %q", live)
	}
	if !strings.Contains(live, fgSGR(activeCLITheme.userBubbleFaded)) {
		t.Fatalf("live bundle should fade earlier user bubbles, got %q", live)
	}

	// A new user message demotes the whole bundle.
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "new question"})
	if plain := ansi.Strip(strings.Join(m.transcript, "\n")); strings.Contains(plain, "Corvus") {
		t.Fatalf("bundle must carry no name after a new user message, got %q", plain)
	}
	if strings.Contains(strings.Join(m.transcript, "\n"), fgSGR(activeCLITheme.accent)+"› latest question") {
		t.Fatalf("bundle internal user must fade after a new user message, got %q", strings.Join(m.transcript, "\n"))
	}
}
