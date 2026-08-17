package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Runner binds a set of resolved hooks to a session: a working directory, the
// spawner, and a notify callback that surfaces non-blocking hook messages to the
// user. It is the single object the agent (tool events) and the controller
// (prompt/stop events) fire hooks through, so neither has to know how hooks load
// or run. A nil *Runner is a valid no-op (no hooks configured).
type Runner struct {
	hooks     []ResolvedHook
	cwd       string
	homeDir   string // user-state root used for project-hook trust decisions
	spawner   Spawner
	notify    func(string) // surface a non-blocking (warn/error) hook message; may be nil
	mu        sync.RWMutex
	sessionID string
	auditPath string // guarded by mu; empty disables the audit sidecar
}

// SetSessionID updates the Claude-compatible session identifier used in hook
// payloads. It is safe to call when a controller rotates sessions.
func (r *Runner) SetSessionID(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.sessionID = id
	r.mu.Unlock()
}

func (r *Runner) payload(event Event) Payload {
	r.mu.RLock()
	id := r.sessionID
	r.mu.RUnlock()
	return Payload{Event: event, Cwd: r.cwd, SessionID: id}
}

// NewRunner builds a Runner. spawner nil uses DefaultSpawner; notify nil drops
// non-blocking messages.
func NewRunner(hooks []ResolvedHook, cwd string, spawner Spawner, notify func(string)) *Runner {
	return NewRunnerWithHome(hooks, cwd, "", spawner, notify)
}

// NewRunnerWithHome is NewRunner with the user-state override used by the
// project-hook trust store. Existing callers can keep using NewRunner; boot
// uses this constructor so `/hooks trust` and `/hooks revoke` update the same
// state that was consulted during loading.
func NewRunnerWithHome(hooks []ResolvedHook, cwd, homeDir string, spawner Spawner, notify func(string)) *Runner {
	return &Runner{hooks: append([]ResolvedHook(nil), hooks...), cwd: cwd, homeDir: homeDir, spawner: spawner, notify: notify}
}

// TrustProjectHooks records trust for the runner's project and immediately
// reloads the hook set. The settings file is parsed only after the durable
// record has been published.
func (r *Runner) TrustProjectHooks() error {
	if r == nil {
		return nil
	}
	if err := TrustProject(r.homeDir, r.cwd); err != nil {
		return err
	}
	return r.reloadProjectHooks()
}

// RevokeProjectHooks removes trust and immediately removes project hooks from
// the active runner. Global and installed-plugin hooks remain active.
func (r *Runner) RevokeProjectHooks() error {
	if r == nil {
		return nil
	}
	if err := RevokeProjectTrust(r.homeDir, r.cwd); err != nil {
		return err
	}
	return r.reloadProjectHooks()
}

// ProjectHooksTrusted reports the durable trust state without loading project
// settings. It is useful for management UIs and is safe to call mid-turn.
func (r *Runner) ProjectHooksTrusted() bool {
	if r == nil {
		return false
	}
	return IsProjectTrusted(r.homeDir, r.cwd)
}

func (r *Runner) reloadProjectHooks() error {
	hooks, _ := LoadWithReport(LoadOptions{ProjectRoot: r.cwd, HomeDir: r.homeDir})
	r.mu.Lock()
	r.hooks = hooks
	r.mu.Unlock()
	return nil
}

// Hooks returns the resolved hooks (for `/hooks` listing).
func (r *Runner) Hooks() []ResolvedHook {
	if r == nil {
		return nil
	}
	return r.hooksSnapshot()
}

func (r *Runner) hooksSnapshot() []ResolvedHook {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]ResolvedHook(nil), r.hooks...)
}

// Enabled reports whether any hooks are configured.
func (r *Runner) Enabled() bool { return len(r.hooksSnapshot()) > 0 }

