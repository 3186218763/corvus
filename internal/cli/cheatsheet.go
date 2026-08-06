package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"corvus/internal/i18n"
)

// cheatsheetBinding is one key/command row in the static ? overlay.
type cheatsheetBinding struct {
	key  string // literal English chord or slash command
	hint string // short action description
}

// cheatsheetSection groups related bindings under a title.
type cheatsheetSection struct {
	title string
	rows  []cheatsheetBinding
}

// cheatsheetSections returns the static P1 keyboard cheatsheet content.
// Key chords stay literal English; section titles come from i18n.
func cheatsheetSections() []cheatsheetSection {
	return []cheatsheetSection{
		{
			title: i18n.M.CheatsheetSectionNavigation,
			rows: []cheatsheetBinding{
				{key: "Enter", hint: i18n.M.CheatsheetHintEnter},
				{key: "Esc", hint: i18n.M.CheatsheetHintEsc},
				{key: "PgUp/PgDn", hint: i18n.M.CheatsheetHintPageScroll},
				{key: "Ctrl+Home/End", hint: i18n.M.CheatsheetHintHomeEnd},
			},
		},
		{
			title: i18n.M.CheatsheetSectionModes,
			rows: []cheatsheetBinding{
				{key: "Shift+Tab", hint: i18n.M.CheatsheetHintShiftTab},
				{key: "Ctrl+Y", hint: i18n.M.CheatsheetHintCtrlY},
			},
		},
		{
			title: i18n.M.CheatsheetSectionTranscript,
			rows: []cheatsheetBinding{
				{key: "Ctrl+B", hint: i18n.M.CheatsheetHintCtrlB},
				{key: "Ctrl+O", hint: i18n.M.CheatsheetHintCtrlO},
			},
		},
		{
			title: i18n.M.CheatsheetSectionDiscover,
			rows: []cheatsheetBinding{
				{key: "?", hint: i18n.M.CheatsheetHintQuestion},
				{key: "Ctrl+P", hint: i18n.M.CheatsheetHintCtrlP},
				{key: "/", hint: i18n.M.CheatsheetHintSlash},
				{key: "/status", hint: i18n.M.CheatsheetHintStatus},
			},
		},
		{
			title: i18n.M.CheatsheetSectionSession,
			rows: []cheatsheetBinding{
				{key: "/resume", hint: i18n.M.CheatsheetHintResume},
				{key: "/new", hint: i18n.M.CheatsheetHintNew},
				{key: "/clear", hint: i18n.M.CheatsheetHintClear},
				{key: "/help", hint: i18n.M.CheatsheetHintHelp},
			},
		},
	}
}

// renderCheatsheet draws the static keyboard cheatsheet panel at the given
// terminal width, framed like other bottom pickers (choicePanelStyle).
func renderCheatsheet(width int) string {
	w := max(viewWidth(width), 40)
	var b strings.Builder
	b.WriteString(viewHeader("%s", i18n.M.CheatsheetTitle) + "\n")
	for _, sec := range cheatsheetSections() {
		b.WriteString(viewSubhead(sec.title) + "\n")
		for _, row := range sec.rows {
			// Pad key column so hints line up; chords stay literal English.
			fmt.Fprintf(&b, "  %-16s %s\n", row.key, viewMeta(row.hint))
		}
	}
	b.WriteString(viewHint(i18n.M.CheatsheetCloseHint))
	return choicePanelStyle.Width(w).Render(strings.TrimRight(b.String(), "\n"))
}

func (m chatTUI) renderCheatsheet() string {
	if !m.cheatsheetOpen {
		return ""
	}
	return renderCheatsheet(m.width)
}

// handleCheatsheetKey routes keys while the ? overlay is open. Esc closes;
// other keys are swallowed so the parent composer draft is not mutated.
func (m chatTUI) handleCheatsheetKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.cheatsheetOpen {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.cheatsheetOpen = false
	}
	// Swallow everything else (including printable input) while open.
	return m, nil
}

// openCheatsheetIfEmpty opens the cheatsheet when idle with an empty composer
// and the user typed "?". Returns true when the key was consumed.
func (m *chatTUI) openCheatsheetIfEmpty(msg tea.KeyPressMsg) bool {
	if m == nil || m.cheatsheetOpen {
		return false
	}
	if msg.String() != "?" {
		return false
	}
	if m.state != tuiIdle {
		return false
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		return false
	}
	m.cheatsheetOpen = true
	return true
}
