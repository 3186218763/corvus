package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"corvus/internal/compaction"
	"corvus/internal/event"
	"corvus/internal/provider"
)

// Compaction is a low-frequency cache-reset point: the prompt grows append-only
// (high cache hits) until a turn nears compactRatio of the window, then it is
// compacted down to a tail budget. The budget is a fixed token count, not a
// fraction of the window, so a huge window still compacts rarely while a small
// one still lands below the trigger (which is what stops the re-compaction loop).
const (
	defaultSoftCompactRatio    = 0.5   // report growing context here, but keep the cache-stable prefix intact
	defaultToolResultSnipRatio = 0.6   // rewrite stale tool results cheaply before summary compaction
	defaultCompactRatio        = 0.8   // trigger: prompt at this fraction of the window compacts
	defaultCompactForceRatio   = 0.9   // force compaction at this high-water mark even for low-value folds
	defaultCompactTarget       = 0.5   // safety cap: the kept tail never exceeds this fraction of the window
	defaultTailTokens          = 16384 // verbatim recent-tail budget, in tokens
	minRecentKeep              = 2     // never keep fewer recent messages than this
	minCompactMessages         = 2     // skip compaction below this many compactable messages
	fallbackTokPerChar         = 0.25  // ~4 chars/token, used before any usage is available to calibrate
	maxPinnedFirstUserTokens   = 1500  // ceiling on pinning the first user turn verbatim; larger first turns (pasted content) stay foldable
	pinnedFirstUserWindowFrac  = 0.15  // and never pin a first turn worth more than this fraction of the window
)

// summaryTag wraps the compaction summary so the model can distinguish it from
// live user input and later strip or skip it when reasoning about the current turn.
const (
	summaryTagOpen  = "<compaction-summary>"
	summaryTagClose = "</compaction-summary>"
)

// summaryTimeout bounds one summarizer call so a stalled stream surfaces a clear
// failure (then a mechanical fold) instead of hanging compaction indefinitely.
const summaryTimeout = 90 * time.Second

// summarySystemPrompt steers the executor to distill older history into a
// structured briefing it can keep relying on after the originals are dropped.
// The section layout mirrors what a coding agent actually needs to resume work
// mid-task: the goal verbatim, the concrete state of the code, and an explicit
// next step — so the post-compaction turn doesn't lose the thread or re-derive
// decisions already made.
const summarySystemPrompt = `You are compacting the earlier part of a coding agent's conversation to save context.
The agent keeps your summary alongside the user's own turns (kept verbatim) and the recent tail; your job is to fold the assistant/tool work into a briefing it can resume from.
Write under these exact headings, omitting a heading only if it has no content:

## Standing facts & constraints
Everything the user stated that still governs the work — names, paths, IDs, versions, tokens, preferences, and hard "never do X" rules — in their own words. Be exhaustive; this is the durable contract, so prefer over- to under-including.

## Goal
The user's request and intent.

## Decisions & rationale
Key choices made so far and why — so they are not re-litigated or reversed.

## Files & code
Files read or modified, with the specific facts that matter: signatures, line locations, data shapes, and exact edits applied. Be concrete; this is what lets the agent act without re-reading everything.

## Commands & outcomes
Commands run (builds, tests, git) and their relevant results — what passed, what failed, and the error text that matters.

## Errors & fixes
Problems hit and how they were resolved (or not), so the same dead ends are not repeated.

## Pending & next step
What is still in progress or unstarted, and the single most concrete next action to take.

Rules: be terse — bullet points and fragments, not prose. Preserve identifiers, paths, and numbers exactly. Do NOT invent anything not present in the messages; if something is unknown, leave it out rather than guessing.`

