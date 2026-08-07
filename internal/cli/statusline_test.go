package cli

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"corvus/internal/agent"
	"corvus/internal/agent/testutil"
	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/i18n"
	"corvus/internal/provider"
	"corvus/internal/tool"
)

// TestRunStatuslineCmd checks the custom status-line runner: it returns the
// first stdout line and forwards the JSON payload on stdin.
func TestRunStatuslineCmd(t *testing.T) {
	firstLineCmd := "printf 'row-one\\nrow-two\\n'"
	stdinCmd := "cat"
	failCmd := "exit 3"
	if runtime.GOOS == "windows" {
		firstLineCmd = "echo row-one & echo row-two"
		stdinCmd = "more"
		failCmd = "exit /b 3"
	}

	// Multi-line output collapses to the first row.
	if got := runStatuslineCmd(firstLineCmd, "{}"); got != "row-one" {
		t.Errorf("multi-line output should collapse to the first row, got %q", got)
	}
	// The JSON payload is delivered on stdin.
	if got := runStatuslineCmd(stdinCmd, `{"model":"deepseek"}`); got != `{"model":"deepseek"}` {
		t.Errorf("stdin payload not forwarded, got %q", got)
	}
	// A failing command yields an empty line, not an error.
	if got := runStatuslineCmd(failCmd, "{}"); got != "" {
		t.Errorf("failed command should yield empty, got %q", got)
	}
}

func TestRunStatuslineCmdNormalizesQuotedNodeEval(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	script := "let input = ''; process.stdin.setEncoding('utf8'); process.stdin.on('data', chunk => input += chunk); process.stdin.on('end', () => { const payload = JSON.parse(input); console.log(payload.model) })"
	cmd := `node -e "\"` + script + `\""`
	timeout := statuslineCommandTimeout
	if runtime.GOOS == "windows" {
		// Windows CI cold-starts node.exe through Defender scanning while the
		// rest of the module compiles and tests in parallel; a fresh toolchain
		// (empty setup-go cache) pushes that past 10s. The production timeout
		// is not under test here — only the quoted-eval normalization is.
		timeout = 30 * time.Second
	}

	if got := runStatuslineCmdWithTimeout(cmd, `{"model":"deepseek"}`, timeout); got != "deepseek" {
		t.Fatalf("normalized statusline node -e output = %q, want deepseek", got)
	}
}

// TestRunStatuslineDisabled confirms no command means no work (nil cmd), without
// touching the controller.
func TestRunStatuslineDisabled(t *testing.T) {
	m := chatTUI{} // no statuslineCmd, nil ctrl
	if cmd := m.runStatusline(); cmd != nil {
		t.Error("an unconfigured status line must return a nil tea.Cmd")
	}
}

func TestModelSwitchRefreshesCustomStatusline(t *testing.T) {
	oldCtrl := control.New(control.Options{Label: "old-model"})
	newCtrl := control.New(control.Options{Label: "new-model"})
	m := newChatTUI(oldCtrl, "", make(chan event.Event, 1), 80)
	m.statuslineCmd = "cat"
	if runtime.GOOS == "windows" {
		m.statuslineCmd = "more"
	}
	m.statuslineOut = `{"model":"old-model"}`

	_, cmd := m.Update(modelSwitchMsg{
		ref:   "provider/new-model",
		ctrl:  newCtrl,
		label: "new-model",
	})
	if cmd == nil {
		t.Fatal("model switch should schedule commands")
	}
	if !statuslineCommandHasModel(cmd, "new-model") {
		t.Fatal("model switch did not refresh custom statusline with the new model")
	}
}

func statuslineCommandHasModel(cmd tea.Cmd, model string) bool {
	msg := cmd()
	switch msg := msg.(type) {
	case statuslineMsg:
		return strings.Contains(msg.out, `"model":"`+model+`"`)
	case tea.BatchMsg:
		for _, child := range msg {
			if child == nil {
				continue
			}
			if statuslineCommandHasModel(child, model) {
				return true
			}
		}
	}
	return false
}

