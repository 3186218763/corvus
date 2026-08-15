package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func openAppend(t *testing.T, path string, seed string) *os.File {
	t.Helper()
	if seed != "" {
		if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// A torn trailing line gets a separator newline so the next append starts a
// fresh line instead of fusing into the damaged one.
func TestEnsureTrailingNewlineRepairsTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	f := openAppend(t, path, `{"a":1}
{"b":2`)

	if err := EnsureTrailingNewline(f); err != nil {
		t.Fatalf("EnsureTrailingNewline: %v", err)
	}
	if _, err := f.Write([]byte(`{"c":3}` + "\n")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"a\":1}\n{\"b\":2\n{\"c\":3}\n"
	if string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

// An intact file and an empty file are both left byte-identical.
func TestEnsureTrailingNewlineNoopsWhenIntactOrEmpty(t *testing.T) {
	dir := t.TempDir()
	intact := openAppend(t, filepath.Join(dir, "intact.jsonl"), "{\"a\":1}\n")
	if err := EnsureTrailingNewline(intact); err != nil {
		t.Fatalf("intact: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "intact.jsonl"))
	if string(got) != "{\"a\":1}\n" {
		t.Fatalf("intact file changed: %q", got)
	}

	empty := openAppend(t, filepath.Join(dir, "empty.jsonl"), "")
	if err := EnsureTrailingNewline(empty); err != nil {
		t.Fatalf("empty: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "empty.jsonl"))
	if len(got) != 0 {
		t.Fatalf("empty file changed: %q", got)
	}
}
