package boot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"corvus/internal/agent"
	"corvus/internal/command"
	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/history"
	"corvus/internal/installsource"
	"corvus/internal/jobs"
	"corvus/internal/lsp"
	"corvus/internal/memory"
	"corvus/internal/netclient"
	"corvus/internal/plugin"
	"corvus/internal/provider"
	"corvus/internal/sandbox"
	"corvus/internal/skill"
	"corvus/internal/tool"
	"corvus/internal/tool/builtin"
	"corvus/internal/tool/sessiontool"
	"corvus/internal/workspacelease"
)

type toolResult struct {
	reg              *tool.Registry
	writeRoots       []string
	networkEnabled   bool
	forbidReadRoots  []string
	managedConfig    builtin.ManagedConfigPaths
	bashSpec         sandbox.Spec
	sessionGuard     builtin.SessionDataGuard
	searchSpec       builtin.SearchSpec
	bashTimeout      time.Duration
	readPathResolver *builtin.PathResolver
	pluginHost       *plugin.Host
}

// buildToolRegistry constructs the shared tool registry and registers the
// startup built-in tools, then creates the plugin host (shared or private).
func buildToolRegistry(cfg *config.Config, opts Options, root string, stderr io.Writer, additionalDirs []string, shell sandbox.Shell, proxySpec netclient.ProxySpec, tokenEconomy bool) (*toolResult, error) {
	reg := tool.NewRegistry()
	writeRoots := cfg.WriteRootsForRoot(root)
	writeRoots = appendUniquePaths(writeRoots, additionalDirs...)
	if opts.WorkspaceOnly {
		writeRoots = []string{root}
	}
	networkEnabled := cfg.Sandbox.Network
	if opts.SandboxNetworkOverride != nil {
		networkEnabled = *opts.SandboxNetworkOverride
	}
	bashMode := cfg.BashMode()
	if override := strings.TrimSpace(opts.SandboxBashOverride); override != "" {
		bashMode = override
	}
	forbidReadRoots := RuntimeForbidReadRoots(cfg, root)
	// managedConfig names the Corvus-owned config FILES (config.toml,
	// compatibility TOMLs, legacy v0.x config.json) the file-writers may repair
	// outside the workspace after a fresh per-write human approval. The bash
	// OS-sandbox write roots deliberately stay unwidened: config repair goes
	// through the approval-gated file tools, not raw shell writes.
	managedConfig := builtin.NewManagedConfigPaths(config.CorvusManagedConfigPaths())
	bashSpec := sandbox.Spec{Mode: bashMode, WriteRoots: writeRoots, ForbidReadRoots: forbidReadRoots, Network: networkEnabled}
	bashSpec.Shell = shell
	// The session-data guard blocks agent writes into Corvus's own session
	// stores (they race the app's saves and surface as conflict-copy loops);
	// explicit allow_write entries stay a sanctioned escape hatch.
	allowWriteRoots := cfg.AllowWriteRoots()
	if opts.WorkspaceOnly {
		allowWriteRoots = nil
	}
	sessionGuard := builtin.NewSessionDataGuard(config.MemoryUserDir(), allowWriteRoots)
	if bashSpec.Mode == "enforce" && !sandbox.Available() {
		fmt.Fprintln(stderr, "warning: "+sandbox.UnavailableMessage())
	}
	if autoShellPrefer(cfg.Tools.Shell.Prefer) && shell.Kind == sandbox.ShellPowerShell {
		fmt.Fprintln(stderr, "warning: bash not found on PATH; the shell tool will run commands under Windows PowerShell. Install Git for Windows or WSL to use bash, or set [tools.shell] prefer=\"powershell\" to silence this.")
	}
	searchSpec := builtin.ResolveSearch(cfg.Tools.Search.Engine, cfg.Tools.Search.RgPath, stderr)
	bashTimeout := time.Duration(cfg.BashTimeoutSeconds()) * time.Second
	enabledBuiltins := cfg.Tools.Enabled
	if tokenEconomy {
		enabledBuiltins = tokenEconomyBuiltins(enabledBuiltins)
	}
	readPathResolver := builtin.NewPathResolver()
	// An explicit Economy allowlist can contain only on-demand tools, leaving no
	// startup built-ins. Do not pass that filtered empty slice to addBuiltins,
	// where an empty list intentionally means "all built-ins".
	if !tokenEconomy || len(cfg.Tools.Enabled) == 0 || len(enabledBuiltins) > 0 {
		addBuiltins(reg, enabledBuiltins, writeRoots, bashSpec, bashTimeout, searchSpec, stderr, root, proxySpec, forbidReadRoots, readPathResolver, sessionGuard, managedConfig, opts.FileOverlay, opts.TerminalRunner)
	}
	// Use the caller-supplied shared host when set, so controllers for the same
	// workspace root reuse running MCP processes (e.g. one CodeGraph daemon
	// instead of one per tab). Otherwise construct a private host per controller.
	pluginHost := opts.SharedHost
	if pluginHost == nil {
		pluginHost = plugin.NewHost()
	}

	return &toolResult{
		reg:              reg,
		writeRoots:       writeRoots,
		networkEnabled:   networkEnabled,
		forbidReadRoots:  forbidReadRoots,
		managedConfig:    managedConfig,
		bashSpec:         bashSpec,
		sessionGuard:     sessionGuard,
		searchSpec:       searchSpec,
		bashTimeout:      bashTimeout,
		readPathResolver: readPathResolver,
		pluginHost:       pluginHost,
	}, nil
}

