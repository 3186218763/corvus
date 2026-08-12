// Renders a unified diff as line-numbered, syntax-highlighted rows on
// green/red background bars with a +/- gutter.
package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"

	"corvus/internal/event"
	"corvus/internal/i18n"
)

const tabWidth = 4

const (
	// diffFoldLimit is the max lines to show in a diff when folding is enabled
	// (/diff-fold toggle). 0 means show all lines.
	diffFoldLimit = 40
)

var (
	diffChromaFmt = formatters.Get("terminal256")
	hunkRE        = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
)

// Resolve on each render so runtime theme switches and theme-sweep preview
// frames cannot retain syntax colours from the previous light/dark mode.
// catppuccin matches Codex's adaptive default (mocha dark / latte light).
func activeDiffChromaStyle() *chroma.Style {
	if activeCLITheme.name == "light" {
		return styles.Get("catppuccin-latte")
	}
	return styles.Get("catppuccin-mocha")
}

// fileVerb maps a change's shape to its Codex-style verb: pure additions
// "Added", pure removals "Deleted", mixed edits "Edited".
func fileVerb(d event.FileDiff) string {
	switch {
	case d.Added > 0 && d.Removed == 0:
		return "Added"
	case d.Removed > 0 && d.Added == 0:
		return "Deleted"
	default:
		return "Edited"
	}
}

// codexStat renders "(+N -M)" with green/red sides, always showing both like
// Codex's line-count summary.
func codexStat(d event.FileDiff) string {
	return "(" + green("+"+strconv.Itoa(d.Added)) + " " + red("-"+strconv.Itoa(d.Removed)) + ")"
}

func diffPath(args string) string {
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal([]byte(args), &p)
	return p.Path
}

// diffBlock renders a file change as a Codex-style header ("● Added path (+3 -0)")
// plus the highlighted, folded diff body. Returns nil when there's no textual diff.
func diffBlock(name, args string, d event.FileDiff, width, maxLines int) []string {
	if d.Diff == "" {
		return nil
	}
	path := diffPath(args)
	verb := fileVerb(d)
	stat := codexStat(d)
	avail := width - 6 - len([]rune(verb)) - len([]rune(stat))
	displayPath := clampPlain(path, avail)
	header := "  " + dim("●") + " " + bold(verb) + " " + themeFg(activeCLITheme.toolArg, displayPath) + "  " + stat
	return append([]string{header}, diffBody(d, path, width, maxLines)...)
}

// diffBody renders the hunks with a line-number gutter, dropping the file and
// "@@" headers (a dim "⋮" marks each hunk jump) and folding past maxLines to a
// "+N more" footer. path selects the syntax lexer.
func diffBody(d event.FileDiff, path string, width, maxLines int) []string {
	if d.Diff == "" {
		return nil
	}
	src := strings.Split(strings.TrimRight(d.Diff, "\n"), "\n")
	// Drop the "--- a/… / +++ b/…" header pair positionally — matching the prefix
	// on every line would eat real content (a deleted SQL "-- x" renders "--- x",
	// an added "++ y" renders "+++ y").
	if len(src) >= 2 && strings.HasPrefix(src[0], "--- ") && strings.HasPrefix(src[1], "+++ ") {
		src = src[2:]
	}
	gw := gutterWidth(src)

	var rows []string
	oldNo, newNo, hunks := 0, 0, 0
	for _, ln := range src {
		if ln == "" {
			continue
		}
		switch ln[0] {
		case '@':
			if m := hunkRE.FindStringSubmatch(ln); m != nil {
				oldNo, newNo = atoi(m[1]), atoi(m[3])
			}
			if hunks > 0 {
				rows = append(rows, "  "+dim("⋮"))
			}
			hunks++
		case '+':
			rows = append(rows, diffBar('+', ln[1:], path, width, bgSGR(activeCLITheme.diffAddBG), fgSGR(activeCLITheme.success), newNo, gw))
			newNo++
		case '-':
			rows = append(rows, diffBar('-', ln[1:], path, width, bgSGR(activeCLITheme.diffDelBG), fgSGR(activeCLITheme.err), oldNo, gw))
			oldNo++
		case '\\':
			rows = append(rows, "  "+dim(clampPlain(ln, width-2)))
		default:
			code := ln
			if ln[0] == ' ' {
				code = ln[1:]
			}
			rows = append(rows, diffContext(code, path, width, newNo, gw))
			oldNo++
			newNo++
		}
	}

	if maxLines > 0 && len(rows) > maxLines {
		folded := len(rows) - (maxLines - 1)
		rows = rows[:maxLines-1]
		rows = append(rows, "  "+dim(fmt.Sprintf(i18n.M.DiffFoldedFmt, folded)))
	}
	return rows
}

