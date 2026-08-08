# TUI Density & Hierarchy (Codex-First Render Pack) — Design

**Date:** 2026-08-08  
**Status:** Approved for implementation planning  
**Approach:** Render-layer density pack (no agent/HistoryCell rewrite)  
**Identity:** Codex-first markers and chrome; Corvus product name only in session banner  

**Inputs:**
- Live PTY captures: Corvus (`bin/corvus --yolo`) and Codex CLI 0.147 on the same `ls` workload
- Codex source (`/tmp/codex-src/codex-rs/tui`): `history_cell`, `exec_cell`, `status_indicator_widget`, `styles.md`, chatwidget snapshots
- Corvus: `toolcard.go`, `transcript.go`, `chat_tui.go` (reasoning stream/collapse), `status_footer.go`
- Brainstorm decisions: full density pass; Codex-first; acceptance via static HTML ↔ real TUI

**Mockups (acceptance contracts):**
| File | Role |
|------|------|
| `docs/tui-density-target.html` | **Target** — implement against this |
| `docs/tui-compare-corvus.html` | Before (current Corvus density) |
| `docs/tui-compare-codex.html` | Reference (Codex density) |

---

## 1. Problem

Recent Codex-parity work (diff full-line bg, letter selectors, composer tint) improved chrome details but left **transcript information density** and **visual hierarchy** heavier than Codex:

| Area | Current Corvus | Desired (Codex-like) |
|------|----------------|----------------------|
| Tools | One `● Verb` card per call; multi-color dots; `⎿` gutters | Coalesced `• Explored` tree for consecutive reads; `• Ran` / `• Edited` for write/exec |
| Thinking | Live `▎ thinking…` + body block; vertical noise | Ambient one-line working/thinking; default no body in transcript |
| Assistant | Live `◆ Corvus` nameplate + per-turn receipts | Plain prose; no nameplate; no per-turn cache receipt |
| Footer | Multi-field Model/Effort/Work/CTX/path bands | Single thin row; progressive collapse |
| Markers | Mixed `●` / `◆` / `▎` / `❯` | Unified `›` (user) + `•` (agent/tool/status) |

Users experience long sessions as “busy” rather than “dense but clear.”

---

## 2. Goals and non-goals

### Goals

1. **Unified marker system** — user `› `; agent/tool/reasoning/working `• `; session banner may keep `◆ corvus · model`.
2. **Tool density** — consecutive read-category tools coalesce into one `• Explored` cell with a tree (max ~5 leaves, then `+N more`).
3. **Thinking** — default one-line ambient status while live; no thinking body wall; collapse when turn ends (verbose keeps full text).
4. **Assistant body** — no per-answer `◆ Corvus` name; default foreground prose; history slightly demoted.
5. **Footer** — single row: interaction state + model + path; Effort/Work/CTX/cache% not permanent (available via `/status` or custom statusline).
6. **Composer** — ~2-row default height; keep cool tint from prior work.
7. **Color discipline (Codex palette, not monochrome)**  
   - Follow Codex `styles.md` + exec rendering: default fg body; **cyan** for tips/status/tree verbs (`Search`/`Read`/`List`); **green** success bullets & outcomes; **red** failures; **magenta** sparingly (e.g. shell `$`/accents); bash/commands use **syntax highlight** (syntect-style).  
   - Drop multi-color category `●` dots (read=cyan card / write=green card / exec=yellow card as competing systems). Unified `•` with **semantic** green/red for outcome, not tool-category rainbow.  

8. **Acceptance** — static target HTML reviewed first; ship only when real TUI matches HTML hierarchy on the fixed script.
9. **Tests** — pure render helpers + updated `toolcard` / `transcript` / `status_footer` / chat render tests.

### Non-goals

- Changing agent runtime, tool protocol, permissions, or providers.
- Porting to Ratatui or introducing a full HistoryCell graph model.
- Sidebars, multi-pane layouts, user keymap files.
- Re-doing completed parity items (diff full-line bg, letter selectors) except where marker style touches them.
- Pixel-perfect match to Codex or to the HTML (hierarchy and density are the bar).
- Removing custom `statusline` command support.

---

## 3. Design principles

