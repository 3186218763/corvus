package agent

import (
	"context"
	"fmt"
	"strings"

	"corvus/internal/compaction"
	"corvus/internal/event"
	"corvus/internal/provider"
)

// providerCompactionSummarizer is the adapter at the compaction seam. Its
// provider is supplied by the composition root and is intentionally a
// different client from the executor; the summarizer has no continuation state
// to preserve and no reason to inherit the executor's prompt-cache key.
type providerCompactionSummarizer struct {
	prov        provider.Provider
	temperature float64
	sink        event.Sink
	modelRef    string
	pricing     *provider.Pricing
}

// NewProviderCompactionSummarizer adapts an isolated provider into the
// compaction interface. The Agent still owns timeout/retry/fallback policy;
// this adapter only acquires one summary and accounts for its usage.
func NewProviderCompactionSummarizer(prov provider.Provider, temperature float64, sink event.Sink, modelRef string, pricing *provider.Pricing) compaction.Summarizer {
	if prov == nil {
		return nil
	}
	return &providerCompactionSummarizer{
		prov: prov, temperature: temperature, sink: sink,
		modelRef: modelRef, pricing: pricing,
	}
}

func (s *providerCompactionSummarizer) Summarize(ctx context.Context, region []provider.Message, instructions string) (string, error) {
	ctx = provider.WithRequestAttemptCounter(ctx)
	system := summarySystemPrompt
	if strings.TrimSpace(instructions) != "" {
		system += "\n\nAdditional focus for this compaction (prioritize keeping this):\n" + strings.TrimSpace(instructions)
	}
	var usage *provider.Usage
	defer func() {
		usage = provider.UsageWithRequestAttemptCount(ctx, usage)
		if usage == nil || (usage.TotalTokens == 0 && usage.RequestCount == 0) || s.sink == nil {
			return
		}
		s.sink.Emit(event.Event{
			Kind: event.Usage, ModelRef: s.modelRef, Usage: usage,
			Pricing: s.pricing, UsageSource: event.UsageSourceCompaction,
		})
	}()

	ch, err := s.prov.Stream(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: system},
			{Role: provider.RoleUser, Content: compaction.RenderTranscript(region)},
		},
		Temperature: provider.OptionalTemperature(s.temperature),
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				out := strings.TrimSpace(b.String())
				if out == "" {
					return "", fmt.Errorf("summarizer returned empty output")
				}
				return out, nil
			}
			switch chunk.Type {
			case provider.ChunkText:
				b.WriteString(chunk.Text)
			case provider.ChunkUsage:
				usage = chunk.Usage
			case provider.ChunkError:
				if chunk.Err == nil {
					return "", fmt.Errorf("summarizer stream failed")
				}
				return "", chunk.Err
			}
		}
	}
}
