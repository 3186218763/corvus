package boot

import (
	"io"
	"os"
	"strings"

	"corvus/internal/agent"
	"corvus/internal/capability"
	"corvus/internal/config"
	"corvus/internal/secrets"
)

type configResult struct {
	stderr                   io.Writer
	root                     string
	additionalDirs           []string
	cfg                      *config.Config
	modelName                string
	tokenEconomy             bool
	tokenDelivery            bool
	runtimeProfile           capability.Profile
	keepPolicy               agent.KeepPolicy
	entry                    *config.ProviderEntry
	modelRef                 string
	stepLimitsMigrated       bool
	stepLimitMigErr          error
	redactToolOutputMigrated bool
	redactToolOutputMigErr   error
	memoryCompilerMigrated   bool
	memoryCompilerMigErr     error
}

// buildConfigAndModel loads configuration and resolves the session model
// entry. It runs before any sink exists because the legacy-migration calls
// must finish before config.LoadForRoot picks up freshly written files.
func buildConfigAndModel(opts Options) (*configResult, error) {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	root := resolveWorkspaceRoot(opts.WorkspaceRoot)
	additionalDirs, err := normalizeAdditionalDirs(root, opts.AdditionalDirs)
	if err != nil {
		return nil, err
	}
	stepLimitsMigrated, stepLimitMigErr := config.MigrateLegacyAgentStepLimitsForRoot(root)
	redactToolOutputMigrated, redactToolOutputMigErr := config.MigrateLegacyRedactToolOutputForRoot(root)
	memoryCompilerMigrated, memoryCompilerMigErr := config.MigrateLegacyMemoryCompilerForRoot(root)
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return nil, err
	}
	applyRuntimeAutoPricingCurrency(cfg, opts.AutoPricingCurrency)
	// Arm the credential-protection layers from the user-global [secrets]
	// section before any tool, hook, or plugin subprocess can spawn. Package
	// globals are correct here because [secrets] is user-global (project
	// project .corvus/config.toml cannot override it), so concurrent workspaces agree.
	secrets.SetFilterSubprocessEnv(cfg.Secrets.FilterSubprocessEnv)
	secrets.SetProtectSensitiveFiles(cfg.Secrets.ProtectSensitiveFiles)
	secrets.RegisterCredentialEnvKeys(cfg.CredentialEnvNames())
	// Fall through a keyless default_model to the next configured chat model
	// instead of hard-failing every command on "missing env X_API_KEY" (issue
	// #6996). The fallback only kicks in when the caller did not pass an
	// explicit opts.Model; explicit choices still fail loudly.
	modelName := opts.Model
	if modelName == "" {
		if resolved, _, ok := cfg.ResolveNewSessionChatModel(); ok {
			modelName = resolved
		}
	}
	config.NormalizeLegacyMimoCustomProvidersForRefs(cfg, modelName)
	tokenMode := NormalizeTokenMode(opts.TokenMode)
	tokenEconomy := tokenMode == TokenModeEconomy
	tokenDelivery := tokenMode == TokenModeDelivery
	runtimeProfile := capability.ProfileBalanced
	if tokenEconomy {
		runtimeProfile = capability.ProfileEconomy
	} else if tokenDelivery {
		runtimeProfile = capability.ProfileDelivery
	}
	keepPolicy := agentKeepPolicy(cfg.Agent.Keep)
	entry, modelRef, err := resolveModelEntry(opts, cfg, modelName)
	if err != nil {
		return nil, err
	}
	if opts.EffortOverride != nil {
		entry.Effort = *opts.EffortOverride
		if entry.Kind == "anthropic" && strings.TrimSpace(entry.Effort) != "" && strings.TrimSpace(entry.Thinking) == "" {
			entry.Thinking = "adaptive"
		}
	}
	if opts.RequireKey && opts.ProviderResolver == nil {
		if err := cfg.Validate(modelName); err != nil {
			return nil, err
		}
	}

	return &configResult{
		stderr:                   stderr,
		root:                     root,
		additionalDirs:           additionalDirs,
		cfg:                      cfg,
		modelName:                modelName,
		tokenEconomy:             tokenEconomy,
		tokenDelivery:            tokenDelivery,
		runtimeProfile:           runtimeProfile,
		keepPolicy:               keepPolicy,
		entry:                    entry,
		modelRef:                 modelRef,
		stepLimitsMigrated:       stepLimitsMigrated,
		stepLimitMigErr:          stepLimitMigErr,
		redactToolOutputMigrated: redactToolOutputMigrated,
		redactToolOutputMigErr:   redactToolOutputMigErr,
		memoryCompilerMigrated:   memoryCompilerMigrated,
		memoryCompilerMigErr:     memoryCompilerMigErr,
	}, nil
}

func applyRuntimeAutoPricingCurrency(cfg *config.Config, currency string) {
	if cfg != nil {
		cfg.ApplyRuntimeAutoPricingCurrency(currency)
	}
}
