// Package cli implements the terminal user interface.
package cli

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
	"reasonix/internal/provider/openai"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

var (
	runInteractiveSession = chatREPL
	cliIsInteractive      = isInteractive
)

func Run(args []string, version string) int {
	i18n.DetectLanguage("")
	migrateLegacyConfigForCLI()
	if cfg, err := config.Load(); err == nil && cfg.Language != "" {
		i18n.DetectLanguage(cfg.Language)
	}
	// Help and version work without a TTY so scripts and --help in non-interactive
	// shells still get a useful answer before the TUI gate.
	if code, handled := handleInfoArgs(args, version); handled {
		return code
	}
	if !cliIsInteractive() {
		fmt.Fprintln(os.Stderr, "reasonix requires an interactive terminal; use the TUI entry point from a TTY")
		return 1
	}
	return runInteractiveSession(args, version)
}

// handleInfoArgs answers -h/--help/help and -v/--version/version without opening
// the TUI. Returns handled=false for every other invocation.
func handleInfoArgs(args []string, version string) (code int, handled bool) {
	if len(args) == 0 {
		return 0, false
	}
	// Bare subcommand-style tokens only count in position 0.
	switch args[0] {
	case "help":
		fmt.Println(i18n.M.UsageBody)
		return 0, true
	case "version":
		fmt.Println("reasonix", version)
		return 0, true
	}
	// Flag-style help/version may appear anywhere before "--".
	for _, arg := range args {
		if arg == "--" {
			break
		}
		switch arg {
		case "--help", "-h":
			fmt.Println(i18n.M.UsageBody)
			return 0, true
		case "--version", "-v":
			fmt.Println("reasonix", version)
			return 0, true
		}
	}
	return 0, false
}

func migrateLegacyConfigForCLI() {
	if _, err := config.MigrateLegacyIfNeeded(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: config migration failed:", err)
	}
	if _, err := config.ApplyUserConfigUpgradesOnStartup(config.UserConfigPath()); err != nil {
		fmt.Fprintln(os.Stderr, "warning: config upgrade failed:", err)
	}
}

func migrateMCPConfigForCLIWorkspace() {
	if wd, err := os.Getwd(); err == nil {
		if _, err := config.MigrateMCPToUserConfigOnUpgrade([]string{wd}); err != nil {
			fmt.Fprintln(os.Stderr, "warning: MCP config migration failed:", err)
		}
	}
}

func configureCLIThemeFromConfig() {
	if cfg, err := config.Load(); err == nil {
		configureCLIThemeWithStyle(cfg.UITheme(), cfg.UIThemeStyle())
		cliCursorShape = cfg.UICursorShape()
	} else {
		configureCLITheme("auto")
		cliCursorShape = "bar"
	}
}

func configureCLIThemeFromConfigForTTYOutput() {
	if isTTY(os.Stdout) {
		withTerminalProbe(configureCLIThemeFromConfig)
		return
	}
	configureCLIThemeFromConfig()
}

func setupProfile(ctx context.Context, modelName string, maxStepsOverride int, requireKey bool, sink event.Sink, profile string, workspaceRoot string) (*control.Controller, error) {
	return setupProfileWithOverrides(ctx, modelName, maxStepsOverride, requireKey, sink, profile, cliBuildOverrides{WorkspaceRoot: workspaceRoot})
}

type cliBuildOverrides struct {
	Effort               *string
	PermissionAllow      []string
	AdditionalDirs       []string
	WorkspaceRoot        string
	HeadlessApprovalMode string
	Stderr               io.Writer
	OnSessionRecovered   func(control.SessionRecoveryInfo) error
}

func setupProfileWithOverrides(ctx context.Context, modelName string, maxStepsOverride int, requireKey bool, sink event.Sink, profile string, overrides cliBuildOverrides) (*control.Controller, error) {
	migrateMCPConfigForCLIWorkspace()
	return boot.Build(ctx, cliProfileBuildOptions(modelName, maxStepsOverride, requireKey, sink, profile, overrides))
}

func cliProfileBuildOptions(modelName string, maxStepsOverride int, requireKey bool, sink event.Sink, profile string, overrides cliBuildOverrides) boot.Options {
	return boot.Options{
		Model:                modelName,
		MaxSteps:             maxStepsOverride,
		MaxStepsKey:          "--max-steps",
		RequireKey:           requireKey,
		Sink:                 sink,
		TokenMode:            profile,
		SessionDir:           resolveCLISessionDir(),
		WorkspaceRoot:        overrides.WorkspaceRoot,
		EffortOverride:       overrides.Effort,
		PermissionAllow:      overrides.PermissionAllow,
		AdditionalDirs:       overrides.AdditionalDirs,
		HeadlessApprovalMode: overrides.HeadlessApprovalMode,
		AutoPricingCurrency:  cliAutoPricingCurrency(),
		StatsSource:          "cli",
		Stderr:               overrides.Stderr,
		OnSessionRecovered:   overrides.OnSessionRecovered,
	}
}

func cliAutoPricingCurrency() string {
	switch i18n.CurrentLanguage() {
	case "zh", "zh-TW":
		return "CNY"
	default:
		return "USD"
	}
}

type cliPermissionMode struct {
	approval string
	plan     bool
	allow    []string
}

