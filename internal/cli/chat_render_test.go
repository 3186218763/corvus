package cli

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/i18n"
	"corvus/internal/provider"
)

// newTestChatTUI builds a chatTUI with just the pieces the streaming/commit and
// completion paths need, for unit tests that don't run the bubbletea loop.
func newTestChatTUI() chatTUI {
	commit := []string{}
	ti := textarea.New()
	configureChatTextarea(&ti)
	ti.SetWidth(80)
	shellIdx := map[string]int{}
	shellOut := map[string]string{}
	shellExp := map[string]bool{}
	return chatTUI{
		input:                ti,
		width:                80,
		statusLineCount:      2,
		submittedInputCursor: -1,
		queueEditCursor:      -1,
		nextPasteID:          1,
		reasoningLineIdx:     -1,
		reasoningTextIdx:     -1,
		answerIdx:            -1,
		toolStreamIdx:        -1,
		exploreIdx:           -1,
		reasoning:            &strings.Builder{},
		pending:              &strings.Builder{},
		pendingCommit:        &commit,
		shellOutputs:         shellOut,
		shellExpanded:        shellExp,
		shellTranscriptIdx:   shellIdx,
		toolCardIdx:          map[string]int{},
	}
}

func TestCacheRateLabelKeepsTwoDecimals(t *testing.T) {
	if got := cacheRateLabel("turn hit %s", 998, 1000); got != "turn hit 99.80%" {
		t.Fatalf("cacheRateLabel = %q, want turn hit 99.80%%", got)
	}
	if got := cacheRateLabel("avg %s", 1, 3); got != "avg 33.33%" {
		t.Fatalf("cacheRateLabel = %q, want avg 33.33%%", got)
	}
	if got := cacheRateLabel("avg %s", 1, 0); got != "" {
		t.Fatalf("cacheRateLabel with zero denominator = %q, want empty", got)
	}
}

// TestIngestSeparatesReasoningFromAnswer proves default mode keeps thinking off
// the transcript (ambient only), then the answer commits as its own entry.
func TestIngestSeparatesReasoningFromAnswer(t *testing.T) {
	m := newTestChatTUI()

	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "…reasoning…"})
	if len(m.transcript) != 0 {
		t.Fatalf("default mode must not paint thinking into transcript, transcript=%v", m.transcript)
	}
	if m.reasoning.String() != "…reasoning…" {
		t.Fatalf("reasoning should still buffer for verbose, got %q", m.reasoning.String())
	}

	m.ingestEvent(event.Event{Kind: event.Text, Text: "Hello answer"})
	if len(m.transcript) != 0 {
		t.Fatalf("answer stream should not leave thinking residue, transcript=%v", m.transcript)
	}
	if m.pending.String() != "Hello answer" {
		t.Errorf("answer should be live in pending, got %q", m.pending.String())
	}
	if m.reasoning.Len() != 0 {
		t.Errorf("reasoning buffer should be cleared after commit")
	}

	m.commitPending()
	if len(m.transcript) != 1 || !strings.Contains(m.transcript[0], "Hello") {
		t.Fatalf("answer should commit as a separate entry, transcript=%v", m.transcript)
	}
	if plain := ansi.Strip(m.transcript[0]); !strings.HasPrefix(plain, "  Hello answer") {
		t.Fatalf("answer should be plain indented body (no nameplate), got %q", plain)
	}
}

func TestAssistantAnswerWithoutReasoningHasNoLeadingSpacer(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.Text, Text: "Direct answer"})
	m.ingestEvent(event.Event{Kind: event.Message})

	if len(m.transcript) != 1 {
		t.Fatalf("direct answer should remain one compact block, got %d: %v", len(m.transcript), m.transcript)
	}
	if plain := ansi.Strip(m.transcript[0]); !strings.HasPrefix(plain, "  Direct answer") {
		t.Fatalf("direct answer block = %q", plain)
	}
}

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
	m.turnReceipt = renderCacheHitRate(900, 100)

	for _, block := range m.transcript {
		if strings.Contains(ansi.Strip(block), "cached") {
			t.Fatalf("receipt must not stay in the transcript scrollback, got %q", block)
		}
	}
	if !strings.Contains(ansi.Strip(m.turnReceipt), "cached 90.00%") {
		t.Fatalf("turn receipt not captured, got %q", m.turnReceipt)
	}
	// Density pack: cache hit is not permanent footer chrome (lives on /status).
	view := m.View().Content
	if strings.Contains(ansi.Strip(view), "cached 90.00%") {
		t.Fatalf("View must not pin cache receipt on the default footer:\n%s", ansi.Strip(view))
	}
}

