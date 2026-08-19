package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"corvus/internal/agent"
	"corvus/internal/event"
	"corvus/internal/fileutil"
	"corvus/internal/guardian"
	"corvus/internal/jobs"
	"corvus/internal/runtimepolicy"
	"corvus/internal/store"
)

// beginRotation claims the session-rotation gate. It fails if a turn is running
// or another rotation is already in progress, so the caller holds exclusive
// rights to swap the executor session from the check here through endRotation.
// This closes the TOCTOU window that a bare `if c.running` check left open:
// between that check and the actual SetSession, a turn could start and then be
// yanked out from under the run loop.
func (c *Controller) beginRotation() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errControllerClosed
	}
	if c.running || c.finishing {
		return errTurnRunningRotation
	}
	if c.rotating {
		return errRotationInProgress
	}
	c.rotating = true
	return nil
}

// isClosed reports whether the controller has been torn down.
func (c *Controller) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Controller) endRotation() {
	c.mu.Lock()
	c.rotating = false
	c.mu.Unlock()
}

// NewSession snapshots the current conversation, rotates to a fresh file, and
// resets the executor to a clean session carrying the same system prompt. It
// ends the old session and starts the new one for lifecycle hooks.
func (c *Controller) NewSession() error {
	if c.executor == nil {
		return nil
	}
	// Claim the rotation gate for the whole snapshot-then-swap sequence. A bare
	// `if c.running` check released before Snapshot() left a window where a turn
	// could start during the snapshot and then have its live session replaced by
	// the SetSession below. Submit ("/new") and the bot gateway call this
	// asynchronously, so the gate is load-bearing, not defensive.
	if err := c.beginRotation(); err != nil {
		return err
	}
	defer c.endRotation()
	// Retire asynchronous recovery writes before Snapshot publishes the final
	// old-session checkpoint. Otherwise an earlier write can outlive the path
	// rotation (or process teardown) and race cleanup of the old session.
	oldPath := c.SessionPath()
	c.flushRecoveryPersistence(oldPath)
	if err := c.Snapshot(); err != nil {
		return err
	}
	c.hooks.SessionEnd(context.Background(), "clear")
	c.hooks.SetAuditLog(store.SessionHookLog(c.SessionPath()))
	// Hold snapshotMu across the swap so an in-flight save cannot pair the old
	// path with the fresh session (or the fresh path with the old session).
	c.snapshotMu.Lock()
	if c.sessionDir != "" {
		c.mu.Lock()
		c.sessionPath = agent.NewSessionPath(c.sessionDir, c.label)
		c.guardianPath = guardian.PathFor(c.sessionPath)
		c.mu.Unlock()
	}
	c.setActiveJobSession(c.SessionPath())
	c.executor.BindSession(agent.NewSession(c.systemPrompt), c.sessionPath)
	if c.guardianSess != nil {
		c.guardianSess.Reset()
	}
	c.ResetPlannerSession()
	freshPath := c.SessionPath()
	c.rebindCheckpoints(freshPath)
	c.refreshSessionCacheID(freshPath)
	c.resetRecoveryForNewSession(freshPath)
	c.snapshotMu.Unlock()
	// A new session starts with no active goal: without this, a running goal's
	// text kept injecting into the fresh session's first turns. The old
	// session's goal-state sidecar was persisted before the rotation and stays
	// intact, so resuming it restores its goal; the cleared state below lands
	// on the NEW path (rebindCheckpoints just moved it).
	c.ClearGoal()
	c.mu.Lock()
	c.startedOnce = true // NewSession fires SessionStart itself; don't re-fire on the next turn
	c.mu.Unlock()
	c.hooks.SetSessionID(c.parentSessionID())
	c.hooks.SetAuditLog(store.SessionHookLog(c.SessionPath()))
	c.enqueueHookContexts(c.hooks.SessionStart(context.Background(), "clear"))
	return nil
}