// maybeCompact compacts the session when the last turn's prompt has grown to the
// configured fraction of the context window. It is a no-op when compaction is
// disabled (no window) or usage is unavailable.
func (a *Agent) maybeCompact(ctx context.Context, u *provider.Usage) {
	if a.contextWindow <= 0 || u == nil || u.PromptTokens == 0 {
		return
	}
	high := int(float64(a.contextWindow) * a.compactRatio)
	snip := int(float64(a.contextWindow) * a.toolResultSnipRatio)
	soft := int(float64(a.contextWindow) * a.softCompactRatio)
	// Between the soft ratio and the trigger, report growing context once without
	// rewriting the prefix — a compaction here would needlessly crater the cache.
	if u.PromptTokens >= soft && u.PromptTokens < snip && !a.softCompactNoticed {
		a.softCompactNoticed = true
		detail := fmt.Sprintf("context reached %.0f%% of window; keeping cache-first prefix until compact threshold %.0f%%", a.softCompactRatio*100, a.compactRatio*100)
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Context is getting large; preserving cache until cleanup is needed.", Detail: detail})
		return
	}
	if u.PromptTokens >= snip && u.PromptTokens < high {
		ratio := a.tokPerChar()
		if st, err := a.SnipStaleToolResults(); err == nil && st.Results > 0 {
			saved := int(float64(st.SavedChars) * ratio)
			a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(
				"snipped %d stale tool results (~%d tokens est.) before compaction", st.Results, saved)})
		}
		return
	}
	if u.PromptTokens < high {
		// A turn that sits under the trigger is the breathing room a healthy
		// compaction buys; it clears the stuck latch and the run counter.
		a.consecutiveCompacts = 0
		a.compactStuck = false
		return
	}
	if a.compactStuck {
		return
	}
	force := u.PromptTokens >= int(float64(a.contextWindow)*a.compactForceRatio)
	// Prune before folding: when eliding stale tool results alone clears the
	// trigger, this turn's (paid) summarize call is skipped entirely.
	ratio := a.tokPerChar()
	if st, err := a.PruneStaleToolResults(); err == nil && st.Results > 0 {
		saved := int(float64(st.SavedChars) * ratio)
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(
			"pruned %d stale tool results (~%d tokens est.) before compaction", st.Results, saved)})
		if !force && u.PromptTokens-saved < high {
			return
		}
	}
	if err := a.compact(ctx, "auto", "", force); err != nil {
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Context cleanup skipped for now.", Detail: fmt.Sprintf("compaction skipped: %v", err)})
		return
	}
	// A healthy compaction drops the prompt under the trigger, so the next turn
	// won't compact. Compacting on consecutive turns means the kept tail alone
	// exceeds the trigger — the system prompt plus one verbatim turn is bigger than
	// the window allows. Re-firing every turn is the loop users hit, so pause
	// auto-compaction and say why, once.
	a.consecutiveCompacts++
	if a.consecutiveCompacts >= 2 {
		a.compactStuck = true
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Automatic context cleanup paused because the context window is too small.", Detail: fmt.Sprintf(
			"context_window=%d is too small for compaction to help (the system prompt plus one turn already exceeds %.0f%% of it); raise context_window or shrink tool output. Auto-compaction paused until the prompt drops.",
			a.contextWindow, a.compactRatio*100)})
	}
}

