package compaction

import (
	"strings"
	"testing"

	"corvus/internal/provider"
)

func user(c string) provider.Message { return provider.Message{Role: provider.RoleUser, Content: c} }
func tool(c, id string) provider.Message {
	return provider.Message{Role: provider.RoleTool, Content: c, ToolCallID: id}
}
func asst(calls ...provider.ToolCall) provider.Message {
	return provider.Message{Role: provider.RoleAssistant, Content: "done", ToolCalls: calls}
}

// TestPartitionFoldKeepsPriorDigestsAndPinnedUserTurns verifies the pure fold
// policy: prior summaries and small user turns stay verbatim, everything else
// folds, and policy-kept errors drag their tool-call group along.
func TestPartitionFoldKeepsPriorDigestsAndPinnedUserTurns(t *testing.T) {
	region := []provider.Message{
		user(SummaryTagOpen + "\nprior digest\n"),
		user("small fact"),
		asst(provider.ToolCall{ID: "c1", Name: "grep", Arguments: `{}`}),
		tool("error: boom", "c1"),
		user("large turn that folds"),
	}
	kept, fold := PartitionFold(region, KeepErrors, func(m provider.Message) bool { return m.Content == "small fact" })
	if len(kept) != 4 || len(fold) != 1 {
		t.Fatalf("kept=%d fold=%d, want 4/1", len(kept), len(fold))
	}
	if fold[0].Content != "large turn that folds" {
		t.Fatalf("folded = %q", fold[0].Content)
	}
}

func TestTailStartNeverBeginsWithOrphanToolResult(t *testing.T) {
	msgs := []provider.Message{
		user("first"),
		asst(provider.ToolCall{ID: "c1", Name: "grep", Arguments: `{}`}),
		tool("result", "c1"),
		user("recent"),
	}
	start := TailStart(msgs, 1, 1000, 0.25, 1)
	if start >= len(msgs) || msgs[start].Role == provider.RoleTool {
		t.Fatalf("tail starts at orphan tool result index %d", start)
	}
}

// --- ExtractFileSet ---

func TestExtractFileSetClassifiesReadsAndWrites(t *testing.T) {
	region := []provider.Message{
		user("read this file"),
		asst(
			provider.ToolCall{ID: "r1", Name: "read_file", Arguments: `{"path":"internal/foo.go"}`},
			provider.ToolCall{ID: "e1", Name: "edit_file", Arguments: `{"path":"internal/foo.go","old_string":"a","new_string":"b"}`},
			provider.ToolCall{ID: "w1", Name: "write_file", Arguments: `{"path":"internal/bar.go","content":"..."}`},
			provider.ToolCall{ID: "g1", Name: "grep", Arguments: `{"pattern":"TODO","path":"internal/"}`},
		),
		tool("ok", "r1"), tool("ok", "e1"), tool("ok", "w1"), tool("ok", "g1"),
	}
	fs := ExtractFileSet(region)
	if len(fs.Read) != 2 || len(fs.Modified) != 2 {
		t.Fatalf("read=%v modified=%v, want 2/2", fs.Read, fs.Modified)
	}
	// read_file and grep both contribute "internal/foo.go" and "internal/" —
	// deduplicated, first-seen order preserved.
	if fs.Read[0] != "internal/foo.go" {
		t.Errorf("first read = %q, want internal/foo.go", fs.Read[0])
	}
	if fs.Modified[0] != "internal/foo.go" {
		t.Errorf("first modified = %q, want internal/foo.go", fs.Modified[0])
	}
	if fs.Modified[1] != "internal/bar.go" {
		t.Errorf("second modified = %q, want internal/bar.go", fs.Modified[1])
	}
}

