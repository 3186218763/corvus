package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestHighlightProseTextUsesSemanticColorsAndPreservesText(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	budget := newProseHighlightBudget()
	input := "The renderer parsed the cache and passed the API check."
	got := highlightProseText(input, &budget)
	if plain := ansi.Strip(got); plain != input {
		t.Fatalf("visible text changed: %q", plain)
	}
	for _, want := range []string{
		fgSGR(activeCLITheme.secondary) + "renderer",
		fgSGR(activeCLITheme.secondary) + "cache",
		fgSGR(activeCLITheme.success) + "passed",
		fgSGR(activeCLITheme.secondary) + "API",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing semantic color %q in %q", want, got)
		}
	}
}

func TestHighlightProseTextMatchesChineseAndStructuredTokens(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	budget := newProseHighlightBudget()
	input := "通过 internal/cli/md.go 调用 Function() 和 pkg.Symbol，失败时重试。"
	got := highlightProseText(input, &budget)
	if plain := ansi.Strip(got); plain != input {
		t.Fatalf("visible text changed: %q", plain)
	}
	for _, want := range []string{
		fgSGR(activeCLITheme.success) + "通过",
		fgSGR(activeCLITheme.accent) + "internal/cli/md.go",
		fgSGR(activeCLITheme.accent) + "Function()",
		fgSGR(activeCLITheme.accent) + "pkg.Symbol",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing structured color %q in %q", want, got)
		}
	}
}

func TestHighlightProseTextMatchesCompleteFilenameAndAbsolutePath(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	budget := newProseHighlightBudget()
	input := "Update theme.go before /srv/corvus/internal/cli/md.go."
	got := highlightProseText(input, &budget)
	if plain := ansi.Strip(got); plain != input {
		t.Fatalf("visible text changed: %q", plain)
	}
	for _, want := range []string{
		fgSGR(activeCLITheme.accent) + "theme.go",
		fgSGR(activeCLITheme.accent) + "/srv/corvus/internal/cli/md.go",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing complete structured token %q in %q", want, got)
		}
	}
}

func TestHighlightProseTextPreservesUnicodeBeforeASCIIKeyword(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	budget := newProseHighlightBudget()
	input := "\u212A renderer is ready."
	got := highlightProseText(input, &budget)
	if plain := ansi.Strip(got); plain != input {
		t.Fatalf("visible text changed: got %q, want %q", plain, input)
	}
	if !strings.Contains(got, fgSGR(activeCLITheme.secondary)+"renderer") {
		t.Fatalf("renderer was not highlighted after Unicode text: %q", got)
	}
}

func TestHighlightProseTextCapsMatchesAndDeduplicates(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	budget := newProseHighlightBudget()
	got := highlightProseText("renderer parser cache API TUI model tool renderer parser", &budget)
	if gotCount := strings.Count(got, "\x1b[38;"); gotCount > maxProseHighlights {
		t.Fatalf("more than four colored fragments emitted: %q", got)
	}
	if strings.Count(got, fgSGR(activeCLITheme.secondary)+"renderer") != 1 {
		t.Fatalf("duplicate renderer should be plain after first match: %q", got)
	}
}

func TestHighlightProseTextNoColorIsExact(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	input := "renderer 通过 internal/cli/md.go"
	if got := highlightProseText(input, nil); got != input {
		t.Fatalf("NO_COLOR text changed: %q", got)
	}
}

func TestHighlightProseTextAdaptsToLightAndTrueColor(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	for _, tc := range []struct {
		name    string
		profile colorprofile.Profile
		mode    string
	}{
		{name: "dark-256", profile: colorprofile.ANSI256, mode: "dark"},
		{name: "light-256", profile: colorprofile.ANSI256, mode: "light"},
		{name: "dark-truecolor", profile: colorprofile.TrueColor, mode: "dark"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			activeColorProfile = tc.profile
			configureCLITheme(tc.mode)
			budget := newProseHighlightBudget()
			got := highlightProseText("renderer passed", &budget)
			if !strings.Contains(got, fgSGR(activeCLITheme.secondary)+"renderer") {
				t.Fatalf("renderer color did not follow %s: %q", tc.name, got)
			}
		})
	}
}