// compact summarizes the older middle of the session and replaces it in place:
// the session becomes system + summary + recent tail. The dropped originals are
// archived first, so the full history stays traceable. trigger is "auto" (the
// window threshold) or "manual" (/compact); it rides the Compaction events so a
// frontend can label the card. instructions is optional extra summary guidance
// (the user's `/compact <focus>` text); a PreCompact hook can contribute more.
// force bypasses the fold-economics skip (manual /compact and the force-ratio
// high-water mark always compact). A Started event is emitted before the (network)
// summarize so the UI can show a "compacting…" placeholder, and a Done event
// (carrying the summary) replaces it.
func (a *Agent) compact(ctx context.Context, trigger, instructions string, force bool) error {
	msgs := a.session.Messages
	head, start, ok := a.planCompaction(msgs, minCompactMessages)
	if !ok {
		// A single huge message can still be worth folding. Keep the normal
		// message-count guard for small histories, but let content size decide
		// whether a one-message region has real compaction value.
		head, start, ok = a.planCompaction(msgs, 1)
	}
	if !ok {
		return nil // recent tail already covers everything worth keeping
	}
	// A controller in-flight marker records the pre-turn message count, but a
	// compaction rewrites message indexes. Keep the entire active turn outside
	// the fold so completed tool call/result pairs remain available for a later
	// cancellation or crash recovery instead of surviving only as prose in a
	// summary. This is another manifestation of the tool-pairing cut-point rule:
	// never split an assistant tool_calls message from its results, even when
	// that assistant message is still being executed. The active turn boundary
	// takes precedence over the tail-budget calculation.
	if active := a.activeTurnStart(msgs); active >= head && active < start {
		start = active
		if start <= head {
			return nil
		}
	}
	region := msgs[head:start]

	// Base layer: every small user turn in the region is kept verbatim (the
	// deterministic floor — a fact the user stated is never summarized away,
	// wherever in the session they said it); only the rest folds into the digest.
	kept, fold := a.partitionFold(region)
	if len(fold) == 0 {
		return nil // nothing but kept user turns — a fold would save nothing
	}

	// Economic check on the foldable part (kept user turns don't count toward the
	// savings): skip if too small to justify the call, unless force demands it.
	if !force && !compaction.FoldEconomics(fold) {
		return nil
	}

	a.sink.Emit(event.Event{Kind: event.CompactionStarted, Compaction: event.Compaction{Trigger: trigger}})

	// A PreCompact hook can steer what the summary keeps; its stdout joins any
	// explicit /compact <focus> text.
	if a.hooks != nil {
		if hookInstr := a.hooks.PreCompact(ctx, trigger); hookInstr != "" {
			if instructions != "" {
				instructions += "\n"
			}
			instructions += hookInstr
		}
	}

	archived := ""
	if a.archiveDir != "" {
		path, err := archiveMessages(a.archiveDir, fold)
		if err != nil {
			a.emitCompactionAborted(trigger)
			return fmt.Errorf("archive: %w", err)
		}
		archived = path
	}

	// The digest covers only the foldable work; kept user turns and prior digests
	// are spliced back verbatim, so a fact that reached a digest once is never
	// re-summarized away and the user's own words are never touched. Digests
	// accumulate (small) rather than collapsing into one lossy rolling summary.
	//
	// The deterministic file set is extracted from the fold's tool calls and
	// merged into the accumulated projection, so the model always sees the full
	// set of files it has read or modified — not just what this pass folds.
	regionFileSet := compaction.ExtractFileSet(fold)
	a.compactFileSet.Merge(regionFileSet)
	summary, err := a.summarizeWithRetry(ctx, fold, instructions)
	if err != nil {
		// Mechanical fold: the foldable region is already archived, so stand in a
		// deterministic marker rather than aborting. /compact then always frees
		// context (and auto-compaction can't loop on a still-full window); the
		// verbatim user turns kept above are untouched.
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Context was compacted without a generated summary.", Detail: "compaction summary unavailable (" + err.Error() + "); folded mechanically"})
		summary = compaction.MechanicalFoldDigest(len(fold), archived)
	}
	// Append the deterministic file set so it survives even a mechanical fold
	// (where the LLM summary is absent) and stays accurate across rounds.
	summary += compaction.RenderFileSet(a.compactFileSet)

	compacted := make([]provider.Message, 0, head+len(kept)+1+len(msgs)-start)
	compacted = append(compacted, msgs[:head]...)
	compacted = append(compacted, kept...)
	compacted = append(compacted, provider.Message{
		Role: provider.RoleUser,
		Content: summaryTagOpen + "\n" +
			"Summary of earlier conversation (older messages were compacted to save context):\n" +
			summary + "\n" +
			summaryTagClose,
	})
	compacted = append(compacted, msgs[start:]...)
	a.session.Rewrite(compacted)

	a.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{
		Trigger: trigger, Messages: len(fold), Summary: summary, Archive: archived,
	}})
	return nil
}

