# Reasonix TUI Clarity & Keyboard-First Design

**Date:** 2026-08-06  
**Status:** Design approved for P1 planning; P1 implementation plan written  
**Branch context:** Design only; does not depend on prompt-cache Phase 1 work  
**P1 plan:** `docs/superpowers/plans/2026-08-06-tui-clarity-keyboard-p1.md`  
**Inputs:**  
- Brainstorming session: optimize TUI aesthetics, fluidity, and interaction logic  
- Competitive research: Grok Build, Claude Code, Codex CLI, OpenCode (primary peers); Crush (same Charm stack reference)  
- Product preference: Claude Code clarity + full keyboard reachability; multi-agent visibility without Grok-style click paths  
- Current codebase: Bubble Tea v2 TUI in `internal/cli` (`chat_tui.go` ~4.6k lines is the main surface)  
- Design review (plan subagent): key conflicts, footer inventory, mode taxonomy, Tasks Attach feasibility, statusline migration

---

## 1. Problem

Reasonix already has a capable Bubble Tea TUI: themes, tool cards, status footer, slash commands, pickers, plan/YOLO modes, custom statusline command, mouse optional. Gaps versus the best coding-agent TUIs:

| Gap | User impact |
|-----|-------------|
| Visual hierarchy is uneven | Feels busier / less “clear” than Claude Code |
| Footer can carry too many fields | Crowds the eye; narrow terminals wrap noisily |
| Discoverability is slash-list heavy | Harder to learn keys than Claude `?` / OpenCode palette |
| Multi-agent / background work has no first-class keyboard view | Users cannot comfortably inspect other agents without ad-hoc or click-oriented UX (Grok pain) |
| `chatTUI` is a god-model | Hard to add overlays/focus rules without regressions |

**Not the problem:** missing agent runtime power (permissions, evidence, checkpoint). The product is strong under the hood; the TUI must **present and navigate** that power with Claude-level clarity and keyboard completeness.

---

## 2. Goals and non-goals

### Goals

1. **Claude-level visual clarity** on the main chat shell: progressive disclosure, thin chrome, strong hierarchy.  
2. **Full keyboard reachability** for every primary workflow (chat, approve, mode, status, tasks/subagents, help). Mouse may enhance; it must never be required.  
3. **Multi-agent visibility** via a lightweight, keyboard-first Tasks overlay (Peek + optional Attach).  
4. **Discoverability**: empty-input `?` cheatsheet + `Ctrl+P` command palette (slash remains).  
5. **Phased delivery**: each phase independently mergeable and user-visible.  
6. **Bounded architecture change**: extract overlay stack / key table / status rendering as features need them—no rewrite-for-rewrite’s-sake.

### Non-goals (this program, P0–P4)

1. Persistent sidebar or fixed dual-pane IDE layout.  
2. Full Grok-style Agent Dashboard (pin/dispatch/roster product surface).  
3. Primary paths that only work via hover/click.  
4. User-configurable keymap file (table-driven built-ins only; leave a seam for later).  
5. Replacing Bubble Tea / adopting Ultraviolet-style full screen buffer rewrite.  
6. New default skin or user JSON theme system (keep existing themes; improve hierarchy).  
7. Making Claude Code statusline JSON the sole contract (Reasonix owns schema; optional shim later).  
8. Grok-style dual-focus + vim scrollback as the default navigation model.  
9. Big-bang split of all of `chat_tui.go` without feature delivery.

---

## 3. Design principles (hard rules)

1. **Clarity over decoration**  
   Main transcript is brightest. Chrome (footer, badges, borders) is quieter. Prefer fewer simultaneous colors.

2. **Keyboard is complete; mouse is optional**  
   Every action in palette/slash/tasks must be reachable with keys. Click targets are accelerators only.

3. **Linear shell, modal power**  
   Default layout is Claude-like linear chat. Power features (tasks, pickers, help, palette) are **overlays** with a strict Esc stack—not permanent panes.

