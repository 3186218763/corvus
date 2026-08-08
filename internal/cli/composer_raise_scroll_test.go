package cli

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/skill"
)

// TestComposerDropsRaiseWhenModalCloses is the /skills regression: opening a
// hideComposer manager after a raised slash menu, then closing it, must return
// the input to the bottom (not leave the prior raise-hold).
func TestComposerDropsRaiseWhenModalCloses(t *testing.T) {
	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	m.width = 80
	m.height = 24
	// Simulate a prior slash-menu raise hold.
	m.composerRaisedRows = 6
	// Open skills (hides composer).
	m.skillPick = &skillPicker{
		mode:   pickerSkills,
		skills: []skill.Skill{{Name: "demo", Description: "d"}},
		enabled: map[string]bool{"demo": true},
		originalEnabled: map[string]bool{"demo": true},
	}
	if !m.hideComposer() {
		t.Fatal("skill picker should hide the composer")
	}
	// Close skills via Update path so the raise-drop hook runs.
	m0, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = m0.(chatTUI)
	if m.skillPick != nil {
		t.Fatal("Esc should close skill picker")
	}
	if m.composerRaisedRows != 0 {
		t.Fatalf("composerRaisedRows = %d after closing skills, want 0 (bottom)", m.composerRaisedRows)
	}
}

// TestComposerDropsRaiseOnTranscriptWheel proves scrolling the transcript
// reclaims the held raise space so the input sits at the bottom.
func TestComposerDropsRaiseOnTranscriptWheel(t *testing.T) {
	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	// Fill transcript so wheel can move the viewport.
	for i := 0; i < 40; i++ {
		m.commitLine("line")
	}
	m0, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m0.(chatTUI)
	m.viewport.GotoBottom()
	m.composerRaisedRows = 5
	// Wheel up on transcript (not over composer).
	m0, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 10, Y: 2})
	m = m0.(chatTUI)
	if m.composerRaisedRows != 0 {
		t.Fatalf("composerRaisedRows = %d after wheel, want 0", m.composerRaisedRows)
	}
}

// TestSkillPickerWheelMovesSelection ensures wheel over an open skill manager
// moves the list instead of scrolling the transcript.
func TestSkillPickerWheelMovesSelection(t *testing.T) {
	m := newTestChatTUI()
	m.width = 80
	m.height = 24
	m.skillPick = &skillPicker{
		mode: pickerSkills,
		skills: []skill.Skill{
			{Name: "a", Description: "1"},
			{Name: "b", Description: "2"},
			{Name: "c", Description: "3"},
		},
		enabled:         map[string]bool{"a": true, "b": true, "c": true},
		originalEnabled: map[string]bool{"a": true, "b": true, "c": true},
		sel:             0,
	}
	m0, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 10, Y: 5})
	m = m0.(chatTUI)
	if m.skillPick == nil || m.skillPick.sel == 0 {
		t.Fatalf("wheel-down should advance skill selection, sel=%v", m.skillPick)
	}
}