// TestVerboseReasoningKeepsTextWithoutSummary proves /verbose mode keeps the
// full thinking text when the thinking marker collapses, with no duration
// summary line.
func TestVerboseReasoningKeepsTextWithoutSummary(t *testing.T) {
	m := newTestChatTUI()
	m.showReasoning = true

	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "step one "})
	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "step two"})
	m.ingestEvent(event.Event{Kind: event.Text, Text: "Answer"}) // closes the block

	if len(m.transcript) != 2 {
		t.Fatalf("verbose block should be text + answer separator, transcript=%v", m.transcript)
	}
	if !strings.Contains(m.transcript[0], "step one") || !strings.Contains(m.transcript[0], "step two") {
		t.Errorf("verbose text should remain after the thinking marker collapses, got %q", m.transcript[0])
	}
	if strings.Contains(m.transcript[0], "thought for") {
		t.Errorf("no duration summary should remain, got %q", m.transcript[0])
	}
	if strings.TrimSpace(m.transcript[1]) != "" {
		t.Errorf("verbose reasoning/answer separator = %q, want blank block", m.transcript[1])
	}
}

// TestVerboseReasoningKeepsOnlyLatestTurn proves that when showReasoning is on,
// starting a new turn's thinking drops prior turns' reasoning blocks so the
// transcript never accumulates every historical thought.
func TestVerboseReasoningKeepsOnlyLatestTurn(t *testing.T) {
	m := newTestChatTUI()
	m.showReasoning = true
	m.width = 80

	// Turn 1
	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "old thought A"})
	m.ingestEvent(event.Event{Kind: event.Text, Text: "Answer one"})
	m.ingestEvent(event.Event{Kind: event.Message})
	if countReasoningSources(m) != 1 {
		t.Fatalf("after turn 1 want 1 reasoning block, got %d sources=%v", countReasoningSources(m), m.transcriptSources)
	}
	if !strings.Contains(strings.Join(m.transcript, "\n"), "old thought A") {
		t.Fatalf("turn 1 reasoning missing: %v", m.transcript)
	}

	// Turn 2 — should prune turn 1 reasoning
	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "new thought B"})
	m.ingestEvent(event.Event{Kind: event.Text, Text: "Answer two"})
	m.ingestEvent(event.Event{Kind: event.Message})

	if n := countReasoningSources(m); n != 1 {
		t.Fatalf("after turn 2 want exactly 1 reasoning block, got %d transcript=%v", n, m.transcript)
	}
	joined := strings.Join(m.transcript, "\n")
	if strings.Contains(joined, "old thought A") {
		t.Fatalf("old reasoning should be pruned, got:\n%s", joined)
	}
	if !strings.Contains(joined, "new thought B") {
		t.Fatalf("latest reasoning should remain, got:\n%s", joined)
	}
}

func countReasoningSources(m chatTUI) int {
	n := 0
	for _, s := range m.transcriptSources {
		if s.kind == transcriptSourceReasoning {
			n++
		}
	}
	return n
}

