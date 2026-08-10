package cli

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"corvus/internal/i18n"
)

func configureChatTextarea(ti *textarea.Model) {
	// Keep a stable two-cell input affordance, matching the prompt treatment in
	// other coding TUIs. Continuation rows receive two spaces so text and the
	// real terminal cursor stay aligned without repeating the arrow.
	ti.SetPromptFunc(composerPromptWidth, func(info textarea.PromptInfo) string {
		if info.LineNumber != 0 {
			return ""
		}
		if info.Focused {
			return accent("› ")
		}
		return dim("› ")
	})
	ti.CharLimit = 16384
	// The prompt and real terminal cursor already show where typing starts. Keep
	// the idle composer quiet; modal free-text questions set their own temporary
	// placeholder through refreshInputPlaceholder.
	ti.Placeholder = ""
	ti.DynamicHeight = true
	// Two-row idle field (Codex density); grows with content up to maxInputRows.
	ti.MinHeight = 2
	ti.MaxHeight = maxInputRows
	ti.MaxContentHeight = ti.CharLimit
	ti.SetHeight(2)
	ti.ShowLineNumbers = false
	applyTextareaTheme(ti)
	// Use the real terminal cursor (not a styled virtual one) so View can place
	// it at the insertion point and IME candidate windows anchor to the input.
	ti.SetVirtualCursor(false)
	// Plain Enter submits (the chatTUI handler intercepts it), so the textarea's
	// own InsertNewline binding moves to Alt+Enter / Ctrl+J / Shift+Enter.
	ti.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j", "shift+enter"))
	ti.Focus()
}

func (m *chatTUI) refreshInputPlaceholder() {
	if m.chooserTyping() {
		m.input.Placeholder = i18n.M.AskTypeSomething
		return
	}
	m.input.Placeholder = ""
}

func (m *chatTUI) rememberSubmittedInput(input string) {
	if strings.TrimSpace(input) == "" {
		return
	}
	if len(m.submittedInputs) == 0 || m.submittedInputs[len(m.submittedInputs)-1] != input {
		m.submittedInputs = append(m.submittedInputs, input)
	}
	m.submittedInputCursor = -1
	m.submittedInputDraft = ""
}

func (m *chatTUI) recallSubmittedInput(delta int) bool {
	if len(m.submittedInputs) == 0 {
		return false
	}
	cursor := m.submittedInputCursor
	if cursor < 0 {
		if delta > 0 {
			return false
		}
		if m.input.Line() != 0 {
			return false // first-line Up enters history; lower lines navigate the draft
		}
		m.submittedInputDraft = m.input.Value()
		cursor = len(m.submittedInputs) - 1
	} else {
		cursor += delta
	}

	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(m.submittedInputs) {
		m.submittedInputCursor = -1
		m.input.SetValue(m.submittedInputDraft)
		m.growInputToFit()
		return true
	}
	m.submittedInputCursor = cursor
	m.input.SetValue(m.submittedInputs[cursor])
	m.growInputToFit()
	return true
}

func (m *chatTUI) resetSubmittedInputRecall() {
	m.submittedInputCursor = -1
	m.submittedInputDraft = ""
}

// navigateQueue moves through the pending interject queue during tuiRunning.
// delta < 0 means ↑ (older), delta > 0 means ↓ (newer). Returns true if the
// input was updated.
func (m *chatTUI) navigateQueue(delta int) bool {
	if len(m.pendingInterject) == 0 {
		return false
	}
	cursor := m.queueEditCursor
	if cursor < 0 {
		if delta > 0 {
			return false // already at "new draft" — nothing newer
		}
		// First ↑: save the current draft and jump to the last queued item.
		m.queueEditDraft = m.input.Value()
		cursor = len(m.pendingInterject) - 1
	} else {
		cursor += delta
	}

	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(m.pendingInterject) {
		// Past the end: restore the draft the user was composing.
		m.queueEditCursor = -1
		m.input.SetValue(m.queueEditDraft)
		m.growInputToFit()
		return true
	}
	m.queueEditCursor = cursor
	m.input.SetValue(m.pendingInterject[cursor])
	m.growInputToFit()
	return true
}

// resetQueueNavigation resets the queue browsing cursor so the user returns to
// normal input mode. Any in-progress edit is discarded (the queued item keeps
// its previous value).
func (m *chatTUI) resetQueueNavigation() {
	m.queueEditCursor = -1
	m.queueEditDraft = ""
}

