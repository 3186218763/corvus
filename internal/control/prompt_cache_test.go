package control

import (
	"context"
	"path/filepath"
	"testing"

	"corvus/internal/agent"
	"corvus/internal/event"
	"corvus/internal/provider"
	"corvus/internal/tool"
)

// capturePromptCacheProvider records the last Stream request for sticky-key checks.
type capturePromptCacheProvider struct {
	request provider.Request
}

func (p *capturePromptCacheProvider) Name() string { return "capture-prompt-cache" }

func (p *capturePromptCacheProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.request = req
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func TestSetSessionPathRefreshesSessionCacheID(t *testing.T) {
	prov := &capturePromptCacheProvider{}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
		SessionCacheID:     "old-id",
	}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag})

	path := filepath.Join(t.TempDir(), "chat-newid.jsonl")
	c.SetSessionPath(path)

	if err := c.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	want := "corvus:session:chat-newid"
	if prov.request.PromptCacheKey != want {
		t.Fatalf("PromptCacheKey = %q, want %q after SetSessionPath", prov.request.PromptCacheKey, want)
	}
}

func TestResumeRefreshesSessionCacheID(t *testing.T) {
	prov := &capturePromptCacheProvider{}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
		SessionCacheID:     "before-resume",
	}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag})

	sess := agent.NewSession("sys")
	path := filepath.Join(t.TempDir(), "resumed-branch.jsonl")
	c.Resume(sess, path)

	if err := c.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	want := "corvus:session:resumed-branch"
	if prov.request.PromptCacheKey != want {
		t.Fatalf("PromptCacheKey = %q, want %q after Resume", prov.request.PromptCacheKey, want)
	}
}

func TestNewSessionRefreshesSessionCacheID(t *testing.T) {
	prov := &capturePromptCacheProvider{}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
		SessionCacheID:     "initial",
	}, event.Discard)
	sessionDir := t.TempDir()
	c := New(Options{
		Runner:     ag,
		Executor:   ag,
		SessionDir: sessionDir,
		Label:      "test",
	})
	// Bind an initial path so NewSession has something to rotate from.
	c.SetFreshSessionPath(filepath.Join(sessionDir, "old-session.jsonl"))

	if err := c.NewSession(); err != nil {
		t.Fatal(err)
	}
	freshPath := c.SessionPath()
	if freshPath == "" {
		t.Fatal("NewSession left SessionPath empty")
	}
	wantID := agent.BranchID(freshPath)

	if err := c.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	want := "corvus:session:" + wantID
	if prov.request.PromptCacheKey != want {
		t.Fatalf("PromptCacheKey = %q, want %q after NewSession", prov.request.PromptCacheKey, want)
	}
}

func TestControllerNewSeedsSessionCacheIDFromOptions(t *testing.T) {
	prov := &capturePromptCacheProvider{}
	path := filepath.Join(t.TempDir(), "seeded.jsonl")
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
	}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag, SessionPath: path})
	_ = c

	if err := ag.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	want := "corvus:session:seeded"
	if prov.request.PromptCacheKey != want {
		t.Fatalf("PromptCacheKey = %q, want %q from Options.SessionPath seed", prov.request.PromptCacheKey, want)
	}
}