// Has reports whether any configured hook listens for the given event. Callers
// use it to skip work that only matters when a specific hook exists (e.g. the
// agent buffers reasoning for transform only when a PostLLMCall hook is set).
func (r *Runner) Has(event Event) bool {
	for _, h := range r.hooksSnapshot() {
		if h.Event == event {
			return true
		}
	}
	return false
}

// HasPostLLMCall reports whether a PostLLMCall hook is configured, so the agent
// keeps streaming reasoning live unless a transform is actually wired up.
func (r *Runner) HasPostLLMCall() bool { return r.Has(PostLLMCall) }

// ToolMutationHooksEnabled reports whether any hook runs around a tool call.
// These hooks execute user shell code and may mutate paths that the tool itself
// does not declare, so checkpoint coverage must account for them.
func (r *Runner) ToolMutationHooksEnabled() bool {
	return r.Has(PreToolUse) || r.Has(PostToolUse) || r.Has(PostToolUseFailure)
}

// PreToolUse fires before a tool call. block=true means the call must be
// refused; message is the reason (fed back to the model and shown to the user).
func (r *Runner) PreToolUse(ctx context.Context, name string, args json.RawMessage) (block bool, message string) {
	if !r.Enabled() {
		return false, ""
	}
	hooks := r.hooksSnapshot()
	p := r.payload(PreToolUse)
	p.ToolName, p.ToolArgs = name, args
	rep := Run(ctx, p, hooks, r.spawner)
	return r.handle(rep)
}

// PostToolUse fires after a tool call. It can't block; non-pass outcomes are
// surfaced to the user via notify.
func (r *Runner) PostToolUse(ctx context.Context, name string, args json.RawMessage, result string) {
	if !r.Enabled() {
		return
	}
	hooks := r.hooksSnapshot()
	p := r.payload(PostToolUse)
	p.ToolName, p.ToolArgs, p.ToolResult = name, args, result
	rep := Run(ctx, p, hooks, r.spawner)
	r.handle(rep)
}

// PostToolUseFailure fires when a tool invocation returns an error.
func (r *Runner) PostToolUseFailure(ctx context.Context, name string, args json.RawMessage, result string, err error) {
	if !r.Enabled() {
		return
	}
	hooks := r.hooksSnapshot()
	p := r.payload(PostToolUseFailure)
	p.ToolName, p.ToolArgs, p.ToolResult = name, args, result
	if err != nil {
		p.Error = err.Error()
		p.IsInterrupt = errors.Is(err, context.Canceled)
	}
	r.handle(Run(ctx, p, hooks, r.spawner))
	// Native Corvus PostToolUse historically observed both success and
	// failure. Preserve that contract while Claude hooks use the distinct event.
	legacy := r.nativeHooks(PostToolUse, hooks)
	if len(legacy) > 0 {
		p.Event = PostToolUse
		r.handle(Run(ctx, p, legacy, r.spawner))
	}
}

// PermissionRequest fires before a tool approval prompt is shown. A native
// Corvus hook here can't answer the dialog (non-pass outcomes are surfaced
// via notify only); a Claude-imported hook (PayloadFormat "claude") can
// answer it on the user's behalf via exit 2 or a JSON decision, matching
// Claude's own contract. Project-scoped hooks (ScopeProject) keep the deny
// side of that contract — exit 2 and a JSON deny still block — but their JSON
// "allow" is ignored and treated as no opinion, because repository-controlled
// code must never auto-approve on the user's behalf; only global and
// plugin-package hooks can allow. decision == nil means "no opinion, show the
// prompt normally"; a non-nil decision means the caller should skip the prompt
// and treat it as denied (false) or auto-approved (true).
func (r *Runner) PermissionRequest(ctx context.Context, name, subject string, args json.RawMessage) (decision *bool, message string) {
	if !r.Enabled() {
		return nil, ""
	}
	hooks := r.hooksSnapshot()
	p := r.payload(PermissionRequest)
	p.ToolName, p.ToolArgs, p.Subject = name, args, subject
	rep := Run(ctx, p, hooks, r.spawner)
	block, msg := r.handle(rep)
	switch {
	case block:
		deny := false
		return &deny, msg
	case rep.Allowed:
		allow := true
		return &allow, msg
	default:
		return nil, msg
	}
}

