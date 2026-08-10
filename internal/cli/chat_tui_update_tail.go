package cli

import tea "charm.land/bubbletea/v2"

// tailUpdate is the shared trailing half of update() for messages that fall
// through the type switch: it pushes the message into the textarea, keeps the
// composer sized to its content, re-filters autocomplete after keystrokes, and
// repaints when a wide-cell edit changed the input on Windows. inputBeforeSelection
// carries the pre-selection input value captured by the paste/key handlers so
// the wide-input check compares against the text that existed before the
// selection was deleted. Returns the final model and finalized command batch.
func tailUpdate(m chatTUI, msg tea.Msg, cmds []tea.Cmd, inputBeforeSelection string) (chatTUI, tea.Cmd) {
	beforeInput := m.input.Value()
	if inputBeforeSelection != "" {
		beforeInput = inputBeforeSelection
	}
	var ic tea.Cmd
	m.input, ic = m.input.Update(msg)
	cmds = append(cmds, ic)
	m.growInputToFit()
	// Re-filter the autocomplete menu against the freshly-edited input.
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.updateCompletion()
	}
	if shouldClearWideInputChange(beforeInput, m.input.Value()) {
		cmds = append(cmds, tea.ClearScreen)
	}
	return m, finalize(m, cmds)
}
