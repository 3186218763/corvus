package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/charmbracelet/x/ansi"

	"reasonix/internal/agent"
	"reasonix/internal/agent/testutil"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestTurnReceiptKeepsCompletePerTurnBreakdown(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	i18n.DetectLanguage("zh")

	u := &provider.Usage{
		PromptTokens:     13_625,
		CompletionTokens: 392,
		TotalTokens:      14_017,
		CacheHitTokens:   13_184,
		CacheMissTokens:  441,
		ReasoningTokens:  24,
	}
	p := &provider.Pricing{CacheHit: .1, Input: 1, Output: 2}
	got := renderTurnReceipt(u, p, nil)
	for _, want := range []string{
		"本轮", "14.0K tok", "in 13.6K", "cached 13.2K", "new 441",
		"out 392", "reasoning 24", "¥0.0025",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("turn receipt %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "\033[") {
		t.Fatalf("NO_COLOR turn receipt contains escapes: %q", got)
	}
}

func TestTurnReceiptFallsBackToDerivedFreshTokensAndWrapsCleanly(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	i18n.DetectLanguage("en")

	got := renderTurnReceipt(&provider.Usage{
		PromptTokens: 1_200, CompletionTokens: 80, TotalTokens: 1_280, CacheHitTokens: 900,
	}, nil, &event.CacheDiagnostics{PrefixChanged: true, PrefixChangeReasons: []string{"tools"}})
	plain := ansi.Strip(got)
	for _, want := range []string{"TURN", "cached 900", "new 300", "cache prefix changed: tools"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("turn receipt %q missing %q", plain, want)
		}
	}
	for i, line := range strings.Split(wrapTranscript(got, 32), "\n") {
		if width := visibleWidth(line); width > 32 {
			t.Fatalf("wrapped turn receipt row %d width = %d, want <= 32: %q", i, width, line)
		}
	}
}

func TestTurnReceiptIgnoresEmptyUsage(t *testing.T) {
	if got := renderTurnReceipt(nil, nil, nil); got != "" {
		t.Fatalf("nil usage receipt = %q, want empty", got)
	}
	if got := renderTurnReceipt(&provider.Usage{}, nil, nil); got != "" {
		t.Fatalf("empty usage receipt = %q, want empty", got)
	}
}

func TestTurnReceiptMarksEstimatedUsage(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	defer i18n.DetectLanguage("en")
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	i18n.DetectLanguage("en")

	got := renderTurnReceipt(&provider.Usage{TotalTokens: 1_024, Estimated: true}, nil, nil)
	for _, want := range []string{"≈1.0K tok", "estimated"} {
		if !strings.Contains(got, want) {
			t.Fatalf("estimated turn receipt %q missing %q", got, want)
		}
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
				PromptTokens: 900, CompletionTokens: 100, TotalTokens: 1_000,
			}, nil, nil)
			for _, want := range []string{tt.labelSGR + "TURN", tt.valueSGR + "1.0K tok"} {
				if !strings.Contains(receipt, want) {
					t.Fatalf("%s receipt %q missing semantic style %q", tt.mode, receipt, want)
				}
			}
			if strings.Contains(receipt, "─") {
				t.Fatalf("%s receipt should have no rule separator: %q", tt.mode, ansi.Strip(receipt))
			}
		})
	}
}

