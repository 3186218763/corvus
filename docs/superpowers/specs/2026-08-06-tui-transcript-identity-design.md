# Corvus TUI Transcript Identity & Tool-Card Coloring Design

**Date:** 2026-08-06
**Status:** Design approved in brainstorming (user) — spec written, pending subagent review
**Program context:** follows P1 `2026-08-06-tui-clarity-keyboard-design.md` and P1.5 `2026-08-06-tui-render-animation-design.md`; this is a focused transcript-art slice, no P2/P3 dependencies
**Reference:** user cited Codex CLI keyword coloring as the visual model for tool lines (review may verify online)
**Branch context:** design only; no code changes yet

---

## 1. Problem

Three user-visible gaps in the TUI transcript:

| Gap | Evidence |
|-----|----------|
| Brand noise in history | Every assistant reply renders `  ◆ Corvus` (transcript.go:166–181, header at :179). In a long session the brand name repeats on every reply instead of identifying only the current exchange |
| Weak user/agent differentiation | User bubbles are fully accent-tinted (`renderUserBubble`, chat_tui.go:4760–4767), while assistant bodies are plain text with an accent `◆` header — both sides use the same accent, and history has no depth |
| Tool-call lines have no keyword color | `toolCard`/`toolHead` (toolcard.go:168–181) color only the `●` dot by category (`toolDot` :78–105); the verb is plain bold and the argument renders in the default foreground. Codex CLI distinguishes verb and argument visually (e.g. cyan read verbs vs neutral args) |

**Not the problem:** the top-of-session banner `◆ corvus · <model>` (renderTUIBanner, chat_tui.go:4734–4753) is a session title and stays unchanged. Tool output bodies, diff bodies, receipts, and the composer are out of scope.

---

## 2. Goals and non-goals

### Goals

1. **Brand restraint**: the name "Corvus" appears only on the bottom-most conversation's assistant reply; older replies keep a quiet `◆` marker without the name.
2. **User/agent differentiation**: user bubbles are the only accent-tinted prose in the transcript; assistant bodies stay neutral.
3. **History depth**: historical user bubbles render in a faded variant of the user (accent) color; the latest user bubble keeps the full accent.
4. **Tool keyword coloring**: tool-call lines render the verb in its category color (same taxonomy as the dot) and the argument in a dedicated `toolArg` tone.
5. **Copy parity**: `buildCopyTranscript` (transcript.go:255–288) stays byte-identical (after ANSI strip) to the visible transcript.
6. **Regression safety**: every task is TDD; `go test ./internal/cli/ -count=1` stays green.

### Non-goals (this phase)

1. Changing the session banner (`renderTUIBanner`) — it is the session title, not message identity.
2. Shell-command keyword syntax highlighting inside `Bash` arguments (needs a shell lexer; separate phase).
3. Changing the failure line (`red("●") + bold(name) + red("⊘ err")`, chat_tui.go:3933) — error state keeps red.
4. Background chips for user bubbles — the unused `userBubbleBG` palette slot stays unused.
5. New theme names, user JSON themes, or new config sections — one palette slot + one derived slot only.
6. Changing the transient "working · Ns" line (`tickToolRunning`, chat_tui.go:2447–2480).

---

## 3. Design principles (hard rules)

1. **Liveness is derived, never stored**: the "current" markers are computed from `transcriptSources` at render time. No new fields to keep in sync on append/remove/truncate/clear.
2. **Brand is chrome, not message identity**: message identity uses glyph + color; the brand name is reserved for the live exchange.
3. **History recedes symmetrically**: latest exchange full-strength on both sides (full accent user bubble, named assistant), everything above quiet (faded user color, bare dim `◆`).
4. **Semantic colors only**: no new hardcoded SGR sequences; all new colors go through `cliPalette` slots and `themeFg`/`themeStyle` (`color_discipline_test.go` stays green).
5. **Copy parity**: visible text of `renderAssistantMarkdownCopy`/`renderReplayBundleCopy` must equal the non-copy path after ANSI stripping.
6. **Tool coloring reuses the existing taxonomy**: `read/write/exec/proc/default` maps to `toolRead/success/warn/toolProc/accent`; no new category system.

---

## 4. Transcript identity (message liveness)

### 4.1 Marker model

New bitmask type in `transcript.go`:

```go
type transcriptMarker uint8

const (
    markerNone           transcriptMarker = 0
    markerUserCurrent    transcriptMarker = 1 // render user content full accent
    markerAssistantNamed transcriptMarker = 2 // render assistant name
)
```

