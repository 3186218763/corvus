package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"corvus/internal/i18n"
	"corvus/internal/provider"
)

const (
	statusFooterIndent   = "  "
	statusFooterGroupGap = 2
)

func footerLabel(label string) string {
	return themeFg(activeCLITheme.subtle, label)
}

func footerHint(hint string) string {
	return themeFg(activeCLITheme.subtle, hint)
}

func footerValue(value string) string {
	return themeFg(activeCLITheme.muted, value)
}

func footerInfo(value string) string {
	return themeFg(activeCLITheme.info, value)
}

func footerSecondary(value string) string {
	return themeFg(activeCLITheme.secondary, value)
}

func footerMetric(label, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return footerLabel(label) + " " + value
}

// formatElapsedFixed renders elapsed seconds right-aligned to a stable 3-column
// numeric width (>=999 clamps to 999); locale fmt strings add the unit (e.g.
// "s"/"秒") so the display width never jitters.
func formatElapsedFixed(sec int) string {
	if sec >= 999 {
		sec = 999
	}
	return fmt.Sprintf("%3d", sec)
}

// renderTurnReceipt renders the completed turn's cache-hit readout. Only the
// cache-hit segment survives: totals, in/out/reasoning/cost and cache-prefix
// warnings are intentionally dropped for a quiet footer.
func renderTurnReceipt(u *provider.Usage) string {
	if u == nil {
		return ""
	}
	return footerMetric(i18n.M.ChatCacheHitLabel, footerValue(shortTokens(u.CacheHitTokens)))
}

// primaryStatusLine renders the interaction half of the first footer row. Mode
// chrome lives on the composer badge (renderModeBadge); this row only carries
// contextual UI state and short action hints. The model/profile group is laid
// out separately so it can stay right-anchored on wide terminals and move as
// one unit on narrow terminals.
func (m chatTUI) primaryStatusLine(shellMode, cancelRequested bool) string {
	var body string
	switch {
	case m.rewind != nil:
		body = "⟲ rewind"
	case m.mcpImport != nil:
		body = "MCP import"
	case m.resumePick != nil:
		body = i18n.M.StatusResumePicker
	case m.quickPick != nil:
		body = m.quickPick.title
	case m.mcp != nil:
		body = "MCP"
	case m.skillPick != nil:
		body = i18n.M.SkillPickerStatusLabel
	case m.cheatsheetOpen:
		body = i18n.M.CheatsheetStatusLabel
	case m.chooser != nil:
		body = i18n.M.ChatStatusQuestion
	case m.pendingApproval != nil && m.pendingApproval.Tool == planApprovalTool:
		body = i18n.M.ChatStatusPlanApproval
	case m.pendingApproval != nil:
		body = i18n.M.ChatStatusToolApproval
	case m.clipboardImagePending:
		body = yellow(i18n.M.ClipboardImagePastingHint)
	case m.copyNoticeText != "":
		body = green(m.copyNoticeText)
	case cancelRequested:
		body = i18n.M.CtrlCQuitHint
	case shellMode:
		body = i18n.M.ShellModeHint
	case m.ctrl != nil && m.ctrl.AutoApproveTools():
		body = footerValue(i18n.M.ChatStatusYoloIdle) + " · " + footerHint(i18n.M.ChatStatusCycleHintCompact)
	default:
		body = footerValue(i18n.M.ChatStatusIdle) + " · " + footerHint(i18n.M.ChatStatusCycleHintCompact)
	}
	status := statusFooterIndent + body
	if mt := m.mouseTag(); mt != "" {
		status += " · " + mt
	}
	return status
}