func parsePermissionMode(value string) (cliPermissionMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "default", "ask":
		return cliPermissionMode{approval: control.ToolApprovalAsk}, nil
	case "auto":
		return cliPermissionMode{approval: control.ToolApprovalAuto}, nil
	case "acceptedits", "accept-edits":
		return cliPermissionMode{approval: control.ToolApprovalAsk, allow: []string{
			"write_file", "edit_file", "multi_edit", "move_file", "notebook_edit", "delete_range", "delete_symbol",
		}}, nil
	case "manual":
		return cliPermissionMode{approval: control.ToolApprovalAsk}, nil
	case "dontask", "dont-ask":
		return cliPermissionMode{approval: control.ToolApprovalDontAsk}, nil
	case "plan":
		return cliPermissionMode{approval: control.ToolApprovalAsk, plan: true}, nil
	case "bypasspermissions", "bypass-permissions", "yolo":
		return cliPermissionMode{approval: control.ToolApprovalYolo}, nil
	default:
		return cliPermissionMode{}, fmt.Errorf("unknown permission mode %q (want manual, ask, auto, acceptEdits, dontAsk, plan, or bypassPermissions)", value)
	}
}

func applyPermissionMode(ctrl *control.Controller, mode cliPermissionMode) {
	if ctrl == nil {
		return
	}
	ctrl.SetToolApprovalMode(mode.approval)
	ctrl.SetPlanMode(mode.plan)
}

func resolveCLISessionDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return config.SessionDir()
	}
	if projDir := config.ProjectSessionDir(cwd); projDir != "" && projDir != config.SessionDir() {
		return projDir
	}
	return config.SessionDir()
}

func setupQuietProfile(ctx context.Context, modelName string, maxStepsOverride int, requireKey bool, sink event.Sink, profile string, overrides cliBuildOverrides) (*control.Controller, error) {
	if overrides.Stderr == nil {
		overrides.Stderr = io.Discard
	}
	return boot.Build(ctx, cliProfileBuildOptions(modelName, maxStepsOverride, requireKey, sink, profile, overrides))
}

func parseRuntimeProfile(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "balanced", boot.TokenModeFull:
		return boot.TokenModeFull, nil
	case boot.TokenModeEconomy:
		return boot.TokenModeEconomy, nil
	case boot.TokenModeDelivery:
		return boot.TokenModeDelivery, nil
	default:
		return "", fmt.Errorf("unknown runtime profile %q (want economy, balanced, or delivery)", value)
	}
}

func chdirTo(dir string) int {
	if dir == "" {
		return 0
	}
	if err := os.Chdir(dir); err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 2
	}
	return 0
}

func workspaceRootForDir(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve --dir workspace root: %w", err)
	}
	return wd, nil
}

func modelForResumePath(modelName, resumePath string, cfg *config.Config) string {
	if strings.TrimSpace(modelName) != "" || strings.TrimSpace(resumePath) == "" {
		return modelName
	}
	sessionModel, ok := agent.LoadSessionModel(resumePath)
	if !ok {
		return modelName
	}
	if cfg == nil {
		return sessionModel
	}
	if _, ok := cfg.ResolveModel(sessionModel); !ok {
		return modelName
	}
	return sessionModel
}

func loadResumableSession(path string) (*agent.Session, error) {
	if agent.IsCleanupPending(path) {
		return nil, fmt.Errorf("session is pending cleanup")
	}
	return agent.LoadSession(path)
}

func registerContinueFlag(fs *pflag.FlagSet) *bool {
	return fs.BoolP("continue", "c", false, "resume the most recent saved session")
}

