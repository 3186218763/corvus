package boot

import (
	"fmt"
	"strings"

	"corvus/internal/agent"
	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/netclient"
	"corvus/internal/provider"
	"corvus/internal/runtimepolicy"
	"corvus/internal/sandbox"
	"corvus/internal/skill"
	"corvus/internal/store"
	"corvus/internal/textutil"
	"corvus/internal/tool"
	"corvus/internal/workspacelease"
)

type subagentResult struct {
	resolveSubagentProvider func(modelRef, effort string) (provider.Provider, *provider.Pricing, int, error)
	subagentIdentity        func(modelRef, effort string) (string, string)
	maxSubagentDepth        int
	subagentScheduler       *agent.SubagentScheduler
	addTaskTool             func() string
	addReadOnlyTaskTool     func() string
	taskTool                *agent.TaskTool
	capRuntimeGet           func() *agent.MCPCapabilityRuntime
	capRuntimeSet           func(*agent.MCPCapabilityRuntime)
}

// buildSubagentTools wires the sub-agent machinery: provider resolution,
// task/read_only_task tools, scheduler, and the shared MCP capability runtime
// cell. capRuntimeGet/capRuntimeSet expose the cell to later assembly stages
// that assign the runtime after MCP specs load.
func buildSubagentTools(cfg *config.Config, opts Options, entry *config.ProviderEntry, modelName string, proxySpec netclient.ProxySpec, execProv provider.Provider, reg *tool.Registry, maxSteps int, subagentStore *agent.SubagentStore, headlessGate *control.SharedHeadlessGate, keepPolicy agent.KeepPolicy, root string, bashSpec sandbox.Spec, workspaceLease *workspacelease.Owner, skillStore *skill.Store, policy runtimepolicy.Policy) (*subagentResult, error) {
	// The `task` tool spawns sub-agents that reuse the parent's provider and
	// tool registry. Wired here after the built-ins / plugins are loaded so
	// sub-agents inherit the full tool set (minus `task` itself, to keep
	// nesting out of the picture). It registers into the same reg the
	// executor uses, so the model surfaces it like any other tool.
	resolveSubagentProvider := func(modelRef, effort string) (provider.Provider, *provider.Pricing, int, error) {
		me := *entry
		selectedRef := modelRefFromEntry(entry)
		if strings.TrimSpace(modelRef) != "" {
			if resolved, ok := cfg.ResolveModel(modelRef); ok {
				me = *resolved
				selectedRef = modelRefFromEntry(resolved)
			} else if opts.ProviderResolver != nil {
				me = *syntheticEntryFromResolver(opts.ProviderResolver, modelRef)
				selectedRef = modelRef
			} else {
				return nil, nil, 0, fmt.Errorf("unknown model %q", modelRef)
			}
		}
		var effortOverride *string
		if strings.TrimSpace(effort) != "" {
			normalized, err := config.NormalizeEffort(&me, effort)
			if err != nil {
				if opts.ProviderResolver == nil {
					return nil, nil, 0, err
				}
				normalized = effort
			}
			me.Effort = normalized
			effortOverride = &normalized
			if me.Kind == "anthropic" && strings.TrimSpace(me.Effort) != "" && strings.TrimSpace(me.Thinking) == "" {
				me.Thinking = "adaptive"
			}
		}
		p, err := resolveProvider(opts, cfg, proxySpec, provider.Selection{Ref: selectedRef, Effort: effortOverride})
		if err != nil {
			return nil, nil, 0, err
		}
		return p, me.Price, me.ContextWindow, nil
	}
	subagentIdentity := func(modelRef, effort string) (string, string) {
		return subagentEffectiveIdentity(cfg, opts.ProviderResolver, modelName, entry, modelRef, effort)
	}
	taskModel := textutil.FirstNonBlank(cfg.Agent.SubagentModels["task"], cfg.Agent.SubagentModel)
	taskEffort := textutil.FirstNonBlank(cfg.Agent.SubagentEfforts["task"], cfg.Agent.SubagentEffort)
	maxSubagentDepth := agent.NormalizeMaxSubagentDepth(cfg.Agent.MaxSubagentDepth)
	maxSubagentConcurrency, maxParallelWriters := agent.NormalizeConcurrencyLimits(
		cfg.Agent.MaxSubagentConcurrency, cfg.Agent.MaxParallelWriters,
	)
	subagentScheduler := agent.NewSubagentScheduler(maxSubagentConcurrency, maxParallelWriters)
	profileLookup := func(name string) (agent.ProfileDefinition, bool) {
		sk, ok := skillStore.Read(name)
		if !ok || sk.RunAs != skill.RunSubagent {
			return agent.ProfileDefinition{}, false
		}
		sk = skillStore.Prepare(sk)
		return agent.ProfileDefinition{
			Name:         sk.Name,
			Body:         sk.Body,
			AllowedTools: sk.AllowedTools,
			Model:        sk.Model,
			Effort:       sk.Effort,
			ReadOnly:     sk.ReadOnly,
			Invocation:   sk.Invocation,
			NamedBuiltin: agent.NamedBuiltinProfile(sk.Name),
		}, true
	}
	profileConfigModel := func(profile string) string {
		for _, key := range SubagentModelKeys(profile) {
			if m := strings.TrimSpace(cfg.Agent.SubagentModels[key]); m != "" {
				return m
			}
		}
		return ""
	}
	profileConfigEffort := func(profile string) string {
		for _, key := range SubagentModelKeys(profile) {
			if e := strings.TrimSpace(cfg.Agent.SubagentEfforts[key]); e != "" {
				return e
			}
		}
		return ""
	}
	bashSandboxEnforced := func() bool {
		return bashSpec.Enforce()
	}
	taskToolAdded := false
	readOnlyTaskToolAdded := false
	var taskTool *agent.TaskTool
	// capRuntime is assigned after MCP specs load; closures capture the variable
	// so task tools created later still receive the session-shared substrate.
	var capRuntime *agent.MCPCapabilityRuntime
	newTaskTool := func() *agent.TaskTool {
		// Sticky prompt-cache policy mirrors the parent executor entry; session
		// BranchID + subagent Ref are filled per-run in TaskTool.subagentOptions.
		taskPC := promptCacheOptions(cfg, entry, "", "")
		return agent.NewTaskToolWithOptions(agent.TaskToolOptions{
			Provider:            execProv,
			Pricing:             entry.Price,
			ParentRegistry:      reg,
			MaxSteps:            maxSteps,
			ContextWindow:       entry.ContextWindow,
			RecentKeep:          cfg.Agent.RecentKeep,
			SoftCompactRatio:    cfg.Agent.SoftCompactRatio,
			ToolResultSnipRatio: cfg.Agent.ToolResultSnipRatio,
			CompactRatio:        cfg.Agent.CompactRatio,
			CompactForceRatio:   cfg.Agent.CompactForceRatio,
			Temperature:         cfg.Agent.Temperature,
			ArchiveDir:          config.ArchiveDir(),
			SysPrompt:           "",
			Gate:                headlessGate,
			KeepPolicy:          keepPolicy,
			SubagentModel:       taskModel,
			SubagentEffort:      taskEffort,
			ResolveProvider:     resolveSubagentProvider,
			PromptCacheKeyMode:  taskPC.PromptCacheKeyMode,
			PromptCacheKeyValue: taskPC.PromptCacheKeyValue,
			ProviderKind:        taskPC.ProviderKind,
			ProviderBaseURL:     taskPC.ProviderBaseURL,
		}).
			WithTranscripts(subagentStore, root, modelName, entry.Effort).
			WithTranscriptIdentityResolver(subagentIdentity).
			WithMaxSubagentDepth(maxSubagentDepth).
			WithDeliveryProfile(policy.Completion == runtimepolicy.CompletionVerified).
			WithWorkspaceLease(workspaceLease).
			WithScheduler(subagentScheduler).
			WithProfileLookup(profileLookup).
			WithProfileConfigResolvers(profileConfigModel, profileConfigEffort).
			WithBashSandboxEnforced(bashSandboxEnforced).
			WithCapabilityRuntime(capRuntime)
	}
	addTaskTool := func() string {
		if taskToolAdded {
			return "task tool is already enabled."
		}
		taskToolAdded = true
		if taskTool == nil {
			taskTool = newTaskTool()
		}
		// Fixed registration order for prompt-cache stability: task →
		// parallel_tasks → fleet. Profile names never enter tool schemas.
		reg.Add(taskTool)
		reg.Add(agent.NewParallelTasksTool(taskTool, reg))
		reg.Add(agent.NewFleetTool(taskTool))
		return "enabled task."
	}
	addReadOnlyTaskTool := func() string {
		if readOnlyTaskToolAdded {
			return "read_only_task tool is already enabled."
		}
		readOnlyTaskToolAdded = true
		if taskTool == nil {
			taskTool = newTaskTool()
		}
		reg.Add(agent.NewReadOnlyTaskTool(taskTool))
		return "enabled read_only_task."
	}
	if policy.Exposure == runtimepolicy.ExposureEager {
		addTaskTool()
		addReadOnlyTaskTool()
	}

	capRuntimeGet := func() *agent.MCPCapabilityRuntime { return capRuntime }
	capRuntimeSet := func(rt *agent.MCPCapabilityRuntime) { capRuntime = rt }
	return &subagentResult{
		resolveSubagentProvider: resolveSubagentProvider,
		subagentIdentity:        subagentIdentity,
		maxSubagentDepth:        maxSubagentDepth,
		subagentScheduler:       subagentScheduler,
		addTaskTool:             addTaskTool,
		addReadOnlyTaskTool:     addReadOnlyTaskTool,
		taskTool:                taskTool,
		capRuntimeGet:           capRuntimeGet,
		capRuntimeSet:           capRuntimeSet,
	}, nil
}

