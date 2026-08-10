package checkpoint

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// MutationBarrier provides exclusive workspace mutation access for rewind
// transactions. It is intentionally separate from App.mu / Controller locks so
// file I/O never runs under those mutexes.
//
// Writers call EnterWrite / EnterWriteCtx / ExitWrite around mutations.
// Rewind holds EnterExclusive for the whole prepare+commit critical section.
type MutationBarrier struct {
	mu        sync.Mutex
	writers   int
	exclusive bool
	// generation increments on every exclusive release so prepare tokens can
	// detect concurrent mutation without relying on wall-clock time.
	generation atomic.Uint64
	// closed rejects new enters after shutdown (optional).
	closed bool
	// changed is closed and replaced on every state transition so waiters can
	// select on it against a context instead of blocking in cond.Wait forever.
	changed chan struct{}
}

// NewMutationBarrier returns a ready barrier.
func NewMutationBarrier() *MutationBarrier {
	return &MutationBarrier{changed: make(chan struct{})}
}

// Generation returns the current exclusive-release generation.
func (b *MutationBarrier) Generation() uint64 {
	if b == nil {
		return 0
	}
	return b.generation.Load()
}

// wakeLocked releases every waiter blocked in waitLocked. Caller holds b.mu.
func (b *MutationBarrier) wakeLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// waitLocked blocks until pred reports false, ctx is cancelled, or the barrier
// is closed. Caller holds b.mu; the lock is released while waiting and
// re-acquired before returning.
func (b *MutationBarrier) waitLocked(ctx context.Context, pred func() bool) error {
	for pred() {
		if b.closed {
			return fmt.Errorf("mutation barrier closed")
		}
		changed := b.changed
		b.mu.Unlock()
		var err error
		if ctx != nil {
			select {
			case <-changed:
			case <-ctx.Done():
				err = ctx.Err()
			}
		} else {
			<-changed
		}
		b.mu.Lock()
		if err != nil {
			return err
		}
	}
	return nil
}

// EnterWrite blocks until exclusive access is free, then increments the writer
// count. It is EnterWriteCtx with no cancellation.
func (b *MutationBarrier) EnterWrite() error {
	return b.EnterWriteCtx(context.Background())
}

// EnterWriteCtx blocks until exclusive access is free, then increments the
// writer count. The wait can be cancelled through ctx: a rewind commit holding
// exclusive access across slow I/O no longer wedges writer tools that honour
// their context.
func (b *MutationBarrier) EnterWriteCtx(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.waitLocked(ctx, func() bool { return b.exclusive || b.closed }); err != nil {
		return err
	}
	b.writers++
	return nil
}

// TryEnterWrite is a non-blocking EnterWrite.
func (b *MutationBarrier) TryEnterWrite() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exclusive || b.closed {
		return false
	}
	b.writers++
	return true
}

// ExitWrite decrements the writer count and advances the workspace generation.
// Plans prepared before a completed writer can therefore never authorize a
// later commit without a fresh preview.
func (b *MutationBarrier) ExitWrite() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writers > 0 {
		b.writers--
		b.generation.Add(1)
	}
	if b.writers == 0 {
		b.wakeLocked()
	}
}

// EnterExclusive waits until no writers hold the barrier, then takes exclusive.
func (b *MutationBarrier) EnterExclusive() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.waitLocked(context.Background(), func() bool { return b.exclusive || b.writers > 0 || b.closed }); err != nil {
		return err
	}
	b.exclusive = true
	return nil
}

// TryEnterExclusive is a non-blocking EnterExclusive.
func (b *MutationBarrier) TryEnterExclusive() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exclusive || b.writers > 0 || b.closed {
		return false
	}
	b.exclusive = true
	return true
}

// ExitExclusive releases exclusive access and bumps generation.
func (b *MutationBarrier) ExitExclusive() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.exclusive = false
	b.generation.Add(1)
	b.wakeLocked()
}

// Busy reports whether exclusive is held or writers are active.
func (b *MutationBarrier) Busy() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exclusive || b.writers > 0
}

// Close rejects future enters (best-effort shutdown).
func (b *MutationBarrier) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.closed = true
	b.wakeLocked()
	b.mu.Unlock()
}