func chatREPL(args []string, version string) int {
	fs := pflag.NewFlagSet("reasonix", pflag.ContinueOnError)
	fs.SetInterspersed(true)
	model := fs.String("model", "", "provider name (default: config default_model)")
	profileFlag := fs.String("profile", "balanced", "runtime profile: economy | balanced | delivery")
	maxSteps := fs.Int("max-steps", 0, "one-off max tool-call rounds (0 = automatic)")
	cont := registerContinueFlag(fs)
	resume := fs.StringP("resume", "r", "", "resume by session ID/query, or open the picker when no value is given")
	fs.Lookup("resume").NoOptDefVal = resumePickerSentinel
	copySession := fs.Bool("copy", false, "with --resume/--continue: duplicate the selected session and continue in the copy (escape hatch when the original is held by another Reasonix process)")
	yolo := fs.Bool("dangerously-skip-permissions", false, "YOLO: auto-approve approval-gated tool calls this session; same runtime mode as Ctrl+Y")
	fs.BoolVar(yolo, "yolo", false, "alias for --dangerously-skip-permissions")
	dir := fs.String("dir", "", "change to this directory first (project root); config, sandbox and file tools resolve from here")
	effort := fs.String("effort", "", "session reasoning effort override")
	permissionMode := fs.String("permission-mode", "ask", "permission mode: manual | ask | auto | acceptEdits | dontAsk | plan | bypassPermissions")
	var additionalDirs []string
	fs.StringArrayVar(&additionalDirs, "add-dir", nil, "allow tool access to an additional directory (repeatable)")
	var allowedToolValues []string
	fs.StringArrayVar(&allowedToolValues, "allowed-tools", nil, "comma or space-separated permission rules to allow")
	fs.StringArrayVar(&allowedToolValues, "allowedTools", nil, "alias for --allowed-tools")
	if code, ok := parseCommandFlags(fs, normalizeOptionalResumeArg(args)); !ok {
		return code
	}
	allowedTools, err := splitAllowedToolRules(allowedToolValues)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 2
	}
	profile, err := parseRuntimeProfile(*profileFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 2
	}
	permissions, err := parsePermissionMode(*permissionMode)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 2
	}
	allowedTools = uniqueStrings(append(allowedTools, permissions.allow...))
	if rc := chdirTo(*dir); rc != 0 {
		return rc
	}
	workspaceRoot, err := workspaceRootForDir(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	cfg, err := config.Load()
	if err == nil {
		configureCLIThemeWithStyle(cfg.UITheme(), cfg.UIThemeStyle())
		cliCursorShape = cfg.UICursorShape()
	}
	// Bubble Tea owns the terminal from the resume picker through controller
	// shutdown. Route process logs and plugin stderr to a private, bounded file
	// for that whole lifetime; user-facing warnings arrive as typed TUI events.
	diagnostics := startTUIDiagnostics(config.ReasonixHomeDir())
	defer diagnostics.Close()

	// Decide whether we're starting fresh or resuming. --resume opens an
	// interactive picker; --continue / -c jumps straight into the newest.
	var resumePath string
	resumeValue := strings.TrimSpace(*resume)
	switch strings.ToLower(resumeValue) {
	case "true":
		resumeValue = resumePickerSentinel
	case "false":
		resumeValue = ""
	}
	switch {
	case resumeValue == resumePickerSentinel:
		path, rc := pickSessionToResume()
		if rc != 0 {
			return rc
		}
		resumePath = path
	case resumeValue != "":
		path, err := resolveSessionQuery(resolveCLISessionDir(), resumeValue)
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		resumePath = path
	case *cont:
		sessions, err := agent.ListSessions(resolveCLISessionDir())
		if err != nil || len(sessions) == 0 {
			fmt.Fprintln(os.Stderr, i18n.M.NoSessionToResume)
			return 1
		}
		resumePath = sessions[0].Path
	}
	if *copySession && resumePath == "" {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "--copy requires --resume or --continue")
		return 2
	}
	if *copySession {
		copied, err := copySessionForWriting(resumePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("continuing in a session copy: %s\n", copied)
		resumePath = copied
	}
	// Own the active session file for the TUI's lifetime; in-TUI switches
	// (/resume, /switch, /new, ...) move the lease with the active path.
	// Refusing a held resume target up front is what keeps a desktop window
	// and this chat from silently double-writing one transcript.
	leases := control.NewSessionLeaseKeeper()
	defer leases.Release()
	if resumePath != "" {
		if err := leases.Rebind(resumePath); err != nil {
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, sessionLeaseResumeRefusal(err))
			} else {
				fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			}
			return 1
		}
	}

	ctx := context.Background()
	*model = modelForResumePath(*model, resumePath, cfg)

	// Plumb the controller's typed event stream through a channel so each event
	// can become a tea.Msg inside the TUI's update loop. Buffered generously:
	// streaming bursts (tool results, long answers) shouldn't backpressure the
	// agent goroutine.
	eventCh := make(chan event.Event, 1024)

	var sink event.Sink = &eventSink{ch: eventCh}
	var effortOverride *string
	if strings.TrimSpace(*effort) != "" {
		effortOverride = effort
	}
	overrides := cliBuildOverrides{
		Effort:             effortOverride,
		PermissionAllow:    allowedTools,
		AdditionalDirs:     additionalDirs,
		WorkspaceRoot:      workspaceRoot,
		Stderr:             diagnostics.Writer(),
		OnSessionRecovered: cliSessionRecoveredHandler(leases),
	}
	ctrl, err := setupProfileWithOverrides(ctx, *model, *maxSteps, false, sink, profile, overrides)
	if err != nil && errors.Is(err, boot.ErrUnknownModel) && isInteractive() && config.SourcePath() == "" {
		// True first run whose default model can't resolve: guide setup, then retry.
		// With a config present, fall through to the descriptive error — re-running
		// the wizard would overwrite the user's config (#2856).
		fmt.Fprintln(os.Stderr, i18n.M.ReconfigureOnUnknownModel)
		if rc := interactiveSetup(defaultConfigTarget(), defaultEnvTarget()); rc != 0 {
			return rc
		}
		ctrl, err = setupProfileWithOverrides(ctx, *model, *maxSteps, false, sink, profile, overrides)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}

	// Decide where this conversation's auto-save lands. A resume reuses the
	// file so closing/reopening keeps appending to the same history; a fresh
	// session lands in a new file stamped with the model name.
	if resumePath != "" {
		loaded, err := agent.LoadSession(resumePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		ctrl.Resume(loaded, resumePath)
	}
	ctrl.EnsureSessionPath()
	// Fresh sessions take the lease too (defensive: the path is brand new); a
	// resumed path is already held, making this a no-op.
	if err := leases.Rebind(ctrl.SessionPath()); err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, control.SessionInUseMessage(err)+"; "+control.SessionLeaseCloseHint)
		return 1
	}

	// Surface a missing-key warning inside the TUI banner so the first message
	// failing is at least pre-announced; the user can still enter chat.
	// resolveModelForCLI transparently falls through a keyless default to the
	// next configured provider (issue #6996). Validating the final ref is a
	// no-op for that configured fallback and preserves the warning when every
	// eligible chat provider is still keyless.
	missing := ""
	if cfg, loadErr := config.Load(); loadErr == nil {
		name, _, err := resolveModelForCLI(*model, cfg)
		switch {
		case err != nil:
			missing = err.Error()
		case name != "":
			if vErr := cfg.Validate(name); vErr != nil {
				missing = vErr.Error()
			}
		}
	}

	// Initial terminal width — the TUI re-flows on every WindowSizeMsg so
	// this is just a starting estimate before the first resize event lands.
	termW := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		termW = w
	}

	// Route "ask" decisions to the TUI: the controller emits an ApprovalRequest
	// event and blocks until the user answers via ctrl.Approve. Sub-agents (the
	// task tool) keep their headless gate from setup — no UI to prompt through.
	ctrl.EnableInteractiveApproval()
	applyPermissionMode(ctrl, permissions)
	// YOLO: skip ordinary tool approval requests for the session (deny rules and
	// fresh reviews still apply; ask questions and plan approvals still wait).
	if *yolo {
		ctrl.SetAutoApproveTools(true)
	}

	m := newChatTUI(ctrl, missing, eventCh, termW)
	m.planMode = permissions.plan
	m.leases = leases
	if cfg, err := config.Load(); err == nil {
		m.outputStyle = cfg.Agent.OutputStyle    // shown as the active entry in /output-style
		m.statuslineCmd = cfg.Statusline.Command // custom status-line command, "" = built-in row
		m.showReasoning = cfg.UI.ShowReasoning   // /verbose persistence: start with config default
		m.cfg = cfg
	}

	// /model support: a pure builder the TUI calls to rebuild on a different
	// model (carrying the conversation). It must NOT touch the running model —
	// runModelSubcommand performs the swap on the live copy. The same stable sink
	// feeds the new controller, so events keep flowing to this TUI.
	m.buildController = func(spec controllerBuildSpec, carry []provider.Message, resumePath string, oldCtrl control.SessionAPI) (*control.Controller, error) {
		effectiveOverrides := overrides
		if spec.EffortOverride != nil {
			effectiveOverrides.Effort = spec.EffortOverride
		}
		c, err := setupQuietProfile(ctx, spec.ModelRef, *maxSteps, false, sink, spec.RuntimeProfile, effectiveOverrides)
		if err != nil {
			return nil, err
		}
		if spec.EffortOverride != nil {
			overrides.Effort = spec.EffortOverride
		}
		// Keep the carried conversation in its existing file so the switch doesn't
		// orphan a duplicate (#2807).
		path := agent.ContinueSessionPath(resumePath, c.SessionDir(), c.Label())
		if err := adoptCarriedHistoryPreservingProfileAndGrants(c, carry, path, oldCtrl); err != nil {
			c.Close()
			return nil, err
		}
		c.EnableInteractiveApproval()
		c.SetPlanMode(spec.PlanMode)
		if spec.ToolApprovalMode != "" {
			c.SetToolApprovalMode(spec.ToolApprovalMode)
		}
		return c, nil
	}
	m.runtimeProfile = profile
	if effortOverride != nil {
		m.effortLevel = *effortOverride
	}
	if effortOverride == nil {
		m.refreshEffortStatus()
	}

	if m.nativeScrollback {
		prepareNativeScrollback(os.Stdout, m.bottomRows())
	}

	// Non-Termux terminals use an alt-screen transcript viewport. Termux stays
	// in the normal buffer so native touch scrollback and soft-keyboard focus
	// keep working; finalized transcript lines are emitted via tea.Println.
	p := tea.NewProgram(m)
	// SSH drop (SIGHUP) or service stop (SIGTERM): persist the conversation
	// before the terminal goes away, then unwind through the normal close path
	// so resume picks up the interrupted session (#3772).
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		for range hangup {
			p.Send(tuiShutdownMsg{})
		}
	}()
	final, runErr := p.Run()
	signal.Stop(hangup)
	// Close the active controller plus any retired ones from /model switches.
	// Retired controllers were stashed rather than closed at switch time
	// because Controller.Close() runs SessionEnd hooks and kills plugin
	// subprocesses — operations that corrupt bubbletea's terminal raw mode
	// when executed while the TUI is alive.
	if fm, ok := final.(chatTUI); ok {
		for _, oc := range fm.oldControllers {
			oc.Close()
		}
		if fm.ctrl != nil {
			fm.ctrl.Close()
		} else {
			ctrl.Close()
		}
	} else {
		ctrl.Close()
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, runErr)
		return 1
	}
	return 0
}

