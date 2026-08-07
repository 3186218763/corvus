package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"corvus/internal/control"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestConfigureCLIThemeSwitchesModeAndDefaultStyle(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	configureCLITheme("light")
	if activeCLITheme.name != "light" || activeCLITheme.style != "sandstone" {
		t.Fatalf("light theme = %s/%s, want light/sandstone", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, "\033[38;5;173m") {
		t.Fatalf("light default accent = %q, want sandstone xterm 173", got)
	}

	configureCLITheme("dark")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "graphite" {
		t.Fatalf("dark theme = %s/%s, want dark/graphite", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, ansiAccent) {
		t.Fatalf("dark accent = %q, want %q", got, ansiAccent)
	}
}

func TestConfigureCLIThemeStyleOverride(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	configureCLIThemeWithStyle("dark", "aurora")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "aurora" {
		t.Fatalf("theme = %s/%s, want dark/aurora", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, "\033[38;5;79m") {
		t.Fatalf("aurora accent = %q, want xterm 79", got)
	}

	configureCLITheme("glacier")
	if activeCLITheme.name != "light" || activeCLITheme.style != "glacier" {
		t.Fatalf("theme style command resolved %s/%s, want light/glacier", activeCLITheme.name, activeCLITheme.style)
	}
}

func TestConfigureCLIThemeHonorsEnvOverride(t *testing.T) {
	t.Setenv("CORVUS_THEME", "ember")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	configureCLIThemeWithStyle("light", "glacier")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "ember" {
		t.Fatalf("CORVUS_THEME override resolved %s/%s, want dark/ember", activeCLITheme.name, activeCLITheme.style)
	}
}

func TestThemeRendersAtProfileFidelity(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	configureCLIThemeWithStyle("dark", "graphite")

	activeColorProfile = colorprofile.TrueColor
	if got := accent("x"); !strings.HasPrefix(got, "\033[38;2;217;119;87m") {
		t.Fatalf("truecolor accent = %q, want 24-bit #d97757", got)
	}

	activeColorProfile = colorprofile.ANSI256
	if got := accent("x"); !strings.HasPrefix(got, ansiAccent) {
		t.Fatalf("256-colour accent = %q, want %q", got, ansiAccent)
	}

	activeColorProfile = colorprofile.NoTTY
	if got := accent("x"); got != "x" {
		t.Fatalf("no-tty accent = %q, want unstyled text", got)
	}
}

func TestThemeArgCompletion(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLIThemeWithStyle("dark", "graphite")

	m := newTestChatTUI()
	items, _, ok := m.slashArgItems("/theme ")
	if !ok || len(items) == 0 {
		t.Fatalf("/theme arg completion should offer themes, ok=%v n=%d", ok, len(items))
	}
	if !hasLabel(items, "auto") || !hasLabel(items, "graphite") || !hasLabel(items, "aurora") {
		t.Fatalf("/theme completion missing expected themes: %v", labels(items))
	}
}

func TestRunThemeSubcommandSwitchesAccentAndTextarea(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLIThemeWithStyle("dark", "graphite")

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{})
	if cmd := m.runThemeSubcommand("/theme aurora"); cmd == nil {
		t.Fatal("a real theme change should start the sweep")
	}
	if activeCLITheme.name != "dark" || activeCLITheme.style != "aurora" {
		t.Fatalf("current theme = %s/%s, want dark/aurora", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, "\033[38;5;79m") {
		t.Fatalf("accent = %q, want aurora xterm color", got)
	}
	if m.input.Styles().Cursor.Color == nil {
		t.Fatal("textarea cursor color was not refreshed")
	}
}

func TestParseOSC11Response(t *testing.T) {
	for _, tt := range []struct {
		name  string
		in    string
		want  terminalRGB
		light bool
	}{
		{
			name:  "black-rgb",
			in:    "\x1b]11;rgb:0000/0000/0000\a",
			want:  terminalRGB{0, 0, 0},
			light: false,
		},
		{
			name:  "white-rgb",
			in:    "\x1b]11;rgb:ffff/ffff/ffff\x1b\\",
			want:  terminalRGB{255, 255, 255},
			light: true,
		},
		{
			name:  "hex",
			in:    "\x1b]11;#f8f8f8\a",
			want:  terminalRGB{248, 248, 248},
			light: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseOSC11Response(tt.in)
			if !ok {
				t.Fatalf("parseOSC11Response returned !ok")
			}
			if got != tt.want {
				t.Fatalf("rgb = %+v, want %+v", got, tt.want)
			}
			if got.looksLight() != tt.light {
				t.Fatalf("looksLight = %v, want %v", got.looksLight(), tt.light)
			}
		})
	}
}

func TestAutoThemeFallsBackToColorFGBG(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY

	t.Setenv("COLORFGBG", "0;15")
	if got := resolveCLITheme("auto").name; got != "light" {
		t.Fatalf("COLORFGBG light fallback resolved %q, want light", got)
	}

	t.Setenv("COLORFGBG", "15;0")
	if got := resolveCLITheme("auto").name; got != "dark" {
		t.Fatalf("COLORFGBG dark fallback resolved %q, want dark", got)
	}
}

func TestApplyTextareaThemeClearsCursorLineBackground(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	for _, mode := range []string{"dark", "light", "auto"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "auto" {
				t.Setenv("COLORFGBG", "0;15")
			} else {
				t.Setenv("COLORFGBG", "")
			}
			configureCLITheme(mode)

			ti := textarea.New()
			applyTextareaTheme(&ti)
			styles := ti.Styles()
			emptyBG := lipgloss.NewStyle().GetBackground()

			if bg := styles.Focused.CursorLine.GetBackground(); !reflect.DeepEqual(bg, emptyBG) {
				t.Fatalf("focused cursor line background = %v, want empty", bg)
			}
			if bg := styles.Blurred.CursorLine.GetBackground(); !reflect.DeepEqual(bg, emptyBG) {
				t.Fatalf("blurred cursor line background = %v, want empty", bg)
			}
			if bg := styles.Focused.EndOfBuffer.GetBackground(); !reflect.DeepEqual(bg, emptyBG) {
				t.Fatalf("end-of-buffer background = %v, want empty", bg)
			}
			if styles.Cursor.Color == nil {
				t.Fatal("cursor color is nil with color enabled")
			}
		})
	}
}

