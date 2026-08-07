package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/charmbracelet/x/ansi"

	"corvus/internal/agent"
	"corvus/internal/agent/testutil"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/i18n"
	"corvus/internal/provider"
	"corvus/internal/tool"
)

func TestTurnReceiptShowsOnlyCacheHit(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	i18n.DetectLanguage("zh")

	u := &provider.Usage{
		PromptTokens: 13_625, CompletionTokens: 392, TotalTokens: 14_017,
		CacheHitTokens: 13_184, CacheMissTokens: 441, ReasoningTokens: 24,
	}
	got := renderTurnReceipt(u)
	for _, want := range []string{"缓存命中", "13.2K"} {
		if !strings.Contains(got, want) {
			t.Fatalf("turn receipt %q missing %q", got, want)
		}
	}
	for _, banned := range []string{"tok", "in ", "out ", "reasoning", "¥", "estimated", "prefix"} {
		if strings.Contains(got, banned) {
			t.Fatalf("turn receipt %q must not contain %q", got, banned)
		}
	}
	if strings.Contains(got, "\033[") {
		t.Fatalf("NO_COLOR turn receipt contains escapes: %q", got)
	}
}

func TestTurnReceiptShowsZeroWhenNoHits(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	i18n.DetectLanguage("en")

	got := renderTurnReceipt(&provider.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120})
	for _, want := range []string{"cached", "0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("zero-hit receipt %q missing %q", got, want)
		}
	}
}

func TestTurnReceiptIgnoresNilUsage(t *testing.T) {
	if got := renderTurnReceipt(nil); got != "" {
		t.Fatalf("nil usage receipt = %q, want empty", got)
	}
}

func TestTurnReceiptAdaptsContrastAcrossThemes(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.ANSI256
	i18n.DetectLanguage("en")

	for _, tt := range []struct {
		mode, labelSGR, valueSGR string
	}{
		{mode: "dark", labelSGR: "\033[38;5;247m", valueSGR: "\033[38;5;252m"},
		{mode: "light", labelSGR: "\033[38;5;243m", valueSGR: "\033[38;5;238m"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			configureCLITheme(tt.mode)
			receipt := renderTurnReceipt(&provider.Usage{
				PromptTokens: 900, CompletionTokens: 100, TotalTokens: 1_000, CacheHitTokens: 900,
			})
			for _, want := range []string{tt.labelSGR + "cached", tt.valueSGR + "900"} {
				if !strings.Contains(receipt, want) {
					t.Fatalf("%s receipt %q missing semantic style %q", tt.mode, receipt, want)
				}
			}
		})
	}
}

func TestStatusFooterSemanticPaletteAcrossThemes(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	for _, tt := range []struct {
		mode, labelSGR, valueSGR, infoSGR, secondarySGR string
	}{
		{mode: "dark", labelSGR: "\033[38;5;247m", valueSGR: "\033[38;5;252m", infoSGR: "\033[38;5;80m", secondarySGR: "\033[38;5;141m"},
		{mode: "light", labelSGR: "\033[38;5;243m", valueSGR: "\033[38;5;238m", infoSGR: "\033[38;5;25m", secondarySGR: "\033[38;5;104m"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			configureCLITheme(tt.mode)
			m := newTestChatTUI()
			m.label = "deepseek-v4-flash"
			m.effortLevel = "auto"
			m.runtimeProfile = "full"
			got := m.statusModelWorkGroup(80)
			for _, want := range []string{
				tt.labelSGR + "MODEL",
				tt.infoSGR + "deepseek-v4-flash",
				tt.labelSGR + "EFFORT",
				tt.valueSGR + "auto",
				tt.labelSGR + "WORK",
				tt.secondarySGR + "balanced",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("model/work group %q missing semantic style %q", got, want)
				}
			}
			primary := m.primaryStatusLine(false, false)
			if !strings.Contains(primary, tt.valueSGR+i18n.M.ChatStatusIdle) ||
				!strings.Contains(primary, tt.labelSGR+i18n.M.ChatStatusCycleHintCompact) {
				t.Fatalf("%s interaction hints should use readable semantic contrast: %q", tt.mode, primary)
			}
		})
	}
}

