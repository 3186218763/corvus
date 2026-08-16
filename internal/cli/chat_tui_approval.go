package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"corvus/internal/agent"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/i18n"
	"corvus/internal/permission"
	"corvus/internal/recovery"
	"corvus/internal/tool"
)

func approvalChoices(a *event.Approval) []approvalChoice {
	if a == nil {
		return nil
	}
	var decisions []approvalChoice
	fresh := a.Fresh || control.RequiresFreshHumanApprovalTool(a.Tool)
	switch {
	case isRecoveryApprovalEvent(a):
		if a.Recovery != nil && a.Recovery.CanGrantTask {
			// allowForSession is reused only as a local UI marker. The recovery
			// handler maps it to a task-scoped semantic grant, never a session rule.
			decisions = []approvalChoice{{allow: true}, {allow: true, allowForSession: true}, {}}
		} else {
			decisions = []approvalChoice{{allow: true}, {}}
		}
	case a.Tool == planApprovalTool:
		decisions = []approvalChoice{{allow: true}, {}, {exitPlan: true}}
	case fresh && freshApprovalAllowsSession(a.Tool):
		decisions = []approvalChoice{{allow: true}, {allow: true, allowForSession: true}, {}}
	case fresh:
		decisions = []approvalChoice{{allow: true}, {}}
	default:
		decisions = []approvalChoice{
			{allow: true},
			{allow: true, allowForSession: true},
			{allow: true, allowForSession: true, persistToConfig: true},
			{},
		}
	}
	labels := approvalChoiceLabels(a)
	for i := range decisions {
		if i < len(labels) {
			decisions[i].label = labels[i]
		}
	}
	return decisions
}

func approvalChoiceLabels(a *event.Approval) []string {
	choices := i18n.M.FreshHumanApprovalChoices
	fresh := a.Fresh || control.RequiresFreshHumanApprovalTool(a.Tool)
	if isRecoveryApprovalEvent(a) {
		if isRecoveryPlanChangeApproval(a) {
			choices = i18n.M.RecoveryPlanChangeChoices
		} else {
			choices = i18n.M.RecoveryApprovalChoices
		}
		if !isRecoveryPlanChangeApproval(a) && a.Recovery != nil && a.Recovery.CanGrantTask {
			choices = i18n.M.RecoveryTaskGrantChoices
		}
	} else if a.Tool == planApprovalTool {
		choices = i18n.M.PlanApprovalChoices
	} else if !fresh {
		exactSessionRule := permission.SessionGrantRuleForScope(a.Tool, a.Subject)
		exactPersistentRule := permission.RememberRuleForScope(a.Tool, a.Subject)
		choices = fmt.Sprintf(i18n.M.ToolApprovalChoices, exactSessionRule, exactPersistentRule)
	}
	if a.Tool == control.SandboxEscapeApprovalTool {
		choices = i18n.M.SandboxEscapeApprovalChoices
	}
	if a.Tool == control.ManagedConfigWriteApprovalTool {
		choices = i18n.M.ConfigWriteApprovalChoices
	}
	if !fresh && a.Tool == "bash" && permission.BashCommandPrefix(a.Subject) != "" {
		prefixRule := permission.RememberRuleForScope(a.Tool, a.Subject)
		choices = fmt.Sprintf(i18n.M.BashPrefixChoices, prefixRule, prefixRule)
	}
	var labels []string
	for _, line := range strings.Split(choices, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 || line[0] < '1' || line[0] > '9' || line[1] != '.' {
			continue
		}
		labels = append(labels, strings.TrimSpace(line[2:]))
	}
	if isRecoveryApprovalEvent(a) && a.Recovery != nil && a.Recovery.CanGrantTask && len(labels) > 1 {
		if scope := strings.TrimSpace(a.Recovery.TaskGrantScope); scope != "" {
			labels[1] += " — " + scope
		}
	}
	return labels
}