// ClearSession discards the current conversation without preserving it in
// resume/history, then rotates to a clean session carrying the same system prompt.
func (c *Controller) ClearSession() error {
	if c.executor == nil {
		return nil
	}
	// Same rotation gate as NewSession: hold it across the whole
	// destroy-then-swap so a turn cannot start during the sequence and have its
	// live session replaced.
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return fmt.Errorf("cannot clear while a turn is running")
		}
		return err
	}
	defer c.endRotation()
	c.mu.Lock()
	oldPath := c.sessionPath
	c.mu.Unlock()
	preMarkedCleanup := c.hasUnfinishedSessionJobs(oldPath)
	if preMarkedCleanup {
		if err := agent.MarkCleanupPending(oldPath, "clear"); err != nil {
			return err
		}
	}
	// Retire the old recovery state before deleting its artifacts. Async gate
	// snapshots are path-bound, so wait for every already-scheduled old-path
	// write; otherwise one can recreate the sidecar after removeSessionArtifacts.
	c.loadRecoveryState("")
	c.flushRecoveryPersistence(oldPath)
	// Hold snapshotMu from artifact removal through the swap: a save slipping
	// in between would resurrect the just-removed transcript, and one that
	// overlapped the swap could pair the old path with the fresh session.
	c.snapshotMu.Lock()
	destroy := c.BeginDestroySession(oldPath)
	if !destroy.Async {
		if err := removeSessionArtifacts(oldPath); err != nil {
			destroy.Finish()
			c.snapshotMu.Unlock()
			return err
		}
		destroy.Finish()
	}
	c.hooks.SessionEnd(context.Background(), "clear")
	if c.sessionDir != "" {
		c.mu.Lock()
		c.sessionPath = agent.NewSessionPath(c.sessionDir, c.label)
		c.guardianPath = guardian.PathFor(c.sessionPath)
		c.mu.Unlock()
	}
	c.setActiveJobSession(c.SessionPath())
	c.executor.BindSession(agent.NewSession(c.systemPrompt), c.sessionPath)
	if c.guardianSess != nil {
		c.guardianSess.Reset()
	}
	c.ResetPlannerSession()
	freshPath := c.SessionPath()
	c.rebindCheckpoints(freshPath)
	c.refreshSessionCacheID(freshPath)
	c.resetRecoveryForNewSession(freshPath)
	c.snapshotMu.Unlock()
	// Same contract as NewSession: the fresh session starts with no active goal.
	c.ClearGoal()
	c.mu.Lock()
	c.startedOnce = true
	c.mu.Unlock()
	c.hooks.SetSessionID(c.parentSessionID())
	c.enqueueHookContexts(c.hooks.SessionStart(context.Background(), "clear"))
	if destroy.Async {
		go func() {
			result := destroy.Wait()
			if result.HasTimedOut() && destroy.WaitAll != nil {
				if err := agent.MarkCleanupPending(oldPath, "clear"); err != nil {
					c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "mark cleanup pending failed: " + err.Error()})
				}
				destroy.WaitAll()
			}
			if err := removeSessionArtifacts(oldPath); err != nil {
				c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "clear session cleanup failed: " + err.Error()})
			}
			destroy.Finish()
		}()
	}
	return nil
}

func (c *Controller) hasUnfinishedSessionJobs(sessionPath string) bool {
	if c.jobs == nil {
		return false
	}
	return c.jobs.HasUnfinishedForSession(agent.BranchID(sessionPath))
}