// TestIngestEventFlushesAnswer confirms an event line (e.g. a tool dispatch)
// finalizes the answer streamed before it, preserving order in scrollback.
func TestIngestEventFlushesAnswer(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.Text, Text: "partial answer "})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "read_file", Args: `{"path":"x"}`}})
	// answer, then the Explored card (no blank spacer — compact tool calls).
	if n := len(*m.pendingCommit); n != 2 {
		t.Fatalf("answer + tool card should be two commits, got %d: %v", n, *m.pendingCommit)
	}
	if !strings.Contains((*m.pendingCommit)[0], "partial answer") {
		t.Errorf("first commit should be the buffered answer, got %q", (*m.pendingCommit)[0])
	}
	joined := strings.Join(*m.pendingCommit, "\n")
	if !strings.Contains(joined, "Explored") || !strings.Contains(joined, "x") {
		t.Errorf("second commit should be the Explored card, got %v", *m.pendingCommit)
	}
	if m.pending.Len() != 0 {
		t.Errorf("answer buffer should be drained after the event line")
	}
}

// TestStreamAnswerFlushesCompletedParagraphs proves a multi-paragraph answer
// appears chunk by chunk: a closed paragraph renders to scrollback while the
// still-streaming one stays buffered, and turn end flushes the remainder.
func TestStreamAnswerFlushesCompletedParagraphs(t *testing.T) {
	m := newTestChatTUI()

	m.ingestEvent(event.Event{Kind: event.Text, Text: "First paragraph.\n\nSecond para "})
	if m.answerIdx < 0 {
		t.Fatalf("a completed paragraph should open a streamed answer block")
	}
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "First paragraph.") {
		t.Errorf("completed paragraph should be on screen, transcript=%v", m.transcript)
	}
	if strings.Contains(joined, "Second para") {
		t.Errorf("the still-streaming paragraph must stay buffered, transcript=%v", m.transcript)
	}

	m.ingestEvent(event.Event{Kind: event.Text, Text: "is done now."})
	m.ingestEvent(event.Event{Kind: event.Message})
	final := strings.Join(m.transcript, "\n")
	if !strings.Contains(final, "First paragraph.") || !strings.Contains(final, "Second para is done now.") {
		t.Errorf("turn end should flush the whole answer, transcript=%v", m.transcript)
	}
	if m.pending.Len() != 0 || m.answerIdx != -1 {
		t.Errorf("answer state should reset after commit, pending=%d idx=%d", m.pending.Len(), m.answerIdx)
	}
}

// TestFlushableMarkdownPrefixKeepsOpenFence proves a blank line inside an unclosed
// fenced code block is not a flush boundary — the half-written block stays buffered
// so it never renders mangled, while prose before the fence does flush.
func TestFlushableMarkdownPrefixKeepsOpenFence(t *testing.T) {
	open := "intro line\n\n```go\nfunc f() {\n\n\t// still typing"
	if got := flushableMarkdownPrefix(open); got != "intro line" {
		t.Errorf("open fence: flushable prefix = %q, want %q", got, "intro line")
	}

	closed := "```go\ncode\n\nmore\n```\n\ntrailing"
	if got := flushableMarkdownPrefix(closed); got != "```go\ncode\n\nmore\n```" {
		t.Errorf("closed fence: flushable prefix = %q", got)
	}

	if got := flushableMarkdownPrefix("no boundary yet"); got != "" {
		t.Errorf("no blank line should flush nothing, got %q", got)
	}
}

