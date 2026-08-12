package main

import (
	"encoding/json"
	"strings"
	"testing"

	"corvus/internal/config"
	"corvus/internal/tool"
)

func mcpServerToolSet(t *testing.T, cfg *config.Config) []tool.Tool {
	t.Helper()
	tools, err := buildTools(cfg, t.TempDir(), false)
	if err != nil {
		t.Fatalf("buildTools: %v", err)
	}
	return tools
}

func TestBuildToolsReadOnlySurfaceIncludesToolSearch(t *testing.T) {
	tools := mcpServerToolSet(t, config.Default())
	names := map[string]tool.Tool{}
	for _, tl := range tools {
		names[tl.Name()] = tl
	}
	ts, ok := names["tool_search"]
	if !ok {
		t.Fatalf("tool_search missing from MCP surface; got %d tools", len(tools))
	}
	out, err := ts.Execute(t.Context(), []byte(`{"query":"read"}`))
	if err != nil {
		t.Fatalf("tool_search Execute: %v", err)
	}
	if !strings.Contains(out, "read_file") {
		t.Errorf("tool_search output = %q, want read_file in served surface", out)
	}
}

func TestBuildToolsWebSearchOnlyWhenConfigured(t *testing.T) {
	cfg := config.Default()
	for _, tl := range mcpServerToolSet(t, cfg) {
		if tl.Name() == "web_search" {
			t.Fatal("web_search registered without [web_search] configuration")
		}
	}

	cfg.WebSearch = config.WebSearchConfig{Engine: "searxng", BaseURL: "https://search.example.com"}
	found := false
	for _, tl := range mcpServerToolSet(t, cfg) {
		if tl.Name() == "web_search" {
			found = true
			if !tl.ReadOnly() {
				t.Error("web_search must be read-only")
			}
			var schema map[string]any
			if err := json.Unmarshal(tl.Schema(), &schema); err != nil {
				t.Fatalf("web_search schema invalid: %v", err)
			}
			if _, ok := schema["properties"].(map[string]any)["query"]; !ok {
				t.Errorf("web_search schema missing query: %v", schema)
			}
		}
	}
	if !found {
		t.Fatal("web_search missing from MCP surface when configured")
	}
}

func TestBuildToolsWebSearchBadEngineFails(t *testing.T) {
	cfg := config.Default()
	cfg.WebSearch = config.WebSearchConfig{Engine: "yahoo"}
	if _, err := buildTools(cfg, t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "unknown web_search engine") {
		t.Fatalf("error = %v, want unknown engine failure", err)
	}
}

// TestBuildToolsAllowWriteRegistersWriters pins the --allow-write contract: the
// flag must actually register every writer tool (and a confined bash), not
// re-append the read-only surface.
func TestBuildToolsAllowWriteRegistersWriters(t *testing.T) {
	readOnly := mcpServerToolSet(t, config.Default())
	writeCfg := config.Default()
	tools, err := buildTools(writeCfg, t.TempDir(), true)
	if err != nil {
		t.Fatalf("buildTools(allowWrite): %v", err)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name()] = true
	}
	want := []string{
		"write_file", "edit_file", "multi_edit", "move_file",
		"notebook_edit", "delete_range", "delete_symbol", "bash",
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("--allow-write missing tool %q; got %v", name, names)
		}
	}
	for _, name := range []string{"read_file", "ls", "glob", "grep", "code_index"} {
		if !names[name] {
			t.Errorf("read-only tool %q must survive --allow-write; got %v", name, names)
		}
	}
	if len(tools) <= len(readOnly) {
		t.Errorf("allow-write surface (%d tools) must be a superset of read-only (%d)", len(tools), len(readOnly))
	}
}

// TestBuildToolsWebFetchHonorsNetPolicy: the MCP server is an outbound network
// surface, so [network_policy] rules must gate web_fetch even when the tool is
// called directly (the server has no session to ask the user). Default=deny
// makes the policy layer refuse before any dialing, so the denial message is
// observable without network access.
func TestBuildToolsWebFetchHonorsNetPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.NetworkPolicy = config.NetworkPolicyConfig{
		Default: "deny",
		Deny:    []string{"deny.example.com"},
	}
	tools, err := buildTools(cfg, t.TempDir(), false)
	if err != nil {
		t.Fatalf("buildTools: %v", err)
	}
	var wf tool.Tool
	for _, tl := range tools {
		if tl.Name() == "web_fetch" {
			wf = tl
			break
		}
	}
	if wf == nil {
		t.Fatal("web_fetch missing from MCP surface")
	}
	out, err := wf.Execute(t.Context(), []byte(`{"url":"https://deny.example.com/x"}`))
	if err == nil || !strings.Contains(err.Error(), "network policy denied") {
		t.Fatalf("web_fetch err = %v, want network policy denial; out=%q", err, out)
	}
}
