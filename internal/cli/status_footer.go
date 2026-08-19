package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"corvus/internal/i18n"
)

const (
	statusFooterIndent = "  "
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

// renderCacheHitRate renders the current conversation's prompt-cache hit rate
// (Σhit / Σ(hit+miss) across the whole session), e.g. "cached 87.50%". Hidden
// until the provider reports any cache tokens (denominator 0).
func renderCacheHitRate(hit, miss int) string {
	if hit+miss <= 0 {
		return ""
	}
	return footerMetric(i18n.M.ChatCacheHitLabel, footerValue(cacheRateLabel("%s", hit, hit+miss)))
}

// primaryStatusLine renders the interaction half of the first footer row. The
// mode pill anchors the same row's left edge (statusPrimaryWithBadge); this
// half only carries contextual UI state. The model group is laid out
// separately so it can stay right-anchored on wide terminals and move as one
// unit on narrow terminals.
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
		if m.shouldCompactApprovalStatus(body) {
			body = i18n.M.ChatStatusPlanApprovalCompact
		} else {
			body = i18n.M.ChatStatusPlanApproval
		}
	case m.pendingApproval != nil:
		body = i18n.M.ChatStatusToolApproval
		if m.shouldCompactApprovalStatus(body) {
			body = i18n.M.ChatStatusToolApprovalCompact
		} else {
			body = i18n.M.ChatStatusToolApproval
		}
	case m.clipboardImagePending:
		body = yellow(i18n.M.ClipboardImagePastingHint)
	case m.copyNoticeText != "":
		body = green(m.copyNoticeText)
	case cancelRequested:
		body = i18n.M.CtrlCQuitHint
	case shellMode:
		body = i18n.M.ShellModeHint
	case m.ctrl != nil && m.ctrl.AutoApproveTools():
		body = footerValue(i18n.M.ChatStatusYoloIdle)
	default:
		body = footerValue(i18n.M.ChatStatusIdle)
	}
	status := statusFooterIndent + body
	if mt := m.mouseTag(); mt != "" {
		status += " · " + mt
	}
	return status
}

func (m chatTUI) shouldCompactApprovalStatus(fullBody string) bool {
	if m.pendingApproval == nil || m.height <= 0 || m.width <= 0 {
		return false
	}
	primary := statusFooterIndent + fullBody
	if mt := m.mouseTag(); mt != "" {
		primary += " · " + mt
	}
	statusRows := strings.Count(m.renderStatusBlock(primary, m.width), "\n") + 1
	workingRows := 0
	if m.state == tuiRunning {
		workingRows = wrappedRowCount(m.runningWorkingLine(m.cancelRequested(), false), m.width)
	}
	banner := m.renderApprovalBanner()
	bannerRows := strings.Count(banner, "\n") + 1
	return 1+workingRows+bannerRows+statusRows > m.height
}

// statusModelWorkGroup is the model token for the right side of the footer.
// Effort/Work are not permanent chrome (Codex density); they live on /status.
// A custom statusline still replaces every built-in data field.
func (m chatTUI) statusModelWorkGroup(maxWidth int) string {
	if m.statuslineCmd != "" && m.statuslineOut != "" {
		return ""
	}
	model := strings.TrimSpace(m.label)
	if model == "" {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = 1
	}
	// Bare model name (no "Model " label) — quieter, denser right cluster.
	if visibleWidth(model) <= maxWidth {
		return footerInfo(model)
	}
	return footerInfo(compactMiddle(model, maxWidth))
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

// abbrevHome shortens a path under the user's home directory to "~".
func abbrevHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// projectPath returns the workspace root of the current session, falling back
// to the process cwd when no controller (or no configured root) exists.
func (m chatTUI) projectPath() string {
	root := ""
	if m.ctrl != nil {
		root = m.ctrl.WorkspaceRoot()
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	return abbrevHome(root)
}

// statusRightGroup renders the right half of the single footer row:
// model · path (Codex density). Cache hit / CTX / Effort / Work are not
// permanent. A configured custom statusline replaces all of it.
func (m chatTUI) statusRightGroup(width int) string {
	if m.statuslineCmd != "" && m.statuslineOut != "" {
		return footerHint(ansi.Strip(m.statuslineOut))
	}
	var groups []string
	if model := m.statusModelWorkGroup(max(width/2, 8)); model != "" {
		groups = append(groups, model)
	}
	if path := m.projectPath(); path != "" {
		budget := max(width/3, 12)
		if rem := width - visibleWidth(strings.Join(groups, " · ")) - 3; rem > 0 && rem < budget {
			budget = rem
		}
		groups = append(groups, footerSecondary(compactMiddle(path, budget)))
	}
	return strings.Join(groups, " · ")
}

// layoutSingleStatusLine lays out the one footer row: left status text, right
// data group. When both fit they sit on one row (right group right-aligned);
// otherwise the combined line wraps at " · " group boundaries.
func layoutSingleStatusLine(left, right string, width int) string {
	switch {
	case right == "":
		return wrapStatusGroups(left, width)
	case left == "":
		return wrapStatusGroups(right, width)
	}
	full := left + " · " + right
	if visibleWidth(full) <= width {
		return left + strings.Repeat(" ", width-visibleWidth(left)-visibleWidth(right)) + right
	}
	return wrapStatusGroups(full, width)
}

// renderStatusBlock owns the single persistent footer row under the composer.
func (m chatTUI) renderStatusBlock(primary string, width int) string {
	if width <= 0 {
		width = 1
	}
	return layoutSingleStatusLine(primary, m.statusRightGroup(width), width)
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
