package event

import (
	"sync"
	"sync/atomic"
	"time"

	"corvus/internal/evidence"
	"corvus/internal/nilutil"
)

// Sync wraps a Sink so concurrent Emit calls are serialized. The base Sink
// contract assumes serial emission — the agent's run loop emits one event at a
// time. Background jobs (internal/jobs) emit from their own goroutines, which can
// overlap a running turn's emission; wrapping the session sink once in Sync keeps
// the serial-Emit invariant every sink relies on (an SSE writer, a webview
// EventsEmit, a TUI channel) without each having to lock. A nil sink yields
// Discard.
//
// The wrapper holds its mutex only to enqueue/hand off in the serialization
// chain; the inner sink runs OUTSIDE the lock. A consumer that stalls inside an
// inner Emit (e.g. a full TUI event channel) therefore blocks only the emitter
// whose turn it is, never every emitter behind the global mutex. Ordinary
// events are shed (and counted) when the backlog exceeds maxSyncQueued or when
// their turn does not arrive within syncQueueWait, so one stalled consumer can
// never wedge unrelated emitters (background-job notices, usage telemetry).
// ApprovalRequest / AskRequest always enqueue and wait their turn: the run loop
// blocks on the frontend's answer, so dropping one would hang the turn. The
// same applies to TurnDone / CompactionDone — the frontend's turn-unlock
// signals.
func Sync(s Sink) Sink {
	if nilutil.IsNil(s) {
		return Discard
	}
	return &syncSink{inner: s}
}

const (
	// maxSyncQueued bounds how many ordinary events may wait behind a stalled
	// delivery. Critical events are never subject to this bound.
	maxSyncQueued = 64
	// syncQueueWait bounds how long an ordinary event waits for its turn behind
	// a stalled delivery before it is shed. Healthy operation hands off in
	// microseconds; only a genuinely stuck consumer trips this.
	syncQueueWait = 250 * time.Millisecond
)

// syncNode is one Emit waiting its turn in the serialization chain. ready is
// closed by the predecessor once this node may deliver.
type syncNode struct {
	event Event
	next  *syncNode
	ready chan struct{}
	// abandoned marks a waiter that timed out; handoff skips it.
	abandoned bool
}

type syncSink struct {
	mu      sync.Mutex
	inner   Sink
	head    *syncNode
	tail    *syncNode
	queued  int // waiters behind the head
	dropped atomic.Uint64
}

// Dropped returns how many ordinary events were shed because the serialization
// chain was backed up behind a stalled consumer.
func (s *syncSink) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

// criticalKind reports events that must never be shed: the run loop blocks on
// the frontend's answer to an approval, and the frontend's input gate unlocks
// only on TurnDone / CompactionDone. Dropping either class would hang or
// permanently lock the UI.
func criticalKind(e Event) bool {
	switch e.Kind {
	case ApprovalRequest, AskRequest, TurnDone, CompactionDone:
		return true
	}
	return false
}

func (s *syncSink) Emit(e Event) {
	critical := criticalKind(e)
	node := &syncNode{event: e, ready: make(chan struct{})}

	isHead := false
	s.mu.Lock()
	switch {
	case s.head == nil:
		s.head, s.tail = node, node
		isHead = true
	case critical || s.queued < maxSyncQueued:
		s.tail.next = node
		s.tail = node
		s.queued++
	default:
		s.dropped.Add(1)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if isHead {
		// Nobody precedes us: deliver immediately (the ready handoff is only
		// for waiters behind a busy head).
		s.deliver(node)
		return
	}
	if critical {
		<-node.ready
		s.deliver(node)
		return
	}
	select {
	case <-node.ready:
		s.deliver(node)
	case <-time.After(syncQueueWait):
		s.dropped.Add(1)
		s.abandon(node)
	}
}

// deliver runs the inner sink for the head of the chain. The lock is not held
// here, so a blocking consumer freezes only this emitter.
func (s *syncSink) deliver(node *syncNode) {
	s.inner.Emit(node.event)
	s.mu.Lock()
	if s.head == node {
		s.advanceLocked()
	}
	s.mu.Unlock()
}

// abandon marks node as timed out and, if it was promoted to head while timing
// out, hands the chain past it. An abandoned waiter that is still queued keeps
// its queued count until the skip loop in advanceLocked accounts for it, so
// each waiter is decremented exactly once.
func (s *syncSink) abandon(node *syncNode) {
	s.mu.Lock()
	node.abandoned = true
	if s.head == node {
		s.advanceLocked()
	}
	s.mu.Unlock()
}

// advanceLocked pops the delivered/abandoned head and wakes the next live
// waiter. Caller holds s.mu.
func (s *syncSink) advanceLocked() {
	s.head = s.head.next
	if s.head == nil {
		s.tail = nil
		return
	}
	for s.head.abandoned {
		s.head = s.head.next
		s.queued--
		if s.head == nil {
			s.tail = nil
			return
		}
	}
	s.queued--
	close(s.head.ready)
}

func (s *syncSink) RecordReadinessAudit(a evidence.ReadinessAudit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs, ok := s.inner.(ReadinessAuditSink); ok {
		rs.RecordReadinessAudit(a)
	}
}

func (s *syncSink) RecordTurnCompletion() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts, ok := s.inner.(TurnCompletionSink); ok {
		ts.RecordTurnCompletion()
	}
}

func (s *syncSink) RecordProtocolRecovery(a ProtocolRecoveryAudit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs, ok := s.inner.(ProtocolRecoveryAuditSink); ok {
		rs.RecordProtocolRecovery(a)
	}
}