type lspResult struct {
	cleanup       func()
	lspMgr        *lsp.Manager
	addLSPTools   func() []string
	maxSteps      int
	subagentStore *agent.SubagentStore
}

// buildLSPAndSessionStore wires the LSP manager and its cleanup chain, then
// resolves the subagent transcript store and binds the job-manager destroy
// checker.
func buildLSPAndSessionStore(cfg *config.Config, opts Options, root string, pluginHost *plugin.Host, reg *tool.Registry, tokenEconomy bool, sessionDir string, jm *jobs.Manager) (*lspResult, error) {
	cleanup := pluginHost.Close
	if opts.SharedHost != nil {
		// The caller owns the shared host's lifecycle; the controller must not
		// close it. A no-op cleanup keeps Controller.Close happy without
		// shutting down MCP processes that other controllers still use.
		cleanup = func() {}
	}

	// LSP tools resolve their servers on PATH and spawn lazily on first query, so
	// registering them is cheap even when no server is installed (a query then
	// returns an install hint). The manager is session-scoped; chain its shutdown
	// into the controller's cleanup so servers stop with the session, not the turn.
	var lspMgr *lsp.Manager
	lspToolsAdded := false
	addLSPTools := func() []string {
		if lspMgr == nil || lspToolsAdded {
			return nil
		}
		lspToolsAdded = true
		return addTools(reg, lsp.Tools(lspMgr))
	}
	if cfg.LSP.Enabled {
		lspMgr = lsp.NewManager(root, LSPSpecs(cfg.LSP))
		if !tokenEconomy {
			addLSPTools()
		}
		prev := cleanup
		cleanup = func() { prev(); lspMgr.Close() }
	}

	maxSteps := 0
	if opts.MaxSteps > 0 {
		maxSteps = opts.MaxSteps
	}
	subagentStore, err := newSubagentStore(sessionDir)
	if err != nil {
		return nil, err
	}
	if subagentStore != nil {
		subagentStore.WithDestroyedChecker(jm.IsDestroying)
	}

	return &lspResult{
		cleanup:       cleanup,
		lspMgr:        lspMgr,
		addLSPTools:   addLSPTools,
		maxSteps:      maxSteps,
		subagentStore: subagentStore,
	}, nil
}

type sessionMemoryResult struct {
	addSessionTools func() string
	addMemoryTools  func() string
}

// buildSessionAndMemoryTools registers the history/session/memory tools and
// the always-present ask tool.
func buildSessionAndMemoryTools(sessionDir string, mem *memory.Set, reg *tool.Registry, tokenEconomy bool) (*sessionMemoryResult, error) {
	// Session and memory tools are always present in Balanced/Delivery. Economy
	// installs them only after connect_tool_source requests that capability, so
	// simple coding turns do not pay for unrelated schemas.
	sessionToolsAdded := false
	addSessionTools := func() string {
		if sessionToolsAdded {
			return "sessions are already enabled."
		}
		sessionToolsAdded = true
		reg.Add(history.NewTool(history.Options{SessionDir: sessionDir, GlobalSessionDir: config.SessionDir(), ArchiveDir: config.ArchiveDir()}))
		reg.Add(sessiontool.NewListSessionsTool(sessionDir))
		reg.Add(sessiontool.NewReadSessionTool(sessionDir))
		return "enabled history, list_sessions, read_session."
	}
	memoryToolsAdded := false
	addMemoryTools := func() string {
		if memoryToolsAdded {
			return "memory tools are already enabled."
		}
		memoryToolsAdded = true
		reg.Add(memory.NewRecallTool(mem.Store))
		reg.Add(memory.NewRememberTool(mem.Store))
		reg.Add(memory.NewForgetTool(mem.Store))
		return "enabled memory, remember, forget."
	}
	if !tokenEconomy {
		addSessionTools()
		addMemoryTools()
	}

	// The `ask` tool puts structured multiple-choice questions to the user. It
	// reaches them through the Asker on the call context, which interactive
	// frontends wire to the controller (EnableInteractiveApproval); a headless run
	// has none, so ask resolves to "decide for yourself".
	reg.Add(agent.NewAskTool())

	return &sessionMemoryResult{
		addSessionTools: addSessionTools,
		addMemoryTools:  addMemoryTools,
	}, nil
}

type skillToolsResult struct {
	cmds                  []command.Command
	skillRunner           func(context.Context, skill.Skill, string, skill.SubagentRunOptions) (string, error)
	readOnlySkillRunner   func(context.Context, skill.Skill, string, skill.SubagentRunOptions) (string, error)
	skillProfile          func(skill.Skill) *event.Profile
	addSkillTools         func() string
	addReadOnlySkillTools func() string
	addInstallSourceTool  func() string
	addSlashCommandTool   func(includeSkills bool) string
}