// diffBar draws one added/removed row on a full-width coloured background
// (Codex-style: indent + gutter + sign + code all share the tint). The bg is
// re-applied after every SGR reset so chroma/dim never hollows the bar; the
// trailing pad uses non-clearable NBSP cells so cell-diff / non-fullscreen
// redraws cannot erase the fill with EL/ECH.
func diffBar(sign byte, code, path string, width int, bg, signFg string, lineNo, gw int) string {
	// Layout: "  " + gutter(gw) + " " + sign + " " + code  → prefix cols = 2+gw+1+1+1
	prefixCols := 2 + gw + 3
	codeMax := width - prefixCols
	if codeMax < 1 {
		codeMax = 1
	}
	code = clampPlain(code, codeMax)
	gutterPlain := lpad(strconv.Itoa(lineNo), gw)
	if !colorOn() {
		return "  " + gutterPlain + " " + string(sign) + " " + code
	}
	hl := highlightCode(path, code)
	if sign == '-' {
		hl = ansiDim + strings.ReplaceAll(hl, ansiReset, ansiReset+ansiDim)
	}
	hl = reapplyBG(hl, bg)
	gutter := reapplyBG(dim(gutterPlain), bg)
	pad := width - prefixCols - visibleWidth(code)
	if pad < 0 {
		pad = 0
	}
	// Full-line arm: bg covers indent/gutter/sign/code/pad; every reset re-arms.
	return bg + "  " + gutter + bg + " " + signFg + string(sign) + ansiReset + bg + " " + hl +
		strings.Repeat(completionPadCell, pad) + ansiReset
}

// diffContext draws an unchanged line: the gutter, no background, code aligned
// under the +/- rows' code column.
func diffContext(code, path string, width, lineNo, gw int) string {
	gutter := dim(lpad(strconv.Itoa(lineNo), gw))
	return "  " + gutter + "   " + highlightClamped(code, path, width-4-gw)
}

func gutterWidth(lines []string) int {
	max := 0
	for _, ln := range lines {
		m := hunkRE.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		for _, p := range [][2]int{{1, 2}, {3, 4}} {
			end := atoi(m[p[0]])
			if m[p[1]] != "" {
				end += atoi(m[p[1]])
			} else {
				end++
			}
			if end > max {
				max = end
			}
		}
	}
	if w := len(strconv.Itoa(max)); w > 2 {
		return w
	}
	return 2
}

func lpad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat(" ", w-len(s)) + s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func highlightClamped(code, path string, w int) string {
	c := clampPlain(code, w)
	if !colorOn() {
		return c
	}
	return highlightCode(path, c)
}

func clampPlain(s string, w int) string {
	if w < 1 {
		w = 1
	}
	return ansi.Truncate(expandTabs(sanitizeTerminalText(s)), w, "")
}

// expandTabs replaces tabs with spaces to the next tabWidth stop. A literal tab
// has zero StringWidth but the terminal advances it to a tab stop, so leaving
// tabs in a background-bar row overflows the bar — expand them so the measured
// width matches what's drawn.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	col := 0
	start := 0
	for i, r := range s {
		if r != '\t' {
			continue
		}
		chunk := s[start:i]
		b.WriteString(chunk)
		col += ansi.StringWidth(chunk)
		n := tabWidth - col%tabWidth
		b.WriteString(strings.Repeat(" ", n))
		col += n
		start = i + 1
	}
	b.WriteString(s[start:])
	return b.String()
}