// renderQueueIndicator renders the pending-message queue as dim text to show
// above the input box when messages are queued during a running turn.
func (m chatTUI) renderQueueIndicator() string {
	if m.state != tuiRunning || m.hideComposer() || len(m.pendingInterject) == 0 {
		return ""
	}
	queueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // dim grey
	highlightStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	var lines []string
	rowBudget := m.queueVisibleItemRows()
	if rowBudget == 0 {
		return ""
	}
	start, end := m.queueVisibleRange()
	hidden := len(m.pendingInterject) - (end - start)
	if rowBudget <= 1 && start < end {
		i := start
		cursor := " "
		style := queueStyle
		if m.queueEditCursor == i {
			cursor = "▸"
			style = highlightStyle
		}
		prefix := fmt.Sprintf("  %s [%d] ", cursor, i+1)
		suffix := ""
		if hidden > 0 {
			suffix = fmt.Sprintf(" · +%d queued", hidden)
		}
		previewWidth := max(m.composerFrameWidth()-visibleWidth(prefix)-visibleWidth(suffix), 1)
		line := prefix + ansi.Truncate(oneLineText(m.pendingInterject[i]), previewWidth, "…") + suffix
		return style.Render(ansi.Truncate(line, m.composerFrameWidth(), "…"))
	}
	if hidden > 0 {
		lines = append(lines, queueStyle.Render(fmt.Sprintf("  … %d queued hidden", hidden)))
	}
	for i := start; i < end; i++ {
		msg := m.pendingInterject[i]
		cursor := " "
		style := queueStyle
		if m.queueEditCursor == i {
			cursor = "▸"
			style = highlightStyle
		}
		prefix := fmt.Sprintf("  %s [%d] ", cursor, i+1)
		previewWidth := max(m.composerFrameWidth()-visibleWidth(prefix), 1)
		preview := ansi.Truncate(oneLineText(msg), previewWidth, "…")
		lines = append(lines, style.Render(prefix+preview))
	}
	return strings.Join(lines, "\n")
}

// queueVisibleRange leaves room for the transcript, working/status lines, and
// composer before deciding how many queued feedback previews fit. The current
// edit target, or the newest queued item when not editing, remains in view.
func (m chatTUI) queueVisibleRange() (int, int) {
	total := len(m.pendingInterject)
	if total == 0 {
		return 0, 0
	}
	available := m.queueVisibleItemRows()
	if total <= available {
		return 0, total
	}
	focus := m.queueEditCursor
	if focus < 0 || focus >= total {
		focus = total - 1
	}
	if available <= 1 {
		return focus, focus + 1
	}
	// Reserve one row for the hidden-count hint when the list is windowed.
	return visibleRange(total, focus, available-1)
}

func (m chatTUI) queueVisibleItemRows() int {
	if m.height <= 0 {
		return len(m.pendingInterject)
	}
	return m.interactivePanelBudget().queueRows
}

// interactivePanelBudget assigns the rows left after the status block,
// composer, and one transcript row. Completion gets first claim on spare rows
// because it is the active interaction; queued feedback and the persistent
// todo list retain a compact one-row representation when space is tight.
func (m chatTUI) interactivePanelBudget() interactivePanelBudget {
	if m.height <= 0 || m.hideComposer() {
		return interactivePanelBudget{}
	}

	remaining := m.height - m.computeStatusLineCount(m.composerFrameWidth()) - max(m.input.Height(), 1) - 1
	todos, done := m.todoPanelState()
	hasTodo := len(todos) > 0 && done < len(todos)
	hasQueue := m.state == tuiRunning && len(m.pendingInterject) > 0
	hasCompletion := m.completion.active && len(m.completion.items) > 0
	var budget interactivePanelBudget
	take := func(want int) int {
		if remaining <= 0 || want <= 0 {
			return 0
		}
		rows := min(want, remaining)
		remaining -= rows
		return rows
	}

	if hasCompletion {
		budget.completionRows = take(2) // selected item + footer
	}
	if hasQueue {
		budget.queueRows = take(1)
	}
	if hasTodo {
		budget.todoRows = take(renderedLineCount(m.renderTodoPanelItems(todos, done, 0)))
	}

	if hasCompletion {
		desired := min(maxCompRows+1, len(m.completion.items)+1)
		budget.completionRows += take(desired - budget.completionRows)
	}
	if hasQueue {
		// One extra row is needed only when a windowed queue needs its hidden
		// count hint; the allocator caps it to the live terminal budget.
		budget.queueRows += take(len(m.pendingInterject) + 1 - budget.queueRows)
	}
	if hasTodo {
		budget.todoRows += take(m.todoPanelDesiredRows(todos, done) - budget.todoRows)
	}
	return budget
}

