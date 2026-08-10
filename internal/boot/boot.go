// Package boot assembles a ready-to-drive control.Controller from configuration:
// it loads config, resolves the model(s), builds the tool registry (built-ins +
// plugins), wires the permission gate, and constructs the executor — optionally
// wrapping it in a two-model Coordinator. It is the one place that turns "what the
// user configured" into "a Controller a frontend can drive", so every frontend —
// the terminal TUI, the HTTP/SSE server, the desktop webview — shares the exact
// same assembly instead of each re-deriving it. Frontends pass only a sink and a
// couple of run knobs; everything else comes from config.
package boot

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"corvus/internal/agent"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/plugin"
	"corvus/internal/provider"
	"corvus/internal/tool/builtin"
)

// ErrUnknownModel is returned by Build when the configured model can't be
// resolved to a provider — e.g. a default_model left over from a renamed or
// removed provider. Callers can detect it (errors.Is) to re-run setup.
var ErrUnknownModel = errors.New("unknown model")

func agentKeepPolicy(keep []string) agent.KeepPolicy {
	if keep == nil {
		return agent.KeepErrors
	}
	var p agent.KeepPolicy
	for _, k := range keep {
		switch strings.TrimSpace(k) {
		case "errors":
			p |= agent.KeepErrors
		case "user_marked":
			p |= agent.KeepUserMarked
		}
	}
	return p
}

