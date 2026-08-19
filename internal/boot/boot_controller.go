package boot

import (
	"fmt"
	"log/slog"
	"strings"

	"corvus/internal/agent"
	"corvus/internal/capability"
	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/guardian"
	"corvus/internal/plugin"
	"corvus/internal/provider"
	"corvus/internal/recovery"
	"corvus/internal/runtimepolicy"
)

type runnerResult struct {
	runner   agent.Runner
	executor *agent.Agent
	label    string
}

// buildExecutorAndPlanner constructs the executor session/options and, when a
// distinct planner_model is configured, wraps it in a two-model Coordinator.
func buildExecutorAndPlanner(a *assembly) (*runnerResult, error) {
	execSess := agent.NewSession(a.sysPrompt)
	// Session path is bound later via Controller.SetSessionPath / NewSession /
	// Resume, which call SetSessionCacheID(BranchID). Boot leaves it empty so
	// headless runs without a path correctly omit the sticky key.
	execOpts := agent.Options{
		MaxSteps:    a.maxSteps,
		MaxStepsKey: a.opts.MaxStepsKey,
		Temperature: a.cfg.Agent.Temperature,
		Pricing:     a.entry.Price,
		ModelRef:    a.modelRef,
		Gate:        a.headlessGate,
		Hooks:       a.hookRunner,
		Jobs:        a.jm,
		// Parent write reservation at the executor entry covers all writers
		// (including late Economy/MCP adds) without wrapping tool schemas.
		WriteScheduler:               a.subagentScheduler,
		WriteWorkspaceRoot:           a.root,
		ProjectChecks:                a.projectChecks,
		DeliveryProfile:              a.runtimePolicy.Completion == runtimepolicy.CompletionVerified,
		WorkspaceLease:               a.workspaceLease,
		CapabilityLedger:             a.capLedger,
		CapabilityAudit:              a.capAudit,
		ContextWindow:                a.entry.ContextWindow,
		SoftCompactRatio:             a.cfg.Agent.SoftCompactRatio,
		ToolResultSnipRatio:          a.cfg.Agent.ToolResultSnipRatio,
		CompactRatio:                 a.cfg.Agent.CompactRatio,
		CompactForceRatio:            a.cfg.Agent.CompactForceRatio,
		RecentKeep:                   a.cfg.Agent.RecentKeep,
		ArchiveDir:                   config.ArchiveDir(),
		KeepPolicy:                   a.keepPolicy,
		ReasoningLanguage:            a.cfg.ReasoningLanguage(),
		PlanModeReadOnlyCommands:     a.cfg.Agent.PlanModeReadOnlyCommands,
		SubagentDepth:                0,
		MaxSubagentDepth:             a.maxSubagentDepth,
		MissingReasoningWarnStateDir: config.MissingReasoningWarnStateDir(),
	}
	// Compaction is an out-of-band request. Give it a fresh provider client so
	// stateful executor protocols (notably Responses previous_response_id) do
	// not observe or overwrite summary traffic.
	if a.opts.ProviderResolver == nil {
		compactionProv, err := NewCompactionProviderWithProxy(a.entry, a.proxySpec, a.cfg.WebSearch.Enabled())
		if err != nil {
			return nil, fmt.Errorf("construct compaction summarizer provider: %w", err)
		}
		execOpts.CompactionSummarizer = agent.NewProviderCompactionSummarizer(compactionProv, a.cfg.Agent.Temperature, a.sink, a.modelRef, a.entry.Price)
	}
	promptCacheOptions(a.cfg, a.entry, "", "").apply(&execOpts)
	executor := agent.New(a.execProv, a.reg, execSess, execOpts, a.sink)

	var runner agent.Runner = executor
	label := a.entry.Model
	// Two-model collaboration: a distinct planner_model wraps the executor in a
	// Coordinator with its own session, kept separate for cache stability. The
	// planner gets the same standing memory context and a filtered read-only
	// research tool set, so it can inspect rules/code without side effects.
	if pm := effectivePlannerModel(a.cfg, a.opts, a.runtimePolicy.Exposure == runtimepolicy.ExposureDeferred); pm != "" {
		pe, ok := resolveOptionalEntry(a.opts, a.cfg, pm)
		if !ok {
			return nil, fmt.Errorf("planner_model %q is not a configured provider", pm)
		}
		if pe.Model != a.entry.Model {
			plannerProv, err := resolveProvider(a.opts, a.cfg, a.proxySpec, provider.Selection{Ref: modelRefFromEntry(pe)})
			if err != nil {
				return nil, fmt.Errorf("planner %q: %w", pm, err)
			}
			plannerSess := agent.NewSession(agent.PlannerPromptWithContext(a.mem.Block()))
			// Planner owns an independent ledger/audit and use_capability frontend
			// so its MCP calls cannot satisfy or poison Executor Delivery gates.
			plannerLedger := capability.NewLedger()
			plannerAudit := &capability.Audit{}
			plannerTools := agent.PlannerToolRegistry(a.reg)
			if a.capRuntime != nil {
				// Replace any cloned parent frontend with one bound to the
				// planner ledger (PlannerToolRegistry clones with nil ledger).
				if _, ok := plannerTools.Get("use_capability"); ok {
					plannerTools.RemovePrefix("use_capability")
				}
				plannerTools.Add(a.capRuntime.NewFrontend(plannerLedger, plannerAudit))
			}
			plannerOpts := agent.Options{
				MaxSteps:                     0,
				Gate:                         a.headlessGate,
				ModelRef:                     modelRefFromEntry(pe),
				ContextWindow:                pe.ContextWindow,
				SoftCompactRatio:             a.cfg.Agent.SoftCompactRatio,
				ToolResultSnipRatio:          a.cfg.Agent.ToolResultSnipRatio,
				CompactRatio:                 a.cfg.Agent.CompactRatio,
				CompactForceRatio:            a.cfg.Agent.CompactForceRatio,
				RecentKeep:                   a.cfg.Agent.RecentKeep,
				ArchiveDir:                   config.ArchiveDir(),
				KeepPolicy:                   a.keepPolicy,
				ReasoningLanguage:            a.cfg.ReasoningLanguage(),
				PlanModeReadOnlyCommands:     a.cfg.Agent.PlanModeReadOnlyCommands,
				CapabilityLedger:             plannerLedger,
				CapabilityAudit:              plannerAudit,
				MissingReasoningWarnStateDir: config.MissingReasoningWarnStateDir(),
			}
			if a.opts.ProviderResolver == nil {
				plannerCompactionProv, err := NewCompactionProviderWithProxy(pe, a.proxySpec, a.cfg.WebSearch.Enabled())
				if err != nil {
					return nil, fmt.Errorf("construct planner compaction summarizer provider: %w", err)
				}
				plannerOpts.CompactionSummarizer = agent.NewProviderCompactionSummarizer(plannerCompactionProv, a.cfg.Agent.Temperature, a.sink, modelRefFromEntry(pe), pe.Price)
			}
			// Same sticky-key policy as executor; SessionCacheID refreshed with
			// the executor when the controller rebinds the session path.
			promptCacheOptions(a.cfg, pe, "", "").apply(&plannerOpts)
			runner = agent.NewCoordinatorWithPlannerPolicy(plannerProv, plannerSess, pe.Price, plannerTools, plannerOpts, executor, a.cfg.Agent.Temperature, a.sink, control.NewPlannerPolicy())
			label = a.entry.Model + " + planner " + pe.Model
		}
	}

	return &runnerResult{
		runner:   runner,
		executor: executor,
		label:    label,
	}, nil
}

