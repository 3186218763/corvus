// Package control is the transport-agnostic session driver. A Controller owns
// the agent run loop and session lifecycle, takes commands (Send/Cancel/Approve/
// SetPlanMode/Compact/NewSession/…), and emits everything that happens —
// reasoning, tool calls, approvals, turn completion — as a typed event stream to
// a single event.Sink.
//
// The point is one orchestration layer behind every frontend: a terminal TUI, a
// desktop webview, or an HTTP/SSE server each drive the Controller identically
// (issue commands, render events) and none of them re-implement turn lifecycle,
// cancellation, or approval. The Controller depends on no frontend.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"corvus/internal/agent"
	"corvus/internal/autoresearch"
	"corvus/internal/capability"
	"corvus/internal/checkpoint"
	"corvus/internal/command"
	"corvus/internal/config"
	"corvus/internal/event"
	"corvus/internal/guardian"
	"corvus/internal/hook"
	"corvus/internal/i18n"
	"corvus/internal/jobs"
	"corvus/internal/memory"
	"corvus/internal/nilutil"
	"corvus/internal/permission"
	"corvus/internal/plugin"
	"corvus/internal/provider"
	"corvus/internal/recovery"
	"corvus/internal/sandbox"
	"corvus/internal/skill"
	"corvus/internal/store"
	"corvus/internal/tool"
	"corvus/internal/workspacelease"
)

// ErrTurnRunning reports that a caller tried to start a second foreground turn
// while one is already active in the same Controller.
var ErrTurnRunning = errors.New("turn already running")

// errTurnRunningRotation and errRotationInProgress are returned by the
// session-rotation gate (beginRotation) when a rotation cannot proceed: a turn
// is in flight, or another rotation already holds the gate.
var (
	errTurnRunningRotation = errors.New("cannot start a new session while a turn is running")
	errRotationInProgress  = errors.New("cannot start a new session while another session change is in progress")
	errControllerClosed    = errors.New("controller is closed")
)

// errNoSessionPath is returned by snapshot when a session has content to persist
// but no resolved session path — a misconfiguration (e.g. an unresolvable data
// dir in a bot deployment) that previously dropped conversations silently
// (#4414). Callers log it and continue; it must never be swallowed quietly.
var errNoSessionPath = errors.New("session has content but no session path; conversation cannot be persisted")

// Controller drives one chat session. Construct with New; drive with the command
// methods; observe through the Sink passed in Options.
type Controller struct {
	runner       agent.Runner
	executor     *agent.Agent
	guardianSess *guardian.Session // nil when guardian is disabled
	guardianPath string            // persisted guardian session file ("" when disabled)
	// recoveryGate is the shared Auto Guard state for this controller.
	// nil when the feature is not wired for this controller.
	recoveryGate *recovery.Gate
	sink         event.Sink
	policy       permission.Policy
	// subagentGate is the shared gate every headless-only sub-agent surface
	// reads from (see Options.SubagentGate). Nil when the caller didn't build
	// one — sub-agents then keep whatever gate they were constructed with.
	subagentGate *SharedHeadlessGate

	label        string
	modelRef     string
	systemPrompt string
	sessionDir   string
	commands     atomic.Pointer[[]command.Command]
	// skills owns the session's discovered skills (enabled subset, full set, and
	// the reloadable stores) — the skills slice of the Capabilities concern. See
	// skill.go.
	skills              skillSet
	skillRunner         skill.SubagentRunner
	readOnlySkillRunner skill.SubagentRunner
	skillProfile        skill.ProfileResolver
	slashSkillSeq       atomic.Uint64
	hooks               *hook.Runner // session hook runner; nil-safe (no hooks configured)
	// hookContexts carries one-shot lifecycle hook context into the next real
	// user turn without changing the cache-stable system prompt.
	hookContexts []string
	// memory owns the loaded memory snapshot, the pending turn-tail notes queue,
	// and write serialization behind its own locks, off c.mu — so a memory-panel
	// save never stalls an approval or status poll. See memory.go.
	memory            memoryManager
	cleanup           func()
	responseLanguage  string
	reasoningLanguage string
	// disableColdResumePrune skips stale-tool-result elision on cold resume.
	// Zero value keeps the prune on (the cheaper default).
	disableColdResumePrune            bool
	shell                             sandbox.Shell                    // interpreter for user-invoked "!" commands; zero = auto
	startedOnce                       bool                             // guards the one-shot SessionStart hook on first turn
	closeOnce                         sync.Once                        // makes close idempotent under racing teardown paths
	onRemember                        func(rule string) RememberResult // set via Options; invoked when user picks "always allow"
	onRememberPlanModeReadOnlyCommand func(prefix string) PlanModeReadOnlyCommandTrustResult
	sessionRecoveryMeta               func(SessionRecoveryRequest) agent.BranchMeta
	onSessionRecovered                func(SessionRecoveryInfo) error

	// balanceURL/balanceKey target the active provider's optional wallet-balance
	// endpoint (empty when the provider declares none). Captured at build so a
	// model/key switch — which rebuilds the controller — refreshes them.
	balanceURL    string
	balanceKey    string
	balanceClient *http.Client

	// jobs is the session-scoped background-job manager. The agent's background
	// tools spawn into it; Compose drains its completion notes into the next turn;
	// Close cancels its still-running jobs.
	jobs *jobs.Manager
	// workspaceLease is the Delivery writer owner shared with the executor.
	// It is exposed only through a sanitized state snapshot for Desktop recovery.
	workspaceLease *workspacelease.Owner

	// mcp owns the session's live tool/plugin surface — the MCP plugin Host, the
	// tool registry the executor reads each turn, and the session-scoped context a
	// hot-added stdio server binds its subprocess to — behind its own lock, off
	// c.mu. The Controller keeps the config-facing orchestration (persisting
	// MCP entries to their global/project source on add/remove, building specs
	// from entries). See mcp.go.
	mcp                   mcpManager
	mcpDefaultCallTimeout time.Duration
	mcpConfigureSpec      func(*plugin.Spec)
	capabilityRuntime     *agent.MCPCapabilityRuntime

	// Capability routing (Delivery hybrid route + dual-model Planner proxy).
	// Not part of the provider-visible prefix; only seeds the turn-scoped ledger
	// and optional semantic router.
	pluginCfg       []config.PluginEntry
	capCachedTools  map[string][]plugin.CachedTool
	capCacheKeyOK   map[string]bool
	semanticRouter  *capability.SemanticRouter
	capabilityAudit *capability.Audit
	// capabilityProxy directs unready MCP candidates to use_capability in the
	// transient route block (Delivery and dual-model Planner).
	capabilityProxy bool
	// proxyToolsFn returns live tools observed through use_capability without
	// entering the provider-visible registry (Balanced dual-model Planner).
	proxyToolsFn   func() map[string][]plugin.CachedTool
	runtimeProfile capability.Profile

	// goals owns the active goal's FSM (status, intercepts, idle/turn counters)
	// and its persistence, behind its own mutex so a per-turn goal save never
	// stalls an approval or status poll on c.mu. See goal.go.
	goals        goalMachine
	autoResearch *autoresearch.Store

	// workspaceRoot is the workspace root: the base for resolving @-refs and slash
	// path refs, the working directory for user "!" shell commands and custom
	// command discovery, and the guard root for checkpoint restore writes. It is
	// surfaced to frontends via WorkspaceRoot().
	workspaceRoot string

	// checkpoints owns the snapshot-based rewind bookkeeping (the per-session
	// store, the monotonic turn counter, and the conversation-rewind boundary map)
	// behind its own lock, off c.mu — so a boundary read for a rewind/fork never
	// contends on the run-state lock. The Controller keeps the rewind/fork/summarize
	// orchestration (truncating the session, restoring code, emitting events). See
	// checkpoint.go.
	checkpoints checkpointManager
	// mutationObserver is the host-side file mutation observer for v2 checkpoints.
	mutationObserver *checkpoint.MutationObserver
	// sessionRevision increments on successful rewind/undo and is used as a
	// prepare/commit freshness token.
	sessionRevision int64

	// approval owns the approval/ask prompt bookkeeping and the runtime approval
	// posture (ask/auto/yolo, session grants, the just-approved-plan window)
	// behind its own locks, off c.mu. The Controller keeps the I/O orchestration
	// (requestApproval/Ask emit events + fire hooks + rebuild the executor gate).
	// See approval.go.
	approval approvalManager

	// mu guards the run state; every critical section under it is short and
	// non-blocking.
	mu     sync.Mutex
	cancel context.CancelFunc
	// bgCtx/bgCancel own the background slash-command goroutines launched by
	// submitCommandOrTurn (/compact /new /clear). Close cancels them and waits
	// (bgWG) so a late /new cannot swap the session or /compact cannot rewrite
	// the snapshot after teardown has released the session lease.
	bgCtx     context.Context
	bgCancel  context.CancelFunc
	bgWG      sync.WaitGroup
	running   bool
	finishing bool // TurnDone is still being delivered; park a replacement turn
	canceling bool
	// closed marks the controller as terminally torn down (close() ran). It
	// seals turn admission: without it, a submit arriving AFTER close cleared
	// the parked queue — but while a still-running turn's TurnDone delivery
	// was in flight — would park again and then start against freed resources
	// when the window closed.
	closed bool
	// parkedTurns holds turn bodies that arrived during the finishing window,
	// FIFO. finishGuardedTurn starts the oldest one as it closes the window
	// (see runGuarded/finishGuardedTurn); close() discards any remainder.
	parkedTurns []func(ctx context.Context) error
	// rotating is set under mu while NewSession/ClearSession swap the executor
	// session out. Checking running once and then swapping later leaves a
	// TOCTOU window: a turn can start (running=false at check time) during the
	// intervening Snapshot() and then have its live session replaced. running
	// and rotating are mutually exclusive gates — a turn refuses to start while
	// a rotation is in progress, and a rotation refuses to start while a turn
	// runs — so the run loop's session reference cannot change under it.
	rotating    bool
	autosaveWG  sync.WaitGroup
	planMode    bool
	sessionPath string
	// recoveryDepthCapNotices records session paths that already surfaced the
	// depth-cap recovery warning. Repeated saves on the same conflict copy are
	// diagnostic noise for the UI; keep logging/diagnostics, but emit the user
	// notice once per controller/session path.
	recoveryDepthCapNotices map[string]bool
	// snapshotMu serializes the whole save/recovery handoff for this controller.
	// Agent-level path locks protect individual files, but recovery also moves
	// controller-owned state (sessionPath, guardianPath, checkpoints, rewrite
	// baseline). Letting a second snapshot observe that migration halfway through
	// can turn one conflict into a recovery cascade. Session/path swaps
	// (new/clear/fork/branch/switch/resume/SetSessionPath) hold it for the same
	// reason: a save that reads the old path but the new session would write one
	// transcript's messages into another's file, or manufacture a bogus conflict.
	// Not reentrant — never call snapshot (or anything that snapshots, such as
	// recoverInterruptedTurn or maybeColdResumePrune) while holding it.
	snapshotMu sync.Mutex
	// turn counts model turns this session, passed to hooks in their payload.
	turn int

	displayRecorder func(content, display string)
}

