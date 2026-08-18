package runtimepolicy

import (
	"testing"
)

func TestResolve_LegacyPresets(t *testing.T) {
	tests := []struct {
		name   string
		preset Preset
		want   Policy
	}{
		{
			name:   "full preset",
			preset: PresetFull,
			want: Policy{
				Guidance:           GuidanceOff,
				Completion:         CompletionStandard,
				Exposure:           ExposureEager,
				LegacyPreset:       PresetFull,
				LegacySkillProfile: "default",
				PlannerEligible:    true,
				CapabilityFrontend: "",
				WorkspaceLease:     false,
			},
		},
		{
			name:   "economy preset",
			preset: PresetEconomy,
			want: Policy{
				Guidance:           GuidanceOff,
				Completion:         CompletionStandard,
				Exposure:           ExposureDeferred,
				LegacyPreset:       PresetEconomy,
				LegacySkillProfile: "economy",
				PlannerEligible:    false,
				CapabilityFrontend: "",
				WorkspaceLease:     false,
			},
		},
		{
			name:   "delivery preset",
			preset: PresetDelivery,
			want: Policy{
				Guidance:           GuidanceOff,
				Completion:         CompletionVerified,
				Exposure:           ExposureEager,
				LegacyPreset:       PresetDelivery,
				LegacySkillProfile: "default",
				PlannerEligible:    true,
				CapabilityFrontend: "delivery",
				WorkspaceLease:     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := Input{
				Request: Request{
					Preset:     tt.preset,
					Guidance:   GuidanceSelectionInherit,
					Completion: CompletionSelectionInherit,
					Exposure:   ExposureSelectionInherit,
				},
				CapabilityTier: CapabilityStandard,
				EffortBand:     EffortUnknown,
			}

			got, err := Resolve(input)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			if got != tt.want {
				t.Errorf("Resolve() mismatch:\ngot  = %+v\nwant = %+v", got, tt.want)
			}
		})
	}
}

func TestResolve_ExplicitOverrides(t *testing.T) {
	tests := []struct {
		name       string
		request    Request
		wantPolicy Policy
	}{
		{
			name: "explicit off overrides preset",
			request: Request{
				Preset:     PresetFull,
				Guidance:   GuidanceSelectionOff,
				Completion: CompletionSelectionInherit,
				Exposure:   ExposureSelectionInherit,
			},
			wantPolicy: Policy{
				Guidance:           GuidanceOff,
				Completion:         CompletionStandard,
				Exposure:           ExposureEager,
				LegacyPreset:       PresetFull,
				LegacySkillProfile: "default",
				PlannerEligible:    true,
			},
		},
		{
			name: "explicit verified on economy",
			request: Request{
				Preset:     PresetEconomy,
				Guidance:   GuidanceSelectionInherit,
				Completion: CompletionSelectionVerified,
				Exposure:   ExposureSelectionInherit,
			},
			wantPolicy: Policy{
				Guidance:           GuidanceOff,
				Completion:         CompletionVerified,
				Exposure:           ExposureDeferred,
				LegacyPreset:       PresetEconomy,
				LegacySkillProfile: "economy",
				PlannerEligible:    false,
				CapabilityFrontend: "delivery",
				WorkspaceLease:     true,
			},
		},
		{
			name: "auto axes with full preset",
			request: Request{
				Preset:     PresetFull,
				Guidance:   GuidanceSelectionAuto,
				Completion: CompletionSelectionAuto,
				Exposure:   ExposureSelectionAuto,
			},
			wantPolicy: Policy{
				Guidance:           GuidanceLight,
				Completion:         CompletionStandard,
				Exposure:           ExposureEager,
				LegacyPreset:       PresetFull,
				LegacySkillProfile: "default",
				PlannerEligible:    true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := Input{
				Request:        tt.request,
				CapabilityTier: CapabilityStandard,
				EffortBand:     EffortUnknown,
			}

			got, err := Resolve(input)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			if got.Guidance != tt.wantPolicy.Guidance {
				t.Errorf("Guidance = %v, want %v", got.Guidance, tt.wantPolicy.Guidance)
			}
			if got.Completion != tt.wantPolicy.Completion {
				t.Errorf("Completion = %v, want %v", got.Completion, tt.wantPolicy.Completion)
			}
			if got.Exposure != tt.wantPolicy.Exposure {
				t.Errorf("Exposure = %v, want %v", got.Exposure, tt.wantPolicy.Exposure)
			}
			if got.WorkspaceLease != tt.wantPolicy.WorkspaceLease {
				t.Errorf("WorkspaceLease = %v, want %v", got.WorkspaceLease, tt.wantPolicy.WorkspaceLease)
			}
		})
	}
}

