# Reasonix TUI Render Animation & Art Polish — P1.5 Design

**Date:** 2026-08-06  
**Status:** Design approved (brainstorming §1–§3)  
**Program spec:** `docs/superpowers/specs/2026-08-06-tui-clarity-keyboard-design.md` (P0–P4 roadmap; this phase pulls forward its P4 performance items and adds motion/art work)  
**Research:** `docs/superpowers/research/2026-08-06-render-animation/00-synthesis.md` + 4 单点报告 (claude-code / codex / qwen-grok / charm-capabilities)  
**Branch context:** design only; P1 already merged (`c91edc8`)

---

## 1. Problem

P1 delivered clarity and keyboard completeness. Measured and researched gaps remain in **fluidity, motion discipline, and art**:

| Gap | Evidence |
|-----|----------|
| Streaming janks on long transcripts | `BenchmarkWrapTranscript`: full re-wrap on every content growth — 5,000 lines ≈ 28ms/10MB, 10,000 lines ≈ 58ms/20MB per token |
| Scroll can flicker | Every viewport offset change returns `tea.ClearScreen()` (chat_tui.go:859, Warp-era workaround) — conflicts with bubbletea v2's cell-diff renderer; Claude Code's #35580 lesson: a clear (`CSI 2J`) inside a sync block makes the scroll jump |
| Bottom panels render twice per frame | `bottomRows()` and `View()` each call every `render*` panel (View runs on the 60fps render loop) |
| No motion gate | Spinner, tool frames, elapsed all animate unconditionally; no reduced-motion path |
| Scroll is instant-jump | No smooth-scroll interpolation (bubbletea v2 viewport has none built in) |
| Elapsed counters jitter in width | `%ds` formats shift a column every 10s (Qwen #6533 lesson: fixed-width elapsed) |
| Art gaps | First-screen banner is bare; table code cells may carry full syntax colors; diff colors are hardcoded 256-color escapes; no discipline test forbidding hardcoded colors |

**Not the problem:** missing runtime power, keybindings, or layout architecture. The renderer (bubbletea v2 `cursedRenderer`: cell-level diff, DEC 2026 sync output auto-negotiated, 60fps render loop) is capable; the work is in how the app feeds it.

---

## 2. Goals and non-goals

### Goals

1. **Fluidity first**: fix the measured hot paths (incremental wrap, scroll repaint, panel double-render) so 10k-line sessions stream and scroll smoothly.
2. **Motion discipline**: a single reduced-motion gate all animations must pass; a small, deliberate animation set (smooth scroll, fixed-width elapsed); no decorative animation without function.
3. **Art polish**: width-gated first-screen branding, density consistency, neutral table code cells, semantic color discipline enforced by test.
4. **Regression safety**: benchmarks + property tests + the existing suite stay green; every task is TDD.

### Non-goals (this phase)

1. Scrollback externalization / visible-window virtualization (Claude flat-memory / Codex native-scrollback route) — a later phase.
2. Replacing the bubbletea renderer or adopting a full-screen buffer rewrite (program spec §2 non-goal 5 stays).
3. Real-time fancy animation: gradients, skeleton screens, panel transitions (Qwen's explicit finding: terminals cannot render these well).
4. Replacing the 12-line live thinking tail with Qwen's fixed 1-line header — the live tail is an existing product feature (Ctrl+O toggles verbose).
5. P2 Tasks overlay, P3 statusline schema / palette registry, P4 remainder (copy reliability, doc parity).
6. New theme names, user keymap files, or new config sections.

---

## 3. Design principles (hard rules)

1. **Fluidity is a correctness property**: each frame must do O(changed) work in the hot path, not O(transcript).
2. **Animation is opt-out**: every animated component consults `motionEnabled()`; reduce-motion mode means static/instant, never a different feature.
3. **Information before decoration**: elapsed time updates every second even with motion off; shimmer is the only decoration and stays spike-gated.
4. **One render pass per Update for panels**: `View()` (render loop, up to 60/s) never re-renders bottom panels; they are refreshed once per Update.
5. **Keyboard invariants unchanged**: smooth scroll must not break tail-follow, `AtBottom`, edge auto-scroll, or Esc behavior.
6. **Reuse runtime truth and existing theme slots**: no new color system; migrate hardcoded colors into the theme.
7. **Default conservative**: the only default-behavior changes are the measured fixes (no ClearScreen on scroll, incremental wrap, single panel pass); everything visual is additive.

---

## 4. Fluidity foundation

### 4.1 Scroll repaint (default off)

**Change:** stop returning `tea.ClearScreen()` when the viewport offset moves. Keep the legacy behavior behind an env switch.

- New field `scrollRepaint bool` on `chatTUI`; read once at startup from env `REASONIX_TUI_SCROLL_REPAINT=1` (cli.go, same place other env toggles are read).
- Update wrapper: the ClearScreen branch becomes
  `if cm.viewport.YOffset() != prevYOff && !cm.nativeScrollback && !cm.sessionSwitch && cm.scrollRepaint`.
- Native scrollback path is unchanged (no clear there today).

**Why safe:** bubbletea v2.0.7 `cursedRenderer` diffs at cell level and writes only dirty lines; the workaround predates/duplicates v1-era scroll-region optimization concerns. Claude Code's renderer rewrite treats full clears during updates as a bug (issue #35580: `CSI 2J` inside a sync block jumps the scroll).

**Mitigation:** the env switch is the documented escape hatch for terminals that strand stale rows. Manual verification on the user's terminal + known-problem terminals is part of the plan's acceptance.

### 4.2 Incremental wrap cache

**Change:** replace whole-transcript re-wrap per content growth with a per-block wrapped-line cache.

- `wrapBlock(rendered string, width int) []string` — wraps one SGR-balanced transcript block (same lipgloss width render as today's `wrapTranscript`, but per block).
- New fields on `chatTUI`: `wrappedLines []string` (flat, in display order) and `wrappedBlockOffsets []int` (start index of each block; `len == nBlocks+1`, last = total).
- Operations:
  - **Append** (content grew): wrap only the new block(s), append lines + offset. O(new block).
  - **Set block i** (live tool/reasoning tail, streaming): re-wrap only block i and patch `offsets[i+1:]` by the line-count delta. The streaming hot path is always the last block → O(block); middle-block sets are rare and allowed to be O(n) for the offset patch.
  - **Remove/truncate** (compaction, collapse): truncate `wrappedLines` at `offsets[L]` and truncate the offset array. O(1) after offsets are right.
  - **Width change**: full rebuild via `rebuildWrappedLines(contentW)` reusing `wrapBlock` per block (reflow path, rare).
- `viewport.SetContent(strings.Join(wrappedLines, "\n"))` remains; join is O(total bytes) memcpy (sub-ms at 10k×80), acceptable and benchmarked.
- `cm.wrappedLines` (existing field) stays as the flat slice; remove the `strings.Split` of the whole wrapped content.

**Equivalence:** a property test asserts incremental construction equals full `wrapTranscript` for the same transcript sequence (including set/remove/truncate interleavings).

### 4.3 Bottom panels single pass

**Change:** render all bottom panels once per Update; `bottomRows()` and `View()` consume the cache.

- New `bottom_panels.go`: `bottomPanels` struct holding the rendered strings + total rows for: todo panel, approval banner, chooser, rewind, MCP import, resume picker, quick picker, copy picker, cheatsheet, completion, main manager (native scrollback only).
- `chatTUI.refreshBottomPanels()` runs at the end of the Update wrapper (after `m.update`), using the same width as `View()`; both `bottomRows()` and `View()` read the cache.
- Because Update precedes every rendered frame, the cache is never stale across events; no per-field invalidation bookkeeping is needed.
- `computeStatusLineCount`/`transcriptHeight`/`View` lockstep is preserved by construction (all read the same cache).

**Why safe:** Update runs per event; View runs up to 60/s on the render loop. Rendering panels once per Update removes the render-loop duplication without inventing fine-grained dirty tracking.

---

## 5. Motion & art

### 5.1 Reduced-motion gate

- New `motion.go`: `motionEnabled() bool` = env `REASONIX_REDUCE_MOTION=1` (no config section exists; env only, YAGNI).
- Consumers (must consult the gate):
  - Spinner scheduling: when motion off, the working line shows a static glyph and does **not** schedule `spinner.Tick` (elapsed ticker still runs — it is information).
  - Smooth scroll (§5.2): motion off → instant jump (today's behavior).
  - Shimmer (§5.3, if it ships): off.
- Test: enumerate every animation entry point and assert it consults the gate; plus a behavioral test that with `REASONIX_REDUCE_MOTION=1` no `spinner.Tick`/scroll-tick is scheduled.

### 5.2 Smooth scroll interpolation

- New `smooth_scroll.go`: small state machine `{active, from, to, start, dur}` driven by a 16ms `tea.Tick`.
- Inputs: PgUp/PgDn (page) and wheel up/down (3 lines) start an interpolation from the current `YOffset` to the target, clamped to the viewport bounds; each tick sets `viewport.SetYOffset(lerp)` and re-arms the tick until done.
- **Instant exceptions** (no animation): `GotoTop`/`GotoBottom` (incl. tail-follow), edge auto-scroll while mouse-held, and any scroll input while `!motionEnabled()`.
- **Interrupt**: a new scroll input during an animation cancels it and starts from the current offset.
- AtBottom semantics: during animation the viewport is not at bottom; tail-follow is unaffected (only triggers when at bottom).
- Duration 120–180ms, ease-out; tick interval 16ms aligns with the render loop.
- No behavior change to Esc, draft, or keyboard completeness.

### 5.3 Shimmer (spike-gated, optional)

- Working-line text single-pass sweep using a `Blend1D`-precomputed color table (lipgloss v2 has no animated gradient API).
- **Gate:** stays out of the formal scope unless the spike A/B shows clear feel gain; always gated by `motionEnabled()`.

### 5.4 Fixed-width elapsed

- `formatElapsedFixed(sec int) string` — width-4 right-aligned seconds: `"  3s"`, `" 12s"`, `"123s"`, `"999s"` (values ≥ 1000 clamp to `"999s"`). No column jitter.
- Applied to the working line (`ChatStatusThinkingFmt`, `ChatStatusCancellingFmt`, `ChatStatusRetryingFmt`) and the tool working line (`ChatToolWorkingFmt`): the seconds argument of each fmt string switches from `%d` to `%s` (all three locales) and callers pass `formatElapsedFixed(m.elapsed)`.
- Update all three locales (`messages_en/zh/zh_tw`).
- Turn receipt (`renderTurnReceipt`) shows no elapsed today — unchanged.

### 5.5 First-screen branding (width-gated)

- `renderTUIBanner`: wide (≥60 cols) keeps today's two lines (`◆ reasonix · label` + tip); narrow (<60 cols) renders a single trimmed wordmark line (no tip). Accent + bold wordmark reuse existing theme slots; static, no animation.

### 5.6 Density audit

- Audit `md.go`/`toolcard.go`/`chat_tui.go` for double blank lines or inconsistent margins between blocks (tool cards, thinking tail, user bubble, receipts); fix inconsistencies found.
- `commitSpacer` already guarantees a single blank line between blocks; add a regression test that a mixed-session transcript never contains two consecutive blank lines.

### 5.7 Table code cells neutral

- Verify how code spans inside markdown tables render today; if syntax-highlighted, render table code cells as monospace + neutral color (theme `faint`/`muted`), leaving non-table code highlighting unchanged.
- Test: a table containing a code span renders without syntax color codes in that cell.

### 5.8 Semantic color discipline

- Migrate the only hardcoded SGR colors in `internal/cli` (`diffview.go` bgDiffAdd/bgDiffDel/fgDiffAdd/fgDiffDel) to theme slots: bg → existing `diffAddBG`/`diffDelBG`, fg → `success`/`err`.
- New `TestNoHardcodedColorCodes`: scan `internal/cli` (excluding `theme.go`, `style.go`, `*_test.go`) and assert no SGR color sequences (`38;5`, `48;5`, `38;2`, `3x`, `4x`, `9x`, `10x`) appear in string literals.

---

## 6. Testing

- **Benchmarks** (kept + added): `BenchmarkWrapTranscript` (reflow path, unchanged); `BenchmarkAppendBlock` (append N lines at 10k base); optionally `BenchmarkBottomPanels`. Benchmarks are reports, not assertions.
- **Property test**: incremental wrappedLines == full wrapTranscript equivalence (append/set/remove/truncate interleavings).
- **Scroll repaint**: default no ClearScreen on scroll; env `REASONIX_TUI_SCROLL_REPAINT=1` restores; native scrollback unchanged.
- **Panel cache**: with a render-counting harness, each panel renders once per Update; `bottomRows()` == cached rows; resize refresh.
- **Motion gate**: every animation entry consults `motionEnabled()`; `REASONIX_REDUCE_MOTION=1` schedules no spinner/scroll ticks.
- **Smooth scroll**: start/end states, tick progression, interrupt mid-flight, instant exceptions (GotoBottom, edge auto-scroll), motion-off instant jump, AtBottom during animation.
- **Fixed-width elapsed**: widths stable for 0–999s; both fmt call sites.
- **Branding**: wide ≥60 and narrow <60 variants; tip present/absent.
- **Density**: no double blank lines in mixed transcript.
- **Table cells**: code span in table → neutral, no syntax colors.
- **Color discipline**: `TestNoHardcodedColorCodes` green after diffview migration.
- **Existing suite**: `go test ./internal/cli/ -count=1` stays green (2.9s baseline).

---

## 7. Delivery

- **Spec:** this document; the program spec stays the roadmap (P4 items partially pulled forward into P1.5).
- **File map (indicative):**
  - `internal/cli/motion.go` (new) — gate + tests
  - `internal/cli/smooth_scroll.go` (new) — state machine + tests
  - `internal/cli/bottom_panels.go` (new) — cache + refresh + tests
  - `internal/cli/transcript.go` — `wrapBlock`, wrappedLines cache ops, `rebuildWrappedLines` + tests
  - `internal/cli/chat_tui.go` — `scrollRepaint` field + Update wiring, panel refresh, smooth-scroll key wiring, elapsed call sites, banner wide/narrow
  - `internal/cli/status_footer.go` — `formatElapsedFixed` + tests
  - `internal/cli/md.go` — table code cell neutral + test
  - `internal/cli/diffview.go` + `internal/cli/theme.go` — diff color migration
  - `internal/cli/bench_test.go` — keep + `BenchmarkAppendBlock`
  - `internal/i18n/{messages_en,messages_zh,messages_zh_tw}.go` — elapsed fmt `%s`
  - `internal/cli/cli.go` — env flag reads
- **Process:** TDD per task (failing test → implement → pass → commit); small commits; feature branch/worktree; then one implementation plan via `writing-plans`.
- **Research artifacts:** `docs/superpowers/research/2026-08-06-render-animation/` + `internal/cli/bench_test.go` committed with this spec.

---

## 8. Success criteria

| Scenario | Pass |
|----------|------|
| Streaming at 10k lines | Append cost ~58ms → <2ms per token (benchmark); no visible jank in user terminal |
| Scroll | No ClearScreen by default; env fallback restores; smooth interpolation on PgUp/PgDn/wheel; GotoBottom/auto-scroll instant |
| Bottom region | Panels render once per Update; height/View lockstep tests green |
| Reduce motion | `REASONIX_REDUCE_MOTION=1` → static spinner, instant scroll, no shimmer; elapsed still ticks |
| Art | Wide/narrow branding; neutral table code cells; diff colors themed; `TestNoHardcodedColorCodes` green |
| Regression | Full `internal/cli` suite green; keyboard/Esc/draft invariants unchanged |

---

## 9. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Removing ClearScreen strands rows on some terminals | env `REASONIX_TUI_SCROLL_REPAINT=1`; document; verify on user terminal + Warp-style list |
| wrappedLines cache bugs (offset drift) | property equivalence test; O(1) truncate via offsets; last-block fast path |
| Smooth scroll breaks tail-follow / AtBottom | instant exceptions + dedicated tests |
| Panel cache staleness | refresh once per Update (before any render) — no per-field dirty tracking |
| i18n fmt change breaks locales | update all three message files in the same task; locale tests |
| Shimmer feels cheap | spike A/B gate; motion gate; drop if not clearly better |

---

## 10. Decision log (brainstorming)

| # | Topic | Choice |
|---|-------|--------|
| 1 | Round direction | C — dual track: fluidity foundation + curated motion/art |
| 2 | Scroll repaint | Default off; env `REASONIX_TUI_SCROLL_REPAINT=1` legacy |
| 3 | Wrap strategy | Per-block incremental cache + offsets; property-tested |
| 4 | Panel rendering | Once per Update cache; View/bottomRows read it |
| 5 | Motion gate | env `REASONIX_REDUCE_MOTION=1`; all animation entry points tested |
| 6 | Smooth scroll | 120–180ms ease-out, 16ms tick; GotoBottom/auto-scroll instant; interruptible |
| 7 | Shimmer | Spike-gated; not in formal scope unless A/B wins |
| 8 | Thinking tail | Keep 12-line live tail; only elapsed width fixed |
| 9 | Elapsed | `formatElapsedFixed` width-4; working + tool lines |
| 10 | Branding | Width-gated ≥60 cols; static |
| 11 | Diff colors | Migrate to theme slots (diffAddBG/diffDelBG + success/err) |
| 12 | Color discipline | `TestNoHardcodedColorCodes` after migration |
| 13 | P2/P3/P4 | Unchanged roadmap; P4 perf items pulled forward |

---

## 11. Next step

1. User review gate for this spec.
2. Invoke `superpowers:writing-plans` to produce the P1.5 implementation plan (three tracks: fluidity / motion / art).
3. Implement via plan (subagent-driven or executing-plans) after plan acceptance.
