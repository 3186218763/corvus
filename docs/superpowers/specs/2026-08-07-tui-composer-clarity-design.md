# TUI Composer Clarity & Bottom-Chrome Slimming — Design

**Date:** 2026-08-07  
**Status:** Approved for implementation planning (revised after subagent design review)  
**Inputs:**
- Brainstorming session (2026-08-07): user-driven TUI polish — no-border translucent composer, cache-hit-only turn receipt, single-line footer, project path display, compact tool calls
- Codebase: Bubble Tea v2 TUI in `internal/cli` (`chat_tui.go`, `status_footer.go`, `theme.go`, `toolcard.go`, `transcript.go`, `composer_selection.go`)
- Design review (subagent, 2026-08-07): verified against lipgloss/textarea rendering; 4 blocking findings addressed below — background vs SGR-reset conflict, custom statusline output, native-scrollback Ctrl+B, collapsed empty slot

---

## 1. Problem

Corvus's bottom chrome currently reads busier than the peer coding TUIs:

| Current state | User impact |
|---------------|-------------|
| Composer has top+bottom `NormalBorder` lines ("两个线条") | Boxed-in feel; user wants a translucent, borderless field |
| Turn receipt shows total/in/out/reasoning/cost/estimated/prefix warnings | Too much noise; only cache hit matters |
| Footer below composer can be 2 rows + divider (status row + context/jobs data band) | Eats vertical space |
| Tool completion leaves `⎿ N lines` / `… N more lines (Ctrl+B)` summaries | Extra rows between tool calls |
| Project path (workspace root) of the current session is not visible | User cannot see which project the chat is rooted in |

---

## 2. Goals and non-goals

### Goals

1. Composer becomes a borderless field with a subtle background fill ("translucent" feel), no line art.
2. Turn token info reduces to a single cache-hit readout (`缓存命中 13.2K` / `cached 13.2K`), shown as `缓存命中 0` when zero.
3. Everything below the composer collapses to one logical line: status · project path · model · cache hit.
4. Project path is displayed `~`-abbreviated, ellipsis-truncated on narrow terminals.
5. Tool calls become vertically compact: no output summaries, no blank spacer before cards; `Ctrl+B` expansion still works in alt-screen mode.
6. History transcript (colors, bubbles, `◆` markers) is untouched.

### Non-goals

- Rounded-corner composer (custom cell painting — rejected in brainstorming; square background fill chosen).
- OSC-11 probe–driven dynamic blending of the composer fill (static per-theme slot; probe still used only for auto light/dark).
- Changing any transcript colors or history card styling.
- Reconfigurable footer fields (kept simple: fixed composition below).
- `Ctrl+B` output expansion in native-scrollback (Termux) mode (no stable anchor there; card-only after completion).

---

## 3. Design

### 3.1 Composer (input box)

**Background strategy (revised):** lipgloss `Background` on the outer style cannot cover the textarea content: the prompt (`SetPromptFunc` at `chat_tui.go:641-648`) and textarea internals emit SGR resets, so the field would render hollow. Instead:

- New per-line background painter, e.g. `renderComposerField(view string, width int, bg cliColor)`:
  - Splits the textarea view into lines; emits the background SGR at line start, re-arms it after every SGR reset encountered in the line (preserving the dim `❯` prompt, cursor, and selection styles), right-pads with background-armed spaces to the full `composerBoxWidth`, and ends with a reset.
  - Selection ranges are applied first by the existing `renderComposerInput` (`selStyle`); the painter re-arms the field background after the selection span's closing reset, so selection stays visible.
  - `!colorOn()` (NO_COLOR / NoTTY) → plain passthrough, no SGR.
- `inputBoxStyle` drops `Border` and `BorderForeground`. Shell-mode tint (`withThemeBorderFG(style, statusShellColor)` at `chat_tui.go:2915`) becomes a no-op and is removed; the `Shell` badge remains the mode indicator.
- New palette slot `inputBoxBG`:
  - dark base: `#1c2028` (slightly lighter than typical terminal black), hand-picked 256-color fallback (repo convention).
  - light base: `#eceff4` (slightly darker than typical terminal white), hand-picked fallback.
  - Accent styles reuse the base slot (styles only override accent/selection/userBubbleFaded today).