func adoptCarriedHistoryPreservingProfileAndGrants(c *control.Controller, carry []provider.Message, path string, oldCtrl control.SessionAPI) error {
	if fresh := c.History(); len(fresh) > 0 && fresh[0].Role == provider.RoleSystem {
		if len(carry) > 0 && carry[0].Role == provider.RoleSystem {
			carry[0] = fresh[0]
		} else {
			carry = append([]provider.Message{fresh[0]}, carry...)
		}
	}
	c.AdoptHistory(carry, path)
	if prev, ok := oldCtrl.(*control.Controller); ok {
		c.RestoreSessionAuthorizations(prev.SessionAuthorizations())
	}
	// Persist the adopted history now: the splice above only refreshed the new
	// controller's memory and nothing saves again until the next turn ends, so
	// quitting right after the switch and resuming would otherwise revive the
	// outgoing profile's contract from disk.
	if path != "" {
		if err := c.Snapshot(); err != nil {
			return fmt.Errorf("snapshot after runtime switch: %w", err)
		}
	}
	return nil
}

func prepareNativeScrollback(w io.Writer, rows int) {
	// Clear the terminal's scrollback history so a reopened chat starts
	// with a clean slate (Termux stays in the normal buffer, so prior
	// output would otherwise remain visible above the banner).
	fmt.Fprint(w, "\x1B[3J\x1B[2J\x1B[H")
	reserveNativeScrollbackFrame(w, rows)
}

func reserveNativeScrollbackFrame(w io.Writer, rows int) {
	for i := 0; i < rows; i++ {
		fmt.Fprintln(w)
	}
}

func defaultConfigTarget() string {
	if p := config.UserConfigPath(); p != "" {
		return p
	}
	return "reasonix.toml"
}

func defaultEnvTarget() string {
	return config.CredentialsTargetDescription()
}