// emitCompactionAborted resolves a "compacting…" placeholder when a pass fails
// after the Started event: a Done with no summary tells a frontend to drop the
// placeholder. The caller still surfaces the reason (a Notice), so this carries
// no text of its own.
func (a *Agent) emitCompactionAborted(trigger string) {
	a.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{Trigger: trigger}})
}

// SummarizeFrom replaces the messages from fromIdx onward with a single summary,
// keeping everything before it verbatim ("summarize from here"). fromIdx is a turn
// boundary (a user message), so the split never severs a tool_call/result pair —
// those live within one turn. A no-op when the region is empty.
func (a *Agent) SummarizeFrom(ctx context.Context, fromIdx int) error {
	msgs := a.session.Messages
	if fromIdx < 0 || fromIdx >= len(msgs) {
		return nil
	}
	region, localOnly := splitLocalOnlyMessages(msgs[fromIdx:])
	if len(region) == 0 {
		return nil
	}
	if a.archiveDir != "" {
		_, _ = archiveMessages(a.archiveDir, region) // best-effort traceability
	}
	summary, err := a.summarize(ctx, region, "")
	if err != nil {
		return err
	}
	next := make([]provider.Message, 0, fromIdx+1+len(localOnly))
	next = append(next, msgs[:fromIdx]...)
	next = append(next, provider.Message{
		Role:    provider.RoleUser,
		Content: "Summary of the later conversation (compacted from here on):\n" + summary,
	})
	next = append(next, localOnly...)
	a.session.Rewrite(next)
	a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("summarized %d later messages → summary", len(region))})
	return nil
}

// SummarizeUpTo replaces the messages before toIdx (after the system prompt) with
// a single summary, keeping toIdx onward verbatim ("summarize up to here"). toIdx
// is a turn boundary, so no tool pair is split. A no-op when the region is empty.
func (a *Agent) SummarizeUpTo(ctx context.Context, toIdx int) error {
	msgs := a.session.Messages
	head := 0
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		head = 1
	}
	if toIdx <= head || toIdx > len(msgs) {
		return nil
	}
	region, localOnly := splitLocalOnlyMessages(msgs[head:toIdx])
	if len(region) == 0 {
		return nil
	}
	if a.archiveDir != "" {
		_, _ = archiveMessages(a.archiveDir, region)
	}
	summary, err := a.summarize(ctx, region, "")
	if err != nil {
		return err
	}
	next := make([]provider.Message, 0, head+1+len(localOnly)+len(msgs)-toIdx)
	next = append(next, msgs[:head]...)
	next = append(next, provider.Message{
		Role:    provider.RoleUser,
		Content: "Summary of earlier conversation (compacted up to here):\n" + summary,
	})
	next = append(next, localOnly...)
	next = append(next, msgs[toIdx:]...)
	a.session.Rewrite(next)
	a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("summarized %d earlier messages → summary", len(region))})
	return nil
}

// IsCompactionSummary reports whether m is a rolling digest inserted by a
// prior compaction fold. Exported for session owners outside this package
// (e.g. the guardian) whose turn rollback must not treat a digest as a
// disposable user message.
func IsCompactionSummary(m provider.Message) bool { return compaction.IsCompactionSummary(m) }

func (a *Agent) activeTurnStart(msgs []provider.Message) int {
	createdAt := a.activeTurnCreatedAt.Load()
	if createdAt == 0 {
		return -1
	}
	for i, m := range msgs {
		if m.Role == provider.RoleUser && m.CreatedAt == createdAt {
			return i
		}
	}
	return -1
}