- Existing `border` slot stays for tool-card/diff edges and rules.

**Width and height math:**
- `composerBorderRows` 2 → 0; `inputHeightLimit` (`chat_tui.go:3573,3581,3583`), `bottomRows` (`chat_tui.go:1965-1988`), and `computeStatusLineCount` follow.
- `composerContentWidth` keeps its `-4` budget (the border rows were vertical only; `-4` = padding 1 + prompt 2 + 1 slack — the painter pads to full width so the slack is invisible). Keep the `input.Width() == composerContentWidth()-composerPromptWidth` invariant (`chat_tui_test.go:715`).
- `joinModeBadgeLeftOfComposer` (`chat_tui.go:3517`): badge always on row 0 (no border-row offset; the current `len>=3 → row 1` rule goes away).
- Cursor offsets: remove the top-border `+1` in both `View()` cursor paths (`chat_tui.go:3016` and `chat_tui.go:3046`); X-side `badgeCols + 1` (padding) unchanged; `composerOrigin()` (`composer_selection.go:357-368`) follows automatically.
- `computeStatusLineCount` `ctrl == nil` branch (`chat_tui.go:3436`) returns 1 (mirrors the new single-line status block; +1 for the working line while running).

### 3.2 Single footer line under the composer

- `renderStatusBlock` returns a single logical row; `statusFooterDivider` is removed together with its tests.
- Left group: existing `primaryStatusLine` (thinking spinner, shortcuts, rewind/MCP-import state, mouse tag) — unchanged.
- Right group (in order): project path · model · cache hit.
  - Custom statusline wins: when `statuslineCmd` is set and `statuslineOut` is non-empty, the right group is `statuslineOut` (existing contract: it replaces built-in fields; path/model/cache hidden).
  - Project path: `m.ctrl.WorkspaceRoot()`; fallback `os.Getwd()`; empty → omitted; home abbreviated to `~`; compacted via `compactMiddle` first (path owns the flexible slot), then model uses the remaining budget (existing `statusModelWorkGroup` budget logic).
  - Model: existing model/effort/work rendering retained.
  - Cache hit: `renderTurnReceipt` rewritten to emit only the cache-hit segment. Guard change: `u == nil` → `""`; any non-nil usage emits the segment (`缓存命中 0` when hit is 0). Total/in/out/reasoning/cost/estimated and the `CacheDiagnostics.PrefixChanged` warning are dropped. i18n key: zh `缓存命中`, zh-TW `快取命中` (consistent with existing `快取` terminology in `messages_zh_tw.go`), en `cached`.
- `m.turnReceipt` stores just that segment; the separate pinned receipt line in `View()` (`chat_tui.go:3005-3008`) is removed; `computeStatusLineCount` stops counting a receipt row.
- Context % and jobs are removed from permanent chrome (`/status` still exposes them).
- Narrow terminals: new `layoutSingleStatusLine(left, right, width)` — right-aligns the right group when it fits; otherwise wraps the combined line at ` · ` boundaries (reusing `wrapStatusGroups` on the joined line). It never re-introduces a second data band or divider. `rightAlignStatusGroup`'s overall-truncate path is not used for the new line.

### 3.3 Compact tool calls

- On `ToolResult`, the live output block is removed from the transcript — for zero-output and nonzero-output tools alike. No `⎿ N lines`, no preview, no `… N more lines (Ctrl+B)`; only the `● Verb(arg)` card line remains.
- `commitSpacer()` at the `ToolDispatch` path (`chat_tui.go:3880-3900`) is removed, covering both diff cards and plain tool cards, so consecutive tool cards stack directly.
- `Ctrl+B` expansion (alt-screen): the anchor moves to the tool-card block.
  - `transcriptSourceToolCard` gains an expandable output state (payload sourced from `shellOutputs`); collapsed renders card only; expanded renders card + connector output capped at `shellExpandMaxLines`.
  - `shellTranscriptIdx[id]` points at the card index; `toggleShellOutput` toggles the source state instead of rewriting a separate block; resize reflow preserves the expanded state because rendering derives from the source.
