package cli

import (
	"strings"
	"testing"

	"corvus/internal/boot"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/provider"
)

func TestCacheInvalidationNoticeCopy(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		reason string
		want   string
	}{
		{
			reason: CacheInvalidationReasonModel,
			want:   "Switching models may reset the provider prompt-cache prefix for this session.",
		},
		{
			reason: CacheInvalidationReasonTokenMode,
			want:   "Switching work/token mode changes the tools surface and may reset the prompt-cache prefix.",
		},
		{
			reason: "work_mode",
			want:   "Switching work/token mode changes the tools surface and may reset the prompt-cache prefix.",
		},
		{
			reason: CacheInvalidationReasonTools,
			want:   "Tool definitions changed; the prompt-cache tools prefix may miss on the next turn.",
		},
		{reason: "", want: ""},
		{reason: "unknown", want: ""},
	} {
		if got := CacheInvalidationNotice(tt.reason); got != tt.want {
			t.Errorf("CacheInvalidationNotice(%q) = %q, want %q", tt.reason, got, tt.want)
		}
	}
}

func TestModelSwitchEmitsCacheInvalidationNotice(t *testing.T) {
	isolateUserConfig(t)
	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Label: "old", SessionDir: t.TempDir()})
	m.modelRef = "provider/old-model"
	m.buildController = func(controllerBuildSpec, []provider.Message, string, control.SessionAPI) (*control.Controller, error) {
		return control.New(control.Options{Label: "new"}), nil
	}

	m.runModelSubcommand("/model provider/new-model")
	if m.pendingModelSwitch == nil {
		t.Fatal("model switch did not schedule a rebuild")
	}
	joined := strings.Join(m.transcript, "\n")
	want := CacheInvalidationNotice(CacheInvalidationReasonModel)
	if !strings.Contains(joined, want) {
		t.Fatalf("model switch transcript missing cache invalidation notice %q:\n%s", want, joined)
	}
}

func TestWorkModeSwitchEmitsCacheInvalidationNotice(t *testing.T) {
	m := newChatTUI(control.New(control.Options{Label: "model", SessionDir: t.TempDir()}), "", make(chan event.Event, 1), 100)
	m.modelRef = "provider/model"
	m.runtimeProfile = boot.TokenModeFull
	m.buildController = func(controllerBuildSpec, []provider.Message, string, control.SessionAPI) (*control.Controller, error) {
		return control.New(control.Options{Label: "new"}), nil
	}

	cmd := m.runWorkModeCommand("/work-mode delivery")
	if cmd == nil {
		t.Fatal("work-mode switch did not schedule a rebuild")
	}
	joined := strings.Join(m.transcript, "\n")
	want := CacheInvalidationNotice(CacheInvalidationReasonTokenMode)
	if !strings.Contains(joined, want) {
		t.Fatalf("work-mode switch transcript missing cache invalidation notice %q:\n%s", want, joined)
	}
}