func removeSessionArtifacts(path string) error {
	if path == "" {
		return nil
	}
	if err := jobs.RemoveArtifacts(path); err != nil {
		return err
	}
	remove := []string{path}
	// Sidecars include the event log — the authoritative transcript. Leaving
	// it behind would both leak the cleared conversation and let LoadSession
	// resurrect it on the recycled path. The guardian transcript saves through
	// the same session layer, so its sidecars are swept too.
	remove = append(remove, store.SessionSidecarFiles(path)...)
	remove = append(remove, guardian.PathFor(path), guardian.CursorPathFor(path))
	remove = append(remove, store.SessionSidecarFiles(guardian.PathFor(path))...)
	for _, p := range remove {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if dir := ckptDir(path); dir != "" {
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if dir := store.SessionSpillDir(path); dir != "" {
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := agent.DeleteSubagentsByParent(filepath.Dir(path), agent.BranchID(path)); err != nil {
		return err
	}
	if err := agent.ClearCleanupPending(path); err != nil {
		return err
	}
	return nil
}

// ReconcileCleanupPending retries physical cleanup for logically removed
// sessions that were left behind by a previous process.
func ReconcileCleanupPending(dir string) error {
	return agent.ReconcileCleanupPending(dir, func(item agent.CleanupPendingInfo) error {
		return removeSessionArtifacts(item.SessionPath)
	})
}

// Resume seeds the session from a loaded transcript and pins the active file to
// its path so auto-save keeps appending there.
func (c *Controller) Resume(s *agent.Session, path string) {
	// See snapshotMu: the swap must not interleave with an in-flight save.
	// recoverInterruptedTurn and maybeColdResumePrune snapshot on their own,
	// so they stay outside the locked section (snapshotMu is not reentrant).
	c.snapshotMu.Lock()
	if c.executor != nil {
		c.executor.BindSession(s, path)
	}
	c.ResetPlannerSession()
	c.mu.Lock()
	c.sessionPath = path
	c.guardianPath = guardian.PathFor(path)
	c.mu.Unlock()
	c.setActiveJobSession(path)
	c.rebindCheckpoints(path)
	c.refreshSessionCacheID(path)
	c.goals.restoreFromState(path)
	if c.executor != nil {
		c.executor.RestoreDeliveryCheckpoint(c.goals.deliveryState())
	}
	c.restoreTerminalGoalTodos(path)
	c.loadGuardianSession()
	c.loadRecoveryState(path)
	c.snapshotMu.Unlock()
	c.recoverCheckpointTransactions()
	c.recoverInterruptedTurn(path)
	c.maybeColdResumePrune(path)
}

func (c *Controller) loadGuardianSession() {
	if c.guardianSess == nil {
		return
	}
	c.guardianSess.Reset()
	path := c.guardianPath
	if path == "" {
		return
	}
	if err := c.guardianSess.Load(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("controller: load guardian session", "err", err)
	}
}

// ResetPlannerSession clears the planner's conversation history so the next
// plan starts fresh. In dual-model (Plan+Execute) mode, this prevents stale
// planner output from a previous session or tab from contaminating the current
// executor's handoff. Safe to call on a single-model controller (no-op).
func (c *Controller) ResetPlannerSession() {
	runner, ok := c.runner.(plannerSessionResetter)
	if ok {
		runner.ResetPlannerSession()
	}
}

// cacheColdAfter approximates how long the provider keeps a prompt prefix
// cached. A session idle longer than this resumes against a cold cache, so a
// history rewrite at that moment costs no extra cache misses — it only shrinks
// the full-price first request. Deliberately conservative: too small burns a
// live cache (~4× the miss tokens, measured), too large only forgoes a prune.
// Tighten from benchmarks/cache-ttl-probe data, never below measured retention.
var cacheColdAfter = 24 * time.Hour

// maybeColdResumePrune elides stale tool results when a resumed session has
// been idle past the provider's cache retention, then persists the pruned
// transcript so the saved file and the prompt stay in sync.
func (c *Controller) maybeColdResumePrune(path string) {
	if c.disableColdResumePrune || c.executor == nil || path == "" {
		return
	}
	// Idle time comes from branch meta only — every session the controller has
	// ever snapshotted carries one. A meta-less transcript (e.g. a legacy import
	// not yet saved) skips the prune until its first snapshot creates the meta.
	m, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok || m.UpdatedAt.IsZero() {
		return
	}
	last := m.UpdatedAt
	if time.Since(last) < cacheColdAfter {
		return
	}
	st, err := c.executor.PruneStaleToolResults()
	if err != nil || st.Results == 0 {
		return
	}
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(
		"resumed after %s idle (provider cache expired) — elided %d stale tool results to cheapen the cold restart",
		time.Since(last).Round(time.Minute), st.Results)})
	if err := c.SnapshotRewrite(); err != nil {
		slog.Warn("controller: post-prune snapshot", "err", err)
	}
}

// Snapshot writes the executor's conversation to the active session file. No-op
// when the executor is absent or the session has never been used (no user
// interaction). Returns errNoSessionPath when there IS content but no resolved
// path, so a misconfigured deployment surfaces instead of dropping data.
// Called after every turn so a crash loses at most one in-flight prompt.
func (c *Controller) persistRuntimePolicy(path string) {
	if c == nil || strings.TrimSpace(path) == "" {
		return
	}
	rec := runtimepolicy.RecordFromRequest(c.runtimePolicyRequest)
	if err := agent.PersistSessionRuntimePolicy(path, rec, ""); err != nil {
		slog.Warn("controller: persist runtime policy", "path", path, "err", err)
	}
}

func (c *Controller) RuntimePolicyRequest() runtimepolicy.Request {
	if c == nil {
		return runtimepolicy.Request{}
	}
	return c.runtimePolicyRequest
}

func (c *Controller) RuntimePolicy() runtimepolicy.Policy {
	if c == nil {
		return runtimepolicy.Policy{}
	}
	return c.runtimePolicy
}

func applyRuntimePolicyMeta(dst *agent.BranchMeta, src agent.BranchMeta, c *Controller) {
	if dst == nil {
		return
	}
	if rec, ok := agent.SessionRuntimePolicy(src); ok {
		copied := rec
		dst.RuntimePolicy = &copied
		dst.TokenMode = ""
		return
	}
	if c == nil {
		return
	}
	rec := runtimepolicy.RecordFromRequest(c.runtimePolicyRequest)
	dst.RuntimePolicy = &rec
	dst.TokenMode = ""
}

func (c *Controller) Snapshot() error {
	return c.snapshot(false, false, false)
}

// SnapshotActivity writes the active conversation and marks the session as
// recently active. Use it only after a real user/model turn changes the
// transcript; switch/close snapshots should call Snapshot so they do not reorder
// recent-session pickers.
func (c *Controller) SnapshotActivity() error {
	return c.snapshot(true, false, false)
}

// SnapshotRewrite persists an intentional history rewrite, such as rewind or
// manual compaction. Ordinary autosave paths should use Snapshot so stale
// controllers cannot overwrite a newer transcript.
func (c *Controller) SnapshotRewrite() error {
	return c.snapshot(false, true, false)
}

// midTurnSnapshotInterval is atomic (nanoseconds) so a test shrinking it
// cannot race a previous test's still-parking autosave goroutine.
var midTurnSnapshotInterval atomic.Int64

func init() { midTurnSnapshotInterval.Store(int64(30 * time.Second)) }

// autosaveWhileRunning snapshots the session periodically while a turn runs,
// so an abrupt kill (SSH drop, force-quit) loses at most one interval of a
// long turn instead of all of it (#3772). Session.Save copies under the lock
// and replaces the file atomically, so racing the turn's appends is safe.
func (c *Controller) autosaveWhileRunning(ctx context.Context) {
	t := time.NewTicker(time.Duration(midTurnSnapshotInterval.Load()))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.snapshot(false, false, false); err != nil {
				slog.Warn("controller: mid-turn snapshot", "err", err)
			}
		}
	}
}

