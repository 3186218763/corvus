package cli

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// selectionLetter maps a 0-based row index to a–z. Rows beyond z have no letter.
func selectionLetter(i int) (rune, bool) {
	if i < 0 || i > 25 {
		return 0, false
	}
	return rune('a' + i), true
}

// selectionRow formats one selectable row: "› a. label" when selected,
// "  b. label" when idle (Codex-style letter labels). box is an optional
// multi-select marker ("☐ "/"☑ ") inserted after the letter.
func selectionRow(selected bool, index int, box, label string, active bool) string {
	prefix := "  "
	if selected {
		prefix = accent("› ")
	}
	var body string
	switch {
	case selected:
		if ch, ok := selectionLetter(index); ok {
			body = accent(bold(string(ch)+". ")) + bold(box+label)
		} else {
			body = bold(box + label)
		}
	case active:
		if ch, ok := selectionLetter(index); ok {
			body = yellow(string(ch) + ". " + box + label)
		} else {
			body = yellow(box + label)
		}
	default:
		if ch, ok := selectionLetter(index); ok {
			body = dim(string(ch) + ". " + box + label)
		} else {
			body = dim(box + label)
		}
	}
	return prefix + body
}

// selectionRowWithHint is selectionRow plus a right-aligned dim shortcut tag
// (e.g. approval semantic y/a/p/n).
func selectionRowWithHint(selected bool, index int, box, label, hint string, active bool, width int) string {
	row := selectionRow(selected, index, box, label, active)
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return row
	}
	tag := dim(hint)
	if width <= 0 {
		return row + "  " + tag
	}
	gap := width - visibleWidth(ansi.Strip(row)) - visibleWidth(hint)
	if gap < 2 {
		gap = 2
	}
	return row + strings.Repeat(completionPadCell, gap) + tag
}

// selectionPanel frames a choice list with the shared accent top border used by
// approval / ask / pickers (Codex menu-surface feel).
func selectionPanel(body string, width int) string {
	w := max(width, 10)
	return choicePanelStyle.Width(w).Render(body)
}

// selectionFooter returns the dim key-hint line under a lettered list.
func selectionFooter(extra string) string {
	base := "↑/↓ · a–z · Enter · Esc"
	if extra != "" {
		return dim(base + " · " + extra)
	}
	return dim(base)
}

// selectionIndexKey maps a key string to a 0-based index for a–z / 1–9.
// Returns -1 when the key is not a selection index.
func selectionIndexKey(key string) int {
	if len(key) != 1 {
		return -1
	}
	c := key[0]
	if c >= 'a' && c <= 'z' {
		return int(c - 'a')
	}
	if c >= 'A' && c <= 'Z' {
		return int(c - 'A')
	}
	if c >= '1' && c <= '9' {
		return int(c - '1')
	}
	return -1
}
