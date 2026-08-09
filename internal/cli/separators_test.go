package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"corvus/internal/event"
)

func TestFinalMessageSeparatorPlainUnder60s(t *testing.T) {
	s := ansi.Strip(finalMessageSeparator(40, 12))
	if strings.Contains(s, "Worked") {
		t.Fatalf("no Worked label under 60s: %q", s)
	}
	if strings.Trim(s, "─") != "" {
		t.Fatalf("want only ─ chars, got %q", s)
	}
	if visibleWidth(s) != 40 {
		t.Fatalf("width %d want 40", visibleWidth(s))
	}
}

func TestFinalMessageSeparatorWorkedForOver60s(t *testing.T) {
	s := ansi.Strip(finalMessageSeparator(80, 75))
	if !strings.Contains(s, "Worked for") {
		t.Fatalf("want Worked for, got %q", s)
	}
	if !strings.Contains(s, "1m 15s") {
		t.Fatalf("want compact elapsed, got %q", s)
	}
	if visibleWidth(s) != 80 {
		t.Fatalf("width %d want 80", visibleWidth(s))
	}
}

func TestFormatElapsedCompact(t *testing.T) {
	if got := formatElapsedCompact(12); got != "12s" {
		t.Fatalf("12 → %q", got)
	}
	if got := formatElapsedCompact(75); got != "1m 15s" {
		t.Fatalf("75 → %q", got)
	}
}

func TestTurnDoneEmitsSeparatorAfterTools(t *testing.T) {
	m := newTestChatTUI()
	m.width = 60
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "1", Name: "bash", Args: `{"command":"true"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "1", Name: "bash", Output: "ok"}})
	m.ingestEvent(event.Event{Kind: event.TurnDone})
	joined := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(joined, "─") {
		t.Fatalf("want turn rule after tool work, got %q", joined)
	}
}

func TestTurnSeparatorReflowsToAltScreenContentWidth(t *testing.T) {
	m := newTestChatTUI()
	m.width = 80
	m.nativeScrollback = false
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "1", Name: "bash", Args: `{"command":"true"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "1", Name: "bash", Output: "ok"}})
	m.ingestEvent(event.Event{Kind: event.TurnDone})

	m.reflowTranscript(40)
	found := false
	for _, block := range m.transcript {
		plain := ansi.Strip(block)
		if strings.Trim(plain, "─") == "" && strings.TrimSpace(plain) != "" {
			found = true
			if got, want := visibleWidth(plain), transcriptContentWidth(40, false); got != want {
				t.Fatalf("separator width after resize = %d, want %d: %q", got, want, plain)
			}
		}
	}
	if !found {
		t.Fatal("separator was not found after resize")
	}
}

func TestTurnDoneNoSeparatorPureChat(t *testing.T) {
	m := newTestChatTUI()
	m.width = 60
	m.ingestEvent(event.Event{Kind: event.Text, Text: "hello"})
	m.ingestEvent(event.Event{Kind: event.TurnDone})
	for _, block := range m.transcript {
		plain := strings.TrimSpace(ansi.Strip(block))
		if plain != "" && strings.Trim(plain, "─") == "" {
			t.Fatalf("pure chat must not emit ─ rule, got %q", m.transcript)
		}
	}
}