func subagentModelRef(cfg *config.Config, sk skill.Skill) string {
	if cfg != nil {
		for _, key := range SubagentModelKeys(sk.Name) {
			if m := strings.TrimSpace(cfg.Agent.SubagentModels[key]); m != "" {
				return m
			}
		}
	}
	if m := strings.TrimSpace(sk.Model); m != "" {
		return m
	}
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Agent.SubagentModel)
}

func subagentEffortRef(cfg *config.Config, sk skill.Skill) string {
	if cfg != nil {
		for _, key := range SubagentModelKeys(sk.Name) {
			if e := strings.TrimSpace(cfg.Agent.SubagentEfforts[key]); e != "" {
				return e
			}
		}
	}
	if e := strings.TrimSpace(sk.Effort); e != "" {
		return e
	}
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Agent.SubagentEffort)
}

// SubagentModelKeys returns the cfg.Agent.SubagentModels/SubagentEfforts map
// keys that resolve for a subagent name, in precedence order: the exact name
// first, then its underscore/hyphen alias variants (the dedicated tool
// security_review dispatches the skill security-review, so either spelling in
// config must reach it). Any surface that reads OR clears these maps must
// iterate this same key set — an exact-key delete leaves an alias entry
// silently active.

// SubagentModelKeys returns the cfg.Agent.SubagentModels/SubagentEfforts map
// keys that resolve for a subagent name, in precedence order: the exact name
// first, then its underscore/hyphen alias variants (the dedicated tool
// security_review dispatches the skill security-review, so either spelling in
// config must reach it). Any surface that reads OR clears these maps must
// iterate this same key set — an exact-key delete leaves an alias entry
// silently active.
func SubagentModelKeys(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	keys := []string{name}
	for _, alias := range []string{
		strings.ReplaceAll(name, "-", "_"),
		strings.ReplaceAll(name, "_", "-"),
	} {
		if alias == "" {
			continue
		}
		seen := false
		for _, key := range keys {
			if key == alias {
				seen = true
				break
			}
		}
		if !seen {
			keys = append(keys, alias)
		}
	}
	return keys
}

