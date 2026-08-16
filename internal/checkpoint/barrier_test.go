package checkpoint

import (
	"context"
	"errors"
	"testing"
	"time"
)

// EnterWriteCtx must return a cancellation error while exclusive is held,
// instead of waiting forever in cond.Wait.
func TestEnterWriteCtxCancelledWhileExclusive(t *testing.T) {
	b := NewMutationBarrier()
	if !b.TryEnterExclusive() {
		t.Fatal("TryEnterExclusive on a fresh barrier failed")
	}
	defer b.ExitExclusive()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.EnterWriteCtx(ctx) }()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("EnterWriteCtx = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EnterWriteCtx did not unblock on cancellation")
	}
	b.mu.Lock()
	writers := b.writers
	b.mu.Unlock()
	if writers != 0 {
		t.Fatalf("cancelled EnterWriteCtx leaked a writer hold (%d writers)", writers)
	}
}

// EnterWriteCtx must succeed once the exclusive hold is released, and
// EnterWrite must keep working as the non-cancellable alias.
func TestEnterWriteCtxUnblocksOnExclusiveRelease(t *testing.T) {
	b := NewMutationBarrier()
	if !b.TryEnterExclusive() {
		t.Fatal("TryEnterExclusive on a fresh barrier failed")
	}

	done := make(chan error, 1)
	go func() { done <- b.EnterWriteCtx(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("EnterWriteCtx returned while exclusive held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	b.ExitExclusive()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("EnterWriteCtx after release = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EnterWriteCtx did not unblock on exclusive release")
	}
	defer b.ExitWrite()

	if err := b.EnterWrite(); err != nil {
		t.Fatalf("EnterWrite = %v, want nil", err)
	}
	b.ExitWrite()
}

// RegisterWriter must not hold reg.mu while it waits on the barrier: with an
// exclusive hold in place, UnregisterWriter and ActiveWriters keep working.
func TestRegisterWriterDoesNotFreezeRegistryWhileWaiting(t *testing.T) {
	root := t.TempDir()
	s := New("", root)
	observer := NewMutationObserver(ObserverOptions{Store: s})
	barrier := s.Barrier()
	if !barrier.TryEnterExclusive() {
		t.Fatal("TryEnterExclusive on a fresh barrier failed")
	}

	registered := make(chan error, 1)
	go func() { registered <- observer.RegisterWriter("bg-1", "background_subagent", 0) }()

	// The writer becomes visible even though the barrier claim is pending.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if writers := observer.ActiveWriters(); len(writers) == 1 && writers[0].ID == "bg-1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bg-1 never became visible while the claim was pending")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// UnregisterWriter must not block on the pending RegisterWriter.
	unregistered := make(chan struct{})
	go func() {
		observer.UnregisterWriter("bg-1")
		close(unregistered)
	}()
	select {
	case <-unregistered:
	case <-time.After(5 * time.Second):
		t.Fatal("UnregisterWriter blocked behind a pending RegisterWriter")
	}
	if got := observer.ActiveWriters(); len(got) != 0 {
		t.Fatalf("ActiveWriters = %+v, want empty after UnregisterWriter", got)
	}

	barrier.ExitExclusive()
	select {
	case err := <-registered:
		if err != nil {
			t.Fatalf("RegisterWriter after exclusive release = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RegisterWriter did not complete after exclusive release")
	}
	// The registration completed after the preemptive unregister; clean up the
	// barrier hold it acquired.
	observer.UnregisterWriter("bg-1")
	if !barrier.TryEnterExclusive() {
		t.Fatal("barrier still busy after writers unregistered")
	}
	barrier.ExitExclusive()
}
