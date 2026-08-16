package cli

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/i18n"
)

// newChatTUI assembles the initial model. The controller has already been wired
// with an event sink that feeds eventCh; the TUI issues commands to it and
// renders the events it emits. Model identity, label, history, host, and commands
// are read from the controller, so explicit selections and resumed sessions stay
// authoritative.
func newChatTUI(ctrl *control.Controller, missing string, eventCh chan event.Event, termW int) chatTUI {
	ti := textarea.New()
	configureChatTextarea(&ti)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = themeStyle(activeCLITheme.accent)

	commitBuf := []string{}
	nativeScrollback := detectTermuxTerminal()
	history := ctrl.History()
	nextPasteID, usedPasteIDs := pasteIDStateForHistory(history)
	return chatTUI{
		ctrl:                 ctrl,
		label:                ctrl.Label(),
		modelRef:             ctrl.ModelRef(),
		missing:              missing,
		nativeScrollback:     nativeScrollback,
		scrollRepaint:        scrollRepaintEnabled(),
		mouseCaptureOff:      mouseCaptureOffByDefault(),
		input:                ti,
		spinner:              sp,
		submittedInputCursor: -1,
		queueEditCursor:      -1,
		nextPasteID:          nextPasteID,
		usedPasteIDs:         usedPasteIDs,
		reasoningLineIdx:     -1,
		reasoningTextIdx:     -1,
		answerIdx:            -1,
		toolStreamIdx:        -1,
		exploreIdx:           -1,
		reasoning:            &strings.Builder{},
		pending:              &strings.Builder{},
		pendingCommit:        &commitBuf,
		diffMaxLines:         diffFoldLimit,
		showReasoning:        nativeScrollback,
		shellOutputs:         make(map[string]string),
		shellExpanded:        make(map[string]bool),
		shellMeta:            make(map[string]shellRunMeta),
		shellNativeFlushed:   make(map[string]bool),
		shellLiveIdx:         make(map[string]int),
		shellTranscriptIdx:   make(map[string]int),
		toolCardIdx:          make(map[string]int),
		toolStreams:          make(map[string]*toolProgressState),
		eventCh:              eventCh,
		history:              history,
		host:                 ctrl.Host(),
		commands:             ctrl.Commands(),
		skills:               ctrl.SlashSkills(),
		viewport:             viewport.New(viewport.WithWidth(termW)),
		statusLineCount:      1,
	}
}

// startTurn commits the user bubble to scrollback, resets the turn accumulator,
// and kicks off the controller turn. `sent` goes to the model uncomposed (the
// controller frames it with any plan marker); `displayed` is what the transcript
// shows, and `restore` is what Esc puts back while the bubble is still deferred.
func (m *chatTUI) startTurn(sent, displayed, restore string) tea.Cmd {
	return m.startTurnWithRaw(sent, displayed, restore, sent)
}

// startTurnWithRaw is startTurn plus an explicit unresolved user prompt. This
// keeps reference-expanded model input separate from the text shown/restored by
// the frontend.
func (m *chatTUI) startTurnWithRaw(sent, displayed, restore, raw string) tea.Cmd {
	return m.startControllerTurn(displayed, restore, func() { m.ctrl.SendWithRaw(sent, raw) })
}

// startControllerTurn owns the TUI-side turn setup for controller entry points.
// Most prompts use SendWithRaw; slash-invoked skills use SubmitDisplay so the
// controller can choose inline vs isolated subagent execution from the live
// skill's RunAs metadata without the TUI reimplementing that policy.
func (m *chatTUI) startControllerTurn(displayed, restore string, start func()) tea.Cmd {
	// Flush any half-streamed leftover before the new turn (defensive).
	m.commitReasoning()
	m.commitPending()

	// Echo the user bubble to scrollback now so it appears the instant Enter is
	// pressed, not when the server's first packet lands. It stays un-sendable until
	// then: Esc before the reply pops these lines back off (unsendPending) and
	// restores the text to the input box, leaving nothing stranded.
	m.pendingRestore = restore
	m.pendingPastes = m.pasteLabelsIn(restore)
	m.bubbleStartIdx = len(m.transcript)
	m.flushExploreCard()
	m.commitLine("") // blank line separating turns
	m.commitTranscriptSource(transcriptSource{
		kind: transcriptSourceUser, raw: displayed, planMode: m.planMode,
	})
	m.bubblePending = true
	m.turnDiscarded = false

	m.state = tuiRunning
	m.runStart = time.Now()
	m.elapsed = 0
	m.turnTokens = 0
	// The controller owns the run goroutine, its context, and cancellation; it
	// streams events to eventCh and emits TurnDone when the turn settles.
	start()
	return m.workingBatch()
}

