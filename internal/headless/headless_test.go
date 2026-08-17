package headless

import (
	"os"
	"path/filepath"
	"testing"

	"corvus/internal/event"
)

func TestParsePermissionMode(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"auto", "auto", true},
		{"", "auto", true},
		{"dontAsk", "dontAsk", true},
		{"dont-ask", "dontAsk", true},
		{"DONTASK", "dontAsk", true},
		{"yolo", "yolo", true},
		{"bypass-permissions", "yolo", true},
		{"ask", "ask", true},
		{"manual", "ask", true},
		{"bogus", "", false},
	}
	for _, tt := range tests {
		got, err := parsePermissionMode(tt.in)
		if (err == nil) != tt.ok {
			t.Errorf("parsePermissionMode(%q) err = %v, want ok=%v", tt.in, err, tt.ok)
			continue
		}
		if tt.ok && got != tt.want {
			t.Errorf("parsePermissionMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseProfile(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"balanced", "full", true},
		{"", "full", true},
		{"economy", "economy", true},
		{"delivery", "delivery", true},
		{"ECONOMY", "economy", true},
		{"bogus", "", false},
	}
	for _, tt := range tests {
		got, err := parseProfile(tt.in)
		if (err == nil) != tt.ok {
			t.Errorf("parseProfile(%q) err = %v, want ok=%v", tt.in, err, tt.ok)
			continue
		}
		if tt.ok && got != tt.want {
			t.Errorf("parseProfile(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRunUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"help", []string{"--help"}, exitOK},
		{"version", []string{"--version"}, exitOK},
		{"unknown flag", []string{"--bogus"}, exitUsage},
		{"bad format", []string{"--format", "xml", "hi"}, exitUsage},
		{"bad permission mode", []string{"--permission-mode", "bogus", "hi"}, exitUsage},
		{"bad profile", []string{"--profile", "bogus", "hi"}, exitUsage},
		{"negative max steps", []string{"--max-steps", "-1", "hi"}, exitUsage},
		{"no prompt", []string{}, exitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := Run(tt.args, "test"); code != tt.code {
				t.Errorf("Run(%v) = %d, want %d", tt.args, code, tt.code)
			}
		})
	}
}

func TestResolveSessionQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260101-000000.000000000-session.jsonl")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveSessionQuery(dir, path); err != nil || got != path {
		t.Fatalf("resolveSessionQuery(existing path) = %q, %v; want %q, nil", got, err, path)
	}
	if got, err := resolveSessionQuery(dir, "no-such-session"); err == nil || got != "" {
		t.Fatalf("resolveSessionQuery(missing) = %q, %v; want error", got, err)
	}
}

func TestCompactArgsAndToolResult(t *testing.T) {
	if got := compactArgs("  a\tb\nc "); got != "a b c" {
		t.Errorf("compactArgs = %q, want %q", got, "a b c")
	}
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	if got := compactArgs(string(long)); len(got) != 160 {
		t.Errorf("compactArgs(long) length = %d, want 160", len(got))
	}
	if got := summarizeToolResult(mustEvent("bash", "", "", false, 0)); got != "← bash ok" {
		t.Errorf("summarizeToolResult(ok) = %q, want %q", got, "← bash ok")
	}
	if got := summarizeToolResult(mustEvent("bash", "", "boom\nsecond", false, 1500)); got != "← bash err: boom (1.5s)" {
		t.Errorf("summarizeToolResult(err) = %q", got)
	}
	if got := summarizeToolResult(mustEvent("read_file", "", "", true, 0)); got != "← read_file ok (truncated)" {
		t.Errorf("summarizeToolResult(truncated) = %q", got)
	}
}

func mustEvent(name, out, errText string, truncated bool, durMs int64) event.Event {
	return event.Event{
		Kind: event.ToolResult,
		Tool: event.Tool{Name: name, Output: out, Err: errText, Truncated: truncated, DurationMs: durMs},
	}
}