func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func interactiveSetup(configPath, envPath string) int {
	// Seed from the existing config when reconfiguring, so a re-run to fix a key
	// preserves the user's providers / agent settings instead of resetting to
	// defaults. First run (no file) falls back to the built-in defaults.
	cfg, err := config.LoadForEditReadOnlyStrict(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.WriteConfigErr, err)
		return 1
	}
	session := newProviderSetupSessionForPath(cfg, configPath)
	lang, err := selectLanguage()
	if err != nil {
		fmt.Fprintln(os.Stderr, "\nsetup cancelled.")
		return 1
	}
	session.setLanguage(lang)
	session.applyDeepSeekOfficialDefaultPricing()
	session.resetProviderSummaryBaseline()
	i18n.DetectLanguage(lang)

	// Now that the catalogue matches the user's choice, show the welcome banner
	// in their language before any substantive prompt.
	fmt.Println()
	fmt.Print(boxed([]string{
		accent("◆") + " " + fmt.Sprintf(i18n.M.WelcomeTitleFmt, bold("reasonix")),
		"",
		dim(i18n.M.NoConfigYet),
	}))
	fmt.Println()

	return runProviderSetupManager(session, configPath, envPath)
}

func pickSessionToResume() (string, int) {
	sessions, err := agent.ListSessions(resolveCLISessionDir())
	if err != nil || len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, i18n.M.NoSessionToResume)
		return "", 1
	}
	if !isInteractive() {
		fmt.Fprintln(os.Stderr, i18n.M.ResumeRequiresTTY)
		return "", 1
	}
	const cap = 10
	if len(sessions) > cap {
		sessions = sessions[:cap]
	}
	items := make([]menuItem, len(sessions))
	for i, s := range sessions {
		when := s.ModTime.Local().Format("01-02 15:04")
		preview := s.Preview
		if preview == "" {
			preview = "(no user message yet)"
		}
		items[i] = menuItem{
			name: when,
			desc: fmt.Sprintf("%d turns · %s", s.Turns, preview),
		}
	}
	idx, err := selectOne(i18n.M.PickSessionLabel, items)
	if err != nil {
		return "", 1
	}
	return sessions[idx].Path, 0
}

func selectLanguage() (string, error) {
	detected := i18n.DetectLanguage("")
	items := []menuItem{{name: "English"}, {name: "中文 (简体)"}}
	tags := []string{"en", "zh"}
	if detected == "zh" {
		items[0], items[1] = items[1], items[0]
		tags[0], tags[1] = tags[1], tags[0]
	}
	idx, err := selectOne("Language · 语言", items)
	if err != nil {
		return "", err
	}
	return tags[idx], nil
}