// confirmBubbleSent marks the already-echoed user bubble as really sent once a
// turn's first response packet arrives, so Esc no longer un-sends it (it cancels
// the stream instead). Also called defensively at turn end. A no-op once confirmed.
func (m *chatTUI) confirmBubbleSent() {
	if !m.bubblePending {
		return
	}
	m.bubblePending = false
	m.pendingRestore = ""
}

// unsendPending "un-sends" the in-flight turn while the server hasn't replied yet
// (bubblePending): it pops the echoed bubble back off the transcript, restores the
// just-sent text to the input box, and cancels the request — marking the turn
// discarded so its already-buffered events reach nothing. Once a packet has arrived
// the bubble is confirmed and this path isn't taken (Esc cancels normally instead).
func (m *chatTUI) unsendPending() {
	m.input.SetValue(m.pendingRestore)
	m.growInputToFit()
	m.truncateTranscriptBlocks(m.bubbleStartIdx)
	m.transcriptDirty = true
	m.bubblePending = false
	m.pendingRestore = ""
	m.pendingPastes = nil
	m.turnDiscarded = true
	m.ctrl.Cancel()
}

// ingestEvent routes one typed event from the agent. Reasoning (dim) and answer
// free-text accumulate in their live buffers; every other event first finalizes
// the reasoning and answer streamed so far, then commits its own line —
// preserving order. Switching on the event Kind replaces the old prefix-sniffing
// of a flattened byte stream: the structure is now explicit.
func (m *chatTUI) ingestEvent(e event.Event) {
	if e.Kind == event.Retrying {
		m.retryAttempt = e.RetryAttempt
		m.retryMax = e.RetryMax
		return
	}
	// Any other event means the connection got past the retry window (or the turn
	// ended), so the transient "retrying" indicator clears.
	m.retryAttempt = 0
	m.retryMax = 0
	if m.turnDiscarded {
		// The turn was un-sent (Esc before any packet); swallow whatever was already
		// buffered for it until it settles, so nothing lands in scrollback.
		if e.Kind == event.TurnDone {
			m.turnDiscarded = false
			m.state = tuiIdle
		}
		return
	}
	// The first packet of any kind means the server replied — confirm the send so
	// Esc cancels the stream instead of un-sending. TurnStarted is local (emitted
	// before the request) and TurnDone is handled in its own case.
	if e.Kind != event.TurnStarted && e.Kind != event.TurnDone {
		m.confirmBubbleSent()
	}
	switch e.Kind {
	case event.Reasoning:
		// Default: buffer full text for verbose /debug, but do not paint a
		// "▎ thinking…" wall into the transcript. Live progress is the ambient
		// working line above the composer (Codex density).
		if m.thinkStart.IsZero() {
			m.thinkStart = time.Now()
		}
		m.reasoning.WriteString(e.Text)
		if m.nativeScrollback {
			m.reasoningNative = true
			// Native scrollback still buffers only; verbose commit may print later.
			break
		}
		// Verbose live stream: show trailing body without the old ▎ marker wall.
		if m.showReasoning {
			if m.reasoningTextIdx < 0 {
				m.pruneOlderReasoningBlocks(-1)
				m.commitSpacer()
				m.reasoningTextIdx = len(m.transcript)
				m.commitLine("")
				m.reasoningView = m.reasoningView[:0]
			}
			// streamReasoning expects reasoning already appended once; undo double write
			// by only streaming the chunk into the view (reasoning already has full text).
			chunk := e.Text
			if m.reasoningTextIdx >= 0 {
				m.reasoningView = append(m.reasoningView, chunk...)
				if len(m.reasoningView) > reasoningViewMax {
					drop := len(m.reasoningView) - reasoningViewMax
					for drop < len(m.reasoningView) && !utf8.RuneStart(m.reasoningView[drop]) {
						drop++
					}
					m.reasoningView = m.reasoningView[:copy(m.reasoningView, m.reasoningView[drop:])]
				}
				raw := string(m.reasoningView)
				contentWidth := transcriptContentWidth(m.width, m.nativeScrollback)
				m.setTranscriptBlock(m.reasoningTextIdx, reasoningBlock(raw, contentWidth, reasoningTailLines), transcriptSource{
					kind: transcriptSourceReasoning, raw: raw, maxLines: reasoningTailLines,
				})
				m.transcriptDirty = true
			}
		}

	case event.Text:
		m.commitReasoningBeforeAnswer()
		m.pending.WriteString(e.Text)
		m.streamAnswer()

	case event.Message:
		// The answer stream is complete — freeze reasoning + the markdown answer.
		m.commitReasoning()
		m.commitPending()

	case event.ToolDispatch:
		// The early (partial) dispatch only carries the name — the full dispatch
		// with args prints the line. Same-ID preview refreshes are ignored because
		// native scrollback cannot replace an already-printed diff card.
		if e.Tool.Partial || e.Tool.Refreshed {
			break
		}
		m.finalizeStreamed()
		switch e.Tool.Name {
		case "todo_write":
			// The result decides whether this list becomes canonical; dispatch only
			// means the model asked for an update.
		case planApprovalTool:
			// No longer a tool, but guard anyway: the plan is the assistant's reply.
		default:
			if e.Tool.Diff != "" {
				// One reflowable source (not fixed-width commitLine rows): bars
				// re-render at the live transcript width so narrow / non-fullscreen
				// viewports never lipgloss-wrap mid-background.
				m.flushExploreCard()
				m.ensureBlank()
				m.commitTranscriptSource(transcriptSource{
					kind:     transcriptSourceDiff,
					raw:      e.Tool.Name,
					aux:      e.Tool.Args,
					maxLines: m.diffMaxLines,
					fileDiff: e.Tool.FileDiff,
				})
				m.hadWorkActivity = true
				break
			}
			// A re-run of the same tool id must not render the fresh card with
			// the previous run's output or expansion state.
			delete(m.shellOutputs, e.Tool.ID)
			delete(m.shellExpanded, e.Tool.ID)
			delete(m.shellMeta, e.Tool.ID)
			delete(m.shellNativeFlushed, e.Tool.ID)
			delete(m.shellLiveIdx, e.Tool.ID)
			if isExploreCoalesceTool(e.Tool.Name) {
				m.appendExploreTool(e.Tool.ID, e.Tool.Name, e.Tool.Args)
				m.beginToolRunning(e.Tool.ID)
				break
			}
			m.flushExploreCard()
			m.ensureBlank()
			m.commitTranscriptSource(transcriptSource{
				kind: transcriptSourceToolCard, raw: e.Tool.Name, aux: e.Tool.Args, shellID: e.Tool.ID,
			})
			m.toolCardIdx[e.Tool.ID] = len(m.transcript) - 1
			m.hadWorkActivity = true
			m.beginToolRunning(e.Tool.ID)
		}

	case event.ToolProgress:
		m.streamToolOutput(e.Tool.ID, e.Tool.Output)

	case event.ToolResult:
		// Capture full output + outcome so the card can show a ≤5-line preview
		// (and Ctrl+B can expand). Then drop the live stream canvas.
		if e.Tool.Name == "bash" || strings.HasPrefix(e.Tool.ID, "shell-") {
			if e.Tool.Output != "" {
				m.shellOutputs[e.Tool.ID] = e.Tool.Output
			}
			dur := e.Tool.DurationMs
			if dur == 0 {
				if state := m.toolStreams[e.Tool.ID]; state != nil && !state.startedAt.IsZero() {
					dur = time.Since(state.startedAt).Milliseconds()
				}
			}
			m.shellMeta[e.Tool.ID] = shellRunMeta{
				ok:         e.Tool.Err == "",
				durationMs: dur,
				err:        e.Tool.Err,
			}
		}
		m.collapseToolOutput(e.Tool.ID)
		if e.Tool.Name == "todo_write" && e.Tool.Err == "" {
			m.todoArgs = e.Tool.Args
		}
		if e.Tool.Err != "" {
			m.finalizeStreamed()
			m.flushExploreCard()
			m.commitLine("  " + toolBulletErr() + " " + bold(toolDisplayName(e.Tool.Name)) + " " + red("⊘ "+e.Tool.Err))
		}

	case event.Usage:
		if e.Usage != nil {
			m.turnTokens += e.Usage.CompletionTokens
		}
		m.finalizeStreamed()
		m.turnReceipt = ""
		if m.ctrl != nil {
			hit, miss := m.ctrl.SessionCache()
			m.turnReceipt = renderCacheHitRate(hit, miss)
		}

	case event.Notice:
		glyph := "·"
		if e.Level == event.LevelWarn {
			glyph = "!"
		}
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("  %s %s", glyph, e.Text))

	case event.GuardianAssessment:
		m.finalizeStreamed()
		g := e.Guardian
		line := fmt.Sprintf("Guardian %s · %s", g.Outcome, g.Tool)
		if g.Subject != "" {
			line += " · " + truncateSubject(g.Subject, m.width)
		}
		if g.RiskLevel != "" {
			line += " · risk=" + g.RiskLevel
		}
		if g.UserAuthorization != "" {
			line += " · authorization=" + g.UserAuthorization
		}
		if g.Rationale != "" {
			line += " · " + g.Rationale
		}
		if g.Outcome == "deny" {
			m.commitLine("  ! " + line)
		} else {
			m.commitLine("  · " + line)
		}

	case event.CompactionStarted:
		m.finalizeStreamed()
		m.commitLine(dim("  ⋯ " + i18n.M.CompactionWorking))

	case event.CompactionDone:
		// An aborted pass carries no summary; the accompanying Notice (auto) or
		// compactDoneMsg error (manual) explains why, so don't draw an empty card.
		if e.Compaction.Summary == "" {
			break
		}
		m.finalizeStreamed()
		for _, ln := range compactionCardLines(e.Compaction) {
			m.commitLine(ln)
		}

	case event.Phase:
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("[%s]", e.Text))

	case event.ApprovalRequest:
		// The controller's run goroutine is now blocked inside the gate awaiting
		// this decision; the banner shows it in View and key input answers it via
		// ctrl.Approve. At most one prompt is outstanding (the controller
		// serialises them), so a plain field holds the current one.
		a := e.Approval
		m.pendingApproval = &a
		m.approvalSelection = 0
		if isRecoveryPlanChangeApproval(&a) {
			// A plan decision must start neutral: Enter alone cannot make Auto's
			// strategy/scope choice for the user.
			m.approvalSelection = -1
		}

	case event.AskRequest:
		// The `ask` tool raised a question card; the run goroutine blocks until
		// ctrl.AnswerQuestion resolves it. Keys drive the card while it's set.
		m.finalizeStreamed()
		m.chooser = newChooser(e.Ask)

	case event.TurnDone:
		// The turn settled — freeze anything still streaming, surface a real error,
		// and gate a plan-mode proposal on the user's approval. Autosave already
		// happened in Controller so every frontend shares the same activity-time
		// semantics.
		m.flushExploreCard()
		m.commitReasoning()
		m.commitPending()
		// The bubble was echoed on Enter and an un-sent turn is swallowed above
		// (turnDiscarded), so any turn reaching here keeps its bubble in scrollback;
		// just clear the un-sendable flag.
		m.confirmBubbleSent()
		m.state = tuiIdle
		m.queueEditCursor = -1
		m.queueEditDraft = ""
		m.clearSubmittedPastes()
		if e.Outcome == event.TurnOutcomeRecoveryPaused {
			m.commitLine(wrapForViewport("⏸ "+i18n.M.RecoveryPaused, m.width, activeCLITheme.info))
		} else if e.Err != nil && e.Err.Error() != "" && !strings.Contains(e.Err.Error(), "context canceled") {
			m.commitLine(wrapForViewport(i18n.M.ErrorPrefix+" "+e.Err.Error(), m.width, activeCLITheme.warn))
		}
		// Dim ─ rule after turns that did concrete tool work (Codex FinalMessageSeparator).
		if m.hadWorkActivity {
			m.ensureBlank()
			m.commitTranscriptSource(transcriptSource{kind: transcriptSourceSeparator, elapsed: m.elapsed})
		}
		m.hadWorkActivity = false
		// Plan-mode approval is now driven by the controller (it emits an
		// ApprovalRequest when a plan-mode turn produces a proposal), so there's
		// nothing to detect here.
	}
}

