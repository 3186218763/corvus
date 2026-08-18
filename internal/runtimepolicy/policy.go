// Package runtimepolicy resolves explicit runtime policy from user selections,
// model capability metadata, and effective effort. It is a pure resolver with no
// filesystem, network, or model-ID heuristics.
package runtimepolicy

import (
	"fmt"
	"strings"
)

// Guidance is model-visible cognitive scaffolding.
type Guidance string

const (
	GuidanceOff        Guidance = "off"
	GuidanceLight      Guidance = "light"
	GuidanceStructured Guidance = "structured"
)

// Completion is the host's acceptance contract.
type Completion string

const (
	CompletionStandard Completion = "standard"
	CompletionVerified Completion = "verified"
)

// Exposure is the initial tool surface.
type Exposure string

const (
	ExposureEager    Exposure = "eager"
	ExposureDeferred Exposure = "deferred"
)

// GuidanceSelection is a user request value; inherit and auto are valid inputs.
type GuidanceSelection string

const (
	GuidanceSelectionInherit    GuidanceSelection = "inherit"
	GuidanceSelectionAuto       GuidanceSelection = "auto"
	GuidanceSelectionOff        GuidanceSelection = "off"
	GuidanceSelectionLight      GuidanceSelection = "light"
	GuidanceSelectionStructured GuidanceSelection = "structured"
)

// CompletionSelection is a user request value; inherit and auto are valid inputs.
type CompletionSelection string

const (
	CompletionSelectionInherit  CompletionSelection = "inherit"
	CompletionSelectionAuto     CompletionSelection = "auto"
	CompletionSelectionStandard CompletionSelection = "standard"
	CompletionSelectionVerified CompletionSelection = "verified"
)

// ExposureSelection is a user request value; inherit and auto are valid inputs.
type ExposureSelection string

const (
	ExposureSelectionInherit  ExposureSelection = "inherit"
	ExposureSelectionAuto     ExposureSelection = "auto"
	ExposureSelectionEager    ExposureSelection = "eager"
	ExposureSelectionDeferred ExposureSelection = "deferred"
)

// Preset is the legacy TokenMode compatibility input.
type Preset string

const (
	PresetFull     Preset = "full"
	PresetEconomy  Preset = "economy"
	PresetDelivery Preset = "delivery"
)

// Request is the input to the resolver.
type Request struct {
	Preset     Preset
	Guidance   GuidanceSelection
	Completion CompletionSelection
	Exposure   ExposureSelection
}

// Policy is the resolved runtime policy.
type Policy struct {
	Guidance   Guidance
	Completion Completion
	Exposure   Exposure

	// Derived compatibility decisions; not additional user axes.
	LegacyPreset       Preset
	LegacySkillProfile string
	PlannerEligible    bool
	CapabilityFrontend string
	WorkspaceLease     bool
}

// CapabilityTier describes a model's inherent capacity.
type CapabilityTier string

const (
	CapabilityAuto     CapabilityTier = "auto"
	CapabilityStrong   CapabilityTier = "strong"
	CapabilityStandard CapabilityTier = "standard"
	CapabilityLite     CapabilityTier = "lite"
)

// EffortBand is the normalized effort level after provider-specific resolution.
type EffortBand string

const (
	EffortUnknown  EffortBand = "unknown"
	EffortDisabled EffortBand = "disabled"
	EffortLow      EffortBand = "low"
	EffortMedium   EffortBand = "medium"
	EffortHigh     EffortBand = "high"
	EffortXHigh    EffortBand = "xhigh"
	EffortMax      EffortBand = "max"
)

// Input contains everything the resolver needs.
type Input struct {
	Request        Request
	CapabilityTier CapabilityTier
	EffortBand     EffortBand
}

// Resolve deterministically computes policy from the input.
func Resolve(input Input) (Policy, error) {
	// Normalize capability tier
	capability := input.CapabilityTier
	if capability == "" || capability == CapabilityAuto {
		capability = CapabilityStandard
	}
	if !isValidCapability(capability) {
		return Policy{}, fmt.Errorf("invalid capability tier %q; must be auto|strong|standard|lite", input.CapabilityTier)
	}

	// Normalize effort band
	effort := input.EffortBand
	if effort == "" {
		effort = EffortUnknown
	}
	if !isValidEffort(effort) {
		return Policy{}, fmt.Errorf("invalid effort band %q", input.EffortBand)
	}

	// Resolve each axis
	guidance, err := resolveGuidance(input.Request, capability, effort)
	if err != nil {
		return Policy{}, err
	}

	completion, err := resolveCompletion(input.Request)
	if err != nil {
		return Policy{}, err
	}

	exposure, err := resolveExposure(input.Request)
	if err != nil {
		return Policy{}, err
	}

	// Derive compatibility fields
	preset := input.Request.Preset
	if preset == "" {
		preset = PresetFull
	}

	skillProfile := "default"
	if preset == PresetEconomy {
		skillProfile = "economy"
	}

	plannerEligible := true
	if preset == PresetEconomy {
		plannerEligible = false
	}

	capabilityFrontend := ""
	if completion == CompletionVerified {
		capabilityFrontend = "delivery"
	}

	workspaceLease := completion == CompletionVerified

	return Policy{
		Guidance:           guidance,
		Completion:         completion,
		Exposure:           exposure,
		LegacyPreset:       preset,
		LegacySkillProfile: skillProfile,
		PlannerEligible:    plannerEligible,
		CapabilityFrontend: capabilityFrontend,
		WorkspaceLease:     workspaceLease,
	}, nil
}

