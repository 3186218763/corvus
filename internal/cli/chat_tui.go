package cli

import (
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"corvus/internal/command"
	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/plugin"
	"corvus/internal/provider"
	"corvus/internal/skill"
)

// chatTUI is a bubbletea Model that normally owns the terminal with an
// alt-screen transcript viewport. Termux is the exception: it stays in the
// normal buffer and commits finalized output to native scrollback via
// tea.Println so taps can still focus the soft keyboard.
type chatTUI struct {
	ctrl    *control.Controller
	label   string
	missing string // missing-key warning surfaced once in the banner, "" when ready

	width  int
	height int
	// themeSweep freezes the frame while a /theme switch wipes across it.
	themeSweep *themeSweep
	// nativeScrollback keeps Termux out of alt-screen mode so taps still focus
	// the textarea and raise the soft keyboard.
	nativeScrollback bool
	// mouseCaptureOff releases mouse ownership back to the terminal (View() sets
	// tea.MouseModeNone instead of MouseModeCellMotion) so its native
	// click-drag selection and right-click context menu work again. Toggled by
	// "/mouse" or CORVUS_DISABLE_MOUSE at startup; trades away in-app
	// drag-select, the transcript scrollbar, and wheel-scroll while it's on,
	// since the terminal no longer forwards those events to Corvus.
	mouseCaptureOff bool

	input       textarea.Model
	composerSel composerSelection
	composerMap composerLayoutCache
	// composerScrollOffset is an independent view offset used after the user
	// wheels inside an overflowing composer. The textarea keeps ownership of the
	// real insertion cursor; a subsequent edit or cursor key reattaches the view
	// to that cursor without the wheel having moved it.
	composerScrollOffset   int
	composerScrollDetached bool
	spinner                spinner.Model

	submittedInputs      []string
	submittedInputCursor int
	submittedInputDraft  string
	pastedBlocks         []pastedBlock
	nextPasteID          int
	usedPasteIDs         map[int]struct{}

	state    tuiState
	runStart time.Time
	elapsed  int
	// retryAttempt/retryMax drive the transient "retrying (n/m)" indicator while
	// the provider re-attempts the connection; cleared by the next stream event.
	retryAttempt int
	retryMax     int
	// turnTokens accumulates this turn's output tokens (summed from per-step Usage
	// events) for the live "↓N" readout in the running status line.
	turnTokens int

	// balance is the last-fetched wallet-balance readout (e.g. "¥110.00"), "" when
	// the provider declares no balance_url or a fetch failed. Refreshed async on
	// startup and after each turn so the status line stays roughly current without
	// blocking the event loop.
	balance string

	// todoArgs is the latest todo_write call's raw args; it drives the task list
	// pinned just below the input (see renderTodoPanel). "" when there's no list.
	// Persists across turns until the work completes or a new session starts.
	todoArgs string

	// planMode mirrors the agent's plan-first workflow (Shift+Tab toggles it). The
	// marker rides in outgoing user messages so the cache-stable prompt prefix is
	// left untouched.
	planMode bool
	// sessionSwitch is set by replayActiveBranch to suppress the ClearScreen
	// flicker when the viewport content is completely rebuilt during a session
	// switch (#5441). Cleared after one Update cycle.
	sessionSwitch bool
	// smooth is the in-flight scroll interpolation (nil when idle).
	smooth *smoothScroll
	// scrollRepaint restores the legacy full-screen repaint on every viewport
	// scroll (CORVUS_TUI_SCROLL_REPAINT=1) for terminals that strand stale
	// rows under the cell-diff renderer.
	scrollRepaint bool
	// yoloRestoreToolApprovalMode remembers the Ask/Auto base mode that Ctrl+Y
	// should restore after a desktop-style YOLO toggle.
	yoloRestoreToolApprovalMode string

	// pendingInterject queues input typed while a turn runs; each TurnDone
	// dequeues the front and submits it as the next turn.
	pendingInterject []string
	// queueEditCursor tracks which queued message the user is currently
	// browsing/editing via ↑/↓ during tuiRunning. -1 means "not browsing".
	queueEditCursor int
	// queueEditDraft saves the in-progress input text when the user first
	// presses ↑ to browse the queue, so it can be restored when the cursor
	// moves past the end.
	queueEditDraft string

	// history is a resumed session's messages, committed to scrollback once on
	// the first WindowSizeMsg so a reopened chat shows its prior transcript.
	history []provider.Message

	// reasoning accumulates the in-progress thinking stream (dim); pending
	// accumulates the in-progress answer (raw markdown). They are committed to
	// scrollback (reasoning collapsed by default, answer markdown-rendered) when they
	// finalize — at a tool/usage boundary or turn end — not previewed live, so
	// the bottom region stays a stable height. pendingCommit queues finalized
	// lines so a single Update emits exactly one ordered tea.Println.
	reasoning     *strings.Builder
	pending       *strings.Builder
	pendingCommit *[]string
	showReasoning bool // Ctrl+O / /verbose: show raw thinking text in the CLI
	cfg           *config.Config
	// reasoningLineIdx is the transcript index of the live "▎ thinking…" marker
	// while a reasoning block streams; it's rewritten to "▎ thought for Ns" when
	// the block closes. -1 when no block is open. transcriptDirty forces a
	// viewport re-feed after that in-place rewrite (length is unchanged).
	reasoningLineIdx int
	// reasoningTextIdx is the transcript index of the live reasoning text block
	// (the block right after the marker), streamed in as the model thinks and
	// removed when the block collapses (kept only in verbose mode). -1 when none.
	reasoningTextIdx int
	// reasoningView is a bounded trailing window (≤ reasoningViewMax bytes) of the
	// streaming thought, rendered live; the full text stays in reasoning for verbose.
	reasoningView []byte
	// reasoningNative is the Termux/native-scrollback path: reasoning is buffered
	// without a live transcript block, then appended once as a final summary.
	reasoningNative bool
	thinkStart      time.Time
	// answerIdx is the transcript index of the streaming answer block (rewritten in
	// place as completed paragraphs arrive); -1 when none is open. answerFlushed is
	// how many bytes of pending have already been rendered into it, so a Text packet
	// that doesn't close a new paragraph re-renders nothing.
	answerIdx     int
	answerFlushed int
	// toolStreamIdx is the transcript index of a running tool's live-output block
	// (streamed via ToolProgress under the tool card); -1 when none. toolStreamID
	// is the call ID it belongs to. Only a bounded tail is kept — the last few
	// complete lines (toolTail) plus the in-progress one (toolPartial) — so a
	// high-output command can't balloon memory or cost O(n²) re-splitting;
	// toolLineCount feeds the collapse summary.
	toolStreamIdx int
	toolStreamID  string
	toolTail      []string
	toolPartial   string
	toolLineCount int
	toolStreams   map[string]*toolProgressState
	// shellOutputs stores the full accumulated output of each shell command
	// (tool IDs with "shell-" prefix), so the first 10 lines can be shown after
	// collapse and Ctrl+B can toggle the complete output.
	shellOutputs  map[string]string
	shellExpanded map[string]bool
	// shellMeta stores outcome for finished shells (duration / ok).
	shellMeta map[string]shellRunMeta
	// shellNativeFlushed records ids whose finished preview was already
	// printed into native scrollback (no in-place rewrite there).
	shellNativeFlushed map[string]bool
	// shellLiveIdx maps a tool id to its in-flight stream block index (fixed
	// canvas under the card). Separate from toolCardIdx so Ctrl+B anchors
	// stay on cards while live streams can still be removed on result.
	shellLiveIdx map[string]int
	// shellTranscriptIdx maps a shell tool ID to the transcript index of its
	// card (Ctrl+B). Prefer toolCardIdx; this is kept for compatibility.
	shellTranscriptIdx map[string]int
	// toolCardIdx maps a dispatched tool id to its card block index, so
	// completion can re-anchor Ctrl+B expansion to the card.
	toolCardIdx map[string]int
	// exploreIdx is the transcript index of the open • Explored cell, or -1.
	// Consecutive read-category tools append leaves and re-render that block.
	exploreIdx    int
	exploreLeaves []exploreLeaf
	// hadWorkActivity is true when this turn committed any tool card
	// (explore/ran/edited/mcp/…). Gates the dim ─ turn separator.
	hadWorkActivity bool
	// toolStreamStart / toolStreamFrame drive the "└ working · Ns" line shown
	// under a dispatched tool that hasn't produced output yet, so a slow tool
	// reads as making progress rather than frozen.
	toolStreamStart time.Time
	toolStreamFrame int
	transcriptDirty bool
	// forceGotoBottom is set by replayActiveBranch and resetFreshContextView to
	// pin the viewport to the bottom after a session / branch / clear switch
	// regardless of the previous wasAtBottom state (#4584).
	forceGotoBottom bool
	eventCh         chan event.Event
	started         bool // banner + resumed history committed once

	// transcript holds every finalized line commitLine emits; the viewport
	// renders a scrollable window of it (alt-screen owns the grid, so there's no
	// native terminal scrollback). sel is the live left-drag text selection.
	transcript []string
	// transcriptSources runs parallel to transcript and retains raw, semantic
	// content for blocks whose layout depends on terminal width. Fixed blocks
	// keep their already-rendered text; markdown, user bubbles, reasoning, tool
	// cards, and replay bundles are regenerated after a resize.
	transcriptSources []transcriptSource
	wrappedLines      []string // transcript wrapped to viewport width (rendered each frame)
	blockLineCounts   []int    // wrapped line count per transcript block
	liveDirtyIdx      []int    // blocks mutated this Update, re-wrapped by the wrapper
	// turnReceipt holds the latest completed turn's session cache-hit rate
	// readout ("cached 87.50%"), rendered in the footer row below the composer.
	turnReceipt string
	viewport    viewport.Model
	sel         selection
	// autoScroll drives edge-drag scrolling: -1 up, +1 down, 0 off. dragX is the
	// column the drag is held at, so the ticker can extend the selection head.
	autoScroll int
	dragX      int
	// scrollbarDrag owns left-button drags that start on the transcript scrollbar
	// column. It is separate from text selection so the visual thumb is not a
	// dead target and dragging it never leaves a transcript selection behind.
	scrollbarDrag       bool
	scrollbarGrabOffset int
	// copyNoticeText is a transient "copied to clipboard" hint shown on the status
	// line after a mouse-drag, right-click, or Ctrl+C selection copy; "" when none
	// is showing. copyNoticeSeq guards its expiry tick so an older copy's timer
	// can't clear a newer notice — each copy bumps the sequence and only a tick
	// carrying the current sequence clears the text.
	copyNoticeText string
	copyNoticeSeq  int
	// clipboardImagePending keeps the footer honest while the platform clipboard
	// is being decoded and prevents repeated Ctrl+V presses from attaching the
	// same image multiple times before the first read completes.
	clipboardImagePending bool

	// The user bubble is echoed to scrollback immediately on Enter (bubbleStartIdx
	// marks where in the transcript it landed). It stays "un-sendable" until the
	// first response packet arrives: pressing Esc/Ctrl+C before then pops those
	// lines back off the transcript and restores the text to the input box, leaving
	// no trace. bubblePending is true from startTurn until the first packet confirms
	// the send or it's un-sent; turnDiscarded then swallows the turn's
	// already-buffered events until its TurnDone settles.
	pendingRestore string
	pendingPastes  []string
	bubbleStartIdx int
	bubblePending  bool
	turnDiscarded  bool

	// pendingApproval holds the tool-call approval currently shown in the banner
	// (nil when none). While set, the controller's run goroutine is blocked
	// awaiting ctrl.Approve and key input is captured to answer it.
	pendingApproval   *event.Approval
	approvalSelection int

	// chooser holds the `ask` tool's question card (nil when none). While set, the
	// run goroutine is blocked awaiting ctrl.AnswerQuestion and keys drive the card.
	chooser *chooser

	// rewind holds the Esc-Esc / "/rewind" picker (nil when closed); while set,
	// keys drive it and it renders as an overlay. lastEsc times the double-Esc
	// gesture that opens it on an empty composer.
	rewind *rewindPicker
	// resumePick is the interactive "/resume" session picker overlay. Non-nil
	// while the user browses saved sessions with ↑/↓ and confirms with Enter.
	resumePick *resumePicker
	// quickPick owns searchable single-choice overlays such as /model and
	// /provider. It never invokes a raw-mode prompt inside Bubble Tea.
	quickPick *quickPicker
	copyPick  *copyPicker
	lastEsc   time.Time
	// cheatsheetOpen is the empty-input "?" keyboard shortcuts overlay. Esc
	// closes it (higher priority than cancel/clear). Composer stays visible and
	// the draft is preserved (open only when empty).
	cheatsheetOpen bool

	// mcp is the interactive "/mcp" manager overlay. mcpDisabled tracks servers
	// turned off only for this chat session, matching the desktop connector
	// toggle's non-persistent semantics.
	mcp         *mcpManager
	mcpDisabled map[string]bool

	// clearConfirm is the destructive "/clear" confirmation overlay. It is separate
	// from /new because /clear discards the current transcript instead of saving it.
	clearConfirm *clearConfirm

	// lastCtrlCAt records when Ctrl+C was pressed while idle on an empty
	// composer, enabling a "press again to quit" confirmation pattern (1.5s
	// window). Reset when Ctrl+C clears non-empty input instead.
	lastCtrlCAt time.Time

	// mcpImport holds the interactive cc-switch MCP import picker (nil when
	// closed). It writes selected servers to config and hot-connects the ones that
	// can start successfully.
	mcpImport *mcpImportPicker

	// host is the running MCP servers (nil when no plugins). The TUI reads
	// prompts (slash commands), resources (@-references), and server status
	// (/mcp) from it.
	host *plugin.Host

	// commands are custom slash commands loaded from .corvus/commands; each renders
	// its template with the typed args and sends the result as a turn.
	commands []command.Command

	// skills are the discoverable skills (built-in + user/project); each is offered
	// in the slash menu as "/<name>" and managed via /skills.
	skills []skill.Skill

	// skillPick is the interactive skill picker overlay for /skills. nil when closed.
	skillPick *skillPicker

	// buildController builds a fresh controller for a model/profile pair, carrying prior
	// history across and pinning auto-save to resumePath so the continued
	// conversation stays in one file (set by chatREPL; it must NOT touch this
	// model — the swap happens on the running copy). nil disables runtime
	// rebuild commands. modelRef is the active "provider/model" ref, marked
	// current in the picker. runtimeProfile stores boot's normalized token mode:
	// full (displayed as balanced), economy, or delivery. oldCtrl is the
	// outgoing controller, passed through so the replacement can carry forward
	// same-session tool grants and Plan-mode read-only command trust that
	// don't travel through carry/resumePath (see Controller.RestoreSessionAuthorizations).
	buildController   func(spec controllerBuildSpec, carry []provider.Message, resumePath string, oldCtrl *control.Controller) (*control.Controller, error)
	modelRef          string
	runtimeProfile    string
	runtimeGuidance   string
	runtimeCompletion string
	runtimeExposure   string
	effortLevel       string // "" when the current provider/model has no configurable effort

	// leases owns the session lease guarding the TUI's active session file (set
	// by chatREPL; nil in tests and when persistence is disabled). Every in-TUI
	// operation that rebinds the controller to another session file must move
	// the lease first — see rebindSessionLease / followSessionLease.
	leases *control.SessionLeaseKeeper

	// outputStyle is the active output-style name (config agent.output_style),
	// shown as the current entry in the /output-style listing. "" = default.
	outputStyle string

	// diffMaxLines controls the max lines shown in a diff view. 0 = show all;
	// non-zero = fold at that many lines. Toggled by /diff-fold.
	diffMaxLines int

	// statuslineCmd is the user's custom status-line command (config
	// [statusline].command); "" disables it. statuslineOut caches its latest
	// one-line stdout, refreshed at startup and after each turn and rendered in
	// place of the built-in data row.
	statuslineCmd string
	statuslineOut string
	gitStatus     gitStatus

	// statusLineCount is the number of terminal rows the status block occupies
	// (wrapped working line + wrapped status line + wrapped data line). Updated
	// each frame via computeStatusLineCount so bottomRows can reserve the correct
	// height; starts at 2 (unwrapped) until first render.
	statusLineCount int

	// panels caches the rendered bottom region (todo / approval / chooser /
	// rewind / completion / manager panels) from the last Update so bottomRows()
	// and View() share one render pass per event. panelsValid is false until the
	// first Update, forcing a render-on-demand fallback. panelRenderHook is a
	// test seam; nil in production.
	panels          bottomPanels
	panelsValid     bool
	panelRenderHook func(name string)

	// modelSwitchPending is true while any async controller rebuild is in flight.
	modelSwitchPending bool
	// pendingModelSwitch holds the tea.Cmd that triggers the async build. The
	// historical field name is retained because model, effort, skill refresh,
	// and work-mode changes all share the same atomic swap path.
	pendingModelSwitch tea.Cmd
	// oldControllers accumulates controllers retired by runtime switches.
	// They cannot be closed during the switch (Close runs SessionEnd hooks
	// and kills plugin subprocesses, both of which corrupt the terminal's
	// raw mode). Instead they are closed at process exit when the terminal
	// is already being restored.
	oldControllers []*control.Controller

	// completion is the live autocomplete menu (slash commands; @-refs later).
	completion completion
	// composerRaisedRows holds the total row count of the visible bottom
	// panels so the composer stays raised (Codex-style) after a popup closes,
	// until the next submission drops it back to the bottom.
	composerRaisedRows int
	// fileSearchCache memoizes fileref.Search by query so the bounded walk runs
	// once per @token fragment, not on every keystroke that re-renders the menu.
	fileSearchCache map[string][]string
}