// buildSkillTools wires the skill sub-agent runners, slash commands,
// install_source, and the read-only/full skills sources, then enables them in
// non-economy mode.
func buildSkillTools(ctx context.Context, cfg *config.Config, opts Options, entry *config.ProviderEntry, root string, reg *tool.Registry, balanceClient *http.Client, pluginSpecOptions PluginSpecOptions, pluginHost *plugin.Host, execProv provider.Provider, maxSteps int, subagentStore *agent.SubagentStore, headlessGate *control.SharedHeadlessGate, keepPolicy agent.KeepPolicy, maxSubagentDepth int, subagentScheduler *agent.SubagentScheduler, tokenDelivery bool, workspaceLease *workspacelease.Owner, capRuntimeGet func() *agent.MCPCapabilityRuntime, skillStore *skill.Store, skills []skill.Skill, resolveSubagentProvider func(modelRef, effort string) (provider.Provider, *provider.Pricing, int, error), subagentIdentity func(modelRef, effort string) (string, string), tokenEconomy bool) (*skillToolsResult, error) {
	// Skill tools: read_only_skill is a narrow explicitly read-only entry point; the
	// full skills source adds run_skill / install_skill plus the dedicated
	// subagent wrappers (explore / research / review / security_review). Read-only
	// subagent skills run ephemerally with the same registry boundary as
	// read_only_task, so they cannot write, install, mutate memory, resume/fork
	// transcripts, or delegate further.
	//
	// subagentSkillOptions is the single construction point for skill sub-agent
	// run options, so the read-only and writer-capable runners cannot drift on
	// compaction or language settings — add new fields here, not per runner.
	// sessionCacheID is the parent BranchID (ParentSession); subRef is the
	// transcript Ref (empty for ephemeral / no-parent runs).
	subagentSkillOptions := func(sctx context.Context, steps int, price *provider.Pricing, ctxWin, childDepth int, sessionCacheID, subRef string) agent.Options {
		opts := agent.Options{
			MaxSteps:            steps,
			Temperature:         cfg.Agent.Temperature,
			Pricing:             price,
			UsageSource:         event.UsageSourceSubagent,
			Gate:                headlessGate,
			ContextWindow:       ctxWin,
			RecentKeep:          cfg.Agent.RecentKeep,
			SoftCompactRatio:    cfg.Agent.SoftCompactRatio,
			ToolResultSnipRatio: cfg.Agent.ToolResultSnipRatio,
			CompactRatio:        cfg.Agent.CompactRatio,
			CompactForceRatio:   cfg.Agent.CompactForceRatio,
			ArchiveDir:          config.ArchiveDir(),
			KeepPolicy:          keepPolicy,
			ResponseLanguage:    agent.ResponseLanguageFromContext(sctx),
			ReasoningLanguage:   agent.ReasoningLanguageFromContext(sctx),
			SubagentDepth:       childDepth,
			MaxSubagentDepth:    maxSubagentDepth,
			DeliveryProfile:     tokenDelivery,
			WorkspaceLease:      workspaceLease,
		}
		// Parent entry kind/URL: skill sub-agents that override the model still
		// inherit the parent's sticky-key host policy for Phase 1.
		promptCacheOptions(cfg, entry, sessionCacheID, subRef).apply(&opts)
		return opts
	}
	readOnlySkillRunner := func(sctx context.Context, sk skill.Skill, task string, runOpts skill.SubagentRunOptions) (string, error) {
		if strings.TrimSpace(runOpts.ContinueFrom) != "" || strings.TrimSpace(runOpts.ForkFrom) != "" {
			return "", fmt.Errorf("read_only_skill does not support continue_from/fork_from")
		}
		releaseSlot, err := subagentScheduler.Acquire(sctx, agent.AcquireRequest{
			Writer: false,
			Nested: agent.SubagentDepth(sctx) > 0,
			Label:  sk.Name,
		})
		if err != nil {
			return "", err
		}
		defer releaseSlot()
		sk = skill.WithCodeGraphTools(sk, skill.CodeGraphReadTools(reg))
		prov, price, ctxWin := execProv, entry.Price, entry.ContextWindow
		modelRef := subagentModelRef(cfg, sk)
		effortRef := subagentEffortRef(cfg, sk)
		if modelRef != "" || effortRef != "" {
			p, pr, cw, err := resolveSubagentProvider(modelRef, effortRef)
			if err != nil {
				return "", fmt.Errorf("read-only subagent skill %q profile: %w", sk.Name, err)
			}
			prov, price, ctxWin = p, pr, cw
		}
		childDepth := agent.SubagentDepth(sctx) + 1
		if childDepth > maxSubagentDepth {
			return "", fmt.Errorf("subagent delegation depth limit reached (max_subagent_depth=%d)", maxSubagentDepth)
		}
		subReg := agent.ReadOnlySubagentToolRegistryForDepthWithRuntime(reg, sk.AllowedTools, childDepth, maxSubagentDepth, capRuntimeGet())
		if subReg.Len() == 0 {
			return "", fmt.Errorf("read_only_skill: skill %q has no read-only tools available", sk.Name)
		}
		switch sk.Name {
		case "review", "security-review", "security_review":
			agent.AttachReviewReportTool(subReg)
		}
		steps := maxSteps
		if steps > 0 {
			if steps /= 2; steps < 5 {
				steps = 5
			}
		}
		// Custom and named built-in profiles fully control their system prompt
		// (no implicit concise/DefaultReadOnlyTaskSystemPrompt overlay).
		sysPrompt := strings.TrimSpace(sk.Body)
		if sysPrompt == "" {
			sysPrompt = agent.DefaultReadOnlyTaskSystemPrompt
		}
		// Read-only skill runner is always ephemeral (empty Ref). Parent BranchID
		// still sticks the key when a session is active; empty parent omits it.
		runOptions := subagentSkillOptions(sctx, steps, price, ctxWin, childDepth, agent.ParentSession(sctx), "")
		usageModelRef, _ := subagentIdentity(modelRef, effortRef)
		runOptions.ModelRef = usageModelRef
		// Delivery risk gates consume typed reports; outside Delivery a casual
		// /review run may finish with prose only.
		if runOptions.DeliveryProfile {
			runOptions.RequireReviewReportKind = agent.ReviewReportKindForSkill(sk.Name)
		}
		return agent.RunReadOnlySubAgentWithSession(sctx, prov, subReg, agent.NewSession(sysPrompt), task,
			runOptions, agent.NestedSink(sctx, event.Discard))
	}
	// Writer-capable subagent skills reuse the sub-agent machinery via this
	// runner: an isolated loop with the skill body as system prompt, a tool set
	// scoped to the skill's allowed-tools (minus recursive meta-tools), optional
	// per-skill model, and resumable transcripts when the parent session supports
	// them. Its tool activity nests under the invoking call, like `task`.
	skillRunner := func(sctx context.Context, sk skill.Skill, task string, runOpts skill.SubagentRunOptions) (string, error) {
		// Writer skills without write_paths claim the whole workspace so they
		// cannot race fleet/task writers that declared disjoint paths.
		acq := agent.AcquireRequest{
			Writer: !sk.ReadOnly,
			Nested: agent.SubagentDepth(sctx) > 0,
			Label:  sk.Name,
		}
		if !sk.ReadOnly {
			whole, werr := agent.WholeWorkspaceWriteClaim(root)
			if werr != nil {
				return "", fmt.Errorf("subagent skill %q write claim: %w", sk.Name, werr)
			}
			acq.WritePaths = whole
		}
		releaseSlot, err := subagentScheduler.Acquire(sctx, acq)
		if err != nil {
			return "", err
		}
		defer releaseSlot()
		sk = skill.WithCodeGraphTools(sk, skill.CodeGraphReadTools(reg))
		prov, price, ctxWin := execProv, entry.Price, entry.ContextWindow
		modelRef := subagentModelRef(cfg, sk)
		effortRef := subagentEffortRef(cfg, sk)
		if modelRef != "" || effortRef != "" {
			p, pr, cw, err := resolveSubagentProvider(modelRef, effortRef)
			if err != nil {
				return "", fmt.Errorf("subagent skill %q profile: %w", sk.Name, err)
			}
			prov, price, ctxWin = p, pr, cw
		}
		childDepth := agent.SubagentDepth(sctx) + 1
		if childDepth > maxSubagentDepth {
			return "", fmt.Errorf("subagent delegation depth limit reached (max_subagent_depth=%d)", maxSubagentDepth)
		}
		// A read-only skill (builtin review/security-review, or frontmatter
		// `read-only: true`) gets its promise enforced at the tool boundary:
		// writer tools are stripped and bash runs under the read-only
		// command policy. Transcripts recorded against the writer-capable
		// registry stop matching on continue_from (schema-hash check reports
		// the mismatch).
		var subReg *tool.Registry
		if sk.ReadOnly {
			subReg = agent.ReadOnlySubagentToolRegistryForDepthWithRuntime(reg, sk.AllowedTools, childDepth, maxSubagentDepth, capRuntimeGet())
		} else {
			subReg = agent.SubagentToolRegistryForDepthWithRuntime(reg, sk.AllowedTools, childDepth, maxSubagentDepth, capRuntimeGet())
		}
		// Delivery risk gates require structured review_report from review
		// subagents only — never expose it on the parent tool surface.
		switch sk.Name {
		case "review", "security-review", "security_review":
			agent.AttachReviewReportTool(subReg)
		}
		continueFrom := strings.TrimSpace(runOpts.ContinueFrom)
		legacyForkFrom := strings.TrimSpace(runOpts.ForkFrom)
		if continueFrom != "" && legacyForkFrom != "" {
			return "", fmt.Errorf("continue_from and fork_from are mutually exclusive; pass only continue_from")
		}
		parentID, _, _, _ := agent.CallContext(sctx)
		if runOpts.HostInitiated {
			parentID = ""
		}
		parentSession := agent.ParentSession(sctx)
		var run *agent.SubagentRun
		if subagentStore == nil || parentSession == "" {
			// Headless runs (e.g. `corvus run`) have no persistent session to
			// own a transcript. Run the skill sub-agent ephemerally, as before
			// persisted transcripts existed, instead of failing. Continuation needs
			// a persisted owner, so it errors here.
			if continueFrom != "" || legacyForkFrom != "" {
				return "", fmt.Errorf("subagent continuation requires a persisted session; none is active in this run")
			}
			run = agent.EphemeralSubagentRun(sk.Body)
		} else {
			identityModel, identityEffort := subagentIdentity(modelRef, effortRef)
			spec := agent.SubagentSpec{
				Kind:             "skill",
				Name:             sk.Name,
				WorkspaceRoot:    root,
				ParentSession:    parentSession,
				ParentToolCallID: parentID,
				SystemPrompt:     sk.Body,
				Registry:         subReg,
				Model:            identityModel,
				Effort:           identityEffort,
			}
			var prepErr error
			if continueFrom != "" {
				run, prepErr = subagentStore.PrepareContinue(continueFrom, spec)
			} else if legacyForkFrom != "" {
				run, prepErr = subagentStore.PrepareLegacyForkFrom(legacyForkFrom, spec)
			} else {
				run, prepErr = subagentStore.PrepareFresh(spec)
			}
			if prepErr != nil {
				return "", prepErr
			}
		}
		defer run.Release()
		steps := maxSteps
		if steps > 0 {
			if steps /= 2; steps < 5 {
				steps = 5
			}
		}
		// SessionCacheID = parent BranchID; SubagentCacheID = transcript Ref
		// (empty for ephemeral headless runs without a parent session).
		runOptions := subagentSkillOptions(sctx, steps, price, ctxWin, childDepth, parentSession, run.Ref)
		usageModelRef, _ := subagentIdentity(modelRef, effortRef)
		runOptions.ModelRef = usageModelRef
		// Delivery risk gates consume typed reports; outside Delivery a casual
		// /review run may finish with prose only.
		if runOptions.DeliveryProfile {
			runOptions.RequireReviewReportKind = agent.ReviewReportKindForSkill(sk.Name)
		}
		var answer string
		if sk.ReadOnly {
			answer, err = agent.RunReadOnlySubAgentWithSession(sctx, prov, subReg, run.Session, task,
				runOptions, agent.NestedSink(sctx, event.Discard))
		} else {
			answer, err = agent.RunSubAgentWithSession(sctx, prov, subReg, run.Session, task,
				runOptions, agent.NestedSink(sctx, event.Discard))
		}
		if err != nil {
			return "", errors.Join(err, subagentStore.SaveFailed(run))
		}
		if err := subagentStore.SaveCompleted(run); err != nil {
			return "", errors.Join(err, subagentStore.SaveFailed(run))
		}
		return agent.FormatSubagentRunResult(answer, run, false), nil
	}
	skillProfile := func(sk skill.Skill) *event.Profile {
		model, effort := subagentModelRef(cfg, sk), subagentEffortRef(cfg, sk)
		if model == "" && effort == "" {
			return nil
		}
		return &event.Profile{Model: model, Effort: effort}
	}
	// Custom slash commands (.corvus/commands + user dir). Best-effort: a malformed
	// file is skipped, and a load error never blocks the session.
	cmds, _ := command.LoadRoots(config.CommandRootsForRoot(root)...)
	slashCommandAdded := false
	slashCommandIncludesSkills := false
	addSlashCommandTool := func(includeSkills bool) string {
		if slashCommandAdded && (!includeSkills || slashCommandIncludesSkills) {
			return "slash commands are already enabled."
		}
		// Expose loaded slash commands to the model via slash_command. In economy
		// mode skills join this list only after the skills source is enabled.
		var slashEntries []command.SlashEntry
		if includeSkills {
			for _, sk := range skillStore.SlashList() {
				sk := sk
				slashEntries = append(slashEntries, command.SlashEntry{
					Name:        sk.SlashName(),
					Description: sk.Description,
					Render:      func(args []string) string { return skillStore.Render(sk, strings.Join(args, " ")) },
				})
			}
		}
		for _, cmd := range cmds {
			if cmd.Hidden {
				continue
			}
			cmd := cmd
			slashEntries = append(slashEntries, command.SlashEntry{
				Name:        cmd.Name,
				Description: cmd.Description,
				ArgHint:     cmd.ArgHint,
				Render:      func(args []string) string { return cmd.Render(args) },
			})
		}
		reg.Add(command.NewSlashCommandTool(slashEntries))
		slashCommandAdded = true
		slashCommandIncludesSkills = slashCommandIncludesSkills || includeSkills
		return "enabled slash_command."
	}
	installSourceAdded := false
	addInstallSourceTool := func() string {
		if installSourceAdded {
			return "install_source is already enabled."
		}
		installSourceAdded = true
		reg.Add(installsource.NewTool(installsource.Options{
			ProjectRoot: root,
			HTTPClient:  balanceClient,
			ConnectMCP: func(e config.PluginEntry) (installsource.MCPConnectResult, error) {
				spec := pluginSpecFromEntryWithOptions(e, root, pluginSpecOptions)
				if opts.Stderr != nil {
					spec.Stderr = opts.Stderr
				}
				// Applying an install plan is already an explicit user decision.
				// Project-scoped installs retain project provenance, but record the
				// exact durable launch grant now so neither this connection nor the
				// next session asks the user to authorize the same install again.
				launchAuthorized := false
				if spec.RequireLaunchApproval {
					if err := plugin.AuthorizeSpecLaunch(ctx, spec); err != nil {
						return installsource.MCPConnectResult{}, err
					}
					launchAuthorized = true
				}
				tools, err := pluginHost.Add(ctx, spec)
				if err != nil {
					// The install did not complete, so do not retain consent for a
					// server that never connected. Replacement rollback reauthorizes
					// the previous project entry before reconnecting it.
					if launchAuthorized && spec.LaunchManager != nil {
						_ = spec.LaunchManager.Revoke(spec.Name)
					}
					return installsource.MCPConnectResult{}, err
				}
				reg.RemovePrefix(plugin.ToolPrefix(spec.Name))
				for _, t := range tools {
					reg.Add(t)
				}
				// Disconnect closes the server and drops its namespaced tools.
				// Used by the install_source rollback path when SaveTo fails.
				disconnect := func() {
					if prefix, ok := pluginHost.Remove(spec.Name); ok {
						reg.RemovePrefix(prefix)
					}
					if spec.LaunchManager != nil {
						_ = spec.LaunchManager.Revoke(spec.Name)
					}
				}
				return installsource.MCPConnectResult{
					ToolCount:  len(tools),
					Disconnect: disconnect,
				}, nil
			},
			OnDisconnect: func(serverName string) bool {
				if prefix, ok := pluginHost.Remove(serverName); ok {
					reg.RemovePrefix(prefix)
					return true
				}
				return false
			},
		}))
		return "enabled install_source."
	}
	readOnlySkillToolsAdded := false
	addReadOnlySkillTools := func() string {
		if readOnlySkillToolsAdded {
			return "read_only_skill tool is already enabled.\n\n" + skill.ReadOnlyIndexBlock(skills)
		}
		readOnlySkillToolsAdded = true
		reg.Add(skill.NewReadOnlySkillTool(skillStore, readOnlySkillRunner, skillProfile))
		return "enabled read_only_skill. Use read_only_skill for inline skills or read-only subagent skills on the next model request.\n\n" + skill.ReadOnlyIndexBlock(skills)
	}
	skillToolsAdded := false
	addSkillTools := func() string {
		if skillToolsAdded {
			return "skills are already enabled.\n\n" + skill.IndexBlock(skills)
		}
		skillToolsAdded = true
		addReadOnlySkillTools()
		reg.Add(skill.NewRunSkillTool(skillStore, skillRunner, skillProfile))
		reg.Add(skill.NewReadSkillTool(skillStore))
		reg.Add(skill.NewInstallSkillTool(skillStore, nil))
		for _, t := range skill.BuiltinSubagentTools(skillStore, skillRunner, skillProfile) {
			reg.Add(t)
		}
		addSlashCommandTool(true)
		return "enabled skills. Use run_skill/read_skill/read_only_skill or the dedicated skill tools on the next model request.\n\n" + skill.IndexBlock(skills)
	}
	if !tokenEconomy {
		addInstallSourceTool()
		addSkillTools()
	}

	return &skillToolsResult{
		cmds:                  cmds,
		skillRunner:           skillRunner,
		readOnlySkillRunner:   readOnlySkillRunner,
		skillProfile:          skillProfile,
		addSkillTools:         addSkillTools,
		addReadOnlySkillTools: addReadOnlySkillTools,
		addInstallSourceTool:  addInstallSourceTool,
		addSlashCommandTool:   addSlashCommandTool,
	}, nil
}

