package event

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// A stalled consumer (inner sink blocked) must not freeze ordinary emitters:
// a Notice is shed after the queue wait while an in-flight ApprovalRequest is
// still delivered intact once the consumer recovers.
func TestSyncStalledConsumerShedsOrdinaryEventsButKeepsApproval(t *testing.T) {
	entered := make(chan struct{})
	released := make(chan struct{})
	received := make(chan Event, 4)
	inner := FuncSink(func(e Event) {
		entered <- struct{}{}
		<-released
		received <- e
	})
	s := Sync(inner).(*syncSink)

	approvalDone := make(chan struct{})
	go func() {
		s.Emit(Event{Kind: ApprovalRequest, Approval: Approval{ID: "a1"}})
		close(approvalDone)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("ApprovalRequest never reached the inner sink")
	}

	noticeDone := make(chan struct{})
	go func() {
		s.Emit(Event{Kind: Notice, Text: "background job finished"})
		close(noticeDone)
	}()
	select {
	case <-noticeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary Notice blocked behind the stalled consumer")
	}
	if s.Dropped() == 0 {
		t.Fatal("ordinary Notice was shed but not counted as dropped")
	}

	close(released)
	select {
	case <-approvalDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ApprovalRequest emit did not complete after the consumer recovered")
	}
	select {
	case e := <-received:
		if e.Kind != ApprovalRequest || e.Approval.ID != "a1" {
			t.Fatalf("delivered event = %+v, want ApprovalRequest a1", e)
		}
	default:
		t.Fatal("ApprovalRequest was never delivered")
	}
}

// With the ordinary backlog full, a Notice drops immediately while a critical
// ApprovalRequest is still accepted and delivered once the head unblocks.
func TestSyncBacklogBoundShedsOrdinaryButNotCritical(t *testing.T) {
	entered := make(chan struct{}, 4)
	released := make(chan struct{})
	received := make(chan Event, 4)
	inner := FuncSink(func(e Event) {
		entered <- struct{}{}
		<-released
		received <- e
	})
	s := Sync(inner).(*syncSink)

	go s.Emit(Event{Kind: ApprovalRequest, Approval: Approval{ID: "a1"}})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("head ApprovalRequest never reached the inner sink")
	}

	// Simulate the ordinary backlog at capacity behind the stalled head.
	s.mu.Lock()
	s.queued = maxSyncQueued
	s.mu.Unlock()

	noticeDone := make(chan struct{})
	go func() {
		s.Emit(Event{Kind: Notice, Text: "noise"})
		close(noticeDone)
	}()
	select {
	case <-noticeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Notice blocked instead of being shed by the full backlog")
	}
	if s.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", s.Dropped())
	}

	criticalDone := make(chan struct{})
	go func() {
		s.Emit(Event{Kind: ApprovalRequest, Approval: Approval{ID: "a2"}})
		close(criticalDone)
	}()
	close(released)
	select {
	case <-criticalDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ApprovalRequest was dropped or never delivered despite the full backlog")
	}
	got := map[string]bool{}
	for e := range received {
		got[e.Approval.ID] = true
		if len(got) == 2 {
			break
		}
	}
	if !got["a1"] || !got["a2"] {
		t.Fatalf("delivered approvals = %v, want a1 and a2", got)
	}
}

// Concurrent critical and ordinary emitters through a healthy sink: every
// ApprovalRequest must arrive (never shed), regardless of interleaving.
func TestSyncConcurrentApprovalsNotLost(t *testing.T) {
	received := make(chan Event, 256)
	s := Sync(FuncSink(func(e Event) { received <- e })).(*syncSink)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s.Emit(Event{Kind: ApprovalRequest, Approval: Approval{ID: fmt.Sprintf("a%d", i)}})
		}(i)
		go func() {
			defer wg.Done()
			s.Emit(Event{Kind: Notice, Text: "noise"})
		}()
	}
	wg.Wait()

	got := map[string]bool{}
	for len(got) < n {
		select {
		case e := <-received:
			if e.Kind == ApprovalRequest {
				got[e.Approval.ID] = true
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d/%d approvals delivered", len(got), n)
		}
	}
}