// PromptSubmit fires before a turn starts. block=true aborts the turn; message
// is the reason.
func (r *Runner) PromptSubmit(ctx context.Context, prompt string, turn int) (block bool, message string) {
	if !r.Enabled() {
		return false, ""
	}
	hooks := r.hooksSnapshot()
	p := r.payload(UserPromptSubmit)
	p.Prompt, p.Turn = prompt, turn
	rep := Run(ctx, p, hooks, r.spawner)
	return r.handle(rep)
}

// Stop fires after a turn finishes. It can't block.
func (r *Runner) Stop(ctx context.Context, lastAssistant string, turn int) {
	if !r.Enabled() {
		return
	}
	hooks := r.hooksSnapshot()
	p := r.payload(Stop)
	p.LastAssistant, p.Turn = lastAssistant, turn
	rep := Run(ctx, p, hooks, r.spawner)
	r.handle(rep)
}

// StopResult emits Stop on success and StopFailure when the turn failed.
func (r *Runner) StopResult(ctx context.Context, lastAssistant string, turn int, err error) {
	if err == nil {
		r.Stop(ctx, lastAssistant, turn)
		return
	}
	if !r.Enabled() {
		return
	}
	hooks := r.hooksSnapshot()
	p := r.payload(StopFailure)
	p.LastAssistant, p.Turn, p.Error = lastAssistant, turn, err.Error()
	p.IsInterrupt = errors.Is(err, context.Canceled)
	r.handle(Run(ctx, p, hooks, r.spawner))
	legacy := r.nativeHooks(Stop, hooks)
	if len(legacy) > 0 {
		p.Event = Stop
		r.handle(Run(ctx, p, legacy, r.spawner))
	}
}

func (r *Runner) nativeHooks(event Event, hooks []ResolvedHook) []ResolvedHook {
	var out []ResolvedHook
	for _, h := range hooks {
		if h.Event == event && h.PayloadFormat != "claude" {
			out = append(out, h)
		}
	}
	return out
}

// SessionStart fires when a session becomes active. It can't block; successful
// stdout may contribute one-shot context for the next model request.
func (r *Runner) SessionStart(ctx context.Context, source ...string) []string {
	if !r.Enabled() {
		return nil
	}
	hooks := r.hooksSnapshot()
	p := r.payload(SessionStart)
	p.Source = "startup"
	if len(source) > 0 && strings.TrimSpace(source[0]) != "" {
		p.Source = strings.TrimSpace(source[0])
	}
	rep := Run(ctx, p, hooks, r.spawner)
	r.handle(rep)
	return r.additionalContexts(rep)
}

// SessionEnd fires when a session is closed or rotated (/new). It can't block.
func (r *Runner) SessionEnd(ctx context.Context, reason ...string) {
	if !r.Enabled() {
		return
	}
	hooks := r.hooksSnapshot()
	p := r.payload(SessionEnd)
	p.Reason = "other"
	if len(reason) > 0 && strings.TrimSpace(reason[0]) != "" {
		p.Reason = strings.TrimSpace(reason[0])
	}
	r.handle(Run(ctx, p, hooks, r.spawner))
}

// SubagentStop fires when a `task` sub-agent finishes. It can't block; last is
// the sub-agent's final answer.
func (r *Runner) SubagentStop(ctx context.Context, last string) {
	if !r.Enabled() {
		return
	}
	hooks := r.hooksSnapshot()
	p := r.payload(SubagentStop)
	p.LastAssistant = last
	r.handle(Run(ctx, p, hooks, r.spawner))
}

