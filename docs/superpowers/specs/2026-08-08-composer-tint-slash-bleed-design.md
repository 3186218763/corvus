# Composer Tint & Slash-Menu Background Bleed — Design

**Date:** 2026-08-08  
**Status:** Approved for implementation planning  
**Inputs:**
- User report: composer fill is too dark/solid (not Codex-like translucent lift relative to terminal background); typing `/` paints the first completion row with the composer background
- Live verification (2026-08-08): PTY capture of `bin/corvus` with truecolor; unit dump of `renderComposerField` + `renderCompletion`
- Codebase: `internal/cli/composer_selection.go` (`renderComposerField`), `internal/cli/theme.go` (`inputBoxTintFromBackground`, `inputBoxBG` fallback), `internal/cli/complete.go` (`renderCompletion`)
- Prior specs: `2026-08-07-tui-composer-clarity-design.md` (painter; “ends with a reset”), `2026-08-07-codex-blue-theme-design.md` (relative-bg tint)

---

## 1. Problem

| Observed | Root cause |
|----------|------------|
| Composer has a fill but reads **too dark and solid**, not a light translucent lift over the terminal background | Dark fallback `inputBoxBG` is `#1c2534` (rgb 28,37,52). When OSC-11 probe is unavailable (common), that fixed navy slab is used. When probe works, dark lift is **32% toward white**, which becomes a mid-grey hard panel rather than a soft lift. |
| Typing `/` makes the **first slash option** share the composer background | `renderComposerField` arms `bgSGR(inputBoxBG)` per line and re-arms after SGR resets, but **never ends the line with a reset**. The open background SGR survives the newline into the first completion row. Live cell-diff showed `48;2;28;37;52` on `›` then `49` only later on `/compact`. |

Design note: `2026-08-07-tui-composer-clarity-design.md` already required the painter to “end with a reset”; the implementation omitted that step.

### Live evidence (summary)

- Composer line tail: `…\x1b[m\x1b[48;2;28;37;52m` — background left open.
- First menu row: `38;2;74;155;255;48;2;28;37;52m› ` then `\x1b[49;1m/compact` — bleed then partial clear.
- Unit dump: `boxEndsWithReset=false` for fallback and probed paths.
- This environment did not emit OSC-11; runtime used fallback `#1c2534`.

---

## 2. Goals and non-goals

### Goals

1. Every composer field line ends with a full SGR reset so **no background attribute leaks** into the row below (slash/`@` completion, held blank space, status, or any panel joined under the box).
2. Dark composer tint reads as a **soft lift** relative to the terminal background (Codex-like), not a deep solid slab.
3. Dark **fallback** (no probe) is light enough to look intentional on typical dark terminals, not nearly-black navy.
4. Light mode stays symmetric (slightly recessed field) without over-darkening.
5. Existing painter contract preserved: continuous per-line fill, re-arm after resets, selection reverse survives, `NO_COLOR` / NoTTY passthrough.

### Non-goals

- Changing slash menu layout, selection style, hints, or popup-below-composer behavior.
- Reintroducing lipgloss borders on the composer.
- Dual-stage blend formula (8% lift × 84% mix) from the original blue-theme sketch — single-ratio `mixHex` stays.
- Forcing OSC-11 success; fallback must look good on its own.
- Transcript, footer, accent palette, tool cards, or theme style list changes beyond `inputBoxBG` math/fallback.

---

## 3. Design

### 3.1 Close the background SGR (`renderComposerField`)

**File:** `internal/cli/composer_selection.go`

After painting each line (open with `bg`, `rearmFieldBackground`, right-pad with `ansiReset + bg + spaces` when short), **append `ansiReset`** so the line always terminates with a full reset.

```text
[bg][line with re-armed bg after each reset][optional pad: reset+bg+spaces][ansiReset]
```

Invariants:

- Full-width lines (no pad) still end with `ansiReset`.
- Multi-line composers: each line resets; next composer line re-opens `bg` at its start (unchanged).
- `NO_COLOR`: still pure passthrough (no bg, no extra reset).
- Selection reverse (`\x1b[7m` … reset) still re-arms field bg after the selection’s reset; trailing line reset only fires at end of line.

### 3.2 Soften relative tint + lighten fallback (`theme.go`)

**Probed path** — `inputBoxTintFromBackground`:

| Mode | Current ratio | New ratio | Direction |
|------|---------------|-----------|-----------|
| dark | 0.32 toward `#ffffff` | **0.16** toward `#ffffff` | Soft translucent lift (≈ half the current hard panel) |
| light | 0.15 toward `#000000` | **0.10** toward `#000000` | Milder recessed field |

Formula unchanged: `final = mixHex(bg, ref, ratio)`; xterm via `ansi.Convert256` on the result.