func (c *Controller) snapshot(markActivity, forceRewrite, shutdownRecovery bool) error {
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()

	c.mu.Lock()
	path := c.sessionPath
	modelRef := c.modelRef
	c.mu.Unlock()
	if c.executor == nil {
		return nil
	}
	s := c.executor.Session()
	if !s.HasContent() {
		// Nothing to persist yet (e.g. a fresh session with only a system
		// prompt) — staying quiet here is correct, not a data-loss path.
		return nil
	}
	if !s.HasSystemMessage() {
		// The session has user/assistant/tool messages but no leading system
		// prompt.  Persisting it would create a session file that, when
		// reloaded, has no agent-identity contract — the model falls back to
		// its training-data defaults, giving wrong answers to identity
		// queries ("who are you?").  Log the anomaly so the root cause
		// (typically an empty sysPrompt reaching NewSession) can be
		// diagnosed, then refuse to write a corrupted transcript.
		slog.Warn("controller: refusing to snapshot session with content but no system message",
			"label", c.Label(), "session_dir", c.SessionDir(), "message_count", len(s.Snapshot()))
		return nil
	}
	if path == "" {
		// There IS content but nowhere to write it: this silently dropped whole
		// bot conversations (#4414). Surface it loudly instead of returning nil
		// so the missing session path can be diagnosed and fixed at the source.
		slog.Warn("controller: session has content but no session path; conversation will not be persisted",
			"label", c.Label(), "session_dir", c.SessionDir())
		return errNoSessionPath
	}
	forceRewrite = forceRewrite || s.NeedsRewriteSave()
	var err error
	if forceRewrite {
		err = s.SaveRewrite(path)
	} else {
		err = s.SaveSnapshot(path)
		if errors.Is(err, agent.ErrSessionSnapshotConflict) {
			// The no-rewrite decision may already be stale: auto-compaction
			// can rewrite history between the decision and the write. Re-check
			// and retry once as an owned rewrite before treating the failure as
			// a real cross-runtime conflict.
			if s.NeedsRewriteSave() {
				forceRewrite = true
				err = s.SaveRewrite(path)
			}
		}
	}
	if err != nil {
		if shutdownRecovery && errors.Is(err, agent.ErrSessionFileLockHeld) {
			recoveredPath, recoverErr := c.recoverShutdownSnapshot(path, err)
			if recoverErr != nil {
				return recoverErr
			}
			path = recoveredPath
			s = c.executor.Session()
			err = nil
		}
	}
	if err != nil {
		if !errors.Is(err, agent.ErrSessionSnapshotConflict) {
			return err
		}
		recoveredPath, outcome, recoverErr := c.recoverSnapshotConflict(path, err, forceRewrite)
		if recoverErr != nil {
			if shutdownRecovery && errors.Is(recoverErr, agent.ErrSessionFileLockHeld) {
				recoveredPath, recoverErr = c.recoverShutdownSnapshot(path, recoverErr)
				if recoverErr != nil {
					return recoverErr
				}
				path = recoveredPath
				s = c.executor.Session()
			} else {
				return recoverErr
			}
		} else {
			if outcome == conflictDropped {
				return nil
			}
			// Whatever recovery did — adopted the disk transcript, force-saved
			// the depth-capped branch, or forked — the rewrite baseline lives on
			// the session object and was advanced by the save that succeeded, so
			// there is nothing to re-anchor here.
			path = recoveredPath
			s = c.executor.Session()
		}
	}
	// Persist guardian session so the prefix cache stays warm after restart.
	if c.guardianSess != nil {
		gp := c.guardianPath
		if gp != "" {
			if gerr := c.guardianSess.Save(gp); gerr != nil {
				slog.Warn("controller: guardian snapshot", "err", gerr)
			}
		}
	}
	// Persist recovery gate state so unresolved checkpoints survive restart.
	c.saveRecoveryState(path)
	// Record the listing-only sidecar fields (model, preview, user-turn count)
	// straight from the in-memory conversation, so the sidebar and resume picker
	// never have to decode the whole .jsonl just to show them. markActivity bumps
	// UpdatedAt exactly like the previous TouchBranchMeta did; false preserves it
	// like SetBranchModelPreserveUpdated. The single write subsumes the old
	// EnsureBranchMeta / SetBranchModel / TouchBranchMeta sequence.
	preview, turns := agent.SessionPreviewFromMessages(s.Snapshot())
	if err := agent.UpdateSessionMeta(path, modelRef, preview, turns, markActivity); err != nil {
		return err
	}
	c.persistRuntimePolicy(path)
	return nil
}