type approvalReply struct {
	allow   bool
	session bool
	persist bool // true = write "always allow" rule to config
}

type pendingApproval struct {
	tool         string
	subject      string
	reason       string
	rawInput     json.RawMessage
	fresh        bool
	requireHuman bool
	autoDrain    bool
	kind         string // tool | plan | recovery; empty = tool
	recovery     *event.RecoveryApproval
	reply        chan approvalReply
}

// pendingAsk is an in-flight ask question batch. questions is retained so the
// AskRequest can be re-emitted to a frontend that reconnected after the original
// event (see ReplayPendingPrompts).
type pendingAsk struct {
	questions []event.AskQuestion
	reply     chan []event.AskAnswer
}

type AutoResearchEvidenceInput struct {
	ID       string
	Kind     string
	Summary  string
	Source   string
	Command  string
	Paths    []string
	Accepted bool
}

type plannerSessionResetter interface {
	ResetPlannerSession()
}

// plannerAgentAccessor is satisfied by agent.Coordinator so path rebinds can
// refresh the planner's sticky prompt_cache_key SessionCacheID alongside the
// executor without importing Coordinator internals into every call site.
type plannerAgentAccessor interface {
	PlannerAgent() *agent.Agent
}

// RuntimeStatus is the frontend-facing snapshot of foreground turn state. It is
// intentionally more explicit than the legacy Running bool so UI code can
// distinguish a cancellable foreground turn from pending prompts and background
// jobs.
type RuntimeStatus struct {
	Running         bool
	PendingPrompt   bool
	BackgroundJobs  int
	CancelRequested bool
	Cancellable     bool
}

const (
	ToolApprovalAsk     = "ask"
	ToolApprovalAuto    = "auto"
	ToolApprovalDontAsk = "dontAsk"
	ToolApprovalYolo    = "yolo"
)

const (
	memoryRememberTool = "remember"
	memoryForgetTool   = "forget"
)

// RememberResult describes what happened when an approval rule was persisted.
type RememberResult struct {
	Rule      string
	Path      string
	Saved     bool
	CoveredBy string
	Err       error
}

// PlanModeReadOnlyCommandTrustResult describes what happened when a trusted bash
// command prefix was persisted for plan-mode research.
type PlanModeReadOnlyCommandTrustResult struct {
	Prefix    string
	Path      string
	Saved     bool
	CoveredBy string
	Err       error
}

type SessionRecoveryRequest struct {
	OriginalPath string
	Reason       string
	Mode         string
}

type SessionRecoveryInfo struct {
	OriginalPath string
	RecoveryPath string
	Existing     bool
	Reason       string
	Meta         agent.BranchMeta
}

// Options carries the already-built pieces setup assembles. Lifecycle metadata
// lets the controller mint and rotate session files; Host/Commands are surfaced
// to frontends that resolve MCP prompts and slash commands.
type Options struct {
	Runner   agent.Runner
	Executor *agent.Agent
	Guardian *guardian.Session
	// RecoveryReviewer is the optional independent recovery reviewer (nil =
	// rule-only path with fail-closed human confirmation for ambiguous cases).
	RecoveryReviewer recovery.Reviewer
	// RecoveryHeadless blocks mutations that need confirmation instead of
	// waiting forever when no human decision channel exists.
	RecoveryHeadless bool
	Sink             event.Sink
	Policy           permission.Policy
	// SubagentGate is the shared, mutable gate every headless-only sub-agent
	// surface (task, writer-capable skill sub-agents, planner) reads from. Nil
	// disables gating for those surfaces same as before this field existed.
	// SetToolApprovalMode and ApplyHeadlessApprovalMode call Update on it so a
	// runtime approval-mode switch reaches sub-agents, not just the parent
	// executor's own gate.
	SubagentGate  *SharedHeadlessGate
	Label         string
	ModelRef      string
	SystemPrompt  string
	SessionDir    string
	SessionPath   string
	Host          *plugin.Host
	Commands      []command.Command
	Skills        []skill.Skill
	AllSkills     []skill.Skill
	SkillStore    *skill.Store
	AllSkillStore *skill.Store
	// SkillRunner executes a runAs=subagent skill in an isolated child loop.
	// ReadOnlySkillRunner is reserved for explicitly read-only entry points;
	// Plan itself is a workflow instruction and uses SkillRunner with the shared
	// Permissions/Sandbox gate. SkillProfile supplies model/effort display
	// metadata for the synthetic top-level run_skill event.
	SkillRunner         skill.SubagentRunner
	ReadOnlySkillRunner skill.SubagentRunner
	SkillProfile        skill.ProfileResolver
	Hooks               *hook.Runner
	Memory              *memory.Set
	Cleanup             func()
	// BalanceURL/BalanceKey wire the active provider's optional wallet-balance
	// endpoint and bearer key; empty when the provider declares no balance_url.
	BalanceURL    string
	BalanceKey    string
	BalanceClient *http.Client
	// Jobs is the session-scoped background-job manager (nil disables background jobs).
	Jobs *jobs.Manager
	// WorkspaceLease is the Delivery writer owner shared with the executor.
	WorkspaceLease *workspacelease.Owner
	// Registry is the executor's live tool set, and PluginCtx the session-scoped
	// context; both are needed for hot-adding MCP servers via AddMCPServer.
	Registry  *tool.Registry
	PluginCtx context.Context
	// MCPDefaultCallTimeout is the global MCP call cap used by hot-connected
	// servers when they do not declare a server- or tool-specific override.
	MCPDefaultCallTimeout time.Duration
	// MCPConfigureSpec injects host-local launch and isolation policy into every
	// hot-connected server without persisting that state in project config.
	MCPConfigureSpec func(*plugin.Spec)
	// CapabilityRuntime is the controller-local authoritative MCP inventory used
	// by stable use_capability frontends. It shares Host processes with sibling
	// tabs but never shares their enabled/disabled state.
	CapabilityRuntime *agent.MCPCapabilityRuntime
	// WorkspaceRoot is the project root checkpoint restores are confined to ("" =
	// no confinement). Frontends pass the cwd they launched the session in.
	WorkspaceRoot string
	// ResponseLanguage controls final-answer language preference. Empty/auto
	// means no transient injection because the stable language policy follows the
	// current user turn.
	ResponseLanguage string
	// ReasoningLanguage controls visible reasoning language preference. Empty/auto
	// means no transient injection because the stable language policy already
	// follows the conversation language.
	ReasoningLanguage string
	// DisableColdResumePrune skips the stale-tool-result elision that otherwise
	// runs when a session resumes past the provider cache window. Zero value
	// keeps the prune on (the cheaper default).
	DisableColdResumePrune bool
	// Shell is the interpreter user-invoked "!" commands run under, so /shell
	// matches the agent's configured [tools.shell] choice. Zero value = auto.
	Shell sandbox.Shell
	// OnRemember, when set, is invoked with a new allow rule the user chose to
	// persist to disk (e.g. "Bash(go test:*)"). The callback is wired into the
	// permission Gate on EnableInteractiveApproval.
	OnRemember func(rule string) RememberResult
	// OnRememberPlanModeReadOnlyCommand persists a bash command prefix as trusted
	// read-only when the user chooses "always allow" from the plan-mode trust
	// prompt.
	OnRememberPlanModeReadOnlyCommand func(prefix string) PlanModeReadOnlyCommandTrustResult
	// SessionRecoveryMeta lets a frontend attach scope/topic/profile metadata to
	// an automatic recovery branch before it is written.
	SessionRecoveryMeta func(SessionRecoveryRequest) agent.BranchMeta
	// OnSessionRecovered is called after a stale runtime's transcript has been
	// saved as a recovery branch, before the controller commits to that branch.
	OnSessionRecovered func(SessionRecoveryInfo) error
	// ApprovalTimeout bounds how long a tool-approval or ask prompt blocks waiting
	// for a user decision. Zero (default) waits forever — right for an interactive
	// terminal. Bot/headless frontends set a positive value so an unanswered
	// prompt can't wedge the session indefinitely (#4626, #4402).
	ApprovalTimeout time.Duration
	// RuntimeProfile selects capability routing/filtering behavior. Empty keeps
	// the backward-compatible Balanced profile.
	RuntimeProfile capability.Profile
}

