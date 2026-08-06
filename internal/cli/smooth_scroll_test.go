package cli

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
)

func scrollFixture() chatTUI {
	vp := viewport.New(viewport.WithWidth(80))
	var content string
	for i := 0; i < 200; i++ {
		content += "line\n"
	}
	vp.SetContent(content)
	vp.SetHeight(20)
	m := newTestChatTUI()
	m.viewport = vp
	m.scrollRepaint = false
	return m
}

func TestSmoothScrollStartsAndSnaps(t *testing.T) {
	upd := func(m chatTUI, msg tea.Msg) (chatTUI, tea.Cmd) {
		n, cmd := m.update(msg)
		return n.(chatTUI), cmd
	}
	m := scrollFixture()
	next, cmd := m.startSmoothScroll(50)
	if cmd == nil {
		t.Fatal("motion on should start a tick")
	}
	if next.smooth == nil {
		t.Fatal("smooth state should be active")
	}

	// Mid-flight tick.
	mid := time.Unix(0, 0)
	next.smooth.start = mid
	next2, cmd2 := upd(next, smoothScrollTickMsg{now: mid.Add(75 * time.Millisecond)})
	if cmd2 == nil {
		t.Fatal("mid-flight tick should re-arm")
	}
	off := next2.viewport.YOffset()
	if off <= 0 || off >= 50 {
		t.Fatalf("mid-flight offset %d should be strictly between 0 and 50", off)
	}

	// Final snap.
	next3, cmd3 := upd(next2, smoothScrollTickMsg{now: mid.Add(10 * time.Second)})
	if cmd3 != nil {
		t.Fatal("final tick should not re-arm")
	}
	if next3.smooth != nil {
		t.Fatal("smooth state should clear on arrival")
	}
	if next3.viewport.YOffset() != 50 {
		t.Fatalf("final offset = %d, want 50", next3.viewport.YOffset())
	}
}

func TestSmoothScrollInstantWhenMotionOff(t *testing.T) {
	t.Setenv("REASONIX_REDUCE_MOTION", "1")
	m := scrollFixture()
	next, cmd := m.startSmoothScroll(50)
	if cmd != nil {
		t.Fatal("motion off must jump instantly, no tick")
	}
	if next.smooth != nil {
		t.Fatal("no smooth state when motion off")
	}
	if next.viewport.YOffset() != 50 {
		t.Fatalf("offset = %d, want 50", next.viewport.YOffset())
	}
}

func TestSmoothScrollInstantUnderLegacyRepaint(t *testing.T) {
	m := scrollFixture()
	m.scrollRepaint = true
	next, cmd := m.startSmoothScroll(50)
	if cmd != nil {
		t.Fatal("legacy repaint mode must jump instantly")
	}
	if next.viewport.YOffset() != 50 {
		t.Fatalf("offset = %d, want 50", next.viewport.YOffset())
	}
}

func TestSmoothScrollInterruptRestartsFromCurrent(t *testing.T) {
	upd := func(m chatTUI, msg tea.Msg) (chatTUI, tea.Cmd) {
		n, cmd := m.update(msg)
		return n.(chatTUI), cmd
	}
	m := scrollFixture()
	next, _ := m.startSmoothScroll(50)
	mid := time.Unix(0, 0)
	next.smooth.start = mid
	next2, _ := upd(next, smoothScrollTickMsg{now: mid.Add(75 * time.Millisecond)})
	cur := next2.viewport.YOffset()
	next3, _ := next2.startSmoothScroll(100)
	if next3.smooth.from != cur {
		t.Fatalf("interrupt should restart from current offset %d, got %d", cur, next3.smooth.from)
	}
}

func TestSmoothScrollClampsTarget(t *testing.T) {
	upd := func(m chatTUI, msg tea.Msg) (chatTUI, tea.Cmd) {
		n, cmd := m.update(msg)
		return n.(chatTUI), cmd
	}
	m := scrollFixture()
	next, _ := m.startSmoothScroll(1_000_000)
	mid := time.Unix(0, 0)
	next.smooth.start = mid
	next2, _ := upd(next, smoothScrollTickMsg{now: mid.Add(10 * time.Second)})
	if next2.smooth != nil || next2.viewport.YOffset() >= 200 {
		t.Fatalf("target must clamp to content bounds, offset=%d", next2.viewport.YOffset())
	}
}
