package boot

import (
	"fmt"
	"strings"

	"corvus/internal/config"
	"corvus/internal/runtimepolicy"
)

// resolveRuntimePolicy resolves the runtime policy from user selections,
// model capability metadata, and effective effort.
func resolveRuntimePolicy(req runtimepolicy.Request, entry *config.ProviderEntry) (runtimepolicy.Policy, error) {
	capability, err := resolveCapabilityTier(entry)
	if err != nil {
		return runtimepolicy.Policy{}, err
	}
	effort := resolveEffortBand(entry)
	return runtimepolicy.Resolve(runtimepolicy.Input{
		Request:        req,
		CapabilityTier: capability,
		EffortBand:     effort,
	})
}

// assembleRuntimeRequest overlays config defaults, the TokenMode preset, and
// typed axis selections. Non-empty typed fields are authoritative.
func assembleRuntimeRequest(opts Options, cfg *config.Config) (runtimepolicy.Request, error) {
	preset := normalizeTokenModeToPreset(NormalizeTokenMode(opts.TokenMode))
	req := runtimepolicy.InheritRequest(preset)
	if cfg != nil {
		overlay, err := requestFromConfig(cfg.RuntimePolicy)
		if err != nil {
			return runtimepolicy.Request{}, err
		}
		req = runtimepolicy.OverlayRequest(req, overlay)
		req.Preset = preset
	}
	if opts.Guidance != "" {
		guidance, err := runtimepolicy.ParseGuidanceSelection(opts.Guidance)
		if err != nil {
			return runtimepolicy.Request{}, err
		}
		req.Guidance = guidance
	}
	if opts.Completion != "" {
		completion, err := runtimepolicy.ParseCompletionSelection(opts.Completion)
		if err != nil {
			return runtimepolicy.Request{}, err
		}
		req.Completion = completion
	}
	if opts.Exposure != "" {
		exposure, err := runtimepolicy.ParseExposureSelection(opts.Exposure)
		if err != nil {
			return runtimepolicy.Request{}, err
		}
		req.Exposure = exposure
	}
	return req, nil
}

func requestFromConfig(cfg config.RuntimePolicyConfig) (runtimepolicy.Request, error) {
	var req runtimepolicy.Request
	if strings.TrimSpace(cfg.Guidance) != "" {
		guidance, err := runtimepolicy.ParseGuidanceSelection(cfg.Guidance)
		if err != nil {
			return runtimepolicy.Request{}, fmt.Errorf("runtime_policy.guidance: %w", err)
		}
		req.Guidance = guidance
	}
	if strings.TrimSpace(cfg.Completion) != "" {
		completion, err := runtimepolicy.ParseCompletionSelection(cfg.Completion)
		if err != nil {
			return runtimepolicy.Request{}, fmt.Errorf("runtime_policy.completion: %w", err)
		}
		req.Completion = completion
	}
	if strings.TrimSpace(cfg.Exposure) != "" {
		exposure, err := runtimepolicy.ParseExposureSelection(cfg.Exposure)
		if err != nil {
			return runtimepolicy.Request{}, fmt.Errorf("runtime_policy.exposure: %w", err)
		}
		req.Exposure = exposure
	}
	return req, nil
}

// resolveCapabilityTier extracts the capability tier from provider configuration.
func resolveCapabilityTier(entry *config.ProviderEntry) (runtimepolicy.CapabilityTier, error) {
	if entry == nil {
		return runtimepolicy.CapabilityStandard, nil
	}
	if entry.ModelOverrides != nil {
		if override, ok := entry.ModelOverrides[entry.Model]; ok {
			tier, err := parseCapabilityTier(override.ModelCapability)
			if err != nil {
				return "", fmt.Errorf("model override %q model_capability: %w", entry.Model, err)
			}
			if tier != "" {
				return tier, nil
			}
		}
	}
	tier, err := parseCapabilityTier(entry.ModelCapability)
	if err != nil {
		return "", fmt.Errorf("model_capability: %w", err)
	}
	if tier != "" {
		return tier, nil
	}
	return runtimepolicy.CapabilityStandard, nil
}

func parseCapabilityTier(raw string) (runtimepolicy.CapabilityTier, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "", nil
	case "strong":
		return runtimepolicy.CapabilityStrong, nil
	case "standard":
		return runtimepolicy.CapabilityStandard, nil
	case "lite":
		return runtimepolicy.CapabilityLite, nil
	default:
		return "", fmt.Errorf("invalid model_capability %q; must be auto|strong|standard|lite", raw)
	}
}

// resolveEffortBand extracts the effort band from provider configuration.
func resolveEffortBand(entry *config.ProviderEntry) runtimepolicy.EffortBand {
	if entry == nil {
		return runtimepolicy.EffortUnknown
	}

	effort := config.EffectiveEffort(entry)
	return normalizeEffortBand(effort)
}

// normalizeEffortBand maps config effort strings to runtimepolicy types.
func normalizeEffortBand(raw string) runtimepolicy.EffortBand {
	switch raw {
	case "disabled":
		return runtimepolicy.EffortDisabled
	case "low":
		return runtimepolicy.EffortLow
	case "medium":
		return runtimepolicy.EffortMedium
	case "high":
		return runtimepolicy.EffortHigh
	case "xhigh":
		return runtimepolicy.EffortXHigh
	case "max":
		return runtimepolicy.EffortMax
	default:
		return runtimepolicy.EffortUnknown
	}
}

// normalizeTokenModeToPreset converts legacy TokenMode string to Preset type.
func normalizeTokenModeToPreset(mode string) runtimepolicy.Preset {
	switch mode {
	case TokenModeEconomy:
		return runtimepolicy.PresetEconomy
	case TokenModeDelivery:
		return runtimepolicy.PresetDelivery
	default:
		return runtimepolicy.PresetFull
	}
}
