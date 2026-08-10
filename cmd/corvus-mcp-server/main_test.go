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
