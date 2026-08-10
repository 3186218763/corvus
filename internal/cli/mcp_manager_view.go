package cli

// mcp_manager_view.go renders the /mcp manager overlay and its display strings.

import (
	"fmt"
	"strings"

	"corvus/internal/config"
	"corvus/internal/mcpdiag"
)

func (m chatTUI) renderMCPManager() string {
	if m.mcp == nil {
		return ""
	}
	return m.mcp.renderWithHeight(m.mainManagerWidth(), m.mainManagerBodyHeight())
}

func (p *mcpManager) render(width int) string {
	return p.renderWithHeight(width, 0)
}

func (p *mcpManager) renderWithHeight(width, maxBodyRows int) string {
	w := max(viewWidth(width), 10)
	contentWidth := max(w-2, 1)
	var body string
	switch p.stage {
	case mcpStageDetail:
		body = p.renderDetail(contentWidth, maxBodyRows)
	case mcpStageTools:
		body = p.renderTools(contentWidth, maxBodyRows)
	case mcpStageLogs:
		body = p.renderLogs(contentWidth)
	case mcpStageConfirmRemove:
		body = p.renderConfirmRemove(contentWidth)
	case mcpStageConfirmClearAuth:
		body = p.renderConfirmClearAuth(contentWidth)
	default:
		body = p.renderList(contentWidth, maxBodyRows)
	}
	return managerContentPanelStyle(w).Render(viewFitLines(body, contentWidth))
}

func (p *mcpManager) footerHint() string {
	switch p.stage {
	case mcpStageDetail:
		return "↑/↓ navigate · r refresh · Enter to select · Esc to back"
	case mcpStageTools, mcpStageLogs:
		return "Esc to back"
	case mcpStageConfirmRemove, mcpStageConfirmClearAuth:
		return "Enter to select · y confirm · n cancel · Esc to back"
	default:
		if len(p.snapshot.servers) == 0 {
			return "↑/↓ navigate · Enter to confirm · Esc to cancel"
		}
		return "↑/↓ navigate · r refresh · Enter for details · Esc to close"
	}
}

func (p *mcpManager) compactFooterHint() string {
	switch p.stage {
	case mcpStageDetail:
		return "↑↓ · Enter · Esc"
	case mcpStageTools, mcpStageLogs:
		return "Esc"
	case mcpStageConfirmRemove, mcpStageConfirmClearAuth:
		return "Enter · y · n/Esc"
	default:
		return "↑↓ · Enter · r · Esc"
	}
}

func (p *mcpManager) renderList(width int, maxBodyRows ...int) string {
	budget := 0
	if len(maxBodyRows) > 0 {
		budget = maxBodyRows[0]
	}
	limit := min(mcpListMaxRows, len(p.snapshot.servers))
	for {
		start, end := visibleRange(len(p.snapshot.servers), p.sel, limit)
		body := p.renderListWindow(width, start, end)
		if budget <= 0 || strings.Count(body, "\n")+1 <= budget || limit == 1 {
			return body
		}
		limit--
	}
}

