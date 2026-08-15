package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"corvus/internal/agent"
	"corvus/internal/control"
	"corvus/internal/i18n"
	"corvus/internal/memory"
	"corvus/internal/outputstyle"
	"corvus/internal/plugin"
	"corvus/internal/provider"
	"corvus/internal/sandbox"
	"corvus/internal/skill"
)

// prompts returns the MCP prompts discovered at startup (nil when no plugins).
func (m *chatTUI) prompts() []plugin.Prompt {
	if m.host == nil {
		return nil
	}
	return m.host.Prompts()
}

// runSlashCommand handles "/<cmd> <args>" input. Local commands queue their
// output to scrollback; MCP prompt / custom commands resolve to a model turn.
func (m *chatTUI) runSlashCommand(input string) tea.Cmd {
	typedCmd := strings.TrimSpace(strings.SplitN(input, " ", 2)[0])

	if strings.HasPrefix(typedCmd, "/mcp__") {
		return m.runMCPPrompt(input)
	}
	cmd := canonicalBuiltinSlashCommand(typedCmd)

	switch cmd {
	case "/compact":
		m.echoLocalCommand(input)
		// Compaction makes a (network) summarizer call; run it off the Update loop
		// so the TUI doesn't freeze. The CompactionStarted/Done events render the
		// card as they arrive; compactDoneMsg only handles the terminal error /
		// snapshot once the pass returns. Any text after "/compact" is focus
		// guidance steering what the summary keeps.
		focus := strings.TrimSpace(strings.TrimPrefix(input, typedCmd))
		return func() tea.Msg { return compactDoneMsg{err: m.ctrl.Compact(context.Background(), focus)} }
	case "/new":
		m.echoLocalCommand(input)
		// NewSession snapshots the old transcript and swaps the live session;
		// run it off the Update loop like /compact so the TUI never freezes on
		// the disk write.
		return func() tea.Msg { return newSessionDoneMsg{err: m.ctrl.NewSession()} }
	case "/clear":
		m.echoLocalCommand(input)
		m.clearConfirm = &clearConfirm{confirm: 1}
	case "/cls":
		m.echoLocalCommand(input)
		m.finalizeStreamed()
		m.clearTranscriptDisplay()
		m.commitLine(strings.TrimRight(
			renderTUIBanner(m.label, "", transcriptContentWidth(m.width, m.nativeScrollback)), "\n"))
		m.transcriptDirty = true
		m.forceGotoBottom = true
		m.notice(i18n.M.SlashClsDone)
	case "/resume":
		m.runResumeCommand(input)
	case "/status":
		m.echoLocalCommand(input)
		m.showStatusDetails()
	case "/rename":
		m.runRenameCommand(input)
	case "/todo":
		m.echoLocalCommand(input)
		// Dismiss the pinned task list; a later todo_write brings it back.
		m.todoArgs = ""
		m.notice(i18n.M.SlashTodoCleared)
	case "/verbose":
		m.toggleVerboseReasoning(true)
	case "/mouse":
		m.toggleMouseCapture()
	case "/sandbox":
		m.echoLocalCommand(input)
		m.showSandboxStatus()
	case "/effort":
		return m.runEffortCommand(input)
	case "/work-mode", "/profile":
		m.echoLocalCommand(input)
		return m.runWorkModeCommand(input)
	case "/reasoning-language":
		m.echoLocalCommand(input)
		m.runReasoningLanguageCommand(input)
	case "/rewind":
		m.echoLocalCommand(input)
		m.openRewind()
	case "/tree":
		m.echoLocalCommand(input)
		m.showBranchTree()
	case "/branch":
		m.echoLocalCommand(input)
		m.runBranchCommand(input)
	case "/switch":
		m.echoLocalCommand(input)
		m.runSwitchCommand(input)
	case "/mcp":
		m.echoLocalCommand(input)
		m.runMCPSubcommand(input)
	case "/plugin", "/plugins":
		m.echoLocalCommand(input)
		m.runPluginSubcommand(input)
	case "/model":
		m.echoLocalCommand(input)
		m.runModelSubcommand(input)
		if m.pendingModelSwitch != nil {
			return m.pendingModelSwitch
		}
	case "/hooks":
		m.echoLocalCommand(input)
		m.runHooksSubcommand(input)
	case "/provider":
		m.echoLocalCommand(input)
		m.runProviderCommand(input)
		if m.pendingModelSwitch != nil {
			return m.pendingModelSwitch
		}
	case "/skill", "/skills":
		m.echoLocalCommand(input)
		m.runSkillSubcommand(input)
		if m.pendingModelSwitch != nil {
			return m.pendingModelSwitch
		}
	case "/reload-cmd":
		m.echoLocalCommand(input)
		if m.ctrl == nil {
			m.notice("controller not ready")
			return nil
		}
		if m.ctrl.Running() {
			m.notice("wait for the current turn to finish, then retry /reload-cmd")
			return nil
		}
		prev := len(m.commands)
		err := m.ctrl.ReloadCommands(context.Background())
		m.commands = m.ctrl.Commands()
		m.updateCompletion()
		if err != nil {
			m.notice("reload-cmd: " + err.Error())
			return nil
		}
		m.notice(fmt.Sprintf("commands reloaded: %d → %d commands", prev, len(m.commands)))

	case "/paste-image":
		return m.beginClipboardImagePaste()
	case "/output-style", "/output-styles":
		m.echoLocalCommand(input)
		styles := outputstyle.List(outputstyle.Dirs())
		if len(styles) == 0 {
			m.notice(i18n.M.OutputStyleNone)
		} else {
			m.commitLine(renderOutputStyles(m.width, styles, m.outputStyle))
		}
	case "/diff-fold":
		m.echoLocalCommand(input)
		if m.diffMaxLines == 0 {
			m.diffMaxLines = diffFoldLimit
			m.notice(fmt.Sprintf(i18n.M.DiffFoldEnabledFmt, diffFoldLimit))
		} else {
			m.diffMaxLines = 0
			m.notice(i18n.M.DiffFoldDisabled)
		}
	case "/theme":
		m.echoLocalCommand(input)
		return m.runThemeSubcommand(input)
	case "/language":
		m.echoLocalCommand(input)
		return m.runLanguageSubcommand(input)
	case "/currency":
		m.echoLocalCommand(input)
		return m.runCurrencySubcommand(input)
	case "/help":
		m.echoLocalCommand(input)
		m.showHelp()
	case "/memory":
		m.echoLocalCommand(input)
		m.showMemory(input)
	case "/goal":
		return m.runGoalSubcommand(input)
	case "/remember":
		note := strings.TrimSpace(strings.TrimPrefix(input, typedCmd))
		if note == "" {
			m.notice("nothing to remember")
		} else if path, err := m.ctrl.QuickAdd(memory.ScopeProject, note); err != nil {
			m.notice("memory: " + err.Error())
		} else {
			m.notice("remembered → " + path)
		}
	case "/quit", "/exit":
		return tea.Quit
	case "/copy":
		return m.runCopyCommand(input)
	case "/export":
		m.runExportCommand(input)
	case "/forget":
		m.forgetMemory(strings.TrimSpace(strings.TrimPrefix(input, typedCmd)))
	default:
		// A custom command wins over a skill of the same name; both resolve to a turn.
		if sent, ok := m.ctrl.CustomCommand(input); ok {
			return m.startTurn(sent, input, input)
		}
		if _, ok := m.ctrl.RunSkill(input); ok {
			fields := strings.Fields(input)
			name := strings.TrimPrefix(fields[0], "/")
			for _, sk := range m.ctrl.Skills() {
				if sk.Name == name && sk.RunAs == skill.RunSubagent && len(fields) == 1 {
					m.echoLocalCommand(input)
					m.notice("usage: /" + name + " <task>")
					return nil
				}
			}
			return m.startControllerTurn(input, input, func() { m.ctrl.SubmitDisplay(input, input) })
		}
		m.notice(fmt.Sprintf("%s: %s", i18n.M.SlashUnknown, cmd))
	}
	return nil
}