// New builds a Controller. A nil Sink is replaced with event.Discard.
func New(opts Options) *Controller {
	sink := opts.Sink
	if nilutil.IsNil(sink) {
		sink = event.Discard
	}
	pluginCtx := opts.PluginCtx
	if pluginCtx == nil {
		pluginCtx = context.Background()
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	runtimeProfile := opts.RuntimeProfile
	if runtimeProfile == "" {
		runtimeProfile = capability.ProfileBalanced
	}
	if opts.Hooks != nil {
		opts.Hooks.SetSessionID(agent.BranchID(opts.SessionPath))
	}
	c := &Controller{
		bgCtx:                             bgCtx,
		bgCancel:                          bgCancel,
		runner:                            opts.Runner,
		executor:                          opts.Executor,
		guardianSess:                      opts.Guardian,
		guardianPath:                      guardian.PathFor(opts.SessionPath),
		sink:                              sink,
		policy:                            opts.Policy,
		subagentGate:                      opts.SubagentGate,
		label:                             opts.Label,
		modelRef:                          opts.ModelRef,
		systemPrompt:                      opts.SystemPrompt,
		sessionDir:                        opts.SessionDir,
		sessionPath:                       opts.SessionPath,
		commands:                          atomic.Pointer[[]command.Command]{},
		skills:                            newSkillSet(opts.Skills, opts.AllSkills, opts.SkillStore, opts.AllSkillStore),
		skillRunner:                       opts.SkillRunner,
		readOnlySkillRunner:               opts.ReadOnlySkillRunner,
		skillProfile:                      opts.SkillProfile,
		hooks:                             opts.Hooks,
		memory:                            newMemoryManager(opts.Memory),
		cleanup:                           opts.Cleanup,
		responseLanguage:                  config.NormalizeLanguage(opts.ResponseLanguage),
		reasoningLanguage:                 config.NormalizeReasoningLanguage(opts.ReasoningLanguage),
		disableColdResumePrune:            opts.DisableColdResumePrune,
		shell:                             opts.Shell,
		onRemember:                        opts.OnRemember,
		onRememberPlanModeReadOnlyCommand: opts.OnRememberPlanModeReadOnlyCommand,
		sessionRecoveryMeta:               opts.SessionRecoveryMeta,
		onSessionRecovered:                opts.OnSessionRecovered,
		balanceURL:                        opts.BalanceURL,
		balanceKey:                        opts.BalanceKey,
		balanceClient:                     opts.BalanceClient,
		jobs:                              opts.Jobs,
		workspaceLease:                    opts.WorkspaceLease,
		mcp:                               newMcpManager(opts.Host, opts.Registry, pluginCtx),
		mcpDefaultCallTimeout:             opts.MCPDefaultCallTimeout,
		mcpConfigureSpec:                  opts.MCPConfigureSpec,
		capabilityRuntime:                 opts.CapabilityRuntime,
		runtimeProfile:                    runtimeProfile,
		workspaceRoot:                     opts.WorkspaceRoot,
		approval:                          newApprovalManager(opts.Policy, ToolApprovalAsk, opts.ApprovalTimeout),
	}
	if strings.TrimSpace(opts.WorkspaceRoot) != "" {
		c.autoResearch = autoresearch.NewStore(opts.WorkspaceRoot)
	}
	// Checkpoints: bind a store to the session and route writer pre-edits into it.
	c.rebindCheckpoints(opts.SessionPath)
	c.setActiveJobSession(opts.SessionPath)
	// Seed sticky prompt_cache_key SessionCacheID when a path is known at construction.
	c.refreshSessionCacheID(opts.SessionPath)
	cmdsInit := opts.Commands
	c.commands.Store(&cmdsInit)
	if c.executor != nil {
		c.wireMutationObserver()
		c.executor.SetMemoryQueue(c)
	}
	// Auto Guard is built into Auto. Ask and YOLO bypass it through the mode
	// provider, so no separate enablement state is needed.
	c.initRecoveryGate(opts.RecoveryReviewer, opts.RecoveryHeadless)
	return c
}

// SetDisplayRecorder installs an optional hook used by frontends that persist a
// shorter user-facing transcript than the fully composed model prompt.
func (c *Controller) SetDisplayRecorder(fn func(content, display string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.displayRecorder = fn
}

func (c *Controller) sessionRecoveredHandler() func(SessionRecoveryInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.onSessionRecovered
}

func (c *Controller) recordDisplay(content, display string) {
	if strings.TrimSpace(display) == "" || content == display {
		return
	}
	c.mu.Lock()
	record := c.displayRecorder
	c.mu.Unlock()
	if record != nil {
		record(content, display)
	}
}

func (c *Controller) recordDisplayForNewUser(startMessages int, display string) {
	if strings.TrimSpace(display) == "" {
		return
	}
	msgs := c.History()
	if startMessages > len(msgs) {
		startMessages = len(msgs)
	}
	for _, m := range msgs[startMessages:] {
		if m.Role == provider.RoleUser {
			c.recordDisplay(m.Content, display)
			return
		}
	}
}

// ckptDir derives a session's checkpoint directory from its file path
// (…/<id>.jsonl → …/<id>.ckpt). Empty path → empty (in-memory checkpoints).
func ckptDir(sessionPath string) string {
	return store.SessionCheckpointDir(sessionPath)
}

// rebindCheckpoints points the store at the (possibly new) session, loading any
// checkpoints already on disk, and resets the turn boundaries. Called on
// construction and whenever the session path changes (NewSession/Resume/SetSessionPath).
// Also re-wires the mutation observer so capture targets the new store.
func (c *Controller) rebindCheckpoints(sessionPath string) {
	c.goals.setStatePath(goalStatePath(sessionPath))
	c.checkpoints.rebind(ckptDir(sessionPath), c.workspaceRoot)
	if c.executor != nil {
		c.wireMutationObserver()
	}
}

// beginCheckpoint opens a checkpoint for the turn about to run, recording the
// current message count as the conversation-rewind boundary. Called at the top of
// runTurn, before the user message is appended.
func (c *Controller) beginCheckpoint(input string) {
	if c.executor == nil {
		return
	}
	atomic.AddInt64(&c.sessionRevision, 1)
	c.checkpoints.beginWithObserver(input, len(c.executor.Session().Messages), c.mutationObserver)
}

// admissionResult classifies what runGuarded did with a turn body.
type admissionResult int

const (
	// turnStarted: admission was open; the turn is running now.
	turnStarted admissionResult = iota
	// turnParked: the body landed inside the finishing window (TurnDone was
	// being delivered) and will start the moment the window closes. From the
	// caller's perspective the turn WILL run — nothing was lost.
	turnParked
	// turnDroppedRunning: a turn is genuinely in flight. Deliberately silent,
	// as before: interactive frontends prevent this with their own
	// steer/queue UX, and internal opportunistic callers (goal-loop
	// continuations, replays) rely on a quiet no-op.
	turnDroppedRunning
	// turnDroppedRotating: the executor session is being swapped out
	// (NewSession/ClearSession). The input's intended session is ambiguous,
	// so it is refused with a user-visible Notice asking to resend rather
	// than silently running against a session the user didn't see.
	turnDroppedRotating
	// turnDroppedClosed: the controller has been closed. Deliberately silent:
	// this controller's transports are being (or have been) torn down and the
	// input's home is the replacement controller the host swaps in — a Notice
	// here would go to a dead surface.
	turnDroppedClosed
)

// runGuarded runs body on a background goroutine under a fresh cancellable
// context, guarding against concurrent turns and emitting a TurnDone event when
// it finishes (Err set on failure; nil also for a user Cancel).
//
// Admission is NOT first-come-first-served across all states — see
// admissionResult. In particular, a body arriving during the finishing window
// is parked, not dropped: TurnDone is emitted inside that window, so every
// caller that reacts to TurnDone by submitting again (a frontend's queued
// auto-send, a bot, a fast Enter) would otherwise race a silent drop. That
// exact loss was observed in CI and reproduced on a clean main-v2 worktree,
// and the desktop composer already carries a workaround gating its auto-send
// on submitDisabled rather than turn_done (Composer.tsx).
func (c *Controller) runGuarded(body func(ctx context.Context) error) admissionResult {
	return c.admitGuardedTurn(body, false)
}

// runGuardedOrPark admits like runGuarded but parks the body while another
// turn is running instead of using the deliberately-silent running drop.
// Reserved for inputs that are the user's own words (the steer fallback):
// the FIFO drain in finishGuardedTurn delivers them the moment the current
// turn finishes.
func (c *Controller) runGuardedOrPark(body func(ctx context.Context) error) admissionResult {
	return c.admitGuardedTurn(body, true)
}

func (c *Controller) admitGuardedTurn(body func(ctx context.Context) error, parkWhileRunning bool) admissionResult {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return turnDroppedClosed
	}
	if c.rotating {
		c.mu.Unlock()
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "input was not accepted: the session is being switched — please resend"})
		return turnDroppedRotating
	}
	if c.running {
		if parkWhileRunning {
			c.parkedTurns = append(c.parkedTurns, body)
			c.mu.Unlock()
			return turnParked
		}
		c.mu.Unlock()
		return turnDroppedRunning
	}
	if c.finishing {
		c.parkedTurns = append(c.parkedTurns, body)
		c.mu.Unlock()
		return turnParked
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.running = true
	c.canceling = false
	c.mu.Unlock()
	c.spawnGuardedTurn(ctx, cancel, body)
	return turnStarted
}

// spawnGuardedTurn launches an admitted turn body plus its autosave companion.
// The caller must already have claimed admission (running=true) under c.mu.
func (c *Controller) spawnGuardedTurn(ctx context.Context, cancel context.CancelFunc, body func(ctx context.Context) error) {
	c.autosaveWG.Add(1)
	go func() {
		defer c.autosaveWG.Done()
		c.autosaveWhileRunning(ctx)
	}()
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				c.finishGuardedTurn(fmt.Errorf("internal error: %v", r))
			}
		}()
		err := body(ctx)
		c.finishGuardedTurn(explainError(err))
	}()
}

// finishGuardedTurn keeps admission closed while TurnDone is delivered. The
// sink fan-out may detach per-turn transports; allowing a replacement turn in
// after running=false but before that fan-out completed let the old completion
// clear or inherit the replacement turn's transport.
//
// When the window closes, the oldest parked turn (if any) is started under the
// SAME critical section that clears finishing: opening the gate first and then
// re-admitting would let an unrelated submit slip in ahead and bounce the
// parked turn back to a drop. Remaining parked turns drain one per
// finishGuardedTurn, preserving FIFO order. Rotation cannot interleave here:
// beginRotation refuses while running or finishing, and the drain flips
// finishing directly into running.
func (c *Controller) finishGuardedTurn(err error) {
	c.memory.clearAutoRemember()
	c.mu.Lock()
	cancelRequested := c.canceling
	c.running = false
	// A live controller keeps admission closed until TurnDone fan-out finishes.
	// Close has already sealed admission permanently, so a late completion must
	// not resurrect a finishing state after teardown.
	c.finishing = !c.closed
	c.cancel = nil
	c.canceling = false
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.finishing = false
		if c.closed || len(c.parkedTurns) == 0 {
			// A closed controller must not start a parked turn against freed
			// resources; close() also cleared the queue, this guards the
			// close-raced-with-delivery ordering.
			c.mu.Unlock()
			return
		}
		next := c.parkedTurns[0]
		c.parkedTurns = c.parkedTurns[1:]
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel = cancel
		c.running = true
		c.canceling = false
		c.mu.Unlock()
		c.spawnGuardedTurn(ctx, cancel, next)
	}()
	done := event.Event{Kind: event.TurnDone, Err: err, Cancelled: cancelRequested, Outcome: turnOutcome(err)}
	var readinessErr *agent.FinalReadinessError
	if errors.As(err, &readinessErr) {
		done.Readiness = &event.FinalReadiness{Attempts: readinessErr.Attempts, Missing: append([]string(nil), readinessErr.Missing...)}
	}
	c.sink.Emit(done)
}

func turnOutcome(err error) string {
	var readinessErr *agent.FinalReadinessError
	if errors.As(err, &readinessErr) {
		return event.TurnOutcomeFinalReadiness
	}
	var pauseErr *agent.RecoveryPauseError
	if errors.As(err, &pauseErr) {
		return event.TurnOutcomeRecoveryPaused
	}
	return ""
}

// SendWithRaw starts a turn with separate model input and raw prompt text.
func (c *Controller) SendWithRaw(input, raw string) {
	c.runGuarded(func(ctx context.Context) error { return c.runGoalLoopWithRaw(ctx, input, raw) })
}

// planApprovalTool is the Tool name on the ApprovalRequest the controller emits
// to gate a proposed plan. Frontends key their plan-approval UI on it (the
// desktop renders a plan card; the chat TUI a plan banner).
const planApprovalTool = "exit_plan_mode"

// SandboxEscapeApprovalTool is the internal Tool name used for one-shot approval
// to rerun a shell command without the OS sandbox after the sandbox failed.
const SandboxEscapeApprovalTool = "sandbox_escape"

// ManagedConfigWriteApprovalTool is the internal Tool name used for per-write
// approval when a file tool targets a Corvus-managed config file outside the
// workspace write roots. It is a fresh human decision: config files control
// providers, sandbox rules, permissions, and MCP servers for future sessions,
// so YOLO/auto approval must never answer it.
const ManagedConfigWriteApprovalTool = "config_write"