func TestStatusFooterThemesKeepIdenticalGeometry(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{})
	m.label = "deepseek-v4-flash"
	m.effortLevel = "max"
	m.runtimeProfile = "full"
	m.balance = "¥12.34"
	m.gitStatus = gitStatus{Repo: "DeepSeek-Corvus", Branch: "feature/theme-footer", Added: 3}

	render := func(mode string, profile colorprofile.Profile) string {
		activeColorProfile = profile
		configureCLITheme(mode)
		primary := m.primaryStatusLine(false, false)
		return ansi.Strip(m.renderStatusBlock(primary, 132))
	}
	dark := render("dark", colorprofile.ANSI256)
	light := render("light", colorprofile.ANSI256)
	plain := render("dark", colorprofile.NoTTY)
	if dark != light || dark != plain {
		t.Fatalf("theme modes changed footer geometry:\ndark:\n%s\nlight:\n%s\nplain:\n%s", dark, light, plain)
	}
}

func TestStatusFooterGitAdaptsToTheme(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	for _, tt := range []struct {
		mode, gitSGR string
	}{
		{mode: "dark", gitSGR: "\033[38;5;179m"},
		{mode: "light", gitSGR: "\033[38;5;136m"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			configureCLITheme(tt.mode)
			// Git porcelain is off default chrome; color still applies when rendered
			// for /status (gitTag) or other detail hosts.
			git := gitStatus{Repo: "DeepSeek-Corvus", Branch: "db4be5e6", Detached: true}.
				RenderWithin(80, activeCLITheme.warn)
			if !strings.Contains(git, tt.gitSGR+"DeepSeek-Corvus") {
				t.Fatalf("%s Git identity should use warm semantic colour: %q", tt.mode, git)
			}
		})
	}
}

func TestSingleStatusLineWrapsAtGroupBoundaries(t *testing.T) {
	i18n.DetectLanguage("en")
	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{WorkspaceRoot: "/home/user/project"})
	m.label = "deepseek-v4-flash"
	m.turnReceipt = renderTurnReceipt(&provider.Usage{TotalTokens: 1050, CacheHitTokens: 900})

	primary := m.primaryStatusLine(false, false)
	got := m.renderStatusBlock(primary, 30)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("narrow footer should wrap, got one row: %q", got)
	}
	for i, line := range lines {
		if width := visibleWidth(line); width > 30 {
			t.Fatalf("row %d width = %d, want <= 30: %q", i, width, line)
		}
	}
}