func TestComposerModeBadgeUsesModeTagText(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.TrueColor
	i18n.DetectLanguage("en")

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{})
	m.planMode = true
	m.width = 80
	m.height = 24
	if got := m.modeTagText(); got != "Plan" {
		t.Fatalf("modeTagText() = %q, want Plan", got)
	}

	badgePlain := ansi.Strip(m.renderModeBadge(false))
	if !strings.Contains(badgePlain, "Plan") {
		t.Fatalf("composer mode badge missing Plan: %q", badgePlain)
	}

	content := m.View().Content
	plainView := ansi.Strip(content)
	if !strings.Contains(plainView, "Plan") {
		t.Fatalf("View missing Plan mode badge:\n%s", plainView)
	}
	// Badge must sit left of the prompt on the same composer content row.
	shared := false
	for _, line := range strings.Split(plainView, "\n") {
		if strings.Contains(line, "Plan") && strings.Contains(line, "❯") {
			shared = true
			break
		}
	}
	if !shared {
		t.Fatalf("Plan badge and composer prompt must share a line:\n%s", plainView)
	}
	if !strings.Contains(content, "\x1b[48;2;37;99;235m") {
		t.Fatalf("Plan badge should use blue pill background, got:\n%q", content)
	}

	primary := strings.TrimSpace(ansi.Strip(m.primaryStatusLine(false, false)))
	if strings.HasPrefix(primary, "Plan") {
		t.Fatalf("footer primary must not start with mode pill: %q", primary)
	}
	if !strings.Contains(primary, "ready") {
		t.Fatalf("footer primary missing idle state: %q", primary)
	}
}

func TestComposerBadgeJoinDoesNotExceedFrameWidth(t *testing.T) {
	// Wide badge ("Don't Ask") + former min box width of 10 overflowed narrow
	// terminals. Joined composer row must stay ≤ frame width.
	ctrl := control.New(control.Options{})
	ctrl.SetToolApprovalMode(control.ToolApprovalDontAsk)
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 20)
	m.cfg = config.Default()
	if err := m.cfg.SetUIShortcutLayout("desktop"); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 12})
	m = next.(chatTUI)
	if got := m.modeTagText(); got != "Don't Ask" {
		t.Fatalf("modeTagText() = %q, want Don't Ask", got)
	}
	badgeCols := m.modeBadgeColumnWidth()
	boxW := m.composerBoxWidth(badgeCols)
	if boxW < 1 {
		t.Fatalf("composerBoxWidth = %d, want >= 1", boxW)
	}
	if total := badgeCols + boxW; total > m.composerFrameWidth() {
		t.Fatalf("badgeCols(%d)+box(%d)=%d exceeds frame %d", badgeCols, boxW, total, m.composerFrameWidth())
	}
	// Same budget for the SetWidth path.
	if got, want := m.composerContentWidth(), boxW; got != want {
		t.Fatalf("composerContentWidth = %d, want %d (aligned with box)", got, want)
	}
	for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if w := visibleWidth(line); w > m.composerFrameWidth() {
			// Transcript viewport may pad; only check composer-ish rows with badge.
			if strings.Contains(line, "Don't Ask") || strings.Contains(line, "❯") {
				t.Fatalf("composer-related row width %d > frame %d: %q", w, m.composerFrameWidth(), line)
			}
		}
	}
}

func TestShellPrefixBadgeIsShell(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.TrueColor
	i18n.DetectLanguage("en")

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{})
	m.width = 80
	m.height = 24
	m.input.SetValue("!ls")

	badgePlain := ansi.Strip(m.renderModeBadge(true))
	if strings.TrimSpace(badgePlain) != "Shell" {
		t.Fatalf("shell badge = %q, want Shell", badgePlain)
	}
	content := m.View().Content
	if !strings.Contains(ansi.Strip(content), "Shell") {
		t.Fatalf("View missing Shell badge:\n%s", ansi.Strip(content))
	}
	if !strings.Contains(content, "\x1b[48;2;22;163;74m") {
		t.Fatalf("Shell badge should use green pill background, got:\n%q", content)
	}
	primary := strings.TrimSpace(ansi.Strip(m.primaryStatusLine(true, false)))
	if strings.HasPrefix(primary, "Shell") {
		t.Fatalf("footer primary must not start with Shell pill: %q", primary)
	}
}