// planApprovedMessage is the follow-up turn sent once the user approves a plan —
// the in-context nudge to execute and keep the (already-seeded) task list honest.
const planApprovedMessage = "Plan approved — plan mode is off. Implement the plan now. The ordinary writer fallback is approved for this execution turn; explicit ask/deny rules and forced fresh reviews still apply. Use this serial workflow: 1) mark the first sub-step in_progress with todo_write (this establishes the task list); 2) execute the sub-step; 3) call complete_step with evidence — the host then marks that sub-step completed and moves the next one to in_progress for you. Repeat 2–3 for each remaining sub-step. You don’t need another todo_write to mark steps completed; each complete_step advances the list. Sign off one sub-step at a time — never batch multiple completions."

// runTurn runs one model turn, then applies the plan-approval gate. This is the
// single, frontend-agnostic plan flow: in Plan the model is instructed to
// research and write its plan as a normal answer, while any tool calls still use
// the active Permissions/Sandbox path.
// When the turn ends with a text proposal, the controller asks the user to
// approve (reusing the ApprovalRequest channel both frontends already render);
// on approval it exits plan mode, seeds the task list from the plan, and
// continues straight into execution; on rejection it stays in plan mode so the
// next turn can revise. Plan mode is only ever set interactively, so the headless
// `Run` path (which doesn't call this) never blocks on a prompt.
func (c *Controller) runTurn(ctx context.Context, input string) error {
	return c.runGoalLoopWithRaw(ctx, input, input)
}

// RunTurn executes one foreground turn synchronously through the same lifecycle
// used by interactive frontends: transient memory/background-job
// composition, checkpoints, hooks, and plan approval. It is for transports that
// need a blocking request/response boundary, such as ACP session/prompt.
func (c *Controller) RunTurn(ctx context.Context, input string) error {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	// finishing is part of the gate: TurnDone delivery for the previous turn
	// is still fanning out, and starting a synchronous turn inside that
	// window recreates the completion/transport crosstalk the window exists
	// to prevent (Running() already reports true here). closed seals a torn-
	// down controller. Synchronous callers get an error rather than parking:
	// they hold a request/response boundary open and already handle busy.
	if c.running || c.finishing || c.rotating || c.closed {
		c.mu.Unlock()
		cancel()
		return ErrTurnRunning
	}
	c.cancel = cancel
	c.running = true
	c.canceling = false
	c.mu.Unlock()
	defer event.RecordTurnCompletion(c.sink)

	defer func() {
		c.mu.Lock()
		c.running = false
		c.cancel = nil
		c.canceling = false
		c.mu.Unlock()
		cancel()
	}()
	return c.runTurn(ctx, input)
}

func (c *Controller) runTurnWithRaw(ctx context.Context, input, raw string) error {
	return c.runTurnWithRawDisplay(ctx, input, raw, "")
}

func (c *Controller) runGoalLoopWithRaw(ctx context.Context, input, raw string) error {
	return c.runGoalLoopWithRawDisplay(ctx, input, raw, "")
}

func (c *Controller) runGoalLoopWithRawDisplay(ctx context.Context, input, raw, display string) error {
	return newTurnOrchestrator(c).runGoalLoopWithRawDisplay(ctx, input, raw, display)
}

func (c *Controller) runTurnWithRawDisplay(ctx context.Context, input, raw, display string) error {
	return newTurnOrchestrator(c).runTurnWithRawDisplay(ctx, input, raw, display)
}

func (c *Controller) runSubagentSkillSlash(sk skill.Skill, task, raw, display string) {
	sk = c.skills.prepare(sk)
	c.runGuarded(func(ctx context.Context) error {
		planMode := c.PlanMode()
		runner := c.skillRunner
		if runner == nil {
			return fmt.Errorf("subagent skill runner is unavailable for /%s", sk.Name)
		}
		return newTurnOrchestrator(c).runSubagentSkillGoalLoop(ctx, sk, task, raw, display, runner, planMode)
	})
}

// toolWasCalledLastTurn reports whether the most recent assistant message
// contained any tool calls, indicating the agent made observable progress.
func (c *Controller) toolWasCalledLastTurn() bool {
	msgs := c.History()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == provider.RoleAssistant {
			return len(m.ToolCalls) > 0
		}
		if m.Role == provider.RoleUser {
			return false
		}
	}
	return false
}

func (c *Controller) stopGoal(status string) {
	path, data, ok := c.goals.stop(status, c.goalTodos())
	c.persistGoalState(path, data, ok)
}

// Cancel aborts the in-flight turn. A goroutine blocked awaiting approval
// unblocks via the cancelled context.
func (c *Controller) Cancel() {
	c.mu.Lock()
	cancel := c.cancel
	if cancel != nil {
		c.canceling = true
	}
	c.mu.Unlock()
	if cancel != nil {
		c.approval.clearAll()
		cancel()
		return
	}
	if c.goals.active() {
		c.stopGoal(GoalStatusStopped)
	}
}

// Running reports whether a turn is currently in flight.
func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running || c.finishing
}

// Approve answers a pending ApprovalRequest by ID: allow runs the call, session
// also remembers a grant for the rest of the session so the same approval scope
// is not re-prompted. Unknown/expired IDs are ignored.
func (c *Controller) Approve(id string, allow, session, persist bool) {
	// Recovery cards are strict fresh decisions. Prefer ResolveRecovery so a
	// continue/deny from an old client that only knows Approve still maps onto
	// the recovery state machine (allow=continue, deny=revise without feedback).
	// Session/persist grants are intentionally ignored for recovery.
	//
	// Lookup must use the live waiter table (HasApproval), not Snapshot: pre-
	// normal-execution plan prompts park a waiter without an armed taskRuntime, so
	// they never appear in the persistence snapshot.
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil && gate.HasApproval(id) {
		action := agent.RecoveryActionRevise
		if allow {
			action = agent.RecoveryActionContinue
		}
		_ = c.ResolveRecovery(id, action, "")
		return
	}
	pending := c.approval.resolve(id)
	if pending.reply != nil {
		pending.reply <- approvalReply{allow: allow, session: session, persist: persist} // buffered, never blocks
	}
}

// EnableInteractiveApproval swaps the executor's gate for one that routes
// approval decisions to the frontend via ApprovalRequest events, and wires the
// controller in as the executor's Asker so the `ask` tool can question the user.
// Interactive frontends (chat, desktop) call this; the headless run keeps the
// silent gate and a nil asker from setup.
func (c *Controller) EnableInteractiveApproval() {
	trustGate := planModeReadOnlyTrustApprover{c}
	escapeApprover := sandboxEscapeApprover{c}
	configApprover := managedConfigWriteApprover{c}
	if c.executor != nil {
		c.executor.SetGate(c.newInteractiveGate())
		c.executor.SetPlanModeReadOnlyTrustGate(trustGate)
		c.executor.SetSandboxEscapeApprover(escapeApprover)
		c.executor.SetConfigWriteApprover(configApprover)
		c.executor.SetAsker(c)
	}
	if setter, ok := c.runner.(interface {
		SetPlanModeReadOnlyTrustGate(agent.PlanModeReadOnlyTrustGate)
	}); ok {
		setter.SetPlanModeReadOnlyTrustGate(trustGate)
	}
	if setter, ok := c.runner.(interface {
		SetSandboxEscapeApprover(sandbox.EscapeApprover)
	}); ok {
		setter.SetSandboxEscapeApprover(escapeApprover)
	}
	if setter, ok := c.runner.(interface {
		SetConfigWriteApprover(tool.ConfigWriteApprover)
	}); ok {
		setter.SetConfigWriteApprover(configApprover)
	}
	if setter, ok := c.runner.(interface {
		SetPlannerPlanApprover(agent.PlannerPlanApprover)
	}); ok {
		setter.SetPlannerPlanApprover(plannerPlanApprover{c: c})
	}
	if setter, ok := c.runner.(interface {
		SetPlannerUserDecisionAsker(agent.PlannerUserDecisionAsker)
	}); ok {
		setter.SetPlannerUserDecisionAsker(plannerUserDecisionAsker{c: c})
	}
}

type plannerPlanApprover struct {
	c *Controller
}

func (p plannerPlanApprover) RunWithPlannerApproval(ctx context.Context, plan string, run func(context.Context) error) error {
	c := p.c
	allow, _, err := c.requestApprovalWithReason(ctx, planApprovalTool, "", nil, "Planner requested host approval before execution.")
	if err != nil {
		return err
	}
	if !allow {
		return nil
	}
	todoArgs := c.seedPlanTodos(plan)
	execStart := c.sessionMessageCount()
	c.approval.setPlanAutoApprove(true)
	defer c.approval.setPlanAutoApprove(false)
	if err := run(ctx); err != nil {
		return err
	}
	if todoArgs != "" && !c.hasTodoUpdateSince(execStart) {
		c.completePlanTodos(todoArgs)
	}
	return nil
}

type plannerUserDecisionAsker struct {
	c *Controller
}

func (p plannerUserDecisionAsker) RunWithPlannerUserDecision(ctx context.Context, _ string, question event.AskQuestion, run func(context.Context, string) error) error {
	answers, err := p.c.Ask(ctx, []event.AskQuestion{question})
	if err != nil {
		return err
	}
	answer := plannerUserDecisionAnswer(question, answers)
	if strings.TrimSpace(answer) == "" {
		return nil
	}
	return run(ctx, answer)
}

func plannerUserDecisionAnswer(question event.AskQuestion, answers []event.AskAnswer) string {
	for _, answer := range answers {
		if answer.QuestionID != question.ID {
			continue
		}
		selected := make([]string, 0, len(answer.Selected))
		for _, item := range answer.Selected {
			if s := strings.TrimSpace(item); s != "" {
				selected = append(selected, s)
			}
		}
		return strings.Join(selected, ", ")
	}
	return ""
}

func (c *Controller) newInteractiveGate() *permission.Gate {
	policy := c.policy
	mode := c.approval.mode()
	switch mode {
	case ToolApprovalAuto, ToolApprovalYolo:
		policy.Mode = permission.Allow
	case ToolApprovalDontAsk:
		policy.Mode = permission.Deny
	default:
		policy.Mode = permission.Ask
	}
	// A session allowlist (e.g. --allowed-tools) must never satisfy a tool that
	// requires fresh human approval on every call — memory remember/forget, plan
	// approval, sandbox escape, managed config write. SessionAllow is checked
	// before Ask in Policy.Decide, so leaving those entries in would let
	// `--allowed-tools remember` write memory with no prompt. Strip them so the
	// forced Ask rules below stay authoritative.
	policy.SessionAllow = rulesWithoutFreshHumanApproval(policy.SessionAllow)
	policy.Ask = append(policy.Ask,
		permission.Rule{Tool: memoryRememberTool},
		permission.Rule{Tool: memoryForgetTool},
	)
	var approver permission.Approver = gateApprover{c}
	if mode == ToolApprovalDontAsk {
		approver = denyPermissionApprover{}
	}
	gate := permission.NewGate(policy, approver)
	gate.OnRemember = func(rule string) {
		if c.onRemember != nil {
			_ = c.onRemember(rule)
		}
	}
	return gate
}