// statusModelWorkGroup is the bounded, session-level group placed at the right
// edge of the first footer row. A custom statusline still replaces every
// built-in data field, matching its existing configuration contract.
func (m chatTUI) statusModelWorkGroup(maxWidth int) string {
	if m.statuslineCmd != "" && m.statuslineOut != "" {
		return ""
	}
	model := strings.TrimSpace(m.label)
	work := ""
	if m.runtimeProfile != "" {
		work = runtimeProfileDisplay(m.runtimeProfile)
	}
	if maxWidth <= 0 {
		maxWidth = 1
	}

	const separator = "   "
	tail := make([]string, 0, 2)
	if effort := m.effortTag(); effort != "" {
		tail = append(tail, effort)
	}
	if work != "" {
		tail = append(tail, footerMetric(i18n.M.ChatStatusWorkLabel, footerSecondary(work)))
	}
	if model == "" && len(tail) == 0 {
		return ""
	}

	fields := append([]string(nil), tail...)
	if model != "" {
		fields = append([]string{footerMetric(i18n.M.ChatStatusModelLabel, footerInfo(model))}, fields...)
	}
	full := strings.Join(fields, separator)
	if visibleWidth(full) <= maxWidth {
		return full
	}

	// Model names own the flexible slot. Keep effort and work intact while they
	// fit, and compact only the model before falling back to a bounded plain group.
	if model != "" {
		tailWidth := visibleWidth(strings.Join(tail, separator))
		if len(tail) > 0 {
			tailWidth += visibleWidth(separator)
		}
		modelBudget := maxWidth - tailWidth - visibleWidth(i18n.M.ChatStatusModelLabel+" ")
		if modelBudget >= 4 {
			modelField := footerMetric(i18n.M.ChatStatusModelLabel, footerInfo(compactMiddle(model, modelBudget)))
			if len(tail) == 0 {
				return modelField
			}
			return modelField + separator + strings.Join(tail, separator)
		}
	}
	return footerHint(compactMiddle(ansi.Strip(full), maxWidth))
}

func renderContextStatusGroups(used, window int, ratio float64) []string {
	if used == 0 || window == 0 {
		return nil
	}
	pct := used * 100 / window
	ctxValue := fmt.Sprintf("%s (%d%%)", shortTokens(used), pct)

	if ratio <= 0 || ratio >= 1 {
		ctxValue = fmt.Sprintf("%s / %s (%d%%)", shortTokens(used), shortTokens(window), pct)
		color := activeCLITheme.muted
		switch {
		case pct >= 85:
			color = activeCLITheme.danger
		case pct >= 60:
			color = activeCLITheme.warn
		}
		return []string{footerMetric(i18n.M.ChatStatusContextLabel, themeFg(color, ctxValue))}
	}

	threshold := int(ratio * 100)
	left := max(threshold-pct, 0)
	ctxColor := activeCLITheme.muted
	compactColor := activeCLITheme.muted
	switch {
	case pct >= threshold:
		// Preserve two levels of urgency from the selected design: context is a
		// warning, while the exhausted compaction headroom is the actual danger.
		ctxColor = activeCLITheme.warn
		compactColor = activeCLITheme.danger
	case left <= 10:
		ctxColor = activeCLITheme.warn
		compactColor = activeCLITheme.warn
	}
	return []string{
		footerMetric(i18n.M.ChatStatusContextLabel, themeFg(ctxColor, ctxValue)),
		footerMetric(i18n.M.ChatStatusCompactLabel, themeFg(compactColor, fmt.Sprintf("%d%%", left))),
	}
}

// statusTelemetryGroups returns independently placeable session metrics for the
// default data band: context (+ compact headroom) and jobs when > 0. Balance,
// cache diagnostics, and git porcelain live on /status instead of permanent chrome.
// A custom statusline still replaces this entire band.
func (m chatTUI) statusTelemetryGroups() []string {
	if m.statuslineCmd != "" && m.statuslineOut != "" {
		return []string{m.statuslineOut}
	}
	var data []string
	if m.ctrl != nil {
		used, window := m.ctrl.ContextSnapshot()
		data = append(data, renderContextStatusGroups(used, window, m.ctrl.CompactRatio())...)
		if jt := m.jobsTag(); jt != "" {
			data = append(data, footerMetric(i18n.M.ChatStatusJobsLabel, footerInfo(ansi.Strip(jt))))
		}
	}
	return data
}

// renderStatusBlock owns the complete persistent footer layout. The optional
// data band is separated from interaction state when telemetry exists; narrow
// screens add deliberate left-aligned rows only between semantic groups.
func (m chatTUI) renderStatusBlock(primary string, width int) string {
	if width <= 0 {
		width = 1
	}
	primary = hideStatusHintWhenKeyNamesCannotFit(primary, width)
	modelWork := m.statusModelWorkGroup(max(width-visibleWidth(statusFooterIndent), 1))
	first := layoutStatusSides(primary, modelWork, width)
	second := m.layoutDataBand(width)
	if second == "" {
		return first
	}
	return first + "\n" + statusFooterDivider(width) + "\n" + second
}