// buildToolSourceConnector registers the economy-mode connect_tool_source
// connector that enables skill/task/MCP/LSP/session/memory sources on demand.
func buildToolSourceConnector(ctx context.Context, opts Options, cfg *config.Config, root string, reg *tool.Registry, writeRoots []string, forbidReadRoots []string, bashSpec sandbox.Spec, bashTimeout time.Duration, searchSpec builtin.SearchSpec, proxySpec netclient.ProxySpec, readPathResolver *builtin.PathResolver, sessionGuard builtin.SessionDataGuard, managedConfig builtin.ManagedConfigPaths, addSkillTools func() string, addTaskTool func() string, addReadOnlyTaskTool func() string, addReadOnlySkillTools func() string, addInstallSourceTool func() string, addSlashCommandTool func(includeSkills bool) string, addLSPTools func() []string, lspMgr *lsp.Manager, addSessionTools func() string, addMemoryTools func() string, onDemandMCPSpecs map[string]plugin.Spec, onDemandMCPNames []string, pluginHost *plugin.Host, tokenEconomy bool) {
	if tokenEconomy {
		addBuiltinSourceTools := func(source string, names ...string) string {
			var missing []string
			for _, name := range names {
				if !builtinToolEnabled(cfg.Tools.Enabled, name) {
					continue
				}
				if _, exists := reg.Get(name); !exists {
					missing = append(missing, name)
				}
			}
			if len(missing) == 0 {
				return source + " tools are already enabled or disabled by [tools].enabled."
			}
			installed := addTools(reg, builtin.Workspace{
				Dir:             root,
				WriteRoots:      writeRoots,
				ForbidReadRoots: forbidReadRoots,
				Bash:            bashSpec,
				BashTimeout:     bashTimeout,
				Search:          searchSpec,
				ProxySpec:       proxySpec,
				ReadPaths:       readPathResolver,
				SessionGuard:    sessionGuard,
				ManagedConfig:   managedConfig,
				FileOverlay:     opts.FileOverlay,
				Terminal:        opts.TerminalRunner,
			}.Tools(missing...))
			return "enabled " + strings.Join(installed, ", ") + "."
		}
		reg.Add(&toolSourceConnector{
			skills: func(context.Context) (string, error) {
				return addSkillTools(), nil
			},
			task: func(context.Context) (string, error) {
				return addTaskTool(), nil
			},
			readOnlyTask: func(context.Context) (string, error) {
				return addReadOnlyTaskTool(), nil
			},
			readOnlySkill: func(context.Context) (string, error) {
				return addReadOnlySkillTools(), nil
			},
			install: func(context.Context) (string, error) {
				return addInstallSourceTool(), nil
			},
			webFetch: func(context.Context) (string, error) {
				if !builtinToolEnabled(cfg.Tools.Enabled, "web_fetch") {
					return "web_fetch is disabled by [tools].enabled.", nil
				}
				names := addTools(reg, builtin.Workspace{
					Dir:         root,
					WriteRoots:  writeRoots,
					Bash:        bashSpec,
					BashTimeout: bashTimeout,
					Search:      searchSpec,
					ProxySpec:   proxySpec,
				}.Tools("web_fetch"))
				if len(names) == 0 {
					return "web_fetch is already enabled or unavailable.", nil
				}
				return "enabled " + strings.Join(names, ", ") + ".", nil
			},
			lsp: func(context.Context) (string, error) {
				if lspMgr == nil {
					return "", fmt.Errorf("LSP is disabled in config")
				}
				names := addLSPTools()
				if len(names) == 0 {
					return "LSP tools are already enabled.", nil
				}
				return "enabled " + strings.Join(names, ", ") + ".", nil
			},
			sessions: func(context.Context) (string, error) {
				return addSessionTools(), nil
			},
			memory: func(context.Context) (string, error) {
				return addMemoryTools(), nil
			},
			commands: func(context.Context) (string, error) {
				return addSlashCommandTool(false), nil
			},
			search: func(context.Context) (string, error) {
				return addBuiltinSourceTools("search", "code_index", "glob", "grep", "ls"), nil
			},
			files: func(context.Context) (string, error) {
				return addBuiltinSourceTools("files", "delete_range", "delete_symbol", "move_file", "multi_edit", "notebook_edit"), nil
			},
			workflow: func(ctx context.Context) (string, error) {
				// complete_step is explicitly execution-phase-only. Keep todo_write
				// available while planning, then expose complete_step on a fresh
				// workflow connect after approval.
				if agent.PlanModeFromContext(ctx) {
					return addBuiltinSourceTools("workflow", "todo_write") +
						" complete_step stays blocked in plan mode; connect workflow again after plan approval to enable it.", nil
				}
				return addBuiltinSourceTools("workflow", "complete_step", "todo_write"), nil
			},
			mcp: func(_ context.Context, name string) (string, error) {
				spec, ok := onDemandMCPSpecs[name]
				if !ok {
					return "", fmt.Errorf("no configured MCP server named %q", name)
				}
				if opts.Stderr != nil {
					spec.Stderr = opts.Stderr
				}
				tools, err := pluginHost.Add(ctx, spec)
				if err != nil {
					// On a shared host the server may already be connected
					// (e.g. another tab started it). Fall back to fetching
					// its tools from the existing client.
					if errors.Is(err, plugin.ErrServerAlreadyConnected) || errors.Is(err, plugin.ErrSpawningInFlight) {
						tools, err2 := pluginHost.ToolsFor(ctx, spec.Name)
						if err2 != nil {
							return "", err2
						}
						reg.RemovePrefix(plugin.ToolPrefix(spec.Name))
						names := addTools(reg, tools)
						if len(names) == 0 {
							return fmt.Sprintf("MCP server %q connected but exposed no tools.", spec.Name), nil
						}
						return fmt.Sprintf("enabled MCP server %q tools: %s.", spec.Name, strings.Join(names, ", ")), nil
					}
					return "", err
				}
				reg.RemovePrefix(plugin.ToolPrefix(spec.Name))
				names := addTools(reg, tools)
				if len(names) == 0 {
					return fmt.Sprintf("MCP server %q connected but exposed no tools.", spec.Name), nil
				}
				return fmt.Sprintf("enabled MCP server %q tools: %s.", spec.Name, strings.Join(names, ", ")), nil
			},
			mcpNames: onDemandMCPNames,
		})
	}
}

