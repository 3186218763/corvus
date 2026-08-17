// Package headless implements Corvus's one-shot, non-interactive frontend.
//
// The frontend is shared by corvus-exec and the main corvus command's
// --headless mode so both entry points assemble and drive the same controller.
package headless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"corvus/internal/agent"
	"corvus/internal/boot"
	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/i18n"

	"github.com/spf13/pflag"
	"golang.org/x/term"
)

const (
	exitOK      = 0
	exitRunErr  = 1
	exitUsage   = 2
	exitTurnErr = 2
)

const (
	formatText = "text"
	formatJSON = "json"
)

// Run executes one prompt using the corvus-exec command name in diagnostics
// and help output.
func Run(args []string, version string) int {
	return RunAs(args, version, "corvus-exec")
}

// RunAs executes one prompt using commandName in diagnostics and help output.
// It is used by the main corvus binary for --headless, where the same
// interface should identify itself as corvus.
func RunAs(args []string, version, commandName string) int {
	commandName = strings.TrimSpace(commandName)
	if commandName == "" {
		commandName = "corvus-exec"
	}

	i18n.DetectLanguage("")
	if cfg, err := config.Load(); err == nil && cfg.Language != "" {
		i18n.DetectLanguage(cfg.Language)
	}

	fs := pflag.NewFlagSet(commandName, pflag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printUsage(os.Stderr, fs, commandName) }

	format := fs.String("format", formatText, "output format: text or json")
	jsonOut := fs.Bool("json", false, "alias for --format json")
	model := fs.String("model", "", "model reference (default: configured default_model)")
	profile := fs.String("profile", "balanced", "runtime profile: economy, balanced, or delivery")
	maxSteps := fs.Int("max-steps", 0, "maximum tool-call steps (0 = automatic)")
	permissionMode := fs.String("permission-mode", "auto", "headless approval mode: auto, dontAsk, yolo, or ask")
	dir := fs.String("dir", "", "change to this directory and use it as the workspace root")
	resume := fs.String("resume", "", "resume a saved session by ID, path, or query")
	showHelp := fs.BoolP("help", "h", false, "show help and exit")
	showVersion := fs.BoolP("version", "v", false, "show version and exit")

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *showHelp {
		printUsage(os.Stdout, fs, commandName)
		return exitOK
	}
	if *showVersion {
		fmt.Println(commandName, version)
		return exitOK
	}

	outFormat := strings.ToLower(strings.TrimSpace(*format))
	if *jsonOut {
		outFormat = formatJSON
	}
	if outFormat != formatText && outFormat != formatJSON {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, fmt.Sprintf("unknown format %q (want text or json)", *format))
		return exitUsage
	}
	approvalMode, err := parsePermissionMode(*permissionMode)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return exitUsage
	}
	tokenMode, err := parseProfile(*profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return exitUsage
	}
	if *maxSteps < 0 {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "--max-steps must be >= 0")
		return exitUsage
	}
	if *dir != "" {
		if err := os.Chdir(*dir); err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return exitUsage
		}
	}
	var workspaceRoot string
	if *dir != "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "resolve workspace root:", err)
			return exitRunErr
		}
		workspaceRoot = wd
	}
	sessionDir := resolveSessionDir()

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "no prompt: pass one or more positional arguments or pipe input on stdin")
			return exitUsage
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "read prompt from stdin:", err)
			return exitRunErr
		}
		prompt = strings.TrimSpace(string(data))
		if prompt == "" {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "no prompt: stdin was empty; pass one or more positional arguments or pipe input on stdin")
			return exitUsage
		}
	}

	// Resolve the --resume target before building so the resumed session's own
	// model can be picked up when --model was not given (mirrors the CLI).
	resumePath := ""
	if strings.TrimSpace(*resume) != "" {
		path, err := resolveSessionQuery(sessionDir, *resume)
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return exitRunErr
		}
		resumePath = path
	}

	eventCh := make(chan event.Event, 1024)
	sink := channelSink{ch: eventCh}
	pump := newEventPump(outFormat)
	done := make(chan event.Event, 1)
	go func() {
		for e := range eventCh {
			pump.display(e)
			if e.Kind == event.TurnDone {
				done <- e
				return
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leases := control.NewSessionLeaseKeeper()
	defer leases.Release()

	opts := boot.Options{
		Model:                modelForResume(*model, resumePath),
		MaxSteps:             *maxSteps,
		MaxStepsKey:          "--max-steps",
		RequireKey:           true,
		Sink:                 sink,
		TokenMode:            tokenMode,
		SessionDir:           sessionDir,
		WorkspaceRoot:        workspaceRoot,
		HeadlessApprovalMode: approvalMode,
		AutoPricingCurrency:  autoPricingCurrency(),
		StatsSource:          "exec",
		Stderr:               os.Stderr,
		OnSessionRecovered:   leases.HandleSessionRecovered,
	}
	ctrl, err := boot.Build(ctx, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return exitRunErr
	}
	defer ctrl.Close()

	// Re-apply the headless posture on the live controller so the parent
	// executor and every sub-agent gate share the selected mode.
	ctrl.ApplyHeadlessApprovalMode(approvalMode)
	ctrl.SetDisplayRecorder(pump.recordDisplay)

	if resumePath != "" {
		loaded, err := agent.LoadSession(resumePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return exitRunErr
		}
		ctrl.Resume(loaded, resumePath)
	}
	ctrl.EnsureSessionPath()
	if err := leases.Rebind(ctrl.SessionPath()); err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, control.SessionInUseMessage(err)+"; "+control.SessionLeaseCloseHint)
		return exitRunErr
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			ctrl.Cancel()
		case <-ctx.Done():
		}
	}()

	runErr := ctrl.RunTurn(ctx, prompt)

	// RunTurn is the synchronous entry point: it emits TurnStarted (and the
	// rest of the stream) but not TurnDone — that is reserved for the
	// asynchronous Submit paths. Synthesize the terminal event so JSONL
	// consumers see a complete TurnStarted..TurnDone record and text mode has
	// the final answer to print.
	cancelled := errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)
	completion := event.Event{Kind: event.TurnDone, Err: runErr, Cancelled: cancelled}
	if runErr != nil {
		// Err marshals as {} in JSON; carry the human-readable message in Text.
		completion.Text = runErr.Error()
	}
	eventCh <- completion
	td := <-done

	switch {
	case td.Err != nil && !td.Cancelled && !errors.Is(td.Err, context.Canceled) && !errors.Is(td.Err, context.DeadlineExceeded):
		return exitTurnErr
	case td.Err != nil || td.Cancelled:
		return exitRunErr
	default:
		return exitOK
	}
}