4. **Progressive disclosure**  
   Default view answers “what happened / what should I do next?” Details (tool stdout, long reasoning) expand on demand or when failed/approval-critical.

5. **One focus model for the main shell**  
   Default focus stays on the composer. Transcript scrolls with PgUp/PgDn (and wheel). Do not require a focus mode switch for ordinary reading.

6. **Esc is a stack, not a grab-bag**  
   Documented priority (top wins). No silent mode flips on Esc.

7. **Phased vertical slices**  
   Each phase ships a complete user-visible improvement under the same principles.

8. **Reuse runtime truth**  
   TUI reads Controller/Agent state; it does not invent a second source of truth for tasks, modes, or context usage.

---

## 4. Competitive anchors (what we copy / reject)

| Peer | Copy | Reject |
|------|------|--------|
| **Claude Code** | Clarity, thin footer, progressive tool collapse, **empty-input `?`**, statusline *script idea*, permission/mode glanceability | Model lock-in; Claude JSON field names as ours; **do not claim Claude has a Ctrl+P command palette** (Claude uses `/` + `?`; Ctrl+P/N is history/completion-style nav) |
| **Grok Build** | Multi-agent *need*, tasks pane concept, rich shortcuts docs; Ctrl+G → tasks is a reasonable chord to reuse | Click/hover-primary paths, dense multi-pane chrome, dual-focus complexity as default, full dashboard |
| **OpenCode** | **Command palette** (Ctrl+P-style action list) + leader discoverability, optional info density | Sidebar-first layout as default |
| **Codex CLI** | Clean status, sandbox/mode as first-class, keymap direction (later), `?`-style help | Under-investing in multi-agent UI |
| **Crush** (stack ref) | Lazy list / overlay discipline on Bubble Tea | Full layout rewrite |

**Product one-liner:** *Claude clarity and keyboard completeness; OpenCode-style palette discoverability; Grok multi-agent *inspection* as a linear-shell overlay (Peek first).*

**Chord semantics note:** Reasonix **Ctrl+B** = expand shell/tool body (existing). Claude Code often uses Ctrl+B for backgrounding. Cheatsheet must document Reasonix meaning—not Claude parity for every chord.

---

## 5. Information architecture

### 5.1 Main shell (always)

```
┌──────────────────────────────────────────────────┐
│ Transcript                                       │
│  user / assistant / thinking summary / tools     │
├──────────────────────────────────────────────────┤
│ Status interaction row (hints / transient state) │
│ Status data row (optional): context · model · ⚙  │
├──────────────────────────────────────────────────┤
│ [Plan]  composer                                 │
└──────────────────────────────────────────────────┘
```

No persistent sidebar. **At most two** built-in status bands (interaction + optional data). A third persistent band is forbidden. Mode lives on the **composer badge**, not as long footer prose (see §5.3–5.4).

### 5.2 Overlays (modal)

| Overlay | Opens via | Closes via |
|---------|-----------|------------|
| Slash / completion menus | `/`, `@`, Tab flows | Esc, selection |
| `?` cheatsheet | Empty composer `?` | Esc |
| Command palette | `Ctrl+P` | Esc |
| Tasks list | `Ctrl+G`, `/tasks`, palette | Esc |
| Task Peek | Enter on task row | Esc → list |
| Task Peek / list | See §9 | Esc one level |
| Task Attach | **P2.1 only** if runtime supports (see §9.3) | Esc → list (preferred) |
| Existing pickers | model, resume, skill, MCP, … | Esc |
| Approvals / questions | runtime events | existing keys + Esc rules |

### 5.3 Status placement rules (P1 chrome inventory)

Today’s footer is already multi-row (interaction + git/telemetry). P1 does **not** mean “delete the data band blindly”; it means a **lean, explicit inventory**.