func newSubagentStore(sessionDir string) (*agent.SubagentStore, error) {
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return nil, nil
	}
	store := agent.NewSubagentStore(store.SubagentDir(sessionDir))
	if _, err := store.CleanupStaleRunning(); err != nil {
		return nil, fmt.Errorf("cleanup stale subagents: %w", err)
	}
	return store, nil
}

func subagentEffectiveIdentity(cfg *config.Config, resolver provider.Resolver, baseModelRef string, base *config.ProviderEntry, modelRef, effort string) (string, string) {
	var entry config.ProviderEntry
	if base != nil {
		entry = *base
	}
	ref := strings.TrimSpace(modelRef)
	explicit := ref != ""
	if !explicit {
		ref = strings.TrimSpace(baseModelRef)
	}
	if explicit && cfg != nil && ref != "" {
		if resolved, ok := cfg.ResolveModel(ref); ok {
			entry = *resolved
		} else if resolved := syntheticEntryFromResolver(resolver, ref); strings.TrimSpace(resolved.Name) != "" {
			entry = *resolved
		} else {
			entry.Model = ref
		}
	} else if explicit {
		if resolved := syntheticEntryFromResolver(resolver, ref); strings.TrimSpace(resolved.Name) != "" {
			entry = *resolved
		} else {
			entry.Model = ref
		}
	} else if base == nil && ref != "" {
		if resolved := syntheticEntryFromResolver(resolver, ref); strings.TrimSpace(resolved.Name) != "" {
			entry = *resolved
		} else if cfg != nil {
			if resolved, ok := cfg.ResolveModel(ref); ok {
				entry = *resolved
			}
		}
	}
	if rawEffort := strings.TrimSpace(effort); rawEffort != "" {
		if normalized, err := config.NormalizeEffort(&entry, rawEffort); err == nil {
			entry.Effort = normalized
		} else {
			entry.Effort = rawEffort
		}
	}
	modelID := strings.TrimSpace(entry.Name)
	model := strings.TrimSpace(entry.Model)
	if modelID != "" && model != "" {
		modelID += "/" + model
	} else if model != "" {
		modelID = model
	} else if modelID == "" {
		modelID = ref
	}
	return modelID, strings.TrimSpace(config.EffectiveEffort(&entry))
}

// NewProvider builds a provider.Provider from a configured entry. Exported so
// custom assemblers (e.g. the ACP per-session factory) can reuse it without
// going through the full Build.
