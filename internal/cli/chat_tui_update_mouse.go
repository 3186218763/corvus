package cli

import tea "charm.land/bubbletea/v2"

// This file holds chatTUI's mouse click/release handlers. Wheel scrolling,
// motion drag, and edge auto-scroll live in chat_tui_mouse.go; these handlers
// own the click semantics: right-click copy/paste, middle-click tmux paste,
// left-press selection anchoring, and drag-release auto-copy.

// handleMouseClick handles tea.MouseClickMsg for update.
func (m chatTUI) handleMouseClick(msg tea.MouseClickMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	// Match the complete terminal right-click convention while Corvus owns
	// the mouse: copy an active selection, otherwise paste clipboard text into
	// the visible composer. Left-press begins a selection unless it lands on
	// the transcript scrollbar or a shell-output hint line.
	// Middle-click pastes tmux's current buffer when tmux owns the pane;
	// otherwise it follows the X11/Wayland PRIMARY-selection convention.
	if msg.Button == tea.MouseMiddle {
		if m.hideComposer() {
			return m, nil
		}
		cmds = append(cmds, pasteMiddleClick())
		return m, finalize(m, cmds)
	}
	if msg.Button == tea.MouseRight && m.validComposerSelection() && !m.composerSel.empty() {
		cmds = append(cmds, m.copySelectionWithNotice(m.selectedComposerText()))
		return m, finalize(m, cmds)
	}
	if msg.Button == tea.MouseRight && m.sel.active && !m.sel.empty() {
		text := m.selectedText()
		m.sel = selection{}
		cmds = append(cmds, m.copySelectionWithNotice(text))
		return m, finalize(m, cmds)
	}
	if msg.Button == tea.MouseRight && !m.hideComposer() {
		cmds = append(cmds, pasteClipboardText())
		return m, finalize(m, cmds)
	}
	if msg.Button == tea.MouseLeft {
		if at, ok := m.composerCaretAt(msg.X, msg.Y, false); ok {
			m.sel = selection{}
			m.autoScroll = 0
			m.setComposerCursor(at.offset)
			m.composerSel = composerSelection{
				active: true, anchor: at.offset, head: at.offset, value: m.input.Value(),
			}
			return m, nil
		}
		m.composerSel = composerSelection{}
	}
	if msg.Button == tea.MouseLeft && m.inScrollbar(msg.X, msg.Y) {
		m.sel = selection{}
		m.autoScroll = 0
		m.scrollbarDrag = true
		m.scrollbarGrabOffset = m.scrollbarGrabRowOffset(msg.Y)
		m.dragScrollbar(msg.Y)
		return m, nil
	}
	if msg.Button == tea.MouseLeft && msg.Y < m.viewport.Height() {
		at := m.transcriptCaret(msg.X, msg.Y)
		m.sel = selection{active: true, anchor: at, head: at}
		m.autoScroll = 0
	}
	return m, nil
}

// handleMouseRelease handles tea.MouseReleaseMsg for update.
func (m chatTUI) handleMouseRelease(msg tea.MouseReleaseMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	if msg.Button == tea.MouseLeft && m.validComposerSelection() {
		if at, ok := m.composerCaretAt(msg.X, msg.Y, true); ok {
			m.composerSel.head = at.offset
			m.setComposerCursor(at.offset)
		}
		if m.composerSel.empty() {
			m.composerSel = composerSelection{}
			return m, nil
		}
		// The terminal cannot see Corvus's application-owned highlight, and
		// macOS commonly consumes Cmd+C before it reaches the TUI. Copy on drag
		// release just like transcript selection so the visible selection always
		// has a usable clipboard result.
		cmds = append(cmds, m.copySelectionWithNotice(m.selectedComposerText()))
		return m, finalize(m, cmds)
	}
	// Release finalizes the selection: a real drag auto-copies it (native
	// terminal convention), while the highlight stays on as the visual
	// "what's selected" cue and a right-click can still re-copy it. A plain
	// click (no drag) clears any prior selection.
	if m.scrollbarDrag {
		m.dragScrollbar(msg.Y)
		m.scrollbarDrag = false
		m.scrollbarGrabOffset = 0
		return m, nil
	}
	m.autoScroll = 0 // stop edge auto-scroll
	if msg.Button == tea.MouseLeft && m.sel.active {
		if m.sel.empty() {
			m.sel = selection{}
		} else {
			cmds = append(cmds, m.copySelectionWithNotice(m.selectedText()))
		}
	}
	return m, finalize(m, cmds)
}