Stateless computation, `func currentTranscriptMarkers(sources []transcriptSource) []transcriptMarker`:

1. `lastUser` = last index with kind `transcriptSourceUser`.
2. `lastAssistant` = last index with kind `transcriptSourceMarkdown` or `transcriptSourceReplayBundle`.
3. `namedIdx` = `lastAssistant`; if `lastAssistant >= 0 && lastUser > lastAssistant` → `namedIdx = -1`.
4. Per block:
   - kind `user` → `markerUserCurrent` iff `i == lastUser`.
   - kind `markdown` → `markerAssistantNamed` iff `i == namedIdx`.
   - kind `replayBundle` → `markerAssistantNamed` iff `i == namedIdx`; `markerUserCurrent` iff `lastUser < i` (no user source after the bundle ⇒ the bundle's last internal user message is the most recent user content).

Expected behavior:

| Transcript | Markers |
|-----------|---------|
| `[u1, a1]` | u1 full accent; a1 named — the bottom-most conversation is highlighted as a unit |
| `[u1, a1, u2]` | u1 faded; a1 unnamed; u2 full — sending a new message demotes the previous exchange |
| `[u1, a1, tool, a2]` | only a2 named (last answer of the turn) |
| `[bundle]` (resumed session) | bundle's last internal user full + last internal assistant named |
| `[bundle, a2]` | bundle internal user full (still latest user content), a2 named |
| `[bundle, u1]` | bundle fully demoted, u1 full |
| after `/cls` (`clearTranscriptDisplay`) | nothing named until the next reply |

Reasoning, tool-card, notice, and banner blocks never carry markers.

### 4.2 Render behavior

- `renderAssistantMarkdown(raw string, contentWidth int, named bool)`:
  - `named` → current header `  ◆ Corvus` (accent diamond + bold name, transcript.go:179 unchanged).
  - `!named` → `  ◆` only, diamond rendered in `activeCLITheme.faint` (dim) — quiet agent identity, no brand.
  - Body rendering unchanged (two-cell indent, plain foreground).
- `renderAssistantMarkdownCopy(raw string, contentWidth int, prefix string, named bool)` — mirrors the above; zero-width math markers unchanged.
- `renderUserBubble(line string, width int, planMode bool, current bool)`:
  - `current` → existing `"  " + accent(prefix+line)`.
  - `!current` → same layout with `themeFg(activeCLITheme.userBubbleFaded, prefix+line)`.
- `renderTranscriptSource(source transcriptSource, terminalWidth int, marker transcriptMarker)`:
  - markdown → `renderAssistantMarkdown(source.raw, contentWidth, marker&markerAssistantNamed != 0)`
  - user → `renderUserBubble(source.raw, terminalWidth, source.planMode, marker&markerUserCurrent != 0)`
  - replayBundle → `m.renderReplayBundle(source, contentWidth, renderers, marker)`
  - reasoning / toolCard / banner / fixed unchanged.
- `renderReplayBundle` / `renderReplayBundleCopy` gain the marker and pass it into `replaySectionsForWithAssistantRenderer`.
- `replaySectionsForWithAssistantRenderer(history, width, renderAssistant, renderUser, nameLast, lastUserFull)`:
  - Pre-scan history once: `lastUserSectionIdx` = last section rendering a user bubble (`!LocalOnly`, Role `user`, and not a steer message — the `agent.SteerText` branch stays a notice line); `lastAssistantBodyIdx` = last section rendering an assistant body (Role `assistant` with non-empty Content, or `LocalOnly` with non-empty Content).
  - `renderUser` is called with `current == (i == lastUserSectionIdx && lastUserFull)`.
  - `renderAssistant` is called with `named == (i == lastAssistantBodyIdx && nameLast)`.
  - Reasoning, tool cards, steer, interrupted notices unchanged.
- `replaySectionsFor` (chat_tui.go:4677, currently test-only) keeps a default (all demoted) or moves into tests — decided in the plan.

### 4.3 Re-render sync points

`commitTranscriptSource` (transcript.go:225–230):
1. `oldMarkers := currentTranscriptMarkers(m.transcriptSources)` (pre-append).
2. Render the new block with its deterministic post-append marker:
   - kind `user` → `markerUserCurrent`
   - kind `markdown` → `markerAssistantNamed`
   - kind `replayBundle` → `markerAssistantNamed | markerUserCurrent`
   - otherwise → `markerNone`
3. `appendTranscriptBlock` (transcript.go:50–54).
4. `newMarkers := currentTranscriptMarkers(m.transcriptSources)`; re-render every pre-existing index where `old != new` via `setTranscriptBlock` (transcript.go:66–72). At most 2 indices change per commit.

`streamAnswer` (chat_tui.go:2557) and `commitPending` (chat_tui.go:2578): compute the marker from the current state (`currentTranscriptMarkers(...)[answerIdx]`) — the streaming block is always the current named block, but deriving from state keeps every path correct.

`removeTranscriptBlock` (transcript.go:74–96) and `truncateTranscriptBlocks` (transcript.go:98–109): compute old markers before the mutation, new markers after, re-render changed surviving indices (same helper as commit).

`reflowTranscript` (transcript.go:214–223): pass per-index markers from `currentTranscriptMarkers` — no extra work.

`buildCopyTranscript` (transcript.go:255–288): compute markers once, pass to `renderAssistantMarkdownCopy` / `renderReplayBundleCopy`.

`clearTranscriptDisplay` (chat_tui.go:1872–1896): nothing to do — markers are derived, state resets naturally.

**Native scrollback (Termux) limitation:** lines are printed once via the `pendingCommit` flush (chat_tui.go:1858–1860); `setTranscriptBlock` re-renders cannot retract printed text. Markers are therefore frozen at commit time for printed lines — a name once printed stays until `/cls`. Documented limitation, matches the append-only nature of the mode.

### 4.4 Palette: `userBubbleFaded`

- New `cliPalette` slot `userBubbleFaded cliColor` (theme.go:36–48), set in `applyCLIThemeStyle` (theme.go:148–152) so it tracks the active accent style (graphite/ember/aurora/midnight/sandstone/porcelain/linen/glacier).
- hex derivation: `c' = round(0.45*accent + 0.55*#808080)` per channel — desaturated tint "near my color", one formula for both modes.
- xterm fallback: hand-chosen per accent style (repo convention: hand-picked 256-color fallbacks, theme.go:30–35 comment); a small `map[styleName]xterm` with the accent's own xterm as fallback.
- Rendered via `themeFg`, flows through `fgSGR` (truecolor vs xterm) like every other color.

---

## 5. Tool-card keyword coloring

### 5.1 Category-colored verb

- Extract `func toolCategoryColor(name string) cliColor` from the switch inside `toolDot` (toolcard.go:81–105): read→`toolRead`, write→`success`, exec→`warn`, proc→`toolProc`, default→`accent`.
- `toolHead` (toolcard.go:172–181):
  - verb: `themeFg(toolCategoryColor(name), bold(label))` (was plain bold).
  - parens: unchanged `dim`.
  - arg: `themeFg(activeCLITheme.toolArg, clampPlain(arg, avail))` (was default foreground).
- `toolDot` keeps rendering `●` via the same helper — one taxonomy, two consumers.
- Auto-benefits: `toolCard` (toolcard.go:168–170), diff header (`diffBlock`, diffview.go:72), replayed tool cards (chat_tui.go:4696/4721).

### 5.2 Palette: `toolArg`

- New `cliPalette` slot `toolArg cliColor`, fixed per mode like `toolRead`/`toolProc` (not accent-style-driven):
  - dark: `#a5b0bd` (steel blue-gray), xterm `145`.
  - light: `#5a6470` (deep steel gray), xterm `240`.
- Starting values; implementation verifies on both themes and pins final values in tests. Requirement: clearly distinct from `faint` (#858b96), `muted` (#cbd0d8), and every category verb color.

### 5.3 Unchanged lines

- Failure line (chat_tui.go:3933) stays red — error state overrides category color.
- Transient working line (`tickToolRunning`) unchanged.
- No bash argument syntax highlighting (non-goal §2.2).

---

## 6. Testing

TDD per task (failing test → implement → commit):

1. **Marker computation** (new table-driven test): sequences from §4.1 + empty/all-fixed + all-three-kinds (`[u, md, bundle]`, `[bundle, md, u]`).
2. **renderAssistantMarkdown**: named keeps `  ◆ Corvus`; unnamed renders `  ◆` with faint SGR and no name (strip-ANSI pin + color pin).
3. **renderUserBubble**: current → accent SGR; faded → `userBubbleFaded` SGR; both color profiles (truecolor/xterm) and both themes.
4. **Commit re-render**: two turns — after the second user commit the first bubble is faded and the first answer loses the name; after the second answer it is named again.
5. **Remove/truncate re-tag**: `[u, md, md]` → remove last md → first md becomes named.
6. **Reflow**: width change preserves markers.
7. **Copy parity**: `buildCopyTranscript` ANSI-stripped visible text equals the joined transcript for a mixed transcript (extend the existing parity test).
8. **Tool colors**: `toolCard("grep", …)` verb carries `toolRead` SGR + arg carries `toolArg` SGR; `bash` → warn verb; `write_file` → success verb; parens dim; dot unchanged (extend `toolcard_test.go`).
9. **diffBlock header** carries the same treatment (diffview_test.go).
10. **Existing pins updated** (only where semantics intentionally changed):
    - transcript_test.go:24–27 `TestAssistantMarkdownHasIdentityAndIndentedBody` → named=true.
    - transcript_test.go:43–55 `TestReplaySectionsKeepAssistantIdentity` → uses `replaySectionsFor` default markers (specify in plan).
    - chat_render_test.go:97/110 — unchanged (single answers are the named block).
    - chat_tui_test.go:1436–1451 `TestUserBubbleIsLightweightTranscriptLine` — unchanged (single bubble is current).
    - toolcard_test.go plain-text assertions — unchanged.
11. **color_discipline_test.go** stays green (no new SGR literals).

---

## 7. Delivery

Plan tasks (sequential — tasks 2–4 touch the same functions in transcript.go):

1. Marker computation + table-driven tests (pure function, no rendering change).
2. Palette slots (`userBubbleFaded`, `toolArg`) + `applyCLIThemeStyle` derivation + render signature changes (`renderAssistantMarkdown`, `renderUserBubble`) + their tests.
3. `renderTranscriptSource`/`commitTranscriptSource`/remove/truncate/reflow/copy plumbing + tests.
4. Replay-bundle internal flags (`renderReplayBundle`/`renderReplayBundleCopy`/`replaySectionsForWithAssistantRenderer`) + tests.
5. Tool-card coloring (`toolCategoryColor` extraction + `toolHead`) + tests.
6. Existing test updates + full suite + bench sanity (`go test ./internal/cli/ -count=1`, `go vet ./internal/cli/`).

---

## 8. Success criteria

| Scenario | Pass |
|----------|------|
| Fresh session, one exchange | user bubble full accent; answer header `◆ Corvus` |
| Second exchange | first bubble faded, first answer shows `◆` only, second exchange full |
| Multi-answer turn | only the last answer named |
| Resume session | bundle's last internal user full + last internal assistant named; after sending, all bundle content demoted |
| `/cls` | no name until the next reply |
| Copy | selected-text parity holds |
| Tool cards | verb colored by category, arg in `toolArg` tone, dot unchanged, diff header consistent |
| Themes | both modes + all 8 accent styles render faded variants (visual pass + pinned SGR tests) |
| Native scrollback | printed names stay (documented limitation); no crash |
| Regression | full CLI suite green; no new hardcoded SGR |

---

## 9. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Marker drift on mutation paths | Stateless derivation + one resync helper used by commit/remove/truncate; tests for each mutation (mirrors the `liveDirtyIdx` pattern) |
| Faded color illegible on light/dark | Single mix formula verified per mode; hand-picked xterm fallback; visual check pinned in tests |
| Replay-bundle dual-flag complexity | One function owns the internal pre-scan; copy path uses the same function; bundle marker cases table-tested |
| Re-render churn | Bounded to ≤2 blocks per commit; O(n) marker scan only on commit/reflow, never per frame |
| Existing "◆ Corvus" pins | Enumerated in §6.10; changed only where semantics intentionally changed |
| Codex parity is qualitative | Subagent review (online) verifies the reference; acceptance is "distinct keyword colors", not byte-matching Codex |

---

## 10. Decision log (brainstorming)

| # | Topic | Choice |
|---|-------|--------|
| 1 | "corvus" scope | Per-message `◆ Corvus` header; session banner unchanged (user confirmed) |
| 2 | Liveness rule | Last user full accent + last assistant named (no user source after); demotion on new user commit |
| 3 | History assistant marker | Bare dim `◆`, name removed |
| 4 | Faded user color | Accent mixed toward neutral gray (#808080, 45/55); hand-picked xterm per accent style |
| 5 | Tool coloring | Verb = existing category color, bold; arg = new `toolArg` slot; failure line stays red |
| 6 | Bash keyword highlighting | Out of scope |
| 7 | Mechanism | Stateless marker derivation (rejected: stateful fields; static age-based fading) |

---

## 11. Next step

1. Subagent design review (online-capable) → `docs/superpowers/research/2026-08-06-transcript-identity/review-design.md` (P0/P1/P2 severity).
2. Apply P0/P1 fixes, re-commit the spec.
3. Invoke `superpowers:writing-plans` for the implementation plan, then execute TDD task-by-task.