type tuiState int

const (
	tuiIdle tuiState = iota
	tuiRunning
)

type controllerBuildSpec struct {
	ModelRef         string
	RuntimeProfile   string
	Guidance         string
	Completion       string
	Exposure         string
	ToolApprovalMode string
	PlanMode         bool
	EffortOverride   *string
}

// armControllerRebuild arms the async controller rebuild shared by every
// in-session switch (model, effort, work mode, runtime rebuild, skill hooks):
// approval and plan state carry over from the live controller, and both
// outcomes land as one modelSwitchMsg. spec carries the per-site build inputs;
// outcome carries the per-site result fields (ref, profile, failurePrefix,
// successNotice). The returned tea.Cmd is also stored in pendingModelSwitch.
func (m *chatTUI) armControllerRebuild(spec controllerBuildSpec, carried []provider.Message, resumePath string, outcome modelSwitchMsg) tea.Cmd {
	oldCtrl := m.ctrl
	build := m.buildController
	if outcome.guidance == "" {
		outcome.guidance = spec.Guidance
	}
	if outcome.completion == "" {
		outcome.completion = spec.Completion
	}
	if outcome.exposure == "" {
		outcome.exposure = spec.Exposure
	}
	m.modelSwitchPending = true
	m.pendingModelSwitch = func() tea.Msg {
		spec.ToolApprovalMode = oldCtrl.ToolApprovalMode()
		spec.PlanMode = oldCtrl.PlanMode()
		c, err := build(spec, carried, resumePath, oldCtrl)
		if err != nil {
			msg := outcome
			msg.err = err
			return msg
		}
		return modelSwitchMsg{
			ref:           outcome.ref,
			profile:       outcome.profile,
			ctrl:          c,
			oldCtrl:       oldCtrl,
			label:         c.Label(),
			commands:      c.Commands(),
			skills:        c.SlashSkills(),
			host:          c.Host(),
			successNotice: outcome.successNotice,
		}
	}
	return m.pendingModelSwitch
}

