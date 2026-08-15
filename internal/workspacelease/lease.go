// Package workspacelease serializes Delivery writers that target the same
// workspace. Readers never acquire a lease. A writer keeps its lease from the
// first mutation until every participating agent run and background job has
// finished, so review and verification cannot be invalidated by another
// Delivery session changing the workspace mid-turn.
package workspacelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"corvus/internal/filelock"
)

// DefaultRetainGrace bounds how long RetainUntil keeps the write lease for a
// background job that never reports completion. When it fires, the hold is
// released and the leak is counted and logged; the job itself keeps running.
const DefaultRetainGrace = 5 * time.Minute

// DefaultAcquireTimeout bounds how long AcquireWrite waits for the write lease
// before failing. It is a conservative safety net for a peer session that
// wedged while holding the lock; healthy acquisitions complete in milliseconds.
const DefaultAcquireTimeout = 10 * time.Minute

// WaitNotice is called once when an acquisition cannot complete immediately.
// It must return quickly and must not call back into Owner.
type WaitNotice func()

// Owner is one Delivery session's re-entrant workspace lease. One Owner may be
// shared by the root agent and all of its subagents. Different sessions must
// use different Owners, even when they share a workspace.
// Option configures an Owner.
type Option func(*Owner)

// WithRetainGrace bounds how long RetainUntil keeps an acquired lease for a
// background job whose completion is never observed. A non-positive value
// waits forever (legacy behavior). Defaults to DefaultRetainGrace.
func WithRetainGrace(d time.Duration) Option {
	return func(o *Owner) {
		if d > 0 {
			o.retainGrace = d
		}
	}
}

// WithAcquireTimeout bounds how long AcquireWrite waits for the write lease.
// A non-positive value waits forever (legacy behavior). Defaults to
// DefaultAcquireTimeout.
func WithAcquireTimeout(d time.Duration) Option {
	return func(o *Owner) {
		if d > 0 {
			o.acquireTimeout = d
		}
	}
}

type Owner struct {
	lockPath string
	onWait   WaitNotice

	retainGrace    time.Duration
	acquireTimeout time.Duration

	mu            sync.Mutex
	activeRuns    int
	background    int
	acquired      bool
	acquiring     bool
	waiting       bool
	acquireDone   chan struct{}
	releaseSystem func()
	// leaked counts RetainUntil holds released by the grace timer instead of
	// the job's completion.
	leaked atomic.Uint64
}

// State is a sanitized process-local snapshot used by Desktop to explain a
// workspace conflict. It deliberately contains no path, PID, or lock token.
type State struct {
	Acquired bool
	Waiting  bool
}

// State returns the current acquisition state without performing lease I/O.
func (o *Owner) State() State {
	if o == nil {
		return State{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return State{Acquired: o.acquired, Waiting: o.waiting}
}

// New returns a Delivery-session lease owner for workspaceRoot. lockDir must be
// shared by Corvus processes for cross-process protection; it is kept outside
// the workspace so acquiring a lease never dirties user files.
func New(workspaceRoot, lockDir string, onWait WaitNotice, opts ...Option) (*Owner, error) {
	canonical, err := CanonicalWorkspace(workspaceRoot)
	if err != nil {
		return nil, err
	}
	lockDir = strings.TrimSpace(lockDir)
	if lockDir == "" {
		return nil, errors.New("workspace lease directory is unavailable")
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace lease directory: %w", err)
	}
	sum := sha256.Sum256([]byte(canonical))
	key := hex.EncodeToString(sum[:])

	o := &Owner{
		lockPath:       filepath.Join(lockDir, key+".lock"),
		onWait:         onWait,
		retainGrace:    DefaultRetainGrace,
		acquireTimeout: DefaultAcquireTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o, nil
}

// CanonicalWorkspace returns the stable identity used to key a workspace. It
// resolves symlinks when possible and folds case on Windows, where paths are
// case-insensitive by default.
func CanonicalWorkspace(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("workspace root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	abs = filepath.Clean(abs)
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = filepath.Clean(resolved)
	} else if !os.IsNotExist(resolveErr) {
		return "", fmt.Errorf("canonicalize workspace root: %w", resolveErr)
	}
	abs = nearestGitWorktreeRoot(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(filepath.ToSlash(abs))
	}
	return abs, nil
}

// nearestGitWorktreeRoot folds a repository root and any selected directory
// beneath it into one writer domain. It intentionally detects the .git marker
// through the filesystem instead of invoking Git, so the no-Git Windows path
// keeps the same safety guarantee. Linked worktrees each have their own .git
// marker and therefore remain independent writer domains.
func nearestGitWorktreeRoot(path string) string {
	start := path
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		start = filepath.Dir(path)
	}
	for current := start; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
	}
}

// BeginRun registers an agent run that participates in this session. The call
// is intentionally cheap and does not acquire the write lease; read-only turns
// therefore remain fully concurrent.
func (o *Owner) BeginRun() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.activeRuns++
	o.mu.Unlock()
}

