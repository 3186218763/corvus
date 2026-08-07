package cli

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"corvus/internal/event"
)

func TestDiffBodyDropsHeadersKeepsLineNumbers(t *testing.T) {
	d := event.FileDiff{Diff: "--- a/x.go\n+++ b/x.go\n@@ -7 +7 @@\n-old\n+new\n", Added: 1, Removed: 1}
	joined := strings.Join(diffBody(d, "x.go", 80, 40), "\n")
	if strings.Contains(joined, "--- a/") || strings.Contains(joined, "+++ b/") || strings.Contains(joined, "@@") {
		t.Fatalf("file/hunk headers should be dropped, got:\n%s", joined)
	}
	for _, want := range []string{"old", "new", "7"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q (code or line number) in:\n%s", want, joined)
		}
	}
}

func TestDiffBodyFolds(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- a/x\n+++ b/x\n@@ -1,8 +1,8 @@\n")
	for i := 0; i < 8; i++ {
		b.WriteString("+line\n")
	}
	body := diffBody(event.FileDiff{Diff: b.String()}, "x", 80, 5)
	if len(body) != 5 {
		t.Fatalf("want 5 rows (4 content + footer), got %d:\n%s", len(body), strings.Join(body, "\n"))
	}
	// 8 rendered add rows minus the 4 kept = 4 folded.
	if !strings.Contains(body[len(body)-1], "4") {
		t.Fatalf("footer should report 4 folded lines, got %q", body[len(body)-1])
	}
}

func TestDiffBodyNoFoldWhenShort(t *testing.T) {
	d := event.FileDiff{Diff: "@@ -1 +1 @@\n+a\n"}
	if got := len(diffBody(d, "x", 80, 40)); got != 1 {
		t.Fatalf("want 1 unfolded row, got %d", got)
	}
}

func TestDiffBlockHeader(t *testing.T) {
	d := event.FileDiff{Diff: "@@ -1 +1 @@\n-a\n+b\n", Added: 1, Removed: 1}
	block := diffBlock("edit_file", `{"path":"pkg/x.go"}`, d, 80, 40)
	if len(block) == 0 || !strings.Contains(block[0], "Edited") || !strings.Contains(block[0], "pkg/x.go") {
		t.Fatalf("header should name verb + path, got %q", block[0])
	}
}

func TestDiffBlockNilWithoutDiff(t *testing.T) {
	if diffBlock("write_file", `{"path":"x"}`, event.FileDiff{}, 80, 40) != nil {
		t.Fatal("no diff should yield no block")
	}
}

func TestToolHeadUsesCategoryAndArgColors(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	head := toolHead("write_file", "x.go", 80)
	if !strings.Contains(head, fgSGR(activeCLITheme.success)) {
		t.Fatalf("write diff header verb should carry success SGR, got %q", head)
	}
	if !strings.Contains(head, ansiBold) {
		t.Fatalf("write diff header verb should stay bold, got %q", head)
	}
	if !strings.Contains(head, fgSGR(activeCLITheme.toolArg)) {
		t.Fatalf("diff header path should carry toolArg SGR, got %q", head)
	}
}

func TestDiffPath(t *testing.T) {
	if got := diffPath(`{"path":"a/b.go","old_string":"x"}`); got != "a/b.go" {
		t.Fatalf("got %q", got)
	}
	if got := diffPath(`not json`); got != "" {
		t.Fatalf("malformed args should yield empty path, got %q", got)
	}
}

func TestDiffBarReappliesBackground(t *testing.T) {
	defer func(prev colorprofile.Profile) { activeColorProfile = prev }(activeColorProfile)
	activeColorProfile = colorprofile.ANSI256

	line := diffBar('+', "a + b", "x.go", 40, bgSGR(activeCLITheme.diffAddBG), fgSGR(activeCLITheme.success), 12, 3)
	// Syntax highlighting emits multiple \033[0m resets; each must re-arm the bar
	// background, so the bg sequence appears more than once and the row ends reset.
	if strings.Count(line, bgSGR(activeCLITheme.diffAddBG)) < 2 {
		t.Fatalf("background not re-applied after chroma resets: %q", line)
	}
	if !strings.HasSuffix(line, ansiReset) {
		t.Fatalf("row should end with a reset: %q", line)
	}
}