func (m *chatTUI) runtimeSwitchBusy() bool {
	if m == nil || m.ctrl == nil {
		return false
	}
	status := m.ctrl.RuntimeStatus()
	return status.Running || status.PendingPrompt || status.BackgroundJobs > 0 || m.pendingApproval != nil || m.chooser != nil
}

// agentEventMsg is one typed event from the agent's run loop.
type agentEventMsg event.Event

// maxEventDrain caps how many buffered events one Update coalesces before
// yielding to render, so a sustained output flood still shows live progress.
const maxEventDrain = 512

const resetMouseTracking = ansi.ResetModeMouseX10 +
	ansi.ResetModeMouseNormal +
	ansi.ResetModeMouseHighlight +
	ansi.ResetModeMouseButtonEvent +
	ansi.ResetModeMouseAnyEvent +
	ansi.ResetModeMouseExtSgr +
	ansi.ResetModeMouseExtUtf8 +
	ansi.ResetModeMouseExtUrxvt +
	ansi.ResetModeMouseExtSgrPixel

// compactDoneMsg reports that an async /compact pass returned. The card was
// already drawn from the CompactionDone event; this only surfaces a failure and
// snapshots on success.
type compactDoneMsg struct{ err error }

// compactSnapshotMsg reports that the post-compaction session snapshot write
// finished, so the lease follow-up can run in Update.
type compactSnapshotMsg struct{}

