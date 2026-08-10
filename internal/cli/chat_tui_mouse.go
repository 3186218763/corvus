package cli

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// mouseCaptureOffByDefault lets a user opt out of in-app mouse capture for
// every run (e.g. a terminal/multiplexer combo where the native right-click
// menu and click-drag selection matter more than the scrollbar and
// wheel-scroll) without having to type "/mouse" each session.
func mouseCaptureOffByDefault() bool {
	v := strings.TrimSpace(os.Getenv("CORVUS_DISABLE_MOUSE"))
	return v != "" && v != "0"
}

func isTermuxTerminal() bool {
	if os.Getenv("TERMUX_VERSION") != "" || os.Getenv("TERMUX_APP_PID") != "" || os.Getenv("TERMUX__PREFIX") != "" {
		return true
	}
	return strings.Contains(os.Getenv("PREFIX"), "/com.termux/")
}

func suspendWithMouseReset() tea.Cmd {
	return tea.Sequence(tea.Raw(resetMouseTracking), tea.Suspend)
}

// handleMouseWheel handles tea.MouseWheelMsg for update.
func (m chatTUI) handleMouseWheel(msg tea.MouseWheelMsg) (chatTUI, tea.Cmd) {
	if m.mouseOverComposer(msg.X, msg.Y) {
		delta := 0
		switch msg.Button {
		case tea.MouseWheelUp:
			delta = -composerWheelRows
		case tea.MouseWheelDown:
			delta = composerWheelRows
		}
		if delta != 0 && m.scrollComposer(delta) {
			return m, nil
		}
	}
	// Skill/MCP-style managers hide the composer and own the main area:
	// route the wheel to list selection instead of scrolling the transcript
	// under the overlay (which left the input at a stale raised height).
	if m.scrollHiddenComposerOverlay(msg) {
		return m, nil
	}
	// Outside the composer, or once its internal viewport has reached the
	// requested edge, continue the gesture in the transcript. This mirrors
	// ordinary nested-scroll behavior and avoids a dead wheel at boundaries.
	prevOff := m.viewport.YOffset()
	switch msg.Button {
	case tea.MouseWheelUp:
		m.viewport.ScrollUp(3)
	case tea.MouseWheelDown:
		m.viewport.ScrollDown(3)
	}
	// Reading history should reclaim vertical space: drop any Codex raise-hold.
	if m.viewport.YOffset() != prevOff {
		m.composerRaisedRows = 0
	}
	return m, nil
}

// handleMouseMotion handles tea.MouseMotionMsg for update.
func (m chatTUI) handleMouseMotion(msg tea.MouseMotionMsg) (chatTUI, tea.Cmd) {
	if m.validComposerSelection() {
		if at, ok := m.composerCaretAt(msg.X, msg.Y, true); ok {
			m.composerSel.head = at.offset
		}
		return m, nil
	}
	if m.scrollbarDrag {
		m.dragScrollbar(msg.Y)
		return m, nil
	}
	// Drag extends the live selection (CellMotion only reports motion while
	// a button is held, so this is a drag). A drag held against the top or
	// bottom edge starts an auto-scroll ticker so the selection can run past
	// the visible window.
	if m.sel.active {
		m.sel.head = m.transcriptCaret(msg.X, msg.Y)
		m.dragX = msg.X
		prev := m.autoScroll
		m.autoScroll = edgeScrollDir(msg.Y, m.viewport.Height())
		if m.autoScroll != 0 && prev == 0 {
			return m, autoScrollTick()
		}
	}
	return m, nil
}

// handleAutoScroll handles autoScrollMsg for update.
func (m chatTUI) handleAutoScroll(msg autoScrollMsg) (chatTUI, tea.Cmd) {
	// One edge-scroll step: scroll a single line, drag the selection head to
	// the edge row, and keep ticking until the drag ends, leaves the edge, or
	// the viewport can't scroll further (so it can't run away to the end).
	if !m.sel.active || m.autoScroll == 0 {
		return m, nil
	}
	edgeY := 0
	if m.autoScroll > 0 {
		m.viewport.ScrollDown(1)
		edgeY = m.viewport.Height() - 1
	} else {
		m.viewport.ScrollUp(1)
	}
	m.sel.head = m.transcriptCaret(m.dragX, edgeY)
	// Stop at the boundary so a held edge can't run away to the very end.
	if (m.autoScroll > 0 && m.viewport.AtBottom()) || (m.autoScroll < 0 && m.viewport.AtTop()) {
		m.autoScroll = 0
		return m, nil
	}
	return m, autoScrollTick()
}
