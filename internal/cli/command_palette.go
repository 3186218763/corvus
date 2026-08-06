package cli

import tea "charm.land/bubbletea/v2"

// commandPaletteItems returns the curated P1 Ctrl+P action list.
// IDs are stable for tests; Tasks is intentionally omitted until P2.
func commandPaletteItems() []quickPickerItem {
	return []quickPickerItem{
		{ID: "help", Label: "Help / cheatsheet", Description: "Keyboard shortcuts (?)"},
		{ID: "status", Label: "Session status", Description: "Show runtime diagnostics (/status)"},
		{ID: "model", Label: "Switch model", Description: "Open model picker"},
		{ID: "resume", Label: "Resume session", Description: "Pick a previous session"},
		{ID: "verbose", Label: "Toggle verbose reasoning", Description: "Show or hide thinking text"},
		{ID: "mouse", Label: "Toggle mouse capture", Description: "Hand mouse back to the terminal"},
		{ID: "mcp", Label: "MCP manager", Description: "Manage MCP servers"},
		{ID: "skills", Label: "Skills", Description: "Browse and enable skills"},
		{ID: "compact", Label: "Compact context", Description: "Summarize conversation to free context"},
		{ID: "clear", Label: "Clear session", Description: "Discard transcript and start fresh"},
		{ID: "new", Label: "New session", Description: "Save current and start a new session"},
	}
}

// openCommandPalette opens the idle-only Ctrl+P command palette as a quickPicker.
func (m *chatTUI) openCommandPalette() {
	if m == nil {
		return
	}
	m.quickPick = &quickPicker{
		kind:  quickPickerCommand,
		title: "Commands",
		hint:  "Type to filter · ↑/↓ · Enter run · Esc cancel",
		items: commandPaletteItems(),
	}
}

// runCommandPaletteItem executes a curated palette action by stable ID.
// Returns a tea.Cmd when the action schedules async work (e.g. compact).
func (m *chatTUI) runCommandPaletteItem(id string) tea.Cmd {
	if m == nil {
		return nil
	}
	switch id {
	case "help":
		m.cheatsheetOpen = true
	case "status":
		m.showStatusDetails()
	case "model":
		m.openModelPicker()
	case "resume":
		if m.ctrl != nil {
			m.runResumeCommand("/resume")
		}
	case "verbose":
		m.toggleVerboseReasoning(true)
	case "mouse":
		m.toggleMouseCapture()
	case "mcp":
		m.openMCPManager("")
	case "skills":
		m.openSkillPicker()
	case "compact":
		if m.ctrl != nil {
			return m.runSlashCommand("/compact")
		}
	case "clear":
		return m.runSlashCommand("/clear")
	case "new":
		if m.ctrl != nil {
			return m.runSlashCommand("/new")
		}
	}
	return nil
}