func TestApplyTextareaThemeHonorsCursorShape(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	prevShape := cliCursorShape
	defer func() { cliCursorShape = prevShape }()
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	for _, tt := range []struct {
		name string
		in   string
		want tea.CursorShape
	}{
		{name: "default", in: "", want: tea.CursorBar},
		{name: "underline", in: "underline", want: tea.CursorUnderline},
		{name: "block", in: "block", want: tea.CursorBlock},
		{name: "bar", in: "bar", want: tea.CursorBar},
		{name: "unknown", in: "unknown", want: tea.CursorBar},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cliCursorShape = tt.in
			ti := textarea.New()
			applyTextareaTheme(&ti)
			if got := ti.Styles().Cursor.Shape; got != tt.want {
				t.Fatalf("cursor shape = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComposerTintAndCursorFollowTheme(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	for _, theme := range cliThemeStyles {
		t.Run(theme.name, func(t *testing.T) {
			configureCLITheme(theme.name)
			wantTint := cliColor{"#eceff4", 255}
			if theme.mode == "dark" {
				wantTint = cliColor{"#1c2028", 234}
			}
			if got := activeCLITheme.inputBoxBG; !reflect.DeepEqual(got, wantTint) {
				t.Fatalf("%s inputBoxBG = %v, want %v", theme.name, got, wantTint)
			}
			if got := inputBoxStyle.GetBorderTop(); got {
				t.Fatalf("inputBoxStyle must not keep a top border (painter owns the tint), got %v", got)
			}
			if got := inputBoxStyle.GetBorderBottom(); got {
				t.Fatalf("inputBoxStyle must not keep a bottom border (painter owns the tint), got %v", got)
			}
			if got := inputBoxStyle.GetBackground(); !reflect.DeepEqual(got, lipgloss.Color("")) {
				t.Fatalf("inputBoxStyle must not carry a lipgloss background (painter owns the tint), got %v", got)
			}
			want := themeLipColor(activeCLITheme.accent)
			ti := textarea.New()
			applyTextareaTheme(&ti)
			if got := ti.Styles().Cursor.Color; !reflect.DeepEqual(got, want) {
				t.Fatalf("composer cursor color = %v, want theme accent %v", got, want)
			}
		})
	}

	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	if got := activeCLITheme.inputBoxBG; !reflect.DeepEqual(got, cliColor{"#1c2028", 234}) {
		t.Fatalf("dark theme must keep its tint slot even under NO_COLOR, got %v", got)
	}
	if got := inputBoxStyle.GetPaddingLeft(); got != 1 {
		t.Fatalf("NO_COLOR inputBoxStyle padding-left = %d, want 1", got)
	}
	if got := inputBoxStyle.GetBorderTop(); got {
		t.Fatalf("NO_COLOR inputBoxStyle must stay borderless, got top border %v", got)
	}
	if got := inputBoxStyle.GetBorderBottom(); got {
		t.Fatalf("NO_COLOR inputBoxStyle must stay borderless, got bottom border %v", got)
	}
}

// TestRuntimeAutoThemeDoesNotProbeStdin guards the fix for a runtime `/theme auto`
// that live-probed the terminal (raw-mode stdin read) while the TUI owned stdin,
// racing bubbletea's input reader. The switch must resolve via the COLORFGBG
// fallback instead, never invoking the probe.
func TestRuntimeAutoThemeDoesNotProbeStdin(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	t.Setenv("COLORFGBG", "15;0")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	probed := false
	defer func(prev func() (terminalRGB, bool)) { terminalProbe = prev }(terminalProbe)
	terminalProbe = func() (terminalRGB, bool) {
		probed = true
		return terminalRGB{255, 255, 255}, true
	}

	if got := setCLIThemeMode("auto").name; got != "dark" {
		t.Fatalf("auto with COLORFGBG=15;0 resolved %q, want dark", got)
	}
	if probed {
		t.Fatal("runtime /theme auto probed the terminal while the TUI owns stdin")
	}

	withTerminalProbe(func() {
		if got := resolveCLITheme("auto").name; got != "light" {
			t.Fatalf("opted-in probe resolved %q, want light", got)
		}
	})
	if !probed {
		t.Fatal("withTerminalProbe should be the one path that reaches the terminal")
	}
}

// TestThemeHierarchyBodyBrighterThanChromeBorder locks body-vs-chrome contrast:
// muted (body-adjacent values) must stay distinct from quiet border chrome, and
// borders stay low-chroma so tool cards / footer rules do not shout.
func TestThemeHierarchyBodyBrighterThanChromeBorder(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	for _, mode := range []string{"dark", "light"} {
		t.Run(mode, func(t *testing.T) {
			configureCLITheme(mode)
			th := activeCLITheme
			if th.muted.hex == th.border.hex {
				t.Fatalf("%s muted hex equals border hex %q — body and chrome must differ", mode, th.muted.hex)
			}
			if th.muted.hex == th.subtle.hex {
				t.Fatalf("%s muted hex equals subtle hex %q — body values and chrome labels must differ", mode, th.muted.hex)
			}
			if th.subtle.hex == th.border.hex {
				t.Fatalf("%s subtle hex equals border hex %q — chrome layers must stay distinct", mode, th.subtle.hex)
			}

			mutedL := cliColorLuma(th.muted)
			subtleL := cliColorLuma(th.subtle)
			borderL := cliColorLuma(th.border)
			if mode == "dark" {
				// Dark shell: body-adjacent text is brighter than chrome labels/borders.
				if mutedL <= subtleL {
					t.Fatalf("dark muted luma %.1f should exceed subtle chrome %.1f", mutedL, subtleL)
				}
				if mutedL <= borderL {
					t.Fatalf("dark muted luma %.1f should exceed border chrome %.1f", mutedL, borderL)
				}
				if subtleL <= borderL {
					t.Fatalf("dark subtle luma %.1f should exceed border %.1f", subtleL, borderL)
				}
			} else {
				// Light shell: body text is darker (higher contrast) than quiet chrome.
				if mutedL >= subtleL {
					t.Fatalf("light muted luma %.1f should be darker than subtle chrome %.1f", mutedL, subtleL)
				}
				if mutedL >= borderL {
					t.Fatalf("light muted luma %.1f should be darker than border chrome %.1f", mutedL, borderL)
				}
				if subtleL >= borderL {
					t.Fatalf("light subtle luma %.1f should be darker than border %.1f", subtleL, borderL)
				}
			}

			// Border stays low-chroma (near-neutral grey) so chrome is quiet.
			if ch := cliColorChroma(th.border); ch > 30 {
				t.Fatalf("%s border chroma %d too high for quiet chrome (hex %s)", mode, ch, th.border.hex)
			}
		})
	}
}

func cliColorLuma(c cliColor) float64 {
	r, g, b, ok := parseHexColor(c.hex)
	if !ok {
		return -1
	}
	return 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
}

func cliColorChroma(c cliColor) int {
	r, g, b, ok := parseHexColor(c.hex)
	if !ok {
		return 999
	}
	maxC, minC := r, r
	if g > maxC {
		maxC = g
	}
	if b > maxC {
		maxC = b
	}
	if g < minC {
		minC = g
	}
	if b < minC {
		minC = b
	}
	return maxC - minC
}

func restoreThemeForTest(prevColor colorprofile.Profile, prevTheme cliPalette) {
	activeColorProfile = prevColor
	activeCLITheme = prevTheme
	refreshCLIStyles()
}

func TestUserBubbleFadedFollowsAccent(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)

	configureCLIThemeWithStyle("dark", "graphite")
	if activeCLITheme.userBubbleFaded.hex == activeCLITheme.accent.hex {
		t.Fatalf("faded must differ from accent: %s", activeCLITheme.userBubbleFaded.hex)
	}
	if got, want := activeCLITheme.userBubbleFaded.hex, "#a87c6e"; got != want {
		t.Fatalf("graphite faded hex = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.userBubbleFaded.xterm, 95; got != want {
		t.Fatalf("graphite faded xterm = %d, want 95", got)
	}
	// Faded must stay distinguishable from the quiet chrome (spec §8
	// three-tier distinguishability: accent / userBubbleFaded / faint).
	if activeCLITheme.userBubbleFaded.hex == activeCLITheme.faint.hex {
		t.Fatalf("faded must differ from faint: %s", activeCLITheme.userBubbleFaded.hex)
	}
	if activeCLITheme.userBubbleFaded.hex == activeCLITheme.muted.hex {
		t.Fatalf("faded must differ from muted: %s", activeCLITheme.userBubbleFaded.hex)
	}

	configureCLIThemeWithStyle("dark", "ember")
	if got, want := activeCLITheme.userBubbleFaded.xterm, 131; got != want {
		t.Fatalf("ember faded xterm = %d, want 131", got)
	}
	configureCLIThemeWithStyle("dark", "aurora")
	if got, want := activeCLITheme.userBubbleFaded.hex, "#5e9e91"; got != want {
		t.Fatalf("aurora faded hex = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.userBubbleFaded.xterm, 72; got != want {
		t.Fatalf("aurora faded xterm = %d, want 72", got)
	}
	configureCLIThemeWithStyle("dark", "midnight")
	if got, want := activeCLITheme.userBubbleFaded.xterm, 140; got != want {
		t.Fatalf("midnight faded xterm = %d, want 140", got)
	}

	configureCLIThemeWithStyle("light", "sandstone")
	if got, want := activeCLITheme.userBubbleFaded.hex, "#9e7263"; got != want {
		t.Fatalf("sandstone faded hex = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.userBubbleFaded.xterm, 95; got != want {
		t.Fatalf("sandstone faded xterm = %d, want 95", got)
	}
	configureCLIThemeWithStyle("light", "porcelain")
	if got, want := activeCLITheme.userBubbleFaded.xterm, 103; got != want {
		t.Fatalf("porcelain faded xterm = %d, want 103", got)
	}
	configureCLIThemeWithStyle("light", "linen")
	if got, want := activeCLITheme.userBubbleFaded.xterm, 131; got != want {
		t.Fatalf("linen faded xterm = %d, want 131", got)
	}
	configureCLIThemeWithStyle("light", "glacier")
	if got, want := activeCLITheme.userBubbleFaded.xterm, 67; got != want {
		t.Fatalf("glacier faded xterm = %d, want 67", got)
	}

	configureCLIThemeWithStyle("light", "sandstone")
	if got, want := activeCLITheme.toolArg.hex, "#5a6470"; got != want {
		t.Fatalf("light toolArg = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.toolArg.xterm, 240; got != want {
		t.Fatalf("light toolArg xterm = %d, want 240", got)
	}
	configureCLIThemeWithStyle("dark", "graphite")
	if got, want := activeCLITheme.toolArg.hex, "#a5b0bd"; got != want {
		t.Fatalf("dark toolArg = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.toolArg.xterm, 145; got != want {
		t.Fatalf("dark toolArg xterm = %d, want 145", got)
	}
}