func wrappedRowCount(s string, width int) int {
	if s == "" {
		return 0
	}
	return strings.Count(wrapStatusLine(s, width), "\n") + 1
}

func (m chatTUI) queueIndicatorRows(width int) int {
	if m.hideComposer() {
		return 0
	}
	return wrappedRowCount(m.renderQueueIndicator(), width)
}

// consumeModalPaste gives an open overlay exclusive ownership of terminal
// paste. Searchable overlays accept the text as a query; choice-only overlays
// swallow it so a hidden composer cannot accumulate an accidental command.
func (m *chatTUI) consumeModalPaste(content string) bool {
	query := oneLineText(content)
	switch {
	case m.skillPick != nil:
		p := m.skillPick
		if p.mode == pickerSkills && query != "" {
			p.searchActive = true
			p.query += query
			p.sel = clampSel(p.sel, p.filteredSkills())
		}
		return true
	case m.quickPick != nil:
		if query != "" {
			m.quickPick.query += query
			m.quickPick.selected = 0
		}
		return true
	case m.resumePick != nil && m.resumePick.quick != nil:
		if query != "" {
			m.resumePick.quick.query += query
			m.resumePick.quick.selected = 0
			m.resumePick.sel = 0
		}
		return true
	case m.chooserTyping():
		return false
	case m.hideComposer() || m.cheatsheetOpen:
		return true
	default:
		return false
	}
}

// scrollHiddenComposerOverlay routes mouse-wheel to the active modal list when
// the composer is hidden (skills/MCP/resume/…). Returns true when the wheel was
// consumed so the transcript does not scroll underneath the overlay.
func (m *chatTUI) scrollHiddenComposerOverlay(msg tea.MouseWheelMsg) bool {
	if !m.hideComposer() {
		return false
	}
	delta := 0
	switch msg.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return false
	}
	// Prefer the same step size as keyboard j/k (one row per notch).
	for i := 0; i < 3; i++ {
		if !m.nudgeHiddenComposerOverlay(delta) {
			break
		}
	}
	return true
}

// nudgeHiddenComposerOverlay moves the active overlay selection by one step.
// Returns false when the selection cannot move further in that direction.
func (m *chatTUI) nudgeHiddenComposerOverlay(delta int) bool {
	switch {
	case m.skillPick != nil:
		return m.nudgeSkillPicker(delta)
	case m.mcp != nil:
		p := m.mcp
		if p.stage != mcpStageList {
			return false
		}
		n := len(p.snapshot.servers)
		if n == 0 {
			return false
		}
		next := p.sel + delta
		if next < 0 || next >= n {
			return false
		}
		p.sel = next
		return true
	case m.resumePick != nil:
		r := m.resumePick
		if r.quick != nil {
			items := r.quick.filteredItems()
			if len(items) == 0 {
				return false
			}
			next := r.quick.selected + delta
			if next < 0 || next >= len(items) {
				return false
			}
			r.quick.selected = next
			r.sel = next
			return true
		}
		if len(r.sessions) == 0 {
			return false
		}
		next := r.sel + delta
		if next < 0 || next >= len(r.sessions) {
			return false
		}
		r.sel = next
		return true
	case m.quickPick != nil:
		items := m.quickPick.filteredItems()
		if len(items) == 0 {
			return false
		}
		next := m.quickPick.selected + delta
		if next < 0 || next >= len(items) {
			return false
		}
		m.quickPick.selected = next
		return true
	case m.copyPick != nil:
		if len(m.copyPick.parts) == 0 {
			return false
		}
		next := m.copyPick.sel + delta
		if next < 0 || next >= len(m.copyPick.parts) {
			return false
		}
		m.copyPick.sel = next
		return true
	default:
		return false
	}
}