// EndRun releases the lease after the final participating run and retained
// background job finishes.
func (o *Owner) EndRun() {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.activeRuns > 0 {
		o.activeRuns--
	}
	release := o.releaseIfIdleLocked()
	o.mu.Unlock()
	if release != nil {
		release()
	}
}

// AcquireWrite lazily acquires this session's exclusive write lease. It is
// re-entrant across parallel tool calls and shared subagents.
func (o *Owner) AcquireWrite(ctx context.Context) error {
	if o == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if o.acquireTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.acquireTimeout)
		defer cancel()
	}
	for {
		o.mu.Lock()
		if o.acquired {
			o.mu.Unlock()
			return nil
		}
		if o.acquiring {
			done := o.acquireDone
			o.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		o.acquiring = true
		o.acquireDone = make(chan struct{})
		done := o.acquireDone
		o.mu.Unlock()

		release, err := o.acquire(ctx)
		o.mu.Lock()
		o.acquiring = false
		o.waiting = false
		if err == nil {
			o.acquired = true
			o.releaseSystem = release
		}
		close(done)
		releaseIfIdle := o.releaseIfIdleLocked()
		o.mu.Unlock()
		if releaseIfIdle != nil {
			releaseIfIdle()
		}
		return err
	}
}

// RetainUntil keeps an already-acquired lease alive for a background job. It
// is a no-op when this session has not acquired the workspace, which preserves
// concurrency for background readers.
func (o *Owner) RetainUntil(done <-chan struct{}) {
	if o == nil || done == nil {
		return
	}
	o.mu.Lock()
	if !o.acquired {
		o.mu.Unlock()
		return
	}
	o.background++
	o.mu.Unlock()
	go func() {
		var timer *time.Timer
		var timeout <-chan time.Time
		if o.retainGrace > 0 {
			timer = time.NewTimer(o.retainGrace)
			timeout = timer.C
		}
		select {
		case <-done:
			if timer != nil {
				timer.Stop()
			}
		case <-timeout:
			// The job never reported completion. Release the lease hold anyway
			// (bounded by retainGrace) and count the leak so operators can see
			// background jobs that outlive their lease.
			o.leaked.Add(1)
			slog.Warn("workspace lease: background job exceeded retain grace; releasing lease hold",
				"grace", o.retainGrace)
		}
		o.mu.Lock()
		if o.background > 0 {
			o.background--
		}
		release := o.releaseIfIdleLocked()
		o.mu.Unlock()
		if release != nil {
			release()
		}
	}()
}

// LeakedRetentions reports how many RetainUntil holds were released by the
// grace timer because the retaining background job never reported completion.
func (o *Owner) LeakedRetentions() uint64 {
	if o == nil {
		return 0
	}
	return o.leaked.Load()
}

func (o *Owner) releaseIfIdleLocked() func() {
	if !o.acquired || o.acquiring || o.activeRuns != 0 || o.background != 0 {
		return nil
	}
	release := o.releaseSystem
	o.acquired = false
	o.releaseSystem = nil
	return release
}

func (o *Owner) acquire(ctx context.Context) (func(), error) {
	// One shared lock implementation (ADR-0006): filelock serializes this
	// process and the cross-process file; the wait hook carries the lease's
	// "busy" notice and Waiting state once, when acquisition cannot complete
	// immediately.
	waitHook := func() {
		o.mu.Lock()
		o.waiting = true
		o.mu.Unlock()
		if o.onWait != nil {
			o.onWait()
		}
	}
	release, err := filelock.Acquire(ctx, o.lockPath, filelock.WithWaitHook(waitHook))
	if err != nil {
		return nil, fmt.Errorf("acquire workspace write lease: %w", err)
	}
	return release, nil
}