type channelSink struct{ ch chan event.Event }

func (s channelSink) Emit(e event.Event) { s.ch <- e }

func printUsage(w io.Writer, fs *pflag.FlagSet, commandName string) {
	fmt.Fprintf(w, "%s runs a single Corvus prompt headlessly and exits.\n\n", commandName)
	fmt.Fprintf(w, "Usage:\n  %s [flags] [prompt...]\n\n", commandName)
	fmt.Fprintln(w, "With no positional arguments the prompt is read from stdin (pipes supported).")
	fmt.Fprintln(w, "\nFlags:")
	prev := fs.Output()
	fs.SetOutput(w)
	fs.PrintDefaults()
	fs.SetOutput(prev)
}

func parsePermissionMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto", "approve", "allow":
		return control.ToolApprovalAuto, nil
	case "dontask", "dont-ask":
		return control.ToolApprovalDontAsk, nil
	case "yolo", "bypasspermissions", "bypass-permissions", "full", "full-access", "bypass":
		return control.ToolApprovalYolo, nil
	case "ask", "manual", "default":
		return control.ToolApprovalAsk, nil
	default:
		return "", fmt.Errorf("unknown permission mode %q (want auto, dontAsk, yolo, or ask)", value)
	}
}

func parseProfile(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "balanced", "full":
		return boot.TokenModeFull, nil
	case "economy":
		return boot.TokenModeEconomy, nil
	case "delivery":
		return boot.TokenModeDelivery, nil
	default:
		return "", fmt.Errorf("unknown runtime profile %q (want economy, balanced, or delivery)", value)
	}
}

func resolveSessionDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return config.SessionDir()
	}
	if projDir := config.ProjectSessionDir(cwd); projDir != "" && projDir != config.SessionDir() {
		return projDir
	}
	return config.SessionDir()
}

func resolveSessionQuery(dir, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", nil
	}
	if info, err := os.Stat(query); err == nil && !info.IsDir() {
		abs, absErr := filepath.Abs(query)
		if absErr != nil {
			return "", absErr
		}
		return abs, nil
	}
	sessions, err := agent.ListSessions(dir)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	lower := strings.ToLower(query)
	var exact []string
	var partial []string
	for _, session := range sessions {
		id := agent.BranchID(session.Path)
		base := filepath.Base(session.Path)
		if query == id || query == base || query == session.Path {
			exact = append(exact, session.Path)
			continue
		}
		haystack := strings.ToLower(strings.Join([]string{id, base, session.CustomTitle, session.TopicTitle, session.Preview}, "\n"))
		if strings.Contains(haystack, lower) {
			partial = append(partial, session.Path)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matches %q", query)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("session query %q is ambiguous (%d matches)", query, len(matches))
	}
}