// snapshotConflictLogAttrs flattens a snapshot-conflict error into slog attrs.
// Field reports of #6069-class "session changed on disk" spam are only
// diagnosable when the logs say which trigger fired and what the revision
// ledger looked like, so every recoverSnapshotConflict outcome logs these.
func snapshotConflictLogAttrs(saveErr error, path, mode string) []any {
	attrs := []any{"path", path, "mode", mode}
	var conflict *agent.SessionSnapshotConflictError
	if errors.As(saveErr, &conflict) && conflict != nil {
		attrs = append(attrs,
			"kind", string(conflict.Kind),
			"disk_messages", conflict.ExistingMessages,
			"snapshot_messages", conflict.SnapshotMessages,
			"base_revision", conflict.BaseRevision,
			"disk_revision", conflict.DiskRevision,
		)
	}
	return attrs
}

type snapshotConflictDiagnostic struct {
	At               time.Time `json:"at"`
	BranchID         string    `json:"branch_id"`
	Mode             string    `json:"mode"`
	Outcome          string    `json:"outcome"`
	Kind             string    `json:"kind,omitempty"`
	DiskMessages     int       `json:"disk_messages,omitempty"`
	SnapshotMessages int       `json:"snapshot_messages,omitempty"`
	BaseRevision     int64     `json:"base_revision,omitempty"`
	DiskRevision     int64     `json:"disk_revision,omitempty"`
	RecoveryBranchID string    `json:"recovery_branch_id,omitempty"`
	ExistingRecovery bool      `json:"existing_recovery,omitempty"`
}

