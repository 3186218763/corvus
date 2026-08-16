package boot

import (
	"bytes"
	"strings"
	"testing"

	"corvus/internal/config"
	"corvus/internal/netclient"
	"corvus/internal/netpolicy"
	"corvus/internal/sandbox"
	"corvus/internal/tool"
	"corvus/internal/tool/builtin"
)

func TestAddBuiltinsBindsToolSearchToRegistry(t *testing.T) {
	reg := tool.NewRegistry()
	var stderr bytes.Buffer
	addBuiltins(reg, nil, nil, nil, sandbox.Spec{}, 0, builtin.SearchSpec{}, &stderr, "", netclient.ProxySpec{}, netpolicy.Policy{}, nil, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}, nil, nil)

	ts, ok := reg.Get("tool_search")
	if !ok {
		t.Fatalf("tool_search not registered; got %v", reg.Names())
	}
	// The bound instance must search the live registry (e.g. find bash).
	out, err := ts.Execute(t.Context(), []byte(`{"query":"shell"}`))
	if err != nil {
		t.Fatalf("tool_search Execute: %v", err)
	}
	if !strings.Contains(out, "bash") {
		t.Errorf("tool_search output = %q, want bash matched from live registry", out)
	}
}

func TestAddBuiltinsOmitsWebSearchWhenUnconfigured(t *testing.T) {
	reg := tool.NewRegistry()
	var stderr bytes.Buffer
	addBuiltins(reg, nil, nil, nil, sandbox.Spec{}, 0, builtin.SearchSpec{}, &stderr, "", netclient.ProxySpec{}, netpolicy.Policy{}, nil, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}, nil, nil)
	if _, ok := reg.Get("web_search"); ok {
		t.Fatalf("web_search registered without configuration; got %v", reg.Names())
	}
}

func TestBuildWebSearchToolValidatesEngine(t *testing.T) {
	cfg := config.Default()
	cfg.WebSearch = config.WebSearchConfig{Engine: "yahoo"}
	if _, err := buildWebSearchTool(cfg, netclient.ProxySpec{}, netpolicy.Policy{}); err == nil || !strings.Contains(err.Error(), "unknown web_search engine") {
		t.Fatalf("error = %v, want unknown engine", err)
	}
}

func TestBuildWebSearchToolRequiresSearxngBaseURL(t *testing.T) {
	cfg := config.Default()
	cfg.WebSearch = config.WebSearchConfig{Engine: "searxng"}
	if _, err := buildWebSearchTool(cfg, netclient.ProxySpec{}, netpolicy.Policy{}); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("error = %v, want base_url required", err)
	}
}

func TestBuildWebSearchToolRequiresBraveKey(t *testing.T) {
	cfg := config.Default()
	cfg.WebSearch = config.WebSearchConfig{Engine: "brave"}
	if _, err := buildWebSearchTool(cfg, netclient.ProxySpec{}, netpolicy.Policy{}); err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("error = %v, want api_key required", err)
	}
}

func TestBuildWebSearchToolDisabled(t *testing.T) {
	cfg := config.Default()
	tl, err := buildWebSearchTool(cfg, netclient.ProxySpec{}, netpolicy.Policy{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tl != nil {
		t.Fatalf("tool = %v, want nil when engine unset", tl)
	}
}
