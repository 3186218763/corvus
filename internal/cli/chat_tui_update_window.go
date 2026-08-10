package cli

import (
	tea "charm.land/bubbletea/v2"

	"corvus/internal/provider"
)

// This file holds chatTUI's tea.WindowSizeMsg handler: resizing the composer
// to the new width and committing the initial banner/replay bundle once the
// terminal size is first known.

// handleWindowSize handles tea.WindowSizeMsg for update.
func (m chatTUI) handleWindowSize(msg tea.WindowSizeMsg) (chatTUI, tea.Cmd) {
	m.followComposerCursor()
	m.width = msg.Width
	m.height = msg.Height
	m.input.SetWidth(m.composerContentWidth())
	// Commit the banner — and a resumed session's transcript — once, now
	// that the width is known.
	if !m.started {
		m.started = true
		history := append([]provider.Message(nil), m.history...)
		m.commitTranscriptSource(transcriptSource{
			kind: transcriptSourceReplayBundle, raw: m.missing, history: history,
		})
		m.history = nil
	}
	return tailUpdate(m, msg, nil, "")
}