func (c *Controller) allowLowRiskRemember(args json.RawMessage) bool {
	mem := c.Memory()
	if mem != nil {
		if assessment := memory.AssessRememberWrite(mem.Store, args); assessment.AutoAllow {
			c.memory.authorizeAutoRemember(args)
			return true
		}
	}
	c.memory.revokeAutoRemember(args)
	return false
}

func (c *Controller) newHeadlessGate(mode string) *freshHumanHeadlessGate {
	gate := BuildHeadlessApprovalGate(c.policy, mode)
	gate.allowLowRiskFreshAction = func(toolName string, args json.RawMessage) bool {
		return toolName == memoryRememberTool && c.allowLowRiskRemember(args)
	}
	return gate
}

type denyPermissionApprover struct{}

func (denyPermissionApprover) Approve(context.Context, string, string, json.RawMessage) (bool, bool, error) {
	return false, false, nil
}

// rulesWithoutFreshHumanApproval drops any session-allow rule that targets a
// tool requiring fresh human approval, so an explicit allowlist cannot bypass
// the always-prompt contract for those tools.
func rulesWithoutFreshHumanApproval(rules []permission.Rule) []permission.Rule {
	if len(rules) == 0 {
		return rules
	}
	filtered := make([]permission.Rule, 0, len(rules))
	for _, r := range rules {
		if RequiresFreshHumanApprovalTool(r.Tool) {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered
}

// ApplyHeadlessApprovalMode configures the executor gate for a non-interactive
// (`corvus run`) session from an explicit --permission-mode. Unlike
// EnableInteractiveApproval it installs no blocking approver, asker, or
// fresh-approval prompt: there is no key loop to answer them, and the default
// infinite approval timeout would wedge the run forever on an Ask rule, the
// `ask` tool, or a sandbox/config approval. Modes map straight onto a headless
// gate, and each preserves the interactive contract as closely as a run with no
// one to prompt allows:
//
//   - auto: auto-approve the writer fallback (Mode=Allow) but PRESERVE explicit
//     ask rules. Interactive auto prompts on those (it never auto-approves them);
//     headless can't prompt, so a would-ask decision fails closed (deny) rather
//     than running silently. Only bypass may run such a command unattended.
//   - yolo/bypassPermissions: skip ordinary approval-gated decisions (nil
//     approver); deny rules and fresh decisions still fail closed.
//   - dontAsk: deny anything that would ask, and deny the writer fallback too.
//
// Deny rules and fresh-human tools (memory, plan, sandbox, config) stay enforced
// by the gate for every mode. The only exception is a controller-assessed,
// create-only project/reference memory; every other memory write remains denied.
func (c *Controller) ApplyHeadlessApprovalMode(mode string) {
	mode = normalizeToolApprovalMode(mode)
	c.approval.setMode(mode)
	if c.subagentGate != nil {
		c.subagentGate.Update(mode)
	}
	if c.executor != nil {
		c.executor.SetGate(c.newHeadlessGate(mode))
	}
}

func (c *Controller) refreshInteractiveGate() {
	if c.executor != nil {
		c.executor.SetGate(c.newInteractiveGate())
	}
}

// Ask implements agent.Asker: it emits an AskRequest and blocks until
// AnswerQuestion(ID, …) answers or ctx is cancelled. promptMu serialises it
// against tool-approval prompts so at most one user prompt is outstanding.
// Unlike tool-approval gates, Ask is NOT bypassed in YOLO mode — the `ask`
// tool exists to get a genuine user decision, and YOLO only auto-approves
// tool calls; it must not answer the user's questions for them.
func (c *Controller) Ask(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	c.approval.promptMu.Lock()
	defer c.approval.promptMu.Unlock()

	id, reply := c.approval.registerAsk(questions)
	c.sink.Emit(event.Event{Kind: event.AskRequest, Ask: event.Ask{ID: id, Questions: questions}})

	waitCtx, cancelWait := c.approval.waitContext(ctx)
	defer cancelWait()

	select {
	case ans := <-reply:
		return ans, nil
	case <-waitCtx.Done():
		c.approval.cancelAsk(id)
		return nil, waitCtx.Err()
	}
}

// AnswerQuestion resolves a pending AskRequest by ID with the user's selections.
// Unknown/expired IDs are ignored.
func (c *Controller) AnswerQuestion(id string, answers []event.AskAnswer) {
	if pending, ok := c.approval.resolveAsk(id); ok {
		// An answer batch with no selections is the explicit "skip and continue
		// chat" path. End the current turn instead of feeding a prose dismissal
		// back to the model and trusting it not to ask again (#6869).
		if !askAnswersHaveSelection(answers) {
			c.mu.Lock()
			activeTurn := c.cancel != nil
			c.mu.Unlock()
			if activeTurn {
				c.Cancel()
				return
			}
		}
		pending.reply <- answers // buffered, never blocks
	}
}

func askAnswersHaveSelection(answers []event.AskAnswer) bool {
	for _, answer := range answers {
		if len(answer.Selected) > 0 {
			return true
		}
	}
	return false
}

// SetPlanMode flips the executor's plan-first workflow flag without touching the
// cache-stable system/tool prefix, and remembers the state so Compose can prepend
// the plan-mode marker to outgoing user turns.
func (c *Controller) SetPlanMode(v bool) {
	c.applyPlanMode(v)
}

func (c *Controller) applyPlanMode(v bool) {
	c.mu.Lock()
	c.planMode = v
	c.mu.Unlock()
	if setter, ok := c.runner.(interface{ SetPlanMode(bool) }); ok {
		setter.SetPlanMode(v)
		return
	}
	if c.executor != nil {
		c.executor.SetPlanMode(v)
	}
}

// SetResponseLanguage updates the final-answer language preference for
// subsequent turns.
func (c *Controller) SetResponseLanguage(lang string) {
	mode := config.NormalizeLanguage(lang)
	c.mu.Lock()
	c.responseLanguage = mode
	c.mu.Unlock()
	if setter, ok := c.runner.(interface{ SetResponseLanguage(string) }); ok {
		setter.SetResponseLanguage(mode)
	} else if c.executor != nil {
		c.executor.SetResponseLanguage(mode)
	}
}

// SetReasoningLanguage updates the visible reasoning language preference for
// subsequent turns.
func (c *Controller) SetReasoningLanguage(lang string) {
	mode := config.NormalizeReasoningLanguage(lang)
	c.mu.Lock()
	c.reasoningLanguage = mode
	c.mu.Unlock()
	if setter, ok := c.runner.(interface{ SetReasoningLanguage(string) }); ok {
		setter.SetReasoningLanguage(mode)
	} else if c.executor != nil {
		c.executor.SetReasoningLanguage(mode)
	}
}

// PlanMode reports whether outgoing turns currently receive the plan-mode
// marker.
func (c *Controller) PlanMode() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.planMode
}

// GoalStrict enables or disables strict goal mode. In strict mode the agent
// cannot override an incomplete-todo intercept — it must actually finish or
// update all items before [goal:complete] is accepted.
func (c *Controller) GoalStrict(strict bool) {
	path, data, ok := c.goals.setStrict(strict, c.goalTodos())
	c.persistGoalState(path, data, ok)
}

// SetGoal stores a session-scoped active goal. Compose injects it into outgoing
// user turns, not the system prompt or tool schema, so it does not disturb the
// cache-stable prefix.
func (c *Controller) SetGoal(goal string) {
	c.SetGoalWithResearchMode(goal, GoalResearchAuto)
}

// SetGoalDurable updates the Goal only when its sidecar can be replaced
// atomically. Remote Profile transactions persist autoResearchCreateToken
// before calling this method so crash recovery owns any newly-created task.
func (c *Controller) SetGoalDurable(goal, autoResearchCreateToken string) error {
	snapshot := c.goals.capture()
	setup := c.prepareAutoResearchTask(goal, GoalResearchAuto, autoResearchCreateToken)
	path, data, persist := c.goals.set(goal, GoalResearchAuto, setup.taskID, c.goalTodos())
	if setup.blockReason != "" {
		path, data, persist = c.goals.stop(GoalStatusBlocked, c.goalTodos())
	}
	if persist {
		if err := c.goals.writeStateErr(path, data); err != nil {
			c.goals.restore(snapshot)
			if setup.created && c.autoResearch != nil {
				if removeErr := c.autoResearch.RemoveTask(setup.taskID, setup.createToken); removeErr != nil {
					slog.Warn("controller: rollback autoresearch task", "task_id", setup.taskID, "err", removeErr)
				}
			}
			return err
		}
	}
	if setup.notice != "" {
		c.notice(setup.notice)
	}
	if setup.blockReason != "" {
		c.notice("autoresearch resume failed: " + setup.blockReason)
	}
	return nil
}

func (c *Controller) SetGoalWithResearchMode(goal string, researchMode GoalResearchMode) {
	setup := c.prepareAutoResearchTask(goal, researchMode, "")
	if setup.notice != "" {
		c.notice(setup.notice)
	}
	path, data, ok := c.goals.set(goal, researchMode, setup.taskID, c.goalTodos())
	c.persistGoalState(path, data, ok)
	if setup.blockReason != "" {
		path, data, ok := c.goals.stop(GoalStatusBlocked, c.goalTodos())
		c.persistGoalState(path, data, ok)
		c.notice("autoresearch resume failed: " + setup.blockReason)
	}
}

// ResumeGoal re-enters a recoverable blocked/stopped Goal without resetting its
// delivery evidence scope or AutoResearch identity.
func (c *Controller) ResumeGoal() bool {
	path, data, persist, resumed := c.goals.resume(c.goalTodos())
	if !resumed {
		return false
	}
	c.persistGoalState(path, data, persist)
	if c.executor != nil {
		c.executor.RestoreDeliveryCheckpoint(c.goals.deliveryState())
	}
	return true
}

func (c *Controller) persistGoalDeliveryCheckpoint() {
	if c.executor == nil {
		return
	}
	checkpoint := c.executor.DeliveryCheckpoint()
	path, data, ok := c.goals.setDeliveryCheckpoint(checkpoint, c.goalTodos())
	c.persistGoalState(path, data, ok)
}

func (c *Controller) ClearGoal() {
	c.SetGoal("")
}

func (c *Controller) Goal() string {
	return c.goals.goalText()
}

func (c *Controller) GoalStatus() string {
	return c.goals.statusForDisplay()
}

// Compact runs one compaction pass on the executor's session on demand.
// instructions is optional `/compact <focus>` guidance steering what to keep.
func (c *Controller) Compact(ctx context.Context, instructions string) error {
	if c.executor == nil {
		return nil
	}
	// The run loop is the only sanctioned writer of the live session during a
	// turn; a manual compact would rewrite the log underneath it. The rotation
	// gate (not a bare Running() check) also blocks a turn from starting while
	// the compaction rewrites the session — see beginRotation.
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return fmt.Errorf("cannot compact while a turn is running")
		}
		return err
	}
	defer c.endRotation()
	return c.executor.CompactNow(ctx, instructions)
}