// newSessionDoneMsg reports that an async /new rotation returned. The
// post-rotation UI updates run in Update so the model is only touched there.
type newSessionDoneMsg struct{ err error }

// tuiShutdownMsg asks the live TUI model to persist its current controller and
// quit. It is injected from the signal handler so shutdown does not snapshot a
// stale controller captured before an in-TUI rebuild.
type tuiShutdownMsg struct{}

// elapsedTickMsg fires once a second while a turn runs, driving the "thinking
// Ns" counter in the status line.
// shellRunMeta is the outcome attached to a finished shell tool card.
type shellRunMeta struct {
	ok         bool
	durationMs int64
	err        string
}

type toolProgressState struct {
	tail      []string
	partial   string
	lineCount int
	startedAt time.Time
}

type elapsedTickMsg struct{}

// balanceMsg carries the result of an async wallet-balance fetch; text is the
// formatted readout ("" when none/failed).
type balanceMsg struct{ text string }

// statuslineMsg carries the latest custom status-line output (one line, ""
// when none/failed).
type statuslineMsg struct{ out string }

// gitStatusMsg carries the latest lightweight git readout for the built-in
// status line. Empty means "not a git worktree" or "git unavailable".
type gitStatusMsg struct{ status gitStatus }

const statuslineCommandTimeout = 2 * time.Second