// TestToolProgressStreamsThenCollapses proves a running tool's output streams
// live under its card via the └ connector, then vanishes entirely when the
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
	if !strings.Contains(joined, "└") {
		t.Fatalf("live output should use the └ connector:\n%s", joined)
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

// TestToolWorkingLineThenClears proves a dispatched tool that streams no output
// (e.g. symbol_context) shows a live "working · Ns" line so it doesn't look
// frozen, and that the line is removed on the result — no "0 lines", no blank
// slot.
func TestToolWorkingLineThenClears(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "c1", Name: "symbol_context", Args: `{"q":"x"}`}})

	m.tickToolRunning() // one elapsed tick fills the placeholder
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "└") || !strings.Contains(joined, "working") {
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

// TestConsecutiveToolCallsStayCompact is the regression test for back-to-back
// Bash tool calls: each tool's live block is removed when ITS OWN result
// lands, no summary rows remain, and the transcript holds only the two cards
// in dispatch order (no blank spacer, no └ markers).
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
	for _, banned := range []string{"└", "lines", "On branch", "nothing to commit", "* main-v2"} {
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

// TestRepeatedShellCommandDoesNotAccumulateOutput is the regression test for a
// re-run of the same "!" command (e.g. !pwd three times). RunShell derives a
// stable id from the command text ("shell-pwd"), so streamToolOutput kept
// appending each run's output onto the previous run's in m.shellOutputs[id];
// beginToolRunning now clears the entry so each run starts from a clean slate.
func TestRepeatedShellCommandDoesNotAccumulateOutput(t *testing.T) {
	m := newTestChatTUI()
	const id = "shell-pwd"
	const out = "/home/user/project\n"

	for range 3 {
		m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: id, Name: "bash", Args: `{"command":"pwd"}`}})
		m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: id, Output: out}})
		m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: id, Name: "bash", Output: out}})
	}

	if got := m.shellOutputs[id]; got != out {
		t.Fatalf("a re-run must not accumulate prior output: shellOutputs[%q] = %q, want %q", id, got, out)
	}
}

// TestCtrlBTogglesOutputOnTheCardBlock proves Ctrl+B expands the finished
// shell output onto the card block (card + └ output) and collapses back to
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
	if !strings.Contains(got, "line") || !strings.Contains(got, "└") {
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
	if got := m.transcript[0]; strings.Contains(got, "line") || strings.Contains(got, "└") {
		t.Fatalf("collapsed card should be bare, got %q", got)
	}
	if m.shellExpanded[id] {
		t.Fatalf("second toggle must collapse the output")
	}
}

// TestConsecutiveNonShellToolsLeaveNoSlots proves back-to-back read tools
// coalesce into one Explored cell after results — no count summaries, no
// raw file contents left in the transcript.
func TestConsecutiveNonShellToolsLeaveNoSlots(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "read_file-1", Name: "read_file", Args: `{"path":"a.txt"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "read_file-2", Name: "read_file", Args: `{"path":"b.txt"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "read_file-1", Name: "read_file", Output: "a.txt contents"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "read_file-2", Name: "read_file", Output: "b.txt contents"}})

	joined := strings.Join(m.transcript, "\n")
	for _, banned := range []string{"lines", "a.txt contents", "b.txt contents"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("transcript must not contain %q:\n%s", banned, joined)
		}
	}
	if len(m.transcript) != 1 {
		t.Fatalf("consecutive reads should coalesce into one Explored cell, got %d blocks:\n%s", len(m.transcript), joined)
	}
	if !strings.Contains(joined, "Explored") || !strings.Contains(joined, "a.txt") || !strings.Contains(joined, "b.txt") {
		t.Fatalf("Explored cell should list both paths:\n%s", joined)
	}
}

func TestTodoPanelKeepsLastSuccessfulTodoWrite(t *testing.T) {
	m := newTestChatTUI()
	initial := `{"todos":[{"content":"Sync main-v2","status":"in_progress"},{"content":"Push origin","status":"pending"}]}`
	failed := `{"todos":[{"content":"Sync main-v2","status":"completed"},{"content":"Push origin","status":"in_progress"}]}`

	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "todo-1", Name: "todo_write", Args: initial}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "todo-1", Name: "todo_write", Args: initial, Output: "Todos updated"}})
	if m.todoArgs != initial {
		t.Fatalf("todoArgs after successful result = %q, want initial args", m.todoArgs)
	}

	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "todo-2", Name: "todo_write", Args: failed}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "todo-2", Name: "todo_write", Args: failed, Err: "missing complete_step"}})
	if m.todoArgs != initial {
		t.Fatalf("failed todo_write must not replace the panel: got %q, want %q", m.todoArgs, initial)
	}
}