func (m *chatTUI) nudgeSkillPicker(delta int) bool {
	p := m.skillPick
	if p == nil {
		return false
	}
	switch p.mode {
	case pickerSkills:
		items := p.skills
		if p.searchActive {
			items = p.filteredSkills()
		}
		if len(items) == 0 {
			return false
		}
		next := p.sel + delta
		if next < 0 || next >= len(items) {
			return false
		}
		p.sel = next
		return true
	case pickerSources:
		visible := p.visibleRoots()
		if len(visible) == 0 {
			return false
		}
		next := p.sourceSel + delta
		if next < 0 || next >= len(visible) {
			return false
		}
		p.sourceSel = next
		return true
	case pickerSourceSkills:
		skills := p.selectedRootSkills()
		if len(skills) == 0 {
			return false
		}
		next := p.sourceSkillSel + delta
		if next < 0 || next >= len(skills) {
			return false
		}
		p.sourceSkillSel = next
		return true
	case pickerDetail:
		actions := skillActionsFor(p.detailSkill)
		next := p.detailAction + delta
		if next < 0 || next >= len(actions) {
			return false
		}
		p.detailAction = next
		return true
	default:
		return false
	}
}

// hideComposer is the single ownership gate for the bottom composer.
//
// Rule for new CLI panels:
//   - If a panel is modal and keystrokes navigate/confirm/cancel the panel, hide
//     the composer so users do not see an inactive chat input.
//   - If a panel is input-owned (autocomplete, or chooser free-text mode), keep
//     the composer visible because the textarea is the active control.
//
// Whenever a new slash-command overlay or approval-style prompt is added, update
// this function and the modal layout tests together. Otherwise the panel may
// reserve rows for a composer that cannot receive input, leaving a confusing
// blank area at the bottom of the TUI.
func (m chatTUI) hideComposer() bool {
	if m.mcp != nil || m.clearConfirm != nil || m.mcpImport != nil || m.skillPick != nil || m.resumePick != nil || m.quickPick != nil || m.copyPick != nil || m.rewind != nil || m.pendingApproval != nil {
		return true
	}
	return m.chooser != nil && !m.chooser.typing
}

// transcriptHeight is the row budget left for the transcript viewport once the
// pinned bottom region is accounted for (at least one row).
func (m chatTUI) transcriptHeight() int {
	if h := m.height - m.bottomRows(); h > 1 {
		return h
	}
	return 1
}

// composerFrameWidth is the terminal width View uses for the bottom frame
// (composer + status block). Matches View's boxW floor.
func (m chatTUI) composerFrameWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	if w < 10 {
		return 10
	}
	return w
}

// composerContentWidth is the textarea SetWidth budget. The borderless field
// adds no chrome of its own; only the two-column ❯ prompt is reserved by the
// textarea, so the content budget is the full box width. The painter right-pads
// each line to the same width, keeping SetWidth and View in lockstep.
func (m chatTUI) composerContentWidth() int {
	return m.composerFrameWidth()
}

func (m *chatTUI) chooserTyping() bool {
	return m.chooser != nil && m.chooser.typing
}

// inputHeightLimit returns the number of visible textarea rows that fit without
// letting the complete composer block consume more than half the terminal or
// pushing the transcript below its minimum useful height. Panel and wrapped
// status rows are treated as fixed bottom chrome and remain outside the input
// viewport.
func (m chatTUI) inputHeightLimit() int {
	if m.height <= 0 {
		return maxInputRows
	}

	limit := maxInputRows
	// Match the bounded-composer convention used by other coding TUIs: the
	// borderless field is part of the half-screen budget, not extra rows added
	// afterward.
	halfScreen := max(1, m.height/2)
	limit = min(limit, halfScreen)

	// bottomRows includes the current composer. Remove it to get the fixed
	// panels/status budget, then reserve a readable slice of transcript. On
	// extremely short terminals one editable row still wins.
	fixedBottomRows := m.bottomRows()
	if !m.hideComposer() {
		fixedBottomRows -= m.input.Height()
	}
	available := max(1, m.height-fixedBottomRows-minTranscriptRows)
	return max(1, min(limit, available))
}

func (m *chatTUI) syncInputHeightLimit() {
	limit := m.inputHeightLimit()
	wantWidth := m.composerContentWidth()
	if m.input.MaxHeight != limit {
		m.followComposerCursor()
		m.input.MaxHeight = limit
	}
	// Always refresh width so mode-badge column changes (mode cycle, !shell)
	// reflow the textarea in lockstep with View's badge layout.
	// SetWidth recalculates DynamicHeight from the full soft-wrapped content,
	// clamping the visible viewport to the new limit while preserving the text.
	m.input.SetWidth(wantWidth)
}

func (m *chatTUI) growInputToFit() {
	if m.input.DynamicHeight {
		return
	}
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > maxInputRows {
		lines = maxInputRows
	}
	if lines != m.input.Height() {
		m.input.SetHeight(lines)
	}
}
