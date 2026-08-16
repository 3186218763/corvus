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
	// The badge must never sit beside the prompt inside the composer.
	shared := false
	for _, line := range strings.Split(plainView, "\n") {
		if strings.Contains(line, "Plan") && strings.Contains(line, "›") {
			shared = true
			break
		}
	}
	if shared {
		t.Fatalf("Plan badge must not share the composer prompt line:\n%s", plainView)
	}
	if !strings.Contains(content, "\x1b[48;2;37;99;235m") {
		t.Fatalf("Plan badge should use blue pill background, got:\n%q", content)
	}

	primary := strings.TrimSpace(ansi.Strip(m.primaryStatusLine(false, false)))
	if strings.Contains(primary, "Plan") {
		t.Fatalf("footer primary must not contain the mode pill: %q", primary)
	}
	if !strings.Contains(primary, "ready") {
		t.Fatalf("footer primary missing idle state: %q", primary)
	}
	// The badge anchors the bottom-left of the footer row under the composer,
	// never a dangling row of its own between the box and the status line.
	footer := footerInteractionPlain(content)
	if !strings.HasPrefix(strings.TrimSpace(footer), "Plan") {
		t.Fatalf("footer row should start with the Plan badge, got %q", footer)
	}
	standalone := false
	for _, line := range strings.Split(plainView, "\n") {
		if strings.TrimSpace(line) == "Plan" {
			standalone = true
			break
		}
	}
	if standalone {
		t.Fatalf("Plan badge must not sit on its own row under the composer:\n%s", plainView)
	}
}