func TestResolve_AutomaticGuidanceMatrix(t *testing.T) {
	tests := []struct {
		name       string
		capability CapabilityTier
		effort     EffortBand
		want       Guidance
	}{
		// Strong capability
		{name: "strong/unknown", capability: CapabilityStrong, effort: EffortUnknown, want: GuidanceLight},
		{name: "strong/disabled", capability: CapabilityStrong, effort: EffortDisabled, want: GuidanceStructured},
		{name: "strong/low", capability: CapabilityStrong, effort: EffortLow, want: GuidanceLight},
		{name: "strong/medium", capability: CapabilityStrong, effort: EffortMedium, want: GuidanceLight},
		{name: "strong/high", capability: CapabilityStrong, effort: EffortHigh, want: GuidanceOff},
		{name: "strong/xhigh", capability: CapabilityStrong, effort: EffortXHigh, want: GuidanceOff},
		{name: "strong/max", capability: CapabilityStrong, effort: EffortMax, want: GuidanceOff},

		// Standard capability
		{name: "standard/unknown", capability: CapabilityStandard, effort: EffortUnknown, want: GuidanceLight},
		{name: "standard/disabled", capability: CapabilityStandard, effort: EffortDisabled, want: GuidanceStructured},
		{name: "standard/low", capability: CapabilityStandard, effort: EffortLow, want: GuidanceStructured},
		{name: "standard/medium", capability: CapabilityStandard, effort: EffortMedium, want: GuidanceStructured},
		{name: "standard/high", capability: CapabilityStandard, effort: EffortHigh, want: GuidanceLight},
		{name: "standard/xhigh", capability: CapabilityStandard, effort: EffortXHigh, want: GuidanceLight},
		{name: "standard/max", capability: CapabilityStandard, effort: EffortMax, want: GuidanceLight},

		// Lite capability
		{name: "lite/unknown", capability: CapabilityLite, effort: EffortUnknown, want: GuidanceStructured},
		{name: "lite/disabled", capability: CapabilityLite, effort: EffortDisabled, want: GuidanceStructured},
		{name: "lite/low", capability: CapabilityLite, effort: EffortLow, want: GuidanceStructured},
		{name: "lite/medium", capability: CapabilityLite, effort: EffortMedium, want: GuidanceStructured},
		{name: "lite/high", capability: CapabilityLite, effort: EffortHigh, want: GuidanceStructured},
		{name: "lite/xhigh", capability: CapabilityLite, effort: EffortXHigh, want: GuidanceStructured},
		{name: "lite/max", capability: CapabilityLite, effort: EffortMax, want: GuidanceLight},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := Input{
				Request: Request{
					Preset:     PresetFull,
					Guidance:   GuidanceSelectionAuto,
					Completion: CompletionSelectionInherit,
					Exposure:   ExposureSelectionInherit,
				},
				CapabilityTier: tt.capability,
				EffortBand:     tt.effort,
			}

			got, err := Resolve(input)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			if got.Guidance != tt.want {
				t.Errorf("Guidance = %v, want %v", got.Guidance, tt.want)
			}
		})
	}
}

