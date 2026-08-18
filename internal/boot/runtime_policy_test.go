package boot

import (
	"testing"

	"corvus/internal/config"
	"corvus/internal/runtimepolicy"
)

func TestResolveCapabilityTier(t *testing.T) {
	tests := []struct {
		name  string
		entry *config.ProviderEntry
		want  runtimepolicy.CapabilityTier
	}{
		{
			name:  "nil entry defaults to standard",
			entry: nil,
			want:  runtimepolicy.CapabilityStandard,
		},
		{
			name: "provider-level strong",
			entry: &config.ProviderEntry{
				ModelCapability: "strong",
			},
			want: runtimepolicy.CapabilityStrong,
		},
		{
			name: "provider-level lite",
			entry: &config.ProviderEntry{
				ModelCapability: "lite",
			},
			want: runtimepolicy.CapabilityLite,
		},
		{
			name: "model override wins",
			entry: &config.ProviderEntry{
				Model:           "advanced-model",
				ModelCapability: "standard",
				ModelOverrides: map[string]config.ProviderModelOverride{
					"advanced-model": {
						ModelCapability: "strong",
					},
				},
			},
			want: runtimepolicy.CapabilityStrong,
		},
		{
			name: "empty/auto defaults to standard",
			entry: &config.ProviderEntry{
				ModelCapability: "",
			},
			want: runtimepolicy.CapabilityStandard,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCapabilityTier(tt.entry)
			if err != nil {
				t.Fatalf("resolveCapabilityTier() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveCapabilityTier() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveEffortBand(t *testing.T) {
	tests := []struct {
		name  string
		entry *config.ProviderEntry
		want  runtimepolicy.EffortBand
	}{
		{
			name:  "nil entry",
			entry: nil,
			want:  runtimepolicy.EffortUnknown,
		},
		{
			name: "explicit low",
			entry: &config.ProviderEntry{
				Effort: "low",
			},
			want: runtimepolicy.EffortLow,
		},
		{
			name: "explicit high",
			entry: &config.ProviderEntry{
				Effort: "high",
			},
			want: runtimepolicy.EffortHigh,
		},
		{
			name: "explicit max",
			entry: &config.ProviderEntry{
				Effort: "max",
			},
			want: runtimepolicy.EffortMax,
		},
		{
			name: "disabled",
			entry: &config.ProviderEntry{
				Effort: "disabled",
			},
			want: runtimepolicy.EffortDisabled,
		},
		{
			name: "empty defaults to unknown",
			entry: &config.ProviderEntry{
				Effort: "",
			},
			want: runtimepolicy.EffortUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveEffortBand(tt.entry)
			if got != tt.want {
				t.Errorf("resolveEffortBand() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveRuntimePolicy_LegacyPresets(t *testing.T) {
	entry := &config.ProviderEntry{
		ModelCapability: "standard",
		Effort:          "",
	}

	tests := []struct {
		name   string
		preset runtimepolicy.Preset
		want   struct {
			guidance   runtimepolicy.Guidance
			completion runtimepolicy.Completion
			exposure   runtimepolicy.Exposure
		}
	}{
		{
			name:   "full preset",
			preset: runtimepolicy.PresetFull,
			want: struct {
				guidance   runtimepolicy.Guidance
				completion runtimepolicy.Completion
				exposure   runtimepolicy.Exposure
			}{
				guidance:   runtimepolicy.GuidanceOff,
				completion: runtimepolicy.CompletionStandard,
				exposure:   runtimepolicy.ExposureEager,
			},
		},
		{
			name:   "economy preset",
			preset: runtimepolicy.PresetEconomy,
			want: struct {
				guidance   runtimepolicy.Guidance
				completion runtimepolicy.Completion
				exposure   runtimepolicy.Exposure
			}{
				guidance:   runtimepolicy.GuidanceOff,
				completion: runtimepolicy.CompletionStandard,
				exposure:   runtimepolicy.ExposureDeferred,
			},
		},
		{
			name:   "delivery preset",
			preset: runtimepolicy.PresetDelivery,
			want: struct {
				guidance   runtimepolicy.Guidance
				completion runtimepolicy.Completion
				exposure   runtimepolicy.Exposure
			}{
				guidance:   runtimepolicy.GuidanceOff,
				completion: runtimepolicy.CompletionVerified,
				exposure:   runtimepolicy.ExposureEager,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := runtimepolicy.Request{
				Preset:     tt.preset,
				Guidance:   runtimepolicy.GuidanceSelectionInherit,
				Completion: runtimepolicy.CompletionSelectionInherit,
				Exposure:   runtimepolicy.ExposureSelectionInherit,
			}

			got, err := resolveRuntimePolicy(req, entry)
			if err != nil {
				t.Fatalf("resolveRuntimePolicy() error = %v", err)
			}

			if got.Guidance != tt.want.guidance {
				t.Errorf("Guidance = %v, want %v", got.Guidance, tt.want.guidance)
			}
			if got.Completion != tt.want.completion {
				t.Errorf("Completion = %v, want %v", got.Completion, tt.want.completion)
			}
			if got.Exposure != tt.want.exposure {
				t.Errorf("Exposure = %v, want %v", got.Exposure, tt.want.exposure)
			}
		})
	}
}

func TestResolveRuntimePolicy_AutomaticGuidance(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		effort     string
		want       runtimepolicy.Guidance
	}{
		{
			name:       "strong + high = off",
			capability: "strong",
			effort:     "high",
			want:       runtimepolicy.GuidanceOff,
		},
		{
			name:       "standard + medium = structured",
			capability: "standard",
			effort:     "medium",
			want:       runtimepolicy.GuidanceStructured,
		},
		{
			name:       "lite + low = structured",
			capability: "lite",
			effort:     "low",
			want:       runtimepolicy.GuidanceStructured,
		},
		{
			name:       "standard + high = light",
			capability: "standard",
			effort:     "high",
			want:       runtimepolicy.GuidanceLight,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &config.ProviderEntry{
				ModelCapability: tt.capability,
				Effort:          tt.effort,
			}

			req := runtimepolicy.Request{
				Preset:     runtimepolicy.PresetFull,
				Guidance:   runtimepolicy.GuidanceSelectionAuto,
				Completion: runtimepolicy.CompletionSelectionInherit,
				Exposure:   runtimepolicy.ExposureSelectionInherit,
			}

			got, err := resolveRuntimePolicy(req, entry)
			if err != nil {
				t.Fatalf("resolveRuntimePolicy() error = %v", err)
			}

			if got.Guidance != tt.want {
				t.Errorf("Guidance = %v, want %v", got.Guidance, tt.want)
			}
		})
	}
}

func TestResolveCapabilityTierRejectsInvalid(t *testing.T) {
	_, err := resolveCapabilityTier(&config.ProviderEntry{ModelCapability: "genius"})
	if err == nil {
		t.Fatal("invalid model_capability should error")
	}
}

func TestAssembleRuntimeRequestTypedAxesWin(t *testing.T) {
	cfg := &config.Config{RuntimePolicy: config.RuntimePolicyConfig{Guidance: "auto"}}
	req, err := assembleRuntimeRequest(Options{
		TokenMode:  TokenModeEconomy,
		Guidance:   "off",
		Completion: "verified",
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if req.Preset != runtimepolicy.PresetEconomy {
		t.Fatalf("preset = %q", req.Preset)
	}
	if req.Guidance != runtimepolicy.GuidanceSelectionOff {
		t.Fatalf("guidance = %q", req.Guidance)
	}
	if req.Completion != runtimepolicy.CompletionSelectionVerified {
		t.Fatalf("completion = %q", req.Completion)
	}
	if req.Exposure != runtimepolicy.ExposureSelectionInherit {
		t.Fatalf("exposure = %q", req.Exposure)
	}
}