func TestAbbrevHomeShortensHomePrefix(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	for _, tt := range []struct{ in, want string }{
		{"/home/user/project", "~/project"},
		{"/home/user", "~"},
		{"/srv/other", "/srv/other"},
		{"", ""},
	} {
		if got := abbrevHome(tt.in); got != tt.want {
			t.Fatalf("abbrevHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSingleStatusLineRightAlignsWhenItFits(t *testing.T) {
	left := footerHint("idle")
	right := footerSecondary("~/project") + " · " + footerInfo("MODEL m")
	got := layoutSingleStatusLine(left, right, 40)
	if strings.Contains(got, "\n") {
		t.Fatalf("expected one row, got %q", got)
	}
	if width := visibleWidth(got); width != 40 {
		t.Fatalf("row width = %d, want 40: %q", width, got)
	}
	if !strings.HasSuffix(ansi.Strip(got), "MODEL m") {
		t.Fatalf("right group should be right-aligned: %q", ansi.Strip(got))
	}
}

func TestContextFooterColorsOnlyValuesByUrgency(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	normal := strings.Join(renderContextStatusGroups(10, 100, .8), " ")
	if !strings.Contains(normal, "\033[38;5;247mCTX") || !strings.Contains(normal, "\033[38;5;252m10 (10%)") {
		t.Fatalf("normal context should use subtle label and neutral value: %q", normal)
	}

	warning := strings.Join(renderContextStatusGroups(75, 100, .8), " ")
	if !strings.Contains(warning, "\033[38;5;247mCOMPACT") || !strings.Contains(warning, "\033[38;5;179m5%") {
		t.Fatalf("near-threshold context should warn only on values: %q", warning)
	}

	critical := strings.Join(renderContextStatusGroups(80, 100, .8), " ")
	if !strings.Contains(critical, "\033[38;5;179m80 (80%)") || !strings.Contains(critical, "\033[38;5;167m0%") {
		t.Fatalf("critical context should keep warning/danger hierarchy: %q", critical)
	}
}

func TestStatusFooterNoColorKeepsSemanticLabels(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.label = "deepseek-v4-flash"
	m.effortLevel = "auto"
	m.runtimeProfile = "full"
	m.balance = "¥12.34"
	block := m.renderStatusBlock(m.primaryStatusLine(false, false), 120)
	if strings.Contains(block, "\033[") {
		t.Fatalf("NO_COLOR footer contains escapes: %q", block)
	}
	for _, want := range []string{"MODEL deepseek-v4-flash", "EFFORT auto", "WORK balanced"} {
		if !strings.Contains(block, want) {
			t.Fatalf("NO_COLOR footer missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "BAL") || strings.Contains(block, "¥12.34") {
		t.Fatalf("NO_COLOR lean footer must omit balance:\n%s", block)
	}
}

func TestStatusFooterUsesReadableLocalizedHintAndWrapsCleanly(t *testing.T) {
	defer i18n.DetectLanguage("en")
	for _, tt := range []struct {
		lang, compact, session string
	}{
		{lang: "en", compact: "Shift+Tab ask/auto/plan · Ctrl+Y YOLO", session: "MODEL deepseek-v4-flash   EFFORT auto   WORK balanced"},
		{lang: "zh", compact: "Shift+Tab 询问/自动/计划 · Ctrl+Y YOLO", session: "模型 deepseek-v4-flash   强度 auto   模式 均衡"},
		{lang: "zh-TW", compact: "Shift+Tab 詢問/自動/計畫 · Ctrl+Y YOLO", session: "模型 deepseek-v4-flash   強度 auto   模式 均衡"},
	} {
		t.Run(tt.lang, func(t *testing.T) {
			i18n.DetectLanguage(tt.lang)
			m := newTestChatTUI()
			m.ctrl = control.New(control.Options{})
			m.label = "deepseek-v4-flash"
			m.runtimeProfile = "full"
			m.effortLevel = "auto"

			primary := m.primaryStatusLine(false, false)
			block := ansi.Strip(m.renderStatusBlock(primary, 100))
			lines := strings.Split(block, "\n")
			// Without a mode pill, some locales fit interaction + session on one
			// row at width 100; others still split. Either is fine as long as both
			// groups are present and there is no empty data band/divider.
			if len(lines) < 1 || len(lines) > 2 {
				t.Fatalf("localized footer rows = %d, want 1–2 (no empty data band):\n%s", len(lines), block)
			}
			if !strings.Contains(block, tt.compact) || !strings.Contains(block, tt.session) {
				t.Fatalf("localized footer did not keep readable shortcut and session groups:\n%s", block)
			}
			if strings.Contains(block, "⇧Tab") || strings.Contains(block, "^Y") {
				t.Fatalf("localized footer fell back to symbolic shortcut notation:\n%s", block)
			}
			if strings.Contains(block, "─") {
				t.Fatalf("localized footer should not paint a data-band divider without Git/telemetry:\n%s", block)
			}
			for row, line := range lines {
				if width := visibleWidth(line); width > 100 {
					t.Fatalf("localized footer row %d width = %d, want <= 100: %q", row, width, line)
				}
			}

			narrow := ansi.Strip(m.renderStatusBlock(primary, 24))
			if strings.Contains(narrow, "Shift+Tab") || strings.Contains(narrow, "Ctrl+Y") {
				t.Fatalf("shortcut help should yield when readable key names cannot fit:\n%s", narrow)
			}
			if !strings.Contains(narrow, ansi.Strip(footerValue(i18n.M.ChatStatusIdle))) {
				t.Fatalf("narrow footer should preserve the idle state:\n%s", narrow)
			}
		})
	}
}

func TestStatusFooterLocalizesMetricLabelsAndKeepsNarrowRows(t *testing.T) {
	defer i18n.DetectLanguage("en")
	for _, tt := range []struct {
		lang      string
		session   string
		telemetry []string
	}{
		{
			lang:      "zh",
			session:   "模型 deepseek-v4-flash   强度 auto   模式 均衡",
			telemetry: []string{"上下文", "压缩", "任务"},
		},
		{
			lang:      "zh-TW",
			session:   "模型 deepseek-v4-flash   強度 auto   模式 均衡",
			telemetry: []string{"上下文", "壓縮", "任務"},
		},
	} {
		t.Run(tt.lang, func(t *testing.T) {
			i18n.DetectLanguage(tt.lang)
			m := newTestChatTUI()
			m.label = "deepseek-v4-flash"
			m.effortLevel = "auto"
			m.runtimeProfile = "full"
			if got := ansi.Strip(m.statusModelWorkGroup(80)); got != tt.session {
				t.Fatalf("localized session metrics = %q, want %q", got, tt.session)
			}

			// Packing helper still works for lean default metrics only.
			groups := append([]string(nil), renderContextStatusGroups(75, 100, .8)...)
			groups = append(groups, footerMetric(i18n.M.ChatStatusJobsLabel, footerInfo("2")))
			packed := ansi.Strip(packStatusGroups(groups, 22))
			for _, label := range tt.telemetry {
				if !strings.Contains(packed, label+" ") {
					t.Fatalf("localized telemetry missing %q:\n%s", label, packed)
				}
			}
			for row, line := range strings.Split(packed, "\n") {
				if width := visibleWidth(line); width > 22 {
					t.Fatalf("localized telemetry row %d width = %d, want <= 22: %q", row, width, line)
				}
			}
		})
	}
}

func TestStatusFooterWideLayoutSingleRow(t *testing.T) {
	i18n.DetectLanguage("en")

	prov := testutil.NewMock("deepseek-v4-flash", testutil.Turn{
		Text: "ok",
		Usage: &provider.Usage{
			PromptTokens: 10_000, CompletionTokens: 200, TotalTokens: 10_200,
		},
	})
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{MaxSteps: 1, ContextWindow: 200_000}, event.Discard)
	if err := exec.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("seed agent usage: %v", err)
	}

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Executor: exec})
	m.label = "deepseek-v4-flash"
	m.runtimeProfile = "full"
	m.effortLevel = "auto"
	m.balance = "¥12.34"
	m.gitStatus = gitStatus{
		Repo:      "DeepSeek-Corvus",
		Branch:    "feature/responsive-footer",
		Added:     1199,
		Removed:   244,
		Untracked: 3,
	}

	primary := m.primaryStatusLine(false, false)
	plain := ansi.Strip(m.renderStatusBlock(primary, 160))
	if strings.Count(plain, "\n") != 0 {
		t.Fatalf("wide status block should be one row:\n%s", plain)
	}
	if !strings.Contains(plain, "MODEL deepseek-v4-flash   EFFORT auto   WORK balanced") {
		t.Fatalf("single row should keep model, effort, and work in one session group:\n%s", plain)
	}
	for _, banned := range []string{"DeepSeek-Corvus", "BAL", "¥12.34", "+1199", "CTX"} {
		if strings.Contains(plain, banned) {
			t.Fatalf("single row must omit %q:\n%s", banned, plain)
		}
	}
	if got := visibleWidth(plain); got > 160 {
		t.Fatalf("row width = %d, want <= 160: %q", got, plain)
	}
}

func TestStatusFooterDataBandLeftAlignsTelemetry(t *testing.T) {
	defer i18n.DetectLanguage("en")
	i18n.DetectLanguage("en")

	// Force a non-empty lean data band via context groups.
	groups := renderContextStatusGroups(10_000, 200_000, 0)
	line := ansi.Strip(packStatusGroups(groups, 120))
	if !strings.HasPrefix(line, statusFooterIndent+"CTX ") {
		t.Fatalf("lean telemetry should be left aligned, got %q", line)
	}
	if visibleWidth(line) >= 120 {
		t.Fatalf("lean telemetry unexpectedly retained right-alignment padding: %q", line)
	}
}

func TestStatusFooterSingleRowWithoutTelemetry(t *testing.T) {
	m := newTestChatTUI()
	primary := "  Auto · ready"
	block := ansi.Strip(m.renderStatusBlock(primary, 120))
	if strings.Count(block, "\n") != 0 {
		t.Fatalf("footer without telemetry should stay one row: %q", block)
	}
	if !strings.HasPrefix(block, primary) {
		t.Fatalf("footer should lead with the interaction text, got %q", block)
	}
	if strings.Contains(block, "─") {
		t.Fatalf("single-line footer must not paint a divider: %q", block)
	}
}

func TestStatusFooterMediumLayoutLeftAlignsModelWork(t *testing.T) {
	i18n.DetectLanguage("en")

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{})
	m.label = "deepseek-v4-flash"
	m.runtimeProfile = "full"
	m.effortLevel = "auto"

	primary := m.primaryStatusLine(false, false)
	lines := strings.Split(ansi.Strip(m.renderStatusBlock(primary, 82)), "\n")
	if len(lines) != 2 {
		t.Fatalf("medium footer rows = %d, want primary plus model/work without an empty data band:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	modelRow := lines[1]
	if !strings.HasPrefix(modelRow, statusFooterIndent+"MODEL deepseek-v4-flash") ||
		!strings.Contains(modelRow, "EFFORT auto   WORK balanced") {
		t.Fatalf("medium model/effort/work row should be left aligned, got %q:\n%s", modelRow, strings.Join(lines, "\n"))
	}
	if strings.Count(strings.TrimLeft(modelRow, " "), "MODEL") != 1 {
		t.Fatalf("medium model/work row should remain a single semantic group: %q", modelRow)
	}
}

func TestStatusFooterPacksLeanTelemetryWithoutFloatingContinuation(t *testing.T) {
	i18n.DetectLanguage("en")

	groups := append([]string(nil), renderContextStatusGroups(75_000, 100_000, .8)...)
	groups = append(groups, footerMetric(i18n.M.ChatStatusJobsLabel, footerInfo("⚙ 2")))
	lines := strings.Split(ansi.Strip(packStatusGroups(groups, 28)), "\n")
	if len(lines) < 2 {
		t.Fatalf("stacked lean telemetry rows = %d, want wrapping between groups:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], statusFooterIndent+"CTX ") {
		t.Fatalf("context should lead the data band:\n%s", strings.Join(lines, "\n"))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "JOBS") {
		t.Fatalf("jobs should pack on a continuation row when narrow:\n%s", joined)
	}
	for i, line := range lines {
		if got := visibleWidth(line); got > 28 {
			t.Fatalf("row %d width = %d, want <= 28: %q", i, got, line)
		}
	}
}

func TestStatusFooterNarrowLayoutWrapsLongModel(t *testing.T) {
	i18n.DetectLanguage("en")

	prov := testutil.NewMock("deepseek-v4-flash", testutil.Turn{
		Text: "ok",
		Usage: &provider.Usage{
			PromptTokens: 80_000, CompletionTokens: 1_000, TotalTokens: 81_000,
		},
	})
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{
		MaxSteps: 1, ContextWindow: 100_000, CompactRatio: 0.8,
	}, event.Discard)
	if err := exec.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("seed agent usage: %v", err)
	}

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Executor: exec})
	m.label = "provider/" + strings.Repeat("long-model-", 8)
	m.runtimeProfile = "delivery"
	m.balance = "¥123.45"
	m.gitStatus = gitStatus{
		Repo:    "DeepSeek-Corvus-Workspace",
		Branch:  "feature/" + strings.Repeat("long-branch-", 8),
		Added:   20,
		Removed: 4,
	}

	primary := m.primaryStatusLine(false, false)
	block := ansi.Strip(m.renderStatusBlock(primary, 40))
	lines := strings.Split(block, "\n")
	if len(lines) <= 1 {
		t.Fatalf("narrow status block lines = %d, want semantic wrapping:\n%s", len(lines), block)
	}
	for i, line := range lines {
		if got := visibleWidth(line); got > 40 {
			t.Fatalf("row %d width = %d, want <= 40: %q", i, got, line)
		}
	}
	if !strings.Contains(block, "MODEL") {
		t.Fatalf("narrow layout dropped the model group:\n%s", block)
	}
	for _, banned := range []string{"@", "+20", "¥123.45", "BAL"} {
		if strings.Contains(block, banned) {
			t.Fatalf("narrow single-line layout must omit %q:\n%s", banned, block)
		}
	}
}