| Band | Contents (default, no custom statusline) | P1 action |
|------|------------------------------------------|-----------|
| **Interaction row** | Contextual UI state (rewind/picker/approval/running cancel hints), short action hints (`? help`, `esc interrupt`), mouse-off tag when relevant | **Keep**, tighten copy; **remove long mode cycle prose** once composer badge exists |
| **Data row** (optional second band) | **context%** (or used/window), **model** (compact-middle if needed), **jobs count** (`⚙ N` / tasks running) when &gt; 0; effort/work profile if space | **Keep** as the quiet “glance metrics” band |
| **Not on default chrome** | balance, cache diagnostics detail, full git porcelain, cost | **Move to `/status`** (and custom statusline). P1 may stop fetching/showing git/balance on the default data row to reduce noise—preserve packing helpers for narrow widths |
| **Mode** | Shown on **composer badge only** (§5.4) | **Remove** mode pill/long labels from interaction row to avoid double display |

**Narrow terminals:** keep existing packing discipline (`packStatusGroups`, hide hints when key names cannot fit). Do not replace with naive truncation that drops all metrics.

**Custom statusline:** when `[statusline].command` is set:

- Script stdout replaces the **data band** (same idea as today: telemetry/data fields yield to the script).  
- **Interaction row** still shows transient state + short hints when space allows.  
- Composer **mode badge** remains independent of the script.

### 5.4 Mode badge

- Left of composer: short text badge with semantic colors (Title Case to match existing tags unless a later polish pass standardizes casing).  
- **No border-color-only encoding** (badge text is the accessibility baseline; 256-color fallbacks via existing theme).  
- Mode axes stay product-true: **Shift+Tab** cycles Ask → Auto → Plan (existing); **Ctrl+Y** toggles YOLO orthogonally; Goal / Don't Ask / Shell remain real states.

**Badge taxonomy (must render these, not a 4-value fantasy enum):**

