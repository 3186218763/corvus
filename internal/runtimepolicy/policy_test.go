package runtimepolicy

import (
	"testing"
)

func TestResolve_LegacyPresetIsIgnoredAfterMigration(t *testing.T) {
	got, err := Resolve(Input{
		Request:        Request{Preset: PresetEconomy},
		CapabilityTier: CapabilityStandard,
		EffortBand:     EffortUnknown,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Guidance != GuidanceLight || got.Completion != CompletionStandard || got.Exposure != ExposureEager {
		t.Fatalf("legacy preset leaked into runtime policy: %+v", got)
	}
}

func TestResolve_NewRequestUsesIndependentDefaults(t *testing.T) {
	got, err := Resolve(Input{
		Request:        Request{},
		CapabilityTier: CapabilityStrong,
		EffortBand:     EffortHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Guidance != GuidanceOff || got.Completion != CompletionStandard || got.Exposure != ExposureEager {
		t.Fatalf("new defaults = %+v, want strong/high + standard/eager", got)
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
				Guidance:   GuidanceOff,
				Completion: CompletionStandard,
				Exposure:   ExposureEager,
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
				Guidance:   GuidanceLight,
				Completion: CompletionVerified,
				Exposure:   ExposureEager,
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
				Guidance:   GuidanceLight,
				Completion: CompletionStandard,
				Exposure:   ExposureEager,
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
		{name: "strong/low", capability: CapabilityStrong, effort: EffortLow, want: GuidanceOff},
		{name: "strong/medium", capability: CapabilityStrong, effort: EffortMedium, want: GuidanceOff},
		{name: "strong/high", capability: CapabilityStrong, effort: EffortHigh, want: GuidanceOff},
		{name: "strong/xhigh", capability: CapabilityStrong, effort: EffortXHigh, want: GuidanceOff},
		{name: "strong/max", capability: CapabilityStrong, effort: EffortMax, want: GuidanceOff},

		// Standard capability
		{name: "standard/unknown", capability: CapabilityStandard, effort: EffortUnknown, want: GuidanceLight},
		{name: "standard/disabled", capability: CapabilityStandard, effort: EffortDisabled, want: GuidanceStructured},
		{name: "standard/low", capability: CapabilityStandard, effort: EffortLow, want: GuidanceLight},
		{name: "standard/medium", capability: CapabilityStandard, effort: EffortMedium, want: GuidanceLight},
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
		{name: "lite/max", capability: CapabilityLite, effort: EffortMax, want: GuidanceStructured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := Input{
				Request: Request{
					Preset:     PresetFull,
					Guidance:   GuidanceSelectionAuto,
					Completion: CompletionSelectionInherit,
					Exposure:   ExposureSelectionDeferred,
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
	if got.Guidance != GuidanceLight {
		t.Errorf("With auto capability (resolved to standard) and medium effort, expected light guidance, got %v", got.Guidance)
	}
}

func TestResolve_VerifiedPlusDeferred(t *testing.T) {
	input := Input{
		Request: Request{
			Preset:     PresetEconomy,
			Guidance:   GuidanceSelectionInherit,
			Completion: CompletionSelectionVerified,
			Exposure:   ExposureSelectionDeferred,
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
		wantGUID Guidance
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
			wantGUID: GuidanceOff,
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
			wantGUID: GuidanceLight,
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
			wantGUID: GuidanceStructured,
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

			if got.Guidance != tt.wantGUID {
				t.Errorf("Guidance = %v, want %v", got.Guidance, tt.wantGUID)
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