// RewindScope selects what a Rewind restores.
type RewindScope int

const (
	RewindCode         RewindScope = iota // files only
	RewindConversation                    // message log only
	RewindBoth                            // both
)

// Checkpoints lists the session's rewind points (one per user turn), oldest first.
//
// Each Meta.Prompt is reduced to what the user typed. A checkpoint opens with
// the composed turn, so the stored prompt can carry the plan-mode marker and
// transient blocks; every consumer of this list is a label (the rewind picker,
// the desktop change list, the workbench projection) and the picker also
// restores the prompt into the composer, so composed text must not reach them.
// Stripping on read rather than only on write keeps checkpoints already on disk
// readable — they were recorded composed.
func (c *Controller) Checkpoints() []checkpoint.Meta {
	metas := c.checkpoints.list()
	for i := range metas {
		metas[i].Prompt = StripComposePrefixes(metas[i].Prompt)
	}
	return metas
}

func (c *Controller) CheckpointFileState(path string) (checkpoint.FileState, bool) {
	return c.checkpoints.fileState(path)
}

func (c *Controller) CheckpointTurnsByMessageIndex() map[int]int {
	return c.checkpoints.turnsByMessageIndex()
}

// rewindFail emits the error as a Warn notice (so a frontend that swallows the
// returned error — e.g. the desktop bridge's .catch — still shows the user why
// the rewind did nothing) and returns it.
func (c *Controller) rewindFail(err error) error {
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: err.Error()})
	return err
}

// Commands returns the loaded custom slash commands.
func (c *Controller) Commands() []command.Command {
	if p := c.commands.Load(); p != nil {
		return *p
	}
	return nil
}

// ReloadCommands rescans all command directories and hot-swaps the slash_command
// tool and the internal command slice — no MCP restart, no hook rerun.
func (c *Controller) ReloadCommands(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	cmds, loadErr := command.LoadRoots(config.CommandRootsForRoot(c.workspaceRoot)...)
	cmdSkills := c.SlashSkills()

	entries := make([]command.SlashEntry, 0, len(cmdSkills)+len(cmds))
	for _, sk := range cmdSkills {
		sk := sk
		entries = append(entries, command.SlashEntry{
			Name:        sk.SlashName(),
			Description: sk.Description,
			Render:      func(args []string) string { return c.skills.render(sk, strings.Join(args, " ")) },
		})
	}
	for _, cmd := range cmds {
		if cmd.Hidden {
			continue
		}
		cmd := cmd
		entries = append(entries, command.SlashEntry{
			Name:        cmd.Name,
			Description: cmd.Description,
			ArgHint:     cmd.ArgHint,
			Render:      func(args []string) string { return cmd.Render(args) },
		})
	}
	c.mcp.registerTool(command.NewSlashCommandTool(entries))
	cmdSlice := cmds
	c.commands.Store(&cmdSlice)
	return loadErr
}

// Skills returns the discoverable skills (for the slash menu and `/skills`).
// When a live Store is available, scan it on demand so skills installed during
// this session appear without rewriting the cache-stable system prompt.
// Executor returns the underlying agent when present (nil for pure runners).
func (c *Controller) Executor() *agent.Agent {
	if c == nil {
		return nil
	}
	return c.executor
}

func (c *Controller) Skills() []skill.Skill {
	return c.skills.list()
}

// SlashSkills returns the user-visible skill directory. Plugin skills use
// package-qualified names while Skills keeps bare model/run_skill identifiers.
func (c *Controller) SlashSkills() []skill.Skill {
	return c.skills.slashList()
}

// AllSkills returns every discoverable skill, including disabled ones, for
// management surfaces that need to re-enable a hidden skill.
func (c *Controller) AllSkills() []skill.Skill {
	return c.skills.listAll()
}

// DisabledSkills returns all discoverable skills that are disabled in config.
func (c *Controller) DisabledSkills() []skill.Skill {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	var out []skill.Skill
	for _, sk := range c.AllSkills() {
		if cfg.IsSkillDisabled(sk.Name) {
			out = append(out, sk)
		}
	}
	return out
}

// SkillEnabled reports whether a discoverable skill is enabled.
func (c *Controller) SkillEnabled(name string) bool {
	cfg, err := config.Load()
	if err != nil {
		return true
	}
	return !cfg.IsSkillDisabled(name)
}

// SetSkillEnabled persists a skill enable/disable preference. The caller should
// rebuild the controller for the prompt/tool registry to reflect it immediately.
func (c *Controller) SetSkillEnabled(name string, enabled bool) error {
	found := false
	for _, sk := range c.AllSkills() {
		if config.SkillNameKey(sk.Name) == config.SkillNameKey(name) {
			name = sk.Name
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown skill: %s", name)
	}
	// Serialize the load-modify-save against other in-process user-config
	// editors so concurrent writers (bot mapping persistence, desktop
	// settings) don't drop this toggle or lose their own fields.
	unlock := config.LockUserConfigEdits()
	defer unlock()
	cfg := config.LoadForEdit(config.UserConfigPath())
	if err := cfg.SetSkillEnabled(name, enabled); err != nil {
		return err
	}
	return cfg.SaveTo(config.UserConfigPath())
}

// CreateSkill writes a new skill file at the given scope and returns its
// path. Skills()/AllSkills()/RunSkill() read the live store on demand, so the
// new skill is usable (by name) immediately with no rebuild; the caller
// should still rebuild the controller for the pinned Skills index and tool
// registry to reflect it on the model's next turn, mirroring how
// SetSkillEnabled's callers already rebuild after a config change.
func (c *Controller) CreateSkill(name string, scope skill.Scope, content string) (string, error) {
	w := c.skills.writer()
	if w == nil {
		return "", fmt.Errorf("no writable skill store in this session")
	}
	return w.CreateWithContent(name, scope, content)
}

// UpdateSkill overwrites an existing user-authored skill file in place. See
// skill.Store.UpdateContent for the builtin-refusal and scope-match rules.
func (c *Controller) UpdateSkill(name string, scope skill.Scope, content string) error {
	w := c.skills.writer()
	if w == nil {
		return fmt.Errorf("no writable skill store in this session")
	}
	return w.UpdateContent(name, scope, content)
}

// DeleteSkill removes a user-authored skill file at the given scope. See
// skill.Store.Delete for the builtin-refusal and scope-match rules.
func (c *Controller) DeleteSkill(name string, scope skill.Scope) error {
	w := c.skills.writer()
	if w == nil {
		return fmt.Errorf("no writable skill store in this session")
	}
	return w.Delete(name, scope)
}

// HookRunner returns the session hook runner so the TUI can list active hooks.
func (c *Controller) HookRunner() *hook.Runner { return c.hooks }

// Label returns the human-readable model label, e.g. "deepseek-flash".
func (c *Controller) Label() string { return c.label }

// ModelRef returns the canonical provider/model reference for the session.
func (c *Controller) ModelRef() string { return c.modelRef }

// WorkspaceRoot returns the workspace root for this controller's session
// (the directory that file-writers and @-references are scoped to).
// Empty means no scoping is in effect.
func (c *Controller) WorkspaceRoot() string { return c.workspaceRoot }

func (c *Controller) imageInputEnabled() bool {
	ref := c.modelRef
	cfg, err := config.LoadForRoot(c.workspaceRoot)
	if err == nil && ref == "" {
		ref = cfg.DefaultModel
	}
	if err != nil || ref == "" {
		return false
	}
	entry, ok := cfg.ResolveModel(ref)
	return ok && config.EffectiveVision(entry)
}

// SessionAuthorizations snapshots this controller's same-session tool
// grants ("Allow for this session") and Plan-mode read-only command trust,
// for carrying into a replacement controller across a rebuild — see
// RestoreSessionAuthorizations.
func (c *Controller) SessionAuthorizations() SessionAuthorizations {
	return c.approval.snapshotSessionAuthorizations()
}

// RestoreSessionAuthorizations re-applies session authorizations captured
// from a prior controller in the same session (see SessionAuthorizations). A
// model/effort/profile switch rebuilds the controller, and without this the
// replacement forgets every grant the user already made this session.
func (c *Controller) RestoreSessionAuthorizations(auth SessionAuthorizations) {
	c.approval.restoreSessionAuthorizations(auth)
}

// Jobs returns the still-running background jobs for the status bar (nil when
// background jobs are disabled).
func (c *Controller) Jobs() []jobs.View {
	if c.jobs == nil {
		return nil
	}
	return c.jobs.RunningForSession(c.parentSessionID())
}

// SetToolApprovalMode changes the runtime approval posture for permission-gated
// tools. It does not answer business asks or plan approval. Sub-agents (task,
// writer-capable skill sub-agents, the planner) have no UI to prompt through,
// so this also pushes the mode to the shared headless gate they read from —
// without it, a mode switch (Shift+Tab) would only rebuild the parent
// executor's gate and leave sub-agents pinned to whatever mode was active
// when the session booted.
func (c *Controller) SetToolApprovalMode(mode string) {
	c.ApplyToolApprovalMode(mode)
}

// ApplyToolApprovalMode is SetToolApprovalMode reporting which pending
// approval prompt ids the new posture auto-allowed. Prompts NOT in the
// returned set are still pending here — fresh user decisions (plan, memory,
// sandbox escape) never drain, and auto keeps approvals an allow policy would
// not cover — so a frontend must keep showing them instead of assuming the
// posture switch resolved everything (#6432).
func (c *Controller) ApplyToolApprovalMode(mode string) []string {
	mode = normalizeToolApprovalMode(mode)
	// Capture mode-change recovery dismissals before approval drain so a
	// same-value hydrate/reconcile never rotates Episode state, while a real
	// Auto↔Yolo/Ask switch clears temporary failure/reviewer locks and waiters
	// without auto-approving the original mutation.
	var recoveryDismissed []string
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		if ctrl, ok := any(gate).(agent.RecoveryEpisodeControl); ok {
			// Do not hold controller/approval locks while rotating the gate.
			recoveryDismissed = ctrl.OnModeChange(mode)
		}
	}
	pending := c.approval.setMode(mode)
	if c.subagentGate != nil {
		c.subagentGate.Update(mode)
	}
	c.refreshInteractiveGate()
	// Clear recovery cards dismissed by the mode switch outside the gate lock.
	for _, id := range recoveryDismissed {
		p := c.approval.resolve(id)
		if p.reply != nil {
			// Do not approve the pending mutation; signal cancel/deny so legacy
			// paths drop the card.
			select {
			case p.reply <- approvalReply{allow: false}:
			default:
			}
		}
	}
	drained := make([]string, 0, len(pending))
	for _, p := range pending {
		p.reply <- approvalReply{allow: true}
		drained = append(drained, p.id)
	}
	return drained
}

