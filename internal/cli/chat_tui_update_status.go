package cli

import tea "charm.land/bubbletea/v2"

// This file holds chatTUI's update handlers for one-shot status messages:
// balance, status line, and git status results. Each stores the payload on
// the model and falls through to the shared textarea update via tailUpdate,
// matching the original fall-through placement.

// handleBalance handles balanceMsg for update.
func (m chatTUI) handleBalance(msg balanceMsg) (chatTUI, tea.Cmd) {
	m.balance = msg.text
	return tailUpdate(m, msg, nil, "")
}

// handleStatusline handles statuslineMsg for update.
func (m chatTUI) handleStatusline(msg statuslineMsg) (chatTUI, tea.Cmd) {
	m.statuslineOut = msg.out
	return tailUpdate(m, msg, nil, "")
}

// handleGitStatus handles gitStatusMsg for update.
func (m chatTUI) handleGitStatus(msg gitStatusMsg) (chatTUI, tea.Cmd) {
	m.gitStatus = msg.status
	return tailUpdate(m, msg, nil, "")
}
