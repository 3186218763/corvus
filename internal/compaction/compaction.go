// Package compaction owns the pure context-compaction policy: which messages
// fold, what stays verbatim, token economics, and digest/transcript rendering.
// The agent loop keeps the stateful glue (trigger thresholds, session
// mutation, events, hooks, provider-backed summarization) and consumes this
// package; the Summarizer interface is the swappable seam a backend or test
// plugs in. Modeled on deepseek-harness's compaction seam: one optional
// capability beside the loop spine, with the policy functions split out so the
// loop does not own them.
package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"corvus/internal/provider"
)

// SummaryTagOpen wraps a compaction summary so the model can distinguish it
// from live user input and later folds can detect prior digests.
const SummaryTagOpen = "<compaction-summary>"

// KeepPolicy selects retention classes for the compaction fold. A kept message
// and its tool-call group stay verbatim instead of folding.
type KeepPolicy int

const (
	KeepErrors KeepPolicy = 1 << iota
	KeepUserMarked
)

// Summarizer folds a region of old messages into a compact briefing. It must
// be safe to call once per compaction pass; the loop owns timeout, retry, and
// the mechanical-fallback path.
type Summarizer interface {
	Summarize(ctx context.Context, region []provider.Message, instructions string) (string, error)
}

// FoldEconomics estimates whether compacting the given region saves enough
// tokens to justify the summarization API call. It returns false when the
// region is too small for the savings to outweigh the extra round-trip cost
// and latency of calling the summarizer.
func FoldEconomics(region []provider.Message) bool {
	const minFoldTokens = 400
	return EstimateMessagesTokens(region) >= minFoldTokens
}

// EstimateMessagesTokens approximates the provider-side token cost of a
// message sequence, skipping local-only messages.
func EstimateMessagesTokens(msgs []provider.Message) int {
	total := 0
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		total += 4 // chat-message framing overhead
		total += EstimateTextTokens(m.Content)
		total += EstimateTextTokens(m.ReasoningContent)
		total += EstimateTextTokens(m.Name)
		total += EstimateTextTokens(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			total += 8
			total += EstimateTextTokens(tc.ID)
			total += EstimateTextTokens(tc.Name)
			total += EstimateTextTokens(tc.Arguments)
		}
	}
	return total
}

// EstimateTextTokens is a conservative cross-language approximation:
// English-ish text trends near four bytes per token, while CJK-heavy text is
// closer to one rune per token.
func EstimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	bytes := len(s)
	runes := utf8.RuneCountInString(s)
	byBytes := (bytes + 3) / 4
	if runes > byBytes {
		return runes
	}
	return byBytes
}

// IsCompactionSummary reports whether m is a rolling summary from a prior fold.
func IsCompactionSummary(m provider.Message) bool {
	return m.Role == provider.RoleUser &&
		strings.HasPrefix(strings.TrimLeft(m.Content, "\n "), SummaryTagOpen)
}

// PartitionFold splits a compaction region into what is kept verbatim — small
// user turns (a fact the user stated is never summarized away), policy-kept
// messages, and prior digests (so a later fold never re-summarizes an earlier
// digest and drops the facts it already captured) — and the rest, which folds.
// Order within each group is preserved. pinnable decides whether one user turn
// is small enough to keep verbatim; the loop supplies its window-aware test.
func PartitionFold(region []provider.Message, keepPolicy KeepPolicy, pinnable func(provider.Message) bool) (kept, fold []provider.Message) {
	policyKeep := KeepIndexes(region, keepPolicy)
	for i, m := range region {
		if m.LocalOnly || policyKeep[i] || IsCompactionSummary(m) || (m.Role == provider.RoleUser && pinnable != nil && pinnable(m)) {
			kept = append(kept, m)
		} else {
			fold = append(fold, m)
		}
	}
	return kept, fold
}

// KeepIndexes marks retention-policy messages and their tool-call groups.
func KeepIndexes(region []provider.Message, policy KeepPolicy) []bool {
	keep := make([]bool, len(region))
	policyStart := 0
	for i, m := range region {
		if IsCompactionSummary(m) {
			policyStart = i + 1
		}
	}
	// Retention applies only to messages since the latest digest; older kept
	// messages are allowed to fold on the next pass so they cannot grow forever.
	for i, m := range region {
		if i >= policyStart && ShouldKeepMessage(m, policy) {
			keep[i] = true
		}
	}
	for i, m := range region {
		if !keep[i] {
			continue
		}
		switch m.Role {
		case provider.RoleTool:
			if j := FindToolCaller(region, i, m.ToolCallID); j >= 0 {
				KeepToolCallGroup(region, keep, j)
			}
		case provider.RoleAssistant:
			KeepToolCallGroup(region, keep, i)
		}
	}
	return keep
}

// KeepToolCallGroup keeps an assistant message and the tool results belonging
// to its tool calls.
func KeepToolCallGroup(region []provider.Message, keep []bool, assistantIndex int) {
	if assistantIndex < 0 || assistantIndex >= len(region) {
		return
	}
	m := region[assistantIndex]
	if m.Role != provider.RoleAssistant || len(m.ToolCalls) == 0 {
		return
	}
	keep[assistantIndex] = true
	ids := ToolCallIDs(m)
	for j := assistantIndex + 1; j < len(region) && region[j].Role == provider.RoleTool; j++ {
		if ids[region[j].ToolCallID] {
			keep[j] = true
		}
	}
}