func reapplyBG(s, bg string) string {
	if s == "" {
		return s
	}
	return strings.ReplaceAll(s, ansiReset, ansiReset+bg)
}

// highlightCode returns code with chroma ANSI foreground colours for the lexer
// matched by path (plain fallback for unknown types). It emits no background, so
// it composes onto a diff bar; the caller re-applies the bar background.
func highlightCode(path, code string) string {
	if code == "" {
		return code
	}
	lexer := lexers.Match(path)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var b strings.Builder
	if diffChromaFmt.Format(&b, activeDiffChromaStyle(), it) != nil {
		return code
	}
	return strings.TrimRight(b.String(), "\n")
}

// bashFlagRE matches "-x" / "--flag" style arguments, which the chroma bash
// lexer leaves as plain Text; Codex's shell grammar colours them as
// variable.parameter, so we re-tag them as NameAttribute for the style to tint.
var bashFlagRE = regexp.MustCompile(`-{1,2}[A-Za-z0-9][A-Za-z0-9_-]*`)

// tokeniseBash runs the chroma bash lexer and re-tags "-x"/"--flag" arguments
// (outside quoted strings) as NameAttribute. Returns a single Text token for
// lexer errors so callers always get a renderable stream.
func tokeniseBash(cmd string) []chroma.Token {
	it, err := lexers.Get("bash").Tokenise(nil, cmd)
	if err != nil {
		return []chroma.Token{{Type: chroma.Text, Value: cmd}}
	}
	var out []chroma.Token
	for _, t := range it.Tokens() {
		if t.Type != chroma.Text {
			out = append(out, t)
			continue
		}
		last := 0
		for _, loc := range bashFlagRE.FindAllStringIndex(t.Value, -1) {
			if loc[0] > last {
				out = append(out, chroma.Token{Type: chroma.Text, Value: t.Value[last:loc[0]]})
			}
			out = append(out, chroma.Token{Type: chroma.NameAttribute, Value: t.Value[loc[0]:loc[1]]})
			last = loc[1]
		}
		if last < len(t.Value) {
			out = append(out, chroma.Token{Type: chroma.Text, Value: t.Value[last:]})
		}
	}
	return out
}

// activeBashChromaStyle is the catppuccin theme plus a NameAttribute override
// that tints "-x"/"--flag" arguments (Codex's variable.parameter yellow).
func activeBashChromaStyle() *chroma.Style {
	flag := "#f9e2af" // catppuccin-mocha yellow
	if activeCLITheme.name == "light" {
		flag = "#df8e1d" // catppuccin-latte yellow
	}
	s, err := activeDiffChromaStyle().Builder().
		AddEntry(chroma.NameAttribute, chroma.StyleEntry{Colour: chroma.MustParseColour(flag)}).
		Build()
	if err != nil {
		return activeDiffChromaStyle()
	}
	return s
}

// highlightBash returns cmd with chroma bash foreground colours (catppuccin,
// flags tinted). It emits no background, mirroring highlightCode, and passes
// plain text through when the terminal has no colour.
func highlightBash(cmd string) string {
	if cmd == "" || !colorOn() {
		return cmd
	}
	var b strings.Builder
	if diffChromaFmt.Format(&b, activeBashChromaStyle(), tokenIterator(tokeniseBash(cmd))) != nil {
		return cmd
	}
	return strings.TrimRight(b.String(), "\n")
}

// tokenIterator adapts a token slice to chroma's iterator protocol.
func tokenIterator(tokens []chroma.Token) chroma.Iterator {
	i := 0
	return func() chroma.Token {
		if i >= len(tokens) {
			return chroma.EOF
		}
		t := tokens[i]
		i++
		return t
	}
}
