package agent

import (
	"context"
	"testing"

	"corvus/internal/event"
	"corvus/internal/provider"
	"corvus/internal/tool"
)

func TestStreamAttachesPromptCacheKey(t *testing.T) {
	prov := &userInputCaptureProvider{}
	sess := NewSession("system")
	a := New(prov, tool.NewRegistry(), sess, Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
		SessionCacheID:     "abc123",
	}, event.Discard)
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if prov.request.PromptCacheKey != "corvus:session:abc123" {
		t.Fatalf("got %q", prov.request.PromptCacheKey)
	}
}

func TestStreamOmitsPromptCacheKeyForDeepSeek(t *testing.T) {
	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{
		PromptCacheKeyMode: "on",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.deepseek.com",
		SessionCacheID:     "abc123",
	}, event.Discard)
	_ = a.Run(context.Background(), "hi")
	if prov.request.PromptCacheKey != "" {
		t.Fatalf("got %q", prov.request.PromptCacheKey)
	}
}

func TestStreamSubagentPromptCacheKey(t *testing.T) {
	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
		SessionCacheID:     "parent1",
		SubagentCacheID:    "sa_ref1",
	}, event.Discard)
	_ = a.Run(context.Background(), "hi")
	if prov.request.PromptCacheKey != "corvus:session:parent1:sub:sa_ref1" {
		t.Fatalf("got %q", prov.request.PromptCacheKey)
	}
}

func TestStreamOmitsPromptCacheKeyWhenFailOpenDisabled(t *testing.T) {
	provider.ClearPromptCacheKeyDisablesForTest()
	t.Cleanup(provider.ClearPromptCacheKeyDisablesForTest)

	fp := provider.ProviderFingerprint("openai", "https://gateway.example/v1")
	provider.DisablePromptCacheKey(fp)

	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://gateway.example/v1",
		SessionCacheID:     "abc123",
	}, event.Discard)
	_ = a.Run(context.Background(), "hi")
	if prov.request.PromptCacheKey != "" {
		t.Fatalf("got %q, want empty after fail-open disable", prov.request.PromptCacheKey)
	}
}

func TestSetSessionCacheIDUpdatesResolvedKey(t *testing.T) {
	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
		SessionCacheID:     "old",
	}, event.Discard)
	a.SetSessionCacheID("newid")
	_ = a.Run(context.Background(), "hi")
	if prov.request.PromptCacheKey != "corvus:session:newid" {
		t.Fatalf("got %q", prov.request.PromptCacheKey)
	}
}