func modelForResume(modelName, resumePath string) string {
	if strings.TrimSpace(modelName) != "" || strings.TrimSpace(resumePath) == "" {
		return modelName
	}
	sessionModel, ok := agent.LoadSessionModel(resumePath)
	if !ok {
		return modelName
	}
	cfg, err := config.Load()
	if err != nil {
		return sessionModel
	}
	if _, ok := cfg.ResolveModel(sessionModel); !ok {
		return modelName
	}
	return sessionModel
}

func autoPricingCurrency() string {
	switch i18n.CurrentLanguage() {
	case "zh", "zh-TW":
		return "CNY"
	default:
		return "USD"
	}
}

type eventPump struct {
	format string

	mu           sync.Mutex
	sawText      bool
	sawReasoning bool
	finalMessage string
	lastDisplay  string
}

func newEventPump(format string) *eventPump {
	return &eventPump{format: format}
}

func (p *eventPump) recordDisplay(content, display string) {
	if p.format != formatText || strings.TrimSpace(display) == "" || content == display {
		return
	}
	p.mu.Lock()
	p.lastDisplay = display
	p.mu.Unlock()
}

func (p *eventPump) display(e event.Event) {
	if p.format == formatJSON {
		writeJSONEvent(e)
		return
	}
	switch e.Kind {
	case event.Reasoning:
		p.mu.Lock()
		p.sawReasoning = true
		p.mu.Unlock()
		fmt.Fprint(os.Stderr, "💭 ", e.Text)
	case event.Text:
		p.mu.Lock()
		p.sawText = true
		p.mu.Unlock()
		_, _ = os.Stdout.WriteString(e.Text)
	case event.Message:
		p.mu.Lock()
		p.finalMessage = e.Text
		p.mu.Unlock()
	case event.ToolDispatch:
		fmt.Fprintf(os.Stderr, "→ %s %s\n", e.Tool.Name, compactArgs(e.Tool.Args))
	case event.ToolResult:
		fmt.Fprintln(os.Stderr, summarizeToolResult(e))
	case event.Notice:
		fmt.Fprintln(os.Stderr, "!", e.Text)
	case event.ApprovalRequest:
		fmt.Fprintf(os.Stderr, "? approval: %s %s\n", e.Approval.Tool, e.Approval.Subject)
	case event.AskRequest:
		prompt := ""
		if len(e.Ask.Questions) > 0 {
			prompt = e.Ask.Questions[0].Prompt
		}
		fmt.Fprintf(os.Stderr, "? ask: %s\n", firstLine(prompt))
	case event.TurnDone:
		p.finalize(e)
	}
}

func (p *eventPump) finalize(e event.Event) {
	p.mu.Lock()
	sawText, sawReasoning := p.sawText, p.sawReasoning
	finalMessage, lastDisplay := p.finalMessage, p.lastDisplay
	p.mu.Unlock()

	if sawReasoning {
		fmt.Fprintln(os.Stderr)
	}
	if e.Err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, e.Err)
	}
	switch {
	case finalMessage != "":
		if sawText {
			fmt.Fprintln(os.Stdout)
		}
		fmt.Fprintln(os.Stdout, finalMessage)
	case !sawText && lastDisplay != "":
		fmt.Fprintln(os.Stdout, lastDisplay)
	}
}

func writeJSONEvent(e event.Event) {
	data, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "encode event:", err)
		return
	}
	_, _ = os.Stdout.Write(data)
	_, _ = os.Stdout.Write([]byte{'\n'})
}

func compactArgs(args string) string {
	s := strings.Join(strings.Fields(args), " ")
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

func summarizeToolResult(e event.Event) string {
	name := e.Tool.Name
	dur := ""
	if e.Tool.DurationMs > 0 {
		dur = fmt.Sprintf(" (%.1fs)", float64(e.Tool.DurationMs)/1000)
	}
	if e.Tool.Err != "" {
		return "← " + name + " err: " + firstLine(e.Tool.Err) + dur
	}
	if e.Tool.Truncated {
		return "← " + name + " ok (truncated)" + dur
	}
	return "← " + name + " ok" + dur
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