// buildController assembles control.Options and constructs the controller,
// attaching the guardian, recovery reviewer, capability proxy tools, and
// completion/deferred-exposure/dual-model capability routing.
func buildController(a *assembly) (*control.Controller, error) {
	ctrlOpts := control.Options{
		Runner:                a.runner,
		Executor:              a.executor,
		Sink:                  a.sink,
		Policy:                a.policy,
		SubagentGate:          a.headlessGate,
		Label:                 a.label,
		ModelRef:              a.modelRef,
		SystemPrompt:          a.sysPrompt,
		SessionDir:            a.sessionDir,
		Host:                  a.pluginHost,
		Commands:              a.cmds,
		Skills:                a.skills,
		AllSkills:             a.allSkills,
		SkillStore:            a.skillStore,
		AllSkillStore:         a.allSkillStore,
		SkillRunner:           a.skillRunner,
		ReadOnlySkillRunner:   a.readOnlySkillRunner,
		SkillProfile:          a.skillProfile,
		Hooks:                 a.hookRunner,
		Memory:                a.mem,
		Cleanup:               a.cleanup,
		BalanceURL:            a.entry.BalanceURL,
		BalanceKey:            a.entry.EffectiveAPIKey(),
		BalanceClient:         a.balanceClient,
		Jobs:                  a.jm,
		WorkspaceLease:        a.workspaceLease,
		Registry:              a.reg,
		PluginCtx:             a.ctx,
		MCPDefaultCallTimeout: a.pluginSpecOptions.DefaultCallTimeout,
		MCPConfigureSpec: func(spec *plugin.Spec) {
			if spec == nil {
				return
			}
			spec.LaunchManager = a.pluginSpecOptions.LaunchManager
			if strings.TrimSpace(spec.ConfigSource) == "" {
				spec.ConfigSource = a.pluginSpecOptions.ConfigSource
			}
			if spec.DefaultStartupTimeout <= 0 {
				spec.DefaultStartupTimeout = a.pluginSpecOptions.DefaultStartupTimeout
			}
			applyMCPIsolation(spec, a.root, a.pluginSpecOptions)
		},
		CapabilityRuntime:      a.capRuntime,
		WorkspaceRoot:          a.root,
		ResponseLanguage:       a.cfg.ResponseLanguage(),
		ReasoningLanguage:      a.cfg.ReasoningLanguage(),
		DisableColdResumePrune: !a.cfg.ColdResumePruneEnabled(),
		Shell:                  a.shell,
		ApprovalTimeout:        a.opts.ApprovalTimeout,
		RuntimePolicyRequest:   a.runtimeRequest,
		RuntimePolicy:          a.runtimePolicy,
		OnRemember: func(rule string) control.RememberResult {
			return rememberPermissionRule(a.root, rule)
		},
		SessionRecoveryMeta: a.opts.SessionRecoveryMeta,
		OnSessionRecovered:  a.opts.OnSessionRecovered,
	}
	// Guardian: when guardian_model is configured, spawn an LLM safety reviewer
	// that can auto-allow safe Ask decisions and annotate risky ones before
	// escalating to the human approval prompt.
	if guardianModel := a.cfg.Agent.GuardianModel; guardianModel != "" {
		ge, ok := resolveOptionalEntry(a.opts, a.cfg, guardianModel)
		if !ok {
			slog.Warn("guardian model is not a configured provider — guardian disabled", "model", guardianModel)
			a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Guardian was disabled because its model was not found.", Detail: fmt.Sprintf("guardian_model %q not found — guardian disabled", guardianModel)})
		} else {
			pProv, err := resolveProvider(a.opts, a.cfg, a.proxySpec, provider.Selection{Ref: modelRefFromEntry(ge)})
			if err != nil {
				slog.Warn("guardian provider construction failed — guardian disabled", "model", guardianModel, "err", err)
				a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Guardian was disabled because it could not start.", Detail: fmt.Sprintf("guardian construction failed: %v — guardian disabled", err)})
			} else {
				guardianReg := agent.FilterReadOnlyRegistry(a.reg, agent.SubagentMetaTools()...)
				ctrlOpts.Guardian = guardian.NewSession(pProv, guardianReg, guardian.PolicyPrompt(), modelRefFromEntry(ge), a.cfg.Agent.GuardianTemperature, ge.Price, a.sink)
				a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf("guardian enabled · model=%s", ge.Model)})
			}
		}
	}
	// Recovery reviewer: prefer recovery_model, then guardian_model, then the
	// active main model with an isolated session/policy.
	{
		recoveryModel := strings.TrimSpace(a.cfg.Agent.RecoveryModel)
		if recoveryModel == "" {
			recoveryModel = strings.TrimSpace(a.cfg.Agent.GuardianModel)
		}
		if recoveryModel == "" {
			recoveryModel = a.modelRef
		}
		if recoveryModel != "" {
			if re, ok := a.cfg.ResolveModel(recoveryModel); ok {
				if rProv, err := NewProviderWithProxy(re, a.proxySpec, a.cfg.WebSearch.Enabled()); err == nil {
					ctrlOpts.RecoveryReviewer = recovery.NewSessionWithSink(rProv, re.Price, modelRefFromEntry(re), a.sink)
				} else {
					slog.Warn("recovery reviewer provider construction failed — rule-only recovery", "model", recoveryModel, "err", err)
				}
			}
		}
		// HeadlessApprovalMode is an explicit declaration that this frontend has
		// no decision channel (`corvus run`). ApprovalTimeout is not a proxy for
		// that capability: bots have a bounded timeout and can still answer cards.
		ctrlOpts.RecoveryHeadless = recoveryHeadlessMode(a.opts)
	}
	ctrl := control.New(ctrlOpts)
	// Share the recovery checkpoint with task/fleet sub-agents so background
	// writers observe the same failure state as the root agent.
	if a.taskTool != nil {
		if g := ctrl.Executor(); g != nil {
			a.taskTool.WithRecoveryGate(g.RecoveryGate())
		}
	}
	if a.capRuntime != nil {
		ctrl.SetCapabilityProxyTools(a.capRuntime.ConnectedProxyTools)
	}
	// Task tools created before capRuntime assignment still need the runtime if
	// they were built early; re-bind when present.
	if a.taskTool != nil && a.capRuntime != nil {
		a.taskTool.WithCapabilityRuntime(a.capRuntime)
	}
	if a.runtimePolicy.Completion == runtimepolicy.CompletionVerified {
		var router *capability.SemanticRouter
		// Prefer agent.subagent_models["capability-router"] when configured.
		if routerRef := strings.TrimSpace(a.cfg.Agent.SubagentModels["capability-router"]); routerRef != "" {
			effortRef := strings.TrimSpace(a.cfg.Agent.SubagentEfforts["capability-router"])
			if p, price, _, err := a.resolveSubagentProvider(routerRef, effortRef); err == nil && p != nil {
				usageModelRef, _ := a.subagentIdentity(routerRef, effortRef)
				router = &capability.SemanticRouter{Provider: p, Sink: a.sink, Model: usageModelRef, Pricing: price, Audit: a.capAudit}
			}
		}
		if router == nil {
			// Fallback to the executor's provider — and its pricing, so router
			// usage events never display as zero-cost.
			router = &capability.SemanticRouter{Provider: a.execProv, Sink: a.sink, Model: a.modelRef, Pricing: a.entry.Price, Audit: a.capAudit}
		}
		ctrl.WireCapabilityRouting(a.cfg.Plugins, a.capSpecs, router, a.capAudit)
		ctrl.SetCapabilityProxyRouting(true)
	} else if a.runtimePolicy.Exposure == runtimepolicy.ExposureDeferred {
		ctrl.WireCapabilityRouting(a.cfg.Plugins, a.capSpecs, nil, nil)
	} else if a.dualModelPlanner {
		// Dual-model planner: load plugin config + schema cache so not-yet-
		// started MCP can route through the stable Planner/Executor proxy.
		// No semantic router — deterministic route only.
		ctrl.WireCapabilityRouting(a.cfg.Plugins, a.capSpecs, nil, a.capAudit)
		ctrl.SetCapabilityProxyRouting(true)
	}
	return ctrl, nil
}

func recoveryHeadlessMode(opts Options) bool {
	return strings.TrimSpace(opts.HeadlessApprovalMode) != ""
}

// effectivePlannerModel centralizes planner precedence. Exposure is
// intentionally not an input to planner selection: deferred tools affect the
// initial surface only, never whether the configured planner exists.
func effectivePlannerModel(cfg *config.Config, opts Options, exposureDeferred bool) string {
	_ = exposureDeferred // retained for source compatibility with older callers
	if cfg == nil || opts.DisablePlanner {
		return ""
	}
	return strings.TrimSpace(cfg.Agent.PlannerModel)
}
