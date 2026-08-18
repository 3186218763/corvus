package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type requiredTool struct{ completionProbeTool }

func (requiredTool) CompletionRequired() bool { return true }

type completionProbeTool struct{}

func (completionProbeTool) Name() string            { return "stub" }
func (completionProbeTool) Description() string     { return "" }
func (completionProbeTool) Schema() json.RawMessage { return nil }
func (completionProbeTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (completionProbeTool) ReadOnly() bool { return true }

func TestIsCompletionRequired(t *testing.T) {
	if IsCompletionRequired(nil) || IsCompletionRequired(completionProbeTool{}) {
		t.Fatal("missing/false capability must not be required")
	}
	if !IsCompletionRequired(requiredTool{}) {
		t.Fatal("explicit CompletionRequired() should be detected")
	}
}
