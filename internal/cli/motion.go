package cli

import (
	"os"
	"strings"

	"charm.land/bubbletea/v2"
)

// motionEnabled reports whether decorative animation is enabled. Reduced motion
// (CORVUS_REDUCE_MOTION=1) disables spinner motion, smooth scroll, and any
// shimmer. Read on every call so tests observe the current environment.
func motionEnabled() bool {
	v := strings.TrimSpace(os.Getenv("CORVUS_REDUCE_MOTION"))
	return v == "" || v == "0"
}

// scrollRepaintEnabled reports whether the legacy full-screen repaint on every
// viewport scroll is requested (CORVUS_TUI_SCROLL_REPAINT=1).
func scrollRepaintEnabled() bool {
	v := strings.TrimSpace(os.Getenv("CORVUS_TUI_SCROLL_REPAINT"))
	return v != "" && v != "0"
}

// workingCmds returns the commands driving the running-state indicators: the
// elapsed ticker always runs (information), the spinner tick is decorative and
// is suppressed under reduced motion (returns nil).
func (m chatTUI) workingCmds() (elapsedCmd, spinnerCmd tea.Cmd) {
	if !motionEnabled() {
		return elapsedTick(), nil
	}
	return elapsedTick(), m.spinner.Tick
}

// workingBatch wraps workingCmds for tea.Batch call sites.
func (m chatTUI) workingBatch() tea.Cmd {
	el, sp := m.workingCmds()
	if sp != nil {
		return tea.Batch(el, sp)
	}
	return el
}
