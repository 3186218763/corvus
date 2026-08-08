package cli

import (
	"fmt"
	"strings"
)

// finalMessageSeparator is the dim turn rule Codex draws after a turn that
// performed concrete work (tools/exec/edit). Under 60s it is a plain ─ line;
// longer turns get a "Worked for …" label padded with ─ to width.
func finalMessageSeparator(width, elapsedSec int) string {
	if width < 8 {
		width = 8
	}
	if elapsedSec > 60 {
		label := fmt.Sprintf("─ Worked for %s ─", formatElapsedCompact(elapsedSec))
		return dim(padRuleLine(label, width))
	}
	return dim(strings.Repeat("─", width))
}

// formatElapsedCompact matches Codex status compact elapsed: 75 → "1m 15s".
func formatElapsedCompact(elapsedSec int) string {
	if elapsedSec < 0 {
		elapsedSec = 0
	}
	if elapsedSec < 60 {
		return fmt.Sprintf("%ds", elapsedSec)
	}
	if elapsedSec < 3600 {
		m := elapsedSec / 60
		s := elapsedSec % 60
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	h := elapsedSec / 3600
	m := (elapsedSec % 3600) / 60
	s := elapsedSec % 60
	return fmt.Sprintf("%dh %02dm %02ds", h, m, s)
}

func padRuleLine(label string, width int) string {
	lw := visibleWidth(label)
	if lw >= width {
		// Trim by runes roughly; visible clamp is enough for rule lines.
		return clampPlain(label, width)
	}
	return label + strings.Repeat("─", width-lw)
}