// showStatusDetails keeps diagnostics available without permanently crowding
// the two-line composer footer.
func (m *chatTUI) showStatusDetails() {
	var lines []string
	lines = append(lines, viewHeader("%s", "Session status"))
	mode := "Ask"
	if m.ctrl != nil {
		mode = m.modeTagText()
	}
	lines = append(lines, "  mode       "+mode)
	model := strings.TrimSpace(m.modelRef)
	if model == "" {
		model = strings.TrimSpace(m.label)
	}
	if model != "" {
		lines = append(lines, "  model      "+model)
	}
	if m.ctrl != nil {
		if tag := m.contextTag(); tag != "" {
			lines = append(lines, "  context    "+tag)
		}
	}
	if tag := m.workModeTag(); tag != "" {
		lines = append(lines, "  profile    "+tag)
	}
	if m.effortLevel != "" {
		// The persistent footer uses a Title Case semantic label. The expanded
		// diagnostic view keeps its sentence-like wording for readability.
		lines = append(lines, "  effort     effort "+m.effortLevel)
	}
	if m.ctrl != nil {
		if tag := m.cacheTag(); tag != "" {
			lines = append(lines, "  cache      "+tag)
		}
	}
	if tag := m.gitTag(); tag != "" {
		lines = append(lines, "  git        "+tag)
	}
	if m.ctrl != nil {
		if tag := m.jobsTag(); tag != "" {
			lines = append(lines, "  jobs       "+tag)
		}
	}
	if m.balance != "" {
		lines = append(lines, "  balance    "+m.balance)
	}
	if tag := m.mouseTag(); tag != "" {
		lines = append(lines, "  mouse      "+tag)
	}
	m.commitLine(strings.Join(lines, "\n"))
}

