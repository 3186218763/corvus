package boot

import (
	"fmt"
	"log/slog"
	"strings"

	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/hook"
	"corvus/internal/permission"
	"corvus/internal/sandbox"
)

type hookResult struct {
	policy       permission.Policy
	headlessGate *control.SharedHeadlessGate
	hookRunner   *hook.Runner
}

// buildPermissionAndHooks constructs the permission policy, the shared
// headless approval gate, and the hook runner.
func buildPermissionAndHooks(cfg *config.Config, opts Options, root string, shell sandbox.Shell, sink event.Sink) (*hookResult, error) {
	// Permission policy gates every tool call. With no HeadlessApprovalMode
	// (interactive bootstrap), the temporary gate preserves the legacy behavior
	// until chat/desktop installs an interactive gate. A real headless caller
	// such as `corvus run` always supplies a mode: Ask fails closed, Auto
	// allows ordinary writer fallbacks, and DontAsk denies them (#6927).
	// The selected contract is also applied to sub-agents, so they cannot be a
	// weaker path around the parent gate.
	// Sub-agents always run headless: they have no UI to answer a prompt, so they
	// inherit this same gate.
	policy := permission.New(cfg.Permissions.Mode, cfg.Permissions.Allow, cfg.Permissions.Ask, cfg.Permissions.Deny).
		WithAllowDynamicBashFallback(cfg.Permissions.AllowDynamicBash).
		WithSessionAllow(opts.PermissionAllow)
	headlessGate := control.NewSharedHeadlessGate(policy, opts.HeadlessApprovalMode)

	// Hooks: load the global settings.json plus the project's. Non-blocking hook
	// output is surfaced to the user as a Notice through the shared sink. The
	// runner fires PreToolUse/PostToolUse in the agent loop and
	// PermissionRequest/UserPromptSubmit/Stop at the controller boundary.
	resolvedHooks := hook.Load(hook.LoadOptions{ProjectRoot: root})
	hookRuntime := hook.RuntimeOptions{}
	if shell.Kind == sandbox.ShellBash {
		hookRuntime.BashPath = shell.Path
	}
	hookRunner := hook.NewRunner(
		resolvedHooks, root, hook.NewDefaultSpawner(hookRuntime),
		func(msg string) { sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: msg}) },
	)

	return &hookResult{
		policy:       policy,
		headlessGate: headlessGate,
		hookRunner:   hookRunner,
	}, nil
}

func rememberPermissionRule(workspaceRoot, rule string) control.RememberResult {
	path := rememberPermissionConfigPath(workspaceRoot)
	result := control.RememberResult{Rule: strings.TrimSpace(rule), Path: path}
	unlock, err := config.LockConfigFileEdits(path)
	if err != nil {
		slog.Warn("lock config for permission rule", "path", path, "err", err)
		result.Err = err
		return result
	}
	defer unlock()

	edit, err := config.LoadForEditReadOnlyStrict(path)
	if err != nil {
		slog.Warn("load config for permission rule", "path", path, "err", err)
		result.Err = err
		return result
	}
	if coveredBy := coveredPermissionRule(edit.Permissions.Allow, result.Rule); coveredBy != "" {
		result.CoveredBy = coveredBy
		return result
	}
	edit.Permissions.Allow = pruneCoveredPermissionRules(edit.Permissions.Allow, result.Rule)
	if err := edit.AddPermissionRule("allow", rule); err != nil {
		slog.Warn("persist permission rule", "rule", rule, "err", err)
		result.Err = err
		return result
	}
	if err := config.WritePermissionsAllow(path, edit.Permissions.Allow); err != nil {
		slog.Warn("save config after permission rule", "err", err)
		result.Err = err
		return result
	}
	result.Saved = true
	return result
}

func rememberPermissionConfigPath(workspaceRoot string) string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot != "" {
		return config.ProjectConfigPathForRoot(workspaceRoot)
	}
	path := config.SourcePath()
	if path == "" {
		path = config.ProjectConfigPathForRoot(".") // match Config.Save() fallback
	}
	return path
}

func rememberPlanModeReadOnlyCommand(workspaceRoot, prefix string) control.PlanModeReadOnlyCommandTrustResult {
	prefix = strings.TrimSpace(prefix)
	path := rememberPermissionConfigPath(workspaceRoot)
	result := control.PlanModeReadOnlyCommandTrustResult{Prefix: prefix, Path: path}
	if prefix == "" {
		result.Err = fmt.Errorf("empty plan-mode read-only command prefix")
		return result
	}
	unlock, err := config.LockConfigFileEdits(path)
	if err != nil {
		result.Err = err
		return result
	}
	defer unlock()
	edit, err := config.LoadForEditReadOnlyStrict(path)
	if err != nil {
		result.Err = err
		return result
	}
	if coveredBy := coveredPlanModeReadOnlyCommand(edit.Agent.PlanModeReadOnlyCommands, prefix); coveredBy != "" {
		result.CoveredBy = coveredBy
		return result
	}
	edit.Agent.PlanModeReadOnlyCommands = append(edit.Agent.PlanModeReadOnlyCommands, prefix)
	if err := edit.SaveTo(path); err != nil {
		slog.Warn("persist plan-mode read-only command trust", "prefix", prefix, "err", err)
		result.Err = err
		return result
	}
	result.Saved = true
	return result
}

func coveredPlanModeReadOnlyCommand(existing []string, candidate string) string {
	candidateFields := strings.Fields(strings.TrimSpace(candidate))
	if len(candidateFields) == 0 {
		return ""
	}
	for _, item := range existing {
		itemFields := strings.Fields(strings.TrimSpace(item))
		if len(itemFields) == 0 || len(itemFields) > len(candidateFields) {
			continue
		}
		matches := true
		for i, field := range itemFields {
			if candidateFields[i] != field {
				matches = false
				break
			}
		}
		if matches {
			return strings.Join(itemFields, " ")
		}
	}
	return ""
}

func coveredPermissionRule(rules []string, rule string) string {
	for _, existing := range rules {
		if permission.RuleCoversString(existing, rule) {
			return strings.TrimSpace(existing)
		}
	}
	return ""
}

func pruneCoveredPermissionRules(rules []string, rule string) []string {
	out := rules[:0]
	for _, existing := range rules {
		if strings.TrimSpace(existing) == "" || permission.RuleCoversString(rule, existing) {
			continue
		}
		out = append(out, existing)
	}
	return out
}