func familyStaticModels(providers []config.ProviderEntry, idxs []int) []string {
	var out []string
	seen := map[string]bool{}
	for _, i := range idxs {
		for _, m := range providers[i].ModelList() {
			if m != "" && !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func fetchOrFallback(probe *config.ProviderEntry, famName string) []string {
	static := probe.ModelList()
	if probe.BaseURL == "" {
		return static
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := probe.FetchModels(ctx)
	if err != nil || len(models) == 0 {
		if len(static) > 0 {
			fmt.Fprintf(os.Stderr, "  %s\n", dim(fmt.Sprintf(i18n.M.FetchModelsUsingPresetsFmt, famName)))
		}
		return static
	}
	fmt.Printf("  %s\n", green(fmt.Sprintf(i18n.M.FetchModelsSuccessFmt, len(models), famName)))
	return models
}

func fetchModelListCompat(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	candidates, err := config.BuildModelFetchURLs(baseURL, "")
	if err != nil {
		return nil, err
	}
	var lastErr error
	var firstHardErr error
	for _, u := range candidates {
		models, err := openai.FetchModels(ctx, u, apiKey, nil)
		if err == nil {
			return models, nil
		}
		lastErr = err
		if !openai.IsModelFetchEndpointMiss(err) && firstHardErr == nil {
			firstHardErr = err
		}
	}
	if firstHardErr != nil {
		return nil, firstHardErr
	}
	if lastErr != nil {
		slog.Debug("model-list probe: all candidates missed", "base_url", baseURL, "err", lastErr)
	}
	return nil, nil
}

func buildFamilyEntries(probe config.ProviderEntry, members []config.ProviderEntry, selected []string) []config.ProviderEntry {
	tmpl := map[string]config.ProviderEntry{probe.Name: probe}
	ownerName := map[string]string{}
	for _, m := range members {
		tmpl[m.Name] = m
		for _, id := range m.ModelList() {
			ownerName[id] = m.Name
		}
	}
	var order []string
	groups := map[string][]string{}
	for _, sm := range selected {
		name, ok := ownerName[sm]
		if !ok {
			name = probe.Name
		}
		if _, seen := groups[name]; !seen {
			order = append(order, name)
		}
		groups[name] = append(groups[name], sm)
	}
	out := make([]config.ProviderEntry, 0, len(order))
	for _, name := range order {
		out = append(out, buildFamilyEntry(tmpl[name], groups[name]))
	}
	return out
}

func buildFamilyEntry(probe config.ProviderEntry, selected []string) config.ProviderEntry {
	entry := probe
	entry.Models = selected
	entry.Model = selected[0]
	if entry.Default == "" || !containsString(selected, entry.Default) {
		entry.Default = selected[0]
	}
	return entry
}

func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func filterStaleCustomEntries(providers []config.ProviderEntry) (kept, dropped []config.ProviderEntry) {
	for _, p := range providers {
		if p.Name == "custom" && p.Kind == "openai" {
			dropped = append(dropped, p)
			continue
		}
		if p.Name == "anthropic" && p.Kind == "anthropic" {
			dropped = append(dropped, p)
			continue
		}
		kept = append(kept, p)
	}
	return
}

func providerSlug(kind, baseURL string) string {
	var host string
	if u, err := url.Parse(baseURL); err == nil {
		host = u.Host
	}
	if host == "" {
		sum := sha1.Sum([]byte(baseURL))
		return kind + "-" + hex.EncodeToString(sum[:4])
	}
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	var b strings.Builder
	prevDash := false
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if slug == "" {
		sum := sha1.Sum([]byte(baseURL))
		return kind + "-" + hex.EncodeToString(sum[:4])
	}
	return kind + "-" + slug
}

func apiKeyEnvFromProviderName(name string) string {
	stem := strings.ToUpper(strings.TrimSpace(name))
	stem = strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, stem)
	stem = strings.Trim(stem, "_")
	if stem == "" {
		return "CUSTOM_" + fnv1a32Hex(name) + "_API_KEY"
	}
	if stem[0] >= '0' && stem[0] <= '9' {
		stem = "CUSTOM_" + stem
	}
	return stem + "_API_KEY"
}

type providerKeyEnvRepair struct {
	provider string
	old      string
	new      string
}

func repairInvalidProviderKeyEnvs(providers []config.ProviderEntry) ([]config.ProviderEntry, []providerKeyEnvRepair) {
	providers = append([]config.ProviderEntry(nil), providers...)
	var repairs []providerKeyEnvRepair
	for i := range providers {
		old := strings.TrimSpace(providers[i].APIKeyEnv)
		if old == "" || config.IsValidCredentialKey(old) {
			continue
		}
		keyEnv := apiKeyEnvFromProviderName(providers[i].Name)
		providers[i].APIKeyEnv = keyEnv
		repairs = append(repairs, providerKeyEnvRepair{provider: providers[i].Name, old: old, new: keyEnv})
	}
	return providers, repairs
}

func promptAPIKeyEnvName(in *bufio.Scanner, w io.Writer, label, def string) string {
	for {
		keyEnv := ask(in, w, label, def)
		if config.IsValidCredentialKey(keyEnv) {
			return keyEnv
		}
		fmt.Fprintf(w, i18n.M.InvalidAPIKeyEnvFmt+"\n", keyEnv)
	}
}

func fnv1a32Hex(s string) string {
	hash := uint32(0x811c9dc5)
	for _, unit := range utf16.Encode([]rune(strings.TrimSpace(s))) {
		hash ^= uint32(unit)
		hash *= 0x01000193
	}
	return fmt.Sprintf("%08x", hash)
}

type providerFamily struct {
	key  string
	name string
	desc string
}

func familyOf(name string) providerFamily {
	switch {
	case strings.HasPrefix(name, "deepseek"):
		return providerFamily{key: "deepseek", name: "DeepSeek", desc: "fast & cheap, plus a stronger Pro SKU"}
	default:
		return providerFamily{key: name, name: name}
	}
}

type providerPromptResult struct {
	entries     []config.ProviderEntry
	credentials map[string]string
}

func newProviderPromptResult(entries []config.ProviderEntry, key, value string) providerPromptResult {
	result := providerPromptResult{entries: entries}
	if key != "" && value != "" {
		result.credentials = map[string]string{key: value}
	}
	return result
}

func promptCustomProvider() (providerPromptResult, error) {
	methodIdx, err := selectOne(i18n.M.CustomAddMethodLabel, []menuItem{
		{name: i18n.M.CustomMethodManual},
		{name: i18n.M.CustomMethodURL},
	})
	if err != nil {
		return providerPromptResult{}, err
	}
	if methodIdx == 0 {
		return promptCustomProviderManual()
	}
	return promptCustomProviderFromURL()
}

func promptCustomProviderManual() (providerPromptResult, error) {
	return promptCustomProviderManualWith(bufio.NewScanner(os.Stdin), "", "", "")
}

func promptCustomProviderManualWith(in *bufio.Scanner, baseURL, keyEnv, apiKey string) (providerPromptResult, error) {
	fmt.Println()
	if baseURL == "" {
		baseURL = ask(in, os.Stdout, i18n.M.CustomPromptBaseURL, "")
		if baseURL == "" {
			return providerPromptResult{}, fmt.Errorf("base URL is required")
		}
	}
	providerName := providerSlug("custom", baseURL)
	modelName := ask(in, os.Stdout, i18n.M.CustomPromptModel, "")
	if modelName == "" {
		return providerPromptResult{}, fmt.Errorf("model name is required")
	}
	if keyEnv == "" {
		keyEnv = promptAPIKeyEnvName(in, os.Stdout, i18n.M.CustomPromptKeyEnv, apiKeyEnvFromProviderName(providerName))
	} else if !config.IsValidCredentialKey(keyEnv) {
		return providerPromptResult{}, fmt.Errorf("invalid API key variable name %q", keyEnv)
	}
	if apiKey == "" {
		apiKey = ask(in, os.Stdout, i18n.M.CustomPromptAPIKey, "")
	}
	entry := config.ProviderEntry{
		Name: providerName, Kind: "openai", BaseURL: baseURL,
		Model: modelName, APIKeyEnv: keyEnv, ContextWindow: 128000,
	}
	fmt.Printf("  %s\n", green(fmt.Sprintf(i18n.M.CustomAddedFmt, entry.Name+"/"+modelName)))
	return newProviderPromptResult([]config.ProviderEntry{entry}, keyEnv, apiKey), nil
}

func promptCustomProviderFromURL() (providerPromptResult, error) {
	in := bufio.NewScanner(os.Stdin)
	fmt.Println()

	baseURL := ask(in, os.Stdout, i18n.M.CustomPromptBaseURL, "")
	if baseURL == "" {
		return providerPromptResult{}, fmt.Errorf("base URL is required")
	}
	providerName := providerSlug("custom", baseURL)
	keyEnv := promptAPIKeyEnvName(in, os.Stdout, i18n.M.CustomPromptKeyEnv, apiKeyEnvFromProviderName(providerName))
	apiKey := ask(in, os.Stdout, i18n.M.CustomPromptAPIKey, "")

	fmt.Printf("  %s\n", dim(fmt.Sprintf(i18n.M.FetchingModelsFmt, "custom")))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := fetchModelListCompat(ctx, baseURL, apiKey)
	if err != nil || len(models) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s\n", dim(fmt.Sprintf(i18n.M.FetchModelsFailedFmt, "custom", err)))
		} else {
			fmt.Fprintf(os.Stderr, "  %s\n", dim(i18n.M.CustomFetchEmpty))
		}
		return promptCustomProviderManualWith(in, baseURL, keyEnv, apiKey)
	}
	fmt.Printf("  %s\n", green(fmt.Sprintf(i18n.M.FetchModelsSuccessFmt, len(models), "custom")))

	items := make([]menuItem, len(models))
	for i, m := range models {
		items[i] = menuItem{name: m}
	}
	idxs, err := selectMany(fmt.Sprintf(i18n.M.SelectModelsLabel, "custom"), items)
	if err != nil || len(idxs) == 0 {
		return providerPromptResult{}, fmt.Errorf("no models selected")
	}
	var selected []string
	for _, i := range idxs {
		selected = append(selected, models[i])
	}
	entry := config.ProviderEntry{
		Name: providerName, Kind: "openai", BaseURL: baseURL,
		Models: selected, Model: selected[0], APIKeyEnv: keyEnv, ContextWindow: 128000,
	}
	fmt.Printf("  %s\n", green(fmt.Sprintf(i18n.M.CustomAddedFmt, entry.Name+"/"+selected[0])))
	return newProviderPromptResult([]config.ProviderEntry{entry}, keyEnv, apiKey), nil
}

func promptAnthropicProvider() (providerPromptResult, error) {
	methodIdx, err := selectOne(i18n.M.AnthropicAddMethodLabel, []menuItem{
		{name: i18n.M.AnthropicMethodManual},
		{name: i18n.M.AnthropicMethodURL},
	})
	if err != nil {
		return providerPromptResult{}, err
	}
	if methodIdx == 0 {
		return promptAnthropicProviderManual()
	}
	return promptAnthropicProviderFromURL()
}

func promptAnthropicProviderManual() (providerPromptResult, error) {
	return promptAnthropicProviderManualWith(bufio.NewScanner(os.Stdin), "", "", "")
}

func promptAnthropicProviderManualWith(in *bufio.Scanner, baseURL, keyEnv, apiKey string) (providerPromptResult, error) {
	fmt.Println()
	if baseURL == "" {
		baseURL = ask(in, os.Stdout, i18n.M.AnthropicPromptBaseURL, "")
		if baseURL == "" {
			return providerPromptResult{}, fmt.Errorf("base URL is required")
		}
	}
	modelName := ask(in, os.Stdout, i18n.M.AnthropicPromptModel, "")
	if modelName == "" {
		return providerPromptResult{}, fmt.Errorf("model name is required")
	}
	if keyEnv == "" {
		keyEnv = promptAPIKeyEnvName(in, os.Stdout, i18n.M.AnthropicPromptKeyEnv, "ANTHROPIC_API_KEY")
	} else if !config.IsValidCredentialKey(keyEnv) {
		return providerPromptResult{}, fmt.Errorf("invalid API key variable name %q", keyEnv)
	}
	if apiKey == "" {
		apiKey = ask(in, os.Stdout, i18n.M.AnthropicPromptAPIKey, "")
	}
	entry := config.ProviderEntry{
		Name: providerSlug("anthropic", baseURL), Kind: "anthropic", BaseURL: baseURL,
		Model: modelName, APIKeyEnv: keyEnv, ContextWindow: 128000,
	}
	fmt.Printf("  %s\n", green(fmt.Sprintf(i18n.M.AnthropicAddedFmt, entry.Name+"/"+modelName)))
	return newProviderPromptResult([]config.ProviderEntry{entry}, keyEnv, apiKey), nil
}

func promptAnthropicProviderFromURL() (providerPromptResult, error) {
	in := bufio.NewScanner(os.Stdin)
	fmt.Println()

	baseURL := ask(in, os.Stdout, i18n.M.AnthropicPromptBaseURL, "")
	if baseURL == "" {
		return providerPromptResult{}, fmt.Errorf("base URL is required")
	}
	keyEnv := promptAPIKeyEnvName(in, os.Stdout, i18n.M.AnthropicPromptKeyEnv, "ANTHROPIC_API_KEY")
	apiKey := ask(in, os.Stdout, i18n.M.AnthropicPromptAPIKey, "")

	fmt.Printf("  %s\n", dim(fmt.Sprintf(i18n.M.AnthropicFetchingModelsFmt, "anthropic")))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := fetchModelListCompat(ctx, baseURL, apiKey)
	if err != nil || len(models) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s\n", dim(fmt.Sprintf(i18n.M.AnthropicFetchModelsFailedFmt, "anthropic", err)))
		} else {
			fmt.Fprintf(os.Stderr, "  %s\n", dim(i18n.M.AnthropicFetchEmpty))
		}
		return promptAnthropicProviderManualWith(in, baseURL, keyEnv, apiKey)
	}
	fmt.Printf("  %s\n", green(fmt.Sprintf(i18n.M.AnthropicFetchModelsSuccessFmt, len(models), "anthropic")))

	items := make([]menuItem, len(models))
	for i, m := range models {
		items[i] = menuItem{name: m}
	}
	idxs, err := selectMany(fmt.Sprintf(i18n.M.AnthropicSelectModelsLabel, "anthropic"), items)
	if err != nil || len(idxs) == 0 {
		return providerPromptResult{}, fmt.Errorf("no models selected")
	}
	var selected []string
	for _, i := range idxs {
		selected = append(selected, models[i])
	}
	entry := config.ProviderEntry{
		Name: providerSlug("anthropic", baseURL), Kind: "anthropic", BaseURL: baseURL,
		Models: selected, Model: selected[0], APIKeyEnv: keyEnv, ContextWindow: 128000,
	}
	fmt.Printf("  %s\n", green(fmt.Sprintf(i18n.M.AnthropicAddedFmt, entry.Name+"/"+selected[0])))
	return newProviderPromptResult([]config.ProviderEntry{entry}, keyEnv, apiKey), nil
}