// handleApprovalKey resolves a pending approval from a keystroke and re-arms the
// listener. 1/y/Enter allows once, 2/a allows for the rest of the session,
// 3/p writes an "always allow" rule to the config file for ordinary tool
// approvals. Fresh two-choice prompts use 2 for deny, while n/Esc and legacy 4
// still deny. Plan prompts use 1 to execute, 2/n/Esc to keep planning, and 3 to
// reject the pending plan and leave plan mode without executing it.
// Ctrl-C cancels the whole turn via the run context. For a plan approval
// (planApprovalTool), starting execution or explicitly exiting without execution
// drops the local [plan] tag and turns plan mode off on the controller.
func (m chatTUI) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	choices := approvalChoices(m.pendingApproval)
	answer := func(choice approvalChoice) (tea.Model, tea.Cmd) {
		allow, session, persist := choice.allow, choice.allowForSession, choice.persistToConfig
		if isRecoveryApprovalEvent(m.pendingApproval) {
			action := agent.RecoveryActionRevise
			if allow {
				action = agent.RecoveryActionContinue
				if session {
					action = agent.RecoveryActionContinueTask
				}
			}
			_ = m.ctrl.ResolveRecovery(m.pendingApproval.ID, action, "")
			m.pendingApproval = nil
			return m, nil
		}
		if m.pendingApproval.Tool == planApprovalTool && (allow || choice.exitPlan) {
			m.planMode = false
			m.ctrl.SetPlanMode(false)
		}
		m.ctrl.Approve(m.pendingApproval.ID, allow, session, persist)
		m.pendingApproval = nil
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c":
		m.ctrl.Cancel()
		return answer(approvalChoice{})
	case "up", "k", "ctrl+p":
		if m.approvalSelection < 0 && len(choices) > 0 {
			m.approvalSelection = 0
		} else if m.approvalSelection > 0 {
			m.approvalSelection--
		}
		return m, nil
	case "down", "j", "ctrl+n":
		if m.approvalSelection < len(choices)-1 {
			m.approvalSelection++
		}
		return m, nil
	case "enter":
		if m.approvalSelection >= 0 && m.approvalSelection < len(choices) {
			return answer(choices[m.approvalSelection])
		}
		return m, nil
	case "esc":
		return answer(approvalChoice{})
	}
	lower := strings.ToLower(msg.String())
	// Semantic shortcuts first (display uses a/b/c…; key "a" remains session-allow).
	switch lower {
	case "y":
		if len(choices) > 0 {
			return answer(choices[0])
		}
	case "a":
		for _, choice := range choices {
			if choice.allowForSession && !choice.persistToConfig {
				return answer(choice)
			}
		}
	case "p":
		for _, choice := range choices {
			if choice.persistToConfig {
				return answer(choice)
			}
		}
	case "n":
		return answer(approvalChoice{})
	}
	// Letter/digit index (b–z and 1–9; "a" already handled as semantic above).
	if idx := selectionIndexKey(lower); idx >= 0 {
		if idx < len(choices) && lower != "a" {
			return answer(choices[idx])
		}
		// Legacy muscle memory: tool approvals historically numbered deny as 4.
		if lower == "4" {
			return answer(approvalChoice{})
		}
	}
	return m, nil
}

func isRecoveryApprovalEvent(a *event.Approval) bool {
	return a != nil && (a.Kind == recovery.ApprovalKindRecovery || a.Recovery != nil)
}

func isRecoveryPlanChangeApproval(a *event.Approval) bool {
	if !isRecoveryApprovalEvent(a) || a.Recovery == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(a.Recovery.ChangeKind)) {
	case string(recovery.ChangeStrategy), string(recovery.ChangeScope):
		return true
	default:
		return false
	}
}

func freshApprovalAllowsSession(toolName string) bool {
	return toolName == control.SandboxEscapeApprovalTool || toolName == control.ManagedConfigWriteApprovalTool
}

func (m chatTUI) cancelRequested() bool {
	if m.state != tuiRunning || m.ctrl == nil {
		return false
	}
	return m.ctrl.CancelRequested()
}

func (m chatTUI) runningWorkingLine(cancelRequested, styled bool) string {
	if m.state != tuiRunning {
		return ""
	}
	if m.retryAttempt > 0 && !cancelRequested {
		return fmt.Sprintf("  "+i18n.M.ChatStatusRetryingFmt, m.spinner.View(), m.retryAttempt, m.retryMax)
	}

	var working string
	if cancelRequested {
		working = fmt.Sprintf("  "+i18n.M.ChatStatusCancellingFmt, m.spinner.View(), formatElapsedFixed(m.elapsed))
	} else {
		working = fmt.Sprintf("  "+i18n.M.ChatStatusThinkingFmt, m.spinner.View(), formatElapsedFixed(m.elapsed))
	}
	if m.turnTokens > 0 {
		working += " · ↓" + shortTokens(m.turnTokens)
	}
	if n := len(m.pendingInterject); n > 0 {
		var queued string
		if n == 1 {
			queued = " · ✎ feedback queued"
		} else {
			queued = fmt.Sprintf(" · ✎ %d queued", n)
		}
		if styled {
			working += dim(queued)
		} else {
			working += queued
		}
	}
	return working
}

