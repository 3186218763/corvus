package jobs

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"corvus/internal/event"
)

// completionGatedSink blocks only the closing completion Notice until
// unblocked, simulating a stalled event consumer (full TUI channel) at the
// exact point that used to wedge the job goroutine before close(j.done).
type completionGatedSink struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *completionGatedSink) Emit(ev event.Event) {
	if ev.Kind == event.Notice && strings.Contains(ev.Text, "background bash finished") {
		s.once.Do(func() { close(s.entered) })
		<-s.release
	}
}

// A stalled sink must not delay job completion: done closes and WaitForSession
// returns even though the closing Notice cannot be delivered yet.
func TestCompletionClosesDoneBeforeBlockingNoticeEmit(t *testing.T) {
	sink := &completionGatedSink{entered: make(chan struct{}), release: make(chan struct{})}
	m := NewManager(sink)
	defer func() {
		close(sink.release)
		m.Close()
	}()

	j := m.StartForSession("", "bash", "stalled sink", func(context.Context, io.Writer) (string, error) {
		return "finished", nil
	})

	// The completion Notice emit is now in flight (blocked) — but done must
	// already be closed and WaitForSession must return regardless.
	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("completion notice never reached the sink")
	}
	select {
	case <-j.done:
	case <-time.After(5 * time.Second):
		t.Fatal("job done channel not closed: completion notice emit blocked before close(j.done)")
	}

	res := m.WaitForSession(context.Background(), "", []string{j.ID}, 0)
	if len(res) != 1 || res[0].Status != Done {
		t.Fatalf("WaitForSession = %+v, want one Done result", res)
	}
	if note := m.DrainCompletedNoteForSession(""); note == "" || !strings.Contains(note, j.ID) {
		t.Fatalf("completion note = %q, want it queued before done closed", note)
	}
}