// TestToolProgressTailCap proves the live block only keeps the last
// toolStreamTailLines lines so a chatty build doesn't flood scrollback.
func TestToolProgressTailCap(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "b1", Name: "bash", Args: `{"command":"x"}`}})
	for i := 0; i < toolStreamTailLines+5; i++ {
		m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "b1", Output: "line" + string(rune('A'+i)) + "\n"}})
	}
	block := m.transcript[m.toolStreamIdx]
	if got := strings.Count(block, "\n") + 1; got > toolStreamTailLines {
		t.Fatalf("live block kept %d lines, want <= %d:\n%s", got, toolStreamTailLines, block)
	}
	if strings.Contains(block, "lineA") {
		t.Fatalf("oldest line should have scrolled out of the tail:\n%s", block)
	}
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "b1", Name: "bash", Output: "lineA\n"}})
	if got := strings.Join(m.transcript, "\n"); strings.Contains(got, "line") {
		t.Fatalf("completed tool output must be removed:\n%s", got)
	}
}

// TestReasoningViewBounded proves verbose live thinking stays bounded under a
// long stream — the fix for the O(n²)/multi-GB re-render of the full thought.
func TestReasoningViewBounded(t *testing.T) {
	m := newTestChatTUI()
	m.showReasoning = true
	for i := 0; i < 5000; i++ {
		m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "some thinking text token "})
	}
	if len(m.reasoningView) > reasoningViewMax {
		t.Fatalf("reasoningView unbounded: %d > %d", len(m.reasoningView), reasoningViewMax)
	}
	if m.reasoningTextIdx < 0 {
		t.Fatal("verbose mode should open a live reasoning block")
	}
	if c := strings.Count(m.transcript[m.reasoningTextIdx], "\n") + 1; c > reasoningTailLines {
		t.Fatalf("live reasoning block kept %d lines, want <= %d", c, reasoningTailLines)
	}
}

func TestRenderTUIBannerWideAndNarrow(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

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
	if !strings.Contains(narrow, "corvus") {
		t.Fatalf("narrow banner should keep the wordmark, got %q", narrow)
	}
	// A long label must actually truncate to the target width, not overflow.
	truncated := renderTUIBanner(strings.Repeat("long-label-", 8), "", 40)
	if strings.Count(truncated, "\n") != 0 || !strings.Contains(truncated, "…") {
		t.Fatalf("narrow banner should truncate a long label, got %q", truncated)
	}
	if w := ansi.StringWidth(truncated); w > 40 {
		t.Fatalf("truncated banner width %d exceeds 40", w)
	}
	// 60 is the wide/narrow gate.
	if got := strings.Count(renderTUIBanner("model-x", "", 59), "\n"); got != 0 {
		t.Fatalf("width 59 must be narrow (single line), got %d lines", got)
	}
	if got := strings.Count(renderTUIBanner("model-x", "", 60), "\n"); got < 1 {
		t.Fatalf("width 60 must be wide (tip line present), got %d lines", got)
	}
}

