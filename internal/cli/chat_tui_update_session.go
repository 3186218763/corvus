package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"corvus/internal/event"
	"corvus/internal/i18n"
)

// This file holds chatTUI's update handlers for agent event streams and
// session-scoped control messages: ingesting controller events (with burst
// coalescing), applying a /model switch result, resolving deferred prompt /
// @-reference / MCP external commands, and the follow-up balance/status/git
// refreshes they trigger. Model switch and agent events fall through to the
// shared textarea update via tailUpdate, matching their original placement.

// handleAgentEvent handles agentEventMsg for update.
func (m chatTUI) handleAgentEvent(msg agentEventMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	e := event.Event(msg)
	m.ingestEvent(e)
	turnDone := e.Kind == event.TurnDone
	gitMaybeChanged := e.Kind == event.ToolResult && !e.Tool.ReadOnly
	// Coalesce a burst: the goroutine that produced this event has already
	// exited (a Cmd reads the channel once), so it's safe to drain the events
	// already buffered and ingest them now. One re-wrap then covers the whole
	// batch instead of one per event — bounds the O(transcript) re-render cost
	// when bash output or reasoning floods in. Capped so a sustained flood
	// still yields to render periodically.
drain:
	for drained := 0; drained < maxEventDrain; drained++ {
		select {
		case e2 := <-m.eventCh:
			m.ingestEvent(e2)
			if e2.Kind == event.TurnDone {
				turnDone = true
			}
			if e2.Kind == event.ToolResult && !e2.Tool.ReadOnly {
				gitMaybeChanged = true
			}
		default:
			break drain
		}
	}
	cmds = append(cmds, waitForAgentEvent(m.eventCh))
	// A turn just spent tokens (and money) — refresh the balance readout and
	// the custom status line (its context/cost inputs just changed).
	if turnDone {
		cmds = append(cmds, fetchBalance(m.ctrl))
		if c := m.runStatusline(); c != nil {
			cmds = append(cmds, c)
		}
		if len(m.pendingInterject) > 0 {
			interject := m.pendingInterject[0]
			m.pendingInterject = m.pendingInterject[1:]
			// Reset queue navigation — the indices shifted.
			m.queueEditCursor = -1
			m.queueEditDraft = ""
			cmds = append(cmds, m.startTurn(interject, interject, interject))
		}
	}
	if turnDone || gitMaybeChanged {
		if c := m.refreshGitStatus(); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tailUpdate(m, msg, cmds, "")
}

// handleModelSwitch handles modelSwitchMsg for update.
func (m chatTUI) handleModelSwitch(msg modelSwitchMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	m.modelSwitchPending = false
	m.pendingModelSwitch = nil
	if msg.err != nil {
		prefix := msg.failurePrefix
		if prefix == "" {
			prefix = "model"
		}
		m.notice(prefix + ": " + msg.err.Error())
		// Build failed — no old controller to retire. The kept controller
		// may still have been retargeted to a recovery branch by the
		// pre-switch snapshot, so the lease must follow it.
		m.followSessionLease()
	} else {
		m.ctrl = msg.ctrl
		m.label = msg.label
		m.commands = msg.commands
		m.skills = msg.skills
		m.host = msg.host
		m.modelRef = msg.ref
		if msg.guidance != "" {
			m.runtimeGuidance = msg.guidance
		}
		if msg.completion != "" {
			m.runtimeCompletion = msg.completion
		}
		if msg.exposure != "" {
			m.runtimeExposure = msg.exposure
		}
		m.refreshEffortStatus()
		// Stash the old controller for cleanup at exit. It cannot be
		// closed here or in the build goroutine — Close() runs
		// SessionEnd hooks and kills plugin subprocesses, both of
		// which corrupt bubbletea's terminal raw mode.
		if msg.oldCtrl != nil {
			m.oldControllers = append(m.oldControllers, msg.oldCtrl)
		}
		// The lease follows the controller's session file. Normally a
		// no-op (a carried conversation keeps its file); it moves when
		// the pre-switch snapshot recovered onto a recovery branch — a
		// fresh file created by this process, so failure is theoretical.
		m.followSessionLease()
		if msg.successNotice != "" {
			m.notice(msg.successNotice)
		} else {
			m.notice(fmt.Sprintf(i18n.M.ModelSwitchedFmt, m.label))
		}
		cmds = append(cmds, fetchBalance(m.ctrl))
		if c := m.runStatusline(); c != nil {
			cmds = append(cmds, c)
		}
		// Do NOT re-issue waitForAgentEvent here — the goroutine from the
		// last agentEventMsg handler is still blocked on the same channel.
		// Starting a second one creates a race: two goroutines compete on
		// p.Send (unbuffered), and the receiver may read them out of order,
		// garbling the streamed text (words appear reordered).
	}
	return tailUpdate(m, msg, cmds, "")
}

// handlePromptResolved handles promptResolvedMsg for update.
func (m chatTUI) handlePromptResolved(msg promptResolvedMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	switch {
	case msg.err != nil:
		m.commitLine(wrapForViewport(i18n.M.ErrorPrefix+" "+msg.err.Error(), m.width, activeCLITheme.warn))
	case strings.TrimSpace(msg.sent) == "":
		m.notice(i18n.M.SlashPromptEmpty)
	default:
		cmds = append(cmds, m.startTurn(msg.sent, msg.display, msg.display))
	}
	return tailUpdate(m, msg, cmds, "")
}

// handleMCPExternalDone handles mcpExternalDoneMsg for update.
func (m chatTUI) handleMCPExternalDone(msg mcpExternalDoneMsg) (chatTUI, tea.Cmd) {
	if msg.err != nil {
		m.notice(msg.label + ": " + msg.err.Error())
	} else if msg.target != "" {
		m.notice(msg.label + ": " + msg.target)
	}
	return tailUpdate(m, msg, nil, "")
}

// handleRefsResolved handles refsResolvedMsg for update.
func (m chatTUI) handleRefsResolved(msg refsResolvedMsg) (chatTUI, tea.Cmd) {
	var cmds []tea.Cmd
	for _, e := range msg.errs {
		m.notice(e) // surface a fetch failure but still send the turn
	}
	sent := msg.sent
	if msg.block != "" {
		sent = "Referenced context:\n\n" + msg.block + "\n\n" + msg.sent
	}
	// raw = msg.display (the expanded paste content, without resolved @-ref
	// payloads) — NOT msg.restore (the folded label). See the non-refs branch
	// above for why raw needs the expansion.
	cmds = append(cmds, m.startTurnWithRaw(sent, msg.display, msg.restore, msg.display))
	return tailUpdate(m, msg, cmds, "")
}