// splitLocalOnlyMessages removes display-only interrupted output from the
// summarizer/archive input while returning it in transcript order for durable
// reattachment. Explicit range summaries are user-requested rewrites, but they
// must not erase visible output or expose private partial reasoning to a model.
func splitLocalOnlyMessages(msgs []provider.Message) (model, localOnly []provider.Message) {
	for _, m := range msgs {
		if m.LocalOnly {
			localOnly = append(localOnly, m)
			continue
		}
		model = append(model, m)
	}
	return model, localOnly
}

// pinnedPrefixLen counts the leading messages a fold keeps verbatim: the system
// prompt, the first user turn (its task + stated facts/constraints) when it is
// small enough to be a brief, and any prior summaries — so a fold never
// summarizes the user's facts away, and a later fold never re-summarizes an
// earlier summary into nothing (the drift that silently dropped user-stated facts
// after the second compaction). A large first turn (pasted content) stays
// foldable so pinning never starves the window.
func (a *Agent) pinnedPrefixLen(msgs []provider.Message) int {
	i := 0
	if i < len(msgs) && msgs[i].Role == provider.RoleSystem {
		i++
	}
	if i < len(msgs) && msgs[i].Role == provider.RoleUser && !compaction.IsCompactionSummary(msgs[i]) && a.pinnableUserTurn(msgs[i]) {
		i++
	}
	for i < len(msgs) && compaction.IsCompactionSummary(msgs[i]) {
		i++
	}
	return i
}

// pinnableUserTurn reports whether a user turn is small enough to keep verbatim. A
// turn larger than a brief (pasted content) folds like any other message so the
// kept-verbatim floor never starves the window.
func (a *Agent) pinnableUserTurn(m provider.Message) bool {
	budget := maxPinnedFirstUserTokens
	if a.contextWindow > 0 {
		if f := int(float64(a.contextWindow) * pinnedFirstUserWindowFrac); f < budget {
			budget = f
		}
	}
	return int(float64(compaction.MsgChars(m))*a.tokPerChar()) <= budget
}

// partitionFold splits a compaction region into what is kept verbatim — small user
// turns (a fact the user stated is never summarized away) and prior digests (so a
// later fold never re-summarizes an earlier digest and drops the facts it already
// captured) — and the rest, which folds. Order within each group is preserved.

func (a *Agent) partitionFold(region []provider.Message) (kept, fold []provider.Message) {
	return compaction.PartitionFold(region, a.keepPolicy, a.pinnableUserTurn)
}

// planCompaction locates the region to summarize. head is the count of leading
// messages preserved verbatim (see pinnedPrefixLen); start is where the preserved
// recent tail begins, so msgs[head:start] is compacted. The tail is bounded by a
// token budget (not a message count), so a few large tool outputs can't keep it
// above the trigger and re-fire compaction every turn. ok is false when there is
// too little to compact.
func (a *Agent) planCompaction(msgs []provider.Message, min int) (head, start int, ok bool) {
	head = a.pinnedPrefixLen(msgs)
	if a.contextWindow > 0 {
		budget := defaultTailTokens
		if maxByWin := int(float64(a.contextWindow) * defaultCompactTarget); maxByWin < budget {
			budget = maxByWin
		}
		start = tailStart(msgs, head, budget, a.tokPerChar(), a.tailFloor())
	} else {
		// No window to budget against (manual /compact on an unconfigured
		// provider): keep a fixed count of recent messages, aligned off any tool.
		start = len(msgs) - a.tailFloor()
		for start > head && msgs[start].Role == provider.RoleTool {
			start--
		}
	}
	if start < head {
		start = head
	}
	if start-head < min {
		return head, start, false
	}
	return head, start, true
}

func (a *Agent) tailFloor() int {
	if a.recentKeep > minRecentKeep {
		return a.recentKeep
	}
	return minRecentKeep
}