// modelSwitchMsg carries the result of an async /model switch. A nil err means
// the new controller is ready in ctrl; label/commands/skills/host mirror the
// fields that runModelSubcommand used to set synchronously. oldCtrl is the
// previous controller that must be closed after the switch — its cleanup
// (SessionEnd hooks, plugin subprocess kill) is deferred to a tea.Cmd so it
// runs after the render completes, avoiding corruption of the terminal's raw
// mode that would occur if Close() were called from the build goroutine.
type modelSwitchMsg struct {
	ref           string
	profile       string
	guidance      string
	completion    string
	exposure      string
	ctrl          *control.Controller
	oldCtrl       *control.Controller
	label         string
	commands      []command.Command
	skills        []skill.Skill
	host          *plugin.Host
	failurePrefix string
	successNotice string
	err           error
}

// promptResolvedMsg carries the result of fetching an MCP prompt (an async
// prompts/get). display is the command line echoed as the user bubble; sent is
// the rendered prompt text that becomes the model turn.
type promptResolvedMsg struct {
	display string
	sent    string
	err     error
}

// refsResolvedMsg carries the result of resolving the @references in a
// submitted line (async file reads / MCP resources/read).
type refsResolvedMsg struct {
	sent    string
	display string
	restore string
	block   string
	errs    []string
}

type clipboardImageMsg struct {
	path string
	err  error
}

var detectTermuxTerminal = isTermuxTerminal

type interactivePanelBudget struct {
	queueRows      int
	todoRows       int
	completionRows int
}

func (m chatTUI) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		waitForAgentEvent(m.eventCh),
		fetchBalance(m.ctrl),
		m.runStatusline(), // nil (no-op) unless a custom status line is configured
		m.refreshGitStatus(),
	)
}

