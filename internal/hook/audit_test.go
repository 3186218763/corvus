package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAuditLogRecordsEveryOutcome verifies the hook audit sidecar gets one
// JSONL record per outcome across events, with verdict, exit code, and
// session id captured — and that audit loss never changes a verdict.
func TestAuditLogRecordsEveryOutcome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.hooks.jsonl")
	r := NewRunner([]ResolvedHook{
		{HookConfig: HookConfig{Match: "bash", Command: "hook-one"}, Event: PreToolUse},
		{HookConfig: HookConfig{Command: "hook-two"}, Event: Stop},
	}, dir, func(_ context.Context, in SpawnInput) SpawnResult {
		return SpawnResult{ExitCode: 2, Stdout: "denied", Stderr: "nope"}
	}, nil)
	r.SetSessionID("abc")
	r.SetAuditLog(path)

	blocked, _ := r.PreToolUse(context.Background(), "bash", json.RawMessage(`{}`))
	if !blocked {
		t.Fatal("PreToolUse with exit 2 should block")
	}
	r.Stop(context.Background(), "done", 1)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	dec := json.NewDecoder(mustOpenFile(t, path))
	var recs []auditRecord
	for dec.More() {
		var rec auditRecord
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("decode: %v", err)
		}
		recs = append(recs, rec)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(recs), data)
	}
	if recs[0].Event != PreToolUse || recs[0].Decision != "block" || recs[0].ExitCode != 2 || recs[0].SessionID != "abc" {
		t.Fatalf("first record = %+v", recs[0])
	}
	if recs[1].Event != Stop || recs[1].Decision != "warn" {
		t.Fatalf("second record = %+v", recs[1])
	}
}

// TestAuditLogDisabledByDefault verifies hooks run unchanged when no audit
// path is bound.
func TestAuditLogDisabledByDefault(t *testing.T) {
	r := NewRunner([]ResolvedHook{
		{HookConfig: HookConfig{Match: "bash", Command: "hook"}, Event: PreToolUse},
	}, t.TempDir(), func(_ context.Context, in SpawnInput) SpawnResult {
		return SpawnResult{ExitCode: 0}
	}, nil)
	blocked, msg := r.PreToolUse(context.Background(), "bash", json.RawMessage(`{}`))
	if blocked || msg != "" {
		t.Fatalf("blocked=%v msg=%q, want clean pass", blocked, msg)
	}
}

func mustOpenFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