func appendSnapshotConflictDiagnostic(path, mode, outcome string, saveErr error, recoveryPath string, existing bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	rec := snapshotConflictDiagnostic{
		At:       time.Now(),
		BranchID: agent.BranchID(path),
		Mode:     mode,
		Outcome:  outcome,
	}
	var conflict *agent.SessionSnapshotConflictError
	if errors.As(saveErr, &conflict) && conflict != nil {
		rec.Kind = string(conflict.Kind)
		rec.DiskMessages = conflict.ExistingMessages
		rec.SnapshotMessages = conflict.SnapshotMessages
		rec.BaseRevision = conflict.BaseRevision
		rec.DiskRevision = conflict.DiskRevision
	}
	if recoveryPath != "" {
		rec.RecoveryBranchID = agent.BranchID(recoveryPath)
		rec.ExistingRecovery = existing
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	logPath := store.SessionConflictLog(path)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if fileutil.EnsureTrailingNewline(f) != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
}

// conflictOutcome is recoverSnapshotConflict's declared result. Callers act
// on it directly instead of re-deriving what happened from path or session
// pointer comparisons — the misclassification that broke the depth-cap
// rewrite baseline (#6120) hid in exactly that inference.
type conflictOutcome int

const (
	// conflictDropped: nothing was recovered and the disk transcript could
	// not be adopted; this snapshot was deliberately dropped.
	conflictDropped conflictOutcome = iota
	// conflictAdoptedDisk: the executor session object was replaced by the
	// newer disk transcript; adoptDiskSession already reset its baselines.
	conflictAdoptedDisk
	// conflictForceSavedBranch: recovery depth was exhausted and the same
	// in-memory session was force-saved onto the same branch; that save
	// advanced the session-owned rewrite baseline like any other full save.
	conflictForceSavedBranch
	// conflictForkedBranch: the same in-memory session moved to a freshly
	// forked recovery branch path.
	conflictForkedBranch
)

const recoveryDepthCapNoticeText = "repeated save conflicts were detected; saved the current conflict copy in place"

func (c *Controller) emitRecoveryDepthCapNotice(path string) {
	key := filepath.Clean(strings.TrimSpace(path))
	c.mu.Lock()
	if c.recoveryDepthCapNotices == nil {
		c.recoveryDepthCapNotices = make(map[string]bool)
	}
	if c.recoveryDepthCapNotices[key] {
		c.mu.Unlock()
		return
	}
	c.recoveryDepthCapNotices[key] = true
	c.mu.Unlock()
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: recoveryDepthCapNoticeText})
}

func (c *Controller) recoverSnapshotConflict(path string, saveErr error, forceRewrite bool) (string, conflictOutcome, error) {
	if c.executor == nil || strings.TrimSpace(path) == "" {
		return "", conflictDropped, saveErr
	}
	mode := "snapshot"
	if forceRewrite {
		mode = "rewrite"
	}
	logAttrs := snapshotConflictLogAttrs(saveErr, path, mode)
	if kind, ok := agent.SnapshotConflictKind(saveErr); ok && kind == agent.SessionSnapshotConflictStalePrefix {
		if c.adoptDiskSession(path) {
			appendSnapshotConflictDiagnostic(path, mode, "adopted_newer_disk_transcript", saveErr, "", false)
			slog.Warn("controller: snapshot conflict; adopted newer disk transcript", logAttrs...)
			c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: "session changed on disk; adopted the newer transcript"})
			return path, conflictAdoptedDisk, nil
		}
	}
	reason := "snapshot conflict"
	if forceRewrite {
		reason = "rewrite conflict"
	}
	req := SessionRecoveryRequest{OriginalPath: path, Reason: reason, Mode: mode}
	meta := agent.BranchMeta{}
	if c.sessionRecoveryMeta != nil {
		meta = c.sessionRecoveryMeta(req)
	}
	info, err := c.executor.Session().SaveRecoveryBranch(agent.RecoveryBranchOptions{
		OriginalPath: path,
		Reason:       reason,
		BranchMeta:   meta,
	})
	if err != nil {
		if errors.Is(err, agent.ErrSessionRecoveryDepthExceeded) {
			// Saves keep conflicting on recovery branches this runtime itself
			// created; forking again multiplies session files without
			// converging (#5993 reached 8 nested levels). This runtime is the
			// only writer of its own recovery branches, so force-writing the
			// transcript back onto the current branch keeps the data and
			// stops the chain.
			if forceErr := c.executor.Session().Save(path); forceErr != nil {
				return "", conflictDropped, fmt.Errorf("recovery chain depth exceeded; force save failed: %w", forceErr)
			}
			appendSnapshotConflictDiagnostic(path, mode, "recovery_depth_cap_force_saved", saveErr, path, false)
			slog.Warn("controller: snapshot conflict; recovery depth cap reached, force-saved onto current branch", logAttrs...)
			c.emitRecoveryDepthCapNotice(path)
			return path, conflictForceSavedBranch, nil
		}
		if errors.Is(err, agent.ErrSessionRecoveryNotNeeded) {
			if c.adoptDiskSession(path) {
				appendSnapshotConflictDiagnostic(path, mode, "recovery_not_needed_adopted_disk_transcript", saveErr, "", false)
				slog.Warn("controller: snapshot conflict; recovery not needed, adopted disk transcript", logAttrs...)
				c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
					Text: "session changed on disk; adopted the newer transcript (local changes already covered)"})
				return path, conflictAdoptedDisk, nil
			}
			// Nothing was recovered AND the disk transcript could not be
			// adopted: the snapshot is silently dropped. Leave a trace so
			// "my last turns vanished" reports can be tied to this path.
			appendSnapshotConflictDiagnostic(path, mode, "recovery_not_needed_adopt_failed", saveErr, "", false)
			slog.Warn("controller: snapshot conflict; recovery not needed but disk transcript could not be adopted", logAttrs...)
			return "", conflictDropped, nil
		}
		return "", conflictDropped, fmt.Errorf("recover stale session snapshot: %w", err)
	}
	if err := c.commitRecoveredSession(path, reason, info); err != nil {
		return "", conflictDropped, err
	}
	appendSnapshotConflictDiagnostic(path, mode, "forked_recovery_branch", saveErr, info.Path, info.Existing)
	slog.Warn("controller: snapshot conflict; forked recovery branch",
		append(logAttrs, "recovery", info.Path, "existing", info.Existing)...)
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
		Text: "session changed on disk; unsaved local transcript was saved as a conflict copy"})
	return info.Path, conflictForkedBranch, nil
}

