# TUI Codex Rhythm Pack — Markers, Weight, Spacing — Design

**Date:** 2026-08-08  
**Status:** Approved for implementation planning  
**Approach:** Codex-faithful rhythm pack (render-layer only)  
**Prior art:** Density pack already landed (`›`/`•`, Explored coalesce, ambient thinking, plain assistant, thin footer). This pass fixes remaining monotony (everything looks like `•`), vertical density, and missing size/weight hierarchy.

**Inputs:**
- Live code: `internal/cli/{toolcard,transcript,chat_tui,status_footer,theme,diffview}.go`
- Codex CLI source (`/tmp/codex-src/codex-rs/tui`): `history_cell/{messages,separators,patches}.rs`, `exec_cell/render.rs`, `style.rs`, `status_indicator_widget.rs`, `chatwidget/turn_runtime.rs`, chatwidget snapshots
- Brainstorm: full rhythm pass (user bubble, tools, assistant, gaps, separators, banner, working line)

---

## 1. Problem

After the density pack, the transcript is closer to Codex in *shape* but still feels dense and flat:

| Area | Current Corvus | Desired (Codex rhythm) |
|------|----------------|------------------------|
| Markers | Nearly all rows use the same dim `•` | Same primary anchors (`›` / `•`) plus **structure** (`└` `│` `$`) and **outcome** (`✓` `✗`) and **turn rule** (`─`) |
| Weight | Verbs, paths, bullets at similar strength | Bold verbs; dim bullets/gutters; cyan tree verbs; green/red outcomes |
| Spacing | `commitSpacer` only; live vs replay `\n\n` drift | Explicit gap state machine; 0 inside trees; 1 between cells; user block pad |
| User | Accent `›` line, no full-line wash; no-color uses `│ ›` | Soft full-line bg + `›` bold+dim + vertical pad rows |
| Assistant | Two-space indent only | First line `• ` dim; continuations `  ` |
| Explored | Sibling `├` / `└` tree (heavy) | Single `└` hanging all leaves |
| Ran | Card + collapsed output by default | Short preview (≤5 lines), `│` continuations, `✓`/`✗ · duration` |
| Turn end | No visual break after tool work | Dim `─` rule when turn had concrete work |
| Banner | Two lines (wordmark + tip) | Single line `◆ corvus · model` |

---

## 2. Goals and non-goals

### Goals

1. **Marker taxonomy** — primary `›`/`•`; structural `└`/`│`; outcome `✓`/`✗`; turn `─`; banner `◆` only at session top.
2. **Weight ladder** — primary body, strong (bold verbs / outcomes), accent structure (cyan tree verbs, bash highlight), secondary (dim bullets/gutters/elapsed/rules).
3. **Gap control** — shared live/replay rules; no double blanks; tree-internal density 0.
4. **User bubble** — full-line soft background with pad rows (theme `userBubbleBG` / blend).
5. **Assistant** — dim `• ` on first line of each answer block.
6. **Explored** — single-`└` nest; keep Read coalesce and max-5 leaves.
7. **Ran** — default ≤5-line preview; command wrap via `│`; success/fail markers.
8. **Turn separator** — after turns with tool/exec/edit work only.
9. **Banner** — one line; drop tip line.
10. **Working** — ambient above composer only; no thinking/working walls in transcript (default).
11. **Tests** — pure render + gap/separator + updated card/assistant/user tests.

### Non-goals

- Agent runtime, tool protocol, providers.
- HistoryCell graph rewrite or Ratatui port.
- Footer redesign to Codex `? for shortcuts` / `100% context left`.
- Pixel-perfect Codex match.
- Replacing letter selectors / diff full-line bg (already done) except where prefixes touch them.
- New category-colored `●` rainbow.

---

## 3. Design principles

1. **Rhythm over decoration** — fewer competing glyphs; weight and blank space do the hierarchy work.
2. **Codex-faithful, not Codex-cloned** — same contracts from source/snapshots; Corvus product name only on banner.
3. **Render-layer only** — coalesce, gaps, and restyle at TUI ingest/render.
4. **One gap API** — live commit path and history rebuild share the same helpers.
5. **Progressive disclosure** — default answers “what happened?”; Ctrl+B for full shell output.

---

## 4. Marker and weight contracts

### 4.1 Markers

| Role | Glyph | Style |
|------|-------|--------|
| User | `› ` | bold+dim on soft bg rows |
| Assistant body | first line `• `, rest `  ` | bullet dim; body default fg |
| Tool header | `• Explored` / `• Ran` / `• Edited` | bullet dim (green/red bold when terminal success/fail); **verb bold** |
| Explored leaves | under one `  └ ` then `    ` | `└` dim; verb **cyan**; path default |
| Ran command wrap | `  │ ` | dim |
| Ran / tool output | first `  └ `, rest `    ` | dim; ≤5 lines default |
| Outcome | `✓` / `✗` | green/red bold + dim ` · duration` |
| Turn separator | full-width `─` or `─ Worked for … ─` + fill | all dim |
| Live working | above composer | not a transcript wall |
| Session banner | `◆ corvus · model` | accent + dim model; **one line** |

