# Codex-Style Composer Popup, Cache Hit-Rate & Footer Label Casing — Design

**Date:** 2026-08-07
**Status:** Approved for implementation planning
**Inputs:**
- Brainstorming session (2026-08-07): user-driven TUI behavior alignment with Codex CLI
- Open-source research (2026-08-07): `openai/codex` `codex-rs/tui` source + live observation of `codex-cli 0.146.1`
- Codebase: Bubble Tea v2 TUI in `internal/cli` (`chat_tui.go`, `status_footer.go`, `complete.go`, `i18n/messages_en.go`)

---

## 1. Problem

Corvus's bottom-chrome popup behavior diverges from Codex CLI in three ways the user called out:

| Current state | Codex behavior |
|---------------|----------------|
| Slash/`@`/pickers pop **above** the composer, pushing the input box down | Popups expand **below** the input box; the input stays raised with the menu in the space beneath it |
| After dismissing a popup the layout immediately collapses back | The raised input position is **kept** after cancel; it only returns to the bottom when the agent starts executing |
| Turn receipt below the chat shows **cached token count** (`cached 1234`) | User wants the **current conversation's cache hit rate** (`cached 87.50%`) |
| Footer labels are ALL CAPS (`MODEL EFFORT WORK`) | User wants Title Case (`Model Effort Work`) |

## 2. Goals and non-goals

### Goals

1. All bottom popups (slash completion, `@`-references, chooser, cheatsheet, todo, rewind, approval, etc.) render **below** the composer instead of above.
2. Composer raises when a popup opens; the raised position **persists after cancel**; it drops back to the bottom only when the user submits a message (agent starts executing).
3. Turn receipt shows the session-aggregate cache hit rate (`Σhit / Σ(hit+miss)`, `%.2f%%`), not a token count.
4. English footer labels become Title Case: `Model`, `Effort`, `Work`.

### Non-goals

- Floating/overlay popups drawn on top of the transcript (z-order, mouse hit-testing, repaint isolation) — rejected; layout-order change chosen.
- Changing any transcript colors or history card styling.
- Changing `/status` diagnostic view content or formatting.
- Touching Chinese locale labels (`模型`/`强度`/`模式`, and `模型`/`強度`/`模式` for zh-TW) — already localized.
- Reserving the raised space permanently (the space must collapse when a turn starts).

## 3. Research: how Codex does it

Source: `openai/codex` `codex-rs/tui`, plus live observation of `codex-cli 0.146.1` in a PTY.

- `chat_composer.rs` `layout_areas_with_textarea_right_reserve`:
  `Layout::vertical([Constraint::Min(3), popup_constraint]).areas(area)` — the input sits at the **top** of the composer widget, the popup renders **below** it; the footer/status line renders under the popup region.
- `chat_composer.rs` `desired_height_with_textarea_right_reserve` = textarea height + padding + (popup required height | footer height). Opening a popup grows the bottom pane's desired height, so the input rises and the popup fills the space beneath it.
- `app.rs` `with_chat_widget_frame` renders the whole chat widget at `desired_height` with a bottom-aligned viewport; `bottom_pane/mod.rs` `as_renderable` stacks status/preview rows (flex) above the composer.
- Observed (PTY, 24-row terminal): idle — input row 12, status row 14; type `/` — input stays at row 12, command menu rows 14–22, status pushed below the menu; Esc — menu gone, input still at row 12, status back at row 14. Submitting closes the popup and returns the layout to idle.

## 4. Design

### 4.1 Layout reorder (popups below the composer)

In `chat_tui.go` `View()` the bottom-region `parts` are joined in this order today:

```
[panels…] [thinking] [managerFooter] [queueIndicator] [composer] [statusBlock]
```

New order:

```
[thinking] [queueIndicator] [composer] [panels…] [managerFooter] [statusBlock]
```

- Panel render functions are unchanged; only the join order moves.
- `rowsAboveBox` counts only the thinking line, managerFooter, and queue indicator — **not** the panels. The composer cursor anchor uses the reduced `rowsAboveBox` in both native-scrollback and alt-screen paths.
- `bottomRows()` is unchanged: panels still count toward the pinned bottom region, so `transcriptHeight()` shrinks by the popup height exactly as today — the status row can never be pushed off-screen.
- When the composer is hidden (`hideComposer()` modal states), the panels simply render in the below-composer slot, i.e. just above the status block; no special-casing needed.

### 4.2 Raised-composer state machine

New field on `chatTUI`, e.g. `composerRaisedRows int` (0 = not raised).

- **Raise:** whenever a bottom panel is rendered with nonzero height **while the composer is visible** (completion menu, chooser, cheatsheet, todo, rewind, etc.), record that panel height into `composerRaisedRows`. Hidden-composer modal states (approval, non-typing chooser, etc.) never set it.
- **Hold:** when the panel closes (Esc, selection, etc.), keep `composerRaisedRows`; `bottomRows()` adds the held rows and `View()` renders that many blank rows between the composer and the status block, so the input stays at its raised position with empty space beneath.
- **Drop:** the only clearing condition is the user submitting a message (turn starts / agent begins executing). Clear `composerRaisedRows`; the input returns to the bottom. It stays down after the turn until the next popup raises it again.
- Hidden-composer modal states neither set nor clear the flag.
- The held space is the last popup's height, so closing a popup never causes a visible jump in the input position.

### 4.3 Cache hit-rate readout

- `renderTurnReceipt` (`status_footer.go:56`) changes from `cached <CacheHitTokens>` to the session-aggregate rate:
  `footerMetric(i18n.M.ChatCacheHitLabel, footerValue(cacheRateLabel("%s", hit, hit+miss)))` where `hit, miss = m.ctrl.SessionCache()`.
- `cacheRateLabel` already formats `%.2f%%` (used by `/status`); keep the format consistent.
- Still updated on the turn-completed event (`chat_tui.go:3782`); hidden when `hit+miss == 0` (no cache data yet), same as today.
- Labels unchanged: `cached` (en) / `缓存命中` (zh) / `快取命中` (zh-TW).

### 4.4 Footer label casing

`internal/i18n/messages_en.go`:

- `ChatStatusModelLabel: "MODEL"` → `"Model"`
- `ChatStatusEffortLabel: "EFFORT"` → `"Effort"`
- `ChatStatusWorkLabel: "WORK"` → `"Work"`

zh / zh-TW labels untouched. Update the stale "uppercase semantic label" comment near `chat_tui.go:4122`.

## 5. Testing

- `status_footer_test.go`: replace `MODEL/EFFORT/WORK` assertions with `Model/Effort/Work`; `cached 900`-style receipt assertions become percentage assertions (e.g. `cached 90.00%`).
- `statusline_test.go`: same label replacements.
- `chat_render_test.go`: receipt-below-composer assertions now check the percentage form.
- New raised-state tests:
  - popup open → input above panels, menu below the composer;
  - cancel → input stays at the raised row (held blank rows rendered);
  - submit → `composerRaisedRows` cleared, input back at the bottom;
  - no cache data → no receipt.
- Manual verification: `make build`, run the TUI, check `/` menu direction, cancel-hold, and drop-on-submit; verify `cached` percentage appears after a turn.

## 6. Risks

- **Cursor anchoring:** the composer cursor Y offset must drop the panel rows or the caret will land in the wrong terminal row. Covered by the `rowsAboveBox` change plus the existing cursor tests.
- **Held blank rows in native scrollback mode:** blank rows participate in the pinned bottom region there too; the same `bottomRows()` accounting keeps the status row visible.
- **Popups that open while raised:** the held height is overwritten by the new popup's height, keeping the input stable across open/cancel cycles.