// ShouldKeepMessage reports whether the retention policy keeps m verbatim.
func ShouldKeepMessage(m provider.Message, policy KeepPolicy) bool {
	if policy&KeepErrors != 0 && IsErrorMessage(m) {
		return true
	}
	if policy&KeepUserMarked != 0 && IsUserMarked(m) {
		return true
	}
	return false
}

// IsErrorMessage reports whether m is a tool error the retention policy may keep.
func IsErrorMessage(m provider.Message) bool {
	if m.Role != provider.RoleTool {
		return false
	}
	s := strings.TrimSpace(strings.ToLower(m.Content))
	return strings.HasPrefix(s, "error:") || strings.HasPrefix(s, "blocked:")
}

// IsUserMarked reports whether a user turn carries an explicit keep marker.
func IsUserMarked(m provider.Message) bool {
	if m.Role != provider.RoleUser {
		return false
	}
	content := strings.TrimSpace(strings.ToLower(m.Content))
	return strings.HasPrefix(content, "[[keep]]") ||
		strings.HasPrefix(content, "[keep]") ||
		strings.HasPrefix(content, "<keep>") ||
		strings.HasPrefix(content, "<!-- keep -->")
}

// FindToolCaller returns the assistant index whose tool call issued the tool
// result at toolIndex, or -1.
func FindToolCaller(region []provider.Message, toolIndex int, id string) int {
	for i := toolIndex - 1; i >= 0; i-- {
		if region[i].Role != provider.RoleAssistant {
			continue
		}
		for _, tc := range region[i].ToolCalls {
			if tc.ID == id {
				return i
			}
		}
	}
	return -1
}

// ToolCallIDs returns the set of tool-call ids an assistant message issued.
func ToolCallIDs(m provider.Message) map[string]bool {
	ids := make(map[string]bool, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		ids[tc.ID] = true
	}
	return ids
}

// TailStart walks newest→oldest, growing the verbatim tail until the next
// message would push its token estimate past budgetTokens (but never below
// minKeep messages), then aligns the boundary back off any tool result so the
// tail never begins with an orphan whose assistant tool_calls were summarized
// away.
func TailStart(msgs []provider.Message, head, budgetTokens int, tokPerChar float64, minKeep int) int {
	start := len(msgs)
	acc := 0
	for i := len(msgs) - 1; i > head; i-- {
		c := int(float64(MsgChars(msgs[i])) * tokPerChar)
		if len(msgs)-i > minKeep && acc+c > budgetTokens {
			break
		}
		acc += c
		start = i
	}
	// start == len(msgs) when nothing fit the tail (a session too small to have a
	// message after head); there is no msgs[start] to align off, and the caller's
	// minCompactMessages check then no-ops the pass.
	for start > head && start < len(msgs) && msgs[start].Role == provider.RoleTool {
		start--
	}
	return start
}

// MsgChars counts the characters that ride to the provider for one message —
// content plus tool-call names and arguments, but not reasoning (stripped on
// send).
func MsgChars(m provider.Message) int {
	if m.LocalOnly {
		return 0
	}
	n := len(m.Content)
	for _, tc := range m.ToolCalls {
		n += len(tc.Name) + len(tc.Arguments)
	}
	return n
}

// CharsOfMessages sums MsgChars over a sequence.
func CharsOfMessages(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		n += MsgChars(m)
	}
	return n
}

// MechanicalFoldDigest is the deterministic stand-in used when the summarizer
// is unreachable: the foldable region is already archived, so the digest just
// notes the gap and points the model at the user for anything it needs from
// before it.
func MechanicalFoldDigest(n int, archive string) string {
	where := "."
	if archive != "" {
		where = " (archived to " + archive + ")."
	}
	return fmt.Sprintf("%d earlier message(s) were folded here to free context, but the automatic summary was unavailable%s Ask the user if you need details from before this point.", n, where)
}

// RenderTranscript flattens messages into a readable transcript for
// summarization.
func RenderTranscript(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		switch m.Role {
		case provider.RoleUser:
			fmt.Fprintf(&b, "[user]\n%s\n\n", m.Content)
		case provider.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "[assistant]\n%s\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[assistant calls %s] %s\n", tc.Name, SummarizeToolArgs(tc.Arguments))
			}
			b.WriteString("\n")
		case provider.RoleTool:
			fmt.Fprintf(&b, "[tool %s result]\n%s\n\n", m.Name, m.Content)
		case provider.RoleSystem:
			fmt.Fprintf(&b, "[system]\n%s\n\n", m.Content)
		}
	}
	return b.String()
}

// SummarizeToolArgs returns a short summary of tool-call arguments instead of
// the full JSON. This prevents the summarizer from reproducing long argument
// text (like sub-agent task prompts) in the compaction summary, which would
// leak into the session as a user message.
func SummarizeToolArgs(args string) string {
	if args == "" {
		return "(no arguments)"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		// Not valid JSON — return a length hint instead of raw text.
		return fmt.Sprintf("(%d bytes)", len(args))
	}
	keys := make([]string, 0, len(parsed))
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("{%s} (%d keys)", strings.Join(keys, ", "), len(parsed))
}
