package runtimepolicy

import (
	"fmt"
	"strings"
)

// RecordVersion is the persisted runtime-policy metadata version.
const RecordVersion = 1

// Record is the versioned session-sidecar selection. Preset is decode-only
// compatibility for records written before the three-axis policy existed.
type Record struct {
	Version    int    `json:"version"`
	Preset     string `json:"preset,omitempty"`
	Guidance   string `json:"guidance,omitempty"`
	Completion string `json:"completion,omitempty"`
	Exposure   string `json:"exposure,omitempty"`
}

// Guidance prompt fragments. Exact wording is part of the V5 contract.
const (
	GuidanceLightPrompt      = "inspect relevant context and choose a short plan before acting."
	GuidanceStructuredPrompt = "inspect context, state small steps, work one step at a time, and revisit the plan when evidence changes."
)

// GuidancePrompt returns the model-visible guidance fragment, or empty for off.
func GuidancePrompt(g Guidance) string {
	switch g {
	case GuidanceLight:
		return GuidanceLightPrompt
	case GuidanceStructured:
		return GuidanceStructuredPrompt
	default:
		return ""
	}
}

// ParseGuidanceSelection normalizes a user/config/CLI guidance value.
// Empty becomes inherit so omitted fields keep the preset mapping.
func ParseGuidanceSelection(raw string) (GuidanceSelection, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "", string(GuidanceSelectionInherit):
		return GuidanceSelectionInherit, nil
	case string(GuidanceSelectionAuto), string(GuidanceSelectionOff), string(GuidanceSelectionLight), string(GuidanceSelectionStructured):
		return GuidanceSelection(s), nil
	default:
		return "", fmt.Errorf("invalid guidance selection %q; must be inherit|auto|off|light|structured", raw)
	}
}

// ParseCompletionSelection normalizes a user/config/CLI completion value.
func ParseCompletionSelection(raw string) (CompletionSelection, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "", string(CompletionSelectionInherit):
		return CompletionSelectionInherit, nil
	case string(CompletionSelectionAuto), string(CompletionSelectionStandard), string(CompletionSelectionVerified):
		return CompletionSelection(s), nil
	default:
		return "", fmt.Errorf("invalid completion selection %q; must be inherit|auto|standard|verified", raw)
	}
}

// ParseExposureSelection normalizes a user/config/CLI exposure value.
func ParseExposureSelection(raw string) (ExposureSelection, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "", string(ExposureSelectionInherit):
		return ExposureSelectionInherit, nil
	case string(ExposureSelectionAuto), string(ExposureSelectionEager), string(ExposureSelectionDeferred):
		return ExposureSelection(s), nil
	default:
		return "", fmt.Errorf("invalid exposure selection %q; must be inherit|auto|eager|deferred", raw)
	}
}

// ParsePreset accepts the canonical persisted presets only. Alias folding
// (balanced, eco, quality) belongs at the TokenMode compatibility boundary.
func ParsePreset(raw string) (Preset, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "":
		return "", nil
	case string(PresetFull):
		return PresetFull, nil
	case string(PresetEconomy), string(PresetDelivery):
		return Preset(s), nil
	default:
		return "", fmt.Errorf("invalid runtime preset %q; must be full|economy|delivery", raw)
	}
}

// OverlayRequest applies non-empty overlay fields onto base. Empty overlay
// fields leave the corresponding base value unchanged.
func OverlayRequest(base Request, overlay Request) Request {
	if overlay.Preset != "" {
		base.Preset = overlay.Preset
	}
	if overlay.Guidance != "" {
		base.Guidance = overlay.Guidance
	}
	if overlay.Completion != "" {
		base.Completion = overlay.Completion
	}
	if overlay.Exposure != "" {
		base.Exposure = overlay.Exposure
	}
	return base
}

