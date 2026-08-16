package compaction

import (
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

func TestSummarizeToolArgsNeverLeaksArgumentText(t *testing.T) {
	if got := SummarizeToolArgs(`{"prompt":"secret task text"}`); got != "{prompt} (1 keys)" {
		t.Fatalf("SummarizeToolArgs = %q", got)
	}
	if got := SummarizeToolArgs("not json"); got == "not json" {
		t.Fatal("non-JSON args leaked verbatim")
	}
}
