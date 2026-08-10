package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"corvus/internal/tool"
)

func toolSearchEntries() []tool.ContractEntry {
	return []tool.ContractEntry{
		{Name: "read_file", Description: "Read a file from the workspace and return its content.", ReadOnly: true},
		{Name: "write_file", Description: "Write or overwrite a file inside the workspace.", ReadOnly: false},
		{Name: "edit_file", Description: "Apply a targeted edit to an existing file.", ReadOnly: false},
		{Name: "bash", Description: "Run a shell command in the workspace directory.", ReadOnly: false},
		{Name: "web_fetch", Description: "Fetch a URL over HTTPS and return its text content.", ReadOnly: true},
		{Name: "mcp__github__create_issue", Description: "Create an issue on the connected GitHub server.", ReadOnly: false},
		{Name: "tool_search", Description: "Search registered tools by keyword.", ReadOnly: true},
	}
}

func toolSearchExec(t *testing.T, ts toolSearch, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return ts.Execute(context.Background(), raw)
}

func TestToolSearchFindsExactNameMatch(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	out, err := toolSearchExec(t, ts, map[string]any{"query": "read_file"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "read_file") {
		t.Errorf("output = %q, want read_file in results", out)
	}
	if !strings.Contains(out, "Read a file from the workspace") {
		t.Errorf("output = %q, want description snippet", out)
	}
}

func TestToolSearchMatchesByToken(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	out, err := toolSearchExec(t, ts, map[string]any{"query": "shell"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "bash") {
		t.Errorf("output = %q, want bash matched via description token", out)
	}
}

func TestToolSearchRankingPutsExactNameFirst(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	out, err := toolSearchExec(t, ts, map[string]any{"query": "read"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "- ") {
			if !strings.HasPrefix(line, "- read_file") {
				t.Errorf("first result line = %q, want read_file ranked first:\n%s", line, out)
			}
			break
		}
	}
}

func TestToolSearchLimit(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	out, err := toolSearchExec(t, ts, map[string]any{"query": "file", "limit": 2})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	lines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "- ") {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("result lines = %d, want 2:\n%s", lines, out)
	}
}

func TestToolSearchSkipsItself(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	out, err := toolSearchExec(t, ts, map[string]any{"query": "search"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.Contains(out, "- tool_search") {
		t.Errorf("output includes tool_search itself:\n%s", out)
	}
}

func TestToolSearchIncludesMCPTools(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	out, err := toolSearchExec(t, ts, map[string]any{"query": "github issue"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "mcp__github__create_issue") {
		t.Errorf("output = %q, want MCP tool in results", out)
	}
}

func TestToolSearchNoMatchHintsConnectSource(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	out, err := toolSearchExec(t, ts, map[string]any{"query": "quantum teleportation"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "connect_tool_source") {
		t.Errorf("output = %q, want connect_tool_source hint on no match", out)
	}
}

func TestToolSearchEmptyQuery(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	_, err := toolSearchExec(t, ts, map[string]any{"query": "  "})
	if err == nil {
		t.Fatal("Execute with blank query succeeded, want error")
	}
}

func TestToolSearchNilSnapshotFailsClosed(t *testing.T) {
	ts := toolSearch{} // bare init instance without a registry snapshot
	_, err := toolSearchExec(t, ts, map[string]any{"query": "read"})
	if err == nil {
		t.Fatal("Execute with nil snapshot succeeded, want error")
	}
}

func TestToolSearchContract(t *testing.T) {
	ts := toolSearch{snapshot: toolSearchEntries}
	if !ts.ReadOnly() {
		t.Error("tool_search must be read-only")
	}
	if ts.Name() != "tool_search" {
		t.Errorf("Name = %q, want tool_search", ts.Name())
	}
	var schema map[string]any
	if err := json.Unmarshal(ts.Schema(), &schema); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing: %v", schema)
	}
	if _, ok := props["query"]; !ok {
		t.Errorf("schema missing query property: %v", schema)
	}
	if _, ok := props["limit"]; !ok {
		t.Errorf("schema missing limit property: %v", schema)
	}
}
