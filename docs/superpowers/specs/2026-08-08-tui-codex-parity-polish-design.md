# TUI Codex-Parity Polish — Diff Full-Line BG, Letter Selectors, Cool Composer Tint — Design

**Date:** 2026-08-08  
**Status:** Approved for implementation planning  
**Inputs:**
- User: non-fullscreen `+/-` diff backgrounds incomplete / not full-line; agent jump-out selection should use `a/b/c…` not `1/2/3…` with better selector chrome; all pickers; composer fill looks cement-gray on Ubuntu purple; target Codex-like feel
- Live: PTY captures of Corvus + Codex 0.147; purple OSC-11 tint samples
- Codex source (`openai/codex` `codex-rs/tui`): `diff_render.rs` (whole-line `line_bg`), `list_selection_view.rs` / `approval_overlay.rs` / `selection_popup_common.rs` (menu surface, `›` prefix, shortcuts)
- Corvus: `diffview.go` (`diffBar`), `chooser.go` (`rowLine`), approval in `chat_tui.go`, `theme.go` (`inputBoxTintFromBackground`)

---

## 1. Problem

| Area | Current | Desired (Codex-like) |
|------|---------|----------------------|
| Diff `+/-` rows | Background only from sign through padded code; leading indent/gutter unpainted; ASCII space pad erasable under cell-diff / non-fullscreen; width may use full `m.width` while transcript uses `width-1` | Entire row tinted; pad to content width with non-clearable blanks; trailing reset; width = transcript content width |
| Selectors | `rowLine` → `❯ N. label`; digit hotkeys; flat panel | `› a. label` style; letter labels; shared surface + selected-row emphasis; all pickers |
| Approval keys | Digits + semantic `y`/`a`/`p`/`n` | **Display** letters; **keep** semantic `y`/`a`/`p`/`n` (priority over letter-index for those keys) |
| Composer fill | `mix(bg, white, 0.22)` → cement purple-gray on Ubuntu purple; cool fallback `#353c4d` still clashes | Cool-surface blend (not pure white) + cooler fallback |

---

## 2. Goals and non-goals

### Goals

1. **Diff:** Full-line add/delete backgrounds that survive non-fullscreen and scrollback flush; width aligned with transcript.
2. **Selectors:** One shared row/panel renderer; labels `a`–`z`; all current `rowLine` call sites migrated.
3. **Approval:** UI letters + preserved `y`/`a`/`p`/`n` semantics with documented key priority.
4. **Composer:** Purple-friendly cool tint; better no-probe fallback.
5. Tests for painter width/reset, letter mapping, approval key priority, tint fixtures.

### Non-goals

- Slash `/` completion menu restyle (stays as today).
- Changing transcript history colors, accent palette, or theme style list beyond `inputBoxBG` math/fallback.
- Ratatui port or true alpha blending.
- More than 26 letter keys (rows beyond `z` have no letter hotkey; navigate with arrows).

---

## 3. Design

### 3.1 Diff full-line background (`diffview.go`)

**Target layout per `+/-` row (visible columns = `width`):**

```text
[bg][indent][gutter][space][sign][space][highlighted code][nbsp pad to width][ansiReset]
```

- Apply `bgSGR` from the **first cell** of the row (including the two-space indent and line-number gutter).
- Re-arm bg after every SGR reset in highlighted code (`reapplyBG` / equivalent), same as today for mid-line chroma resets.
- Right-pad with **`completionPadCell` (NBSP `\u00a0`)** repeated until visible width == `width` (not ASCII space).
- End every painted row with **`ansiReset`**.
- Delete rows: keep dim overlay on content; bg still full-line.
- Context rows: **no** add/del background (Codex context has no tint).

**Width source:**

- All `diffBlock` / `diffBody` / `diffBar` call sites that currently pass `m.width` must pass **`transcriptContentWidth(m.width, m.nativeScrollback)`** so alt-screen scrollbar column is not double-painted and native scrollback uses full width.

**Tests (`diffview_test.go`):**

- Stripped visible width == requested width for short and long code.
- Line contains open bg before gutter content; ends with reset.
- NBSP present in pad path; no reliance on trailing ASCII spaces alone.
- Width helper used in the chat dispatch path (unit or integration assert on the width argument if practical).

### 3.2 Unified selection rows (`selection_row.go` + call sites)

**API (names illustrative; keep package-local):**

```go
func selectionLetter(i int) (rune, bool) // 'a'+i for i in 0..25; else false
func selectionRow(selected bool, index int, box, label string, active bool) string
func selectionPanel(body string, width int) string
```

Replace `rowLine` with `selectionRow`. Deprecate/remove `rowLine` after migration.

**Visual contract:**

| State | Prefix | Letter | Label |
|-------|--------|--------|-------|
| Selected | accent `› ` | accent + bold `a.` | bold label |
| Idle | `"  "` | dim `b.` | dim label |
| Active flag (e.g. current model) | same as idle prefix | dim letter | subtle/warn label (not selected) |

Optional selected-row wash: if cheap and reliable, a single soft `bgSGR` on the selected line only (must end with reset; pad with NBSP to panel content width). If tests or terminals fight it, ship without wash first — **letter + › + bold is mandatory; wash is preferred.**

