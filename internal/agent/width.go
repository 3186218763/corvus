package agent

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// visibleWidth returns the printable column width of s: ANSI escape codes are
// ignored and wide / grapheme-cluster characters (CJK, emoji ZWJ sequences,
// keycaps, flags) count as the cells they occupy. x/ansi is the single width
// authority (ADR-0002); go-runewidth answered 1 cell for flags and keycaps,
// which undercounted streamed rows and left stale output on redraw.
func visibleWidth(s string) int {
	return ansi.StringWidth(s)
}

// streamedRows counts how many rows the cursor has descended after raw text
// of length s was printed at the given terminal width. Used by the markdown
// redraw to know how far up to move before clearing. Each \n descends one
// row; lines whose visible width exceeds the terminal width descend an extra
// row per wrap. A line exactly the terminal width does not wrap on its own —
// terminals "lazy-wrap" only when the next visible character lands.
func streamedRows(s string, width int) int {
	if width <= 0 {
		width = 80
	}
	rows := 0
	for _, line := range strings.Split(s, "\n") {
		if w := visibleWidth(line); w > 0 {
			rows += (w - 1) / width
		}
	}
	rows += strings.Count(s, "\n")
	return rows
}
