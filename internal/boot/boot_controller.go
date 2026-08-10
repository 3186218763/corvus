package boot

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"corvus/internal/agent"
	"corvus/internal/capability"
	"corvus/internal/command"
	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/guardian"
	"corvus/internal/hook"
	"corvus/internal/instruction"
	"corvus/internal/jobs"
	"corvus/internal/memory"
	"corvus/internal/netclient"
	"corvus/internal/permission"
	"corvus/internal/plugin"
	"corvus/internal/provider"
	"corvus/internal/recovery"
	"corvus/internal/sandbox"
	"corvus/internal/skill"
	"corvus/internal/tool"
	"corvus/internal/tool/builtin"
	"corvus/internal/workspacelease"
)

type runnerResult struct {
	runner   agent.Runner
	executor *agent.Agent
	label    string
}

// buildExecutorAndPlanner constructs the executor session/options and, when a
// distinct planner_model is configured, wraps it in a two-model Coordinator.
func buildExecutorAndPlanner(ctx context.Context, opts Options, cfg *config.Config, entry *config.ProviderEntry, modelRef string, execProv provider.Provider, reg *tool.Registry, sysPrompt string, sink event.Sink, headlessGate *control.SharedHeadlessGate, hookRunner *hook.Runner, jm *jobs.Manager, subagentScheduler *agent.SubagentScheduler, root string, projectChecks []instruction.VerifyCheck, tokenDelivery bool, workspaceLease *workspacelease.Owner, capLedger *capability.Ledger, capAudit *capability.Audit, keepPolicy agent.KeepPolicy, maxSubagentDepth, maxSteps int, mem *memory.Set, capRuntime *agent.MCPCapabilityRuntime, tokenEconomy bool, proxySpec netclient.ProxySpec) (*runnerResult, error) {
	execSess := agent.NewSession(sysPrompt)
	// Session path is bound later via Controller.SetSessionPath / NewSession /
	// Resume, which call SetSessionCacheID(BranchID). Boot leaves it empty so
	// headless runs without a path correctly omit the sticky key.
	execOpts := agent.Options{
		MaxSteps:    maxSteps,
		MaxStepsKey: opts.MaxStepsKey,
		Temperature: cfg.Agent.Temperature,
		Pricing:     entry.Price,
		ModelRef:    modelRef,
		Gate:        headlessGate,
		Hooks:       hookRunner,
		Jobs:        jm,
		// Parent write reservation at the executor entry covers all writers
		// (including late Economy/MCP adds) without wrapping tool schemas.
		WriteScheduler:               subagentScheduler,
		WriteWorkspaceRoot:           root,
		ProjectChecks:                projectChecks,
		DeliveryProfile:              tokenDelivery,
		WorkspaceLease:               workspaceLease,
		CapabilityLedger:             capLedger,
		CapabilityAudit:              capAudit,
		ContextWindow:                entry.ContextWindow,
		SoftCompactRatio:             cfg.Agent.SoftCompactRatio,
		ToolResultSnipRatio:          cfg.Agent.ToolResultSnipRatio,
		CompactRatio:                 cfg.Agent.CompactRatio,
		CompactForceRatio:            cfg.Agent.CompactForceRatio,
		RecentKeep:                   cfg.Agent.RecentKeep,
		ArchiveDir:                   config.ArchiveDir(),
		KeepPolicy:                   keepPolicy,
		ReasoningLanguage:            cfg.ReasoningLanguage(),
		PlanModeReadOnlyCommands:     cfg.Agent.PlanModeReadOnlyCommands,
		SubagentDepth:                0,
		MaxSubagentDepth:             maxSubagentDepth,
		MissingReasoningWarnStateDir: config.MissingReasoningWarnStateDir(),
	}
	promptCacheOptions(cfg, entry, "", "").apply(&execOpts)
	executor := agent.New(execProv, reg, execSess, execOpts, sink)

	var runner agent.Runner = executor
	label := entry.Model
	// Two-model collaboration: a distinct planner_model wraps the executor in a
	// Coordinator with its own session, kept separate for cache stability. The
	// planner gets the same standing memory context and a filtered read-only
	// research tool set, so it can inspect rules/code without side effects.
	if pm := effectivePlannerModel(cfg, opts, tokenEconomy); pm != "" {
		pe, ok := resolveOptionalEntry(opts, cfg, pm)
		if !ok {
			return nil, fmt.Errorf("planner_model %q is not a configured provider", pm)
		}
		if pe.Model != entry.Model {
			plannerProv, err := resolveProvider(opts, cfg, proxySpec, provider.Selection{Ref: modelRefFromEntry(pe)})
			if err != nil {
				return nil, fmt.Errorf("planner %q: %w", pm, err)
			}
			plannerSess := agent.NewSession(agent.PlannerPromptWithContext(mem.Block()))
			// Planner owns an independent ledger/audit and use_capability frontend
			// so its MCP calls cannot satisfy or poison Executor Delivery gates.
			plannerLedger := capability.NewLedger()
			plannerAudit := &capability.Audit{}
			plannerTools := agent.PlannerToolRegistry(reg)
			if capRuntime != nil {
				// Replace any cloned parent frontend with one bound to the
				// planner ledger (PlannerToolRegistry clones with nil ledger).
				if _, ok := plannerTools.Get("use_capability"); ok {
					plannerTools.RemovePrefix("use_capability")
				}
				plannerTools.Add(capRuntime.NewFrontend(plannerLedger, plannerAudit))
			}
			plannerOpts := agent.Options{
				MaxSteps:                     0,
				Gate:                         headlessGate,
				ModelRef:                     modelRefFromEntry(pe),
				ContextWindow:                pe.ContextWindow,
				SoftCompactRatio:             cfg.Agent.SoftCompactRatio,
				ToolResultSnipRatio:          cfg.Agent.ToolResultSnipRatio,
				CompactRatio:                 cfg.Agent.CompactRatio,
				CompactForceRatio:            cfg.Agent.CompactForceRatio,
				RecentKeep:                   cfg.Agent.RecentKeep,
				ArchiveDir:                   config.ArchiveDir(),
				KeepPolicy:                   keepPolicy,
				ReasoningLanguage:            cfg.ReasoningLanguage(),
				PlanModeReadOnlyCommands:     cfg.Agent.PlanModeReadOnlyCommands,
				CapabilityLedger:             plannerLedger,
				CapabilityAudit:              plannerAudit,
				MissingReasoningWarnStateDir: config.MissingReasoningWarnStateDir(),
			}
			// Same sticky-key policy as executor; SessionCacheID refreshed with
			// the executor when the controller rebinds the session path.
			promptCacheOptions(cfg, pe, "", "").apply(&plannerOpts)
			runner = agent.NewCoordinatorWithPlannerPolicy(plannerProv, plannerSess, pe.Price, plannerTools, plannerOpts, executor, cfg.Agent.Temperature, sink, control.NewPlannerPolicy())
			label = entry.Model + " + planner " + pe.Model
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
// Delivery/Economy/dual-model capability routing.
func buildController(ctx context.Context, opts Options, cfg *config.Config, root string, sink event.Sink, policy permission.Policy, headlessGate *control.SharedHeadlessGate, label, modelRef, sysPrompt, sessionDir string, pluginHost *plugin.Host, cmds []command.Command, skills []skill.Skill, allSkills []skill.Skill, skillStore *skill.Store, allSkillStore *skill.Store, skillRunner func(context.Context, skill.Skill, string, skill.SubagentRunOptions) (string, error), readOnlySkillRunner func(context.Context, skill.Skill, string, skill.SubagentRunOptions) (string, error), skillProfile func(skill.Skill) *event.Profile, hookRunner *hook.Runner, mem *memory.Set, cleanup func(), entry *config.ProviderEntry, balanceClient *http.Client, jm *jobs.Manager, workspaceLease *workspacelease.Owner, reg *tool.Registry, pluginSpecOptions PluginSpecOptions, capRuntime *agent.MCPCapabilityRuntime, readPathResolver *builtin.PathResolver, shell sandbox.Shell, runtimeProfile capability.Profile, taskTool *agent.TaskTool, capSpecs []plugin.Spec, capAudit *capability.Audit, resolveSubagentProvider func(modelRef, effort string) (provider.Provider, *provider.Pricing, int, error), subagentIdentity func(modelRef, effort string) (string, string), execProv provider.Provider, proxySpec netclient.ProxySpec, tokenDelivery, tokenEconomy, dualModelPlanner bool, runner agent.Runner, executor *agent.Agent) (*control.Controller, error) {
	ctrlOpts := control.Options{
		Runner:                runner,
		Executor:              executor,
		Sink:                  sink,
		Policy:                policy,
		SubagentGate:          headlessGate,
		Label:                 label,
		ModelRef:              modelRef,
		SystemPrompt:          sysPrompt,
		SessionDir:            sessionDir,
		Host:                  pluginHost,
		Commands:              cmds,
		Skills:                skills,
		AllSkills:             allSkills,
		SkillStore:            skillStore,
		AllSkillStore:         allSkillStore,
		SkillRunner:           skillRunner,
		ReadOnlySkillRunner:   readOnlySkillRunner,
		SkillProfile:          skillProfile,
		Hooks:                 hookRunner,
		Memory:                mem,
		Cleanup:               cleanup,
		BalanceURL:            entry.BalanceURL,
		BalanceKey:            entry.EffectiveAPIKey(),
		BalanceClient:         balanceClient,
		Jobs:                  jm,
		WorkspaceLease:        workspaceLease,
		Registry:              reg,
		PluginCtx:             ctx,
		MCPDefaultCallTimeout: pluginSpecOptions.DefaultCallTimeout,
		MCPConfigureSpec: func(spec *plugin.Spec) {
			if spec == nil {
				return
			}
			spec.LaunchManager = pluginSpecOptions.LaunchManager
			if strings.TrimSpace(spec.ConfigSource) == "" {
				spec.ConfigSource = pluginSpecOptions.ConfigSource
			}
			if spec.DefaultStartupTimeout <= 0 {
				spec.DefaultStartupTimeout = pluginSpecOptions.DefaultStartupTimeout
			}
			applyMCPIsolation(spec, root, pluginSpecOptions)
		},
		CapabilityRuntime:      capRuntime,
		WorkspaceRoot:          root,
		ExternalFolderToolRefs: readPathResolver,
		ResponseLanguage:       cfg.ResponseLanguage(),
		ReasoningLanguage:      cfg.ReasoningLanguage(),
		DisableColdResumePrune: !cfg.ColdResumePruneEnabled(),
		Shell:                  shell,
		ApprovalTimeout:        opts.ApprovalTimeout,
		RuntimeProfile:         runtimeProfile,
		OnRemember: func(rule string) control.RememberResult {
			return rememberPermissionRule(root, rule)
		},
		OnRememberPlanModeReadOnlyCommand: func(prefix string) control.PlanModeReadOnlyCommandTrustResult {
			return rememberPlanModeReadOnlyCommand(root, prefix)
		},
		SessionRecoveryMeta: opts.SessionRecoveryMeta,
		OnSessionRecovered:  opts.OnSessionRecovered,
	}
	// Guardian: when guardian_model is configured, spawn an LLM safety reviewer
	// that can auto-allow safe Ask decisions and annotate risky ones before
	// escalating to the human approval prompt.
	if guardianModel := cfg.Agent.GuardianModel; guardianModel != "" {
		ge, ok := resolveOptionalEntry(opts, cfg, guardianModel)
		if !ok {
			slog.Warn("guardian model is not a configured provider — guardian disabled", "model", guardianModel)
			sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Guardian was disabled because its model was not found.", Detail: fmt.Sprintf("guardian_model %q not found — guardian disabled", guardianModel)})
		} else {
			pProv, err := resolveProvider(opts, cfg, proxySpec, provider.Selection{Ref: modelRefFromEntry(ge)})
			if err != nil {
				slog.Warn("guardian provider construction failed — guardian disabled", "model", guardianModel, "err", err)
				sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Guardian was disabled because it could not start.", Detail: fmt.Sprintf("guardian construction failed: %v — guardian disabled", err)})
			} else {
				guardianReg := agent.FilterReadOnlyRegistry(reg, agent.SubagentMetaTools()...)
				ctrlOpts.Guardian = guardian.NewSession(pProv, guardianReg, guardian.PolicyPrompt(), modelRefFromEntry(ge), cfg.Agent.GuardianTemperature, ge.Price, sink)
				sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf("guardian enabled · model=%s", ge.Model)})
			}
		}
	}
	// Recovery reviewer: prefer recovery_model, then guardian_model, then the
	// active main model with an isolated session/policy.
	{
		recoveryModel := strings.TrimSpace(cfg.Agent.RecoveryModel)
		if recoveryModel == "" {
			recoveryModel = strings.TrimSpace(cfg.Agent.GuardianModel)
		}
		if recoveryModel == "" {
			recoveryModel = modelRef
		}
		if recoveryModel != "" {
			if re, ok := cfg.ResolveModel(recoveryModel); ok {
				if rProv, err := NewProviderWithProxy(re, proxySpec, cfg.WebSearch.Enabled()); err == nil {
					ctrlOpts.RecoveryReviewer = recovery.NewSessionWithSink(rProv, re.Price, modelRefFromEntry(re), sink)
				} else {
					slog.Warn("recovery reviewer provider construction failed — rule-only recovery", "model", recoveryModel, "err", err)
				}
			}
		}
		// HeadlessApprovalMode is an explicit declaration that this frontend has
		// no decision channel (`corvus run`). ApprovalTimeout is not a proxy for
		// that capability: bots have a bounded timeout and can still answer cards.
		ctrlOpts.RecoveryHeadless = recoveryHeadlessMode(opts)
	}
	ctrl := control.New(ctrlOpts)
	// Share the recovery checkpoint with task/fleet sub-agents so background
	// writers observe the same failure state as the root agent.
	if taskTool != nil {
		if g := ctrl.Executor(); g != nil {
			taskTool.WithRecoveryGate(g.RecoveryGate())
		}
	}
	if capRuntime != nil {
		ctrl.SetCapabilityProxyTools(capRuntime.ConnectedProxyTools)
	}
	// Task tools created before capRuntime assignment still need the runtime if
	// they were built early; re-bind when present.
	if taskTool != nil && capRuntime != nil {
		taskTool.WithCapabilityRuntime(capRuntime)
	}
	if tokenDelivery {
		var router *capability.SemanticRouter
		// Prefer agent.subagent_models["capability-router"] when configured.
		if modelRef := strings.TrimSpace(cfg.Agent.SubagentModels["capability-router"]); modelRef != "" {
			effortRef := strings.TrimSpace(cfg.Agent.SubagentEfforts["capability-router"])
			if p, price, _, err := resolveSubagentProvider(modelRef, effortRef); err == nil && p != nil {
				usageModelRef, _ := subagentIdentity(modelRef, effortRef)
				router = &capability.SemanticRouter{Provider: p, Sink: sink, Model: usageModelRef, Pricing: price, Audit: capAudit}
			}
		}
		if router == nil {
			// Fallback to the executor's provider — and its pricing, so router
			// usage events never display as zero-cost.
			router = &capability.SemanticRouter{Provider: execProv, Sink: sink, Model: modelRef, Pricing: entry.Price, Audit: capAudit}
		}
		ctrl.WireCapabilityRouting(cfg.Plugins, capSpecs, router, capAudit)
		ctrl.SetCapabilityProxyRouting(true)
	} else if tokenEconomy {
		ctrl.WireCapabilityRouting(cfg.Plugins, capSpecs, nil, nil)
	} else if dualModelPlanner {
		// Balanced dual-model: load plugin config + schema cache so not-yet-
		// started MCP can route through the stable Planner/Executor proxy.
		// No semantic router — deterministic route only.
		ctrl.WireCapabilityRouting(cfg.Plugins, capSpecs, nil, capAudit)
		ctrl.SetCapabilityProxyRouting(true)
	}
	return ctrl, nil
}

func recoveryHeadlessMode(opts Options) bool {
	return strings.TrimSpace(opts.HeadlessApprovalMode) != ""
}

// effectivePlannerModel centralizes planner precedence. The explicit ACP hard
// override is checked before user/project config and cannot be reversed by a
// later assembly branch.
func effectivePlannerModel(cfg *config.Config, opts Options, tokenEconomy bool) string {
	if cfg == nil || opts.DisablePlanner || tokenEconomy {
		return ""
	}
	return strings.TrimSpace(cfg.Agent.PlannerModel)
}
