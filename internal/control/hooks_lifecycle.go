package control

import (
	"context"
	"strings"

	"corvus/internal/agent"
	"corvus/internal/event"
	"corvus/internal/jobs"
	"corvus/internal/provider"
)

// lastAssistantText returns the content of the most recent assistant message with
// non-empty text — the model's final answer for the turn (its plan, in plan mode).
func lastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	return ""
}

// Run executes a turn synchronously, returning the agent's error. Used by the
// headless `corvus run` path, where the Sink renders to stdout and the caller
// just needs the exit status — no TurnDone event, no cancel bookkeeping.
func (c *Controller) Run(ctx context.Context, input string) (err error) {
	defer event.RecordTurnCompletion(c.sink)
	c.maybeSessionStart(ctx)
	parentSession := c.parentSessionID()
	ctx = agent.WithParentSession(ctx, parentSession)
	ctx = jobs.WithSession(ctx, parentSession)
	ctx = agent.WithUserImages(ctx, c.inputImages(input))
	rawInput := input
	ctx = agent.WithRawUserInput(ctx, rawInput)
	input = c.Compose(input)
	startMessages := c.messageCount()
	defer c.snapshotActivityIfChanged(startMessages)
	c.beginCheckpoint(input)
	if c.guardianSess != nil {
		c.guardianSess.ResetTurn()
	}
	if c.hooks.Enabled() {
		c.mu.Lock()
		c.turn++
		turn := c.turn
		c.mu.Unlock()
		if block, _ := c.hooks.PromptSubmit(ctx, input, turn); block {
			return nil
		}
		defer func() { c.hooks.StopResult(context.Background(), lastAssistantText(c.History()), turn, err) }()
	}
	c.markInFlightTurn(startMessages, true)
	defer c.clearInFlightTurn()
	ctx = c.withPlannerTurnMetadata(ctx, rawInput, false, startMessages)
	err = c.runner.Run(ctx, c.withCapabilityRoute(input, rawInput))
	return err
}

// maybeSessionStart fires the SessionStart hook exactly once per session, lazily
// on the first turn — by then the sink/notify is wired, and a resumed session
// fires it too (its first post-resume turn).
func (c *Controller) maybeSessionStart(ctx context.Context) {
	c.hooks.SetSessionID(c.parentSessionID())
	c.mu.Lock()
	if c.startedOnce {
		c.mu.Unlock()
		return
	}
	c.startedOnce = true
	c.mu.Unlock()
	c.enqueueHookContexts(c.hooks.SessionStart(ctx))
}

// ReleaseResources stops plugin subprocesses and releases resources without
// firing SessionEnd. Use it only when replacing the controller for the same
// logical session.
func (c *Controller) ReleaseResources() {
	c.close(false, closeJobsWithGrace)
}

// Close stops plugin subprocesses and releases resources. A session that ever
// started fires SessionEnd so a teardown hook runs.
func (c *Controller) Close() {
	c.close(true, closeJobsWithGrace)
}

// CloseAfterDestroy releases controller resources after the caller has already
// begun session-specific job teardown. It avoids a second synchronous job grace
// wait while still cancelling the manager root and reaping temporary artifacts
// once every job goroutine finally exits.
func (c *Controller) CloseAfterDestroy() {
	c.close(true, closeJobsAsync)
}

type closeJobsMode int

const (
	closeJobsWithGrace closeJobsMode = iota
	closeJobsAsync
)

func (c *Controller) close(fireSessionEnd bool, jobsMode closeJobsMode) {
	// Desktop tab lifecycles can race a rebind/model-switch/close on the same
	// controller; make teardown idempotent so a duplicate Close cannot re-fire
	// SessionEnd hooks or re-run cleanup. The first caller's jobsMode wins.
	c.closeOnce.Do(func() {
		c.mu.Lock()
		started := c.startedOnce
		cancel := c.cancel
		// Seal turn admission and drop anything already parked: a parked turn
		// must not start against a controller that is being torn down, and
		// without the closed flag a submit landing after this critical
		// section (while a running turn's TurnDone delivery is still in
		// flight) would park again and start after teardown.
		c.closed = true
		c.parkedTurns = nil
		// A finishing-only controller no longer needs the delivery gate because
		// closed seals every admission path. Keep running truthful until the
		// foreground goroutine actually exits; clearing it here would report idle
		// while tools and prompt waiters were still live.
		c.finishing = false
		if cancel != nil {
			c.canceling = true
		}
		c.mu.Unlock()
		if cancel != nil {
			// clearAll deliberately does not signal waiters. Pair it with the
			// foreground cancellation so approval/ask waits always unblock.
			c.approval.clearAll()
			cancel()
		}
		if fireSessionEnd && started {
			c.hooks.SessionEnd(context.Background(), "other")
		}
		if c.jobs != nil {
			switch jobsMode {
			case closeJobsAsync:
				c.jobs.CloseAsync()
			default:
				c.jobs.Close() // cancel any still-running background jobs
			}
		}
		if c.cleanup != nil {
			c.cleanup()
		}
		if c.bgCancel != nil {
			// Cancel in-flight background slash commands so their sessions ops
			// fail fast at beginRotation and the summarizer unwinds.
			c.bgCancel()
		}
	})
	// Wait for the background command goroutines so a late /new /compact /clear
	// cannot mutate the session after Close returned (and after the session
	// lease has been released by the caller).
	c.waitForBackgroundCommands()
}
