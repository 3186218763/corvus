package cli

import (
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// This file holds chatTUI's update handlers for the recurring tick messages:
// theme-sweep animation, elapsed-turn readout, spinner, and smooth-scroll.
// The smooth-scroll tick returns early like the original; the others fall
// through to the shared textarea update via tailUpdate.

// handleThemeSweepTick handles themeSweepTickMsg for update.
func (m chatTUI) handleThemeSweepTick(msg themeSweepTickMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	if m.themeSweep != nil {
		if m.themeSweep.advance() {
			cmds = append(cmds, themeSweepTick())
		} else {
			m.themeSweep = nil
		}
	}
	return tailUpdate(m, msg, cmds, "")
}

// handleElapsedTick handles elapsedTickMsg for update.
func (m chatTUI) handleElapsedTick(msg elapsedTickMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	if m.state == tuiRunning {
		m.elapsed = int(time.Since(m.runStart).Seconds())
		m.tickToolRunning()
		cmds = append(cmds, elapsedTick())
	}
	return tailUpdate(m, msg, cmds, "")
}

// handleSpinnerTick handles spinner.TickMsg for update.
func (m chatTUI) handleSpinnerTick(msg spinner.TickMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	if m.state == tuiRunning {
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}
	return tailUpdate(m, msg, cmds, "")
}

// handleSmoothScrollTick handles smoothScrollTickMsg for update.
func (m chatTUI) handleSmoothScrollTick(msg smoothScrollTickMsg) (chatTUI, tea.Cmd) {
	if m.smooth == nil {
		return m, nil
	}
	off, done := m.smooth.offsetAt(msg.now)
	m.viewport.SetYOffset(off)
	if done {
		m.smooth = nil
		return m, nil
	}
	var cmds []tea.Cmd
	cmds = append(cmds, smoothScrollTick())
	return tailUpdate(m, msg, cmds, "")
}
