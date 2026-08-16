package cli

import (
	"context"
	"strings"
	"testing"

	"corvus/internal/agent"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/provider"
	"corvus/internal/tool"
)

// usageProvider streams one usage chunk per turn, letting a real
// controller+agent pair carry the exact token telemetry the status line reads.
type usageProvider struct {
	turns []*provider.Usage
	next  int
}

func (*usageProvider) Name() string { return "usage" }

func (p *usageProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	if p.next < len(p.turns) {
		ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: p.turns[p.next]}
		p.next++
	}
	close(ch)
	return ch, nil
}

// newUsageCtrl runs one visible turn per Usage against a single agent, leaving
// the last turn's numbers in LastUsage and the session sum in SessionCache.
func newUsageCtrl(t *testing.T, usages ...*provider.Usage) *control.Controller {
	t.Helper()
	prov := &usageProvider{turns: usages}
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := control.New(control.Options{Runner: exec, Executor: exec})
	t.Cleanup(c.Close)
	for range usages {
		if err := c.Run(context.Background(), "hi"); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	return c
}

func TestCacheTagHiddenWhenProviderReportsNoCacheFields(t *testing.T) {
	// A provider without prompt-cache support reports prompt tokens but no
	// cache hit/miss fields. Falling back to PromptTokens as the denominator
	// used to paint a bogus "turn hit 0.00%"; the tag must stay empty instead.
	m := chatTUI{ctrl: newUsageCtrl(t, &provider.Usage{PromptTokens: 1000})}
	if got := m.cacheTag(); got != "" {
		t.Fatalf("cacheTag with no cache fields = %q, want empty", got)
	}
}

func TestCacheTagShowsRealZeroHit(t *testing.T) {
	// A genuine full miss (provider reports the fields, hit is zero) is
	// informative and must still render.
	m := chatTUI{ctrl: newUsageCtrl(t, &provider.Usage{PromptTokens: 1000, CacheMissTokens: 1000})}
	if got := m.cacheTag(); !strings.Contains(got, "0.00%") {
		t.Fatalf("cacheTag with a real full miss = %q, want 0.00%% rendered", got)
	}
}

func TestCacheTagRendersHitRateAndSessionAverage(t *testing.T) {
	m := chatTUI{ctrl: newUsageCtrl(t,
		&provider.Usage{CacheHitTokens: 620, CacheMissTokens: 280},
		&provider.Usage{CacheHitTokens: 80, CacheMissTokens: 20},
	)}
	got := m.cacheTag()
	if !strings.Contains(got, "80.00%") {
		t.Fatalf("cacheTag = %q, want turn rate 80.00%%", got)
	}
	if !strings.Contains(got, "70.00%") {
		t.Fatalf("cacheTag = %q, want session average 70.00%%", got)
	}
}