**Fallback path** (no `activeBackgroundProbe` hit) — palette slots on the base themes:

| Slot | Current | New |
|------|---------|-----|
| dark `inputBoxBG` | `#1c2534` / xterm 235 | **`#2a3140` / xterm 236** (hand-picked curated index — do **not** use `ansi.Convert256`, which maps this hex to low index 23 and loses the grey-blue read; same convention as other palette slots) |
| light `inputBoxBG` | `#eceff4` / 255 | **unchanged** |

**Probed fixture pins** (recomputed at the new ratios for existing test RGB samples; xterm via `ansi.Convert256` only on the *computed* tint, which is the existing probed-path rule):

| Probe RGB | Mode | New hex | Notes |
|-----------|------|---------|-------|
| 10,12,16 | dark 0.16 | `#313336` | was `#585a5c` at 0.32 |
| 0,0,0 | dark 0.16 | `#292929` | was `#525252` at 0.32 |
| 48,10,36 | dark 0.16 | `#513147` | was `#72586a` at 0.32 |
| 240,242,245 | light 0.10 | `#d8dadd` | was `#ccced0` at 0.15 |
| 255,255,255 | light 0.10 | `#e6e6e6` | was `#d9d9d9` at 0.15 |

Xterm indices for those probed hex values are whatever `ansi.Convert256` returns at implement time; pin the full `cliColor` in tests after one local compute (same as today).

`buildCLITheme` still overwrites `inputBoxBG` only when probe succeeds; tests that force `noTerminalBackground` assert the new fallback.

### 3.3 Rendering chain (unchanged wiring)

```text
renderComposerField(view, width)
  → composerFieldBackground() → bgSGR(activeCLITheme.inputBoxBG)
  → per-line paint + trailing reset
View() joins: [composer box] \n [completion…]
```

No changes to `renderCompletion`, `bottom_panels.go`, or raised-composer state machine.

---

## 4. Testing

### Bleed / painter (`composer_selection_test.go`)

1. **Trailing reset:** painted field (short and full-width content) ends with `\x1b[0m` or equivalent full reset; last non-reset SGR must not be an open `48;…` background.
2. **No bleed into next row:** build `joined = renderComposerField(…) + "\n" + firstCompletionLine` (or a synthetic first menu line with only fg SGR). After the newline, the first menu line’s cells must not inherit an open composer `48;…` from the previous line — assert the composer segment ends with reset before `\n`.
3. Keep: continuous bg re-arm after `\x1b[0m` / `\x1b[m`, selection reverse survives, `NO_COLOR` passthrough.

### Tint (`theme_test.go`)

1. Update pins for dark fallback `inputBoxBG` → `cliColor{"#2a3140", 236}`.
2. Update `inputBoxTintFromBackground` expected values for fixtures in §3.2 (ratios 0.16 / 0.10); re-pin xterm from `Convert256` on the new hex.
3. Keep: probe overrides fallback; probe-off uses fallback; light/dark branch selection.
4. Any `mixHex(..., 0.32)` example assertions in `theme_test.go` that document the old lift must move to 0.16 (or assert the named constant).

### Regression smoke

```text
go test ./internal/cli/ -run 'TestComposerField|TestComposerTint|TestInputBoxTint|inputBoxTint'
```

Optional manual: run TUI, type `/`, confirm first row has no composer slab; empty composer reads as a soft lift on dark terminals with and without probe.

---

## 5. Implementation notes

- Touch only `composer_selection.go`, `theme.go`, and the tests above (plus any hard-coded `#1c2534` / 0.32 ratio comments or pins found by search).
- Do not change `compSelStyle`, completion padding (NBSP), or popup order.
- Ratio constants should be named (e.g. `inputBoxDarkLift`, `inputBoxLightSink`) next to `inputBoxTintFromBackground` so the next tweak is one place.

---

## 6. Risks

| Risk | Mitigation |
|------|------------|
| 0.16 lift too subtle on very dark true-black terminals | Fallback `#2a3140` covers no-probe; if probe returns pure black, 0.16 still yields a visible but soft grey — adjust ratio only if manual check fails |
| Some terminals treat `\x1b[m` vs `\x1b[0m` differently | Use the same `ansiReset` constant already used by the painter pad path |
| Cell-diff renderer might still copy bg attributes from prior cells | Trailing reset is necessary; if a residual remains after fix, follow-up would reset at the start of `renderCompletion` — out of scope unless verified post-fix |

---

## 7. Success criteria

1. Typing `/` does not paint the first completion option with the composer background.
2. Dark composer fill is visibly lighter than a near-black slab and softer than the old 32% grey panel.
3. No-probe dark sessions use `#2a3140` (or the pinned equivalent) and still look like a field, not empty chrome.
4. Existing composer painter and theme tests pass with updated pins.
