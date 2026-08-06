# Corvus TUI Render Animation & Art Polish — P1.5 Design

**Date:** 2026-08-06  
**Status:** Design approved (brainstorming §1–§3) + subagent review applied (2026-08-06, P0/P1 all fixed)  
**Program spec:** `docs/superpowers/specs/2026-08-06-tui-clarity-keyboard-design.md` (P0–P4 roadmap; this phase pulls forward selected P4 performance items and adds motion/art work)  
**Research:** `docs/superpowers/research/2026-08-06-render-animation/00-synthesis.md` + 4 单点报告 (claude-code / codex / qwen-grok / charm-capabilities)  
**Reviews:** `docs/superpowers/research/2026-08-06-render-animation/review-tech-facts.md` (技术事实核查) + `review-design.md` (设计审查) — 本版已吸收其 P0/P1 修订  
**Branch context:** design only; P1 already merged (`c91edc8`)

---

## 1. Problem

P1 delivered clarity and keyboard completeness. Measured and researched gaps remain in **fluidity, motion discipline, and art**:

| Gap | Evidence |
|-----|----------|
| Streaming janks on long transcripts | `BenchmarkWrapTranscript`: full re-wrap on every content growth — 5,000 lines ≈ 28ms/10MB, 10,000 lines ≈ 58ms/20MB per token |
| Scroll can flicker | Every viewport offset change returns `tea.ClearScreen()` (chat_tui.go:859, Warp-era workaround). In bubbletea v2 `ClearScreen` is a buffer erase + full-frame redraw (cursed_renderer.go:633–639), discarding the cell-diff renderer's work; full clears on scroll are a known flicker/jump source |
| Bottom panels render 3× per event | `View()` is called once per event (tea.go:888; the 60fps ticker only flushes the buffer), and inside that event `bottomRows()` + panel renders run ≈3 times total (update-path reads + View). Streaming events make this hot |
| No motion gate | Spinner, tool braille frames, elapsed all animate unconditionally; no reduced-motion path |
| Scroll is instant-jump | No smooth-scroll interpolation (bubbletea v2 viewport has none built in) |
| Elapsed counters jitter in width | `%ds` formats shift a column every 10s (Qwen #6533 lesson: fixed-width elapsed) |
| Art gaps | First-screen banner is bare; table code spans use the accent color everywhere (md.go:362–364, no syntax highlighting exists); diffview.go has 4 dead hardcoded color constants (only tests reference them); no discipline test forbidding hardcoded colors |

**Not the problem:** missing runtime power, keybindings, or layout architecture. The renderer (bubbletea v2 `cursedRenderer`: cell-level diff, DEC 2026 sync output auto-negotiated when the terminal reports support) is capable; the work is in how the app feeds it.

---

## 2. Goals and non-goals

### Goals

1. **Fluidity first**: fix the measured hot paths (incremental wrap, scroll repaint, panel triple-render) so 10k-line sessions stream and scroll smoothly.
2. **Motion discipline**: a single reduced-motion gate all animations must pass; a small, deliberate animation set (smooth scroll, fixed-width elapsed); no decorative animation without function.
3. **Art polish**: width-gated first-screen branding, density consistency, neutral table code cells, semantic color discipline enforced by test.
4. **Regression safety**: benchmarks + property tests + the existing suite stay green; every task is TDD.

### Non-goals (this phase)

1. Scrollback externalization / visible-window virtualization (Claude flat-memory / Codex native-scrollback route) — a later phase.
2. Replacing the bubbletea renderer or adopting a full-screen buffer rewrite (program spec §2 non-goal 5 stays).
3. Real-time fancy animation: gradients, skeleton screens, panel transitions (Qwen's explicit finding: terminals cannot render these well).
4. Replacing the 12-line live thinking tail with Qwen's fixed 1-line header — the live tail is an existing product feature (Ctrl+O toggles verbose).
5. P2 Tasks overlay, P3 statusline schema / palette registry, P4 remainder (copy reliability, doc parity).
6. New theme names, user keymap files, or new config sections (env-only switches).

---

## 3. Design principles (hard rules)

1. **Fluidity is a correctness property**: each event must do O(changed) work in the hot path, not O(transcript).
2. **Animation is opt-out**: every animated component consults `motionEnabled()`; reduce-motion mode means static/instant, never a different feature.
3. **Information before decoration**: elapsed time updates every second even with motion off; shimmer is the only decoration and stays spike-gated.
4. **One render pass per Update for panels**: panels are refreshed once at the end of `m.update()` (the single state-mutation point); `bottomRows()`, `computeStatusLineCount`, `transcriptHeight`, and `View()` only read the cache.
5. **Keyboard invariants unchanged**: smooth scroll must not break tail-follow, `AtBottom`, edge auto-scroll, or Esc behavior.
6. **Reuse runtime truth and existing theme slots**: no new color system; delete dead hardcoded colors instead of migrating them.
7. **Default conservative**: the default *runtime/architecture* changes are exactly the three measured fixes (§4); §5 lists every default-visible change explicitly.

---

## 4. Fluidity foundation

### 4.1 Scroll repaint (default off)

**Change:** stop returning `tea.ClearScreen()` when the viewport offset moves. Keep the legacy behavior behind an env switch.

- New field `scrollRepaint bool` on `chatTUI`; read once in `newChatTUI()` from env `CORVUS_TUI_SCROLL_REPAINT=1`, following the existing TUI env pattern (`mouseCaptureOffByDefault()` at chat_tui.go:612).
- Update wrapper: the ClearScreen branch becomes
  `if cm.viewport.YOffset() != prevYOff && !cm.nativeScrollback && !cm.sessionSwitch && cm.scrollRepaint`.
- Native scrollback path is unchanged (no clear there today).

**Why safe:** bubbletea v2.0.7 `cursedRenderer` diffs at cell level and writes only dirty lines. `ClearScreen` (buffer erase + full redraw) discards that diffing, and doing it on every scroll step (including smooth-scroll ticks, §5.2) is the flicker source. The env switch is the documented escape hatch for terminals that strand stale rows.

**Tests:** rewrite the existing ClearScreen test families (`chat_tui_test.go` ≈2917/2921) in the same task: default no ClearScreen, `CORVUS_TUI_SCROLL_REPAINT=1` restores, sessionSwitch suppression semantics preserved.

### 4.2 Incremental wrap cache

**Change:** replace whole-transcript re-wrap per content growth with a per-block wrapped-line cache.

- `wrapBlock(rendered string, width int) []string` — wraps one SGR-balanced transcript block (same lipgloss width render as today's `wrapTranscript`, but per block).
- New fields on `chatTUI`: `wrappedLines []string` (flat, in display order) and `blockLineCounts []int` (wrapped line count per transcript block; `len(wrappedLines)` is the total).
- Operations:
  - **Append** (content grew): wrap only the new block(s), append lines + offset. O(new block).
  - **Set block i** (live tool/reasoning tail): re-wrap only block i and replace its line range; last-block sets (common streaming case) are O(block) with an O(1) start lookup (`len(wrappedLines) − old count`); middle-block sets are rare and O(nBlocks) for the prefix sum — acceptable.
  - **Remove/truncate** (compaction, collapse): drop the block's line range via a prefix sum (O(nBlocks), rare) and splice `blockLineCounts`.
  - **Width change**: full rebuild via `rebuildWrappedLines(contentW)` reusing `wrapBlock` per block (reflow path, rare).
- **Mutation inventory (mandatory):** `m.transcript[idx]` is written directly at chat_tui.go:2202/2318/2331/2452 (tool stream preview, collapse, `tickToolRunning`), bypassing `setTranscriptBlock`. All four sites must either go through a cache-aware setter or mark the changed block dirty (`dirtyBlockIdx`) so the wrapper re-wraps exactly that block. `transcriptDirty` alone is not enough (it is set at every live update); the plan's first task is a transcript-mutation inventory table.
- `viewport.SetContent(strings.Join(wrappedLines, "\n"))` remains; join is O(total bytes) memcpy (sub-ms at 10k×80), acceptable and benchmarked.
- `cm.wrappedLines` (existing field) stays as the flat slice; remove the `strings.Split` of the whole wrapped content.

**Equivalence:** a property test asserts incremental construction equals full `wrapTranscript` for the same transcript sequence (including set/remove/truncate interleavings and the four direct-mutation patterns).

### 4.3 Bottom panels single pass

**Change:** render all bottom panels once per Update; all consumers read the cache.

- New `bottom_panels.go`: `bottomPanels` struct holding the rendered strings + total rows for: todo panel, approval banner, chooser, rewind, MCP import, resume picker, quick picker, copy picker, cheatsheet, completion, main manager (native scrollback only, incl. `renderMainManagerFooter` which today renders in both `bottomRows()` and `View()`).
- `chatTUI.refreshBottomPanels()` runs **at the end of `m.update()`** — after all state mutations (including native-scrollback `finalize` paths) and before every consumer: `computeStatusLineCount`, `syncInputHeightLimit`, `transcriptHeight`, `bottomRows()`, and `View()`. The Update wrapper needs no further panel work.
- Empty-cache fallback: if `View()`/`bottomRows()` are called before the first refresh (tests, startup), render on demand. 
- Because `m.update()` is the single mutation point and the refresh happens inside it, the cache is never stale across events; no per-field dirty tracking.
- `computeStatusLineCount`/`transcriptHeight`/`View` lockstep is preserved by construction (all read the same cache).

**Why safe:** `View()` runs once per event (tea.go:888) and the 60fps ticker only flushes the renderer buffer; today's ≈3 panel renders per event collapse to 1. During streaming this removes per-token duplication without fine-grained dirty tracking.

---

## 5. Motion & art

### 5.1 Reduced-motion gate

- New `motion.go`: `motionEnabled() bool` is **true when animation is enabled** — it reads env `CORVUS_REDUCE_MOTION` on every call and returns false when that env is set to `1` (pattern follows `mouseCaptureOffByDefault()`; no config section exists, env only, YAGNI).
- Consumers (must consult the gate):
  - Spinner scheduling: motion off → the working line shows a static glyph (first frame) and does **not** schedule `spinner.Tick` (elapsed ticker still runs — it is information).
  - **Tool working line**: `tickToolRunning` (driven by `elapsedTickMsg`) must not advance `toolStreamFrame` when motion is off — the braille frames freeze on the first glyph. (It animates today because the elapsed ticker keeps running.)
  - Smooth scroll (§5.2): motion off → instant jump (today's behavior).
  - Shimmer (§5.3, if it ships): off.
- Test: enumerate every animation entry point (spinner scheduling, tool frame advancement, smooth-scroll start) and assert it consults the gate; with `CORVUS_REDUCE_MOTION=1` no `spinner.Tick`/scroll-tick is scheduled and tool frames stay frozen.
- Test seam: gate helpers return `nil` cmd when disabled; tick messages are synthetic and injectable; no wall-clock sleeps in tests.

### 5.2 Smooth scroll interpolation (pinned spec)

- New `smooth_scroll.go`: single state machine `{active, from, to, start, dur}` driven by a 16ms `tea.Tick`.
- Inputs: PgUp/PgDn (page = viewport height − 1 lines) and wheel up/down (3 lines) start an interpolation from the current `YOffset` to the target, clamped to `[0, maxOffset]`.
- Timing: **dur = 150ms fixed**, ease-out cubic `t' = 1 − (1−t)³`; each tick recomputes and **reclamps** the offset to current bounds, and the final tick **snaps** exactly to the target.
- **Instant exceptions** (no animation): `GotoTop`/`GotoBottom` (incl. tail-follow), edge auto-scroll while mouse-held, any scroll input while `!motionEnabled()`, and **whenever `CORVUS_TUI_SCROLL_REPAINT=1`** (legacy full repaint per tick would flicker).
- **Interrupt**: a new scroll input during an animation cancels it and starts from the current offset. Content growth during an animation reclamps the target to the new bounds.
- AtBottom semantics: during animation the viewport is not at bottom; tail-follow is unaffected (only triggers when at bottom).
- No behavior change to Esc, draft, or keyboard completeness.

### 5.3 Shimmer (spike-gated, optional)

- Working-line text single-pass sweep using a `Blend1D`-precomputed color table (lipgloss v2 has no animated gradient API).
- **Gate:** stays out of the formal scope unless the spike A/B shows clear feel gain; always gated by `motionEnabled()`. Spike is a bounded optional task inside the plan (≈half a day), with an explicit go/no-go outcome recorded.

### 5.4 Fixed-width elapsed

- `formatElapsedFixed(sec int) string` — `%3d` right-aligned seconds **without unit**: `"  3"`, `" 12"`, `"123"`, `"999"` (values ≥ 1000 clamp to `"999"`). With the locale unit suffix the display is a stable 4 columns (e.g. `"  3s"`, `" 12s"`). The unit stays in each locale's fmt string (avoids `"3s 秒"`).
- **All six elapsed call sites** (verified): working line `ChatStatusThinkingFmt` (chat_tui.go:2825) + `ChatStatusCancellingFmt` (2823); tool working line `ChatToolWorkingFmt` (2431, 2452); collapsed reasoning marker `ChatThoughtForFmt` (2465, 2480). Each fmt switches its seconds argument from `%d` to `%s` in all three locales (`messages_en/zh/zh_tw`) and callers pass `formatElapsedFixed(...)`.
- `ChatStatusRetryingFmt` is **not** an elapsed site (its `%d/%d` are attempt/max) — unchanged.
- `internal/i18n/i18n.go` comments updated (`%s = fixed-width elapsed seconds`).
- Turn receipt (`renderTurnReceipt`) shows no elapsed today — unchanged.

### 5.5 First-screen branding (width-gated)

- `renderTUIBanner`: wide (≥60 cols) keeps today's two lines (`◆ corvus · label` + tip); narrow (<60 cols) renders a single trimmed wordmark line (no tip, hard-truncated to width). Accent + bold wordmark reuse existing theme slots; static, no animation.

### 5.6 Density audit (sealed scope)

- Scope is exactly: find and fix **double blank lines / inconsistent margins** between blocks (tool cards, thinking tail, user bubble, receipts) in `md.go`/`toolcard.go`/`chat_tui.go`. No spacing system redesign.
- `commitSpacer` already guarantees a single blank line between blocks; acceptance = a regression test asserting a mixed-session transcript never contains two consecutive blank lines.

### 5.7 Table code cells neutral

- **Premise (verified):** md.go has no syntax highlighting; every `CodeSpan` renders as `accent(...)` (md.go:362–364), including inside table cells.
- Change: inside table cells, `CodeSpan` renders as monospace + neutral color (theme `muted`/`faint`) instead of accent; outside tables unchanged.
- Implementation: `appendInline` gains an "in table cell" context flag (set while walking table cell nodes).
- Test: a table containing a code span renders that cell without accent color codes.

### 5.8 Semantic color discipline

- The 4 diffview constants (`bgDiffAdd`/`bgDiffDel`/`fgDiffAdd`/`fgDiffDel`, diffview.go:29–32) are **dead code** (only `diffview_test.go` references them; runtime rendering already uses theme slots) → **delete** them and update `diffview_test.go` to source colors from `activeCLITheme` (`diffAddBG`/`diffDelBG`/`success`/`err`).
- `style.go`'s `ansiAccent` (`\033[38;5;173m`) is exempt with a documented reason: it is a test-pinned concrete sequence for theme-independent tests (comment already states this; the color-discipline test whitelists `style.go` for that purpose).
- New `TestNoHardcodedColorCodes`: use **go/ast to scan string literals** in `internal/cli` (excluding `theme.go`, `style.go`, `*_test.go`) for anchored color-only CSI patterns ending in `m`: `\033\[[34][0-7]m`, `\033\[9[0-7]m`, `\033\[10[0-7]m`, `\033\[38;5;[0-9]+m`, `\033\[48;5;[0-9]+m`, `\033\[38;2;[0-9]+;[0-9]+;[0-9]+m`, `\033\[48;2;[0-9]+;[0-9]+;[0-9]+m`. go/ast avoids false hits in comments and non-color sequences (`\033[K`, OSC 1337 markers).

---

## 6. Testing

- **Benchmarks** (kept + added): `BenchmarkWrapTranscript` (reflow path, unchanged); `BenchmarkAppendBlock` (append 1 block ≈ 1 token increment at a 10k-line base); optionally `BenchmarkBottomPanels`. Benchmarks are reports, not assertions.
- **Property test**: incremental wrappedLines == full wrapTranscript equivalence (append/set/remove/truncate interleavings + the four direct-mutation patterns).
- **Scroll repaint**: default no ClearScreen on scroll; env `CORVUS_TUI_SCROLL_REPAINT=1` restores; sessionSwitch semantics preserved; native scrollback unchanged. Rewrites the two existing ClearScreen test families in the same task.
- **Panel cache**: `panelRenderHook` counter proves each panel renders once per event; `bottomRows()` == cached rows; resize refresh; first-frame fallback.
- **Motion gate**: all four animation entries (spinner scheduling, tool frame advancement, smooth-scroll start, shimmer-if-shipped) consult `motionEnabled()`; `CORVUS_REDUCE_MOTION=1` schedules no spinner/scroll ticks and freezes tool frames.
- **Smooth scroll**: start/end states, ease-out progression, final snap, interrupt mid-flight, instant exceptions (GotoBottom, edge auto-scroll, legacy env combo), motion-off instant jump, content-growth reclamp, AtBottom during animation.
- **Fixed-width elapsed**: widths stable for 0–999s at all six call sites.
- **Branding**: wide ≥60 and narrow <60 variants; tip present/absent; narrow truncates to width.
- **Density**: no double blank lines in mixed transcript.
- **Table cells**: code span in table → neutral, no accent; outside table unchanged.
- **Color discipline**: `TestNoHardcodedColorCodes` green after dead-constant deletion.
- **Keyboard regression**: a checklist row (not just unit tests) — chat, interrupt, approve, Esc stack, draft preservation, completion Ctrl+P/N — verified by existing tests + the plan's manual acceptance.
- **Existing suite**: `go test ./internal/cli/ -count=1` stays green (2.9s baseline).

---

## 7. Delivery

- **Spec:** this document; the program spec gets a short P1.5 note (§12 dependency area + §18 next steps) pointing here.
- **File map (indicative):**
  - `internal/cli/motion.go` (new) — gate + tests
  - `internal/cli/smooth_scroll.go` (new) — state machine + tests
  - `internal/cli/bottom_panels.go` (new) — cache + refresh + tests
  - `internal/cli/transcript.go` — `wrapBlock`, wrappedLines cache ops, `rebuildWrappedLines`, cache-aware setter for direct-mutation sites + tests
  - `internal/cli/chat_tui.go` — `scrollRepaint` field + Update wiring, panel refresh, smooth-scroll key wiring, elapsed call sites, banner wide/narrow, env reads (`newChatTUI`/`mouseCaptureOffByDefault` pattern)
  - `internal/cli/chat_tui_test.go` — ClearScreen test families rewritten (same task as §4.1)
  - `internal/cli/status_footer.go` — `formatElapsedFixed` + tests
  - `internal/cli/md.go` — table cell context flag + neutral CodeSpan + test
  - `internal/cli/diffview.go` + `internal/cli/diffview_test.go` — delete dead constants, test uses theme slots
  - `internal/cli/style.go` — keep `ansiAccent` (exempt, documented)
  - `internal/cli/bench_test.go` — keep + `BenchmarkAppendBlock`
  - `internal/i18n/{i18n,messages_en,messages_zh,messages_zh_tw}.go` — elapsed fmt `%s` + comment updates
- **Process:** TDD per task (failing test → implement → pass → commit); one task = one §4.x/§5.x item; order fluidity → motion → art; shimmer spike is an optional task with go/no-go. Commit prefixes follow P1 (`feat(cli):` / `test(cli):` / `style(cli):`), one commit per task. Feature branch/worktree.
- **Plan opening sections:** transcript-mutation inventory table (required by §4.2) + test-seam list (synthetic ticks, nil-cmd gating, `panelRenderHook`, injectable clock).
- **Spec-coverage table:** the plan ends with a §4/§5 item → task mapping (P1 plan convention).
- **Env docs:** `CORVUS_TUI_SCROLL_REPAINT` / `CORVUS_REDUCE_MOTION` documented in README (env section).
- **Research artifacts:** `docs/superpowers/research/2026-08-06-render-animation/` + `internal/cli/bench_test.go` already committed with the first spec version.

---

## 8. Success criteria

| Scenario | Pass |
|----------|------|
| Streaming at 10k lines | Append cost ~58ms → <2ms per token (benchmark); no visible jank in user terminal |
| Scroll | Default no ClearScreen, smooth interpolation on PgUp/PgDn/wheel, GotoBottom/auto-scroll instant. Env matrix: default = smooth; `CORVUS_REDUCE_MOTION=1` = instant; `CORVUS_TUI_SCROLL_REPAINT=1` = instant (legacy full repaint) |
| Bottom region | Panels render once per event; height/View lockstep tests green |
| Reduce motion | `CORVUS_REDUCE_MOTION=1` → static spinner, frozen tool frames, instant scroll, no shimmer; elapsed still ticks |
| Art | Wide/narrow branding; neutral table code cells; dead diff constants deleted; `TestNoHardcodedColorCodes` green |
| Regression | Full `internal/cli` suite green; keyboard/Esc/draft invariants unchanged (existing + manual checklist) |
| Manual | Verified on user's terminal + at least one of Warp / iTerm2 / Windows Terminal / konsole (env fallback documented) |

---

## 9. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Removing ClearScreen strands rows on some terminals | env `CORVUS_TUI_SCROLL_REPAINT=1`; README docs; manual terminal list (§8) |
| wrappedLines cache bugs (offset drift, missed mutation sites) | property equivalence test; plan opens with mutation inventory; cache-aware setter for the 4 direct writes |
| Smooth scroll breaks tail-follow / AtBottom / legacy combo | instant exceptions + env combo tests |
| Panel cache staleness | refresh inside `m.update()` (single mutation point), before all consumers; empty-cache fallback |
| i18n fmt change breaks locales or retry test | all three locales + `i18n.go` comments in one task; `ChatStatusRetryingFmt` untouched; locale tests |
| Shimmer feels cheap | spike go/no-go gate; motion gate; drop if not clearly better |
| Color test false positives | go/ast + anchored color-only CSI patterns; `style.go` exemption documented |

---

## 10. Decision log (brainstorming + review)

| # | Topic | Choice |
|---|-------|--------|
| 1 | Round direction | C — dual track: fluidity foundation + curated motion/art |
| 2 | Scroll repaint | Default off; env `CORVUS_TUI_SCROLL_REPAINT=1` legacy; read in `newChatTUI()` |
| 3 | Wrap strategy | Per-block incremental cache + `blockLineCounts`; O(1) last-block rewrap, O(nBlocks) prefix sums for rare middle/truncate; mutation inventory required |
| 4 | Panel rendering | Once per `m.update()`; `View()` is per-event (tea.go:888), not per-frame |
| 5 | Motion gate | `motionEnabled()` = animation enabled; env `CORVUS_REDUCE_MOTION=1` disables it (read per call); all 4 animation entries incl. tool frames |
| 6 | Smooth scroll | 150ms fixed, ease-out cubic, 16ms tick, final snap, per-tick reclamp; instant exceptions incl. legacy env |
| 7 | Shimmer | Spike-gated; bounded optional task with go/no-go |
| 8 | Thinking tail | Keep 12-line live tail; only elapsed width fixed |
| 9 | Elapsed | `formatElapsedFixed` width-4, unit in locale fmt; 6 call sites; RetryingFmt untouched |
| 10 | Branding | Width-gated ≥60 cols; static |
| 11 | Diff colors | Dead constants deleted (not migrated); test uses theme slots |
| 12 | Color discipline | go/ast + anchored CSI patterns; `style.go` `ansiAccent` exempt (test-pinned) |
| 13 | Table code cells | CodeSpan neutral inside tables only; no syntax highlighting premise (verified) |
| 14 | P2/P3/P4 | Unchanged roadmap; P4 perf items pulled forward; program spec gets P1.5 note |

---

## 11. Next step

1. User review gate for this revised spec.
2. Invoke `superpowers:writing-plans` to produce the P1.5 implementation plan (tracks: fluidity / motion / art; opening sections = mutation inventory + test seams).
3. Implement via plan (subagent-driven or executing-plans) after plan acceptance.