// Options carries the per-run knobs a frontend chooses; everything else is read
// from configuration. Model "" falls back to the configured default_model;
// MaxSteps 0 uses automatic execution. RequireKey forces the executor's API key to
// be present (run/serve pass true so a missing key fails fast; chat/desktop pass
// false so the UI is reachable before a key is set). Sink receives the agent's
// typed event stream.
type Options struct {
	Model       string
	MaxSteps    int
	MaxStepsKey string
	RequireKey  bool
	Sink        event.Sink
	// EffortOverride is a session-local reasoning effort override. Nil means use
	// the resolved provider config; a non-nil empty string means provider default.
	EffortOverride *string
	// PermissionAllow adds process-local allow rules (for example CLI
	// --allowed-tools). They override configured ask rules but never deny rules
	// and are not persisted.
	PermissionAllow []string
	// AdditionalDirs grants this session's file writers and sandboxed shell
	// access to extra directories without changing persisted sandbox config.
	AdditionalDirs []string
	// Stderr is the writer for diagnostic warnings and plugin subprocess
	// stderr output. When nil, defaults to os.Stderr. Interactive terminal
	// frontends must provide a private diagnostic writer (or io.Discard) so
	// background output cannot corrupt the TUI's terminal raw mode.
	Stderr io.Writer
	// WorkspaceRoot is the project root directory for config, skills, memory,
	// commands, hooks, and tool confinement. When empty, the current working
	// directory is used (CLI default). Desktop tabs pass their project root here
	// so each tab loads its own config/skills/hooks without changing the process
	// cwd — enabling concurrent multi-project sessions.
	WorkspaceRoot string
	// AutoPricingCurrency supplies a frontend-resolved pricing region when the
	// persisted desktop currency and language settings are all automatic. It is
	// applied to the in-memory config only and never turns Auto into a persisted
	// CNY/USD choice.
	AutoPricingCurrency string
	// StatsSource labels this frontend's usage records (desktop/cli/serve).
	// Empty disables usage recording for this controller.
	StatsSource string
	// ExtraPlugins are session-scoped MCP servers supplied by a host transport
	// (for example ACP session/new). They are connected eagerly for this
	// controller but are not persisted to .corvus/config.toml.
	ExtraPlugins []plugin.Spec
	// MCPLaunchApprover decides whether a repository-declared (project-scoped)
	// MCP server may be connected during Build. It is invoked once per server
	// per boot when no durable exact-identity launch grant exists yet; approval
	// is persisted by plugin.AuthorizeProjectSpecLaunch so unchanged servers
	// start silently on later sessions. A denial (or error) records a failure
	// marked RequiresLaunchApproval and the server is never started. When nil,
	// Build uses a fail-closed default: prompts on an interactive stdin and
	// denies in non-interactive (headless/CI) environments. User-config and
	// plugin-package MCP servers never reach this callback.
	MCPLaunchApprover func(ctx context.Context, spec plugin.Spec) (bool, error)
	// TokenMode selects the session's runtime profile. Empty/full/balanced preserves
	// the normal capability surface. "economy" keeps the core coding tools visible
	// and moves optional sources behind connect_tool_source. "delivery" keeps the
	// full surface and adds a stable completion-and-verification contract.
	TokenMode string
	// SessionDir overrides where persisted chat transcripts are written. When
	// empty, the shared CLI/global session directory is used.
	SessionDir string
	// SharedHost is an optional plugin.Host shared across controllers for the
	// same workspace root. When set, boot.Build reuses its running clients
	// instead of creating new subprocesses, and the caller manages the host's
	// lifecycle. When nil, Build creates and owns a new host as before.
	SharedHost *plugin.Host
	// CleanupPendingReconciler retries delayed physical cleanup for session
	// artifacts left by a previous process. Nil uses the core physical-delete
	// reconciler; frontends with different deletion semantics can override it.
	CleanupPendingReconciler func(sessionDir string) error
	// ApprovalTimeout bounds how long a tool-approval or ask prompt blocks for a
	// user decision. Zero (default) waits forever — correct for an interactive
	// terminal. Headless/bot frontends pass a positive value so an unanswered
	// prompt can't wedge the session indefinitely (#4626, #4402).
	ApprovalTimeout time.Duration
	// HeadlessApprovalMode selects the non-interactive tool-approval contract
	// (control.ToolApprovalAuto/DontAsk/Yolo) applied to every headless-only gate
	// this boot constructs: the top-level executor, task/read_only_task,
	// writer-capable skill sub-agents, and the planner runner. Empty (or "ask")
	// keeps the default fail-closed headless gate. Callers that later call
	// Controller.ApplyHeadlessApprovalMode with a
	// different mode than they passed here should also pass it here, or
	// sub-agent gates will not match the parent executor's mode.
	HeadlessApprovalMode string
	// SessionRecoveryMeta and OnSessionRecovered let richer frontends attach
	// local UI metadata to automatic transcript recovery branches.
	SessionRecoveryMeta func(control.SessionRecoveryRequest) agent.BranchMeta
	OnSessionRecovered  func(control.SessionRecoveryInfo) error
	// FileOverlay and TerminalRunner let a host transport (ACP) serve file
	// content from editor buffers and run foreground bash in a host terminal.
	// Both only change where tool I/O happens — tool names, descriptions, and
	// schemas stay byte-identical, so the provider-visible surface is unchanged.
	FileOverlay    builtin.FileOverlay
	TerminalRunner builtin.TerminalRunner
	// ProviderResolver routes every model role through a caller-owned provider
	// catalog. Remote Workbench injects a Broker resolver so no credential or
	// provider endpoint has to exist on the Host. Nil preserves local behavior.
	ProviderResolver provider.Resolver
	// DisablePlanner is a process-local hard override used by supervised ACP
	// workers. It wins over user/project planner_model configuration without
	// mutating config or changing the provider-visible prompt/tool surface.
	DisablePlanner bool
	// SandboxNetworkOverride and WorkspaceOnly are process-local hard bounds for
	// supervised ACP workers. Nil/false preserve normal Corvus config.
	SandboxNetworkOverride *bool
	SandboxBashOverride    string
	WorkspaceOnly          bool
}

