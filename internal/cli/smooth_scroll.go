package cli

import (
	"math"
	"time"

	"charm.land/bubbletea/v2"
)

const (
	smoothScrollDuration = 150 * time.Millisecond
	smoothScrollTickDur  = 16 * time.Millisecond
)

// smoothScroll is the PgUp/PgDn/wheel interpolation state; nil when idle.
type smoothScroll struct {
	from, to int
	start    time.Time
	dur      time.Duration
}

type smoothScrollTickMsg struct {
	now time.Time
}

func smoothScrollTick() tea.Cmd {
	return tea.Tick(smoothScrollTickDur, func(t time.Time) tea.Msg {
		return smoothScrollTickMsg{now: t}
	})
}

// startSmoothScroll animates the viewport offset to target (150ms ease-out
// cubic). Jumps instantly when reduced motion is on or the legacy repaint mode
// is active; interrupts an in-flight animation from its current offset.
func (m chatTUI) startSmoothScroll(target int) (chatTUI, tea.Cmd) {
	if !motionEnabled() || m.scrollRepaint {
		m.viewport.SetYOffset(target)
		return m, nil
	}
	from := m.viewport.YOffset()
	if from == target {
		return m, nil
	}
	m.smooth = &smoothScroll{from: from, to: target, start: time.Now(), dur: smoothScrollDuration}
	return m, smoothScrollTick()
}

// offsetAt returns the eased offset at time now; done=true when finished.
func (s *smoothScroll) offsetAt(now time.Time) (offset int, done bool) {
	if now.Before(s.start) {
		now = s.start
	}
	t := float64(now.Sub(s.start)) / float64(s.dur)
	if t >= 1 {
		return s.to, true
	}
	eased := 1 - math.Pow(1-t, 3)
	return s.from + int(float64(s.to-s.from)*eased), false
}