func TestActiveDiffChromaStyleFollowsCLITheme(t *testing.T) {
	previous := activeCLITheme
	defer func() { activeCLITheme = previous }()

	tests := []struct {
		name  string
		theme cliPalette
		want  string
	}{
		{name: "dark", theme: cliDarkTheme, want: "catppuccin-mocha"},
		{name: "light", theme: cliLightTheme, want: "catppuccin-latte"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activeCLITheme = tt.theme
			if got := activeDiffChromaStyle().Name; got != tt.want {
				t.Fatalf("diff syntax style = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHighlightCodeUpdatesOnThemeSwitch(t *testing.T) {
	previousTheme := activeCLITheme
	previousProfile := activeColorProfile
	defer func() {
		activeCLITheme = previousTheme
		activeColorProfile = previousProfile
	}()
	activeColorProfile = colorprofile.ANSI256

	code := `const answer = "value"`
	activeCLITheme = cliLightTheme
	light := highlightCode("example.ts", code)
	activeCLITheme = cliDarkTheme
	dark := highlightCode("example.ts", code)

	if light == dark {
		t.Fatalf("light and dark themes produced identical highlighting: %q", light)
	}
	for name, got := range map[string]string{"light": light, "dark": dark} {
		if plain := ansi.Strip(got); plain != code {
			t.Fatalf("%s theme changed code text: got %q, want %q", name, plain, code)
		}
	}
}

func TestTokeniseBashFlags(t *testing.T) {
	tokens := tokeniseBash(`git add . && git commit -m "fix" --no-verify`)
	var flags []string
	for _, tk := range tokens {
		if tk.Type == chroma.NameAttribute {
			flags = append(flags, tk.Value)
		}
	}
	found := false
	for _, f := range flags {
		if f == "--no-verify" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --no-verify re-tagged as NameAttribute, flags=%v tokens=%+v", flags, tokens)
	}
}

func TestTokeniseBashSkipsFlagsInsideStrings(t *testing.T) {
	for _, tk := range tokeniseBash(`echo "--keep" '--also'`) {
		if tk.Type == chroma.NameAttribute {
			t.Fatalf("flag inside a quoted string must stay plain: %+v", tk)
		}
	}
}

func TestTokeniseBashLeavesOperatorTokens(t *testing.T) {
	tokens := tokeniseBash(`a && b || c`)
	ops := 0
	for _, tk := range tokens {
		if tk.Type == chroma.Operator {
			ops++
		}
	}
	if ops != 2 {
		t.Fatalf("expected 2 operator tokens (&&, ||), got %d: %+v", ops, tokens)
	}
}

func TestHighlightBashPreservesTextAndAddsColor(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	cmd := `git add . && git commit -m "fix" --no-verify`
	got := highlightBash(cmd)
	if plain := ansi.Strip(got); plain != cmd {
		t.Fatalf("highlight changed command text: got %q, want %q", plain, cmd)
	}
	if !strings.Contains(got, "\033[") {
		t.Fatalf("expected SGR colours, got %q", got)
	}
}

func TestHighlightBashPlainWithoutColor(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ASCII

	cmd := `echo hi && echo there`
	if got := highlightBash(cmd); got != cmd {
		t.Fatalf("no-colour terminal should pass text through, got %q", got)
	}
}

func TestHighlightBashEmpty(t *testing.T) {
	if got := highlightBash(""); got != "" {
		t.Fatalf("empty command should stay empty, got %q", got)
	}
}

func TestFileVerb(t *testing.T) {
	cases := []struct {
		d    event.FileDiff
		want string
	}{
		{d: event.FileDiff{Added: 3}, want: "Added"},
		{d: event.FileDiff{Removed: 2}, want: "Deleted"},
		{d: event.FileDiff{Added: 1, Removed: 1}, want: "Edited"},
		{d: event.FileDiff{}, want: "Edited"},
	}
	for _, c := range cases {
		if got := fileVerb(c.d); got != c.want {
			t.Fatalf("fileVerb(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestDiffBlockCodexHeader(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	d := event.FileDiff{Diff: "@@ -1 +1 @@\n-a\n+b\n", Added: 1, Removed: 1}
	block := diffBlock("edit_file", `{"path":"pkg/x.go"}`, d, 80, 40)
	h := block[0]
	plain := ansi.Strip(h)
	for _, want := range []string{"Edited", "pkg/x.go", "(+1", "-1)"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("header %q missing %q", h, want)
		}
	}
	if !strings.Contains(h, fgSGR(activeCLITheme.success)) || !strings.Contains(h, fgSGR(activeCLITheme.err)) {
		t.Fatalf("stat sides should carry green/red SGR, got %q", h)
	}
	if !strings.Contains(h, ansiBold) {
		t.Fatalf("verb should be bold, got %q", h)
	}
}

func TestDiffBlockCodexHeaderPureAdd(t *testing.T) {
	d := event.FileDiff{Diff: "@@ -0,0 +1 @@\n+package main\n", Added: 1}
	block := diffBlock("write_file", `{"path":"new.go"}`, d, 80, 40)
	if !strings.Contains(block[0], "Added") || !strings.Contains(block[0], "(+1 -0)") {
		t.Fatalf("pure add should read 'Added ... (+1 -0)', got %q", block[0])
	}
}

func TestDiffBlockCodexHeaderPureDelete(t *testing.T) {
	d := event.FileDiff{Diff: "@@ -1 +0,0 @@\n-old\n", Removed: 1}
	block := diffBlock("delete_file", `{"path":"old.go"}`, d, 80, 40)
	if !strings.Contains(block[0], "Deleted") || !strings.Contains(block[0], "(+0 -1)") {
		t.Fatalf("pure delete should read 'Deleted ... (+0 -1)', got %q", block[0])
	}
}

func TestDiffBarDimsDeleteContent(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	line := diffBar('-', "const x = 1", "x.go", 40, bgSGR(activeCLITheme.diffDelBG), fgSGR(activeCLITheme.err), 1, 2)
	if !strings.Contains(line, ansiDim) {
		t.Fatalf("delete row should dim its content, got %q", line)
	}
	// The dim must survive chroma resets inside the highlighted code.
	if strings.Count(line, ansiDim) < 2 {
		t.Fatalf("dim not re-armed after chroma resets, got %q", line)
	}
}

func TestDiffBarAddNotDimmed(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	line := diffBar('+', "func main() {}", "x.go", 40, bgSGR(activeCLITheme.diffAddBG), fgSGR(activeCLITheme.success), 1, 2)
	if strings.Contains(line, ansiDim) {
		t.Fatalf("add row should not be dimmed, got %q", line)
	}
}
