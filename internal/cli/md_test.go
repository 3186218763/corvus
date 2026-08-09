package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// TestRenderEmpty covers the contract that empty / whitespace-only input
// returns "" — callers rely on this to skip a redraw when there's nothing
// substantive to show.
func TestRenderEmpty(t *testing.T) {
	r := newMarkdownRenderer(80)
	for _, in := range []string{"", " ", "\n", "\t\n  \n"} {
		if got := r.Render(in); got != "" {
			t.Errorf("Render(%q) = %q, want empty", in, got)
		}
	}
}

// TestRenderConstructsRound-trip checks each major construct emits something
// styled while preserving the underlying text. We don't assert exact ANSI
// sequences (palette could shift) — only that key visible text survives and
// that we don't degrade to literal markdown.
func TestRenderConstructs(t *testing.T) {
	r := newMarkdownRenderer(80)
	cases := []struct {
		name     string
		in       string
		contains []string
		notRaw   []string // substrings that must NOT appear (raw markdown leaking through)
		strip    bool     // assert on ANSI-stripped output (token styling splits plain text)
	}{
		{
			name:     "heading",
			in:       "# Hello\n",
			contains: []string{"Hello"},
			notRaw:   []string{"# Hello", "## "},
		},
		{
			name:     "heading h2 drops prefix",
			in:       "## Section\n",
			contains: []string{"Section"},
			notRaw:   []string{"## ", "###"},
		},
		{
			name:     "bold",
			in:       "this is **important** text",
			contains: []string{"important", "this is", "text"},
		},
		{
			name:     "italic",
			in:       "see *here* for details",
			contains: []string{"here", "see", "for details"},
		},
		{
			name:     "code span",
			in:       "use `os.Setenv` to set",
			contains: []string{"os.Setenv", "use", "to set"},
		},
		{
			name:     "unordered list",
			in:       "- one\n- two\n- three\n",
			contains: []string{"one", "two", "three", "•"},
		},
		{
			name:     "ordered list",
			in:       "1. first\n2. second\n",
			contains: []string{"first", "second", "1.", "2."},
		},
		{
			name:     "fenced code",
			in:       "```go\nfunc main() {}\n```\n",
			contains: []string{"func main()"},
			strip:    true,
		},
		{
			name:     "thematic break",
			in:       "above\n\n---\n\nbelow",
			contains: []string{"above", "below", "─"},
		},
		{
			name:     "gfm table",
			in:       "| name | size |\n|------|------|\n| a    | 12   |\n| bb   | 345  |\n",
			contains: []string{"name", "size", "a", "12", "bb", "345", "│"},
			notRaw:   []string{"|------|"}, // raw separator must be transformed
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := r.Render(tc.in)
			if tc.strip {
				out = ansi.Strip(out)
			}
			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Errorf("Render(%q) missing %q\n--- output ---\n%s", tc.in, want, out)
				}
			}
			for _, leak := range tc.notRaw {
				if strings.Contains(out, leak) {
					t.Errorf("Render(%q) leaked raw markdown %q", tc.in, leak)
				}
			}
		})
	}
}

