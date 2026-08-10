package boot

import (
	"context"
	"io"
	"runtime"
	"strconv"
	"strings"

	"corvus/internal/capability"
	"corvus/internal/config"
	"corvus/internal/environment"
	"corvus/internal/event"
	"corvus/internal/instruction"
	"corvus/internal/memory"
	"corvus/internal/outputstyle"
	"corvus/internal/sandbox"
	"corvus/internal/skill"
)

type promptResult struct {
	sysPrompt     string
	mem           *memory.Set
	projectChecks []instruction.VerifyCheck
	skillStore    *skill.Store
	skills        []skill.Skill
	allSkillStore *skill.Store
	allSkills     []skill.Skill
}

// buildPromptAndMemory assembles the cache-stable system-prompt prefix:
// base prompt, output style, policy blocks, environment probe section, memory
// compose, and the skills index.
func buildPromptAndMemory(ctx context.Context, cfg *config.Config, opts Options, root string, shell sandbox.Shell, sink event.Sink, tokenEconomy, tokenDelivery bool, runtimeProfile capability.Profile) (*promptResult, error) {
	sysPrompt, err := cfg.ResolveSystemPromptForRoot(root)
	if err != nil {
		if !config.IsMissingSystemPromptFile(err) {
			return nil, err
		}
		// A stale missing prompt file must not block startup: warn and fall back
		// to the inline (or built-in default) system prompt. Other read failures
		// stay fatal so Corvus never runs without explicitly configured policy.
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: err.Error() + "; falling back to inline/default system prompt"})
		sysPrompt = cfg.InlineSystemPrompt()
	}
	// Output style: fold the selected persona/tone block into the base prompt
	// before language/memory/skills append, so a "replace" style (keep-coding
	// false) still keeps those. Applied once, into the cache-stable prefix.
	if st, ok := outputstyle.Resolve(cfg.Agent.OutputStyle, outputstyle.Dirs()); ok {
		sysPrompt = outputstyle.Apply(sysPrompt, st)
	}
	sysPrompt += "\n\n" + config.UserDecisionPolicy
	sysPrompt += "\n\n" + config.LanguagePolicy
	if workspaceLine := currentWorkspacePromptLine(root); workspaceLine != "" {
		sysPrompt += "\n\n" + workspaceLine
	}
	if tokenEconomy {
		sysPrompt += "\n\n" + tokenEconomyPrompt
	} else if tokenDelivery {
		sysPrompt += "\n\n" + tokenDeliveryPrompt
	}
	if cfg.EnvironmentEnabled() {
		shellLabel := shell.Kind.String()
		if strings.TrimSpace(cfg.Tools.Shell.Path) != "" {
			shellLabel = shell.Path
		}
		envSection := environment.FormatSection(
			environment.RunProbesWithOptions(ctx, environment.DefaultProbes(), environment.ProbeOptions{
				Overrides: cfg.Environment.Tools,
				DenyRoots: []string{root},
				// Persist probe results across restarts: the section below sits
				// inside the provider-cached prompt prefix, and re-observing
				// per boot let transient probe flaps (timeouts, PATH drift)
				// rewrite the prefix and cold-start every session's cache.
				SnapshotDir: config.CacheDir(),
			}),
			runtime.GOOS+"/"+runtime.GOARCH,
			shellLabel,
			cfg.Environment.Tools,
		)
		if envSection != "" {
			sysPrompt += "\n\n" + envSection
		}
	}

	// Persistent memory (CORVUS.md / AGENTS.md hierarchy + auto-memory index)
	// folds into the system prompt exactly here, once: it becomes part of the
	// durable, cache-stable prefix every turn reuses, so memory costs nothing per
	// turn. Mid-session changes never touch this prefix — they ride the
	// controller's transient turn-injection and fold in on the next session.
	if _, err := memory.StoreFor(config.MemoryUserDir(), root).MigrateV2(); err != nil {
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Memory metadata migration did not complete.", Detail: err.Error()})
	}
	mem := memory.Load(memory.Options{CWD: root, UserDir: config.MemoryUserDir()})
	projectChecks := instruction.ExtractHostChecks(mem.Docs)
	sysPrompt = memory.Compose(sysPrompt, mem)

	// Skills: discover playbooks (built-in + project/custom/global) and fold their
	// one-liner index into the same cache-stable prefix — names + descriptions
	// only; bodies load on demand via run_skill or "/<name>". Bodies never enter
	// the prefix, so the index costs a fixed, small amount per turn.
	skillStore := skill.New(skill.Options{
		ProjectRoot:      root,
		CustomPaths:      cfg.SkillCustomPaths(),
		PluginPaths:      cfg.PluginPackageSkillOwners(),
		PluginAgentPaths: cfg.PluginPackageAgentOwners(),
		ExcludedPaths:    cfg.SkillExcludedPaths(),
		DisabledNames:    cfg.DisabledSkillNames(),
		MaxDepth:         cfg.SkillMaxDepth(),
		Stderr:           opts.Stderr,
	})
	// Install the static profile filter before building the prompt index and
	// dedicated skill tools. The dependency checker is attached once the live
	// registry/plugin host has been assembled below.
	skillStore.ConfigureInvocationPolicy(string(runtimeProfile), nil)
	skills := skillStore.List()
	allSkillStore := skill.New(skill.Options{ProjectRoot: root, CustomPaths: cfg.SkillCustomPaths(), PluginPaths: cfg.PluginPackageSkillOwners(), PluginAgentPaths: cfg.PluginPackageAgentOwners(), ExcludedPaths: cfg.SkillExcludedPaths(), MaxDepth: cfg.SkillMaxDepth(), Stderr: io.Discard})
	allSkills := allSkillStore.List()
	if !tokenEconomy {
		sysPrompt = skill.ApplyIndex(sysPrompt, skills)
	}

	return &promptResult{
		sysPrompt:     sysPrompt,
		mem:           mem,
		projectChecks: projectChecks,
		skillStore:    skillStore,
		skills:        skills,
		allSkillStore: allSkillStore,
		allSkills:     allSkills,
	}, nil
}

func currentWorkspacePromptLine(root string) string {
	if root == "" {
		return ""
	}
	return "Current workspace: " + strconv.Quote(root)
}
