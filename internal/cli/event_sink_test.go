package cli

import (
	"testing"
	"time"

	"corvus/internal/event"
)

// A stalled TUI consumer (full event channel) must not block an ordinary
// Notice emitter: the event is shed and counted.
func TestEventSinkDropsNoticeWhenChannelFull(t *testing.T) {
	ch := make(chan event.Event, 2)
	ch <- event.Event{Kind: event.Text, Text: "a"}
	ch <- event.Event{Kind: event.Text, Text: "b"}
	s := &eventSink{ch: ch}

	done := make(chan struct{})
	go func() {
		s.Emit(event.Event{Kind: event.Notice, Text: "background job finished"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Notice Emit blocked on a full channel (stalled consumer)")
	}
	if got := s.droppedEvents(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	if got := len(ch); got != 2 {
		t.Fatalf("channel length = %d, want 2 (no Notice enqueued)", got)
	}
}

// An ApprovalRequest must be delivered even when the channel is full: the
// run loop blocks on the answer, so dropping the prompt would wedge the turn.
func TestEventSinkDeliversApprovalWhenChannelFull(t *testing.T) {
	ch := make(chan event.Event, 1)
	ch <- event.Event{Kind: event.Text, Text: "stalled stream"}
	s := &eventSink{ch: ch}

	emitDone := make(chan struct{})
	go func() {
		s.Emit(event.Event{Kind: event.ApprovalRequest, Approval: event.Approval{ID: "a1"}})
		close(emitDone)
	}()

	// The consumer drains one event; the approval must arrive next.
	select {
	case got := <-ch:
		if got.Kind != event.Text {
			t.Fatalf("first drained event = %v, want the buffered Text", got.Kind)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("buffered event never drained")
	}
	select {
	case got := <-ch:
		if got.Kind != event.ApprovalRequest || got.Approval.ID != "a1" {
			t.Fatalf("delivered event = %+v, want ApprovalRequest a1", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ApprovalRequest was never delivered")
	}
	select {
	case <-emitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ApprovalRequest Emit did not complete after delivery")
	}
	if got := s.droppedEvents(); got != 0 {
		t.Fatalf("dropped = %d, want 0 (approvals must never shed)", got)
	}
}

// AskRequest follows the same reliable path as ApprovalRequest.
func TestEventSinkDeliversAskWhenChannelFull(t *testing.T) {
	ch := make(chan event.Event)
	s := &eventSink{ch: ch}

	emitDone := make(chan struct{})
	go func() {
		s.Emit(event.Event{Kind: event.AskRequest, Ask: event.Ask{ID: "q1"}})
		close(emitDone)
	}()
	select {
	case got := <-ch:
		if got.Kind != event.AskRequest || got.Ask.ID != "q1" {
			t.Fatalf("delivered event = %+v, want AskRequest q1", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskRequest was never delivered")
	}
	select {
	case <-emitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("AskRequest Emit did not complete after delivery")
	}
}