func TestStatusFooterCustomLineStillReplacesBuiltInData(t *testing.T) {
	i18n.DetectLanguage("en")

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{})
	m.label = "deepseek-v4-flash"
	m.runtimeProfile = "delivery"
	m.balance = "¥12.34"
	m.statuslineCmd = "custom-status"
	m.statuslineOut = "custom telemetry"
	m.gitStatus = gitStatus{Repo: "Corvus", Branch: "main"}

	primary := m.primaryStatusLine(false, false)
	block := ansi.Strip(m.renderStatusBlock(primary, 120))
	if strings.Contains(block, "deepseek-v4-flash") || strings.Contains(block, "work delivery") || strings.Contains(block, "¥12.34") {
		t.Fatalf("custom statusline should replace built-in data fields:\n%s", block)
	}
	if strings.Contains(block, "Corvus@main") {
		t.Fatalf("custom statusline data band should not reintroduce Git porcelain:\n%s", block)
	}
	if !strings.Contains(block, "custom telemetry") {
		t.Fatalf("custom statusline should own the data band:\n%s", block)
	}
}

func TestStatusFooterHeightCountUsesRenderedLayout(t *testing.T) {
	i18n.DetectLanguage("en")

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{})
	m.width = 34
	m.label = "provider/" + strings.Repeat("long-model-", 6)
	m.runtimeProfile = "delivery"
	m.gitStatus = gitStatus{Repo: "VeryLongWorkspaceName", Branch: strings.Repeat("branch/", 8)}
	m.balance = "¥12.34"

	primary := m.primaryStatusLine(false, false)
	want := strings.Count(m.renderStatusBlock(primary, m.width), "\n") + 1
	if got := m.computeStatusLineCount(m.width); got != want {
		t.Fatalf("computed status rows = %d, rendered rows = %d", got, want)
	}
}

