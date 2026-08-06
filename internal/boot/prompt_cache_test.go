package boot

import (
	"testing"

	"corvus/internal/agent"
	"corvus/internal/config"
)

func TestPromptCacheOptionsMainSession(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.PromptCacheKey = "auto"
	cfg.Agent.PromptCacheKeyValue = ""
	entry := &config.ProviderEntry{
		Kind:    "openai",
		BaseURL: "https://api.openai.com/v1",
	}
	path := "/tmp/sessions/chat-abc123.jsonl"
	got := promptCacheOptions(cfg, entry, agent.BranchID(path), "")
	if got.PromptCacheKeyMode != "auto" {
		t.Fatalf("mode = %q, want auto", got.PromptCacheKeyMode)
	}
	if got.ProviderKind != "openai" || got.ProviderBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("provider = %q %q", got.ProviderKind, got.ProviderBaseURL)
	}
	if got.SessionCacheID != "chat-abc123" {
		t.Fatalf("SessionCacheID = %q, want chat-abc123", got.SessionCacheID)
	}
	if got.SubagentCacheID != "" {
		t.Fatalf("SubagentCacheID = %q, want empty", got.SubagentCacheID)
	}
}

func TestPromptCacheOptionsSubagent(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.PromptCacheKey = "on"
	cfg.Agent.PromptCacheKeyValue = "ignored-when-on"
	entry := &config.ProviderEntry{Kind: "responses", BaseURL: "https://gateway.example/v1"}
	got := promptCacheOptions(cfg, entry, "parent-stem", "sa_deadbeef")
	if got.SessionCacheID != "parent-stem" {
		t.Fatalf("SessionCacheID = %q", got.SessionCacheID)
	}
	if got.SubagentCacheID != "sa_deadbeef" {
		t.Fatalf("SubagentCacheID = %q", got.SubagentCacheID)
	}
	if got.ProviderKind != "responses" {
		t.Fatalf("ProviderKind = %q", got.ProviderKind)
	}
}

func TestPromptCacheOptionsEphemeralEmptyOmits(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.PromptCacheKey = "auto"
	entry := &config.ProviderEntry{Kind: "openai", BaseURL: "https://api.openai.com/v1"}
	// No stable parent and no ref → empty ids so Resolve returns "".
	got := promptCacheOptions(cfg, entry, "", "")
	if got.SessionCacheID != "" || got.SubagentCacheID != "" {
		t.Fatalf("want empty ids, got session=%q sub=%q", got.SessionCacheID, got.SubagentCacheID)
	}
}

func TestPromptCacheOptionsCustomValue(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.PromptCacheKey = "custom"
	cfg.Agent.PromptCacheKeyValue = "my-sticky-key"
	entry := &config.ProviderEntry{Kind: "dashscope-responses", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"}
	got := promptCacheOptions(cfg, entry, "sess", "")
	if got.PromptCacheKeyMode != "custom" || got.PromptCacheKeyValue != "my-sticky-key" {
		t.Fatalf("custom fields: mode=%q value=%q", got.PromptCacheKeyMode, got.PromptCacheKeyValue)
	}
	if got.ProviderKind != "dashscope-responses" {
		t.Fatalf("ProviderKind = %q, want dashscope-responses from entry.Kind", got.ProviderKind)
	}
}

func TestPromptCacheFieldsApply(t *testing.T) {
	f := promptCacheFields{
		PromptCacheKeyMode:  "off",
		PromptCacheKeyValue: "x",
		ProviderKind:        "openai",
		ProviderBaseURL:     "https://api.openai.com/v1",
		SessionCacheID:      "id1",
		SubagentCacheID:     "sub1",
	}
	var opts agent.Options
	f.apply(&opts)
	if opts.PromptCacheKeyMode != "off" || opts.SessionCacheID != "id1" || opts.SubagentCacheID != "sub1" {
		t.Fatalf("apply failed: %+v", opts)
	}
	f.apply(nil) // must not panic
}

func TestPromptCacheOptionsNilSafe(t *testing.T) {
	got := promptCacheOptions(nil, nil, "s", "r")
	if got.SessionCacheID != "s" || got.SubagentCacheID != "r" {
		t.Fatalf("got %+v", got)
	}
	if got.ProviderKind != "" || got.PromptCacheKeyMode != "" {
		t.Fatalf("nil cfg/entry should leave mode/kind empty: %+v", got)
	}
}