// hideStatusHintWhenKeyNamesCannotFit keeps the readable Shift+Tab/Ctrl+Y
// spelling on normal terminals without hard-wrapping a single shortcut on an
// extremely narrow terminal. In that case the idle state remains visible and
// the optional shortcut help yields space to the composer.
func hideStatusHintWhenKeyNamesCannotFit(primary string, width int) string {
	hint := i18n.M.ChatStatusCycleHintCompact
	for _, group := range strings.Split(hint, " · ") {
		if visibleWidth(statusFooterIndent+group) > width {
			return strings.Replace(primary, " · "+footerHint(hint), "", 1)
		}
	}
	return primary
}

func statusFooterDivider(width int) string {
	width = max(width, 1)
	if width <= visibleWidth(statusFooterIndent) {
		return themeFg(activeCLITheme.border, strings.Repeat("─", width))
	}
	ruleWidth := width - visibleWidth(statusFooterIndent)
	return statusFooterIndent + themeFg(activeCLITheme.border, strings.Repeat("─", ruleWidth))
}

func layoutStatusSides(left, right string, width int) string {
	switch {
	case right == "":
		return wrapStatusGroups(left, width)
	case left == "":
		return rightAlignStatusGroup(right, width)
	}
	leftWidth := visibleWidth(left)
	rightWidth := visibleWidth(right)
	if leftWidth+statusFooterGroupGap+rightWidth <= width {
		return left + strings.Repeat(" ", width-leftWidth-rightWidth) + right
	}
	// Once the two semantic halves no longer fit, switch layout deliberately:
	// interaction groups wrap only at their separators, while model/work owns a
	// new left-aligned row. This avoids the floating right-side orphan seen when
	// a terminal crosses the medium-width breakpoint.
	return wrapStatusGroups(left, width) + "\n" + statusFooterIndent + right
}

func wrapStatusGroups(line string, width int) string {
	if width <= 0 || line == "" || visibleWidth(line) <= width {
		return line
	}
	groups := strings.Split(line, " · ")
	if len(groups) < 2 {
		return wrapStatusLine(line, width)
	}

	var rows []string
	current := groups[0]
	for _, group := range groups[1:] {
		candidate := current + " · " + group
		if visibleWidth(candidate) <= width {
			current = candidate
			continue
		}
		rows = append(rows, wrapStatusLine(current, width))
		current = statusFooterIndent + group
	}
	rows = append(rows, wrapStatusLine(current, width))
	return strings.Join(rows, "\n")
}

func rightAlignStatusGroup(group string, width int) string {
	if group == "" {
		return ""
	}
	if visibleWidth(group) <= width {
		return strings.Repeat(" ", width-visibleWidth(group)) + group
	}
	return wrapStatusLine(group, width)
}

// layoutDataBand packs the lean default telemetry groups (or custom statusline
// output) left-to-right by semantic group. Git/balance/cache are not rendered
// here; /status hosts that detail.
func (m chatTUI) layoutDataBand(width int) string {
	return packStatusGroups(m.statusTelemetryGroups(), width)
}

// layoutGitTelemetry is retained as a thin alias for older call sites/tests.
func (m chatTUI) layoutGitTelemetry(width int) string {
	return m.layoutDataBand(width)
}

func packStatusGroups(groups []string, width int) string {
	width = max(width, 1)
	if len(groups) == 0 {
		return ""
	}
	indent := statusFooterIndent
	if width <= visibleWidth(indent) {
		indent = ""
	}

	var rows []string
	current := indent
	for _, group := range groups {
		if strings.TrimSpace(ansi.Strip(group)) == "" {
			continue
		}
		candidate := current + group
		if strings.TrimSpace(ansi.Strip(current)) != "" {
			candidate = current + "  " + group
		}
		if visibleWidth(candidate) <= width {
			current = candidate
			continue
		}
		if strings.TrimSpace(ansi.Strip(current)) != "" {
			rows = append(rows, current)
		}
		current = indent + group
		if visibleWidth(current) > width {
			rows = append(rows, wrapStatusLine(current, width))
			current = indent
		}
	}
	if strings.TrimSpace(ansi.Strip(current)) != "" {
		rows = append(rows, current)
	}
	return strings.Join(rows, "\n")
}