func TestExtractFileSetSkipsBashAndUnknownTools(t *testing.T) {
	region := []provider.Message{
		asst(
			provider.ToolCall{ID: "b1", Name: "bash", Arguments: `{"command":"cat internal/foo.go"}`},
			provider.ToolCall{ID: "u1", Name: "custom_tool", Arguments: `{"path":"internal/secret.go"}`},
		),
		tool("ok", "b1"), tool("ok", "u1"),
	}
	fs := ExtractFileSet(region)
	if len(fs.Read) != 0 || len(fs.Modified) != 0 {
		t.Fatalf("bash and unknown tools should not contribute: read=%v modified=%v", fs.Read, fs.Modified)
	}
}

func TestExtractFileSetMoveFileClassifiesBothPaths(t *testing.T) {
	region := []provider.Message{
		asst(provider.ToolCall{ID: "m1", Name: "move_file", Arguments: `{"source_path":"a.go","destination_path":"b.go"}`}),
		tool("ok", "m1"),
	}
	fs := ExtractFileSet(region)
	if len(fs.Read) != 1 || fs.Read[0] != "a.go" {
		t.Fatalf("read = %v, want [a.go]", fs.Read)
	}
	if len(fs.Modified) != 1 || fs.Modified[0] != "b.go" {
		t.Fatalf("modified = %v, want [b.go]", fs.Modified)
	}
}

func TestExtractFileSetDeduplicatesAndCleans(t *testing.T) {
	region := []provider.Message{
		asst(
			provider.ToolCall{ID: "r1", Name: "read_file", Arguments: `{"path":"./internal/foo.go"}`},
			provider.ToolCall{ID: "r2", Name: "read_file", Arguments: `{"path":"internal/foo.go"}`},
			provider.ToolCall{ID: "r3", Name: "read_file", Arguments: `{"path":"internal//foo.go"}`},
		),
		tool("ok", "r1"), tool("ok", "r2"), tool("ok", "r3"),
	}
	fs := ExtractFileSet(region)
	if len(fs.Read) != 1 {
		t.Fatalf("deduplicated read = %v, want 1 entry", fs.Read)
	}
	if fs.Read[0] != "internal/foo.go" {
		t.Errorf("cleaned = %q, want internal/foo.go", fs.Read[0])
	}
}

func TestFileSetMergeAccumulatesAcrossRounds(t *testing.T) {
	var fs FileSet
	fs.Merge(ExtractFileSet([]provider.Message{
		asst(provider.ToolCall{ID: "r1", Name: "read_file", Arguments: `{"path":"a.go"}`}),
		tool("ok", "r1"),
	}))
	fs.Merge(ExtractFileSet([]provider.Message{
		asst(provider.ToolCall{ID: "w1", Name: "write_file", Arguments: `{"path":"b.go","content":"..."}`}),
		tool("ok", "w1"),
	}))
	if len(fs.Read) != 1 || fs.Read[0] != "a.go" {
		t.Fatalf("accumulated read = %v, want [a.go]", fs.Read)
	}
	if len(fs.Modified) != 1 || fs.Modified[0] != "b.go" {
		t.Fatalf("accumulated modified = %v, want [b.go]", fs.Modified)
	}
	// A second read of a.go should not duplicate.
	fs.Merge(ExtractFileSet([]provider.Message{
		asst(provider.ToolCall{ID: "r2", Name: "read_file", Arguments: `{"path":"a.go"}`}),
		tool("ok", "r2"),
	}))
	if len(fs.Read) != 1 {
		t.Fatalf("re-read should not duplicate: %v", fs.Read)
	}
}

func TestRenderFileSetEmptyReturnsNothing(t *testing.T) {
	if got := RenderFileSet(FileSet{}); got != "" {
		t.Fatalf("empty set rendered %q, want \"\"", got)
	}
}

func TestRenderFileSetContainsBothSections(t *testing.T) {
	fs := FileSet{
		Read:     []string{"a.go"},
		Modified: []string{"b.go"},
	}
	got := RenderFileSet(fs)
	if !strings.Contains(got, "Modified:") || !strings.Contains(got, "b.go") {
		t.Errorf("missing modified section: %q", got)
	}
	if !strings.Contains(got, "Read:") || !strings.Contains(got, "a.go") {
		t.Errorf("missing read section: %q", got)
	}
}