func TestDesktopModeTagParityOnBadge(t *testing.T) {
	i18n.DetectLanguage("en")

	// Desktop default: Ask (not classic Auto).
	content := renderStatuslineViewWithShortcutLayout(t, "desktop")
	plain := ansi.Strip(content)
	if !strings.Contains(plain, "Ask") {
		t.Fatalf("desktop layout badge should show Ask from modeTagText():\n%s", plain)
	}
	// Don't Ask when tool approval is dontAsk.
	ctrl := control.New(control.Options{})
	ctrl.SetToolApprovalMode(control.ToolApprovalDontAsk)
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	m.cfg = config.Default()
	if err := m.cfg.SetUIShortcutLayout("desktop"); err != nil {
		t.Fatal(err)
	}
	if got := m.modeTagText(); got != "Don't Ask" {
		t.Fatalf("desktop dontAsk modeTagText() = %q, want Don't Ask", got)
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderModeBadge(false))); got != "Don't Ask" {
		t.Fatalf("desktop dontAsk badge = %q, want Don't Ask", got)
	}
}

func TestIdleStatuslineIsCompact(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.TrueColor
	i18n.DetectLanguage("en")

	content := renderStatuslineView(t, false)
	plainView := ansi.Strip(content)
	// Mode chrome lives on the composer badge; footer keeps idle + cycle hints.
	if !strings.Contains(plainView, "Auto") {
		t.Fatalf("idle view missing Auto mode badge:\n%s", plainView)
	}
	footer := footerInteractionPlain(content)
	if !strings.Contains(footer, "ready") {
		t.Fatalf("idle status line missing ready state:\n%s", footer)
	}
	if strings.HasPrefix(strings.TrimSpace(footer), "Auto") {
		t.Fatalf("footer primary must not start with mode pill: %q", footer)
	}
	if !strings.Contains(footer, "Shift+Tab ask/auto/plan · Ctrl+Y YOLO") {
		t.Fatalf("idle status line missing plan-toggle hint:\n%s", footer)
	}
	for _, old := range []string{"Shift-Tab", "Ctrl-O", "Ctrl-D", "Enter sends", "Esc clears/exits state", "PgUp/PgDn"} {
		if strings.Contains(footer, old) {
			t.Fatalf("idle status line should not contain %q:\n%s", old, footer)
		}
	}
	if strings.Contains(footer, "[auto]") {
		t.Fatalf("idle status line should use pill label, not bracketed tag:\n%s", footer)
	}
	if !strings.Contains(content, "\x1b[48;2;245;158;11m") {
		t.Fatalf("Auto badge should use amber pill background, got:\n%q", content)
	}
}

func TestYoloStatuslineUsesDangerPill(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.TrueColor
	i18n.DetectLanguage("en")

	content := renderStatuslineView(t, true)
	plainView := ansi.Strip(content)
	footer := footerInteractionPlain(content)
	if !strings.Contains(plainView, "YOLO") {
		t.Fatalf("YOLO view missing mode badge:\n%s", plainView)
	}
	if !strings.Contains(footer, "approvals skipped") || !strings.Contains(footer, "Shift+Tab ask/auto/plan · Ctrl+Y YOLO") {
		t.Fatalf("YOLO status line missing warning text:\n%s", footer)
	}
	if strings.HasPrefix(strings.TrimSpace(footer), "YOLO") {
		t.Fatalf("footer primary must not start with YOLO pill: %q", footer)
	}
	if strings.Contains(footer, "[YOLO]") {
		t.Fatalf("YOLO status line should use a pill label, not bracketed tag:\n%s", footer)
	}
	if !strings.Contains(content, "\x1b[48;2;229;72;77m") {
		t.Fatalf("YOLO badge should use danger pill background, got:\n%q", content)
	}
}

