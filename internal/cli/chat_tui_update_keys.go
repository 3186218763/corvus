package cli

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"corvus/internal/control"
	"corvus/internal/i18n"
	"corvus/internal/memory"
)

// This file holds chatTUI's tea.KeyPressMsg handler: the main keyboard path.
// It owns selection dismissal, transcript scroll keys, modal routing (chooser,
// rewind/MCP/copy/resume/quick pickers, MCP manager, clear-confirm, skill
// picker, approvals, autocomplete, cheatsheet), queue navigation, Esc/Ctrl+C
// semantics, and submission (interject, quit, memory notes, shell commands,
// slash commands, @refs, and normal turns). Keystrokes that no higher-priority
// branch claims fall through to the shared textarea update in tailUpdate.

// handleKeyPress handles tea.KeyPressMsg for update.
func (m chatTUI) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var inputBeforeSelection string
	// Any keystroke dismisses a finished selection (copy is a right-click),
	// with a few exceptions: Ctrl/Super/Meta+C copies the selection, the
	// paste shortcuts keep it so the async clipboard result can replace
	// it, and Left/Right collapse it to its ordered start/end.
	sel := m.sel
	m.sel = selection{}
	if m.validComposerSelection() && !m.composerSel.empty() {
		switch {
		case msg.String() == "ctrl+c" || msg.String() == "super+c" || msg.String() == "meta+c":
			cmds = append(cmds, m.copySelectionWithNotice(m.selectedComposerText()))
			return m, finalize(m, cmds)
		case imagePasteShortcut(msg.String(), runtime.GOOS):
			// The asynchronous image result replaces the still-active
			// selection. Terminal text paste arrives separately as PasteMsg.
		case msg.String() == "left":
			start, _ := m.composerSel.ordered()
			m.composerSel = composerSelection{}
			m.setComposerCursor(start)
			return m, finalize(m, cmds)
		case msg.String() == "right":
			_, end := m.composerSel.ordered()
			m.composerSel = composerSelection{}
			m.setComposerCursor(end)
			return m, finalize(m, cmds)
		default:
			inputBeforeSelection = m.input.Value()
			if composerSelectionDeletes(msg, m.input.KeyMap) {
				m.deleteComposerSelection()
				m.growInputToFit()
				m.updateCompletion()
				if shouldClearWideInputChange(inputBeforeSelection, m.input.Value()) {
					cmds = append(cmds, tea.ClearScreen)
				}
				return m, finalize(m, cmds)
			}
			if composerSelectionReplaces(msg, m.input.KeyMap) {
				m.deleteComposerSelection()
			} else {
				m.composerSel = composerSelection{}
			}
		}
	}
	// Transcript scroll keys work in any state (PgUp/PgDn are never text).
	switch msg.String() {
	case "pgup":
		next, sc := m.startSmoothScroll(m.viewport.YOffset() - m.viewport.Height())
		return next, finalize(next, append(cmds, sc))
	case "pgdown":
		next, sc := m.startSmoothScroll(m.viewport.YOffset() + m.viewport.Height())
		return next, finalize(next, append(cmds, sc))
	case "ctrl+home":
		m.viewport.GotoTop()
		return m, finalize(m, cmds)
	case "ctrl+end":
		m.viewport.GotoBottom()
		return m, finalize(m, cmds)
	case "ctrl+z":
		return m, suspendWithMouseReset()
	}
	// From this point on the key belongs to the active control rather than
	// transcript navigation. Editing or moving the insertion cursor restores
	// the textarea's normal caret-following viewport.
	m.followComposerCursor()
	// A question card is modal: keys drive it. In its free-text ("Type
	// something") mode, the keystroke goes to the textarea — Enter confirms the
	// custom answer, Esc backs out of typing — so input/IME work as usual.
	if m.chooser != nil {
		if m.chooser.typing {
			switch msg.String() {
			case "enter":
				val := strings.TrimSpace(m.input.Value())
				m.input.Reset()
				m.chooser.typing = false
				m.refreshInputPlaceholder()
				if val == "" {
					return m, finalize(m, cmds)
				}
				m.chooser.custom[m.chooser.tab] = val
				m.chooser.sel[m.chooser.tab] = map[int]bool{}
				return m.chooserAdvance()
			case "esc":
				m.chooser.typing = false
				m.input.Reset()
				m.refreshInputPlaceholder()
				return m, finalize(m, cmds)
			}
			beforeInput := m.input.Value()
			var ic tea.Cmd
			m.input, ic = m.input.Update(msg)
			cmds = append(cmds, ic)
			m.growInputToFit()
			if shouldClearWideInputChange(beforeInput, m.input.Value()) {
				cmds = append(cmds, tea.ClearScreen)
			}
			return m, finalize(m, cmds)
		}
		return m.handleChooserKey(msg)
	}
	// The rewind picker is modal while open: keys navigate it.
	if m.rewind != nil {
		return m.handleRewindKey(msg)
	}
	// The MCP import picker is modal while open: keys select candidates.
	if m.mcpImport != nil {
		return m.handleMCPImportKey(msg)
	}
	// Copy picker is modal while open.
	if m.copyPick != nil {
		return m.handleCopyPickerKey(msg)
	}
	// The resume picker is modal while open: keys navigate it.
	if m.resumePick != nil {
		return m.handleResumePickerKey(msg)
	}
	// Searchable command pickers are modal while open.
	if m.quickPick != nil {
		return m.handleQuickPickerKey(msg)
	}
	// The MCP manager is modal while open: keys navigate it.
	if m.mcp != nil {
		return m.handleMCPManagerKey(msg)
	}
	// The destructive /clear confirmation is modal while open.
	if m.clearConfirm != nil {
		return m.handleClearConfirmKey(msg)
	}
	// The skill picker is modal while open: keys navigate it.
	if m.skillPick != nil {
		return m.handleSkillPickerKey(msg)
	}
	// A pending tool approval is modal: keystrokes answer it (y/a/n, Enter,
	// Esc) rather than reaching the input.
	if m.pendingApproval != nil {
		return m.handleApprovalKey(msg)
	}
	// While the autocomplete menu is open it captures navigation/accept keys
	// (↑/↓ move, Tab/Enter accept, Esc close); everything else falls through
	// to the textarea and re-filters the menu at the end of Update.
	if m.completion.active {
		switch msg.String() {
		case "up", "ctrl+p":
			m.moveCompletion(-1)
			return m, nil
		case "down", "ctrl+n":
			m.moveCompletion(1)
			return m, nil
		case "tab", "enter":
			if msg.String() == "enter" && (m.completionExactLabel() || m.completionBareOverlayCommand()) {
				m.completion = completion{}
				break // fall through to regular Enter and submit the command
			}
			// When Enter is pressed and the selected completion is already fully
			// present in the input, close the menu and submit instead of accepting
			// the same item again (/resume 1 still has /resume 10 as a prefix match).
			if msg.String() == "enter" && m.completionSelectedInsertPresent() {
				m.completion = completion{}
				break // fall through to regular Enter
			}
			m.acceptCompletion()
			return m, nil
		case "esc":
			m.completion = completion{}
			if m.state == tuiRunning {
				break // a turn is running — also cancel it via the main Esc handler
			}
			return m, nil
		}
	}
	// Empty-input "?" cheatsheet: Esc closes before cancel/clear (spec §7.2 #2).
	// While open, other keys are swallowed so the parent draft is not mutated.
	if m.cheatsheetOpen {
		return m.handleCheatsheetKey(msg)
	}
	// Idle empty-input "?" opens the keyboard cheatsheet (does not insert).
	// Non-empty composer falls through so "?" is typed normally.
	if m.openCheatsheetIfEmpty(msg) {
		return m, nil
	}
	switch msg.String() {
	case "up":
		if m.state == tuiRunning {
			if m.navigateQueue(-1) {
				return m, nil
			}
		} else if m.recallSubmittedInput(-1) {
			return m, nil
		}
	case "down":
		if m.state == tuiRunning {
			if m.navigateQueue(1) {
				return m, nil
			}
		} else if m.recallSubmittedInput(1) {
			return m, nil
		}
	case "enter":
		// Don't reset queue navigation — the Enter handler below needs
		// queueEditCursor to decide whether to save an edit or enqueue.
	default:
		m.resetSubmittedInputRecall()
		m.resetQueueNavigation()
	}
	if imagePasteShortcut(msg.String(), runtime.GOOS) {
		if m.state == tuiRunning {
			return m, nil
		}
		if cmd := m.beginClipboardImagePaste(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, finalize(m, cmds)
	}
	switch msg.String() {
	case "esc":
		// "Back out" of the most specific in-progress state: un-send a just-sent
		// turn (server not yet replied), cancel a streaming turn, or clear
		// typed-but-unsent input. Mode switches (normal/plan/YOLO) are
		// exclusively driven by Shift+Tab — Esc must not silently flip a
		// session from plan or YOLO back to a less-permissive mode. PR #3051
		// removed the YOLO half of this; plan mode was missed and is fixed
		// here. Scrollback is the terminal's now, so there's no viewport to
		// dismiss.
		switch {
		case m.state == tuiRunning && m.bubblePending:
			m.unsendPending()
		case m.state == tuiRunning:
			m.ctrl.Cancel()
			// Defensive: if the controller is no longer running (cancel
			// completed synchronously, e.g. for shell commands), transition
			// to idle immediately instead of waiting for TurnDone.
			if !m.ctrl.Running() {
				m.state = tuiIdle
				m.confirmBubbleSent()
			}
		default:
			// Idle (any mode): a double-Esc on an empty composer opens the
			// rewind picker (Claude Code's gesture); a first Esc just arms
			// it. Non-empty input clears as before.
			if strings.TrimSpace(m.input.Value()) == "" {
				if !m.lastEsc.IsZero() && time.Since(m.lastEsc) < 600*time.Millisecond {
					m.lastEsc = time.Time{}
					m.openRewind()
				} else {
					m.lastEsc = time.Now()
				}
			} else {
				m.input.Reset()
				m.pastedBlocks = nil
			}
		}
		return m, nil
	case "ctrl+c", "super+c", "meta+c":
		if m.state == tuiRunning {
			// Selection takes precedence: copy instead of cancel, same as idle.
			if sel.active && !sel.empty() {
				m.sel = sel
				text := m.selectedText()
				m.sel = selection{}
				cmds = append(cmds, m.copySelectionWithNotice(text))
				return m, finalize(m, cmds)
			}
			if m.bubblePending {
				m.unsendPending() // server not yet replied — restore text, leave no trace
			} else if m.cancelRequested() {
				m.ctrl.Cancel()
				return m, tea.Quit
			} else {
				m.ctrl.Cancel()
			}
			return m, nil
		}
		// Idle: an active text selection takes precedence over the
		// composer-clear / double-press-quit gestures. Standard terminal
		// convention is "Ctrl+C copies the selection" — the user can still
		// clear the input with a second Ctrl+C once the selection is gone.
		// Hoisting this branch above the clear branch also stops the
		// previous behaviour where Ctrl+C would dismiss a selection AND
		// wipe any draft text the user was typing — felt like the
		// selection was being silently lost.
		if sel.active && !sel.empty() {
			m.sel = sel // restore so selectedText() can read it
			text := m.selectedText()
			m.sel = selection{}
			cmds = append(cmds, m.copySelectionWithNotice(text))
			return m, finalize(m, cmds)
		}
		// No selection: if the composer has text, a single press clears it
		// (like Esc); on an empty composer a double-press within 1.5s quits.
		if strings.TrimSpace(m.input.Value()) != "" {
			m.input.Reset()
			m.pastedBlocks = nil
			m.lastCtrlCAt = time.Time{}
			return m, nil
		}
		if !m.lastCtrlCAt.IsZero() && time.Since(m.lastCtrlCAt) < 1500*time.Millisecond {
			return m, tea.Quit
		}
		m.lastCtrlCAt = time.Now()
		m.notice(i18n.M.CtrlCQuitHint)
		return m, finalize(m, nil)
	case "ctrl+d":
		return m, tea.Quit
	case "ctrl+l":
		if m.state != tuiRunning {
			m.finalizeStreamed()
			m.clearTranscriptDisplay()
			m.commitTranscriptSource(transcriptSource{kind: transcriptSourceBanner})
			m.transcriptDirty = true
			m.forceGotoBottom = true
			m.notice(i18n.M.SlashClsDone)
		}
		return m, finalize(m, cmds)
	case "ctrl+y", "super+y", "meta+y":
		m.toggleYoloMode()
		return m, nil
	case "ctrl+p":
		// Idle-only command palette. Completion / quick pickers / approvals
		// already claim Ctrl+P earlier for prev-item navigation (spec §8.2.1).
		// Cheatsheet also claims keys while open, so we only reach here on
		// the main shell with no higher modal.
		if m.state == tuiIdle {
			m.openCommandPalette()
		}
		return m, nil
	case "ctrl+o":
		m.toggleVerboseReasoning(m.state != tuiRunning)
		return m, finalize(m, cmds)
	case "ctrl+b":
		m.toggleShellOutput()
		return m, finalize(m, cmds)
	case "shift+tab":
		// Shift+Tab toggles Plan only. Tool approval stays on its own axis:
		// Ask/Auto are explicit choices, and YOLO is a separate Ctrl+Y toggle.
		m.cycleMode()
		return m, nil
	case "enter":
		if m.state == tuiRunning {
			line := strings.TrimSpace(m.input.Value())
			if line == "" {
				m.viewport.GotoBottom()
				return m, nil
			}
			if m.queueEditCursor >= 0 && m.queueEditCursor < len(m.pendingInterject) {
				// Save the edited text back to the queue slot.
				m.pendingInterject[m.queueEditCursor] = m.expandPastedBlocks(line)
				m.notice(fmt.Sprintf("queue [%d] updated", m.queueEditCursor+1))
				m.queueEditCursor = -1
				m.queueEditDraft = ""
			} else {
				m.pendingInterject = append(m.pendingInterject, m.expandPastedBlocks(line))
				m.notice("feedback queued — will send when the current turn finishes")
				m.queueEditCursor = -1
				m.queueEditDraft = ""
			}
			m.input.Reset()
			m.pastedBlocks = nil
			return m, finalize(m, cmds)
		}
		if m.modelSwitchPending {
			return m, nil // ignore Enter while /model switch is building
		}
		line := strings.TrimSpace(m.input.Value())

		if line == "" {
			m.viewport.GotoBottom()
			return m, nil
		}
		if line == "exit" || line == "quit" || line == ":q" {
			return m, tea.Quit
		}
		m.rememberSubmittedInput(line)
		// The raised composer drops back to the bottom the moment the user
		// submits anything (memory notes, shell commands, slash commands,
		// or a normal turn).
		m.composerRaisedRows = 0

		// "# <note>" quick-adds a memory line locally, no model turn. The
		// space keeps "#7" / "#issue" prompts from being swallowed.
		if note, ok := control.MemoryQuickAddNote(line); ok {
			m.input.Reset()
			m.pastedBlocks = nil
			if note == "" {
				m.notice(i18n.M.QuickRememberEmpty)
			} else if path, err := m.ctrl.QuickAdd(memory.ScopeProject, note); err != nil {
				m.notice("memory: " + err.Error())
			} else {
				m.notice(fmt.Sprintf(i18n.M.QuickRememberDoneFmt, path))
			}
			return m, finalize(m, cmds)
		}

		// "!<cmd>" runs a shell command directly, bypassing the model.
		if strings.HasPrefix(line, "!") {
			cmd := strings.TrimPrefix(line, "!")
			if strings.TrimSpace(cmd) == "" {
				m.input.Reset()
				m.pastedBlocks = nil
				m.notice(i18n.M.ShellExecEmpty)
				return m, finalize(m, cmds)
			}
			m.input.Reset()
			m.pastedBlocks = nil
			m.state = tuiRunning
			m.runStart = time.Now()
			m.elapsed = 0
			m.turnTokens = 0
			m.pendingRestore = line
			m.bubbleStartIdx = len(m.transcript)
			m.flushExploreCard()
			m.commitLine("")
			m.commitTranscriptSource(transcriptSource{
				kind: transcriptSourceUser, raw: line, planMode: m.planMode,
			})
			m.bubblePending = true
			m.turnDiscarded = false
			m.confirmBubbleSent() // shell events arrive instantly
			m.ctrl.RunShell(cmd)
			return m, m.workingBatch()
		}

		// Slash commands run locally without going through the model. A
		// '/'-leading line that's actually a dragged file path is an attachment,
		// not a command, so it's rewritten to an @reference instead.
		if control.SlashCodeCommentLine(line) {
			// Slash-prefixed code comments are prompt text, not commands.
			// Not a command. Fall through to normal message path.
		} else if strings.HasPrefix(line, "/") {
			if ref, ok := control.FileRefLine(line); ok {
				line = ref
			} else {
				m.input.Reset()
				m.pastedBlocks = nil
				cmds = append(cmds, m.runSlashCommand(line))
				return m, finalize(m, cmds)
			}
		}

		sentLine := m.expandPastedBlocks(line)
		m.input.Reset()

		// @references (local files / MCP resources, including inline image
		// attachments) are resolved off the event loop by the controller; the turn
		// starts when they resolve (refsResolvedMsg).
		if m.ctrl.HasRefs(sentLine) {
			cmds = append(cmds, m.resolveRefs(sentLine, sentLine, line))
			return m, finalize(m, cmds)
		}

		// Keep the expanded paste content as the raw turn, not the folded label,
		// so downstream consumers never see just the placeholder label.
		cmds = append(cmds, m.startTurnWithRaw(sentLine, sentLine, line, sentLine))
		return m, finalize(m, cmds)
	}

	return tailUpdate(m, msg, cmds, inputBeforeSelection)
}