func TestStatusFooterSingleLineOmitsBalanceGitCacheContext(t *testing.T) {
	i18n.DetectLanguage("en")

	prov := testutil.NewMock("deepseek-v4-flash", testutil.Turn{
		Text: "ok",
		Usage: &provider.Usage{
			CacheHitTokens:   900,
			CacheMissTokens:  100,
			CompletionTokens: 50,
			PromptTokens:     1000,
			TotalTokens:      1050,
		},
	})
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{MaxSteps: 1, ContextWindow: 200_000}, event.Discard)
	if err := exec.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("seed agent usage: %v", err)
	}

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Executor: exec, WorkspaceRoot: "/home/user/project"})
	m.label = "deepseek-v4-flash"
	m.effortLevel = "auto"
	m.runtimeProfile = "full"
	m.balance = "¥12.34"
	m.gitStatus = gitStatus{Repo: "Corvus", Branch: "main", Added: 1}
	m.turnReceipt = renderTurnReceipt(&provider.Usage{TotalTokens: 1050, CacheHitTokens: 900})

	plain := ansi.Strip(m.renderStatusBlock(m.primaryStatusLine(false, false), 140))
	for _, banned := range []string{
		"BAL", "¥12.34", "Corvus", "main",
		i18n.M.ChatStatusCacheLabel, "turn hit", "avg 90",
		i18n.M.ChatStatusContextLabel, i18n.M.ChatStatusJobsLabel, "COMPACT",
	} {
		if strings.Contains(plain, banned) {
			t.Fatalf("single-line footer must omit %q:\n%s", banned, plain)
		}
	}
	for _, want := range []string{"/home/user/project", "MODEL deepseek-v4-flash", "WORK balanced", "cached 900"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("single-line footer missing %q:\n%s", want, plain)
		}
	}
	if strings.Count(plain, "\n") != 0 {
		t.Fatalf("footer must be one row at width 140:\n%s", plain)
	}
}