func groupByFamily(providers []config.ProviderEntry) ([]string, map[string][]int, map[string]providerFamily) {
	var order []string
	members := map[string][]int{}
	info := map[string]providerFamily{}
	for i, p := range providers {
		f := familyOf(p.Name)
		if _, seen := members[f.key]; !seen {
			order = append(order, f.key)
			info[f.key] = f
		}
		members[f.key] = append(members[f.key], i)
	}
	return order, members, info
}

func withBuiltinFamilies(providers []config.ProviderEntry) []config.ProviderEntry {
	return withBuiltinFamiliesForLanguage(providers, "")
}

func withBuiltinFamiliesForLanguage(providers []config.ProviderEntry, pricingLanguage string) []config.ProviderEntry {
	haveName := map[string]bool{}
	for _, p := range providers {
		haveName[p.Name] = true
	}
	defaults := config.Default()
	defaults.Language = pricingLanguage
	defaults.ApplyDeepSeekOfficialDefaultPricing()
	for _, bp := range defaults.Providers {
		if !haveName[bp.Name] {
			providers = append(providers, bp)
		}
	}
	return providers
}

func providersWithMissingKeys(cfg *config.Config) []config.ProviderEntry {
	if cfg == nil {
		return nil
	}
	refs := []string{
		cfg.DefaultModel,
		cfg.Agent.PlannerModel,
		cfg.Agent.SubagentModel,
	}
	if len(cfg.Agent.SubagentModels) > 0 {
		keys := make([]string, 0, len(cfg.Agent.SubagentModels))
		for key := range cfg.Agent.SubagentModels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			refs = append(refs, cfg.Agent.SubagentModels[key])
		}
	}

	var out []config.ProviderEntry
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		p, ok := cfg.ResolveModel(ref)
		if !ok || p.APIKeyEnv == "" || os.Getenv(p.APIKeyEnv) != "" || seen[p.APIKeyEnv] {
			continue
		}
		seen[p.APIKeyEnv] = true
		out = append(out, *p)
	}
	return out
}