func Build(ctx context.Context, opts Options) (*control.Controller, error) {
	// Config + model: runs legacy migrations first so LoadForRoot picks up
	// freshly written config and ~/.env this same boot.
	cfgResult, err := buildConfigAndModel(opts)
	if err != nil {
		return nil, err
	}
	// Sink + one-time boot notices.
	sink, err := buildSinkAndNotices(opts, cfgResult.cfg, cfgResult.entry, cfgResult.modelName, cfgResult.migrated, cfgResult.migErr, cfgResult.stepLimitsMigrated, cfgResult.stepLimitMigErr, cfgResult.redactToolOutputMigrated, cfgResult.redactToolOutputMigErr, cfgResult.memoryCompilerMigrated, cfgResult.memoryCompilerMigErr)
	if err != nil {
		return nil, err
	}
	// Jobs, lease, network client, executor provider, shell.
	jobsResult, err := buildJobsAndProviders(opts, sink, cfgResult.cfg, cfgResult.root, cfgResult.stderr, cfgResult.modelRef, cfgResult.tokenDelivery)
	if err != nil {
		return nil, err
	}
	// Cache-stable system-prompt prefix (base + style + memory + skills index).
	promptResult, err := buildPromptAndMemory(ctx, cfgResult.cfg, opts, cfgResult.root, jobsResult.shell, sink, cfgResult.tokenEconomy, cfgResult.tokenDelivery, cfgResult.runtimeProfile)
	if err != nil {
		return nil, err
	}
	// Shared tool registry with startup built-ins, plus the plugin host.
	toolResult, err := buildToolRegistry(cfgResult.cfg, opts, cfgResult.root, cfgResult.stderr, cfgResult.additionalDirs, jobsResult.shell, jobsResult.proxySpec, cfgResult.tokenEconomy)
	if err != nil {
		return nil, err
	}
	// MCP specs, launch isolation, catalog placeholders, demotion notices.
	pluginResult, err := buildMCPPlugins(ctx, opts, sink, cfgResult.cfg, cfgResult.root, toolResult.reg, toolResult.pluginHost, toolResult.writeRoots, toolResult.networkEnabled, toolResult.forbidReadRoots, cfgResult.tokenEconomy)
	if err != nil {
		return nil, err
	}
	// LSP manager + cleanup chain, subagent transcript store, max steps.
	lspResult, err := buildLSPAndSessionStore(cfgResult.cfg, opts, cfgResult.root, toolResult.pluginHost, toolResult.reg, cfgResult.tokenEconomy, jobsResult.sessionDir, jobsResult.jm)
	if err != nil {
		return nil, err
	}
	// Permission policy, headless gate, hook runner.
	hookResult, err := buildPermissionAndHooks(cfgResult.cfg, opts, cfgResult.root, jobsResult.shell, sink)
	if err != nil {
		return nil, err
	}
	// Sub-agent machinery: task tools, scheduler, capability-runtime cell.
	subagentResult, err := buildSubagentTools(cfgResult.cfg, opts, cfgResult.entry, cfgResult.modelName, jobsResult.proxySpec, jobsResult.execProv, toolResult.reg, lspResult.maxSteps, lspResult.subagentStore, hookResult.headlessGate, cfgResult.keepPolicy, cfgResult.root, toolResult.bashSpec, jobsResult.workspaceLease, promptResult.skillStore, cfgResult.tokenEconomy, cfgResult.tokenDelivery)
	if err != nil {
		return nil, err
	}
	// Session/memory tools + ask tool.
	sessionMemoryResult, err := buildSessionAndMemoryTools(jobsResult.sessionDir, promptResult.mem, toolResult.reg, cfgResult.tokenEconomy)
	if err != nil {
		return nil, err
	}
	// Skill sub-agent runners, slash commands, install_source, skills sources.
	skillToolsResult, err := buildSkillTools(ctx, cfgResult.cfg, opts, cfgResult.entry, cfgResult.root, toolResult.reg, jobsResult.balanceClient, pluginResult.pluginSpecOptions, toolResult.pluginHost, jobsResult.execProv, lspResult.maxSteps, lspResult.subagentStore, hookResult.headlessGate, cfgResult.keepPolicy, subagentResult.maxSubagentDepth, subagentResult.subagentScheduler, cfgResult.tokenDelivery, jobsResult.workspaceLease, subagentResult.capRuntimeGet, promptResult.skillStore, promptResult.skills, subagentResult.resolveSubagentProvider, subagentResult.subagentIdentity, cfgResult.tokenEconomy)
	if err != nil {
		return nil, err
	}
	// Economy-mode on-demand tool sources.
	buildToolSourceConnector(ctx, opts, cfgResult.cfg, cfgResult.root, toolResult.reg, toolResult.writeRoots, toolResult.forbidReadRoots, toolResult.bashSpec, toolResult.bashTimeout, toolResult.searchSpec, jobsResult.proxySpec, toolResult.readPathResolver, toolResult.sessionGuard, toolResult.managedConfig, skillToolsResult.addSkillTools, subagentResult.addTaskTool, subagentResult.addReadOnlyTaskTool, skillToolsResult.addReadOnlySkillTools, skillToolsResult.addInstallSourceTool, skillToolsResult.addSlashCommandTool, lspResult.addLSPTools, lspResult.lspMgr, sessionMemoryResult.addSessionTools, sessionMemoryResult.addMemoryTools, pluginResult.onDemandMCPSpecs, pluginResult.onDemandMCPNames, toolResult.pluginHost, cfgResult.tokenEconomy)
	// Session-shared MCP capability runtime + Delivery/dual-model frontends.
	capabilityResult, err := buildCapabilityRuntime(ctx, cfgResult.cfg, opts, cfgResult.root, toolResult.reg, promptResult.skillStore, toolResult.pluginHost, pluginResult.pluginSpecOptions, pluginResult.enabledMCPNames, cfgResult.runtimeProfile, cfgResult.tokenEconomy, cfgResult.tokenDelivery, cfgResult.entry, subagentResult.capRuntimeGet, subagentResult.capRuntimeSet)
	if err != nil {
		return nil, err
	}
	// Executor (+ optional planner coordinator).
	runnerResult, err := buildExecutorAndPlanner(ctx, opts, cfgResult.cfg, cfgResult.entry, cfgResult.modelRef, jobsResult.execProv, toolResult.reg, promptResult.sysPrompt, sink, hookResult.headlessGate, hookResult.hookRunner, jobsResult.jm, subagentResult.subagentScheduler, cfgResult.root, promptResult.projectChecks, cfgResult.tokenDelivery, jobsResult.workspaceLease, capabilityResult.capLedger, capabilityResult.capAudit, cfgResult.keepPolicy, subagentResult.maxSubagentDepth, lspResult.maxSteps, promptResult.mem, capabilityResult.capRuntime, cfgResult.tokenEconomy, jobsResult.proxySpec)
	if err != nil {
		return nil, err
	}
	// Controller assembly (guardian, recovery reviewer, capability routing).
	return buildController(ctx, opts, cfgResult.cfg, cfgResult.root, sink, hookResult.policy, hookResult.headlessGate, runnerResult.label, cfgResult.modelRef, promptResult.sysPrompt, jobsResult.sessionDir, toolResult.pluginHost, skillToolsResult.cmds, promptResult.skills, promptResult.allSkills, promptResult.skillStore, promptResult.allSkillStore, skillToolsResult.skillRunner, skillToolsResult.readOnlySkillRunner, skillToolsResult.skillProfile, hookResult.hookRunner, promptResult.mem, lspResult.cleanup, cfgResult.entry, jobsResult.balanceClient, jobsResult.jm, jobsResult.workspaceLease, toolResult.reg, pluginResult.pluginSpecOptions, capabilityResult.capRuntime, toolResult.readPathResolver, jobsResult.shell, cfgResult.runtimeProfile, subagentResult.taskTool, capabilityResult.capSpecs, capabilityResult.capAudit, subagentResult.resolveSubagentProvider, subagentResult.subagentIdentity, jobsResult.execProv, jobsResult.proxySpec, cfgResult.tokenDelivery, cfgResult.tokenEconomy, capabilityResult.dualModelPlanner, runnerResult.runner, runnerResult.executor)
}