func TestStatusCommandStillShowsMovedFields(t *testing.T) {
	i18n.DetectLanguage("en")

	prov := testutil.NewMock("deepseek-v4-flash", testutil.Turn{
		Text: "ok",
		Usage: &provider.Usage{
			CacheHitTokens:   900,
			CacheMissTokens:  100,
			CompletionTokens: 50,
			PromptTokens:     1000,
			TotalTokens:      1050,
		},
	})
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession(""), agent.Options{MaxSteps: 1, ContextWindow: 200_000}, event.Discard)
	if err := exec.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("seed agent usage: %v", err)
	}

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Executor: exec})
	m.balance = "$10.00"
	m.gitStatus = gitStatus{Repo: "Corvus", Branch: "feature"}
	m.runSlashCommand("/status")
	out := ansi.Strip(strings.Join(m.transcript, "\n"))
	for _, want := range []string{"$10.00", "feature", "Session status", "turn hit", "cache"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in /status:\n%s", want, out)
		}
	}
}

func TestFormatElapsedFixed(t *testing.T) {
	for _, tc := range []struct {
		sec  int
		want string
	}{
		{0, "  0"}, {3, "  3"}, {12, " 12"}, {123, "123"}, {999, "999"}, {1000, "999"}, {9999, "999"},
	} {
		if got := formatElapsedFixed(tc.sec); got != tc.want {
			t.Fatalf("formatElapsedFixed(%d) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

func TestWorkingLineElapsedStableWidth(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiRunning
	m.elapsed = 3
	line3 := m.runningWorkingLine(false, false)
	m.elapsed = 12
	line12 := m.runningWorkingLine(false, false)
	// The time segment keeps a stable 4-column width ("  3s" vs " 12s").
	if !strings.Contains(line3, "  3s") || !strings.Contains(line12, " 12s") {
		t.Fatalf("elapsed width must be fixed: %q vs %q", line3, line12)
	}
}
