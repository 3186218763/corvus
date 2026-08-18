# ADR 0011: Compaction Improvements from External Agent Research

**Date**: 2026-08-18  
**Status**: Accepted  
**Context**: Research B (external agent design review)

## Context

Research B surveyed external agent projects (pi, crush, opencode, codex, goose) to identify proven patterns corvus could adopt. The compaction and tool-output handling systems were compared against pi's battle-tested implementation, which has explicit design documentation for these subsystems.

## Decision

### Implemented Improvements (P1)

#### 1. Prompt-cache isolation for compaction requests

**Change**: Set `PromptCacheKey = ""` on summarization requests in `internal/agent/compact.go`.

**Rationale** (from pi `compaction.md`):
> 摘要请求用**全新 routing session ID + 关闭 prompt-cache 写入**  
> 一次性的摘要请求不值得污染缓存

A compaction summary request is a one-off side conversation with the model that:
1. Uses a different system prompt (summarization instructions)
2. Contains rendered transcript text that will never appear again
3. Does not benefit from cache on subsequent requests
4. Pollutes the prompt cache for the main conversation

By explicitly disabling cache writes (`PromptCacheKey = ""`), we avoid cache pollution and preserve cache efficiency for the actual conversation turns.

**Impact**: 
- No functional change to compaction behavior
- Prevents cache pollution from summary requests
- Keeps main conversation cache hits clean

#### 2. Tool-pairing cut-point rule documentation

**Change**: Added comprehensive documentation explaining why compaction cut points must never land on a tool result message.

**The Rule**: The verbatim tail's starting boundary is always aligned backward off any `RoleTool` message, even if that pushes the tail larger than the token budget. This ensures:

1. Every assistant message with `tool_calls` in the kept tail has its complete set of tool results alongside it
2. Every assistant message with `tool_calls` in the summarized region is fully paired before summarization
3. The cut point always lands on a user or assistant message boundary

**Rationale**: Splitting an assistant `tool_calls` message from its tool results violates the OpenAI/Anthropic API contract. The provider APIs require:
- Every assistant message with `tool_calls` must be followed by tool messages responding to each `tool_call_id`
- A tool message must follow such a call

Violating this causes 400 errors on replay. Pi's `compaction.md` explicitly documents this as a hard rule:
> 切点**永远不在 tool result 上**（必须和它的 tool call 待在一起）

**Locations**:
- `internal/compaction/compaction.go:TailStart` — the core alignment loop
- `internal/agent/compact.go:tailStart` — agent wrapper
- `internal/agent/compact.go:compact` — active turn boundary handling

**Implementation status**: The rule was ALREADY correctly implemented in corvus. The improvement is documentation clarity so future maintainers understand why the alignment exists and must be preserved.

### Verified Existing Implementations

The research confirmed corvus already implements these pi patterns correctly:

#### 1. Spill output annotation (P1.1)

**Status**: ✅ Already implemented in `internal/agent/agent.go:4206-4208`

When tool output is spilled to disk, the model receives:
```
…[tool output is %d bytes — saved in full to %s; %s]…
```

Where:
- `%s` (path) = full absolute path to the spilled file
- `%s` (hint) = `"use read_file to retrieve the full output, or grep the path to search it"`

This matches pi's pattern from `extensions.md`:
> 在返回给 LLM 的文本里标注「已截断 + 完整输出的文件路径」

**No change needed**: The implementation is complete and correct.

#### 2. Structured summary format (P1.4)

**Status**: ✅ Already implemented in `internal/agent/compact.go:54-79`

The `summarySystemPrompt` already enforces a structured format matching pi's design:

| Corvus Section | Pi Equivalent | Purpose |
|---|---|---|
| Standing facts & constraints | Constraints | Durable contract from user |
| Goal | Goal | The user's request |
| Decisions & rationale | Key Decisions | Choices made and why |
| Files & code | Files touched | Concrete state of edits |
| Commands & outcomes | Progress (Done) | Build/test results |
| Errors & fixes | Progress (Blocked) | Dead ends to avoid |
| Pending & next step | Next Steps | What to do next |

**Deterministic file accumulation**: Also fully implemented:
- `compaction.ExtractFileSet()` — extracts file paths from tool calls
- `FileSet.Merge()` — accumulates across rounds
- Rendered as appendix to every summary

Pi's key insight:
> 文件操作跨摘要累积。每次摘要都从「本次被摘要的消息 + 上一条摘要的 details.readFiles/modifiedFiles」合并提取，所以无论压缩多少轮，模型始终知道整个会话读过/改过哪些文件。

Corvus implements this exactly: `a.compactFileSet.Merge(regionFileSet)` at `compact.go:229` accumulates the projection, and `compaction.RenderFileSet()` appends it to every summary.

**No change needed**: The implementation is complete and correct.

#### 3. Fold economics check (implicit in research)

**Status**: ✅ Already implemented in `internal/compaction/compaction.go:47-50`

```go
func FoldEconomics(region []provider.Message) bool {
    const minFoldTokens = 400
    return EstimateMessagesTokens(region) >= minFoldTokens
}
```

Pi's `compaction.md` mentions this pattern (checking if fold saves enough to justify the API call), and corvus implements it correctly with a sensible threshold.

## Consequences

### Positive
- **Cache efficiency**: Summary requests no longer pollute the prompt cache for main conversation
- **Correctness documentation**: The tool-pairing rule is now explicitly documented so future changes won't accidentally break it
- **Validation**: External research confirms corvus's compaction implementation is high-quality and matches best practices

### Neutral
- No functional behavior changes to compaction (beyond cache isolation)
- No schema changes
- No migration required

### Risks
- None identified: these are documentation and hygiene improvements

## Alternatives Considered

### Session tree structure (P2.1)

**Deferred**: Requires structural change (adding `parentId` to session entries) and full tree navigation UI. High implementation cost, unclear immediate value without user demand for branching workflows.

**Decision**: Track for future consideration when/if users request conversation branching.

### Hook interception points expansion (P2.3)

**Deferred**: Pi's richer hook event model includes several points corvus lacks:
- `before_provider_request` — modify system prompt
- `tool_call` parameter modification — rewrite tool arguments
- `tool_result` chaining — transform/redact results

**Decision**: Tracked in `.scratch/IMPLEMENTATION_PLAN_2026-08-18.md` for future enhancement. Current hook system is adequate for immediate needs; these are quality-of-life improvements for power users.

### Client/server split (P2.2)

**Deferred**: Requires architectural change. Valuable for multi-client scenarios (web UI, IDE plugins) but premature without concrete need.

**Decision**: Design when multi-client support becomes a priority.

## References

- Research B: `.scratch/research/external-agent-design.md`
- Implementation Plan: `.scratch/IMPLEMENTATION_PLAN_2026-08-18.md`
- Pi compaction docs: `packages/coding-agent/docs/compaction.md` (external, reviewed)
- Pi extensions docs: `packages/coding-agent/docs/extensions.md` (external, reviewed)

## Related Decisions

- ADR 0006: Persistence durability tiers (session spill directory)
- ADR 0008: Audit findings tracking (P0-P3 prioritization)