// InheritRequest returns a request that takes every axis from the preset.
func InheritRequest(preset Preset) Request {
	return Request{
		Preset:     preset,
		Guidance:   GuidanceSelectionInherit,
		Completion: CompletionSelectionInherit,
		Exposure:   ExposureSelectionInherit,
	}
}

// RequestFromRecord reconstructs a resolver request from persisted metadata.
func RequestFromRecord(rec Record) (Request, error) {
	if migrated, _, err := MigrateRecord(rec); err != nil {
		return Request{}, err
	} else {
		rec = migrated
	}
	preset, err := ParsePreset(rec.Preset)
	if err != nil {
		return Request{}, err
	}
	guidance, err := ParseGuidanceSelection(rec.Guidance)
	if err != nil {
		return Request{}, err
	}
	completion, err := ParseCompletionSelection(rec.Completion)
	if err != nil {
		return Request{}, err
	}
	exposure, err := ParseExposureSelection(rec.Exposure)
	if err != nil {
		return Request{}, err
	}
	return Request{
		Preset:     preset,
		Guidance:   guidance,
		Completion: completion,
		Exposure:   exposure,
	}, nil
}

// MigrateRecord converts the old preset field into explicit axis selections.
// The returned record is canonical: it never carries Preset, so callers can
// persist it back once and stop propagating the retired work-mode vocabulary.
func MigrateRecord(rec Record) (Record, bool, error) {
	if strings.TrimSpace(rec.Preset) == "" {
		return rec, false, nil
	}
	preset, err := ParsePreset(rec.Preset)
	if err != nil {
		// Some early session writers stored the UI alias directly. It is
		// equivalent to the ordinary new-session defaults and carries no axis.
		if strings.EqualFold(strings.TrimSpace(rec.Preset), "balanced") {
			preset = PresetFull
		} else {
			return Record{}, false, err
		}
	}
	if rec.Guidance == "" {
		rec.Guidance = string(GuidanceSelectionInherit)
	}
	if rec.Completion == "" {
		if preset == PresetDelivery {
			rec.Completion = string(CompletionSelectionVerified)
		} else {
			rec.Completion = string(CompletionSelectionStandard)
		}
	}
	if rec.Exposure == "" {
		if preset == PresetEconomy {
			rec.Exposure = string(ExposureSelectionDeferred)
		} else {
			rec.Exposure = string(ExposureSelectionEager)
		}
	}
	rec.Preset = ""
	if rec.Version == 0 {
		rec.Version = RecordVersion
	}
	return rec, true, nil
}

// RecordFromRequest stores the request selections. Resolved values are not
// persisted; they are re-derived from the request plus current model/effort.
func RecordFromRequest(req Request) Record {
	return Record{
		Version:    RecordVersion,
		Guidance:   string(orInheritGuidance(req.Guidance)),
		Completion: string(orInheritCompletion(req.Completion)),
		Exposure:   string(orInheritExposure(req.Exposure)),
	}
}

// RecordFromTokenMode migrates a legacy token_mode sidecar value.
func RecordFromTokenMode(mode string) Record {
	rec := Record{Version: RecordVersion}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case string(PresetEconomy):
		rec.Exposure = string(ExposureSelectionDeferred)
	case string(PresetDelivery):
		rec.Completion = string(CompletionSelectionVerified)
	}
	if rec.Guidance == "" {
		rec.Guidance = string(GuidanceSelectionAuto)
	}
	if rec.Completion == "" {
		rec.Completion = string(CompletionSelectionStandard)
	}
	if rec.Exposure == "" {
		rec.Exposure = string(ExposureSelectionEager)
	}
	return rec
}

func orInheritGuidance(s GuidanceSelection) GuidanceSelection {
	if s == "" {
		return GuidanceSelectionInherit
	}
	return s
}

func orInheritCompletion(s CompletionSelection) CompletionSelection {
	if s == "" {
		return CompletionSelectionInherit
	}
	return s
}

func orInheritExposure(s ExposureSelection) ExposureSelection {
	if s == "" {
		return ExposureSelectionInherit
	}
	return s
}
