package cli

import (
	"strings"
	"testing"

	"corvus/internal/event"
)

// TestParallelBashCallsStayCompact is the compact parallel-Bash regression:
// three parallel Bash(ls) calls in one turn, ids "call_<n>" (no "shell-"
// prefix), each streams 22 lines then finishes. Each card's live stream is
// removed when ITS OWN result lands, leaving only the three cards with short
// previews — no "N lines" count summaries, no stacked live slots.
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
	for _, banned := range []string{"0 lines", "-1 lines"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("compact transcript must not contain %q:\n%s", banned, joined)
		}
	}
	// Preview is capped; ellipsis should appear for 22-line dumps.
	if !strings.Contains(joined, "…") && !strings.Contains(joined, "+") {
		t.Fatalf("long output should be ellipsized in preview:\n%s", joined)
	}
	cards := nonEmptyTranscript(transcript)
	if len(cards) != 3 {
		t.Fatalf("only the three cards should remain, got %d blocks:\n%s", len(cards), joined)
	}
	for i, id := range ids {
		if !strings.Contains(cards[i], "Ran ls") {
			t.Fatalf("card %d should be Ran ls in dispatch order, got %q\n%s", i, cards[i], joined)
		}
		idx, ok := m.shellTranscriptIdx[id]
		if !ok || idx < 0 || idx >= len(m.transcript) || !strings.Contains(m.transcript[idx], "Ran") {
			t.Fatalf("%s must keep a Ctrl+B anchor on its card, ok=%v idx=%d", id, ok, idx)
		}
	}
}

// TestNonShellToolsLateResultsLeaveOnlyCards is the no-streaming compact
// variant: a second Bash dispatches before the first emits any ToolProgress,
// and the first's result lands last. No live slots or count summaries may
// remain — just the two cards (with short previews) in dispatch order.
func TestNonShellToolsLateResultsLeaveOnlyCards(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "call_a", Name: "bash", Args: `{"command":"echo a"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "call_b", Name: "bash", Args: `{"command":"echo b"}`}})
	// No ToolProgress for either; the results are the only signal.
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "call_a", Name: "bash", Output: "a\nsecond\nthird\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "call_b", Name: "bash", Output: "b\n"}})

	transcript := m.transcript
	joined := strings.Join(transcript, "\n")
	for _, banned := range []string{"0 lines", "-1 lines"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("transcript must not contain %q:\n%s", banned, joined)
		}
	}
	// Short multi-line preview is expected under └.
	if !strings.Contains(joined, "second") || !strings.Contains(joined, "third") {
		t.Fatalf("card preview should include result lines:\n%s", joined)
	}
	cards := nonEmptyTranscript(transcript)
	if len(cards) != 2 {
		t.Fatalf("only the two cards should remain, got %d blocks:\n%s", len(cards), joined)
	}
	if !strings.Contains(cards[0], "echo a") || !strings.Contains(cards[1], "echo b") {
		t.Fatalf("cards should remain in dispatch order:\n%s", joined)
	}
	if m.toolStreamIdx != -1 {
		t.Fatalf("tool block should be closed after the results, idx=%d", m.toolStreamIdx)
	}
}