func (c *Controller) recoverShutdownSnapshot(path string, saveErr error) (string, error) {
	if c.executor == nil || strings.TrimSpace(path) == "" {
		return "", saveErr
	}
	const reason = "shutdown session file lock timeout"
	req := SessionRecoveryRequest{OriginalPath: path, Reason: reason, Mode: "shutdown"}
	meta := agent.BranchMeta{}
	if c.sessionRecoveryMeta != nil {
		meta = c.sessionRecoveryMeta(req)
	}
	info, err := c.executor.Session().SaveShutdownRecoveryBranch(agent.RecoveryBranchOptions{
		OriginalPath: path,
		Reason:       reason,
		BranchMeta:   meta,
	})
	if err != nil {
		return "", fmt.Errorf("save shutdown recovery branch: %w", err)
	}
	if err := c.commitRecoveredSession(path, reason, info); err != nil {
		return "", err
	}
	appendSnapshotConflictDiagnostic(path, "shutdown", "forked_file_lock_recovery", saveErr, info.Path, info.Existing)
	slog.Warn("controller: shutdown snapshot lock timed out; forked recovery branch",
		"path", path, "recovery", info.Path, "existing", info.Existing)
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
		Text: "session file stayed busy during shutdown; unsaved transcript was saved as a recovery copy"})
	return info.Path, nil
}

func (c *Controller) commitRecoveredSession(originalPath, reason string, info agent.RecoveryBranchInfo) error {
	recoveryInfo := SessionRecoveryInfo{
		OriginalPath: originalPath,
		RecoveryPath: info.Path,
		Existing:     info.Existing,
		Reason:       reason,
		Meta:         info.Meta,
	}
	if onSessionRecovered := c.sessionRecoveredHandler(); onSessionRecovered != nil {
		if err := onSessionRecovered(recoveryInfo); err != nil {
			return fmt.Errorf("commit recovered session: %w", err)
		}
	}
	c.mu.Lock()
	c.sessionPath = info.Path
	c.guardianPath = guardian.PathFor(info.Path)
	c.mu.Unlock()
	c.setActiveJobSession(info.Path)
	c.rebindCheckpoints(info.Path)
	c.refreshSessionCacheID(info.Path)
	c.transplantInFlightTurnMarker(originalPath, info.Path)
	return nil
}

func (c *Controller) adoptDiskSession(path string) bool {
	loaded, err := agent.LoadSession(path)
	if err != nil || loaded == nil {
		return false
	}
	c.executor.BindSession(loaded, path)
	c.ResetPlannerSession()
	c.rebindCheckpoints(path)
	c.setActiveJobSession(path)
	return true
}