func (m chatTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasAtBottom := m.viewport.AtBottom()
	prevLines := len(m.transcript)
	prevWidth := m.width
	prevYOff := m.viewport.YOffset()
	wasHidingComposer := m.hideComposer()
	var resizeAnchor transcriptResizeAnchor
	if size, ok := msg.(tea.WindowSizeMsg); ok && size.Width != m.width && !wasAtBottom {
		resizeAnchor = captureTranscriptResizeAnchor(m.transcript, m.viewport.Width(), prevYOff)
	}

	next, cmd := m.update(msg)
	cm := next.(chatTUI)
	// Render the bottom region once per event; bottomRows()/View() read it.
	cm.panels = cm.renderBottomPanels()
	cm.panelsValid = true
	// Modal managers (skills/MCP/resume/…) hide the composer. When they close,
	// drop any prior raise-hold so the input returns to the bottom instead of
	// floating at the height left by an earlier slash menu.
	if wasHidingComposer && !cm.hideComposer() {
		cm.composerRaisedRows = 0
	}
	// The composer rises with visible panels, then immediately returns to the
	// bottom when a transient panel closes. Holding old popup rows strands large
	// blank regions on short terminals.
	if !cm.hideComposer() {
		cm.composerRaisedRows = cm.panels.rows
	}

	contentW := transcriptContentWidth(cm.width, cm.nativeScrollback)
	cm.viewport.SetWidth(contentW)
	// Recompute the wrapped status-line count so bottomRows reserves the right
	// height for the viewport. Use cm.width (same as boxW in View()) so the
	// wrapping width matches what View() actually renders.
	cm.statusLineCount = cm.computeStatusLineCount(cm.width)
	// Keep the composer proportional to the live terminal instead of letting its
	// absolute row cap crowd the transcript and fixed status rows on short
	// windows. Textarea remains the owner of the scroll offset and caret reveal.
	cm.syncInputHeightLimit()
	cm.viewport.SetHeight(cm.transcriptHeight())
	if cm.width != prevWidth {
		cm.reflowTranscript(cm.width)
		cm.rebuildWrappedLines(contentW)
		// Selection coordinates are visual-line based and cannot survive a
		// semantic reflow without selecting unrelated text.
		cm.sel = selection{}
	} else if len(cm.transcript) != prevLines || len(cm.liveDirtyIdx) > 0 {
		// Cache sync: wrap any blocks appended since the last pass (from the
		// current cache count, which remove/truncate already adjusted), then
		// re-wrap blocks mutated in place by setLiveBlock/setTranscriptBlock.
		cm.appendWrappedBlocks(len(cm.blockLineCounts), contentW)
		for _, idx := range cm.liveDirtyIdx {
			cm.rewrapBlock(idx, contentW)
		}
	} else if cm.transcriptDirty {
		cm.rebuildWrappedLines(contentW) // dirty without a tracked block: full rebuild
	}
	cm.liveDirtyIdx = cm.liveDirtyIdx[:0]
	if cm.width != prevWidth || len(cm.transcript) != prevLines || cm.transcriptDirty {
		cm.viewport.SetContent(strings.Join(cm.wrappedLines, "\n"))
		if wasAtBottom {
			cm.viewport.GotoBottom() // tail-follow: stay pinned to newest output
		} else if cm.width != prevWidth && resizeAnchor.valid {
			cm.viewport.SetYOffset(resizeAnchor.yOffset(cm.transcript, contentW))
		}
	}
	if cm.forceGotoBottom {
		cm.viewport.GotoBottom()
		cm.forceGotoBottom = false
	}
	cm.transcriptDirty = false
	// Any viewport scroll (wheel, PgUp/PgDn, edge auto-scroll, or tail-follow to
	// newest output) shifts the whole window. Some terminals (Warp) mishandle
	// the renderer's scroll/insert-line optimization and strand stale rows, so
	// legacy repaint mode (CORVUS_TUI_SCROLL_REPAINT=1) force-clears on every
	// offset move; the default is incremental repaint without ClearScreen.
	if cm.viewport.YOffset() != prevYOff && !cm.nativeScrollback && !cm.sessionSwitch && cm.scrollRepaint {
		return cm, tea.Batch(tea.ClearScreen, cmd)
	}
	cm.sessionSwitch = false
	return cm, cmd
}

// update runs the model's message handling. Update wraps it to keep the
// transcript viewport sized, fed, and tail-following after every message.
func (m chatTUI) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	case tea.MouseMotionMsg:
		return m.handleMouseMotion(msg)
	case autoScrollMsg:
		return m.handleAutoScroll(msg)
	case tea.MouseReleaseMsg:
		return m.handleMouseRelease(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg)
	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	case agentEventMsg:
		return m.handleAgentEvent(msg)
	case balanceMsg:
		return m.handleBalance(msg)
	case statuslineMsg:
		return m.handleStatusline(msg)
	case gitStatusMsg:
		return m.handleGitStatus(msg)
	case compactDoneMsg:
		return m.handleCompactDone(msg)
	case compactSnapshotMsg:
		return m.handleCompactSnapshot(msg)
	case newSessionDoneMsg:
		return m.handleNewSessionDone(msg)
	case tuiShutdownMsg:
		return m.handleTuiShutdown(msg)
	case modelSwitchMsg:
		return m.handleModelSwitch(msg)
	case promptResolvedMsg:
		return m.handlePromptResolved(msg)
	case mcpExternalDoneMsg:
		return m.handleMCPExternalDone(msg)
	case refsResolvedMsg:
		return m.handleRefsResolved(msg)
	case clipboardImageMsg:
		return m.handleClipboardImage(msg)
	case clipboardTextPasteMsg:
		return m.handleClipboardTextPaste(msg)
	case clipboardCopyMsg:
		return m.handleClipboardCopy(msg)
	case copyNoticeExpireMsg:
		return m.handleCopyNoticeExpire(msg)
	case themeSweepTickMsg:
		return m.handleThemeSweepTick(msg)
	case elapsedTickMsg:
		return m.handleElapsedTick(msg)
	case spinner.TickMsg:
		return m.handleSpinnerTick(msg)
	case smoothScrollTickMsg:
		return m.handleSmoothScrollTick(msg)
	}
	// Messages no case claims still flow through the shared textarea update,
	// matching the original fall-through after the type switch.
	return tailUpdate(m, msg, nil, "")
}

var clearWideInputChanges = runtime.GOOS == "windows"