**Removed / avoided:** multi-color category `●`; no-color `│ ›` user prefix; Explored sibling `├` tree; permanent tip under banner.

### 4.2 Weight ladder (terminal “size”)

1. **Primary** — current user body, assistant prose (default fg).  
2. **Strong** — tool verbs; `✓`/`✗`; success/fail header bullets.  
3. **Accent structure** — cyan Search/Read/List; bash syntax highlight.  
4. **Secondary** — dim bullets, gutters, elapsed, footer secondary, turn rules.

History turns: faded user fg; same bg treatment; no second marker system.

### 4.3 Gap state machine

| Boundary | Blank rows | Notes |
|----------|------------|--------|
| Inside Explored/Ran tree | 0 | In-place updates only |
| Between same-turn cells | 1 | assistant ↔ tool ↔ tool |
| Before/after user message | 1 + bubble internal pad rows | Pad rows are part of the bubble, not extra double-spacers |
| After turn with work | 1 rule line (`─…`) | Only if `hadWorkActivity` |
| After banner before first user | 1 | Banner is single line |

Rules:

- `ensureBlank()`: if last transcript line is non-empty, commit `""`.  
- Never insert a blank if one already trails.  
- Live and replay both call the same helpers (replace ad-hoc `"\n\n"` accumulation).

### 4.4 User bubble

```text
[bg pad row full width]
[bg]› message…[nbsp pad][reset]
[bg pad row full width]
```

- Color on: `bgSGR(userBubbleBG)` from first cell; re-arm after mid-line resets if any; NBSP pad to content width; trailing reset (same survival strategy as diff bars).  
- Color off: `› text` only (no `│`).  
- Prefer blend from probed terminal bg when available (Codex: dark lift white @ ~0.12, light sink black @ ~0.04); else theme `userBubbleBG` fallback.  
- Width = transcript content width.

### 4.5 Assistant

- `renderAssistantMarkdown` / `assistantBlock`: first visible line prefixed with dim `• `; subsequent lines `  `.  
- Reserve 2 columns in body width.  
- No product nameplate.

### 4.6 Explored

```text
• Explored
  └ Search pattern
    Read a.go, b.go
    List path
```

- Drop `├` mid branches.  
- Keep consecutive Read comma-join and `exploreMaxLeaves` (+N more).  
- Header: dim `•` + bold `Explored`.

### 4.7 Ran / output

```text
• Ran <highlighted command>
  │ <continuation ≤2 lines>
  └ <preview line>
    …
  ✓ · 0.41s
```

- Default preview cap: **5** lines (Codex `TOOL_CALL_MAX_LINES`); then dim `… +N lines`.  
- Command continuation prefix: `  │ `.  
- Output prefix: `  └ ` / `    `.  
- Ctrl+B still expands full output / collapses to card+preview policy.  
- Failed runs: red bullet and/or `✗ (code) · duration`.

### 4.8 Edited

- Single file: `• Edited path (+n −m)` when counts known.  
- Multi-file (if already aggregated): `• Edited N files (+a −b)` with `└ path` rows.  
- Diff body: existing full-line bg renderer.

### 4.9 Turn separator

Emit when turn completes and `hadWorkActivity` is true:

- `hadWorkActivity`: any tool card committed this turn (explore/ran/edited/mcp/etc.), not pure prose-only turns.  
- Elapsed unknown or ≤60s: dim full-width `─`.  
- Elapsed >60s: `─ Worked for {compact} ─` then fill with `─` to width (Codex `FinalMessageSeparator`).  
- Optional runtime metrics: **out of scope for v1** unless already trivial to surface.

### 4.10 Banner and working line

- `renderTUIBanner`: single line `◆ corvus · model`; drop `ChatTip` second line; keep missing-key line if needed.  
- `runningWorkingLine`: keep above composer; shape toward `• Working (Ns · esc…)` / existing i18n equivalent.  
- Stop writing braille `working…` connector walls into the transcript for normal tool streams (default). Verbose reasoning path unchanged.

### 4.11 Footer

- Unchanged thin row: interaction + `model · path`.  
- Not part of this pack’s redesign.

---

## 5. Architecture