func (c *Controller) snapshotActivityIfChanged(startMessages int) {
	if c.messageCount() <= startMessages {
		return
	}
	if err := c.SnapshotActivity(); err != nil {
		slog.Warn("controller: activity snapshot", "err", err)
	}
}

// SetSessionPath rebinds auto-save without changing the current session
// preference. Callers creating a genuinely fresh conversation should use
// SetFreshSessionPath; callers resuming history should use Resume.
func (c *Controller) SetSessionPath(p string) {
	c.setSessionPath(p, false)
}

// SetFreshSessionPath binds a path that is known to belong to a newly-created
// session and samples the configured new-session recovery default.
func (c *Controller) SetFreshSessionPath(p string) {
	c.setSessionPath(p, true)
}

func (c *Controller) setSessionPath(p string, fresh bool) {
	// See snapshotMu: the swap must not interleave with an in-flight save.
	c.snapshotMu.Lock()
	c.mu.Lock()
	c.sessionPath = p
	c.guardianPath = guardian.PathFor(p)
	c.mu.Unlock()
	c.setActiveJobSession(p)
	c.rebindCheckpoints(p)
	c.refreshSessionCacheID(p)
	if fresh {
		c.resetRecoveryForNewSession(p)
	} else {
		c.loadRecoveryState(p)
	}
	c.snapshotMu.Unlock()
	c.persistRuntimePolicy(p)
	if !fresh {
		c.recoverCheckpointTransactions()
	}
}

// refreshSessionCacheID updates sticky prompt_cache_key SessionCacheID on the
// executor (and planner, when dual-model) to BranchID(path). Empty path clears
// the id so Resolve omits the key (headless / unbound).
func (c *Controller) refreshSessionCacheID(path string) {
	if c == nil {
		return
	}
	id := agent.BranchID(path)
	if c.executor != nil {
		c.executor.SetSessionCacheID(id)
	}
	if acc, ok := c.runner.(plannerAgentAccessor); ok {
		if pa := acc.PlannerAgent(); pa != nil {
			pa.SetSessionCacheID(id)
		}
	}
}

// SessionDestroyHandle separates waiting for cancelled jobs from ending the
// destroy window, so callers can move/delete persistent artifacts in between.
type SessionDestroyHandle struct {
	Wait    func() jobs.TeardownResult
	WaitAll func()
	Finish  func()
	Async   bool
}

// BeginDestroySession marks a session as leaving active use and cancels its
// background jobs. Call Wait before moving/deleting artifacts, then Finish after
// persistent cleanup/move work is complete.
func (c *Controller) BeginDestroySession(sessionPath string) SessionDestroyHandle {
	parentSession := agent.BranchID(sessionPath)
	if c.jobs == nil || parentSession == "" {
		wait := func() jobs.TeardownResult { return jobs.TeardownResult{} }
		noop := func() {}
		return SessionDestroyHandle{Wait: wait, WaitAll: noop, Finish: noop}
	}
	teardown := c.jobs.BeginDestroySession(parentSession)
	return SessionDestroyHandle{
		Wait: func() jobs.TeardownResult {
			return c.jobs.WaitTeardown(context.Background(), teardown, c.jobs.TeardownGrace())
		},
		WaitAll: func() {
			for _, ch := range teardown.DoneChannels() {
				<-ch
			}
		},
		Finish: func() {
			c.jobs.FinishDestroySession(parentSession)
		},
		Async: teardown.Async(),
	}
}

// IsDestroyingSession reports whether sessionPath is currently in the destroy
// window for this controller's job manager.
func (c *Controller) IsDestroyingSession(sessionPath string) bool {
	if c.jobs == nil {
		return false
	}
	return c.jobs.IsDestroying(agent.BranchID(sessionPath))
}

func (c *Controller) setActiveJobSession(sessionPath string) {
	if c.jobs != nil {
		c.jobs.SetActiveSessionPath(agent.BranchID(sessionPath), sessionPath)
	}
}

// SessionDir reports the directory new session files land in ("" disables
// persistence), so the caller can decide whether to mint a path.
func (c *Controller) SessionDir() string { return c.sessionDir }

// SessionPath reports the file the current conversation auto-saves to ("" when
// persistence is disabled), so a history view can mark the active session.
func (c *Controller) SessionPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionPath
}

func (c *Controller) parentSessionID() string {
	return agent.BranchID(c.SessionPath())
}