func shouldClearWideInputChange(before, after string) bool {
	return clearWideInputChanges &&
		before != after &&
		(hasWideInputCells(before) || hasWideInputCells(after))
}

func hasWideInputCells(s string) bool {
	return s != "" && visibleWidth(s) != utf8.RuneCountInString(s)
}

// finalize drains the committed-line queue and batches the turn's commands. In
// the default alt-screen path the queue is already mirrored in m.transcript. In
// Termux finalized lines are also emitted into the terminal's native scrollback.
func finalize(m chatTUI, cmds []tea.Cmd) tea.Cmd {
	if m.nativeScrollback && len(*m.pendingCommit) > 0 {
		out := strings.TrimRight(clampWidth(strings.Join(*m.pendingCommit, "\n"), m.width), "\n")
		*m.pendingCommit = (*m.pendingCommit)[:0]
		var prints []tea.Cmd
		for _, chunk := range chunkLines(out, m.scrollChunkHeight()) {
			prints = append(prints, tea.Println(chunk))
		}
		cmds = append(cmds, tea.Sequence(prints...))
		return tea.Batch(cmds...)
	}
	*m.pendingCommit = (*m.pendingCommit)[:0]
	return tea.Batch(cmds...)
}

// reasoningViewMax bounds the live thinking buffer the streamed block renders
// from. Re-rendering the full chain of thought on every delta was O(n²) (a 2k-
// token thought churned ~4.7GB); rendering only the trailing window keeps each
// delta O(1). The full text still lives in m.reasoning for verbose mode.
const reasoningViewMax = 4096

// reasoningTailLines caps how many trailing visual lines the live block shows.
const reasoningTailLines = 12

// toolStreamTailLines caps how many trailing output lines a running tool shows;
// the live block scrolls within this window so a chatty build doesn't flood.
const toolStreamTailLines = 8

// shellExpandMaxLines caps how many lines Ctrl+B shows in expanded mode, so a
// very large output (e.g. thousands of lines) doesn't hang the TUI or push the
// input box off-screen.
const shellExpandMaxLines = 200

// planApprovalTool is the Tool name the controller puts on the ApprovalRequest it
// emits to gate a plan (mirrors control's constant). The banner, status line, and
// approval handler key on it to render the plan-specific prompt and to keep the
// [plan] tag in sync when the user starts execution or exits without executing.
const planApprovalTool = "exit_plan_mode"

type approvalChoice struct {
	label           string
	allow           bool
	allowForSession bool
	persistToConfig bool
	exitPlan        bool
}

var (
	// Input box: only top + bottom borders, no sides. The concrete colors are
	// refreshed from the active CLI theme during startup. Kept as a regression
	// guard for the borderless contract; the field painter owns the tint.
	inputBoxStyle    lipgloss.Style
	todoPanelStyle   lipgloss.Style
	statusBlockStyle lipgloss.Style
	workingStyle     lipgloss.Style
	// choicePanelStyle frames bottom pickers / approval / ask surfaces.
	choicePanelStyle lipgloss.Style
)

