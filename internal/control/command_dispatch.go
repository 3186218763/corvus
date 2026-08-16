package control

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"corvus/internal/event"
	"corvus/internal/i18n"
	"corvus/internal/memory"
	"corvus/internal/skill"
)

// SubmitDisplay runs input as a turn while remembering the user-facing display
// text for transcript replay when controller-side composition expands input.
func (c *Controller) SubmitDisplay(display, input string) {
	c.submit(input, display)
}

func (c *Controller) submit(input, display string) {
	trimmed := strings.TrimSpace(input)
	if note, ok := MemoryQuickAddNote(trimmed); ok {
		c.rememberProjectNote(note)
		return
	}
	if note, ok := RememberCommandNote(trimmed); ok {
		c.rememberProjectNote(note)
		return
	}
	if c.applyGoalCommand(trimmed, display) {
		return
	}
	if strings.HasPrefix(trimmed, "!") {
		c.RunShell(trimmed[1:])
		return
	}
	c.submitCommandOrTurn(trimmed, input, display)
}

func (c *Controller) submitCommandOrTurn(trimmed, input, display string) {
	runRefTurn := c.runRefTurn
	runRefTurnWithRefs := c.runRefTurnWithRefs
	runGoalLoop := c.runGoalLoopWithRawDisplay
	// Background slash commands (/compact /new /clear) run under the
	// controller's background context and are tracked by bgWG so Close can
	// cancel them and wait for the goroutines to unwind: a /new arriving after
	// Close must not swap the session or fire session hooks, and a /compact
	// must not rewrite the snapshot once the session lease has been released.
	switch {
	case trimmed == "/compact" || strings.HasPrefix(trimmed, "/compact "):
		focus := strings.TrimSpace(strings.TrimPrefix(trimmed, "/compact"))
		c.launchBackgroundCommand(func() {
			if err := c.Compact(c.bgCtx, focus); err != nil {
				c.notice("compaction failed: " + err.Error())
				return
			}
			c.notice("compacted")
			if c.isClosed() {
				// Teardown released the session lease; writing the snapshot now
				// would resurrect the transcript after Close.
				return
			}
			if err := c.SnapshotRewrite(); err != nil {
				slog.Warn("controller: snapshot after compact", "err", err)
			}
		})
	case trimmed == "/new":
		c.launchBackgroundCommand(func() {
			if err := c.NewSession(); err != nil {
				c.notice("new session failed: " + err.Error())
			} else {
				c.notice("new session")
			}
		})
	case trimmed == "/clear":
		c.launchBackgroundCommand(func() {
			if err := c.ClearSession(); err != nil {
				c.notice("clear context failed: " + err.Error())
			} else {
				c.notice("context cleared")
			}
		})
	case strings.HasPrefix(trimmed, "/mcp__"):
		c.runGuarded(func(ctx context.Context) error {
			sent, found, err := c.MCPPrompt(ctx, trimmed)
			if err != nil {
				return err
			}
			if !found {
				c.notice("unknown command: " + trimmed)
				return nil
			}
			return runGoalLoop(ctx, sent, sent, display)
		})
	case SlashCodeCommentLine(trimmed):
		// Slash-prefixed code comments are prompt text, not slash commands.
		runRefTurn(input, display)
	case strings.HasPrefix(trimmed, "/"):
		if ref, ok := FileRefLine(trimmed); ok {
			runRefTurn(ref, display)
			return
		}
		if ref, ok := SlashPathLineRef(trimmed, c.workspaceRoot); ok {
			runRefTurnWithRefs(input, ref, display)
			return
		}
		if SlashPathLikeLine(trimmed) {
			runRefTurn(input, display)
			return
		}
		// Management verbs (/model /memory /skills /mcp) emit a Notice, so
		// Submit-based frontends (desktop, HTTP) get them with no extra wiring.
		// The chat TUI handles these itself with richer output.
		fields := strings.Fields(trimmed)
		switch fields[0] {
		case "/tree":
			c.notice(c.BranchTreeText())
			return
		case "/branch":
			args := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
			if turn, name, fromTurn, err := ParseBranchTarget(args); err != nil {
				c.notice(err.Error())
			} else if fromTurn {
				if _, err := c.ForkNamed(turn-1, name); err != nil {
					c.notice(err.Error())
				}
			} else {
				if _, err := c.Branch(name); err != nil {
					c.notice(err.Error())
				}
			}
			return
		case "/switch":
			ref := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
			if _, err := c.SwitchBranch(ref); err != nil {
				c.notice(err.Error())
			}
			return
		case "/rewind":
			args := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
			turn, scope, err := parseRewind(args, c.Checkpoints())
			if err != nil {
				c.notice("usage: /rewind [turn] [code|conversation|both]")
				return
			}
			if err := c.Rewind(turn, scope); err != nil {
				c.notice(err.Error())
			}
			return
		case "/plan-exec":
			c.applyPlanExec(trimmed, display)
			return
		case "/prometheus":
			c.applyPrometheus(trimmed, display)
			return
		}
		if c.managementNotice(trimmed) {
			return
		}
		// A custom command wins over a skill of the same name; both resolve to a
		// turn. (Built-in slash verbs like /compact are handled above.)
		if sent, ok := c.CustomCommand(trimmed); ok {
			c.runGuarded(func(ctx context.Context) error {
				return runGoalLoop(ctx, sent, sent, display)
			})
			return
		}
		if sk, task, ok := c.resolveSkillInvocation(trimmed); ok {
			if sk.RunAs == skill.RunSubagent {
				if strings.TrimSpace(task) == "" {
					c.notice("usage: /" + sk.Name + " <task>")
					return
				}
				c.runSubagentSkillSlash(sk, task, trimmed, display)
				return
			}
			sent := c.skills.render(sk, task)
			c.runGuarded(func(ctx context.Context) error {
				return runGoalLoop(ctx, sent, sent, display)
			})
			return
		}
		c.notice("unknown command: " + trimmed)
	default:
		runRefTurn(input, display)
	}
}