// renderApprovalBanner is the slim notice shown below the input while a tool
// call (or a plan) awaits the user's decision.
func (m chatTUI) renderApprovalBanner() string {
	w := m.width
	if w < 10 {
		w = 10
	}
	if m.pendingApproval == nil {
		return ""
	}
	var text string
	var planDetails []string
	if m.pendingApproval.Tool == planApprovalTool {
		text = i18n.M.PlanApprovalPrompt
	} else if isRecoveryPlanChangeApproval(m.pendingApproval) {
		text = i18n.M.RecoveryPlanDecisionPrompt
		if rec := m.pendingApproval.Recovery; rec != nil {
			if before := compactApprovalPlan(rec.PlanBefore); before != "" {
				planDetails = append(planDetails, fmt.Sprintf(i18n.M.RecoveryPlanBeforeFmt, truncateSubject(before, w)))
			}
			if after := compactApprovalPlan(rec.PlanAfter); after != "" {
				planDetails = append(planDetails, fmt.Sprintf(i18n.M.RecoveryPlanAfterFmt, truncateSubject(after, w)))
			}
		}
	} else {
		name, detail := approvalToolDetails(m.pendingApproval.Tool)
		subj := strings.TrimSpace(m.pendingApproval.Subject)
		if subj != "" {
			subj = " " + truncateSubject(subj, w)
		}
		text = strings.TrimSpace(fmt.Sprintf(i18n.M.ToolApprovalPromptFmt, name, subj, detail, ""))
	}
	if reason := strings.TrimSpace(m.pendingApproval.Reason); reason != "" {
		text += " · " + truncateSubject(reason, w)
	}
	var b strings.Builder
	contentWidth := max(w-4, 1)
	b.WriteString("⏸ " + viewCompactText(text, contentWidth) + "\n")
	for _, detail := range planDetails {
		b.WriteString(viewCompactText(detail, contentWidth) + "\n")
	}
	for i, choice := range approvalChoices(m.pendingApproval) {
		hint := ""
		switch {
		case choice.exitPlan:
			hint = ""
		case !choice.allow:
			hint = "n"
		case choice.persistToConfig:
			hint = "p"
		case choice.allowForSession:
			hint = "a"
		default:
			hint = "y"
		}
		b.WriteString(selectionRowWithHint(i == m.approvalSelection, i, "", choice.label, hint, false, w-4) + "\n")
	}
	b.WriteString(selectionFooter("y/a/p/n"))
	return selectionPanel(b.String(), w)
}

func compactApprovalPlan(plan string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(plan), "\n", " · ")), " ")
}

// approvalToolDetails turns provider-visible tool IDs into user-facing labels.
// MCP tools are advertised as mcp__<server>__<tool>; showing the short tool name
// first keeps the approval prompt readable while preserving the source.
func approvalToolDetails(toolName string) (name, detail string) {
	if toolName == control.SandboxEscapeApprovalTool {
		return i18n.M.ApprovalToolLabelSandboxEscape, fmt.Sprintf(i18n.M.ToolApprovalSourceFmt, i18n.M.ToolApprovalBuiltIn)
	}
	if toolName == control.ManagedConfigWriteApprovalTool {
		return i18n.M.ApprovalToolLabelConfigWrite, fmt.Sprintf(i18n.M.ToolApprovalSourceFmt, i18n.M.ToolApprovalBuiltIn)
	}
	if server, short, ok := tool.SplitMCPName(toolName); ok {
		lines := []string{}
		if strings.EqualFold(short, "understand_image") {
			lines = append(lines, i18n.M.ToolApprovalImageUse)
		}
		lines = append(lines, fmt.Sprintf(i18n.M.ToolApprovalSourceFmt, server))
		return short, strings.Join(lines, "\n")
	}
	return approvalToolLabel(toolName), fmt.Sprintf(i18n.M.ToolApprovalSourceFmt, i18n.M.ToolApprovalBuiltIn)
}

func approvalToolLabel(toolName string) string {
	switch toolName {
	case "bash":
		return i18n.M.ApprovalToolLabelBash
	case "edit_file":
		return i18n.M.ApprovalToolLabelEditFile
	case "write_file":
		return i18n.M.ApprovalToolLabelWriteFile
	case "multi_edit":
		return i18n.M.ApprovalToolLabelMultiEdit
	case "move_file":
		return i18n.M.ApprovalToolLabelMoveFile
	case "web_fetch":
		return i18n.M.ApprovalToolLabelWebFetch
	case "run_skill":
		return i18n.M.ApprovalToolLabelRunSkill
	case "remember":
		return i18n.M.ApprovalToolLabelRemember
	case "forget":
		return i18n.M.ApprovalToolLabelForget
	default:
		return toolName
	}
}
