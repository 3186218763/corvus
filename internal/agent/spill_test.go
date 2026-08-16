package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"corvus/internal/provider"
	"corvus/internal/spill"
)

func TestBoundToolResultSpillsOversizedOutput(t *testing.T) {
	a := &Agent{}
	dir := t.TempDir()
	a.SetSpillDir(dir)

	big := strings.Repeat("0123456789abcdef", maxToolOutputBytes/8)
	body, notice := a.boundToolResult("web_fetch", big)
	if notice == "" {
		t.Fatal("no spill notice for oversized output")
	}
	if !strings.Contains(body, dir) {
		t.Fatalf("body does not carry the spill locator: %q", body[:200])
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("spill dir entries = %v, err = %v", entries, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != big {
		t.Fatalf("spilled content differs: %d vs %d bytes", len(got), len(big))
	}
	// The model-facing head stays within the byte budget and points at retrieval.
	if len(body) > maxToolOutputBytes {
		t.Fatalf("model-facing body %d bytes exceeds budget %d", len(body), maxToolOutputBytes)
	}
	if !strings.Contains(body, "read_file") {
		t.Fatalf("body lacks retrieval guidance: %q", body)
	}
}

func TestBoundToolResultSmallOutputUntouched(t *testing.T) {
	a := &Agent{}
	a.SetSpillDir(t.TempDir())
	small := "short result"
	body, notice := a.boundToolResult("grep", small)
	if body != small || notice != "" {
		t.Fatalf("small output changed: body=%q notice=%q", body, notice)
	}
}

func TestBoundToolResultNoSpillDirFallsBackToTruncation(t *testing.T) {
	a := &Agent{}
	big := strings.Repeat("0123456789abcdef", maxToolOutputBytes/8)
	body, notice := a.boundToolResult("grep", big)
	if notice == "" || !strings.Contains(notice, "truncated") {
		t.Fatalf("expected truncation notice, got %q", notice)
	}
	if strings.Contains(body, "saved in full") {
		t.Fatalf("truncation path leaked spill text")
	}
}

type failingSpillStore struct{}

func (failingSpillStore) SaveText(dir, toolName, suggestedName, content string) (spill.Locator, error) {
	return spill.Locator{}, os.ErrPermission
}

func TestBoundToolResultSpillFailureFallsBackToTruncation(t *testing.T) {
	a := &Agent{spillStore: failingSpillStore{}}
	a.SetSpillDir(t.TempDir())
	big := strings.Repeat("0123456789abcdef", maxToolOutputBytes/8)
	body, notice := a.boundToolResult("grep", big)
	if notice == "" || !strings.Contains(notice, "truncated") {
		t.Fatalf("expected truncation notice after spill failure, got %q", notice)
	}
	if strings.Contains(body, "saved in full") {
		t.Fatalf("spill failure leaked locator text")
	}
}

func TestBindSessionSetsSpillDirFromSessionPath(t *testing.T) {
	a := &Agent{}
	path := filepath.Join(t.TempDir(), "sessions", "abc.jsonl")
	a.BindSession(NewSession(""), path)
	if got := a.currentSpillDirForTest(); got != path[:len(path)-len(".jsonl")]+".spill" {
		t.Fatalf("spill dir = %q", got)
	}
}

func (a *Agent) currentSpillDirForTest() string {
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	return a.spillDir
}

func TestVerifySessionEventRoundTrip(t *testing.T) {
	t.Setenv("CORVUS_SESSION_ASSERT", "1")
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "fix the bug"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := verifySessionEventRoundTrip(path, s.Snapshot()); err != nil {
		t.Fatalf("round trip mismatch after save: %v", err)
	}
	altered := append(s.Snapshot(), provider.Message{Role: provider.RoleUser, Content: "ghost"})
	if err := verifySessionEventRoundTrip(path, altered); err == nil {
		t.Fatal("round trip accepted messages that are not in the log")
	}
}