func TestStatusFooterSemanticPaletteAcrossThemes(t *testing.T) {
	t.Setenv("REASONIX_THEME", "")
	t.Setenv("REASONIX_THEME_STYLE", "")
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
	m.gitStatus = gitStatus{Repo: "DeepSeek-Reasonix", Branch: "feature/theme-footer", Added: 3}

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

func TestStatusFooterGitAndDividerAdaptToTheme(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	for _, tt := range []struct {
		mode, gitSGR, borderSGR string
	}{
		{mode: "dark", gitSGR: "\033[38;5;179m", borderSGR: "\033[38;5;236m"},
		{mode: "light", gitSGR: "\033[38;5;136m", borderSGR: "\033[38;5;253m"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			configureCLITheme(tt.mode)
			// Git porcelain is off default chrome; color still applies when rendered
			// for /status (gitTag) or other detail hosts.
			git := gitStatus{Repo: "DeepSeek-Reasonix", Branch: "db4be5e6", Detached: true}.
				RenderWithin(80, activeCLITheme.warn)
			if !strings.Contains(git, tt.gitSGR+"DeepSeek-Reasonix") {
				t.Fatalf("%s Git identity should use warm semantic colour: %q", tt.mode, git)
			}
			divider := statusFooterDivider(40)
			if !strings.Contains(divider, tt.borderSGR) || visibleWidth(divider) != 40 {
				t.Fatalf("%s divider should use border token at full width: %q", tt.mode, divider)
			}
		})
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

func TestStatusFooterWideLayoutKeepsModelOnInteractionRow(t *testing.T) {
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
		Repo:      "DeepSeek-Reasonix",
		Branch:    "feature/responsive-footer",
		Added:     1199,
		Removed:   244,
		Untracked: 3,
	}

	primary := m.primaryStatusLine(false, false)
	lines := strings.Split(ansi.Strip(m.renderStatusBlock(primary, 160)), "\n")
	if len(lines) != 3 {
		t.Fatalf("wide status block lines = %d, want interaction + divider + lean data:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "MODEL deepseek-v4-flash   EFFORT auto   WORK balanced") {
		t.Fatalf("first row should keep model, effort, and work in one session group:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[0], "DeepSeek-Reasonix@") || strings.Contains(lines[0], "BAL") {
		t.Fatalf("first row should not contain Git or balance:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Trim(lines[1], "─ ") != "" {
		t.Fatalf("middle row should be a divider:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[2], "CTX") {
		t.Fatalf("data band should show context:\n%s", strings.Join(lines, "\n"))
	}
	for _, banned := range []string{"DeepSeek-Reasonix", "BAL", "¥12.34", "+1199"} {
		if strings.Contains(lines[2], banned) {
			t.Fatalf("data band must omit %q:\n%s", banned, strings.Join(lines, "\n"))
		}
	}
	for i, line := range lines {
		if got := visibleWidth(line); got > 160 {
			t.Fatalf("row %d width = %d, want <= 160: %q", i, got, line)
		}
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

func TestStatusFooterOmitsEmptyDataBand(t *testing.T) {
	m := newTestChatTUI()
	primary := "  Auto · ready"
	block := ansi.Strip(m.renderStatusBlock(primary, 120))
	if block != primary {
		t.Fatalf("empty Git/telemetry status block = %q, want only %q", block, primary)
	}
	if strings.Contains(block, "─") {
		t.Fatalf("empty Git/telemetry status block retained a divider: %q", block)
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

func TestStatusFooterNarrowLayoutBreaksBetweenGroups(t *testing.T) {
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
		Repo:    "DeepSeek-Reasonix-Workspace",
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
	if !strings.Contains(block, "CTX") || !strings.Contains(block, "MODEL") {
		t.Fatalf("narrow layout dropped required lean information:\n%s", block)
	}
	for _, banned := range []string{"@", "+20", "¥123.45", "BAL"} {
		if strings.Contains(block, banned) {
			t.Fatalf("narrow lean layout must omit %q:\n%s", banned, block)
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
	m.gitStatus = gitStatus{Repo: "Reasonix", Branch: "main"}

	primary := m.primaryStatusLine(false, false)
	block := ansi.Strip(m.renderStatusBlock(primary, 120))
	if strings.Contains(block, "deepseek-v4-flash") || strings.Contains(block, "work delivery") || strings.Contains(block, "¥12.34") {
		t.Fatalf("custom statusline should replace built-in data fields:\n%s", block)
	}
	if strings.Contains(block, "Reasonix@main") {
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

func TestStatusFooterDefaultOmitsBalanceGitCache(t *testing.T) {
	i18n.DetectLanguage("en")

	// Seed usage so CTX appears and cache would have been available pre-lean.
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
	m.label = "deepseek-v4-flash"
	m.effortLevel = "auto"
	m.runtimeProfile = "full"
	m.balance = "¥12.34"
	m.gitStatus = gitStatus{Repo: "Reasonix", Branch: "main", Added: 1}

	plain := ansi.Strip(m.renderStatusBlock(m.primaryStatusLine(false, false), 100))
	for _, banned := range []string{
		"BAL", "¥12.34", "Reasonix", "main",
		i18n.M.ChatStatusCacheLabel, "turn hit", "avg 90",
	} {
		if strings.Contains(plain, banned) {
			t.Fatalf("default footer must omit %q:\n%s", banned, plain)
		}
	}
	for _, want := range []string{"CTX", "MODEL deepseek-v4-flash", "WORK balanced"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("default footer missing %q:\n%s", want, plain)
		}
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
	m.gitStatus = gitStatus{Repo: "Reasonix", Branch: "feature"}
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