```text
agent events (unchanged)
        │
        ▼
chat_tui ingest
  · explore coalesce (existing)
  · gap helpers (ensureBlank / cell boundaries)
  · hadWorkActivity for turn rule
  · reasoning → ambient only (existing)
        │
        ▼
pure render helpers
  renderUserBubble     → bg + pad + ›
  assistantBlock       → • first line
  exploredCard         → single └ nest
  bashToolCard/output  → │ / └ / preview / ✓✗
  finalSeparator       → dim ─ rule
  renderTUIBanner      → one line
        │
        ▼
transcript viewport / native scrollback
```

**Primary files:**  
`internal/cli/toolcard.go`, `transcript.go`, `chat_tui.go`, `theme.go` (user bg blend if needed), optional `gap.go` / `separators.go`, matching `*_test.go`.

**Out of scope files:** `internal/agent/*`, `internal/tool/*`, providers.

---

## 6. Data flow

| Event | Behavior |
|-------|----------|
| User submit | flush explore → ensureBlank → user bubble |
| Assistant commit | ensureBlank → `•` markdown block |
| Explore tools | merge in-place or open Explored after ensureBlank |
| Bash / write | flush explore → ensureBlank → Ran/Edited; update in-place; set hadWork |
| Turn end | clear ambient reasoning → if hadWork then ensureBlank + `─` separator → reset hadWork |
| Ctrl+B | expand/collapse shell preview (existing anchors) |

---

## 7. Acceptance

### 7.1 Manual checklist (fixed multi-turn script)

1. Read-only diagnosis (multiple Search/Read).  
2. Small chrome tweak + run tests.  
3. Coalesce more reads.  
4. Run tests again; inspect vertical rhythm.

| # | Check |
|---|--------|
| 1 | User: soft full-line bg + `›`; no `│ ›` |
| 2 | Assistant: first-line `• `; no `◆ Corvus` |
| 3 | Explored: one `└`, no `├`; Read coalesce |
| 4 | Ran: bold verb; `│` wrap; ≤5 preview; `✓`/`✗ · duration` |
| 5 | Exactly one blank between cells; zero inside trees; no double blank |
| 6 | Dim `─` after tool-bearing turns; none after pure chat |
| 7 | Banner single line; no tip row |
| 8 | Working ambient only; no thinking wall in default mode |
| 9 | Footer remains one thin row |
| 10 | Clearer weight hierarchy (bold / dim / cyan) |

### 7.2 Automated tests (minimum)

| Area | Coverage |
|------|----------|
| User bubble | bg/pad when color on; no `│` when color off |
| Assistant | first line has `•`; no nameplate |
| Explored | single `└`; no `├`; Read merge |
| Ran | `│` / `└` prefixes; preview cap; outcome marker |
| Gap | one blank between two cells; no double; tree internal 0 |
| Separator | tool turn → rule; prose-only → none |
| Banner | no tip substring; single content line |
| Package | `go test ./internal/cli/ -count=1` relevant suites green |

---

## 8. Implementation order

1. Gap helpers + unify live/replay spacing + tests.  
2. Assistant `•` prefix + one-line banner.  
3. User bubble full-line bg + pad.  
4. Explored single-`└` tree + verb weights.  
5. Ran `│`/`└` + default 5-line preview + `✓`/`✗`.  
6. Turn `─` separator (`hadWorkActivity`).  
7. Working-line cleanup (no transcript working wall).  
8. Full cli tests + manual checklist.

No feature flag by default. Optional env kill-switch only if streaming coalesce/gap proves unstable during implementation.

---

## 9. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| User bg clears in native scrollback | NBSP pad + trailing reset (same as diff bars); scrollback smoke |
| Default 5-line preview re-densifies | Hard cap + ellipsis; still far denser-friendly than full dumps |
| `•` assistant vs `•` tool confusion | Bold verb after tool bullet; one blank between cells |
| Live/replay spacing drift | Single gap API + golden spacing tests |
| Separator noise on short turns | Only when hadWork; plain `─` under 60s without long labels |

---

## 10. Decision log

| Decision | Choice |
|----------|--------|
| Approach | A — Codex rhythm pack (not custom multi-glyph B, not spacing-only C) |
| Primary markers | Keep `›`/`•`; add structure/outcome/rule glyphs |
| Explored tree | Single `└` nest (not sibling `├`/`└`) |
| Ran default output | ≤5 line preview (not fully collapsed) |
| Turn rule | Dim `─` when hadWork; Worked for only if >60s |
| Footer | Leave thin model·path row as-is |
| Banner tip | Remove |
| Architecture | Render-layer only |
| Acceptance | Checklist + unit tests; not pixel-perfect HTML |

---

## 11. Success criteria

1. Long sessions read as **sectioned rhythm**, not a wall of identical bullets.  
2. Manual checklist 1–10 pass on the fixed script.  
3. Focused `internal/cli` tests green.  
4. No agent/protocol changes.
