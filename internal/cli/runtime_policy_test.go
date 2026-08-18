package cli

import (
	"strings"
	"testing"

	"corvus/internal/boot"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/runtimepolicy"
)

func TestRenderRuntimePolicyListsSelections(t *testing.T) {
	out := renderRuntimePolicy(80, boot.TokenModeEconomy, "auto", "", "deferred", runtimepolicy.Policy{
		Guidance:   runtimepolicy.GuidanceStructured,
		Completion: runtimepolicy.CompletionStandard,
		Exposure:   runtimepolicy.ExposureDeferred,
	})
	for _, want := range []string{"preset", "economy", "guidance", "auto", "structured", "completion", "inherit", "standard", "exposure", "deferred"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRuntimePolicyCommandStatusAndInvalid(t *testing.T) {
	m := newChatTUI(control.New(control.Options{Label: "model"}), "", make(chan event.Event, 1), 80)
	m.runtimeProfile = boot.TokenModeFull
	if cmd := m.runRuntimePolicyCommand("/runtime-policy"); cmd != nil {
		t.Fatal("status listing should not rebuild")
	}
	if cmd := m.runRuntimePolicyCommand("/runtime-policy mystery on"); cmd != nil {
		t.Fatal("invalid axis should not rebuild")
	}
	if cmd := m.runRuntimePolicyCommand("/runtime-policy guidance verbose"); cmd != nil {
		t.Fatal("invalid value should not rebuild")
	}
}

func TestParseRuntimePolicyFlags(t *testing.T) {
	g, err := parseGuidanceFlag("LIGHT")
	if err != nil || g != "light" {
		t.Fatalf("guidance = %q err=%v", g, err)
	}
	if _, err := parseCompletionFlag("paranoid"); err == nil {
		t.Fatal("invalid completion should error")
	}
	e, err := parseExposureFlag("deferred")
	if err != nil || e != "deferred" {
		t.Fatalf("exposure = %q err=%v", e, err)
	}
}
