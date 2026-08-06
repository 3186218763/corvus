package cli

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCheatsheetOpensOnQuestionWhenComposerEmpty(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiIdle
	m.input.SetValue("")
	next, _ := m.update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = next.(chatTUI)
	if !m.cheatsheetOpen {
		t.Fatal("expected cheatsheet open")
	}
	if m.input.Value() != "" {
		t.Fatalf("draft changed: %q", m.input.Value())
	}
}

func TestCheatsheetInsertsQuestionWhenComposerNonEmpty(t *testing.T) {
	m := newTestChatTUI()
	m.input.SetValue("hello")
	next, _ := m.update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = next.(chatTUI)
	if m.cheatsheetOpen {
		t.Fatal("must not open cheatsheet when non-empty")
	}
	if !strings.Contains(m.input.Value(), "?") {
		t.Fatalf("expected '?' inserted into draft, got %q", m.input.Value())
	}
}

func TestCheatsheetEscCloses(t *testing.T) {
	m := newTestChatTUI()
	m.cheatsheetOpen = true
	next, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(chatTUI)
	if m.cheatsheetOpen {
		t.Fatal("Esc should close cheatsheet")
	}
}

func TestCheatsheetListsCriticalBindings(t *testing.T) {
	body := ansi.Strip(renderCheatsheet(80))
	for _, want := range []string{"Ctrl+P", "Shift+Tab", "Ctrl+Y", "Ctrl+B", "Ctrl+O", "Esc", "/status", "?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("cheatsheet missing %q:\n%s", want, body)
		}
	}
}

func TestCheatsheetWhitespaceOnlyComposerOpens(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiIdle
	m.input.SetValue("   ")
	next, _ := m.update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = next.(chatTUI)
	if !m.cheatsheetOpen {
		t.Fatal("TrimSpace-empty composer should open cheatsheet")
	}
}

func TestCheatsheetDoesNotOpenWhileRunning(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiRunning
	m.input.SetValue("")
	next, _ := m.update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = next.(chatTUI)
	if m.cheatsheetOpen {
		t.Fatal("must not open cheatsheet while turn is running")
	}
}

func TestCheatsheetEscPriorityOverClear(t *testing.T) {
	// Esc stack: close cheatsheet before idle clear/double-Esc rewind.
	m := newTestChatTUI()
	m.cheatsheetOpen = true
	m.input.SetValue("")
	next, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(chatTUI)
	if m.cheatsheetOpen {
		t.Fatal("Esc should close cheatsheet")
	}
	if m.rewind != nil {
		t.Fatal("Esc must not open rewind while closing cheatsheet")
	}
	if !m.lastEsc.IsZero() {
		t.Fatal("closing cheatsheet must not arm double-Esc rewind")
	}
}

func TestCheatsheetKeepsComposerVisible(t *testing.T) {
	m := newTestChatTUI()
	m.cheatsheetOpen = true
	if m.hideComposer() {
		t.Fatal("cheatsheet should keep the composer visible (input-owned overlay)")
	}
	if m.renderCheatsheet() == "" {
		t.Fatal("expected cheatsheet panel to render when open")
	}
}