func (m *chatTUI) runGoalSubcommand(input string) tea.Cmd {
	cmd, ok := control.ParseGoalCommand(input)
	if !ok {
		m.echoLocalCommand(input)
		m.notice(i18n.M.GoalEmpty)
		return nil
	}
	switch cmd.Action {
	case control.GoalCommandSet:
		m.planMode = false
		m.ctrl.SetPlanMode(false)
		m.ctrl.SetGoalWithResearchMode(cmd.Text, cmd.ResearchMode)
		m.ctrl.GoalStrict(cmd.Strict)
		m.notice(fmt.Sprintf(i18n.M.GoalSetFmt, control.ShortGoalForNotice(cmd.Text)))
		return m.startTurn("Start pursuing the active goal now.", input, input)
	case control.GoalCommandClear:
		m.echoLocalCommand(input)
		m.ctrl.ClearGoal()
		m.notice(i18n.M.GoalCleared)
	default:
		m.echoLocalCommand(input)
		goal := m.ctrl.Goal()
		if strings.TrimSpace(goal) == "" {
			m.notice(i18n.M.GoalEmpty)
		} else {
			m.notice(fmt.Sprintf(i18n.M.GoalCurrentFmt, goal))
		}
	}
	return nil
}

// runCopyCommand copies the Nth-latest assistant message from the current turn
// (after the last user message) to the clipboard.
//
//   - "/copy"   — shows a numbered list of assistant messages to choose from.
//   - "/copy N" — copies the Nth message directly (1 = most recent).
//
// Counting does not cross user message boundaries.
func (m *chatTUI) runCopyCommand(input string) tea.Cmd {
	m.echoLocalCommand(input)
	// "/copy N" copies the Nth-newest assistant message directly (1 = most
	// recent), matching the picker's newest-first ordering. A bare "/copy"
	// (or a non-numeric argument) opens the interactive picker instead.
	arg := strings.TrimSpace(strings.TrimPrefix(input, "/copy"))
	if n, err := strconv.Atoi(arg); err == nil && n > 0 {
		msgs := m.ctrl.History()
		parts := copyAssistantParts(msgs)
		if len(parts) == 0 {
			m.notice(i18n.M.SlashCopyEmpty)
			return nil
		}
		// copyAssistantParts is oldest-first; index 0 of the reversed slice
		// is the most recent, so "/copy 1" = parts[len-1].
		idx := len(parts) - n
		if idx < 0 || idx >= len(parts) {
			m.notice(i18n.M.SlashCopyEmpty)
			return nil
		}
		return copyToClipboard(parts[idx])
	}
	m.openCopyPicker()
	return nil
}

// firstLine returns the first non-empty line of s, truncated to 80 runes.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			runes := []rune(t)
			if len(runes) > 80 {
				return string(runes[:77]) + "..."
			}
			return t
		}
	}
	return "..."
}

// copyAssistantParts returns the Content of assistant messages after the last
// user message in msgs, skipping empty strings and model placeholders ("…", "...").
// The result is chronological (oldest first).
func copyAssistantParts(msgs []provider.Message) []string {
	lastUserIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleUser {
			lastUserIdx = i
			break
		}
	}
	start := lastUserIdx + 1
	if lastUserIdx < 0 {
		start = 0
	}
	var parts []string
	for i := start; i < len(msgs); i++ {
		if msgs[i].Role != provider.RoleAssistant {
			continue
		}
		c := strings.TrimSpace(msgs[i].Content)
		if c == "" || c == "..." || c == "…" {
			continue
		}
		parts = append(parts, c)
	}
	return parts
}