func configureKeys(selected []config.ProviderEntry, r io.Reader, w io.Writer) []string {
	in := bufio.NewScanner(r)
	fmt.Fprintln(w, "\n"+i18n.M.EnterAPIKeysHeader)

	seen := map[string]bool{}
	var envLines []string
	for _, p := range selected {
		if p.APIKeyEnv == "" || seen[p.APIKeyEnv] {
			continue
		}
		seen[p.APIKeyEnv] = true

		if cur := os.Getenv(p.APIKeyEnv); cur != "" {
			reset := ask(in, w, "  "+fmt.Sprintf(i18n.M.APIKeyResetPromptFmt, p.APIKeyEnv), "y/N")
			if reset == "y" || reset == "Y" {
				if key := ask(in, w, "  "+p.APIKeyEnv, ""); key != "" {
					envLines = append(envLines, p.APIKeyEnv+"="+key)
					continue
				}
			}
			fmt.Fprintf(w, "  %s %s\n", green("✓"), fmt.Sprintf(i18n.M.APIKeyAlreadySetFmt, p.APIKeyEnv))
			envLines = append(envLines, p.APIKeyEnv+"="+cur)
			continue
		}

		if key := ask(in, w, "  "+p.APIKeyEnv, ""); key != "" {
			envLines = append(envLines, p.APIKeyEnv+"="+key)
		}
	}
	return envLines
}

func ask(in *bufio.Scanner, w io.Writer, label, def string) string {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	if !in.Scan() {
		return def
	}
	if v := strings.TrimSpace(in.Text()); v != "" {
		return v
	}
	return def
}

func isInteractive() bool {
	return isTTY(os.Stdin) && isTTY(os.Stdout)
}

func isTTY(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

func appendEnv(path string, lines []string) error {
	target := map[string]bool{}
	for _, l := range lines {
		if k, _, ok := strings.Cut(l, "="); ok {
			target[strings.TrimSpace(k)] = true
		}
	}

	var kept []string
	if data, err := fileencoding.ReadFileUTF8(path); err == nil {
		for _, raw := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(raw)
			check := strings.TrimPrefix(trimmed, "export ")
			if k, _, ok := strings.Cut(check, "="); ok && target[strings.TrimSpace(k)] {
				continue
			}
			kept = append(kept, raw)
		}
		// strings.Split on a string ending with \n leaves a trailing empty
		// element; trim it so we don't grow a blank line on every rewrite.
		if n := len(kept); n > 0 && kept[n-1] == "" {
			kept = kept[:n-1]
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	var b strings.Builder
	for _, l := range kept {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
		if k, v, ok := strings.Cut(l, "="); ok {
			os.Setenv(strings.TrimSpace(k), v)
		}
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func readStdin() string {
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice != 0 {
		return ""
	}
	data, _ := io.ReadAll(os.Stdin)
	return strings.TrimSpace(string(data))
}
