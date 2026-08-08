package cli

import (
	"strings"
	"testing"

	"corvus/internal/event"
)

// TestParallelBashCallsStayCompact is the compact parallel-Bash regression:
// three parallel Bash(ls) calls in one turn, ids "call_<n>" (no "shell-"
// prefix), each streams 22 lines then finishes. Each card's live block is
// removed when ITS OWN result lands, leaving only the three cards — no
// "└ N lines" summaries, no stacked output slots, no negative counts.
func TestParallelBashCallsStayCompact(t *testing.T) {
	m := newTestChatTUI()
	ids := []string{"call_1", "call_2", "call_3"}
	for _, id := range ids {
		m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: id, Name: "bash", Partial: true}})
	}
	for _, id := range ids {
		m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: id, Name: "bash", Args: `{"command":"ls"}`, Partial: false}})
	}
	for _, id := range ids {
		for i := 0; i < 22; i++ {
			m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: id, Output: "line\n"}})
		}
	}
	for _, id := range ids {
		m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: id, Name: "bash", Output: strings.Repeat("line\n", 22)}})
	}
	transcript := m.transcript
	joined := strings.Join(transcript, "\n")
	for _, banned := range []string{"lines", "└", "line"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("compact transcript must not contain %q:\n%s", banned, joined)
		}
	}
	if len(transcript) != 3 {
		t.Fatalf("only the three cards should remain, got %d blocks:\n%s", len(transcript), joined)
	}
	for i, id := range ids {
		if !strings.Contains(transcript[i], "Ran ls") {
			t.Fatalf("card %d should be Ran ls in dispatch order, got %q\n%s", i, transcript[i], joined)
		}
		if idx, ok := m.shellTranscriptIdx[id]; !ok || idx != i {
			t.Fatalf("%s must keep a Ctrl+B anchor on its card (index %d), got ok=%v idx=%d", id, i, ok, idx)
		}
	}
}

// TestNonShellToolsLateResultsLeaveOnlyCards is the no-streaming compact
// variant: a second Bash dispatches before the first emits any ToolProgress,
// and the first's result lands last. No live slots or count summaries may
// remain — just the two cards in dispatch order.
func TestNonShellToolsLateResultsLeaveOnlyCards(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "call_a", Name: "bash", Args: `{"command":"echo a"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "call_b", Name: "bash", Args: `{"command":"echo b"}`}})
	// No ToolProgress for either; the results are the only signal.
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "call_a", Name: "bash", Output: "a\nsecond\nthird\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "call_b", Name: "bash", Output: "b\n"}})

	transcript := m.transcript
	joined := strings.Join(transcript, "\n")
	for _, banned := range []string{"lines", "└", "second", "third"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("transcript must not contain %q:\n%s", banned, joined)
		}
	}
	if len(transcript) != 2 {
		t.Fatalf("only the two cards should remain, got %d blocks:\n%s", len(transcript), joined)
	}
	if !strings.Contains(transcript[0], "echo a") || !strings.Contains(transcript[1], "echo b") {
		t.Fatalf("cards should remain in dispatch order:\n%s", joined)
	}
	if m.toolStreamIdx != -1 {
		t.Fatalf("tool block should be closed after the results, idx=%d", m.toolStreamIdx)
	}
}