func (m chatTUI) View() tea.View {
	if m.themeSweep != nil {
		v := tea.NewView(m.themeSweep.render())
		if !m.nativeScrollback {
			v.AltScreen = true
			if m.mouseCaptureOff {
				v.MouseMode = tea.MouseModeNone
			} else {
				v.MouseMode = tea.MouseModeCellMotion
			}
		}
		return v
	}
	boxW := m.width
	if boxW < 10 {
		boxW = 10
	}
	hideComposer := m.hideComposer()
	shellMode := strings.HasPrefix(strings.TrimSpace(m.input.Value()), "!")
	cancelRequested := m.cancelRequested()
	var box string
	if !hideComposer {
		// Borderless field: the painter tints the whole box width, so the input
		// row carries no chrome. The mode badge lives on the footer row below
		// (statusPrimaryWithBadge prepends it), keeping the bottom-left corner.
		box = renderComposerField(m.renderComposerInput(), m.composerFrameWidth())
	}

	primaryStatus := m.statusPrimaryWithBadge(shellMode, cancelRequested)
	// The spinning "thinking…" indicator is its own line ABOVE the input box (shown
	// only while a turn runs); the status/data rows stay below. This mirrors Claude
	// Code: live progress over the composer, shortcuts + stats under it.
	working := m.runningWorkingLine(cancelRequested, true)
	// Bottom region pinned under the transcript viewport: optional panels, the
	// composer when visible, then the two status rows. Its height feeds
	// transcriptHeight so the viewport above fills exactly the rest of the screen.
	panels := m.panels
	if !m.panelsValid {
		panels = m.renderBottomPanels()
	}
	var parts []string
	rowsAboveBox := 0 // terminal rows before the composer (working line, queue indicator)
	// The working spinner (when running) and the queue indicator render ABOVE
	// the composer; popups render BELOW it (Codex-style: typing "/" raises the
	// input and the menu expands downward).
	if working != "" {
		working = wrapStatusLine(working, boxW)
		parts = append(parts, workingStyle.Width(boxW).MaxWidth(boxW).Render(working))
		rowsAboveBox += wrappedRowCount(working, boxW)
	}
	if !hideComposer {
		if qi := wrapStatusLine(m.renderQueueIndicator(), boxW); qi != "" {
			parts = append(parts, qi)
			rowsAboveBox += wrappedRowCount(qi, boxW)
		}
		parts = append(parts, box)
	}
	// Popups expand below the composer and raise the input while visible.
	var panelParts []string
	for _, s := range []string{panels.todo, panels.banner, panels.chooser, panels.rewind, panels.mcpImport, panels.resumePick, panels.quickPick, panels.copyPick, panels.cheatsheet, panels.completion} {
		if s != "" {
			panelParts = append(panelParts, s)
		}
	}
	if m.nativeScrollback && panels.manager != "" {
		panelParts = append(panelParts, panels.manager)
	}
	if len(panelParts) > 0 {
		parts = append(parts, panelParts...)
	}
	// This branch only supports a caller that has not yet refreshed panel state;
	// normal Update paths keep composerRaisedRows equal to panels.rows.
	if !hideComposer && m.composerRaisedRows > panels.rows {
		held := m.composerRaisedRows - panels.rows
		parts = append(parts, strings.Repeat("\n", held-1))
	}
	// The manager footer rides the bottom rail with the popups so the manager
	// card and its hint stay adjacent.
	if footer := panels.managerFooter; footer != "" {
		parts = append(parts, footer)
	}
	statusBlock := m.renderStatusBlock(primaryStatus, boxW)
	parts = append(parts, statusBlockStyle.Width(boxW).MaxWidth(boxW).Render(statusBlock))

	if m.nativeScrollback {
		v := tea.NewView(strings.Join(parts, "\n"))
		if !hideComposer {
			if cur := m.composerCursor(); cur != nil {
				// The borderless field has no padding chrome.
				cur.Y += rowsAboveBox
				v.Cursor = cur
			}
		}
		return v
	}

	// Full-screen frame: the transcript viewport on top (it pads to exactly its
	// height), the pinned bottom region beneath. Alt-screen owns the grid, so
	// resize repaints cleanly — no scrollback reflow, no ghost borders.
	mainArea := m.renderTranscript()
	if card := m.renderMainManager(); card != "" {
		mainArea = m.renderTranscriptWithMainManager(card)
	}
	v := tea.NewView(mainArea + "\n" + strings.Join(parts, "\n"))
	v.AltScreen = true
	if m.mouseCaptureOff {
		// Release the mouse to the terminal: native click-drag selection and
		// right-click context menu work again, at the cost of the in-app
		// scrollbar, wheel-scroll, and drag-select while it's off.
		v.MouseMode = tea.MouseModeNone
	} else {
		v.MouseMode = tea.MouseModeCellMotion // wheel targets the hovered scroll region; text selection is handled in-app
	}
	// Anchor the real terminal cursor at the textarea's insertion point only when
	// the composer is visible. input.Cursor() is relative to the textarea; offset
	// by the viewport height + rows above (the borderless field adds no
	// border/padding chrome).
	if !hideComposer {
		if cur := m.composerCursor(); cur != nil {
			cur.Y += m.viewport.Height() + rowsAboveBox
			v.Cursor = cur
		}
	}
	return v
}

// todoPanelMaxRows caps how many task lines the pinned panel shows; a long list
// is truncated with a "+N more" footer so the bottom region stays compact.
const todoPanelMaxRows = 8

type todoPanelTodo struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
	Level      int    `json:"level"`
}

// The composer grows with its content up to this comfort cap. The effective
// cap is lowered for short terminals by syncInputHeightLimit, after which the
// textarea scrolls internally and keeps the caret visible.
const maxInputRows = 8

const minTranscriptRows = 3
const foldedPasteMinChars = 1000
const foldedPasteMinLines = 5

type pastedBlock struct {
	label string
	text  string
	image bool // an image attachment: expands to its bare @ref, not a wrapped block
}

var cliImageRefRe = regexp.MustCompile(`(?:^|\s)@\.corvus/attachments/clipboard-\d{8}-\d{6}\.\d+(?:-(?:\d{6}|[a-f0-9]{8}))?\.(?:png|jpg|jpeg|gif|webp)`)

// eventSink is the event.Sink the agent emits to in TUI mode. Each event
// becomes an agentEventMsg. The channel is generously buffered so streaming
// bursts don't back-pressure the agent goroutine. Ordinary events are shed
// (and counted) when the channel is full so a stalled render loop cannot
// freeze the emitting turn or, through the shared synchronized sink, unrelated
// emitters such as background-job notices. ApprovalRequest/AskRequest are
// always delivered: the run loop blocks on the frontend's answer, so dropping
// one would hang the turn.
type eventSink struct {
	ch      chan<- event.Event
	dropped atomic.Uint64
}

func (s *eventSink) Emit(e event.Event) {
	switch e.Kind {
	case event.ApprovalRequest, event.AskRequest:
		s.ch <- e // reliable: a dropped prompt would wedge the turn forever
	default:
		select {
		case s.ch <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

// droppedEvents reports how many ordinary events were shed because the TUI
// event channel was full.
func (s *eventSink) droppedEvents() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}