// Notification fires when the agent needs the user's attention (e.g. a pending
// approval). It can't block; message describes what's waiting.
func (r *Runner) Notification(ctx context.Context, message string, notificationType ...string) {
	if !r.Enabled() {
		return
	}
	hooks := r.hooksSnapshot()
	p := r.payload(Notification)
	p.Message = message
	if len(notificationType) > 0 {
		p.NotificationType = strings.TrimSpace(notificationType[0])
	}
	r.handle(Run(ctx, p, hooks, r.spawner))
}

// PostLLMCall fires after every model turn completes but before the
// reasoning_content is stored in the session. It returns the hook's stdout as
// the new reasoning text, or the original reasoning if the hook passes with
// empty stdout / doesn't exist / fails. A non-pass outcome is surfaced via
// notify but doesn't block.
func (r *Runner) PostLLMCall(ctx context.Context, reasoning string, turn int) string {
	if !r.Has(PostLLMCall) {
		return reasoning
	}
	hooks := r.hooksSnapshot()
	p := r.payload(PostLLMCall)
	p.Reasoning, p.Turn = reasoning, turn
	rep := Run(ctx, p, hooks, r.spawner)
	r.handle(rep)
	for _, o := range rep.Outcomes {
		if o.Decision == DecisionPass {
			if s := strings.TrimSpace(o.Stdout); s != "" {
				return s
			}
		}
	}
	return reasoning
}

// PreCompact fires just before a compaction pass and returns the concatenated
// stdout of its hooks as extra summary guidance, so a hook can steer what the
// summary keeps. Non-pass outcomes are surfaced via notify.
func (r *Runner) PreCompact(ctx context.Context, trigger string) string {
	if !r.Enabled() {
		return ""
	}
	hooks := r.hooksSnapshot()
	p := r.payload(PreCompact)
	p.Trigger = trigger
	rep := Run(ctx, p, hooks, r.spawner)
	r.handle(rep)
	var b strings.Builder
	for _, o := range rep.Outcomes {
		if s := strings.TrimSpace(o.Stdout); s != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(s)
		}
	}
	return b.String()
}

func (r *Runner) additionalContexts(rep Report) []string {
	var contexts []string
	for _, o := range rep.Outcomes {
		if o.Decision != DecisionPass {
			continue
		}
		out, warnings := ParseOutput(rep.Event, o.Stdout)
		for _, warning := range warnings {
			if r.notify != nil {
				r.notify(FormatOutcome(Outcome{
					Hook:     o.Hook,
					Decision: DecisionWarn,
					Stdout:   warning,
				}))
			}
		}
		if out.AdditionalContext != "" {
			contexts = append(contexts, out.AdditionalContext)
		}
	}
	return contexts
}

// handle surfaces every non-pass outcome to the user (notify) and returns the
// block decision plus the blocking hook's message.
func (r *Runner) handle(rep Report) (bool, string) {
	var blockMsg string
	for _, o := range rep.Outcomes {
		if o.Decision == DecisionPass {
			continue
		}
		msg := FormatOutcome(o)
		if r.notify != nil {
			r.notify(msg)
		}
		if o.Decision == DecisionBlock {
			blockMsg = msg
		}
	}
	r.audit(rep)
	return rep.Blocked, blockMsg
}

// FormatOutcome renders a non-pass outcome as a one-line human message.
func FormatOutcome(o Outcome) string {
	detail := strings.TrimSpace(o.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(o.Stdout)
	}
	tag := string(o.Hook.Scope) + "/" + string(o.Hook.Event)
	cmd := o.Hook.Command
	if cmd == "" && o.Hook.ContextFile != "" {
		cmd = "context:" + o.Hook.ContextFile
	}
	cmd = clipRunes(cmd, 60)
	trunc := ""
	if o.Truncated {
		trunc = " (output truncated)"
	}
	head := fmt.Sprintf("hook [%s] %s — %s%s", tag, cmd, o.Decision, trunc)
	if detail != "" {
		return head + ": " + detail
	}
	return head
}

func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 1 {
		return ""
	}
	return string(r[:max]) + "…"
}
