package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"corvus/internal/i18n"
)

// errCancelled is returned by selectOne when the user aborts (q or Ctrl-C).
var errCancelled = errors.New("selection cancelled")

type menuItem struct {
	name string
	desc string
}

// termHeight returns the terminal's row count, falling back to 24 on error.
func termHeight(fd int) int {
	_, h, err := term.GetSize(fd)
	if err != nil || h <= 0 {
		return 24
	}
	return h
}

// fixedLines returns the number of non-item lines rendered each frame:
// header label, blank separator, scroll-up indicator, scroll-down indicator.
// When searching is true the search bar adds one more line.
func fixedLines(searching bool) int {
	n := 4 // header + blank + scroll-up + scroll-down
	if searching {
		n++ // search bar
	}
	return n
}

// maxViewport calculates how many menu item rows fit after subtracting the
// fixed lines from the available terminal rows, leaving at least 1 row.
func maxViewport(totalItems, termRows int, searching bool) int {
	avail := termRows - fixedLines(searching)
	if avail < 1 {
		avail = 1
	}
	if totalItems < avail {
		return totalItems
	}
	return avail
}

// renderSearchBar draws the search input line when searching is active.
func renderSearchBar(w *os.File, query string) {
	fmt.Fprintf(w, "\r\033[K%s %s\n", accent("🔍"), query+"_")
}

// filterMenuItems returns items whose name or desc contain the query (case-insensitive).
func filterMenuItems(items []menuItem, query string) []menuItem {
	if query == "" {
		return items
	}
	lq := strings.ToLower(query)
	var out []menuItem
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.name), lq) || strings.Contains(strings.ToLower(it.desc), lq) {
			out = append(out, it)
		}
	}
	return out
}

// selectOne renders an interactive single-choice menu navigated with the arrow
// keys (or j/k), confirmed with Enter, aborted with q or Ctrl-C. It puts the
// terminal in raw mode, so it requires a TTY (callers gate on isInteractive).
func selectOne(label string, items []menuItem) (int, error) {
	idx, err := selectLoop(label, items, false)
	if err != nil || len(idx) == 0 {
		return 0, err
	}
	return idx[0], nil
}

// selectMany renders an interactive multi-choice menu: arrow keys (or j/k)
// move, Space toggles, Enter confirms (at least one required), q/Ctrl-C
// aborts. It returns the checked indices in order and requires a TTY.
func selectMany(label string, items []menuItem) ([]int, error) {
	return selectLoop(label, items, true)
}