// TestLateResultDoesNotClobberActiveStream is the regression test for a
// ToolResult arriving for an earlier tool while another tool is still
// streaming: the late result removes only its own stale block and must not
// reset the active stream's state (which would freeze its working line and
// orphan its live block).
func TestLateResultDoesNotClobberActiveStream(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-a", Name: "bash", Args: `{"command":"slow"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-a", Output: "a1\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-b", Name: "bash", Args: `{"command":"faster"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-b", Output: "b1\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-a", Name: "bash", Output: "a1\n"}})

	if m.toolStreamID != "shell-b" || m.toolStreamIdx < 0 {
		t.Fatalf("late result clobbered the active stream: id=%q idx=%d", m.toolStreamID, m.toolStreamIdx)
	}
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "b1") {
		t.Fatalf("active tool's live output must survive the late result:\n%s", joined)
	}
	if strings.Contains(joined, "a1") {
		t.Fatalf("late tool's stale block must be removed:\n%s", joined)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-b", Name: "bash", Output: "b1\n"}})
	if m.toolStreamID != "" || m.toolStreamIdx != -1 {
		t.Fatalf("active stream should close on its own result, id=%q idx=%d", m.toolStreamID, m.toolStreamIdx)
	}
	if got := len(m.transcript); got != 2 {
		t.Fatalf("only the two cards should remain, got %d blocks:\n%s", got, strings.Join(m.transcript, "\n"))
	}
}

// TestToolCardAnchorsFollowRemovedBlocks proves Ctrl+B anchors stay correct
// when back-to-back results remove live blocks: every tool's shellTranscriptIdx
// ends on its own card, and Ctrl+B expands the most recent card.
func TestToolCardAnchorsFollowRemovedBlocks(t *testing.T) {
	m := newTestChatTUI()
	for _, tc := range []struct{ id, cmd string }{
		{"shell-a", `{"command":"a"}`},
		{"shell-b", `{"command":"b"}`},
		{"shell-c", `{"command":"c"}`},
	} {
		m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: tc.id, Name: "bash", Args: tc.cmd}})
		m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: tc.id, Output: tc.id + "-out\n"}})
	}
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-a", Name: "bash", Output: "a-out\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-b", Name: "bash", Output: "b-out\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-c", Name: "bash", Output: "c-out\n"}})

	if got := len(m.transcript); got != 3 {
		t.Fatalf("expected exactly the three cards, got %d blocks:\n%s", got, strings.Join(m.transcript, "\n"))
	}
	for id, want := range map[string]int{"shell-a": 0, "shell-b": 1, "shell-c": 2} {
		if got := m.shellTranscriptIdx[id]; got != want {
			t.Fatalf("Ctrl+B anchor for %s = %d, want %d", id, got, want)
		}
	}
	m.toggleShellOutput()
	if !m.shellExpanded["shell-c"] {
		t.Fatalf("Ctrl+B should expand the most recent card, expanded=%v", m.shellExpanded)
	}
	if got := m.transcript[2]; !strings.Contains(got, "c-out") || !strings.Contains(got, "└") {
		t.Fatalf("most recent card should carry its output when expanded, got %q", got)
	}
}

// TestSameIDRerunDoesNotInheritExpansion is the regression test for re-running
// a command with a stable tool id (e.g. !pwd): the fresh card must render
// collapsed and must not show the previous run's output.
func TestSameIDRerunDoesNotInheritExpansion(t *testing.T) {
	m := newTestChatTUI()
	const id = "shell-pwd"
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: id, Name: "bash", Args: `{"command":"pwd"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: id, Output: "/one\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: id, Name: "bash", Output: "/one\n"}})
	m.toggleShellOutput()
	if got := m.transcript[0]; !strings.Contains(got, "/one") {
		t.Fatalf("expanded card should show the output, got %q", got)
	}

	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: id, Name: "bash", Args: `{"command":"pwd"}`}})
	if m.shellExpanded[id] {
		t.Fatalf("re-dispatch must clear the previous run's expanded state")
	}
	if got := m.transcript[1]; strings.Contains(got, "/one") || strings.Contains(got, "└") {
		t.Fatalf("fresh card must render collapsed without stale output, got %q", got)
	}
}

// TestNativeScrollbackPrintsFinishedOutput proves Termux keeps tool output
// visible: the finished shell output is committed to the transcript (which is
// printed to the native scrollback) at result time, with no line-count summary.
func TestNativeScrollbackPrintsFinishedOutput(t *testing.T) {
	m := newTestChatTUI()
	m.nativeScrollback = true
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-pwd", Name: "bash", Args: `{"command":"pwd"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-pwd", Output: "/home/user\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-pwd", Name: "bash", Output: "/home/user\n"}})

	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "/home/user") {
		t.Fatalf("native scrollback should show the finished output:\n%s", joined)
	}
	if strings.Contains(joined, "lines") {
		t.Fatalf("no line-count summary in native mode:\n%s", joined)
	}
	if m.toolStreamID != "" || m.toolStreamIdx != -1 {
		t.Fatalf("stream state should reset after the result, id=%q idx=%d", m.toolStreamID, m.toolStreamIdx)
	}
}

