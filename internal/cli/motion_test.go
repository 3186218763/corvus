package cli

import "testing"

func TestMotionEnvHelpers(t *testing.T) {
	t.Setenv("REASONIX_REDUCE_MOTION", "1")
	if motionEnabled() {
		t.Fatal("motionEnabled should be false with REASONIX_REDUCE_MOTION=1")
	}
	t.Setenv("REASONIX_REDUCE_MOTION", "0")
	if !motionEnabled() {
		t.Fatal("motionEnabled should be true with REASONIX_REDUCE_MOTION=0")
	}
	t.Setenv("REASONIX_REDUCE_MOTION", "")
	if !motionEnabled() {
		t.Fatal("motionEnabled should be true when unset")
	}
	t.Setenv("REASONIX_TUI_SCROLL_REPAINT", "1")
	if !scrollRepaintEnabled() {
		t.Fatal("scrollRepaintEnabled should be true with REASONIX_TUI_SCROLL_REPAINT=1")
	}
	t.Setenv("REASONIX_TUI_SCROLL_REPAINT", "0")
	if scrollRepaintEnabled() {
		t.Fatal("scrollRepaintEnabled should be false with REASONIX_TUI_SCROLL_REPAINT=0")
	}
	t.Setenv("REASONIX_TUI_SCROLL_REPAINT", "")
	if scrollRepaintEnabled() {
		t.Fatal("scrollRepaintEnabled should be false when unset")
	}
}

func TestWorkingCmdsGatesSpinnerTick(t *testing.T) {
	m := newTestChatTUI()
	t.Setenv("REASONIX_REDUCE_MOTION", "1")
	if _, sp := m.workingCmds(); sp != nil {
		t.Fatal("spinner tick must be suppressed when reduced motion is on")
	}
	t.Setenv("REASONIX_REDUCE_MOTION", "0")
	if _, sp := m.workingCmds(); sp == nil {
		t.Fatal("spinner tick must be scheduled when motion is on")
	}
}