func TestComposerBadgeAndStatusFitWithinFrameWidth(t *testing.T) {
	// The mode badge moved to the footer row; the composer owns the full frame.
	// Composer and badge rows must never exceed the frame width.
	ctrl := control.New(control.Options{})
	ctrl.SetToolApprovalMode(control.ToolApprovalDontAsk)
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 20)
	m.cfg = config.Default()
	m.cfg.UI.ShortcutLayout = "desktop"
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 12})
	m = next.(chatTUI)
	if got := m.modeTagText(); got != "Don't Ask" {
		t.Fatalf("modeTagText() = %q, want Don't Ask", got)
	}
	// Same budget for the SetWidth path and the painter.
	if got, want := m.composerContentWidth(), m.composerFrameWidth(); got != want {
		t.Fatalf("composerContentWidth = %d, want %d (full frame)", got, want)
	}
	for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if w := visibleWidth(line); w > m.composerFrameWidth() {
			// Transcript viewport may pad; only check composer-ish rows with badge.
			if strings.Contains(line, "Don't Ask") || strings.Contains(line, "›") {
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
	if strings.Contains(primary, "Shell") {
		t.Fatalf("footer primary must not contain the Shell pill: %q", primary)
	}
}

func TestClassicModeTagShowsAskForDefaultPosture(t *testing.T) {
	i18n.DetectLanguage("en")
	ctrl := control.New(control.Options{})
	// The controller's default posture is Ask; the classic layout must show
	// "Ask", not the misleading "Auto".
	if got := ctrl.ToolApprovalMode(); got != control.ToolApprovalAsk {
		t.Fatalf("controller default mode = %q, want ask", got)
	}
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	m.cfg = config.Default()
	m.cfg.UI.ShortcutLayout = "classic"
	if got := m.modeTagText(); got != "Ask" {
		t.Fatalf("classic modeTagText() = %q, want Ask", got)
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
	m.cfg.UI.ShortcutLayout = "desktop"
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
	// Mode pill anchors the footer left edge; the footer keeps the idle state.
	// The default posture is Ask, so the pill must not claim Auto.
	if !strings.Contains(plainView, "Ask") {
		t.Fatalf("idle view missing Ask mode badge:\n%s", plainView)
	}
	if strings.Contains(plainView, "Auto") {
		t.Fatalf("idle view mislabels the default Ask posture as Auto:\n%s", plainView)
	}
	footer := footerInteractionPlain(content)
	if !strings.Contains(footer, "ready") {
		t.Fatalf("idle status line missing ready state:\n%s", footer)
	}
	if !strings.HasPrefix(strings.TrimSpace(footer), "Ask") {
		t.Fatalf("footer row should start with the Ask badge (bottom-left anchor), got %q", footer)
	}
	for _, old := range []string{"Shift-Tab", "Ctrl-O", "Ctrl-D", "Enter sends", "Esc clears/exits state", "PgUp/PgDn"} {
		if strings.Contains(footer, old) {
			t.Fatalf("idle status line should not contain %q:\n%s", old, footer)
		}
	}
	if strings.Contains(footer, "Shift+Tab") || strings.Contains(footer, "Ctrl+Y") {
		t.Fatalf("idle status line should not carry shortcut hints:\n%s", footer)
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
	if !strings.Contains(footer, "approvals skipped") {
		t.Fatalf("YOLO status line missing warning text:\n%s", footer)
	}
	if !strings.HasPrefix(strings.TrimSpace(footer), "YOLO") {
		t.Fatalf("footer row should start with the YOLO badge (bottom-left anchor), got %q", footer)
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
	if !strings.Contains(footer, "ready") {
		t.Fatalf("plan status line missing idle status:\n%s", footer)
	}
	if !strings.HasPrefix(strings.TrimSpace(footer), "Plan") {
		t.Fatalf("footer row should start with the Plan badge (bottom-left anchor), got %q", footer)
	}
	if !strings.Contains(content, "\x1b[48;2;37;99;235m") {
		t.Fatalf("Plan badge should use blue pill background, got:\n%q", content)
	}
}

func TestStatuslineLocalizedIdleState(t *testing.T) {
	i18n.DetectLanguage("zh")
	t.Cleanup(func() { i18n.DetectLanguage("en") })

	content := renderStatuslineView(t, false)
	plainView := ansi.Strip(content)
	plain := bottomStatusPlain(content)
	if !strings.Contains(plainView, "Ask") {
		t.Fatalf("localized view missing Ask mode badge:\n%s", plainView)
	}
	if !strings.Contains(plain, "就绪") {
		t.Fatalf("localized idle state missing:\n%s", plain)
	}
	if strings.Contains(plain, "ready") {
		t.Fatalf("localized status line should not fall back to English:\n%s", plain)
	}
}

func TestDesktopShortcutStatuslineShowsAskBadge(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineViewWithShortcutLayout(t, "desktop")
	plainView := ansi.Strip(content)
	footer := footerInteractionPlain(content)
	if !strings.Contains(plainView, "Ask") {
		t.Fatalf("desktop shortcut view missing Ask mode badge:\n%s", plainView)
	}
	if !strings.HasPrefix(strings.TrimSpace(footer), "Ask") {
		t.Fatalf("footer row should start with the Ask badge (bottom-left anchor), got %q", footer)
	}
}

// TestComposerCursorAlignsWithInputRow proves the real terminal cursor lands on
// the composer's first visible row: the mode badge lives in the footer below,
// so nothing between the viewport and the input box inflates the Y offset.
func TestComposerCursorAlignsWithInputRow(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.TrueColor
	i18n.DetectLanguage("en")

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(chatTUI)

	v := m.View()
	if v.Cursor == nil {
		t.Fatal("composer visible, expected a view cursor")
	}
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	if v.Cursor.Y >= len(lines) {
		t.Fatalf("cursor Y %d out of view (has %d lines): %+v", v.Cursor.Y, len(lines), v.Cursor)
	}
	if row := lines[v.Cursor.Y]; !strings.Contains(row, "›") {
		t.Fatalf("cursor row %d should be the composer prompt row, got %q (cursor %+v)", v.Cursor.Y, row, v.Cursor)
	}
	// The row below the composer is the footer carrying the mode badge; a
	// dangling chip on its own row would read as an input-box protrusion.
	if v.Cursor.Y+1 < len(lines) {
		below := strings.TrimSpace(lines[v.Cursor.Y+1])
		if below != "" && !strings.Contains(below, "ready") && !strings.Contains(below, "就绪") {
			t.Fatalf("row below composer = %q, want footer (badge + ready state) or blank composer padding", below)
		}
	}
}

func TestComposerCursorAccountsForWrappedWorkingRows(t *testing.T) {
	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 24)
	m.state = tuiRunning
	next, _ := m.Update(tea.WindowSizeMsg{Width: 24, Height: 14})
	m = next.(chatTUI)

	working := wrapStatusLine(m.runningWorkingLine(false, false), m.width)
	if rows := strings.Count(working, "\n") + 1; rows < 2 {
		t.Fatalf("fixture must wrap the working line, got %d row: %q", rows, ansi.Strip(working))
	}
	v := m.View()
	if v.Cursor == nil {
		t.Fatal("composer visible, expected a view cursor")
	}
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	if v.Cursor.Y >= len(lines) || !strings.Contains(lines[v.Cursor.Y], "›") {
		t.Fatalf("wrapped working rows displaced cursor Y=%d:\n%s", v.Cursor.Y, ansi.Strip(v.Content))
	}
}

func TestStatuslineShowsEffortInPersistentFooter(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineViewWithEffort(t, "auto")
	lines := strings.Split(ansi.Strip(content), "\n")
	statusLine := lines[len(lines)-1]
	if !strings.Contains(statusLine, "deepseek-v4-flash") {
		t.Fatalf("session row should keep effort beside the model:\n%s", statusLine)
	}
}

func TestStatuslineOmitsCacheRatesFromPersistentFooter(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineViewWithCache(t)
	plain := ansi.Strip(content)
	if !strings.Contains(plain, "deepseek-v4-flash") {
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
	if !strings.Contains(plain, "deepseek-v4-flash") {
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
	if !strings.Contains(plain, "deepseek-v4-flash") {
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
			configureCLIThemeWithStyle(tt.mode, "")
			m := newTestChatTUI()
			m.effortLevel = "max"
			content := m.effortTag()
			if !strings.Contains(ansi.Strip(content), "Effort max") {
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
	m.cfg.UI.ShortcutLayout = layout
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