func TestNarrowTableFitsRendererWidthWithoutLosingCells(t *testing.T) {
	const width = 10
	input := "| alpha | beta | gamma |\n|---|---|---|\n| one | two | three |\n"
	out := newMarkdownRenderer(width).Render(input)

	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if got := visibleWidth(line); got > width {
			t.Fatalf("rendered line %d width = %d, want <= %d: %q", i, got, width, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(out)
	for _, want := range []string{"alpha", "beta", "gamma", "one", "two", "three"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("narrow table lost cell %q:\n%s", want, plain)
		}
	}
}

func TestHighlightCodeLine(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "keyword and plain text",
			in:   "func main() {}",
			want: "\x1b[38;5;176mfunc\x1b[0m\x1b[38;5;253m main() {}\x1b[0m",
		},
		{
			name: "number before comment",
			in:   "x := 1 // note",
			want: "\x1b[38;5;253mx := \x1b[0m\x1b[38;5;179m1\x1b[0m\x1b[38;5;253m \x1b[0m\x1b[38;5;66m// note\x1b[0m",
		},
		{
			name: "double-quoted string shields comment chars",
			in:   `s := "// note"`,
			want: "\x1b[38;5;253ms := \x1b[0m\x1b[38;5;149m\"// note\"\x1b[0m",
		},
		{
			name: "single-quoted string",
			in:   "r := 'a'",
			want: "\x1b[38;5;253mr := \x1b[0m\x1b[38;5;149m'a'\x1b[0m",
		},
		{
			name: "backtick string shields keyword",
			in:   "x := `func`",
			want: "\x1b[38;5;253mx := \x1b[0m\x1b[38;5;149m`func`\x1b[0m",
		},
		{
			name: "CJK stays intact",
			in:   "fmt.Println(\"你好 世界\")",
			want: "\x1b[38;5;253mfmt.Println(\x1b[0m\x1b[38;5;149m\"你好 世界\"\x1b[0m\x1b[38;5;253m)\x1b[0m",
		},
		{
			name: "hash comment at line start",
			in:   "# todo",
			want: "\x1b[38;5;66m# todo\x1b[0m",
		},
		{
			name: "keywords and literals",
			in:   "return err == nil",
			want: "\x1b[38;5;176mreturn\x1b[0m\x1b[38;5;253m err == \x1b[0m\x1b[38;5;176mnil\x1b[0m",
		},
		{
			name: "hex number",
			in:   "total := count + 0x1F",
			want: "\x1b[38;5;253mtotal := count + \x1b[0m\x1b[38;5;179m0x1F\x1b[0m",
		},
		{
			name: "empty line",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := highlightCodeLine(tc.in); got != tc.want {
				t.Fatalf("highlightCodeLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestWrapAnsiCJK proves the wrap counter treats CJK as 2 cols, so a line of
// Chinese characters wraps at half the column count.
func TestWrapAnsiCJK(t *testing.T) {
	// Width 10 = room for 5 Chinese characters per row.
	in := strings.Repeat("中", 8)
	out := wrapAnsi(in, 10)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrap, got 1 line: %q", out)
	}
	if visibleWidth(lines[0]) > 10 {
		t.Errorf("first line exceeds width: %d > 10", visibleWidth(lines[0]))
	}
}

// TestTableCodeSpanNeutral proves inline code inside a table cell renders with
// the muted theme color rather than the accent used elsewhere.
func TestTableCodeSpanNeutral(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	r := newMarkdownRenderer(80)
	md := "| lang | code |\n| --- | --- |\n| go | `fmt.Println` |\n"
	got := r.Render(md)
	accentEsc := fgSGR(activeCLITheme.accent)
	mutedEsc := fgSGR(activeCLITheme.muted)
	if strings.Contains(got, accentEsc+"fmt.Println") {
		t.Fatalf("table code span must not use accent:\n%s", got)
	}
	if !strings.Contains(got, mutedEsc+"fmt.Println") {
		t.Fatalf("table code span should use the muted theme color:\n%s", got)
	}
}

// TestInlineCodeSpanStillAccentOutsideTable guards the non-table path: inline
// code outside tables keeps the accent style.
func TestInlineCodeSpanStillAccentOutsideTable(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	r := newMarkdownRenderer(80)
	got := r.Render("use `os.Exit` here")
	accentEsc := fgSGR(activeCLITheme.accent)
	if !strings.Contains(got, accentEsc+"os.Exit") {
		t.Fatalf("inline code outside tables must keep accent:\n%s", got)
	}
}

func TestMarkdownProseKeywordsSkipCodeSpansAndFences(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	r := newMarkdownRenderer(80)
	got := r.Render("The renderer passed; use `renderer` here.\n\n```go\nrenderer()\n```")
	secondaryEsc := fgSGR(activeCLITheme.secondary)
	accentEsc := fgSGR(activeCLITheme.accent)
	if count := strings.Count(got, secondaryEsc+"renderer"); count != 1 {
		t.Fatalf("prose should color 'renderer' once, got %d: %q", count, got)
	}
	if !strings.Contains(got, accentEsc+"renderer") {
		t.Fatalf("inline code should retain accent styling: %q", got)
	}
}

func TestMarkdownProseKeywordBudgetResetsPerParagraph(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	r := newMarkdownRenderer(80)
	got := r.Render("renderer parser cache API TUI model tool.\n\nrenderer is ready.")
	secondaryEsc := fgSGR(activeCLITheme.secondary)
	if count := strings.Count(got, secondaryEsc+"renderer"); count != 2 {
		t.Fatalf("each paragraph should get a fresh budget, got renderer-colored=%d: %q", count, got)
	}
}

func TestMarkdownProseHighlightDoesNotChangeWidthOrCopyText(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	r := newMarkdownRenderer(18)
	input := "renderer 通过 internal/cli/md.go"
	view := r.Render(input)
	for _, line := range strings.Split(strings.TrimRight(view, "\n"), "\n") {
		if got := visibleWidth(line); got > 18 {
			t.Fatalf("line width = %d > 18: %q", got, ansi.Strip(line))
		}
	}
	copy := r.RenderCopy(input, "copy")
	if ansi.Strip(copy) != ansi.Strip(view) {
		t.Fatalf("copy/render visible text differ:\ncopy: %q\nview: %q", ansi.Strip(copy), ansi.Strip(view))
	}
}
