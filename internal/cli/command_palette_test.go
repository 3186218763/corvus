package cli

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCommandPaletteOpensOnCtrlPWhenIdle(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiIdle
	next, _ := m.update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(chatTUI)
	if m.quickPick == nil || m.quickPick.kind != quickPickerCommand {
		t.Fatalf("expected command palette, got %#v", m.quickPick)
	}
	if m.quickPick.title != "Commands" {
		t.Fatalf("palette title = %q, want Commands", m.quickPick.title)
	}
	ids := make([]string, 0, len(m.quickPick.items))
	for _, it := range m.quickPick.items {
		ids = append(ids, it.ID)
	}
	for _, want := range []string{"help", "status", "model", "resume", "verbose", "mouse", "mcp", "skills", "compact", "clear", "new"} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("palette missing item %q; got %v", want, ids)
		}
	}
	for _, id := range ids {
		if id == "tasks" {
			t.Fatal("palette must omit Tasks in P1")
		}
	}
}

func TestCommandPaletteDoesNotOpenWhenCompletionActive(t *testing.T) {
	m := newTestChatTUI()
	m.completion = completion{active: true, items: []compItem{{label: "/model"}, {label: "/mcp"}}, sel: 1}
	next, _ := m.update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(chatTUI)
	if m.quickPick != nil {
		t.Fatal("Ctrl+P must move completion, not open palette")
	}
	if m.completion.sel != 0 {
		t.Fatalf("expected completion sel 0 after ctrl+p, got %d", m.completion.sel)
	}
}

func TestCommandPaletteDoesNotOpenWhenRunning(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiRunning
	next, _ := m.update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(chatTUI)
	if m.quickPick != nil {
		t.Fatal("Ctrl+P must not open palette while a turn is running")
	}
}

func TestCommandPaletteDoesNotOpenWhenCheatsheetOpen(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiIdle
	m.cheatsheetOpen = true
	next, _ := m.update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(chatTUI)
	if m.quickPick != nil {
		t.Fatal("Ctrl+P must not open palette while cheatsheet is open")
	}
	if !m.cheatsheetOpen {
		t.Fatal("cheatsheet should stay open")
	}
}

func TestCommandPaletteEscClosesPreservesDraft(t *testing.T) {
	m := newTestChatTUI()
	m.input.SetValue("keep me")
	m.quickPick = &quickPicker{kind: quickPickerCommand, title: "Commands", items: commandPaletteItems()}
	next, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(chatTUI)
	if m.quickPick != nil {
		t.Fatal("Esc should close palette")
	}
	if m.input.Value() != "keep me" {
		t.Fatalf("draft lost: %q", m.input.Value())
	}
}

func TestCommandPaletteStatusAction(t *testing.T) {
	m := newTestChatTUI()
	m.modelRef = "provider/model"
	m.quickPick = &quickPicker{
		kind:  quickPickerCommand,
		title: "Commands",
		items: commandPaletteItems(),
	}
	// Select the status item and confirm.
	items := m.quickPick.filteredItems()
	sel := -1
	for i, it := range items {
		if it.ID == "status" {
			sel = i
			break
		}
	}
	if sel < 0 {
		t.Fatal("status item missing from palette")
	}
	m.quickPick.selected = sel
	next, _ := m.handleQuickPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(chatTUI)
	if m.quickPick != nil {
		t.Fatal("palette should close after status action")
	}
	out := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(out, "Session status") {
		t.Fatalf("status action should commit Session status, got:\n%s", out)
	}
}

func TestCommandPaletteHelpAction(t *testing.T) {
	m := newTestChatTUI()
	m.quickPick = &quickPicker{
		kind:     quickPickerCommand,
		title:    "Commands",
		items:    []quickPickerItem{{ID: "help", Label: "Help"}},
		selected: 0,
	}
	next, _ := m.handleQuickPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(chatTUI)
	if m.quickPick != nil {
		t.Fatal("palette should close after help action")
	}
	if !m.cheatsheetOpen {
		t.Fatal("help action should open cheatsheet")
	}
}

func TestCommandPaletteItemsHaveStableIDs(t *testing.T) {
	items := commandPaletteItems()
	if len(items) == 0 {
		t.Fatal("commandPaletteItems returned empty")
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.ID == "" || it.Label == "" {
			t.Fatalf("item missing id/label: %+v", it)
		}
		if seen[it.ID] {
			t.Fatalf("duplicate palette id %q", it.ID)
		}
		seen[it.ID] = true
	}
}
