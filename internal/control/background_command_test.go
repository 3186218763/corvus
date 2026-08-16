package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"corvus/internal/agent"
	"corvus/internal/event"
	"corvus/internal/provider"
	"corvus/internal/tool"
)

// gatedCompactProvider blocks inside the summarizer until the caller's context
// is cancelled, signalling each step so the test can order Close against the
// in-flight compaction deterministically.
type gatedCompactProvider struct {
	entered   chan struct{}
	cancelled chan struct{} // closed once Stream observes ctx cancellation
	exited    chan struct{} // closed once Stream returns
}

func (p *gatedCompactProvider) Name() string { return "gated" }

func (p *gatedCompactProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	defer close(p.exited)
	<-ctx.Done()
	close(p.cancelled)
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "summary"}
	close(ch)
	return ch, nil
}

func compactableSession() *agent.Session {
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "step one"})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "more"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "step two"})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "next"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "ok"})
	return sess
}

// A /compact submitted after Close must not rotate the session or write a
// snapshot: beginRotation fails on the closed flag before anything runs.
func TestSubmitCompactAfterCloseDoesNotRotateOrSnapshot(t *testing.T) {
	dir := t.TempDir()
	exec := agent.New(nil, tool.NewRegistry(), compactableSession(), agent.Options{}, event.Discard)
	path := filepath.Join(dir, "session.jsonl")
	c := New(Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, SessionPath: path, Label: "test"})
	c.Close()
	before := c.SessionPath()

	c.SubmitDisplay("", "/compact")
	c.bgWG.Wait()

	if got := c.SessionPath(); got != before {
		t.Fatalf("session path rotated after Close: %q != %q", got, before)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot file written by /compact after Close: %v", err)
	}
	if snap := exec.Session().Snapshot(); len(snap) != len(compactableSession().Snapshot()) {
		t.Fatalf("session mutated by /compact after Close: %d messages", len(snap))
	}
}

// A /new submitted after Close must not swap the session or snapshot it.
func TestSubmitNewAfterCloseDoesNotRotateSession(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "old context"})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	path := filepath.Join(dir, "session.jsonl")
	c := New(Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, SessionPath: path, Label: "test"})
	c.Close()
	before := c.SessionPath()

	c.SubmitDisplay("", "/new")
	c.bgWG.Wait()

	if got := c.SessionPath(); got != before {
		t.Fatalf("/new rotated the session after Close: %q != %q", got, before)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot file written by /new after Close: %v", err)
	}
	if snap := exec.Session().Snapshot(); len(snap) != 2 {
		t.Fatalf("session mutated by /new after Close: %+v", snap)
	}
}

// Close must cancel an in-flight /compact and wait for its goroutine: it may
// not return before the compaction observed cancellation, and the goroutine
// must be gone by the time Close returns.
func TestCloseWaitsForInFlightCompact(t *testing.T) {
	dir := t.TempDir()
	prov := &gatedCompactProvider{
		entered:   make(chan struct{}, 1),
		cancelled: make(chan struct{}),
		exited:    make(chan struct{}),
	}
	exec := agent.New(prov, tool.NewRegistry(), compactableSession(), agent.Options{RecentKeep: 2}, event.Discard)
	path := filepath.Join(dir, "session.jsonl")
	c := New(Options{Executor: exec, SystemPrompt: "sys", SessionDir: dir, SessionPath: path, Label: "test"})

	c.SubmitDisplay("", "/compact")
	select {
	case <-prov.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("compact never reached the summarizer")
	}

	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()

	// Close cancels before it waits: if Close returned without waiting for the
	// in-flight compact, this fires the closed branch.
	select {
	case <-closed:
		select {
		case <-prov.cancelled:
			// Cancellation was observed before Close returned — correct.
		default:
			t.Fatal("Close returned before the in-flight compact observed cancellation")
		}
	case <-prov.cancelled:
	}

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the in-flight compact unwound")
	}
	select {
	case <-prov.exited:
	default:
		t.Fatal("compact goroutine still running after Close returned")
	}
	// The compact's terminal SnapshotRewrite must have been skipped: the
	// controller was already closed when the summarizer unwound.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot written by in-flight /compact after Close: %v", err)
	}
}