func (c *Controller) ToolApprovalMode() string {
	return c.approval.mode()
}

// SetAutoApproveTools turns YOLO tool auto-approval on or off for the session:
// while on, every tool approval request is auto-allowed (writers and bash run
// without asking). Ask requests and plan approval still reach the user. Deny
// rules still block. Runtime-only — never written to config.
func (c *Controller) SetAutoApproveTools(on bool) {
	if on {
		c.SetToolApprovalMode(ToolApprovalYolo)
		return
	}
	c.SetToolApprovalMode(ToolApprovalAsk)
}

// AutoApproveTools reports whether YOLO tool auto-approval is on,
// for status indicators and mode persistence.
func (c *Controller) AutoApproveTools() bool {
	return c.ToolApprovalMode() == ToolApprovalYolo
}

// QuickAdd appends a one-line note to the doc-memory file for scope (project
// CORVUS.md by default) — the write side of "#<note>". Returns the file written.
func (c *Controller) QuickAdd(scope memory.Scope, note string) (string, error) {
	return c.memory.quickAdd(scope, note)
}

// SaveMemory writes an active auto-memory fact and refreshes the in-session
// snapshot. It is the explicit user-confirmed counterpart to the model-owned
// remember tool, used by management surfaces that preview a candidate first.
func (c *Controller) SaveMemory(m memory.Memory) (string, error) {
	return c.memory.saveMemory(m)
}

// ForgetMemory removes a saved auto-memory by name — the panel/TUI forget action,
// the manual counterpart to the model's `forget` tool.
func (c *Controller) ForgetMemory(name string) error {
	return c.memory.forget(name)
}

// QueueMemory implements memory.Queue: when the model runs the remember/forget
// tool, the tool calls this with a note that rides the next turn so the change
// applies this session without touching the cache-stable prefix. It also
// refreshes the snapshot a memory panel reads.
func (c *Controller) QueueMemory(note string) {
	c.memory.queue(note)
}

// ClaimAutoMemoryWrite consumes the one-shot create-only authorization issued
// by gateApprover for a low-risk project fact.
func (c *Controller) ClaimAutoMemoryWrite(args json.RawMessage) bool {
	return c.memory.claimAutoRemember(args)
}

func (c *Controller) MemoryRevisions(ref string) []memory.Memory {
	return c.memory.revisions(ref)
}

// RestoreMemory restores an older active-memory revision as a new audited
// revision and applies it to the next user turn.
func (c *Controller) RestoreMemory(ref string, revision int) (memory.Memory, error) {
	return c.memory.restore(ref, revision)
}

// RestoreArchivedMemory recovers an archived fact as a new audited revision and
// applies it to the next user turn.
func (c *Controller) RestoreArchivedMemory(archivePath string) (memory.Memory, error) {
	return c.memory.restoreArchived(archivePath)
}

// Memory returns the loaded memory snapshot (nil when memory is disabled), for
// frontends that surface a memory panel or the /memory command. The returned
// *Set is immutable — mutations go through QuickAdd / SaveDoc.
func (c *Controller) Memory() *memory.Set {
	return c.memory.current()
}

// gateApprover adapts the Controller to permission.Approver. It is distinct
// from the public Approve command (different signature, different direction).
type gateApprover struct{ c *Controller }

const dynamicBashApprovalReason = "This command uses nested or indirect shell execution. Auto and broad allow rules cannot verify the inner command; approve this exact command or use YOLO."

func (g gateApprover) Approve(ctx context.Context, tool, subject string, args json.RawMessage) (bool, bool, error) {
	allow, remember, _, err := g.ApproveWithReason(ctx, tool, subject, args)
	return allow, remember, err
}

func (g gateApprover) ApproveWithReason(ctx context.Context, tool, subject string, args json.RawMessage) (bool, bool, string, error) {
	return g.approveWithPolicyReason(ctx, tool, subject, args, "")
}

func (g gateApprover) ApproveWithPolicyReason(ctx context.Context, tool, subject string, args json.RawMessage, policyReason string) (bool, bool, string, error) {
	return g.approveWithPolicyReason(ctx, tool, subject, args, policyReason)
}

func combineApprovalReasons(reasons ...string) string {
	var kept []string
	for _, reason := range reasons {
		if reason = strings.TrimSpace(reason); reason != "" {
			kept = append(kept, reason)
		}
	}
	return strings.Join(kept, "\n")
}

func (g gateApprover) approveWithPolicyReason(ctx context.Context, tool, subject string, args json.RawMessage, policyReason string) (bool, bool, string, error) {
	if tool == memoryRememberTool && g.c.allowLowRiskRemember(args) {
		return true, false, "", nil
	}
	subject = approvalDisplaySubject(tool, subject, args)
	requireHuman := strings.EqualFold(tool, "bash") && permission.BashSubjectRequiresExplicitApproval(subject)
	// Check pre-approval first, before any prompt or Guardian review. Dynamic
	// Bash accepts only YOLO or an exact session grant here; ordinary calls also
	// accept the just-approved-plan window. Deny rules already bit at the policy
	// level before this point.
	if requireHuman && g.c.approval.preApprovedForRequiredHuman(tool, subject) {
		return true, false, "", nil
	}
	if !requireHuman && g.c.approval.preApproved(tool, subject, args) {
		return true, false, "", nil
	}
	if g.c.guardianSess != nil && !requireHuman {
		allow, reason, reviewErr := g.c.guardianSess.Review(ctx, tool, args, g.c.executor.Session())
		if reviewErr != nil {
			return false, false, "", reviewErr
		}
		if allow && !requiresFreshApprovalTool(tool) {
			return true, false, "", nil
		}
		reason = combineApprovalReasons(policyReason, reason)
		humanAllow, remember, err := g.c.requestApprovalWithReason(ctx, tool, subject, args, reason)
		if err != nil {
			return false, false, reason, err
		}
		if !humanAllow {
			return false, false, reason, nil
		}
		return true, remember, "", nil
	}
	if requireHuman {
		reason := combineApprovalReasons(policyReason, dynamicBashApprovalReason)
		allow, remember, err := g.c.requestApprovalWithReasonOptions(ctx, tool, subject, args, reason, approvalDecisionOptions{requireHuman: true})
		return allow, remember, "", err
	}
	allow, remember, err := g.c.requestApprovalWithReason(ctx, tool, subject, args, policyReason)
	return allow, remember, "", err
}

type planModeReadOnlyTrustApprover struct{ c *Controller }

type sandboxEscapeApprover struct{ c *Controller }

func (s sandboxEscapeApprover) ApproveSandboxEscape(ctx context.Context, req sandbox.EscapeRequest) (bool, string, error) {
	subject := sandboxEscapeApprovalSubject(req.Command)
	reason := sandboxEscapeApprovalReason(req.Reason)
	reply, err := s.c.requestFreshApprovalDecision(ctx, SandboxEscapeApprovalTool, subject, req.Args, reason)
	if err != nil {
		return false, "approval aborted", err
	}
	if !reply.allow {
		return false, i18n.M.SandboxEscapeDeclined, nil
	}
	if reply.session {
		s.c.approval.grantSession(SandboxEscapeApprovalTool, subject)
	}
	return true, "", nil
}

func (s sandboxEscapeApprover) SandboxEscapeSessionAllowed(_ context.Context, req sandbox.EscapeRequest) bool {
	return s.c.approval.preApprovedForDecision(SandboxEscapeApprovalTool, sandboxEscapeApprovalSubject(req.Command), nil, true)
}

func sandboxEscapeApprovalSubject(command string) string {
	subject := strings.TrimSpace(command)
	if subject == "" {
		return i18n.M.SandboxEscapeSubjectFallback
	}
	return i18n.M.SandboxEscapeSubjectPrefix + subject
}

func sandboxEscapeApprovalReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return i18n.M.SandboxEscapeRuntimeReason
	}
	return reason
}

// managedConfigWriteApprover routes a file tool's Corvus-managed config write
// through the fresh-human approval prompt (see ManagedConfigWriteApprovalTool).
// A session grant is tool-wide (mirroring sandbox_escape): one "allow for this
// session" covers the rest of the repair flow across the handful of managed
// config files without re-prompting on every incremental edit.
type managedConfigWriteApprover struct{ c *Controller }

func (m managedConfigWriteApprover) ApproveManagedConfigWrite(ctx context.Context, req tool.ConfigWriteRequest) (bool, string, error) {
	subject := managedConfigWriteApprovalSubject(req.Path)
	args, _ := json.Marshal(map[string]string{"path": req.Path})
	reply, err := m.c.requestFreshApprovalDecision(ctx, ManagedConfigWriteApprovalTool, subject, args, i18n.M.ConfigWriteReason)
	if err != nil {
		return false, "approval aborted", err
	}
	if !reply.allow {
		return false, i18n.M.ConfigWriteDeclined, nil
	}
	if reply.session {
		m.c.approval.grantSession(ManagedConfigWriteApprovalTool, subject)
	}
	return true, "", nil
}

func (m managedConfigWriteApprover) ManagedConfigWriteSessionAllowed(_ context.Context, req tool.ConfigWriteRequest) bool {
	return m.c.approval.preApprovedForDecision(ManagedConfigWriteApprovalTool, managedConfigWriteApprovalSubject(req.Path), nil, true)
}

func managedConfigWriteApprovalSubject(path string) string {
	return i18n.M.ConfigWriteSubjectPrefix + strings.TrimSpace(path)
}

func (p planModeReadOnlyTrustApprover) CheckPlanModeReadOnlyTrust(ctx context.Context, req agent.PlanModeReadOnlyTrustRequest) (bool, string, error) {
	prefix := normalizePlanModeReadOnlyCommandPrefix(req.Prefix)
	if prefix == "" {
		return false, "missing plan-mode read-only command prefix", nil
	}
	return p.checkBashReadOnlyCommandTrust(ctx, req, prefix)
}

func (p planModeReadOnlyTrustApprover) checkBashReadOnlyCommandTrust(ctx context.Context, req agent.PlanModeReadOnlyTrustRequest, prefix string) (bool, string, error) {
	if p.c.approval.planModeReadOnlyCommandTrusted(prefix) {
		return true, "", nil
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = strings.TrimSpace(string(req.Args))
	}
	subject := fmt.Sprintf(i18n.M.PlanModeBashTrustSubjectFmt, prefix, command)
	reason := i18n.M.PlanModeBashTrustReason
	reply, err := p.c.requestFreshApprovalDecision(ctx, agent.PlanModeReadOnlyCommandApprovalTool, subject, req.Args, reason)
	if err != nil {
		return false, "approval aborted", err
	}
	if !reply.allow {
		return false, i18n.M.PlanModeBashTrustDeclined, nil
	}
	if reply.session {
		p.c.approval.grantPlanModeReadOnlyCommand(prefix)
	}
	if reply.persist && p.c.onRememberPlanModeReadOnlyCommand != nil {
		p.c.emitPlanModeReadOnlyCommandTrustResult(p.c.onRememberPlanModeReadOnlyCommand(prefix))
		p.c.approval.grantPlanModeReadOnlyCommand(prefix)
	}
	return true, "", nil
}