func TestPlanStatuslineUsesBluePill(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.TrueColor
	i18n.DetectLanguage("en")

	content := renderPlanStatuslineView(t)
	plainView := ansi.Strip(content)
	footer := footerInteractionPlain(content)
	if !strings.Contains(plainView, "Plan") {
		t.Fatalf("plan view missing mode badge:\n%s", plainView)
	}
	if !strings.Contains(footer, "ready") || !strings.Contains(footer, "Shift+Tab ask/auto/plan · Ctrl+Y YOLO") {
		t.Fatalf("plan status line missing idle/hint status:\n%s", footer)
	}
	if strings.HasPrefix(strings.TrimSpace(footer), "Plan") {
		t.Fatalf("footer primary must not start with Plan pill: %q", footer)
	}
	if !strings.Contains(content, "\x1b[48;2;37;99;235m") {
		t.Fatalf("Plan badge should use blue pill background, got:\n%q", content)
	}
}

func TestStatuslineCycleHintFollowsLanguage(t *testing.T) {
	i18n.DetectLanguage("zh")
	t.Cleanup(func() { i18n.DetectLanguage("en") })

	content := renderStatuslineView(t, false)
	plainView := ansi.Strip(content)
	plain := bottomStatusPlain(content)
	if !strings.Contains(plainView, "Auto") {
		t.Fatalf("localized view missing Auto mode badge:\n%s", plainView)
	}
	if !strings.Contains(plain, "就绪") || !strings.Contains(plain, "Shift+Tab 询问/自动/计划 · Ctrl+Y YOLO") {
		t.Fatalf("localized plan-toggle hint missing:\n%s", plain)
	}
	if strings.Contains(plain, "ready") || strings.Contains(plain, "Shift+Tab ask/auto/plan · Ctrl+Y YOLO") {
		t.Fatalf("localized status line should not fall back to English:\n%s", plain)
	}
}

func TestDesktopShortcutStatuslineUsesPlanToggleHint(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineViewWithShortcutLayout(t, "desktop")
	plainView := ansi.Strip(content)
	footer := footerInteractionPlain(content)
	if !strings.Contains(plainView, "Ask") {
		t.Fatalf("desktop shortcut view missing Ask mode badge:\n%s", plainView)
	}
	if !strings.Contains(footer, "Shift+Tab ask/auto/plan · Ctrl+Y YOLO") {
		t.Fatalf("desktop shortcut status line missing unified plan-toggle hint:\n%s", footer)
	}
	if strings.HasPrefix(strings.TrimSpace(footer), "Ask") {
		t.Fatalf("footer primary must not start with Ask pill: %q", footer)
	}
}

func TestStatuslineShowsEffortInPersistentFooter(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineViewWithEffort(t, "auto")
	lines := strings.Split(ansi.Strip(content), "\n")
	statusLine := lines[len(lines)-1]
	if !strings.Contains(statusLine, "MODEL deepseek-v4-flash   EFFORT auto") {
		t.Fatalf("session row should keep effort beside the model:\n%s", statusLine)
	}
}

func TestStatuslineOmitsCacheRatesFromPersistentFooter(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineViewWithCache(t)
	plain := ansi.Strip(content)
	if !strings.Contains(plain, "MODEL deepseek-v4-flash") {
		t.Fatalf("footer should still show model:\n%s", plain)
	}
	if strings.Contains(plain, "CTX") {
		t.Fatalf("single-line footer must not show the context band:\n%s", plain)
	}
	if strings.Contains(plain, "CACHE") || strings.Contains(plain, "turn hit") {
		t.Fatalf("lean footer must omit cache diagnostics:\n%s", plain)
	}
}

func TestStatuslineShowsEffortWithoutGitOnPersistentFooter(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineViewWithGitAndEffort(t)
	plain := ansi.Strip(content)
	if !strings.Contains(plain, "MODEL deepseek-v4-flash   EFFORT auto") {
		t.Fatalf("session row should keep effort beside the model:\n%s", plain)
	}
	if strings.Contains(plain, "Corvus@") || strings.Contains(plain, "+3 -1") {
		t.Fatalf("lean footer must omit git porcelain:\n%s", plain)
	}
}

func TestStatuslineShowsWorkModeOmitsBalanceFromPersistentFooter(t *testing.T) {
	i18n.DetectLanguage("en")

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 120)
	m.label = "deepseek-v4-flash"
	m.runtimeProfile = "delivery"
	m.balance = "¥12.34"
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	plain := ansi.Strip(next.(chatTUI).View().Content)
	if !strings.Contains(plain, "MODEL deepseek-v4-flash   WORK delivery") {
		t.Fatalf("footer should show model and work mode:\n%s", plain)
	}
	if strings.Contains(plain, "BAL") || strings.Contains(plain, "¥12.34") {
		t.Fatalf("lean footer must omit balance:\n%s", plain)
	}
}

