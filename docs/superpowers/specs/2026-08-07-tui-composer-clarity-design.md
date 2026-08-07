# TUI Composer Clarity & Bottom-Chrome Slimming — Design

**Date:** 2026-08-07  
**Status:** Approved for implementation planning  
**Inputs:**
- Brainstorming session (2026-08-07): user-driven TUI polish — no-border translucent composer, cache-hit-only turn receipt, single-line footer, project path display, compact tool calls
- Codebase: Bubble Tea v2 TUI in `internal/cli` (`chat_tui.go`, `status_footer.go`, `theme.go`, `toolcard.go`, `composer_selection.go`)

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
5. Tool calls become vertically compact: no output summaries, no blank spacer before cards; `Ctrl+B` expansion still works.
6. History transcript (colors, bubbles, `◆` markers) is untouched.

### Non-goals

- Rounded-corner composer (custom cell painting — rejected in brainstorming; square background fill chosen).
- OSC-11 probe–driven dynamic blending of the composer fill (static per-theme slot; probe still used only for auto light/dark).
- Changing any transcript colors or history card styling.
- Reconfigurable footer fields (kept simple: fixed composition below).

---

## 3. Design

### 3.1 Composer (input box)

- `inputBoxStyle` loses `Border(NormalBorder(), true, false, true, false)` and `BorderForeground`; gains `Background(themeLipColor(inputBoxBG))` with `PaddingLeft(1)` unchanged.
- New palette slot `inputBoxBG`:
  - dark base: `#1c2028` (slightly lighter than typical terminal black), hand-picked 256-color fallback (repo convention).
  - light base: `#eceff4` (slightly darker than typical terminal white), hand-picked fallback.
  - Accent styles reuse the base slot (styles only override accent/selection/userBubbleFaded today).
- Existing `border` slot stays for tool-card/diff edges and rules.
- Layout math updates:
  - `composerBorderRows` 2 → 0; `inputHeightLimit`, `bottomRows`, `computeStatusLineCount` follow.
  - `joinModeBadgeLeftOfComposer`: badge always on row 0 (no border row offset).
  - `composerContentWidth` budget drops the 2 border cells (verify exact chrome accounting vs prompt 2 + padding 1).
  - Composer cursor math (`composerCursor`) re-verified (badge column + padding only).
- `applyTextareaTheme`, prompt, and textarea behavior unchanged.

### 3.2 Single footer line under the composer

- `renderStatusBlock` returns a single logical row (no second data band, no `statusFooterDivider`); `statusFooterDivider` function is removed with its tests.
- Left group: existing `primaryStatusLine` (thinking spinner, shortcuts, rewind/MCP-import state, mouse tag).
- Right group (in order): project path · model · cache hit.
  - Project path: `m.ctrl.WorkspaceRoot()`; fallback `os.Getwd()`; empty → omitted; home abbreviated to `~`; `compactMiddle` when it cannot fit.
  - Model: existing `statusModelWorkGroup` model/effort/work logic retained (path sits before it).
  - Cache hit: `renderTurnReceipt` rewritten to emit only the cache-hit segment; no total/in/out/reasoning/cost/estimated; `CacheDiagnostics.PrefixChanged` warning dropped. `i18n` key added: zh `缓存命中`, zh-TW `緩存命中`, en `cached`.
- `m.turnReceipt` now stores just that segment; the separate pinned receipt line in `View()` is removed; `computeStatusLineCount` stops counting a receipt row.
- Drop `renderContextStatusGroups` output and jobs from permanent chrome (context % remains available via `/status`).
- Narrow terminals: the single logical line wraps at semantic-group boundaries (existing `wrapStatusGroups`); it never re-introduces a two-row data band.

### 3.3 Compact tool calls

- On `ToolResult`, the live output block is removed from the transcript (no `⎿ N lines`, no preview, no `… N more lines (Ctrl+B)`). Only the `● Verb(arg)` card line remains.
- `commitSpacer()` before tool cards is removed so consecutive tool calls stack directly.
- `Ctrl+B` expansion preserved: expansion state attaches to the tool-card transcript block (expanded = card + connector output; collapsed = card only), backed by `shellOutputs` full text; resize reflow keeps behavior.
- The transient `⎿ working · Ns` live line stays (progress feedback, not a summary).
- Failure cards (`⊘ err`) unchanged.
- Termux native-scrollback path: no line-count commit on completion.

---

## 4. Data flow & edge behavior

- No Usage event yet → no cache segment (line renders without it).
- `m.ctrl == nil` (tests) → no path segment.
- NO_COLOR / NoTTY → background fill disabled (existing `colorOn()` gate), text-only layout preserved.
- Resize: single-line footer wraps per `wrapStatusGroups`; composer width recalcs without border rows.
- Tool with zero output → card only (no empty slot), same as nonzero.

---

## 5. Testing

- Update `theme_test.go`: composer border assertions → background slot assertions (per theme + NO_COLOR).
- Update `status_footer_test.go`: receipt tests assert only `缓存命中`/`cached` (+ `0`); remove estimated/prefix-changed cases; remove divider cases with the function.
- Update `chat_tui_test.go`: `bottomRows` 4 → 2 (input 1 + status 1), `transcriptHeight` expectations, `turnReceipt` assertions (`cached 900`), tool-summary cases → "no summary" behavior.
- Update tool-collapse/`Ctrl+B` tests to the card-block expansion model; add a compact-spacing assertion (no spacer, no summary row).
- Add single-line footer composition test (status · path · model · cache-hit; `~` abbreviation; narrow-wrap).
- Verify with `go build ./...` and `go test ./internal/cli/...`.

---

## 6. Files touched

- `internal/cli/theme.go` (palette slot, `refreshCLIStyles`)
- `internal/cli/chat_tui.go` (composer style join, layout math, tool collapse, footer wiring)
- `internal/cli/status_footer.go` (single-line footer, cache-hit-only receipt, divider removal)
- `internal/cli/toolcard.go` (card-block expansion render helpers as needed)
- `internal/cli/composer_selection.go` (badge join if touched)
- `internal/i18n/*` (cache-hit label key)
- Tests listed in §5; this spec is committed before planning.