| Runtime state | Badge text (canonical) | Notes |
|---------------|------------------------|-------|
| Ask | `Ask` | Default tool approval ask |
| Auto | `Auto` | Auto-approve tools |
| Plan | `Plan` | Plan-first |
| YOLO | `YOLO` | Explicit YOLO axis |
| Plan + YOLO | `Plan+YOLO` | Compound |
| Don't Ask | `Don't Ask` | Existing desktop/special layout label when active |
| Goal (and compounds) | `Goal` / `Goal+…` | Match `modeTagText()` compounds where present |
| Shell (`!` prefix) | `Shell` | Input mode, not approval mode |
| Other compounds already emitted by `modeTagText()` | Keep parity | Implementation plan maps 1:1 from current helper |

**Footer:** does not repeat mode badge text. Hints may still say `shift-tab` without naming the current mode.

---

## 6. Progressive disclosure (transcript)

| Block | Default | Auto-expand | Keyboard (P1+) |
|-------|---------|-------------|----------------|
| Reasoning / thinking | One-line summary (`thought Ns`) | Configurable verbose (`Ctrl+O` / existing) | Expand via existing verbose paths; optional last-block expand in plan if needed |
| Tool header | Visible (name + short arg summary) | — | — |
| Tool body / stdout | Collapsed | On failure; when approval needs the body | `Ctrl+B` for shell-style expand (existing); generalize pattern in plan |
| Diffs | Prefer compact summary + expand | On approval for mutating tools when needed | Expand with same block pattern |
| Turn receipt | One quiet line | — | Detail in `/status` history if needed |

Failed tools and pending approvals must never hide the critical detail behind an undiscoverable click.

---

## 7. Focus and Esc

### 7.1 Main-shell focus (chosen: Claude simple)

- Default focus: **composer**.  
- Scroll transcript without stealing focus: `PgUp` / `PgDn` / wheel (and existing home/end bindings where present).  
- No default dual-focus (scrollback mode with `j/k` block selection).  
- Out of scope for this program: vim scrollback mode as default.

### 7.2 Esc stack (top wins)

1. Close autocomplete / slash menu / completion dropdown.  
2. Close `?` cheatsheet or command palette.  
3. Close Tasks Peek → Tasks list → dismiss Tasks (one level per Esc).  
4. Close other overlays (pickers, approvals UI per existing semantics—document any special cases).  
5. If turn running: cancel turn (preserve draft when that is current behavior; do not regress).  
6. Idle: double-Esc patterns for clear draft / rewind. **Match current timings unless fixing a bug:** double-Esc rewind window **600ms**; double-Ctrl+C quit window **1500ms** (as implemented today).

**Rule:** Esc never silently changes Plan/Ask/YOLO mode.

**P1 implementation note:** keep the existing modal if-chain if Esc tests stay maintainable. Introduce a formal `overlayStack` when Tasks (P2) lands or when Esc priority tests become unmaintainable—whichever comes first. Do not block P1 on a full stack rewrite.

---

## 8. Discoverability

### 8.1 Empty-input `?` — cheatsheet

- Opens a scrollable, keyboard-only overlay.  
- **Empty definition:** `strings.TrimSpace(composer text) == ""` (and no image-only draft if that counts as non-empty in current paste model).  
- If composer is **non-empty**, typing `?` inserts the character (does not open cheatsheet).  
- Shows: current-context keybindings + top commands.  
- Not a free-text shell.  
- **`/help`:** remains available; P1 may keep transcript dump or point users at `?`. Avoid permanent dual systems—P3 should decide one canonical long-help surface.  
- Search within cheatsheet is nice-to-have in P3; P1 can be static sections.  
- Key chords in the cheatsheet stay literal English (`Ctrl+P`); section titles follow i18n if other chrome is localized.

### 8.2 Command palette (OpenCode-style; not Claude parity)

- Fuzzy filter over **actions** (P3: unified with slash/command registry).  
- Enter runs action; Esc closes.  
- **Prefer reusing `quick_picker.go`** (fuzzy list, Esc cancel, Ctrl+P/N item nav) rather than a parallel widget.  
- Palette does **not** open while a higher modal is active (completion menu, chooser, approval, rewind, existing pickers, tasks)—modal stack wins.

#### 8.2.1 `Ctrl+P` conflict resolution (blocking decision — locked)

**Code today:** `Ctrl+P` / `Ctrl+N` move selection in completion menus and quick pickers (emacs previous/next).

**Decision (option A):**

| Context | `Ctrl+P` / `Ctrl+N` |
|---------|---------------------|
| Completion menu, quick picker, or any list modal that already binds them | **Previous / next item** (unchanged) |
| Main shell idle (no such modal) | **`Ctrl+P` opens command palette**; `Ctrl+N` unbound or reserved (do not steal unless needed) |
| Turn running / approval open | Palette **does not** open; existing interrupt/approval keys win |

Do **not** steal `Ctrl+P` globally for palette (options B/C rejected for P1).

#### 8.2.2 P1 palette action list (curated skeleton)

Explicit set (implementers must not invent an unbounded catalog):

1. Help / cheatsheet (`?`)  
2. Status details (`/status`)  
3. Model picker  
4. Resume / sessions  
5. Toggle verbose reasoning  
6. Toggle mouse capture (if exposed today)  
7. MCP manager (if exposed)  
8. Skills (if exposed)  
9. Compact (if exposed)  
10. Clear / new session flows already available via slash  
11. **Tasks** — only after P2 ships; omit or gray “coming” is forbidden—either wire real `/tasks` or leave out  

P3 expands this list by generating from the shared registry.

### 8.3 `/` slash

- Remains the power-user path.  
- Must not be the *only* way to discover status after P1; tasks after P2.

---

## 9. Tasks overlay (P2)

### 9.1 Purpose

Let the user **see and navigate** background work / subagent-shaped jobs with keyboard only—addressing the “I need to click to inspect other agents” failure mode of denser multi-pane UIs.

### 9.2 Open / navigate

| Key / command | Action |
|---------------|--------|
| `Ctrl+G` | Toggle or open Tasks list (free in current TUI; peers use this chord differently—document in cheatsheet) |
| `/tasks` | Same |
| Palette “Tasks” | Same (P2+) |
| `↑` `↓` | Move selection |
| `Enter` | **Peek** selected row |
| `Esc` | Back one level |
| Cancel/stop | When runtime supports; keyboard via palette action and/or a documented chord in the P2 plan (avoid silent Ctrl+X conflicts with terminals) |

### 9.3 Peek vs Attach (scoped)

| Mode | Phase | Composer | Content | Use |
|------|-------|----------|---------|-----|
| **Peek** (default Enter) | **P2 MVP** | Does not take over main composer | Read-only: identity, state, recent activity / job log tail | Quick inspection |
| **Attach** (`a`) | **P2.1 gated** | Only if runtime can route input to that work item | Full interactive chrome + header identity | Deep collaboration |

**Attach is not promised for P2 ship.** Full “input goes to child agent session” is Grok-shaped and is **not** proven against today’s `Controller.Jobs()` / tool-subagent surfaces (running job views: id, kind, label, status, startedAt).

**P2.1 gate (all required):**

1. Written inventory of APIs for interactive child I/O or session switch.  
2. Explicit Attach semantics chosen:  
   - **Soft attach:** focus job log + optional queue text into a defined parent/job channel, **or**  
   - **Session switch:** real session identity + detach + draft ownership.  
3. Success criteria updated if Attach ships.

Until then, multi-agent success = **keyboard list + Peek + cancel/stop when available**, not Attach.

**If Attach later ships:** header must show `agent · <name-or-id> · <state>`; Esc returns to **Tasks list** (not silent parent); parent composer draft preserved.

### 9.4 List row content (minimum)

- Identity (name/id/label/kind)  
- State mapped from **runtime truth** (e.g. jobs status). Do **not** invent `needs_input` unless an API exposes it; map approval-waiting parent state only when real.  
- One-line recent activity when available  
- Optional: duration  

### 9.5 Data source (P2 MVP)

- **Primary:** `Controller.Jobs()` / `jobs.View` (and any already-exported job buffers for log tails).  
- **Stretch in same phase only if cheap:** in-flight tool subagent markers already visible in parent transcript—link from list, do not require new control plane.  
- One TUI list DTO even if backend types differ.  
- Empty state copy when nothing is running.  
- P2 plan starts with a short **runtime inventory** section before UI coding.

### 9.6 Draft safety

- Opening/closing Tasks must not destroy the parent composer draft.

---

## 10. Statusline schema (P3; design now)

Extend the existing `[statusline].command` JSON stdin contract. Keep **Reasonix-owned** field names with `schema_version`.

### 10.1 Required direction (fields)

**Migration policy (blocking decision — locked):** schema v1 is an **additive dual-write**, not a silent rename. For at least one release (and until a later breaking version), emit **both**:

- **Legacy flat keys** (keep working): `model`, `contextUsed`, `contextWindow`, `cwd`  
- **v1 additions:** `schema_version`, `mode`, nested `context`, `git`, `session_id`, `tasks`, optional `cost`

```json
{
  "schema_version": 1,
  "model": "string",
  "contextUsed": 0,
  "contextWindow": 0,
  "cwd": "string",
  "mode": "ask|auto|plan|yolo|…",
  "context": { "used": 0, "window": 0, "ratio": 0.0 },
  "git": { "branch": "string" },
  "session_id": "string",
  "tasks": { "running": 0 },
  "cost": null
}
```

- `mode` string should mirror badge-facing mode when practical (including compounds if useful to scripts).  
- `cost` optional/nullable when pricing unknown.  
- `tasks.needs_input` only if runtime exposes it—omit rather than fake.  
- Never remove legacy flat keys without a new `schema_version` and release note.  
- stdout: first line only (existing); multi-line out of scope unless revisited.  
- Timeout and fail-open to empty remain (existing). Fail-open ≠ “old scripts keep working”—**dual-write** is what keeps old scripts working.

### 10.2 Non-goal

Byte-for-byte Claude Code statusline JSON compatibility. A future adapter script may map Reasonix → Claude-like shapes.

---

## 11. Theming (P1 hierarchy, not new skin)

- Keep current dark/light + style accents (graphite, etc.).  
- P1 work:  
  - Raise body text contrast vs chrome  
  - Soften tool card borders  
  - Clearer user vs assistant separation  
  - Mode badge semantic colors with 256-color fallbacks (existing theme machinery)  
- No new default theme name required for success.

---

## 12. Phased delivery

| Phase | User-visible outcome | Primary code areas (indicative) |
|-------|----------------------|----------------------------------|
| **P0** | This spec approved | `docs/superpowers/specs/` |
| **P1** | Clearer main shell; mode badge; lean chrome inventory; Esc tests; `?`; context-sensitive `Ctrl+P` palette via quick_picker; `/status` carries moved detail | `chat_tui.go`, `status_footer.go`, `theme.go`/`style`, `quick_picker.go`, help overlay, tests |
| **P2** | Tasks overlay: list → Peek → Esc; cancel/stop if available | New tasks view + `Jobs()` inventory; key table |
| **P2.1** | Attach **only if** §9.3 gate passes | control/session APIs as needed |
| **P3** | Palette ↔ slash registry unification; statusline schema v1 dual-write | command/slash registry, statusline payload |
| **P4** | Resize/narrow polish; streaming/long-scroll performance; copy reliability; doc parity | transcript render path, viewport |

**Dependency:** P2 reuses P1 overlay patterns + Esc rules. P3 reuses P1 palette shell. P2.1 does not block P2 Peek ship. P4 must not block P1 merge.

**P1.5 note (2026-08-06):** a render/art polish phase now runs between P1 and P2 —
see `docs/superpowers/specs/2026-08-06-tui-render-animation-design.md`. It pulls
forward selected P4 performance items (incremental wrap, scroll repaint, panel
single-pass) plus motion discipline and art polish. It does not block P2 and does
not change P2/P3/P4 scope.

### P1 key table (minimum)

| Key | Action | Notes |
|-----|--------|-------|
| Enter | Send / queue | existing |
| Esc | Esc stack | tighten + **priority-order tests** |
| Shift+Tab | Plan axis | existing semantics |
| Ctrl+Y | YOLO | existing |
| Ctrl+B | Expand shell/tool body | existing Reasonix meaning; document in `?` |
| Ctrl+O | Reasoning verbose | existing |
| PgUp/PgDn | Scroll transcript | existing |
| `?` (empty input) | Cheatsheet | **new**; non-empty inserts `?` |
| Ctrl+P | Palette **when idle** | **new**; completion/picker keeps prev-item (§8.2.1) |
| `/` | Slash | existing |
| `/status` | Detailed status | enhance; host balance/git/cache detail moved from default chrome |

P2 adds `Ctrl+G` / `/tasks` / Peek / Attach. P3 deepens palette.

---

## 13. Architecture notes (for implementers)

### 13.1 Component boundaries (target)

```
chatTUI (Bubble Tea Model)
  ├── transcript / viewport
  ├── composer + mode badge
  ├── status block (interaction + optional data)
  ├── modals (P1: existing flags + help/palette; P2+: prefer overlayStack)
  └── keyBindings (table-driven where new keys land)