1. **Clarity over decoration** — transcript brightest; chrome quietest.
2. **Progressive disclosure** — default answers “what happened?”; details on expand (Ctrl+B) or verbose.
3. **Render-layer only** — coalesce and restyle at TUI ingest/render; do not invent a second tool event stream.
4. **One marker language** — if a glyph is not `›` or `•` (or banner `◆`), justify it.
5. **HTML is the contract** — `docs/tui-density-target.html` is the visual acceptance source of truth for this pass.

---

## 4. Marker and cell contracts

### 4.1 Markers

| Role | Glyph | Notes |
|------|-------|--------|
| User message | `› ` | Current turn full strength; history dim/faded |
| Agent/tool/status | `• ` | Dim bullet + bold verb where needed |
| Session banner | `◆ corvus · <model>` | Top of empty/new session only |
| Working (live) | `• Working (Ns · esc…)` or equivalent | Ambient line above composer, not a transcript wall |
| Tree edges | `└` / `│` + indent | Prefer over default `⎿` for collapsed tool trees |

### 4.2 Tool cells

**Explored coalesce set** (`toolCategory == "read"`, excluding process readbacks):

- Include: `read_file`, `ls`, `glob`, `grep`, `web_fetch`, `web_search`
- Exclude from coalesce: `bash`, `bash_output`, all write tools, `wait`, `kill_shell`, MCP/`use_capability`, `task`, etc.

**Coalesce rules:**

1. Within a single assistant turn, consecutive tools from the include set merge into one open Explored cell.
2. Any non-include tool, user message, or turn boundary flushes the open cell.
3. Live updates may show a growing tree; on flush, render final tree.
4. Tree shows up to 5 leaf lines; remainder as dim `+N more`.
5. Leaf labels reuse existing verbs/args (Read path, Search pattern, List path, …).

**Independent cells:**

```text
• Ran <highlighted bash command>
  └ <short output / ok · duration>

• Edited path (+n −m)
  └ optional one-line summary; full diff remains existing diff renderer
```

**Removed defaults:** multi-color `●` category dots; per-tool Claude-style `● Verb(arg)` as the primary density unit for reads.

### 4.3 Thinking / reasoning

| Mode | Live | After turn |
|------|------|------------|
| Default | Ambient one-liner only; full text buffered off-transcript | Remove live marker; no body left in transcript (align with existing collapse) |
| Verbose (`Ctrl+O` / `/verbose`) | May show dim body | Keep latest-turn full text (existing “only latest” rule) |

Optional: if a short model-provided reasoning summary exists, one dim italic `• <summary>` line is allowed; not required for v1 if current pipeline has no summary channel.

### 4.4 Assistant markdown

- Drop `named` live prefix `◆ Corvus ` from `renderAssistantMarkdown` / `assistantBlock`.
- Body uses default fg; markdown headers bold.
- History demotion: muted/dim text and/or 2-space gutter — no required bare diamond column.
- Remove per-answer cache/elapsed receipt under each reply from the default transcript path.

### 4.5 Footer and composer

**Default single footer row (wide):**

```text
  <interaction>                    <model> · <path>
```

Examples:

```text
  ready                            deepseek-v4-flash · ~/dv_project/corvus
  YOLO · ready                     deepseek-v4-flash · ~/dv_project/corvus
  tool approval                    deepseek-v4-flash · ~/dv_project/corvus
```

**Not permanent on the default row:** Effort, Work profile, CTX%, compact headroom, session cache hit %.

**Progressive collapse (narrow width), drop order:**

1. Shorten path (`~/…`)
2. Middle-compact model
3. Interaction-only line

**Custom statusline:** if configured and non-empty, continues to replace the data band (existing contract).

**Composer:** default visual height ~2 rows (grow with input); keep cool-surface tint; prompt glyph may be `›` or existing `❯` if one character is required for input-widget compatibility — prefer `›` when free.

---

## 5. Architecture (render pack)

```text
agent events (unchanged)
        │
        ▼
chat_tui ingest
  · open Explored coalesce buffer for consecutive reads
  · reasoning → ambient status + optional verbose buffer
        │
        ▼
render helpers
  toolcard.go      → Explored / Ran / Edited strings
  transcript.go    → assistant without nameplate
  status_footer.go → single-line progressive layout
  theme (minimal)  → drop category-dot emphasis if unused
        │
        ▼
existing transcript viewport / native scrollback
```