- The transient `⎿ working · Ns` live line stays (progress feedback, not a summary).
- Failure cards (`⊘ err`) unchanged.
- `streamToolOutput`'s unknown-id empty `commitLine("")` (`chat_tui.go:2198-2202`) is removed.
- Native scrollback (Termux): `ToolResult` commits no summary (output is not streamed there today; card-only after completion). `Ctrl+B` expansion is not supported in native mode (no anchor; avoids re-printing the card).

---

## 4. Edge behavior

- No Usage event yet → no cache segment (line renders without it).
- `m.ctrl == nil` (tests) → no path segment.
- NO_COLOR / NoTTY → background painter passes through; layout intact.
- Resize: single-line footer wraps at ` · ` boundaries; composer width recalcs without border rows; expanded tool output survives reflow via source state.
- Zero-output tool → card only, no empty slot (the live block is removed, so `wrapBlock("")`'s blank line never occurs).

---

## 5. Testing

- `theme_test.go`: `TestComposerBorderAndCursorTrackThemeAccent` → painter background assertions per theme + NoTTY no-background branch; divider color case removed with `statusFooterDivider`.
- `status_footer_test.go`: receipt tests assert only `缓存命中`/`cached` (including the `0` case); estimated/prefix-changed cases removed; divider cases removed; `TestStatusFooterDefaultOmitsBalanceGitCache` `CTX` assertion updated; `TestStatusFooterHeightCountUsesRenderedLayout` updated to the single-line budget.
- `chat_tui_test.go`: `TestTranscriptViewportSizing` idle `bottomRows` 4 → 2, `transcriptHeight` 20 → 22; `TestStatusLineWrapAccounting` and `TestStatusLineRenderedHeightMatchesBudget` updated to single-line wrapping; usage-event assertions → `cached 900` only; panel-table `+2` border rows → 0.
- `chat_render_test.go`: `TestTurnReceiptMovesBelowComposer` repurposed/removed; `TestToolProgressStreamsThenCollapses` (no `2 lines`), `TestToolWorkingLineThenClears`, `TestConsecutiveToolCallsKeepMarkersUnderOwnCard`, `TestCollapsedShellHintUsesKeyboardShortcutOnly`, `TestConsecutiveNonShellToolsDoNotRenderNegativeLineCount`, `TestToolProgressTailCap` updated to card-only completion.
- `transcript_test.go`: `⎿` render case updated for the expandable card source.
- New tests:
  - Background continuity: every visible cell of every rendered composer line carries the background SGR (hollow-field regression).
  - No-border composer: badge always on row 0 with multi-line input; cursor Y has no border offset; `bottomRows` idle = 2, running = 3.
  - Single-line footer: status · path · model · cache composition, `~` abbreviation, ctrl-nil fallback, narrow-terminal wrap (30–46 columns).
  - Ctrl+B: collapsed completion leaves no summary row; expand shows output; resize keeps expanded state; native branch behavior; zero-output tool leaves no blank row.
- Verify with `go build ./...` and `go test ./internal/cli/...`.

---

## 6. Files touched

- `internal/cli/theme.go` — palette slot, `refreshCLIStyles`
- `internal/cli/chat_tui.go` — composer painter wiring, badge join, cursor offsets, layout math, tool collapse, footer wiring
- `internal/cli/status_footer.go` — single-line footer, cache-only receipt, `layoutSingleStatusLine`, divider removal
- `internal/cli/toolcard.go` — expandable card rendering / connector output helpers
- `internal/cli/transcript.go` — tool-card source expansion state
- `internal/cli/composer_selection.go` — background painter, selection/paint order
- `internal/i18n/*` — cache-hit label key (zh / zh-TW / en)
- Tests listed in §5; this spec is committed before planning.