// runExportCommand exports the entire session as a markdown file, excluding
// system messages, reasoning/thinking content, and tool calls/results.
func (m *chatTUI) runExportCommand(input string) {
	m.echoLocalCommand(input)
	msgs := m.ctrl.History()
	if len(msgs) == 0 {
		m.notice(i18n.M.SlashExportEmpty)
		return
	}

	var b strings.Builder
	b.WriteString("# corvus session\n\n")
	lastRole := provider.Role("")
	exportedMessages := 0
	for _, msg := range msgs {
		switch msg.Role {
		case provider.RoleUser:
			// Skip internal steer messages.
			if _, isSteer := agent.SteerText(msg.Content); isSteer {
				continue
			}
			content := exportUserContent(msg.Content)
			if content == "" {
				continue
			}
			if lastRole != provider.RoleUser {
				b.WriteString("## User\n\n")
			}
			b.WriteString(content)
			b.WriteString("\n\n")
			exportedMessages++
			lastRole = provider.RoleUser
		case provider.RoleAssistant:
			content := strings.TrimSpace(msg.Content)
			if content == "" {
				continue
			}
			if lastRole != provider.RoleAssistant {
				b.WriteString("## Assistant\n\n")
			}
			b.WriteString(content)
			b.WriteString("\n\n")
			exportedMessages++
			lastRole = provider.RoleAssistant
		}
	}
	if exportedMessages == 0 {
		m.notice(i18n.M.SlashExportEmpty)
		return
	}

	// Choose a filename. If the workspace has a root, save there; otherwise
	// the current directory. Use a timestamp-based name.
	dir := "."
	if m.ctrl != nil {
		if wr := m.ctrl.WorkspaceRoot(); wr != "" {
			dir = wr
		}
	}
	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("session-%s.md", ts)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashUnknown, err))
		return
	}
	m.notice(fmt.Sprintf(i18n.M.SlashExportDoneFmt, path))
}

func exportUserContent(content string) string {
	content = control.StripComposePrefixes(content)
	content = control.StripReferencedContextPrefix(content)
	return strings.TrimSpace(content)
}

func (m *chatTUI) echoLocalCommand(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}
	m.commitLine(dim("  › " + input))
}

// commandNames renders the custom command list for /help, "" when there are none.
func (m *chatTUI) commandNames() string {
	names := make([]string, 0, len(m.commands))
	for _, c := range m.commands {
		if !c.Hidden {
			names = append(names, "/"+c.Name)
		}
	}
	return strings.Join(names, " · ")
}

// showSandboxStatus displays the current sandbox configuration and whether
// the OS sandbox backend is available. It reads from the stored config so
// the user can inspect sandbox state without leaving the TUI (closes #3316).
func (m *chatTUI) showSandboxStatus() {
	if m.cfg == nil {
		m.notice("sandbox: config not loaded")
		return
	}
	bash := m.cfg.BashMode()
	network := m.cfg.Sandbox.Network
	available := sandbox.Available()
	roots := m.cfg.WriteRoots()

	var b strings.Builder
	b.WriteString("sandbox\n")
	b.WriteString("  phase 0  file-writer confinement\n")
	if len(roots) > 0 {
		fmt.Fprintf(&b, "    write_roots  %s\n", strings.Join(roots, ", "))
	}
	if m.cfg.Sandbox.WorkspaceRoot != "" {
		fmt.Fprintf(&b, "    workspace_root  %s\n", m.cfg.Sandbox.WorkspaceRoot)
	}
	if len(m.cfg.Sandbox.AllowWrite) > 0 {
		fmt.Fprintf(&b, "    allow_write  %s\n", strings.Join(m.cfg.Sandbox.AllowWrite, ", "))
	}
	b.WriteString("  phase 1  OS bash sandbox\n")
	fmt.Fprintf(&b, "    bash        %s", bash)
	if bash == "enforce" && !available {
		b.WriteString(" (unavailable: no OS sandbox on this host; bash execution is refused. " + sandbox.UnavailableRemediation() + ")")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "    network     %v\n", network)
	m.notice(b.String())
}