// addBuiltins adds enabled built-in tools to reg. An empty list means all of
// them. writeRoots confines the file-writing built-ins to the workspace: after
// the (unconfined) defaults are added, each enabled writer is replaced by an
// instance bound to writeRoots (preserving registry order).
// forbidReadRoots confines the read/list/search built-ins so they cannot peek at
// the listed directories.
// When workDir is non-empty, tools resolve relative paths against it instead of
// the process cwd, enabling concurrent multi-project sessions.
// sessionGuard blocks writer-tool targets inside Corvus's own session stores
// and makes bash warn when a command references them. managedConfig names the
// Corvus-owned config files writable outside writeRoots after a fresh
// per-write human approval.
func addBuiltins(reg *tool.Registry, enabled, writeRoots []string, bashSpec sandbox.Spec, bashTimeout time.Duration, searchSpec builtin.SearchSpec, stderr io.Writer, workDir string, proxySpec netclient.ProxySpec, forbidReadRoots []string, readPathResolver *builtin.PathResolver, sessionGuard builtin.SessionDataGuard, managedConfig builtin.ManagedConfigPaths, overlay builtin.FileOverlay, terminal builtin.TerminalRunner) {
	// If a workspace directory is set, use workspace-bound tools that resolve
	// paths relative to that directory. Otherwise fall back to the process-cwd
	// compile-time builtins.
	if workDir != "" {
		ws := builtin.Workspace{Dir: workDir, WriteRoots: writeRoots, ForbidReadRoots: forbidReadRoots, Bash: bashSpec, BashTimeout: bashTimeout, Search: searchSpec, ProxySpec: proxySpec, ReadPaths: readPathResolver, SessionGuard: sessionGuard, ManagedConfig: managedConfig, FileOverlay: overlay, Terminal: terminal}
		for _, t := range ws.Tools(enabled...) {
			reg.Add(t)
		}
		return
	}

	if len(enabled) == 0 {
		for _, t := range tool.Builtins() {
			reg.Add(t)
		}
	} else {
		for _, name := range enabled {
			if t, ok := tool.LookupBuiltin(name); ok {
				reg.Add(t)
			} else {
				fmt.Fprintf(stderr, "warning: unknown built-in tool %q\n", name)
			}
		}
	}
	// Replace the unconfined defaults with confined instances (registry order is
	// preserved on replace): file-writers bound to the workspace, read tools
	// bound to forbid-read roots, bash to the OS sandbox, web_fetch to the proxy.
	// Only replace tools actually enabled/present.
	confined := append(builtin.ConfineWriters(writeRoots, sessionGuard, managedConfig),
		builtin.ConfineBash(bashSpec, sessionGuard, bashTimeout),
		builtin.ConfineSearch(searchSpec, bashSpec, forbidReadRoots),
		builtin.ConfineWebFetch(proxySpec))
	confined = append(confined, builtin.ConfineReaders(forbidReadRoots)...)
	for _, t := range confined {
		if _, ok := reg.Get(t.Name()); ok {
			reg.Add(t)
		}
	}
}

