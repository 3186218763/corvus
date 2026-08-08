package cli

import (
	"testing"
)

func TestMotionEnvHelpers(t *testing.T) {
	t.Setenv("CORVUS_REDUCE_MOTION", "1")
	if motionEnabled() {
		t.Fatal("motionEnabled should be false with CORVUS_REDUCE_MOTION=1")
	}
	t.Setenv("CORVUS_REDUCE_MOTION", "0")
	if !motionEnabled() {
		t.Fatal("motionEnabled should be true with CORVUS_REDUCE_MOTION=0")
	}
	t.Setenv("CORVUS_REDUCE_MOTION", "")
	if !motionEnabled() {
		t.Fatal("motionEnabled should be true when unset")
	}
	t.Setenv("CORVUS_TUI_SCROLL_REPAINT", "1")
	if !scrollRepaintEnabled() {
		t.Fatal("scrollRepaintEnabled should be true with CORVUS_TUI_SCROLL_REPAINT=1")
	}
	t.Setenv("CORVUS_TUI_SCROLL_REPAINT", "0")
	if scrollRepaintEnabled() {
		t.Fatal("scrollRepaintEnabled should be false with CORVUS_TUI_SCROLL_REPAINT=0")
	}
	t.Setenv("CORVUS_TUI_SCROLL_REPAINT", "")
	if scrollRepaintEnabled() {
		t.Fatal("scrollRepaintEnabled should be false when unset")
	}
}

func TestWorkingCmdsGatesSpinnerTick(t *testing.T) {
	m := newTestChatTUI()
	t.Setenv("CORVUS_REDUCE_MOTION", "1")
	if _, sp := m.workingCmds(); sp != nil {
		t.Fatal("spinner tick must be suppressed when reduced motion is on")
	}
	t.Setenv("CORVUS_REDUCE_MOTION", "0")
	if _, sp := m.workingCmds(); sp == nil {
		t.Fatal("spinner tick must be scheduled when motion is on")
	}
}

func TestToolFramesFreezeUnderReducedMotion(t *testing.T) {
	// Tool working walls are ambient-only now; tickToolRunning is a no-op and
	// must not mutate the transcript under any motion setting.
	m := newTestChatTUI()
	m.transcript = append(m.transcript, "card")
	before := m.transcript[0]
	t.Setenv("CORVUS_REDUCE_MOTION", "1")
	m.tickToolRunning()
	t.Setenv("CORVUS_REDUCE_MOTION", "0")
	m.tickToolRunning()
	if m.transcript[0] != before {
		t.Fatalf("tickToolRunning must not paint transcript, got %q", m.transcript[0])
	}
}

func TestWorkingBatchSuppressesSpinner(t *testing.T) {
	m := newTestChatTUI()
	t.Setenv("CORVUS_REDUCE_MOTION", "1")
	if got := m.workingBatch(); got == nil {
		t.Fatal("workingBatch must still return the elapsed ticker")
	}
	t.Setenv("CORVUS_REDUCE_MOTION", "0")
	if got := m.workingBatch(); got == nil {
		t.Fatal("workingBatch must return a batch with motion on")
	}
}