func (c *Controller) rememberProjectNote(note string) {
	if note == "" {
		c.notice("nothing to remember")
		return
	}
	if path, err := c.QuickAdd(memory.ScopeProject, note); err != nil {
		c.notice("memory: " + err.Error())
	} else {
		c.notice("remembered → " + path)
	}
}

func (c *Controller) applyGoalCommand(input, display string) bool {
	cmd, ok := ParseGoalCommand(input)
	if !ok {
		return false
	}
	switch cmd.Action {
	case GoalCommandSet:
		c.SetPlanMode(false)
		c.SetGoalWithResearchMode(cmd.Text, cmd.ResearchMode)
		c.GoalStrict(cmd.Strict)
		c.notice(fmt.Sprintf(i18n.M.GoalSetFmt, ShortGoalForNotice(cmd.Text)))
		if c.runner != nil {
			c.runGuarded(func(ctx context.Context) error {
				return c.runGoalLoopWithRawDisplay(ctx, "Start pursuing the active goal now.", cmd.Text, display)
			})
		}
	case GoalCommandClear:
		c.ClearGoal()
		c.notice(i18n.M.GoalCleared)
	default:
		goal := c.Goal()
		if strings.TrimSpace(goal) == "" {
			c.notice(i18n.M.GoalEmpty)
		} else {
			c.notice(fmt.Sprintf(i18n.M.GoalCurrentFmt, goal))
		}
	}
	return true
}