func approvalDisplaySubject(tool, subject string, args json.RawMessage) string {
	switch tool {
	case memoryRememberTool:
		return rememberApprovalSubject(subject, args)
	case memoryForgetTool:
		return forgetApprovalSubject(subject, args)
	case "move_file":
		return moveApprovalSubject(subject, args)
	default:
		return subject
	}
}

func moveApprovalSubject(fallback string, args json.RawMessage) string {
	if len(args) == 0 {
		return fallback
	}
	var in struct {
		SourcePath      string `json:"source_path"`
		DestinationPath string `json:"destination_path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fallback
	}
	if in.SourcePath == "" || in.DestinationPath == "" {
		return fallback
	}
	return in.SourcePath + " -> " + in.DestinationPath
}

func rememberApprovalSubject(fallback string, args json.RawMessage) string {
	if len(args) == 0 {
		return fallback
	}
	var in struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Type        string `json:"type"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fallback
	}
	name := approvalCompactText(firstNonEmpty(in.Name, in.Title))
	desc := approvalTruncate(approvalCompactText(in.Description), 180)
	body := approvalTruncate(approvalCompactText(in.Body), 240)
	typ := string(memory.NormalizeType(in.Type))

	var b strings.Builder
	b.WriteString(i18n.M.MemoryApprovalSaveUpdate)
	baseLen := b.Len()
	if name != "" {
		fmt.Fprintf(&b, " %q", name)
	}
	if typ != "" {
		fmt.Fprintf(&b, " [%s]", typ)
	}
	if desc != "" {
		b.WriteString(": ")
		b.WriteString(desc)
	}
	if body != "" {
		if desc == "" {
			b.WriteString(": ")
		} else {
			b.WriteString(" | ")
		}
		b.WriteString(i18n.M.MemoryApprovalBodyLabel)
		b.WriteString(": ")
		b.WriteString(body)
	}
	if b.Len() == baseLen && fallback != "" {
		return fallback
	}
	return b.String()
}

func forgetApprovalSubject(fallback string, args json.RawMessage) string {
	if len(args) == 0 {
		return fallback
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fallback
	}
	name := approvalCompactText(in.Name)
	if name == "" {
		return fallback
	}
	return fmt.Sprintf(i18n.M.MemoryApprovalArchiveFmt, name)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func approvalCompactText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func approvalTruncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// parseRewind parses the arguments after "/rewind". The user may provide:
//
//	/rewind              → latest checkpoint, both
//	/rewind <turn>       → that turn, both
//	/rewind <turn> <scope> → that turn, code|conversation|both
//
// If no turn is given, the latest checkpoint is used. If no scope is given, Both is assumed.
func parseRewind(args string, cps []checkpoint.Meta) (int, RewindScope, error) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		if len(cps) == 0 {
			return 0, RewindBoth, fmt.Errorf("no checkpoints available")
		}
		return cps[len(cps)-1].Turn, RewindBoth, nil
	}
	turn, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, RewindBoth, fmt.Errorf("invalid turn: %w", err)
	}
	scope := RewindBoth
	if len(fields) >= 2 {
		switch strings.ToLower(fields[1]) {
		case "code":
			scope = RewindCode
		case "conversation":
			scope = RewindConversation
		case "both":
			scope = RewindBoth
		default:
			return 0, RewindBoth, fmt.Errorf("unknown scope %q", fields[1])
		}
	}
	return turn, scope, nil
}

// requestApproval emits an ApprovalRequest and blocks until Approve(ID, …)
// answers or ctx is cancelled. A prior session grant (or a bypass posture) for
// the same approval scope short-circuits. The approvalManager's promptMu
// serialises outstanding prompts; this method keeps the I/O (events, hooks,
// remember) that the manager deliberately stays out of.
func (c *Controller) requestApproval(ctx context.Context, tool, subject string, args json.RawMessage) (bool, bool, error) {
	return c.requestApprovalWithReason(ctx, tool, subject, args, "")
}

func (c *Controller) requestApprovalWithReason(ctx context.Context, tool, subject string, args json.RawMessage, reason string) (bool, bool, error) {
	return c.requestApprovalWithReasonOptions(ctx, tool, subject, args, reason, approvalDecisionOptions{})
}

func (c *Controller) requestApprovalWithReasonOptions(ctx context.Context, tool, subject string, args json.RawMessage, reason string, opts approvalDecisionOptions) (bool, bool, error) {
	r, err := c.requestApprovalDecisionWithOptions(ctx, tool, subject, args, reason, opts)
	if err != nil {
		return false, false, err
	}
	// Plan approvals are one-shot — never persist a session grant for them, or
	// every future plan would auto-approve.
	if r.allow && r.session && !requiresFreshApprovalTool(tool) {
		c.approval.grantSession(tool, subject)
	}
	if r.allow && r.persist && !requiresFreshApprovalTool(tool) && c.onRemember != nil {
		c.emitRememberResult(c.onRemember(permission.RememberRuleForScope(tool, subject)))
	}
	return r.allow, false, nil
}

func (c *Controller) requestFreshApprovalDecision(ctx context.Context, tool, subject string, args json.RawMessage, reason string) (approvalReply, error) {
	return c.requestApprovalDecisionWithOptions(ctx, tool, subject, args, reason, approvalDecisionOptions{fresh: true})
}

type approvalDecisionOptions struct {
	// fresh marks a user trust/business decision rather than an ordinary tool
	// permission. It may reuse an explicit session grant, but YOLO/auto approval
	// must not answer or drain the prompt.
	fresh bool
	// requireHuman marks an ordinary tool approval that Auto, an approved-plan
	// window, Guardian, or an allowing hook must not answer. Unlike fresh it
	// retains the ordinary four-choice UI and YOLO remains an explicit bypass.
	requireHuman bool
}

func (c *Controller) requestApprovalDecisionWithOptions(ctx context.Context, tool, subject string, args json.RawMessage, reason string, opts approvalDecisionOptions) (approvalReply, error) {
	// YOLO/full access and the just-approved-plan execution window auto-allow
	// approval-gated tools without prompting. Plan approval is a user decision,
	// not a tool permission, so it deliberately stays interactive.
	if c.approval.preApprovedForDecisionOptions(tool, subject, args, opts.fresh, opts.requireHuman) {
		return approvalReply{allow: true}, nil
	}

	c.approval.promptMu.Lock()
	defer c.approval.promptMu.Unlock()

	// Re-check: a session grant may have landed while we queued behind another
	// prompt for the same subject.
	if c.approval.preApprovedForDecisionOptions(tool, subject, args, opts.fresh, opts.requireHuman) {
		return approvalReply{allow: true}, nil
	}

	// Claude's PermissionRequest contract answers the dialog on the plugin's
	// behalf (auto-allow/auto-deny) instead of merely observing it, so a
	// decision here must preempt the prompt rather than just notify — this
	// runs synchronously and before the dialog is shown. Native Corvus
	// PermissionRequest hooks stay advisory-only (see claudePermissionBlocking).
	//
	// A hook's auto-allow must never stand in for a human-required decision:
	// sandbox escapes, Corvus config writes, memory remember/forget, and
	// plan approval (RequiresFreshHumanApprovalTool) are deliberately excluded
	// from YOLO/auto-approval and Guardian too, so a broadly-matched plugin
	// hook returning "allow" can't silently rubber-stamp them. A deny still
	// applies universally — refusing is always safe to honor automatically.
	if hookSubject, hookArgs, ok := permissionRequestHookPayload(tool, subject, args); ok {
		if decision, _ := c.hooks.PermissionRequest(ctx, tool, hookSubject, hookArgs); decision != nil {
			switch {
			case !*decision:
				return approvalReply{}, nil
			case !opts.fresh && !opts.requireHuman && !requiresFreshApprovalTool(tool):
				return approvalReply{allow: true}, nil
			}
			// An "allow" opinion on a fresh-human-required decision is
			// ignored; fall through to the normal interactive prompt.
		}
	}

	var id string
	var reply chan approvalReply
	if opts.fresh || opts.requireHuman {
		id, reply = c.approval.registerDecisionWithInput(tool, subject, reason, args, opts.fresh, opts.requireHuman)
	} else {
		id, reply = c.approval.registerWithInput(tool, subject, reason, args)
	}

	c.sink.Emit(c.approvalRequestEvent(event.Approval{ID: id, Tool: tool, Subject: subject, Reason: reason, RawInput: append(json.RawMessage(nil), args...), Fresh: opts.fresh}))
	// The agent now needs the user's attention; a Notification hook can ping an
	// external channel (desktop notice, phone) while the run blocks on the reply.
	go c.hooks.Notification(ctx, approvalNotificationText(tool, subject), "permission_prompt")

	waitCtx, cancelWait := c.approval.waitContext(ctx)
	defer cancelWait()

	select {
	case r := <-reply:
		return r, nil
	case <-waitCtx.Done():
		c.approval.cancel(id)
		return approvalReply{}, waitCtx.Err()
	}
}

func (c *Controller) approvalRequestEvent(approval event.Approval) event.Event {
	return event.Event{Kind: event.ApprovalRequest, Approval: approval}
}

func (c *Controller) emitRememberResult(r RememberResult) {
	if r.Err != nil {
		c.sink.Emit(event.Event{
			Kind:  event.Notice,
			Level: event.LevelWarn,
			Text:  fmt.Sprintf(i18n.M.PermissionSaveFailedFmt, r.Rule, r.Err),
		})
		return
	}
	switch {
	case r.Saved:
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(i18n.M.PermissionSavedFmt, r.Path, r.Rule)})
	case strings.TrimSpace(r.CoveredBy) != "":
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(i18n.M.PermissionAlreadyAllowedFmt, r.Path, r.CoveredBy)})
	}
}

func (c *Controller) emitPlanModeReadOnlyCommandTrustResult(r PlanModeReadOnlyCommandTrustResult) {
	prefix := strings.TrimSpace(r.Prefix)
	if r.Err != nil {
		c.sink.Emit(event.Event{
			Kind:  event.Notice,
			Level: event.LevelWarn,
			Text:  fmt.Sprintf(i18n.M.PlanModeReadOnlyCommandTrustFailedFmt, prefix, r.Err),
		})
		return
	}
	switch {
	case r.Saved:
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(i18n.M.PlanModeReadOnlyCommandTrustSavedFmt, r.Path, prefix)})
	case strings.TrimSpace(r.CoveredBy) != "":
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(i18n.M.PlanModeReadOnlyCommandTrustAlreadyFmt, r.Path, r.CoveredBy)})
	}
}