func TestEffortTagExplicitValueUsesThemeInfo(t *testing.T) {
	i18n.DetectLanguage("en")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	for _, tt := range []struct {
		mode, infoSGR string
	}{
		{mode: "dark", infoSGR: "\033[1;38;5;75m"},
		{mode: "light", infoSGR: "\033[1;38;5;26m"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			configureCLITheme(tt.mode)
			m := newTestChatTUI()
			m.effortLevel = "max"
			content := m.effortTag()
			if !strings.Contains(ansi.Strip(content), "EFFORT max") {
				t.Fatalf("status data line should show explicit effort:\n%s", ansi.Strip(content))
			}
			if !strings.Contains(content, tt.infoSGR+"max") {
				t.Fatalf("%s explicit effort should use theme info colour, got:\n%q", tt.mode, content)
			}
		})
	}
}

func TestRefreshEffortStatusUsesCurrentModel(t *testing.T) {
	isolateUserConfig(t)

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	m.modelRef = "deepseek-flash/deepseek-v4-flash"
	m.refreshEffortStatus()
	if m.effortLevel != "auto" {
		t.Fatalf("effortLevel = %q, want auto", m.effortLevel)
	}
}

func renderStatuslineView(t *testing.T, yolo bool) string {
	t.Helper()

	ctrl := control.New(control.Options{})
	ctrl.SetAutoApproveTools(yolo)
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(chatTUI).View().Content
}

func renderStatuslineViewWithShortcutLayout(t *testing.T, layout string) string {
	t.Helper()

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	m.cfg = config.Default()
	if err := m.cfg.SetUIShortcutLayout(layout); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(chatTUI).View().Content
}

func renderStatuslineViewWithEffort(t *testing.T, effort string) string {
	t.Helper()

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 120)
	m.label = "deepseek-v4-flash"
	m.effortLevel = effort
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	return next.(chatTUI).View().Content
}

func renderStatuslineViewWithGitAndEffort(t *testing.T) string {
	t.Helper()

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 120)
	m.label = "deepseek-v4-flash"
	m.effortLevel = "auto"
	m.gitStatus = gitStatus{
		Repo:      "Corvus",
		Branch:    "codex/demo",
		Added:     3,
		Removed:   1,
		Untracked: 2,
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	return next.(chatTUI).View().Content
}

func renderStatuslineViewWithCache(t *testing.T) string {
	t.Helper()

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
	ctrl := control.New(control.Options{Executor: exec})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 160)
	m.label = "deepseek-v4-flash"
	m.effortLevel = "auto"
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	return next.(chatTUI).View().Content
}

func renderPlanStatuslineView(t *testing.T) string {
	t.Helper()

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	m.planMode = true
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(chatTUI).View().Content
}

func bottomStatusPlain(content string) string {
	return strings.Join(bottomStatusPlainLines(content), "\n")
}

func bottomStatusPlainLines(content string) []string {
	lines := strings.Split(ansi.Strip(content), "\n")
	if len(lines) < 3 {
		return lines
	}
	return lines[len(lines)-3:]
}

// footerInteractionPlain returns the footer interaction row (idle/hint/state),
// skipping composer rows that bottomStatusPlainLines may include when the
// status block is only one line tall.
func footerInteractionPlain(content string) string {
	for _, line := range strings.Split(ansi.Strip(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Interaction row carries idle/yolo state or contextual chrome labels.
		if strings.Contains(line, "ready") ||
			strings.Contains(line, "就绪") ||
			strings.Contains(line, "就緒") ||
			strings.Contains(line, "approvals skipped") ||
			strings.Contains(line, "Shift+Tab") ||
			strings.Contains(line, "tool approvals") {
			return line
		}
	}
	// Fallback: last non-empty line of the previous 3-line heuristic.
	for i := len(bottomStatusPlainLines(content)) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(bottomStatusPlainLines(content)[i]); s != "" {
			return bottomStatusPlainLines(content)[i]
		}
	}
	return ""
}
