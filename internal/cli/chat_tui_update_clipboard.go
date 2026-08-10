package cli

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"corvus/internal/i18n"
)

// This file holds chatTUI's update handlers for the async clipboard layer:
// image-paste results, text-paste results (re-entering the canonical
// tea.PasteMsg path), copy results with OSC52 fallback and transient notice,
// and copy-notice expiry. All fall through to the shared textarea update via
// tailUpdate except the text-paste re-entry, which recurses into update.

// handleClipboardImage handles clipboardImageMsg for update.
func (m chatTUI) handleClipboardImage(msg clipboardImageMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	m.clipboardImagePending = false
	if msg.err != nil {
		m.notice(fmt.Sprintf(i18n.M.ClipboardImagePasteFailedFmt, msg.err))
		return tailUpdate(m, msg, cmds, "")
	}
	imageBefore := m.input.Value()
	m.insertImageRef(msg.path)
	if shouldClearWideInputChange(imageBefore, m.input.Value()) {
		cmds = append(cmds, tea.ClearScreen)
	}
	return tailUpdate(m, msg, cmds, "")
}

// handleClipboardTextPaste handles clipboardTextPasteMsg for update.
func (m chatTUI) handleClipboardTextPaste(msg clipboardTextPasteMsg) (tea.Model, tea.Cmd) {
	if msg.remote {
		m.notice(i18n.M.ClipboardTextPasteRemoteHint)
		return tailUpdate(m, msg, nil, "")
	}
	if msg.err != nil {
		m.notice(fmt.Sprintf(i18n.M.ClipboardTextPasteFailedFmt, msg.err))
		return tailUpdate(m, msg, nil, "")
	}
	if msg.text == "" {
		return tailUpdate(m, msg, nil, "")
	}
	// Re-enter through the canonical paste path so selection replacement,
	// folded blocks, file references, completion, and wide-cell repainting
	// behave exactly like the terminal's bracketed-paste event.
	return m.update(tea.PasteMsg{Content: msg.text})
}

// handleClipboardCopy handles clipboardCopyMsg for update.
func (m chatTUI) handleClipboardCopy(msg clipboardCopyMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	if msg.statusHint && msg.seq != m.copyNoticeSeq {
		return tailUpdate(m, msg, cmds, "")
	}
	label := i18n.M.MouseCopiedHint
	if !msg.statusHint {
		label = i18n.M.SlashCopyDone
	}
	if msg.osc52 || msg.err != nil {
		label = i18n.M.ClipboardCopyOSC52Hint
		if msg.err != nil {
			label = i18n.M.ClipboardCopyFallbackHint
		}
		cmds = append(cmds, tea.SetClipboard(msg.text))
	}
	if msg.statusHint {
		m.copyNoticeText = label
		cmds = append(cmds, copyNoticeExpire(msg.seq))
	} else {
		m.notice(label)
	}
	return tailUpdate(m, msg, cmds, "")
}

// handleCopyNoticeExpire handles copyNoticeExpireMsg for update.
func (m chatTUI) handleCopyNoticeExpire(msg copyNoticeExpireMsg) (chatTUI, tea.Cmd) {
	if msg.seq == m.copyNoticeSeq {
		m.copyNoticeText = ""
	}
	return tailUpdate(m, msg, nil, "")
}
