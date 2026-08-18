package config

import (
	"strings"
	"testing"
)

func TestModelCapabilityNormalization(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "auto", input: "auto", want: ""},
		{name: "AUTO uppercase", input: "AUTO", want: ""},
		{name: "strong", input: "strong", want: "strong"},
		{name: "Strong mixed case", input: "Strong", want: "strong"},
		{name: "standard", input: "standard", want: "standard"},
		{name: "lite", input: "lite", want: "lite"},
		{name: "invalid preserved", input: "invalid", want: "invalid"},
		{name: "whitespace trimmed", input: "  strong  ", want: "strong"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeModelCapability(tt.input)
			if got != tt.want {
				t.Errorf("normalizeModelCapability(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestProviderModelOverrideNormalization(t *testing.T) {
	input := map[string]ProviderModelOverride{
		"model-a": {
			ModelCapability: "Strong",
			ContextWindow:   100000,
		},
		"model-b": {
			ModelCapability: "AUTO",
		},
		"  empty  ": {},
	}

	got := normalizedModelOverrides(input)

	if len(got) != 1 {
		t.Errorf("expected 1 override after normalization, got %d", len(got))
	}

	if ov, ok := got["model-a"]; !ok {
		t.Error("model-a override missing")
	} else {
		if ov.ModelCapability != "strong" {
			t.Errorf("model-a capability = %q, want strong", ov.ModelCapability)
		}
	}

	if _, ok := got["model-b"]; ok {
		t.Error("model-b should be removed (auto capability with no other fields)")
	}
}

func TestProviderEntryCapabilityField(t *testing.T) {
	entry := ProviderEntry{
		Name:            "test",
		Kind:            "anthropic",
		Model:           "test-model",
		ModelCapability: "STRONG",
	}

	normalizeProviderEffortFields(&entry)

	if entry.ModelCapability != "strong" {
		t.Errorf("ModelCapability = %q, want strong", entry.ModelCapability)
	}
}

func TestProviderModelOverrideWithCapability(t *testing.T) {
	entry := ProviderEntry{
		Name:            "test",
		Kind:            "openai",
		Model:           "base-model",
		ModelCapability: "standard",
		ModelOverrides: map[string]ProviderModelOverride{
			"advanced-model": {
				ModelCapability: "Strong",
				ContextWindow:   200000,
			},
		},
	}

	normalizeProviderEffortFields(&entry)

	if entry.ModelCapability != "standard" {
		t.Errorf("provider capability = %q, want standard", entry.ModelCapability)
	}

	if ov, ok := entry.ModelOverrides["advanced-model"]; !ok {
		t.Fatal("advanced-model override missing")
	} else {
		if ov.ModelCapability != "strong" {
			t.Errorf("override capability = %q, want strong", ov.ModelCapability)
		}
	}
}

func TestRuntimePolicyConfigNormalizesAndRenders(t *testing.T) {
	cfg := &Config{RuntimePolicy: RuntimePolicyConfig{
		Guidance:   "  AUTO ",
		Completion: "Verified",
		Exposure:   "DEFERRED",
	}}
	normalizeEffortConfig(cfg)
	if cfg.RuntimePolicy.Guidance != "auto" || cfg.RuntimePolicy.Completion != "verified" || cfg.RuntimePolicy.Exposure != "deferred" {
		t.Fatalf("normalized = %+v", cfg.RuntimePolicy)
	}
	out := RenderTOML(cfg)
	if !strings.Contains(out, "[runtime_policy]") || !strings.Contains(out, `guidance   = "auto"`) {
		t.Fatalf("render missing runtime_policy:\n%s", out)
	}
	empty := RenderTOML(Default())
	if strings.Contains(empty, "[runtime_policy]") {
		t.Fatal("default config should omit empty runtime_policy")
	}
}