// selectLoop is the shared terminal-menu engine behind selectOne and
// selectMany: raw mode, arrow/j/k navigation, '/' search filtering, and a
// viewport window with scroll indicators when the list exceeds the terminal
// height. multi selects with checkboxes and confirms the checked set on Enter;
// single returns the highlighted row. (Single-select search mode cannot move
// the cursor; Enter confirms the first match.)
func selectLoop(label string, items []menuItem, multi bool) ([]int, error) {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer term.Restore(fd, old)

	w := os.Stdout
	th := termHeight(fd)

	searching := false
	searchQuery := ""
	filtered, filterIdx := filterItems(items, "")

	sel := 0
	checked := make([]bool, len(items))
	scroll := 0
	prevLines := 0

	hint, nameWidth := i18n.M.SelectOneHint, 10
	if multi {
		hint, nameWidth = i18n.M.SelectManyHint, 14
	}

	render := func() {
		n := len(filtered)
		vp := maxViewport(n, th, searching)
		// adjust scroll to keep sel visible
		if sel < scroll {
			scroll = sel
		}
		if sel >= scroll+vp {
			scroll = sel - vp + 1
		}
		if scroll < 0 {
			scroll = 0
		}

		// scroll-up indicator (always 1 line)
		if n > 0 && scroll > 0 {
			fmt.Fprintf(w, "\r\033[K%s\n", dim(fmt.Sprintf(i18n.M.SelectMoreAboveFmt, scroll)))
		} else {
			fmt.Fprintf(w, "\r\033[K\r\n")
		}

		// menu rows; fewer items than the viewport pad with blank lines so the
		// frame height stays constant
		end := scroll + vp
		if end > n {
			end = n
		}
		for i := scroll; i < end; i++ {
			it := filtered[i]
			name := fmt.Sprintf("%-*s", nameWidth, it.name)
			if multi {
				box := "[ ]"
				if checked[filterIdx[i]] {
					box = "[x]"
				}
				if i == sel {
					fmt.Fprintf(w, "\r\033[K%s\r\n", reverse(fmt.Sprintf(" ❯ %s %s %s ", box, name, it.desc)))
				} else {
					fmt.Fprintf(w, "\r\033[K   %s %s %s\r\n", box, name, dim(it.desc))
				}
				continue
			}
			if i == sel {
				fmt.Fprintf(w, "\r\033[K%s\r\n", reverse(fmt.Sprintf(" ❯ %s %s ", name, it.desc)))
			} else {
				fmt.Fprintf(w, "\r\033[K   %s %s\r\n", name, dim(it.desc))
			}
		}
		for i := end - scroll; i < vp; i++ {
			fmt.Fprintf(w, "\r\033[K\r\n")
		}

		// scroll-down indicator (always 1 line)
		if n > 0 && end < n {
			fmt.Fprintf(w, "\r\033[K%s\n", dim(fmt.Sprintf(i18n.M.SelectMoreBelowFmt, n-end)))
		} else {
			fmt.Fprintf(w, "\r\033[K\r\n")
		}
	}

	drawHeader := func() {
		if searching {
			fmt.Fprintf(w, "\r\033[K%s %s  %s\r\n\r\n", accent("▌"), bold(label), dim(i18n.M.SelectSearchHint))
			renderSearchBar(w, searchQuery)
		} else {
			fmt.Fprintf(w, "\r\033[K%s %s  %s\r\n\r\n", accent("▌"), bold(label), dim(hint))
		}
	}

	redraw := func() {
		if prevLines > 0 {
			fmt.Fprintf(w, "\033[%dA", prevLines)
		}
		drawHeader()
		render()
		// Clear everything below the current frame so stale rows from a taller
		// previous frame don't linger.
		fmt.Fprint(w, "\033[J")
		prevLines = fixedLines(searching) + maxViewport(len(filtered), th, searching)
	}

	move := func(delta int) {
		if delta < 0 && sel > 0 {
			sel--
		}
		if delta > 0 && sel < len(filtered)-1 {
			sel++
		}
	}

	// confirm collects the checked set for multi mode; single mode confirms the
	// highlighted row directly in the key loop.
	confirm := func() ([]int, bool) {
		var out []int
		for i, c := range checked {
			if c {
				out = append(out, i)
			}
		}
		return out, len(out) > 0
	}

	redraw() // initial draw

	buf := make([]byte, 8)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return nil, err
		}
		k := buf[:n]

		if searching {
			switch {
			case k[0] == 27: // Esc — exit search
				searching = false
				searchQuery = ""
				filtered, filterIdx = filterItems(items, "")
				sel = 0
				scroll = 0
			case k[0] == '\r' || k[0] == '\n':
				if multi {
					if out, ok := confirm(); ok {
						fmt.Fprint(w, "\r\n")
						return out, nil
					}
				} else if len(filtered) > 0 {
					fmt.Fprint(w, "\r\n")
					return []int{filterIdx[sel]}, nil
				}
			case k[0] == ' ' && multi:
				if len(filtered) > 0 {
					checked[filterIdx[sel]] = !checked[filterIdx[sel]]
				}
			case k[0] == 127 || k[0] == 8: // backspace
				if len(searchQuery) > 0 {
					searchQuery = searchQuery[:len(searchQuery)-1]
					filtered, filterIdx = filterItems(items, searchQuery)
					sel = 0
					scroll = 0
				}
			case k[0] == 3: // Ctrl-C
				fmt.Fprint(w, "\r\n")
				return nil, errCancelled
			case k[0] >= 32 && k[0] < 127: // printable (multi: except space, toggled above)
				searchQuery += string(k[0])
				filtered, filterIdx = filterItems(items, searchQuery)
				sel = 0
				scroll = 0
			case isArrowUp(k), k[0] == 'k':
				if multi {
					move(-1)
				}
			case isArrowDown(k), k[0] == 'j':
				if multi {
					move(1)
				}
			default:
				continue
			}
			redraw()
			continue
		}

		switch {
		case k[0] == '\r' || k[0] == '\n':
			if multi {
				if out, ok := confirm(); ok {
					fmt.Fprint(w, "\r\n")
					return out, nil
				}
				continue // need at least one selection
			}
			fmt.Fprint(w, "\r\n")
			return []int{filterIdx[sel]}, nil
		case k[0] == 3 || k[0] == 'q': // Ctrl-C or q
			fmt.Fprint(w, "\r\n")
			return nil, errCancelled
		case k[0] == '/': // enter search mode
			searching = true
			searchQuery = ""
		case k[0] == ' ' && multi:
			if len(filtered) > 0 {
				checked[filterIdx[sel]] = !checked[filterIdx[sel]]
			}
		case isArrowUp(k), k[0] == 'k':
			move(-1)
		case isArrowDown(k), k[0] == 'j':
			move(1)
		default:
			continue // ignore other keys, no redraw
		}
		redraw()
	}
}

func isArrowUp(k []byte) bool {
	return len(k) >= 3 && k[0] == 27 && k[1] == '[' && k[2] == 'A'
}

func isArrowDown(k []byte) bool {
	return len(k) >= 3 && k[0] == 27 && k[1] == '[' && k[2] == 'B'
}

// filterItems returns the items whose name or desc contain the query
// (case-insensitive) together with their original indices; an empty query
// passes everything through.
func filterItems(items []menuItem, query string) ([]menuItem, []int) {
	if query == "" {
		out := make([]menuItem, len(items))
		idx := make([]int, len(items))
		for i, it := range items {
			out[i], idx[i] = it, i
		}
		return out, idx
	}
	lq := strings.ToLower(query)
	var filtered []menuItem
	var idx []int
	for i, it := range items {
		if strings.Contains(strings.ToLower(it.name), lq) || strings.Contains(strings.ToLower(it.desc), lq) {
			filtered = append(filtered, it)
			idx = append(idx, i)
		}
	}
	return filtered, idx
}

// frameLines returns the total number of terminal lines that
// selectOne/selectMany will print for the given state.
func frameLines(filteredLen, termRows int, searching bool) int {
	return fixedLines(searching) + maxViewport(filteredLen, termRows, searching)
}