// finalizeStreamed freezes any in-progress reasoning + answer into scrollback so
// a following event line lands after them, preserving chronological order. Tool
// streams close only on their matching ToolResult; unrelated events must not
// invent a successful outcome for a still-running call.
func (m *chatTUI) finalizeStreamed() {
	m.commitReasoning()
	m.commitPending()
}

func waitForAgentEvent(ch chan event.Event) tea.Cmd {
	return func() tea.Msg { return agentEventMsg(<-ch) }
}

func elapsedTick() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg { return elapsedTickMsg{} })
}

// handleCompactDone handles compactDoneMsg for update.
func (m chatTUI) handleCompactDone(msg compactDoneMsg) (chatTUI, tea.Cmd) {
	if msg.err != nil {
		m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashCompactFailed, msg.err))
		return m, nil
	}
	// The session file write can block on disk; keep it off the Update loop.
	return m, func() tea.Msg {
		_ = m.ctrl.Snapshot()
		return compactSnapshotMsg{}
	}
}

// handleCompactSnapshot handles compactSnapshotMsg for update.
func (m chatTUI) handleCompactSnapshot(msg compactSnapshotMsg) (chatTUI, tea.Cmd) {
	m.followSessionLease()
	return m, nil
}

// handleNewSessionDone handles newSessionDoneMsg for update.
func (m chatTUI) handleNewSessionDone(msg newSessionDoneMsg) (chatTUI, tea.Cmd) {
	if msg.err != nil {
		m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashNewFailed, msg.err))
		return m, nil
	}
	// Native scrollback keeps the old transcript; mark the fork with a fresh banner.
	m.followSessionLease()
	m.resetFreshContextView(false)
	m.notice(i18n.M.SlashNewDone)
	return m, nil
}

// handleTuiShutdown handles tuiShutdownMsg for update.
func (m chatTUI) handleTuiShutdown(msg tuiShutdownMsg) (chatTUI, tea.Cmd) {
	if m.ctrl != nil {
		_ = m.ctrl.Snapshot()
		m.followSessionLease()
	}
	return m, tea.Quit
}