// TestNativeScrollbackLateResultStillPrints proves a native-mode result that
// arrives while another tool is streaming prints that tool's output exactly
// once, without touching the active stream's state.
func TestNativeScrollbackLateResultStillPrints(t *testing.T) {
	m := newTestChatTUI()
	m.nativeScrollback = true
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-a", Name: "bash", Args: `{"command":"a"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-a", Output: "a-out\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-b", Name: "bash", Args: `{"command":"b"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-b", Output: "b-out\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-a", Name: "bash", Output: "a-out\n"}})

	if m.toolStreamID != "shell-b" {
		t.Fatalf("late result must not touch the active stream, id=%q", m.toolStreamID)
	}
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "a-out") {
		t.Fatalf("late tool's output should still print in native mode:\n%s", joined)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-a", Name: "bash", Output: "a-out\n"}})
	if got := strings.Count(strings.Join(m.transcript, "\n"), "a-out"); got != 1 {
		t.Fatalf("tool output must print at most once, got %d copies", got)
	}
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-b", Name: "bash", Output: "b-out\n"}})
	if got := strings.Join(m.transcript, "\n"); !strings.Contains(got, "b-out") {
		t.Fatalf("active tool's output should print on its result:\n%s", got)
	}
	if m.toolStreamID != "" || m.toolStreamIdx != -1 {
		t.Fatalf("stream state should reset after the active result, id=%q idx=%d", m.toolStreamID, m.toolStreamIdx)
	}
}

// TestAnswerStreamSurvivesToolBlockRemoval is the regression test for a tool
// result that removes its live block while the assistant answer is streaming
// below it: the answer block index must shift so later chunks keep landing in
// the same block instead of being dropped.
func TestAnswerStreamSurvivesToolBlockRemoval(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-a", Name: "bash", Args: `{"command":"a"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-a", Output: "tool-out\n"}})
	m.ingestEvent(event.Event{Kind: event.Text, Text: "First para.\n\nSecond "})
	if m.answerIdx != 2 {
		t.Fatalf("answer block should open below the tool's live block, idx=%d", m.answerIdx)
	}
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-a", Name: "bash", Output: "tool-out\n"}})
	if m.answerIdx != 1 {
		t.Fatalf("answerIdx should shift down when the tool block is removed, got %d", m.answerIdx)
	}
	m.ingestEvent(event.Event{Kind: event.Text, Text: "complete.\n\n"})
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "Second complete.") {
		t.Fatalf("streamed answer must keep appending after the tool block removal:\n%s", joined)
	}
}

// TestReasoningStreamSurvivesToolBlockRemoval is the regression for a tool
// result that removes its live block while reasoning is buffering: default
// mode keeps thinking ambient (no transcript wall) and still clears cleanly.
func TestReasoningStreamSurvivesToolBlockRemoval(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "shell-a", Name: "bash", Args: `{"command":"a"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "shell-a", Output: "tool-out\n"}})
	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "think "})
	if m.reasoningTextIdx != -1 || m.reasoningLineIdx != -1 {
		t.Fatalf("default reasoning must stay ambient, line=%d text=%d", m.reasoningLineIdx, m.reasoningTextIdx)
	}
	if m.reasoning.String() != "think " {
		t.Fatalf("reasoning should buffer, got %q", m.reasoning.String())
	}
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "shell-a", Name: "bash", Output: "tool-out\n"}})
	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "more"})
	if m.reasoning.String() != "think more" {
		t.Fatalf("reasoning buffer must keep appending after tool removal, got %q", m.reasoning.String())
	}
	if strings.Contains(strings.Join(m.transcript, "\n"), "think more") {
		t.Fatalf("default mode must not paint reasoning into transcript:\n%s", strings.Join(m.transcript, "\n"))
	}
	m.ingestEvent(event.Event{Kind: event.Message})
	if m.reasoningTextIdx != -1 || m.reasoningLineIdx != -1 || m.reasoning.Len() != 0 {
		t.Fatalf("reasoning should clear on message boundary, line=%d text=%d buf=%q", m.reasoningLineIdx, m.reasoningTextIdx, m.reasoning.String())
	}
}
