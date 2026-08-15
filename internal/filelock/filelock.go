// Package filelock provides bounded, cross-process advisory file locks.
package filelock

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const retryInterval = 20 * time.Millisecond

// ErrHeld reports that another file descriptor currently owns the lock.
// Callers normally see their context error after Acquire's bounded retry loop.
var ErrHeld = errors.New("file lock held")

// AcquireOption tunes one Acquire call.
type AcquireOption func(*acquireOptions)

type acquireOptions struct {
	waitHook func()
}

// WithWaitHook registers fn to be called at most once, right after Acquire
// learns it cannot complete immediately — the in-process slot or the lock file
// is already held — and before it starts retrying. Use it to surface "waiting"
// UX; Acquire's own loop stays silent.
func WithWaitHook(fn func()) AcquireOption {
	return func(o *acquireOptions) {
		o.waitHook = fn
	}
}

type localLock struct {
	token chan struct{}
}

var localRegistry = struct {
	sync.Mutex
	locks map[string]*localLock
}{locks: map[string]*localLock{}}

// Acquire obtains an exclusive lock on path until the returned release
// function is called. It serializes both goroutines in this process and other
// Corvus processes, and never waits past ctx's deadline.
func Acquire(ctx context.Context, path string, opts ...AcquireOption) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var options acquireOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	notified := false
	notifyWait := func() {
		if notified || options.waitHook == nil {
			return
		}
		notified = true
		options.waitHook()
	}
	key, err := canonicalLockPath(path)
	if err != nil {
		return nil, err
	}
	localRegistry.Lock()
	local := localRegistry.locks[key]
	if local == nil {
		local = &localLock{token: make(chan struct{}, 1)}
		local.token <- struct{}{}
		localRegistry.locks[key] = local
	}
	localRegistry.Unlock()

	select {
	case <-local.token:
	default:
		notifyWait()
		select {
		case <-local.token:
		case <-ctx.Done():
			return nil, fmt.Errorf("acquire file lock: %w", ctx.Err())
		}
	}
	releaseLocal := func() { local.token <- struct{}{} }

	for {
		releaseFile, err := tryLockFile(key)
		if err == nil {
			var once sync.Once
			return func() {
				once.Do(func() {
					releaseFile()
					releaseLocal()
				})
			}, nil
		}
		if !errors.Is(err, ErrHeld) {
			releaseLocal()
			return nil, fmt.Errorf("acquire file lock: %w", err)
		}
		notifyWait()
		timer := time.NewTimer(retryInterval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			releaseLocal()
			return nil, fmt.Errorf("acquire file lock: %w", ctx.Err())
		}
	}
}

func canonicalLockPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("file lock path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve file lock path: %w", err)
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(filepath.ToSlash(abs))
	}
	return abs, nil
}