// runMCPSubcommand handles "/mcp" (status), "/mcp add …" (connect a server live
// and persist it), and "/mcp remove <name>" (disconnect + drop from config). Add
// connects synchronously — like /compact, an explicit command may briefly block
// the UI while the handshake runs.
func (m *chatTUI) runMCPSubcommand(input string) {
	args := tokenizeArgs(input) // args[0] == "/mcp"
	if len(args) < 2 {
		m.openMCPManager("")
		return
	}
	switch args[1] {
	case "list", "ls":
		// The completion menu offers "list"; treat it as the status view (same as
		// the legacy /mcp output) rather than an unknown subcommand.
		m.showMCPStatus()
	case "show":
		if len(args) < 3 {
			m.notice("usage: /mcp show <name>")
			return
		}
		m.openMCPManager(args[2])
	case "tools":
		if len(args) < 3 {
			m.notice("usage: /mcp tools <name>")
			return
		}
		m.openMCPManager(args[2])
		if m.mcp != nil {
			m.mcp.stage = mcpStageTools
		}
	case "add":
		entry, err := parseMCPAdd(args[2:])
		if err != nil {
			m.notice(err.Error())
			return
		}
		n, err := m.ctrl.AddMCPServer(entry)
		if err != nil {
			m.notice("mcp add: " + err.Error())
			return
		}
		m.notice(fmt.Sprintf("connected %s — %d tools, saved to global config (available next message)", entry.Name, n))
	case "connect":
		if len(args) < 3 {
			m.notice("usage: /mcp connect <name>")
			return
		}
		n, err := m.ctrl.ConnectConfiguredMCPServer(args[2])
		if err != nil {
			m.notice("mcp connect: " + err.Error())
			return
		}
		m.host = m.ctrl.Host()
		m.notice(fmt.Sprintf("connected %s — %d tools (available next message)", args[2], n))
	case "remove", "rm":
		if len(args) < 3 {
			m.notice("usage: /mcp remove <name>")
			return
		}
		name := args[2]
		disconnected, err := m.ctrl.RemoveMCPServer(name)
		if err != nil {
			m.notice("mcp remove: " + err.Error())
			return
		}
		if disconnected {
			m.notice("disconnected " + name + " and removed it from config")
		} else {
			m.notice("removed " + name + " from config")
		}
	case "import":
		m.openMCPImportPicker()
	default:
		m.notice("unknown /mcp subcommand " + args[1] + " — try: /mcp, /mcp list, /mcp show, /mcp add, /mcp connect, /mcp import, /mcp remove")
	}
}

// showMCPStatus queues the connected MCP servers, their counts, and the prompt
// commands / resource refs they expose — the discovery surface for /mcp.
func (m *chatTUI) showMCPStatus() {
	if m.host == nil || (len(m.host.Servers()) == 0 && len(m.host.Failures()) == 0) {
		m.notice(i18n.M.SlashMCPNone)
		return
	}
	m.commitLine(renderMCPStatus(m.width, m.host.Servers(), m.host.Prompts(), m.host.Resources(), m.host.Failures()))
}

// notice queues a dim informational line to scrollback.
func (m *chatTUI) notice(note string) {
	m.commitLine(dim("  · " + note))
}

// resolveRefs resolves a line's @references off the event loop via the
// controller, delivering a refsResolvedMsg with the tagged context block.
func (m *chatTUI) resolveRefs(sent, display, restore string) tea.Cmd {
	return func() tea.Msg {
		block, errs := m.ctrl.ResolveRefs(context.Background(), sent)
		return refsResolvedMsg{sent: sent, display: display, restore: restore, block: block, errs: errs}
	}
}

// runMCPPrompt resolves a /mcp__server__prompt command off the event loop via
// the controller, delivering a promptResolvedMsg with the rendered prompt.
func (m *chatTUI) runMCPPrompt(input string) tea.Cmd {
	return func() tea.Msg {
		sent, found, err := m.ctrl.MCPPrompt(context.Background(), input)
		if !found {
			name := strings.TrimPrefix(strings.Fields(input)[0], "/")
			return promptResolvedMsg{display: input, err: fmt.Errorf("%s: /%s", i18n.M.SlashUnknown, name)}
		}
		return promptResolvedMsg{display: input, sent: sent, err: err}
	}
}