```

- P1 may keep boolean modal fields; formal `overlayStack` when Tasks ships or Esc tests demand it.  
- New UI for help/palette/tasks should live in dedicated files under `internal/cli/`.  
- Palette skeleton: extend **`quick_picker`**, do not fork a second fuzzy list.

### 13.2 Testing

- Table-driven key handling tests for Esc stack and mode badge rendering width.  
- Overlay open/close does not clear composer draft.  
- Status line packing at narrow widths (existing `status_footer` tests as base).  
- Tasks: empty list, one running, needs_input, peek/attach focus header.  
- Golden/string tests for cheatsheet containing critical bindings.

### 13.3 i18n

- User-visible strings go through existing `i18n` patterns where the project already localizes CLI chrome.  
- Key names in cheatsheet stay literal (`Ctrl+P`) for muscle memory.

### 13.4 Performance (P4, design constraints early)

- Avoid O(n²) full re-render of entire transcript on every token when cheap incremental updates exist.  
- Tasks list must virtualize or cap rows if counts can be large.  
- Palette filter must stay responsive on modest command sets first; scale in P3.

---

## 14. Success criteria

| Scenario | Pass |
|----------|------|
| Daily single-session | Keyboard-only: chat, interrupt, approve, cycle plan, toggle YOLO, open status detail, open help |
| First-run clarity | Mode visible at composer badge; footer not louder than transcript; tools collapsed by default |
| Multi-agent (P2) | `Ctrl+G` → select → Peek → Esc back; no mouse required; cancel/stop if runtime allows |
| Multi-agent Attach | **Only if P2.1 ships**; not required for P2 acceptance |
| Discoverability | New user finds Status via `?` or `Ctrl+P`; Tasks via same after P2 |
| Ctrl+P regression | Completion menus still use Ctrl+P/N for item nav |
| Regression | Existing slash, permissions, session resume, mouse-off mode still work |
| Custom statusline | Schema v1 dual-write; legacy `contextUsed`/`contextWindow` scripts still work |

---

## 15. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| `chat_tui.go` regressions | Phase-sized PRs; tests on Esc/draft/status; avoid unrelated refactors |
| Tasks data model fragmented | Inventory APIs in P2 plan; single TUI list DTO |
| Key conflicts (`Ctrl+G`, `Ctrl+P`, `?`) | Document conflicts; cheatsheet shows effective bindings; `?` only when composer empty |
| Users expect Grok dashboard | Empty state + docs: Tasks is intentional linear-shell design |
| Palette/slash drift | P3 single registry; until then palette is curated subset |

---

## 16. Open points deferred to implementation plan

These are intentionally not blocking design approval **after** the review locks above:

1. P2 plan’s full runtime inventory table (jobs vs tool-subagent markers)—must exist before Tasks UI coding.  
2. Whether jobs count stays on the data row only or also as a short interaction hint when &gt; 0.  
3. Cheatsheet multi-page layout / search (P3).  
4. Whether `/status` remains a transcript commit (current), becomes an overlay, or both—P1 may enhance the existing commit path.  
5. Desktop shortcut layout (`UIShortcutLayout() == "desktop"`) label variants—**in scope** for badge parity with `modeTagText()`, not a separate product.  
6. P2.1 Attach soft vs session-switch choice (gated).

---

## 17. Decision log (brainstorming)

| # | Topic | Choice |
|---|-------|--------|
| 1 | Main layout | A — Claude linear shell + overlays |
| 2 | Default status | B — one practical line (mode · model · context% · hint) |
| 3 | Disclosure | B — Claude mid: tool headers on, bodies off, fail/approve expand |
| 4 | Discoverability | B — `?` + `Ctrl+P` + `/` |
| 5 | Tasks UI | A — lightweight overlay |
| 6 | Enter on task | C — Peek default, Attach via `a` |
| 7 | Focus model | A — composer default, simple scroll |
| 8 | Mode chrome | B — composer left text badge |
| 9 | Statusline schema | B — Reasonix schema + `schema_version` |
| 10 | Theme | A — keep defaults; improve hierarchy |
| 11 | P1 keys | A — clarity + `?` + palette skeleton |
| 12 | Non-goals | Confirmed as §2 |
| 13 | Design review | Approve with changes → applied: Ctrl+P context split; footer 2-band inventory; mode taxonomy; Peek MVP / Attach P2.1; statusline dual-write |

**Delivery strategy:** vertical slices P0→P4 (recommended approach 1); P2.1 gated.

---

## 18. Next step

1. **P1 plan exists:** `docs/superpowers/plans/2026-08-06-tui-clarity-keyboard-p1.md` — implement via `subagent-driven-development` or `executing-plans` after plan acceptance.  
2. P2 plan must open with runtime inventory; Attach only as P2.1.  
3. Do not start P2/P3/P4 UI until the corresponding plan exists and is accepted.

**P1.5 note:** `docs/superpowers/specs/2026-08-06-tui-render-animation-design.md`
approved 2026-08-06; its implementation plan is the next plan to write (before P2).
