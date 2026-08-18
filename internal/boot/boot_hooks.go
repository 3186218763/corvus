package boot

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/hook"
	"corvus/internal/permission"
	"corvus/internal/sandbox"

	"golang.org/x/term"
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

	// Hooks: load the global settings.json plus the project's. Project settings
	// are held behind a load-time trust decision: parsing a JSON file is already
	// too late to protect a shell hook's execution contract. Non-blocking hook
	// output is surfaced to the user as a Notice through the shared sink. The
	// runner fires PreToolUse/PostToolUse in the agent loop and
	// PermissionRequest/UserPromptSubmit/Stop at the controller boundary.
	headless := recoveryHeadlessMode(opts)
	trustApprover := opts.ProjectHookTrustApprover
	if trustApprover == nil && !headless {
		trustApprover = defaultProjectHookTrustApprover
	}
	resolvedHooks, trustReport := hook.LoadWithReport(hook.LoadOptions{
		ProjectRoot:  root,
		Headless:     headless,
		TrustProject: trustApprover,
	})
	if trustReport.TrustDenied {
		level := event.LevelWarn
		detail := fmt.Sprintf("project hooks from %s were not parsed or registered; trust this workspace explicitly before enabling repository-controlled shell code", trustReport.ProjectSettingsPath)
		if headless {
			detail += "; headless mode fails closed without an existing trust record"
		}
		sink.Emit(event.Event{Kind: event.Notice, Level: level, Text: "Project hooks are disabled until this workspace is trusted.", Detail: detail})
	}
	hookRuntime := hook.RuntimeOptions{}
	if shell.Kind == sandbox.ShellBash {
		hookRuntime.BashPath = shell.Path
	}
	// Defense-in-depth for project-scoped hooks: confine them to an OS sandbox
	// with the same write/read boundaries as the bash tool but **hardcoded
	// network isolation** (project hooks are repo-controlled lifecycle scripts
	// with no legitimate need for network access, unlike model-issued bash that
	// may download packages). Global and plugin hooks are not sandboxed here —
	// they are user-installed and already trusted at install. When the bash
	// sandbox is "enforce" and the backend is unavailable, the spawner fails
	// closed (refuses to run the project hook unconfined).
	hookRuntime.ProjectSandbox = hookProjectSandbox(cfg, root, shell)
	hookRunner := hook.NewRunnerWithHome(
		resolvedHooks, root, "", hook.NewDefaultSpawner(hookRuntime),
		func(msg string) { sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: msg}) },
	)

	return &hookResult{
		policy:       policy,
		headlessGate: headlessGate,
		hookRunner:   hookRunner,
	}, nil
}

// defaultProjectHookTrustApprover runs before the TUI takes ownership of the
// terminal, just like the project-MCP launch prompt. A remembered grant is the
// useful default for an affirmative answer; "once" remains available for a
// user who wants to inspect a repository without changing durable trust state.
func defaultProjectHookTrustApprover(req hook.ProjectTrustRequest) hook.ProjectTrustDecision {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return hook.ProjectTrustDeny
	}
	fmt.Fprintf(os.Stdout, "\nProject hooks found in %s. They can execute shell commands on lifecycle events and may modify this workspace.\nTrust this workspace? [y/N/once] ", req.SettingsPath)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return hook.ProjectTrustDeny
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "remember":
		return hook.ProjectTrustRemember
	case "once":
		return hook.ProjectTrustOnce
	default:
		return hook.ProjectTrustDeny
	}
}

// hookProjectSandbox builds the OS-sandbox spec for project-scoped hooks. It
// mirrors the bash tool's write/read confinement — same write roots, same
// forbid-read roots — but **enforces network isolation regardless of the bash
// tool's network setting**. Project hooks are repository-controlled lifecycle
// scripts that do not need network access for legitimate work (unlike bash
// commands that download modules/packages), so defense-in-depth demands they
// remain network-isolated even when the user has enabled network for builds.
// When the bash sandbox is "off", project hooks run with the legacy
// FilterEnv-only path (Mode == "" does not enforce).
func hookProjectSandbox(cfg *config.Config, root string, shell sandbox.Shell) sandbox.Spec {
	spec := sandbox.Spec{
		Mode:            cfg.BashMode(),
		WriteRoots:      cfg.WriteRootsForRoot(root),
		ForbidReadRoots: RuntimeForbidReadRoots(cfg, root),
		Network:         false, // hardcoded: project hooks never get network access
		Shell:           shell,
		// Hooks don't run builds or package managers, so the broad build-tool
		// cache allowances (go-build, npm, cargo…) are unnecessary. MinimalWrites
		// keeps the jail tight: only the workspace and explicit allow_write roots.
		MinimalWrites: true,
	}
	return spec
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