func (p *mcpManager) renderListWindow(width, start, end int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", viewHeader("Manage MCP servers"))
	fmt.Fprintf(&b, "%s\n\n", viewMeta(fmt.Sprintf("%d servers", len(p.snapshot.servers))))
	if p.snapshot.err != "" {
		fmt.Fprintf(&b, "%s\n\n", yellow("config: "+p.snapshot.err))
	}
	if len(p.snapshot.servers) == 0 {
		b.WriteString(viewMeta("No MCP servers configured. Use /mcp add <name> ... to add one.") + "\n\n")
		return b.String()
	}
	if start > 0 {
		fmt.Fprintf(&b, "%s\n", viewMeta(fmt.Sprintf("↑ %d more above", start)))
	}
	lastGroup := ""
	for i := start; i < end; i++ {
		s := p.snapshot.servers[i]
		group := mcpServerGroupLabel(s)
		if group != lastGroup {
			if lastGroup != "" {
				b.WriteByte('\n')
			}
			header := group
			if group == "Global MCPs" && p.snapshot.configPath != "" {
				header += " (" + p.snapshot.configPath + ")"
			}
			fmt.Fprintf(&b, "  %s\n", bold(header))
			lastGroup = group
		}
		b.WriteString(p.renderListRow(i, s, width) + "\n")
	}
	if end < len(p.snapshot.servers) {
		fmt.Fprintf(&b, "%s\n", viewMeta(fmt.Sprintf("↓ %d more below", len(p.snapshot.servers)-end)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p *mcpManager) renderListRow(i int, s mcpServerView, width int) string {
	prefix := "    "
	if i == p.sel {
		prefix = accent("  › ")
	}
	nameWidth := min(28, max(12, width/3))
	name := compactMiddle(s.Name, nameWidth)
	status := mcpStatusLabel(s)
	meta := fmt.Sprintf("%s · %s", status, countText(s.Tools, "tool"))
	if s.Prompts > 0 {
		meta += " · " + countText(s.Prompts, "prompt")
	}
	if s.Resources > 0 {
		meta += " · " + countText(s.Resources, "resource")
	}
	if s.Transport != "" {
		meta = s.Transport + " · " + meta
	}
	used := visibleWidth(prefix) + nameWidth + 3
	meta = viewCompactText(meta, viewBudget(width, used))
	name = padRight(name, nameWidth)
	if i == p.sel {
		name = bold(name)
	}
	return fmt.Sprintf("%s%s · %s", prefix, name, viewMeta(meta))
}

func (p *mcpManager) renderDetail(width int, maxBodyRows ...int) string {
	budget := 0
	if len(maxBodyRows) > 0 {
		budget = maxBodyRows[0]
	}
	v, ok := p.selectedServer()
	if !ok {
		return "MCP server not found\n\n" + dim("Esc to back")
	}
	actions := mcpActionsFor(v, p.snapshot.configPath)
	if p.action < 0 {
		p.action = 0
	}
	if p.action >= len(actions) {
		p.action = max(0, len(actions)-1)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s MCP Server\n\n", bold(titleText(v.Name)))
	writeMCPDetailField(&b, "Status", mcpStatusLabel(v))
	if auth := mcpAuthLabel(v); auth != "" {
		writeMCPDetailField(&b, "Auth", auth)
	}
	writeMCPDetailField(&b, "Transport", fallbackText(v.Transport, "unknown"))
	if v.BuiltIn {
		writeMCPDetailField(&b, "Config location", "built-in")
	} else if v.Source == config.MCPSourceProjectMCPJSON {
		writeMCPDetailField(&b, "Config location", "current project .mcp.json")
	} else if v.Source == config.MCPSourceProjectConfig {
		writeMCPDetailField(&b, "Config location", "current project .corvus/config.toml")
	} else {
		loc := fallbackText(p.snapshot.configPath, "not saved")
		if loc != "not saved" {
			loc = viewCompactPath(loc, viewBudget(width, 18))
		}
		writeMCPDetailField(&b, "Config location", loc)
	}
	writeMCPDetailField(&b, "Capabilities", mcpCapabilitiesText(v))
	writeMCPDetailField(&b, "Tools", countText(v.Tools, "tool"))
	if line := mcpCommandLine(v); line != "" {
		writeMCPDetailField(&b, mcpCommandLabel(v), viewCompactText(line, viewBudget(width, 18)))
	}
	if len(v.EnvKeys) > 0 {
		writeMCPDetailField(&b, "Env", strings.Join(v.EnvKeys, ", "))
	}
	if v.Error != "" {
		writeMCPDetailField(&b, "Error", viewCompactText(v.Error, viewBudget(width, 18)))
	}
	b.WriteByte('\n')
	if len(actions) == 0 {
		b.WriteString(viewMeta("No actions available.") + "\n")
	} else {
		for i, a := range actions {
			b.WriteString(selectionRow(i == p.action, i, "", a.label, false) + "\n")
		}
	}
	full := strings.TrimRight(b.String(), "\n")
	if budget <= 0 || strings.Count(full, "\n")+1 <= budget {
		return full
	}
	return p.renderCompactDetail(v, actions, width, budget)
}

func (p *mcpManager) renderCompactDetail(v mcpServerView, actions []mcpActionItem, width, budget int) string {
	budget = max(budget, 1)
	if budget == 1 && len(actions) > 0 {
		return selectionRow(true, p.action, "", actions[p.action].label, false)
	}

	lines := []string{bold(viewCompactText(titleText(v.Name)+" MCP Server", width))}
	if budget >= 3 {
		meta := fmt.Sprintf("%s · %s · %s", mcpStatusLabel(v), fallbackText(v.Transport, "unknown"), countText(v.Tools, "tool"))
		lines = append(lines, viewCompactText(meta, width))
	}
	if len(actions) == 0 {
		if len(lines) < budget {
			lines = append(lines, viewMeta("No actions available."))
		}
		return strings.Join(lines, "\n")
	}

	limit := max(1, budget-len(lines))
	start, end := visibleRange(len(actions), p.action, limit)
	for i := start; i < end; i++ {
		lines = append(lines, selectionRow(i == p.action, i, "", actions[i].label, false))
	}
	return strings.Join(lines, "\n")
}

func (p *mcpManager) renderTools(width int, maxBodyRows ...int) string {
	budget := 0
	if len(maxBodyRows) > 0 {
		budget = maxBodyRows[0]
	}
	v, ok := p.selectedServer()
	if !ok {
		return "MCP server not found\n\n" + dim("Esc to back")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s tools\n\n", bold(v.Name))
	if len(v.ToolList) == 0 {
		b.WriteString(viewMeta("Current connection did not return tool details.") + "\n")
	} else {
		limit := len(v.ToolList)
		if limit > mcpToolMaxRows {
			limit = mcpToolMaxRows
		}
		for limit > 1 && budget > 0 {
			extra := len(v.ToolList) - limit
			rows := 2 + limit
			if extra > 0 {
				rows++
			}
			if rows <= budget {
				break
			}
			limit--
		}
		for _, t := range v.ToolList[:limit] {
			desc := t.Description
			if t.SchemaError != "" {
				desc = "unavailable: " + t.SchemaError
			}
			desc = viewCompactText(desc, viewBudget(width, 24))
			fmt.Fprintf(&b, "  %-20s %s\n", t.Name, viewMeta(desc))
		}
		if extra := len(v.ToolList) - limit; extra > 0 {
			fmt.Fprintf(&b, "%s\n", viewMore(extra, "tools"))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p *mcpManager) renderLogs(width int) string {
	v, ok := p.selectedServer()
	if !ok {
		return "MCP server not found\n\n" + dim("Esc to back")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s logs\n\n", bold(v.Name))
	if strings.TrimSpace(v.Error) == "" {
		b.WriteString(viewMeta("No failure log recorded for this MCP.") + "\n")
	} else {
		b.WriteString(viewProtectLines(v.Error, viewBudget(width, 2)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p *mcpManager) renderConfirmRemove(width int) string {
	v, ok := p.selectedServer()
	if !ok {
		return "MCP server not found\n\n" + dim("Esc to back")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Remove MCP server %q?\n", v.Name)
	b.WriteString(viewMeta("This removes it from Corvus config. It cannot be undone from this panel.") + "\n\n")
	b.WriteString(selectionRow(p.confirm == 0, 0, "", "Confirm remove", false) + "\n")
	b.WriteString(selectionRow(p.confirm == 1, 1, "", "Cancel", false) + "\n")
	return strings.TrimRight(b.String(), "\n")
}

func (p *mcpManager) renderConfirmClearAuth(width int) string {
	v, ok := p.selectedServer()
	if !ok {
		return "MCP server not found\n\n" + dim("Esc to back")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Clear authentication for MCP server %q?\n", v.Name)
	hint := "This removes local auth-like headers, environment values, and URL tokens; the server stays in config."
	b.WriteString(viewMeta(viewCompactText(hint, viewBudget(width, 0))) + "\n\n")
	b.WriteString(selectionRow(p.confirm == 0, 0, "", "Confirm clear authentication", false) + "\n")
	b.WriteString(selectionRow(p.confirm == 1, 1, "", "Cancel", false) + "\n")
	return strings.TrimRight(b.String(), "\n")
}

func mcpActionsFor(v mcpServerView, configPath string) []mcpActionItem {
	var out []mcpActionItem
	managed := v.BuiltIn || v.Source == config.MCPSourcePluginPackage
	if v.Tools > 0 || len(v.ToolList) > 0 {
		out = append(out, mcpActionItem{mcpActionViewTools, "View tools"})
	}
	if v.Status == "failed" {
		if mcpAuthStatus(v) == mcpdiag.AuthRequired {
			out = append(out, mcpActionItem{mcpActionAuth, "Authenticate"})
		} else {
			out = append(out, mcpActionItem{mcpActionConnect, "Retry"})
		}
		if mcpCanClearAuth(v) {
			out = append(out, mcpActionItem{mcpActionClearAuth, "Clear authentication"})
		}
		out = appendMCPFailureSecondaryActions(out, v, configPath)
		return out
	}
	switch v.Status {
	case "connected":
		out = append(out, mcpActionItem{mcpActionConnect, "Reconnect"})
	case "deferred":
		out = append(out, mcpActionItem{mcpActionConnect, "Reconnect"})
	case "disabled":
		out = append(out, mcpActionItem{mcpActionConnect, "Enable and connect"})
	}
	out = appendMCPConfigActions(out, v, configPath)
	if mcpCanClearAuth(v) {
		out = append(out, mcpActionItem{mcpActionClearAuth, "Clear authentication"})
	}
	if v.Status != "disabled" {
		out = append(out, mcpActionItem{mcpActionDisable, "Disable for this session"})
	}
	if !managed {
		out = append(out, mcpActionItem{mcpActionRemove, "Remove server"})
	}
	return out
}

func appendMCPFailureSecondaryActions(out []mcpActionItem, v mcpServerView, configPath string) []mcpActionItem {
	managed := v.BuiltIn || v.Source == config.MCPSourcePluginPackage
	if strings.TrimSpace(v.Error) != "" {
		out = append(out, mcpActionItem{mcpActionLogs, "View logs"})
	}
	out = appendMCPConfigActions(out, v, configPath)
	if v.Status != "disabled" {
		out = append(out, mcpActionItem{mcpActionDisable, "Disable for this session"})
	}
	if !managed {
		out = append(out, mcpActionItem{mcpActionRemove, "Remove server"})
	}
	return out
}

func appendMCPConfigActions(out []mcpActionItem, v mcpServerView, configPath string) []mcpActionItem {
	if v.Configured {
		path := mcpConfigPathForView(v, configPath)
		if !v.BuiltIn && v.Source != config.MCPSourcePluginPackage && path != "" {
			out = append(out, mcpActionItem{mcpActionEdit, "Edit config"})
		}
	}
	return out
}

func mcpConfigPathForView(v mcpServerView, fallback string) string {
	if path := strings.TrimSpace(v.ConfigPath); path != "" {
		return path
	}
	return strings.TrimSpace(fallback)
}

func writeMCPDetailField(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "%-16s %s\n", label+":", value)
}

func mcpStatusLabel(v mcpServerView) string {
	switch {
	case v.Status == "connected":
		return green("✓ connected")
	case v.Status == "failed" && mcpAuthStatus(v) == mcpdiag.AuthRequired:
		return yellow("⚠ needs authentication")
	case v.Status == "failed":
		return red("✕ failed")
	case v.Status == "deferred":
		return "○ preparing in background"
	case v.Status == "initializing":
		return "◌ connecting..."
	case v.Status == "disabled":
		return "○ disabled"
	default:
		return viewMeta("unknown")
	}
}

func mcpServerGroupLabel(v mcpServerView) string {
	switch mcpServerGroupRank(v) {
	case 0:
		return "Managed MCPs"
	case 1:
		return "Project MCPs"
	default:
		return "Global MCPs"
	}
}

func mcpAuthLabel(v mcpServerView) string {
	switch {
	case v.Status == "connected":
		return green("✓ authenticated")
	case mcpAuthStatus(v) == mcpdiag.AuthRequired:
		return red("✕ not authenticated")
	case mcpAuthStatus(v) == mcpdiag.AuthPossible:
		return yellow("may need authorization")
	default:
		return ""
	}
}

func mcpCapabilitiesText(v mcpServerView) string {
	var caps []string
	if v.HasTools || v.Tools > 0 {
		caps = append(caps, "tools")
	}
	if v.Prompts > 0 {
		caps = append(caps, "prompts")
	}
	if v.Resources > 0 {
		caps = append(caps, "resources")
	}
	if len(caps) == 0 {
		return "none"
	}
	return strings.Join(caps, ", ")
}

func mcpCommandLabel(v mcpServerView) string {
	if v.Transport == "http" || v.Transport == "sse" {
		return "URL"
	}
	return "Command"
}

func mcpCommandLine(v mcpServerView) string {
	if v.Transport == "http" || v.Transport == "sse" {
		return strings.TrimSpace(v.URL)
	}
	return strings.TrimSpace(v.Command + " " + strings.Join(v.Args, " "))
}