**Panel (`selectionPanel`):**

- Reuse/evolve `choicePanelStyle`: top border in accent/border, left padding 1, width clamped.
- Footer hint line (dim): `↑/↓ · a–z · Enter · Esc` for generic lists; approval adds `· y/a/p/n`.

**Letter mapping:**

- Index `i` → letter `'a'+i` for `i ∈ [0,25]`.
- Index `>25`: no letter in UI; prefix spaces only; arrows + Enter only.

**Key handling:**

| Context | Keys |
|---------|------|
| Generic pickers (quick, copy, clear, mcp, skill, resume, rewind, chooser options) | `a`–`z` activate index; optional silent `1`–`9` index compat; ↑↓ Enter Esc |
| Approval | **First** match semantic: `y` allow-first, `a` allow-session, `p` persist-to-config, `n`/`Esc` deny (existing meaning). **Then** other letters `b`–`z` as index. **Then** `1`–`9` index. ↑↓ Enter unchanged. Document: display letter on row 0 is `a.` but key `a` is **session allow**, not “first row” — secondary hint column or footer must show `y`/`a`/`p`/`n` next to the matching rows where practical |

**Approval row hints (recommended):** when rendering approval choices, append dim shortcut tags for the semantic binding of that choice (e.g. first allow → `y`, session → `a`, persist → `p`, deny → `n`) so display `a.` vs key `a` is disambiguated.

**Call sites to migrate (all current `rowLine` users):**

- `chat_tui.go` — approval banner  
- `chooser.go` — options, type-something, chat-instead  
- `quick_picker.go`  
- `copy_picker.go`  
- `clear_confirm.go`  
- `mcp_manager_view.go`  
- `skill_picker_view.go`  
- `resume_picker.go`  
- `rewind.go` — both turn list and scope rows use letter index (turn **label** still shows turn content; not the old `meta.Turn+1` as the visible index)

**Out of scope:** `renderCompletion` slash/`@` menus.

### 3.3 Cool composer tint (`theme.go`)

**Probed dark:**  
`final = mixHex(bg, "#8b95a8", inputBoxDarkLift)` with `inputBoxDarkLift = 0.28` (cool gray-blue target; keeps purple hue softer than white lift).

**Probed light:**  
`final = mixHex(bg, "#5a6470", inputBoxLightSink)` with `inputBoxLightSink = 0.14`.

**Fallback dark:** `cliColor{"#2b3344", 237}` (hand-picked xterm; **not** Convert256 on the hex if it collapses badly — pin 237).  
**Fallback light:** unchanged `#eceff4`/255.

**Rationale:** Pure white lift on `#300a24` yields ~`#5e4054` cement; cool target stays elevated without desaturating into mud.

**Tests:** update pure-function fixtures for purple `(48,10,36)`, black, light samples; fallback pins; probe override path.

### 3.4 Rendering / key flow summary

```text
Diff:  FileDiff event → transcriptContentWidth → diffBlock → full-line diffBar (NBSP + reset)
Select: keys → (approval semantic first) → letter/digit index → existing answer handlers
Select UI: selectionPanel(selectionRow…) under composer (existing bottom panel order)
Composer: buildCLITheme → cool mix or fallback → renderComposerField (existing painter)
```

---

## 4. Testing matrix

| Area | Cases |
|------|--------|
| Diff | Full width; trailing reset; NBSP pad; re-arm after chroma reset; dim delete; contentWidth wiring |
| selectionLetter | 0→a, 25→z, 26→none |
| selectionRow | selected/idle/active SGR shape smoke (strip + contains › / letter) |
| Approval keys | `a` still session-allow when that choice exists; `y` first allow; `b` selects index 1 if present; `n` deny |
| Picker keys | one representative (e.g. clear confirm): `a`/`b` |
| Tint | purple probe not near old white-lift cement; fallback `#2b3344`/237 |

Commands:

```text
go test ./internal/cli/ -run 'Diff|Selection|Approval|Chooser|Tint|InputBox|ComposerField' -count=1
```

Optional manual: non-maximized terminal + tool edit diff; Ubuntu purple theme + empty composer; approval prompt letters + y/a/n.

---

## 5. Risks

| Risk | Mitigation |
|------|------------|
| Approval `a` vs display `a.` confusion | Footer + per-row semantic hints; document in hint string |
| Full-line diff bg too loud | Keep existing add/del palette; only expand coverage |
| Cool mix too blue on pure black | Fixture pins; fallback curated |
| >26 items | No letter; arrows only (explicit non-goal for aa/ab) |

---

## 6. Success criteria

1. Non-fullscreen / scrollback: `+/-` bars read as continuous full-width bands, not truncated mid-line.  
2. All former `rowLine` UIs show `a.`/`b.`…; approval still accepts `y`/`a`/`p`/`n` with prior semantics.  
3. On Ubuntu purple (or probe `48,10,36`), composer is a cool elevated field, not cement gray.  
4. Focused tests green; no slash-completion regression required beyond compile.

---

## 7. Implementation order (for plan)

1. Diff full-line painter + width wiring + tests  
2. `selectionRow` / `selectionPanel` + migrate all call sites + approval key priority + tests  
3. Cool tint ratios/fallback + tests  
4. Manual smoke checklist  