func TestResolve_InvalidInputs(t *testing.T) {
	tests := []struct {
		name    string
		input   Input
		wantErr bool
	}{
		{
			name: "invalid capability tier",
			input: Input{
				Request: Request{
					Preset:     PresetFull,
					Guidance:   GuidanceSelectionInherit,
					Completion: CompletionSelectionInherit,
					Exposure:   ExposureSelectionInherit,
				},
				CapabilityTier: "invalid",
				EffortBand:     EffortUnknown,
			},
			wantErr: true,
		},
		{
			name: "invalid effort band",
			input: Input{
				Request: Request{
					Preset:     PresetFull,
					Guidance:   GuidanceSelectionInherit,
					Completion: CompletionSelectionInherit,
					Exposure:   ExposureSelectionInherit,
				},
				CapabilityTier: CapabilityStandard,
				EffortBand:     "invalid",
			},
			wantErr: true,
		},
		{
			name: "invalid completion selection",
			input: Input{
				Request: Request{
					Preset:     PresetFull,
					Guidance:   GuidanceSelectionInherit,
					Completion: "invalid",
					Exposure:   ExposureSelectionInherit,
				},
				CapabilityTier: CapabilityStandard,
				EffortBand:     EffortUnknown,
			},
			wantErr: true,
		},
		{
			name: "invalid exposure selection",
			input: Input{
				Request: Request{
					Preset:     PresetFull,
					Guidance:   GuidanceSelectionInherit,
					Completion: CompletionSelectionInherit,
					Exposure:   "invalid",
				},
				CapabilityTier: CapabilityStandard,
				EffortBand:     EffortUnknown,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolve_CapabilityAutoResolvesToStandard(t *testing.T) {
	input := Input{
		Request: Request{
			Preset:     PresetFull,
			Guidance:   GuidanceSelectionAuto,
			Completion: CompletionSelectionInherit,
			Exposure:   ExposureSelectionInherit,
		},
		CapabilityTier: CapabilityAuto,
		EffortBand:     EffortMedium,
	}

	got, err := Resolve(input)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	// Auto with medium effort should behave like standard capability
	if got.Guidance != GuidanceStructured {
		t.Errorf("With auto capability (resolved to standard) and medium effort, expected structured guidance, got %v", got.Guidance)
	}
}

func TestResolve_VerifiedPlusDeferred(t *testing.T) {
	input := Input{
		Request: Request{
			Preset:     PresetEconomy,
			Guidance:   GuidanceSelectionInherit,
			Completion: CompletionSelectionVerified,
			Exposure:   ExposureSelectionInherit,
		},
		CapabilityTier: CapabilityStandard,
		EffortBand:     EffortUnknown,
	}

	got, err := Resolve(input)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got.Completion != CompletionVerified {
		t.Errorf("Completion = %v, want verified", got.Completion)
	}
	if got.Exposure != ExposureDeferred {
		t.Errorf("Exposure = %v, want deferred", got.Exposure)
	}
	if !got.WorkspaceLease {
		t.Error("WorkspaceLease should be true for verified completion")
	}
}

func TestResolve_ConcurrentSafe(t *testing.T) {
	// Resolver must be safe for concurrent calls
	input := Input{
		Request: Request{
			Preset:     PresetFull,
			Guidance:   GuidanceSelectionAuto,
			Completion: CompletionSelectionAuto,
			Exposure:   ExposureSelectionAuto,
		},
		CapabilityTier: CapabilityStandard,
		EffortBand:     EffortHigh,
	}

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := Resolve(input)
			if err != nil {
				t.Errorf("Resolve() error = %v", err)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestNormalize_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name     string
		request  Request
		wantGuid Guidance
		wantComp Completion
		wantExp  Exposure
	}{
		{
			name: "uppercase inputs",
			request: Request{
				Preset:     PresetFull,
				Guidance:   "OFF",
				Completion: "STANDARD",
				Exposure:   "EAGER",
			},
			wantGuid: GuidanceOff,
			wantComp: CompletionStandard,
			wantExp:  ExposureEager,
		},
		{
			name: "mixed case inputs",
			request: Request{
				Preset:     PresetFull,
				Guidance:   "Light",
				Completion: "Verified",
				Exposure:   "Deferred",
			},
			wantGuid: GuidanceLight,
			wantComp: CompletionVerified,
			wantExp:  ExposureDeferred,
		},
		{
			name: "whitespace trimming",
			request: Request{
				Preset:     PresetFull,
				Guidance:   " structured ",
				Completion: " standard ",
				Exposure:   " eager ",
			},
			wantGuid: GuidanceStructured,
			wantComp: CompletionStandard,
			wantExp:  ExposureEager,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := Input{
				Request:        tt.request,
				CapabilityTier: CapabilityStandard,
				EffortBand:     EffortUnknown,
			}

			got, err := Resolve(input)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			if got.Guidance != tt.wantGuid {
				t.Errorf("Guidance = %v, want %v", got.Guidance, tt.wantGuid)
			}
			if got.Completion != tt.wantComp {
				t.Errorf("Completion = %v, want %v", got.Completion, tt.wantComp)
			}
			if got.Exposure != tt.wantExp {
				t.Errorf("Exposure = %v, want %v", got.Exposure, tt.wantExp)
			}
		})
	}
}