// tailStart walks newest→oldest, growing the verbatim tail until the next
// message would push its token estimate past budgetTokens (but never below
// minKeep messages), then aligns the boundary back off any tool result so the
// tail never begins with an orphan whose assistant tool_calls were summarized
// away. See compaction.TailStart for the full rationale on the cut-point rule.
func tailStart(msgs []provider.Message, head, budgetTokens int, tokPerChar float64, minKeep int) int {
	return compaction.TailStart(msgs, head, budgetTokens, tokPerChar, minKeep)
}

// tokPerChar derives a tokens-per-character ratio from the last turn's real
// usage so per-message estimates track the provider's tokenizer without a local
// one. Reasoning content is excluded from the char count to match the prompt
// actually sent (the provider strips it). Falls back to ~4 chars/token before
// any usage is known, and ignores absurd ratios.
func (a *Agent) tokPerChar() float64 {
	if u := a.lastUsage.Load(); u != nil && u.PromptTokens > 0 {
		if c := compaction.CharsOfMessages(a.session.Messages); c > 0 {
			if r := float64(u.PromptTokens) / float64(c); r > 0.05 && r < 2 {
				return r
			}
		}
	}
	return fallbackTokPerChar
}

// summarize asks the executor's own provider (no tools) to distill the region
// into a briefing, returning the collected text. instructions, when non-empty,
// is appended to the system prompt as extra focus guidance (from /compact <focus>
// and/or a PreCompact hook).
func (a *Agent) summarize(ctx context.Context, region []provider.Message, instructions string) (string, error) {
	if a.compactSummarizer != nil {
		ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
		defer cancel()
		return a.compactSummarizer.Summarize(ctx, region, instructions)
	}
	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	ctx = provider.WithRequestAttemptCounter(ctx)
	sys := summarySystemPrompt
	if strings.TrimSpace(instructions) != "" {
		sys += "\n\nAdditional focus for this compaction (prioritize keeping this):\n" + strings.TrimSpace(instructions)
	}
	var usage *provider.Usage
	defer func() {
		usage = provider.UsageWithRequestAttemptCount(ctx, usage)
		if usage != nil && (usage.TotalTokens > 0 || usage.RequestCount > 0) {
			a.sink.Emit(event.Event{Kind: event.Usage, ModelRef: a.modelRef, Usage: usage, Pricing: a.pricing, UsageSource: event.UsageSourceCompaction})
		}
	}()
	ch, err := a.prov.Stream(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: sys},
			{Role: provider.RoleUser, Content: compaction.RenderTranscript(region)},
		},
		Temperature:    provider.OptionalTemperature(a.temperature),
		PromptCacheKey: "", // One-off summary requests don't merit polluting the cache; explicitly disable cache write
	})
	if err != nil {
		return "", err
	}

	// select on ctx.Done so a stalled stream (open but never delivering or closing)
	// unblocks on timeout instead of pinning the "compacting…" placeholder forever.
	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				s := strings.TrimSpace(b.String())
				if s == "" {
					return "", fmt.Errorf("summarizer returned empty output")
				}
				return s, nil
			}
			switch chunk.Type {
			case provider.ChunkText:
				b.WriteString(chunk.Text)
			case provider.ChunkUsage:
				usage = chunk.Usage
			case provider.ChunkError:
				return "", chunk.Err
			}
		}
	}
}

// summarizeWithRetry retries one non-timeout failure (a transient stream drop or
// rate blip); a timeout or a second failure returns so the caller folds
// mechanically rather than waiting again.
func (a *Agent) summarizeWithRetry(ctx context.Context, fold []provider.Message, instructions string) (string, error) {
	summary, err := a.summarize(ctx, fold, instructions)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return summary, err
	}
	return a.summarize(ctx, fold, instructions)
}

// archiveMessages writes the dropped originals to a timestamped .jsonl (one
// message per line) under dir, returning the file path.
func archiveMessages(dir string, msgs []provider.Message) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405.000")+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			return "", err
		}
	}
	return path, nil
}