// applyPlanExec reads the current canonical todo list and starts a goal that
// analyzes and dispatches independent steps concurrently via parallel_tasks.
// Supports --strict flag: /plan-exec --strict enables strict goal mode.
func (c *Controller) applyPlanExec(input, display string) {
	todos := c.executor.CanonicalTodoState()
	if len(todos) == 0 {
		c.notice("no active plan with todos to execute")
		return
	}

	// Parse --strict flag.
	strict := false
	fields := strings.Fields(input)
	for _, f := range fields {
		if f == "--strict" {
			strict = true
			break
		}
	}

	// Count completion status.
	total := len(todos)
	done := 0
	for _, t := range todos {
		if t.Status == "completed" {
			done++
		}
	}

	var b strings.Builder
	b.WriteString("You are the execution conductor. Route each step to the right sub-agent by module.\n\n")

	// Detect project structure for module-aware routing.
	modules := c.detectProjectModules()
	if len(modules) > 0 {
		b.WriteString("## Project modules detected\n\n")
		for _, m := range modules {
			fmt.Fprintf(&b, "- %s/", m)
		}
		b.WriteString("\n\nRoute steps to the module they belong to. Steps in different modules can run in parallel.\n\n")
	}

	b.WriteString("## Plan steps\n\n")
	for _, t := range todos {
		status := t.Status
		if status == "" {
			status = "pending"
		}
		mark := " "
		if status == "completed" {
			mark = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s (%s)\n", mark, t.Content, status)
	}
	b.WriteString("\n## Routing rules\n")
	b.WriteString("1. Group steps by MODULE \u2014 same module = serial, different modules = parallel batches\n")
	b.WriteString("2. Research/exploration across modules = use parallel_tasks\n")
	b.WriteString("3. Dispatch each batch via parallel_tasks \u2014 each sub-agent gets one module\u2019s context\n")
	b.WriteString("4. Verify each batch before the next\n")
	b.WriteString("5. Failures: fix before moving on\n")
	b.WriteString("\nGoal: each sub-agent focuses on one module and does not carry irrelevant context.\n")
	if done > 0 {
		fmt.Fprintf(&b, "\nNote: %d/%d steps are already completed. Focus on the remaining %d steps.\n", done, total, total-done)
	}
	prompt := b.String()

	// Show module preview.
	if len(modules) > 0 {
		c.notice(fmt.Sprintf("plan-exec: detected %d modules — %s", len(modules), strings.Join(modules, ", ")))
	}

	c.SetPlanMode(false)
	c.SetGoal("execute plan: " + ShortGoalForNotice(todos[0].Content))
	c.GoalStrict(strict)
	c.notice(fmt.Sprintf("plan-exec: dispatching %d plan steps (strict=%v)", total, strict))
	if c.runner != nil {
		c.runGuarded(func(ctx context.Context) error {
			return c.runGoalLoopWithRawDisplay(ctx, prompt, prompt, display)
		})
	}
}

// prometheusPrompt is the strategic planner system prompt.
const prometheusPrompt = "You are Prometheus, a strategic planner. Interview the user one question at a time. Cover: scope, modules, files, constraints, tests. When ready, output a numbered plan with each step tagged by module. End with [goal:complete]. Do not implement.\n\nFor independent research directions, use parallel_tasks before planning."

// applyPrometheus starts an interactive planning interview, inspired by OMO's
// Prometheus agent. It enters goal mode with a structured interview prompt.
func (c *Controller) applyPrometheus(input, display string) {
	args := strings.TrimSpace(strings.TrimPrefix(input, "/prometheus"))
	if args == "" || args == "--strict" {
		c.notice("usage: /prometheus <your task description>")
		return
	}
	strict := false
	if strings.HasPrefix(args, "--strict ") {
		strict = true
		args = strings.TrimPrefix(args, "--strict ")
	}
	prompt := prometheusPrompt + "\n\n## User request\n\n" + args + "\n\nBegin the interview by asking your first clarifying question."
	c.SetPlanMode(false)
	c.SetGoal("plan: " + ShortGoalForNotice(args))
	c.GoalStrict(strict)
	c.notice("prometheus: starting planning interview")
	if c.runner != nil {
		c.runGuarded(func(ctx context.Context) error {
			return c.runGoalLoopWithRawDisplay(ctx, prompt, prompt, display)
		})
	}
}

// runRefTurn resolves a line's @references into a context block and starts a
// turn with it prepended (or the raw line when nothing resolved).
func (c *Controller) runRefTurn(input, display string) {
	c.runRefTurnWithRefs(input, input, display)
}

// runRefTurnWithRefs resolves references from refLine while preserving input as
// the user's actual prompt text. This lets compiler diagnostics such as
// "/path/File.kt:12: error" attach @/path/File.kt without rewriting the error.
func (c *Controller) runRefTurnWithRefs(input, refLine, display string) {
	c.runRefTurnWithResolver(input, refLine, display, c.ResolveRefs)
}

func (c *Controller) runRefTurnWithResolver(input, refLine, display string, resolve func(context.Context, string) (string, []string)) {
	c.runGuarded(func(ctx context.Context) error {
		return c.runRefTurnWithResolverSync(ctx, input, refLine, display, resolve)
	})
}

func (c *Controller) runRefTurnWithResolverSync(ctx context.Context, input, refLine, display string, resolve func(context.Context, string) (string, []string)) error {
	block, errs := resolve(ctx, refLine)
	for _, e := range errs {
		c.notice(e)
	}
	sent := input
	if block != "" {
		sent = "Referenced context:\n\n" + block + "\n\n" + input
	}
	return c.runGoalLoopWithRawDisplay(ctx, sent, input, display)
}

// notice emits an informational Notice event.
func (c *Controller) notice(text string) {
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: text})
}

func (c *Controller) noticeDetail(text, detail string) {
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: text, Detail: detail})
}

// detectProjectModules scans the workspace root for top-level source directories
// to enable module-aware task routing in /plan-exec.
func (c *Controller) detectProjectModules() []string {
	root := c.sessionDir
	for i := 0; i < 3 && root != ""; i++ {
		if hasFile(root, "go.mod") || hasFile(root, "package.json") || hasFile(root, ".git") {
			return listSourceDirs(root, 2)
		}
		root = filepath.Dir(root)
		if root == filepath.Dir(root) {
			break
		}
	}
	return nil
}

func hasFile(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func listSourceDirs(root string, maxDepth int) []string {
	skip := map[string]bool{
		".git": true, ".github": true, "node_modules": true,
		"vendor": true, ".corvus": true, "desktop": true,
		"dist": true, "build": true, ".cache": true, "bin": true,
	}
	var dirs []string
	walkDir(root, "", skip, maxDepth, &dirs)
	return dirs
}

func walkDir(root, rel string, skip map[string]bool, depth int, out *[]string) {
	if depth <= 0 {
		return
	}
	dir := root
	if rel != "" {
		dir = filepath.Join(root, rel)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || skip[name] || strings.HasPrefix(name, ".") {
			continue
		}
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		if hasSourceFiles(filepath.Join(root, childRel)) {
			*out = append(*out, childRel)
		}
		walkDir(root, childRel, skip, depth-1, out)
	}
}

func hasSourceFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}