**Primary files:** `internal/cli/toolcard.go`, `transcript.go`, `chat_tui.go`, `status_footer.go`, related `*_test.go`.  
**Out of scope files:** `internal/agent/*`, `internal/tool/*`, provider packages.

**Coalesce implementation sketch (illustrative):**

- State on `chatTUI`: open explored buffer (tool ids, display leaves) or nil.
- On tool start/end: if coalesce-eligible and buffer open → append leaf and re-render one transcript block; else flush and start new cell type.
- Flush on non-read tool, user submit, turn complete, interrupt.

No change to tool JSON or agent loop.

---

## 6. Acceptance

### 6.1 Process

1. Design mockups reviewed (this spec + HTML) — **done when this doc is approved**.
2. Implementation lands against `docs/tui-density-target.html`.
3. **Ship gate:** run the fixed 4-turn script (or equivalent PTY/fixture replay) and compare real TUI to the target HTML using the checklist below. Hierarchy match required; pixel match not required.

### 6.2 Fixed script (same as mockups)

1. Ask why status footer wraps on medium width; read-only diagnosis.  
2. Request quieter second row / layout chrome tweak.  
3. Request coalescing consecutive reads into Explored.  
4. Run focused tests and summarize.

### 6.3 Checklist (HTML ↔ TUI)

- [ ] User lines use `›`; tools/status use dim `•` (no multi-color `●`).
- [ ] Consecutive reads/searches → one `• Explored` tree, not N cards.
- [ ] Bash/write → separate `• Ran` / `• Edited`.
- [ ] No `▎ thinking…` body wall in default mode.
- [ ] No per-answer `◆ Corvus` nameplate; no per-turn cache receipt under answers.
- [ ] Session banner may show `◆ corvus · model` only at top.
- [ ] Footer is one thin row: interaction + model + path.
- [ ] Effort/Work/CTX not permanent on default footer.
- [ ] Composer ~2-row default with cool tint acceptable.
- [ ] Vertical density closer to target HTML than to `tui-compare-corvus.html`.
- [ ] Unit tests for coalesce, assistant identity, footer single-line, reasoning default collapse.

### 6.4 Automated tests (minimum)

| Area | Coverage |
|------|----------|
| Coalesce | 3–4 consecutive reads → one block; write/bash breaks merge |
| Markers | Explored/Ran/Edited strings contain `•`, not category `●` |
| Assistant | Named live block has no `Corvus` nameplate |
| Reasoning | Default collapse leaves no body; verbose keeps text |
| Footer | Single-line layout fixtures; Effort/Work absent from default row |

---

## 7. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Live tool streaming indexes break when merging cards | Keep one transcript index per open Explored cell; update in place; mirror existing `toolCardIdx` patterns |
| Users miss per-tool color scanning | Tree verbs + path still scannable; success/fail colors remain on outcomes |
| Footer loses useful CTX warning | Surface CTX urgency on `/status` and/or only when pct ≥ warn threshold as a temporary left-side notice (optional follow-up; not required for v1 if checklist passes without it) |
| Verbose users regress | Preserve Ctrl+O / `/verbose` paths with tests |
| HTML accepted but TUI feels off at other widths | Add footer collapse unit tests at 60/80/110 cols |

---

## 8. Rollout

1. Implementation plan via writing-plans (task breakdown, TDD where practical).  
2. Single vertical slice preferred (markers + Explored + thinking ambient + footer) so intermediate UI is not half-Codex.  
3. Manual acceptance: open target HTML beside real TUI on the fixed script.  
4. No feature flag required if tests + HTML checklist pass; optional env kill-switch only if streaming coalesce proves unstable during implementation.

---

## 9. Decision log

| Decision | Choice |
|----------|--------|
| Scope | Full density pass (tools + thinking + footer) |
| Identity | Codex-first (A) |
| Architecture | Render-layer pack (A), not HistoryCell rewrite |
| Acceptance | Static HTML target first; real TUI compared to HTML |
| Explored set | read-category tools minus `bash_output` |
| Nameplate | Remove from answers; banner only |
| Footer metrics | Model + path permanent; Effort/Work/CTX off default row |