func resolveGuidance(req Request, capability CapabilityTier, effort EffortBand) (Guidance, error) {
	sel := normalizeGuidanceSelection(req.Guidance)

	// Explicit concrete selection wins
	switch sel {
	case GuidanceSelectionOff:
		return GuidanceOff, nil
	case GuidanceSelectionLight:
		return GuidanceLight, nil
	case GuidanceSelectionStructured:
		return GuidanceStructured, nil
	case GuidanceSelectionAuto:
		return automaticGuidance(capability, effort), nil
	case GuidanceSelectionInherit:
		return presetGuidance(req.Preset), nil
	}

	// Empty input defaults to inherit
	if req.Guidance == "" || strings.TrimSpace(string(req.Guidance)) == "" {
		return presetGuidance(req.Preset), nil
	}

	// Non-empty input that didn't normalize is invalid
	return "", fmt.Errorf("invalid guidance selection %q", req.Guidance)
}

func resolveCompletion(req Request) (Completion, error) {
	sel := normalizeCompletionSelection(req.Completion)

	// Explicit concrete selection wins
	switch sel {
	case CompletionSelectionStandard:
		return CompletionStandard, nil
	case CompletionSelectionVerified:
		return CompletionVerified, nil
	case CompletionSelectionAuto:
		return CompletionStandard, nil
	case CompletionSelectionInherit:
		return presetCompletion(req.Preset), nil
	}

	// Empty input defaults to inherit
	if req.Completion == "" || strings.TrimSpace(string(req.Completion)) == "" {
		return presetCompletion(req.Preset), nil
	}

	// Non-empty input that didn't normalize is invalid
	return "", fmt.Errorf("invalid completion selection %q", req.Completion)
}

func resolveExposure(req Request) (Exposure, error) {
	sel := normalizeExposureSelection(req.Exposure)

	// Explicit concrete selection wins
	switch sel {
	case ExposureSelectionEager:
		return ExposureEager, nil
	case ExposureSelectionDeferred:
		return ExposureDeferred, nil
	case ExposureSelectionAuto:
		return ExposureEager, nil
	case ExposureSelectionInherit:
		return presetExposure(req.Preset), nil
	}

	// Empty input defaults to inherit
	if req.Exposure == "" || strings.TrimSpace(string(req.Exposure)) == "" {
		return presetExposure(req.Preset), nil
	}

	// Non-empty input that didn't normalize is invalid
	return "", fmt.Errorf("invalid exposure selection %q", req.Exposure)
}

// automaticGuidance implements the normative matrix from spec section 5.
func automaticGuidance(capability CapabilityTier, effort EffortBand) Guidance {
	switch capability {
	case CapabilityStrong:
		switch effort {
		case EffortUnknown, EffortLow:
			return GuidanceLight
		case EffortDisabled:
			return GuidanceStructured
		case EffortMedium:
			return GuidanceLight
		case EffortHigh, EffortXHigh, EffortMax:
			return GuidanceOff
		}
	case CapabilityStandard:
		switch effort {
		case EffortUnknown:
			return GuidanceLight
		case EffortDisabled, EffortLow, EffortMedium:
			return GuidanceStructured
		case EffortHigh, EffortXHigh, EffortMax:
			return GuidanceLight
		}
	case CapabilityLite:
		switch effort {
		case EffortMax:
			return GuidanceLight
		default:
			return GuidanceStructured
		}
	}
	// Fallback
	return GuidanceLight
}

// presetGuidance maps legacy presets to guidance.
func presetGuidance(preset Preset) Guidance {
	// All legacy presets use off
	return GuidanceOff
}

// presetCompletion maps legacy presets to completion.
func presetCompletion(preset Preset) Completion {
	if preset == PresetDelivery {
		return CompletionVerified
	}
	return CompletionStandard
}

// presetExposure maps legacy presets to exposure.
func presetExposure(preset Preset) Exposure {
	if preset == PresetEconomy {
		return ExposureDeferred
	}
	return ExposureEager
}

func normalizeGuidanceSelection(raw GuidanceSelection) GuidanceSelection {
	s := GuidanceSelection(strings.ToLower(strings.TrimSpace(string(raw))))
	switch s {
	case GuidanceSelectionInherit, GuidanceSelectionAuto, GuidanceSelectionOff, GuidanceSelectionLight, GuidanceSelectionStructured:
		return s
	}
	return ""
}

func normalizeCompletionSelection(raw CompletionSelection) CompletionSelection {
	s := CompletionSelection(strings.ToLower(strings.TrimSpace(string(raw))))
	switch s {
	case CompletionSelectionInherit, CompletionSelectionAuto, CompletionSelectionStandard, CompletionSelectionVerified:
		return s
	}
	return ""
}

func normalizeExposureSelection(raw ExposureSelection) ExposureSelection {
	s := ExposureSelection(strings.ToLower(strings.TrimSpace(string(raw))))
	switch s {
	case ExposureSelectionInherit, ExposureSelectionAuto, ExposureSelectionEager, ExposureSelectionDeferred:
		return s
	}
	return ""
}

func isValidCapability(c CapabilityTier) bool {
	switch c {
	case CapabilityStrong, CapabilityStandard, CapabilityLite:
		return true
	}
	return false
}

func isValidEffort(e EffortBand) bool {
	switch e {
	case EffortUnknown, EffortDisabled, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	}
	return false
}
