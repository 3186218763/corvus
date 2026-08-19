package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"corvus/internal/i18n"
	"corvus/internal/runtimepolicy"
)

func (m *chatTUI) runRuntimePolicyCommand(input string) tea.Cmd {
	args := tokenizeArgs(input)
	if len(args) == 1 {
		m.commitLine(renderRuntimePolicy(m.width, "", m.runtimeGuidance, m.runtimeCompletion, m.runtimeExposure, m.resolvedRuntimePolicy()))
		return nil
	}
	if len(args) != 3 {
		m.notice(i18n.M.RuntimePolicyUsage)
		return nil
	}
	axis := strings.ToLower(strings.TrimSpace(args[1]))
	value := args[2]
	guidance, completion, exposure := m.runtimeGuidance, m.runtimeCompletion, m.runtimeExposure
	switch axis {
	case "guidance":
		sel, err := runtimepolicy.ParseGuidanceSelection(value)
		if err != nil {
			m.notice(err.Error())
			return nil
		}
		guidance = string(sel)
	case "completion":
		sel, err := runtimepolicy.ParseCompletionSelection(value)
		if err != nil {
			m.notice(err.Error())
			return nil
		}
		completion = string(sel)
	case "exposure":
		sel, err := runtimepolicy.ParseExposureSelection(value)
		if err != nil {
			m.notice(err.Error())
			return nil
		}
		exposure = string(sel)
	default:
		m.notice(i18n.M.RuntimePolicyUsage)
		return nil
	}
	if m.buildController == nil || m.ctrl == nil {
		m.notice(i18n.M.RuntimePolicySwitchUnavailable)
		return nil
	}
	if m.modelSwitchPending {
		m.notice(i18n.M.RuntimeSwitchPending)
		return nil
	}
	if m.runtimeSwitchBusy() {
		m.notice(i18n.M.RuntimePolicySwitchBusy)
		return nil
	}
	if normalizePolicySelection(m.runtimeGuidance) == normalizePolicySelection(guidance) &&
		normalizePolicySelection(m.runtimeCompletion) == normalizePolicySelection(completion) &&
		normalizePolicySelection(m.runtimeExposure) == normalizePolicySelection(exposure) {
		m.notice(fmt.Sprintf(i18n.M.RuntimePolicyAlreadyOnFmt, axis, value))
		return nil
	}
	if err := m.ctrl.Snapshot(); err != nil {
		m.notice("runtime-policy: snapshot failed: " + err.Error())
	}
	carried := m.ctrl.History()
	resumePath := m.ctrl.SessionPath()
	if err := m.rebindSessionLease(resumePath); err != nil {
		m.notice("runtime-policy: " + sessionLeaseHeldNotice(err))
		return nil
	}
	m.notice(fmt.Sprintf(i18n.M.RuntimePolicySwitchingFmt, axis, value))
	m.noticeCacheInvalidation(cacheInvalidationReasonRuntimePolicy)
	m.armControllerRebuild(controllerBuildSpec{
		ModelRef:       m.modelRef,
		RuntimeProfile: "",
		Guidance:       guidance,
		Completion:     completion,
		Exposure:       exposure,
	}, carried, resumePath, modelSwitchMsg{
		ref:           m.modelRef,
		profile:       "",
		guidance:      guidance,
		completion:    completion,
		exposure:      exposure,
		failurePrefix: "runtime-policy",
		successNotice: fmt.Sprintf(i18n.M.RuntimePolicySwitchedFmt, axis, value),
	})
	return m.pendingModelSwitch
}

func (m *chatTUI) resolvedRuntimePolicy() runtimepolicy.Policy {
	if m != nil && m.ctrl != nil {
		return m.ctrl.RuntimePolicy()
	}
	return runtimepolicy.Policy{}
}

func normalizePolicySelection(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "inherit"
	}
	return s
}

func renderRuntimePolicy(width int, preset, guidance, completion, exposure string, resolved runtimepolicy.Policy) string {
	_ = width
	_ = preset // deprecated compatibility input is intentionally not rendered
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", viewHeader("%s", i18n.M.RuntimePolicyHeader))
	fmt.Fprintf(&b, "  guidance    %s  → %s\n", displaySelection(guidance), displayResolved(string(resolved.Guidance)))
	fmt.Fprintf(&b, "  completion  %s  → %s\n", displaySelection(completion), displayResolved(string(resolved.Completion)))
	fmt.Fprintf(&b, "  exposure    %s  → %s\n", displaySelection(exposure), displayResolved(string(resolved.Exposure)))
	b.WriteString(viewHint(i18n.M.RuntimePolicyListHint))
	return strings.TrimRight(b.String(), "\n")
}

func displaySelection(raw string) string {
	return normalizePolicySelection(raw)
}

func displayResolved(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "-"
	}
	return raw
}

func (m *chatTUI) runtimePolicyArgItems(val string) ([]compItem, int, bool) {
	cmdEnd := strings.IndexAny(val, " \t")
	if cmdEnd < 0 {
		return nil, 0, false
	}
	if val[:cmdEnd] != "/runtime-policy" {
		return nil, 0, false
	}
	from := strings.LastIndexAny(val, " \t") + 1
	fields := strings.Fields(val[:from])
	query := strings.ToLower(val[from:])
	var options []workModeOption
	switch len(fields) {
	case 1:
		options = []workModeOption{
			{name: "guidance", desc: "off | light | structured | auto | inherit"},
			{name: "completion", desc: "standard | verified | auto | inherit"},
			{name: "exposure", desc: "eager | deferred | auto | inherit"},
		}
	case 2:
		switch strings.ToLower(fields[1]) {
		case "guidance":
			options = []workModeOption{
				{name: "inherit", desc: "use legacy session metadata when present"},
				{name: "auto", desc: "capability × effort matrix"},
				{name: "off", desc: "no guidance fragment"},
				{name: "light", desc: "short plan before acting"},
				{name: "structured", desc: "small steps, revisit the plan"},
			}
		case "completion":
			options = []workModeOption{
				{name: "inherit", desc: "use legacy session metadata when present"},
				{name: "auto", desc: "standard completion"},
				{name: "standard", desc: "ordinary turn completion"},
				{name: "verified", desc: "delivery evidence contract"},
			}
		case "exposure":
			options = []workModeOption{
				{name: "inherit", desc: "use legacy session metadata when present"},
				{name: "auto", desc: "eager tool surface"},
				{name: "eager", desc: "full startup surface"},
				{name: "deferred", desc: "core tools; connect on demand"},
			}
		default:
			return nil, from, true
		}
	default:
		return nil, from, true
	}
	var out []compItem
	for _, option := range options {
		if query != "" && !strings.HasPrefix(option.name, query) {
			continue
		}
		out = append(out, compItem{label: option.name, insert: option.name, hint: option.desc})
	}
	return out, from, true
}
