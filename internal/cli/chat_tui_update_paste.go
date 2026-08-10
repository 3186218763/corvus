package cli

import tea "charm.land/bubbletea/v2"

// This file holds chatTUI's tea.PasteMsg handler: routing terminal paste
// through modal dialogs, image attachment, file references, and folded paste
// blocks, then falling through to the shared textarea update for ordinary
// text. The clipboard command layer that feeds this path lives in
// chat_tui_paste.go.

// handlePaste handles tea.PasteMsg for update.
func (m chatTUI) handlePaste(msg tea.PasteMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	var inputBeforeSelection string
	if m.consumeModalPaste(msg.Content) {
		return m, finalize(m, cmds)
	}
	m.followComposerCursor()
	pasteBefore := m.input.Value()
	if m.state != tuiRunning && m.attachPastedImages(msg.Content) {
		if shouldClearWideInputChange(pasteBefore, m.input.Value()) {
			cmds = append(cmds, tea.ClearScreen)
		}
		return m, finalize(m, cmds)
	}
	if m.validComposerSelection() && !m.composerSel.empty() {
		inputBeforeSelection = pasteBefore
		m.deleteComposerSelection()
	}
	if ref, ok := pastedFileRef(msg.Content); ok {
		m.input.InsertString(ref + " ")
		m.growInputToFit()
		m.updateCompletion()
		if shouldClearWideInputChange(pasteBefore, m.input.Value()) {
			cmds = append(cmds, tea.ClearScreen)
		}
		return m, finalize(m, cmds)
	}
	if !m.chooserTyping() && m.pendingApproval == nil && m.rewind == nil && m.resumePick == nil && m.mcp == nil && m.clearConfirm == nil && m.mcpImport == nil && m.skillPick == nil && m.shouldFoldPaste(msg.Content) {
		m.insertFoldedPaste(msg.Content)
		m.growInputToFit()
		m.updateCompletion()
		if shouldClearWideInputChange(pasteBefore, m.input.Value()) {
			cmds = append(cmds, tea.ClearScreen)
		}
		return m, finalize(m, cmds)
	}
	return tailUpdate(m, msg, cmds, inputBeforeSelection)
}