func builtinToolEnabled(enabled []string, name string) bool {
	if len(enabled) == 0 {
		return true
	}
	name = strings.TrimSpace(name)
	for _, candidate := range enabled {
		if strings.TrimSpace(candidate) == name {
			return true
		}
	}
	return false
}

// partitionByTier splits configured plugin entries into eager (block boot until
// ready) and background (placeholder + start spawn now). Entries with an empty,
// legacy lazy, or unrecognised tier land in background.

// LSPSpecs returns the language → server map: the built-in defaults overlaid with
// any user overrides. A user entry may set only the fields it wants to change;
// empty fields keep the default for that language.
func LSPSpecs(cfg config.LSPConfig) map[string]lsp.ServerSpec {
	specs := lsp.DefaultSpecs()
	for lang, s := range cfg.Servers {
		spec := specs[lang]
		if s.Command != "" {
			spec.Command = s.Command
		}
		if s.Args != nil {
			spec.Args = s.Args
		}
		if s.Env != nil {
			spec.Env = s.Env
		}
		if s.LanguageID != "" {
			spec.LanguageID = s.LanguageID
		}
		if s.Extensions != nil {
			spec.Extensions = s.Extensions
		}
		if s.InstallHint != "" {
			spec.InstallHint = s.InstallHint
		}
		if spec.LanguageID == "" {
			spec.LanguageID = lang
		}
		specs[lang] = spec
	}
	return specs
}
