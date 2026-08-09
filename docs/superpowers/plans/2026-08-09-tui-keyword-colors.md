# TUI Keyword Colors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add restrained semantic colors to Corvus tool cards and assistant prose while preserving Codex-style transparent composer/history rendering.

**Architecture:** Reuse the existing `cliPalette` semantic slots. Tool cards get a small verb-to-color mapping; Markdown gets a pure, budgeted highlighter for ordinary AST text nodes, while code spans/fences remain on their existing paths. Existing transparent composer and user-message behavior is locked with cross-theme regression tests.

**Tech Stack:** Go, Bubble Tea v2, Lip Gloss v2, Goldmark, `charmbracelet/x/ansi`, existing `internal/cli` test helpers.

**Spec:** `docs/superpowers/specs/2026-08-09-tui-keyword-colors-design.md`

---

### Task 1: Color tool verbs by semantic action

**Files:**
- Modify: `internal/cli/toolcard.go`
- Test: `internal/cli/toolcard_test.go`

- [ ] **Step 1: Write failing semantic-color tests.**

Add a table-driven test using `colorprofile.ANSI256` and `configureCLITheme("dark")`. Assert that the rendered label contains the expected foreground SGR while its visible text remains unchanged:

```go
func TestToolCardSemanticVerbColors(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	cases := []struct {
		name, args string
		color     cliColor
		label     string
	}{
		{"read", `{"path":"theme.go"}`, activeCLITheme.info, "Explored"},
		{"edit", `{"path":"theme.go"}`, activeCLITheme.success, "Edited"},
		{"run", `{"command":"go test ./internal/cli"}`, activeCLITheme.warn, "Ran"},
		{"mcp", `{"capability_id":"browser"}`, activeCLITheme.secondary, "MCP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolCard(map[string]string{
				"read": "read_file", "edit": "edit_file", "run": "bash", "mcp": "use_capability",
			}[tc.name], tc.args, 80)
			if !strings.Contains(ansi.Strip(got), tc.label) {
				t.Fatalf("missing %q in %q", tc.label, ansi.Strip(got))
			}
			if !strings.Contains(got, fgSGR(tc.color)+tc.label) {
				t.Fatalf("%q is not colored with %v: %q", tc.label, tc.color, got)
			}
		})
	}
}
```

The test should also assert that the ordinary argument text does not receive a background SGR.

- [ ] **Step 2: Run the focused test and confirm the current implementation fails.**

Run: `go test ./internal/cli -run TestToolCardSemanticVerbColors -count=1`

Expected: FAIL because `Edited`, `Ran`, and generic tool labels are bold/default rather than mapped semantic colors.

- [ ] **Step 3: Add one mapping and apply it only to labels.**

Add this helper beside the existing tool classification tables:

```go
func toolVerbColor(name string) cliColor {
	switch {
	case name == "bash":
		return activeCLITheme.warn
	case isExploreCoalesceTool(name):
		return activeCLITheme.info
	case isWriteTool(name):
		return activeCLITheme.success
	case name == "task" || name == "use_capability" || toolCategory[name] == "proc":
		return activeCLITheme.secondary
	default:
		return activeCLITheme.muted
	}
}
```

Use `themeFg(toolVerbColor(name), bold(label))` for ordinary and edited cards, color the `Explored` heading with `info`, and color the `Ran` label in `bashToolCard` with `warn`. Leave `toolBullet`, tree gutters, args, and existing outcome marks unchanged.

- [ ] **Step 4: Run tool-card tests and the color-discipline guard.**

Run: `go test ./internal/cli -run 'TestToolCard|TestColorDiscipline' -count=1`

Expected: PASS with no new hardcoded SGR.

- [ ] **Step 5: Commit the isolated tool-card change.**

```bash
git add internal/cli/toolcard.go internal/cli/toolcard_test.go
git commit -m "style(cli): color tool verbs by semantic action"
```

### Task 2: Build the restrained prose keyword highlighter

**Files:**
- Create: `internal/cli/md_keywords.go`
- Create: `internal/cli/md_keywords_test.go`

- [ ] **Step 1: Write failing pure-function tests.**

Cover semantic categories, exact visible text, Chinese matches, structure matches, duplicate suppression, and the four-match budget:

```go
func TestHighlightProseTextUsesSemanticColorsAndPreservesText(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	budget := newProseHighlightBudget()
	input := "The renderer parsed the cache and passed the API check."
	got := highlightProseText(input, &budget)
	if plain := ansi.Strip(got); plain != input {
		t.Fatalf("visible text changed: %q", plain)
	}
	for _, want := range []string{
		fgSGR(activeCLITheme.secondary) + "renderer",
		fgSGR(activeCLITheme.secondary) + "cache",
		fgSGR(activeCLITheme.success) + "passed",
		fgSGR(activeCLITheme.secondary) + "API",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing semantic color %q in %q", want, got)
		}
	}
}

func TestHighlightProseTextCapsMatchesAndDeduplicates(t *testing.T) {
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	budget := newProseHighlightBudget()
	got := highlightProseText("renderer parser cache API TUI model tool renderer parser", &budget)
	if gotCount := strings.Count(got, "\x1b["); gotCount > 8 {
		t.Fatalf("more than four colored fragments emitted: %q", got)
	}
	if strings.Count(got, fgSGR(activeCLITheme.secondary)+"renderer") != 1 {
		t.Fatalf("duplicate renderer should be plain after first match: %q", got)
	}
}
```

Add cases for `internal/cli/md.go`, `renderMarkdown()`, `Function()`, `pkg.Symbol`, `通过`, and `失败`; assert that punctuation and CJK width are preserved.

- [ ] **Step 2: Run the new helper tests and confirm they fail before the helper exists.**

Run: `go test ./internal/cli -run 'TestHighlightProseText' -count=1`

Expected: FAIL with undefined helper errors or missing expected SGRs.

- [ ] **Step 3: Implement a static, bounded matcher.**

Create `md_keywords.go` with these stable interfaces:

```go
const maxProseHighlights = 4

type proseHighlightBudget struct {
	remaining int
	seen      map[string]struct{}
}

func newProseHighlightBudget() proseHighlightBudget {
	return proseHighlightBudget{remaining: maxProseHighlights, seen: make(map[string]struct{})}
}

func highlightProseText(text string, budget *proseHighlightBudget) string
```

Use a static keyword table with the design categories (`secondary`, `success`, `danger`, `warn`) and a compiled structural matcher for paths, command names, `Identifier()`, and `pkg.Symbol`. Scan matches left-to-right, require ASCII token boundaries for English, compare Chinese entries directly, skip a repeated normalized keyword, and decrement `remaining` only when a fragment is actually colored. Render each fragment through `themeFg`; return the original string when colors are disabled, the budget is empty, or no match exists. Keep the matcher independent from Goldmark so it can be tested without parsing Markdown.

- [ ] **Step 4: Re-run pure helper tests in both color profiles.**

Run: `go test ./internal/cli -run 'TestHighlightProseText' -count=1`

Expected: PASS for dark ANSI 256, light ANSI 256, and `NO_COLOR` cases.

- [ ] **Step 5: Commit the pure highlighter.**

```bash
git add internal/cli/md_keywords.go internal/cli/md_keywords_test.go
git commit -m "feat(cli): add restrained prose keyword highlighter"
```

### Task 3: Integrate prose highlighting into Markdown AST rendering

**Files:**
- Modify: `internal/cli/md.go`
- Test: `internal/cli/md_test.go`

- [ ] **Step 1: Write failing Markdown integration tests.**

Add tests that distinguish ordinary text from inline/fenced code, reset the budget per paragraph, preserve visible width, and keep copied text unchanged:

```go
func TestMarkdownProseKeywordsSkipCodeSpansAndFences(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	got := newMarkdownRenderer(80).Render("The renderer passed; use `renderer` here.\n\n```go\nrenderer()\n```")
	if strings.Count(got, fgSGR(activeCLITheme.secondary)+"renderer") != 1 {
		t.Fatalf("ordinary prose should be the only dictionary match: %q", got)
	}
	if !strings.Contains(got, fgSGR(activeCLITheme.accent)+"renderer") {
		t.Fatalf("inline code should retain existing accent styling: %q", got)
	}
}

func TestMarkdownProseKeywordBudgetResetsPerParagraph(t *testing.T) {
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	got := newMarkdownRenderer(80).Render("renderer parser cache API TUI model tool.\n\nrenderer is ready.")
	if strings.Count(got, fgSGR(activeCLITheme.secondary)+"renderer") != 2 {
		t.Fatalf("each paragraph should get a fresh budget: %q", got)
	}
}

func TestMarkdownProseHighlightDoesNotChangeWidthOrCopyText(t *testing.T) {
	activeColorProfile = colorprofile.ANSI256
	r := newMarkdownRenderer(18)
	input := "renderer 通过 internal/cli/md.go"
	view := r.Render(input)
	for _, line := range strings.Split(strings.TrimRight(view, "\n"), "\n") {
		if got := visibleWidth(line); got > 18 {
			t.Fatalf("line width = %d > 18: %q", got, ansi.Strip(line))
		}
	}
	if plain := ansi.Strip(r.RenderCopy(input, "copy")); plain != ansi.Strip(view) {
		t.Fatalf("copy/render visible text differ: %q vs %q", plain, ansi.Strip(view))
	}
}
```

- [ ] **Step 2: Run the integration tests and confirm ordinary prose is currently uncolored.**

Run: `go test ./internal/cli -run 'TestMarkdownProse' -count=1`

Expected: FAIL because only inline code and fenced code currently receive token colors.

- [ ] **Step 3: Add a per-render/per-block budget and invoke the helper only for plain AST text.**

Add value fields `proseBudget proseHighlightBudget` and `proseEnabled bool` to `mdRenderer`. Initialize `proseBudget = newProseHighlightBudget()` at the start of `Render` and `RenderCopy`. Add a small `collectProseInline` wrapper that sets `proseEnabled`, resets the budget, calls `collectInline`, and restores the previous flag. Use that wrapper from paragraph/text-block rendering and from the marker-line inline path in `renderList`; leave headings, tables, links, blockquotes, code spans, and fenced code on the existing plain collector. In `appendInline`, replace the plain `ast.Text` write with:

```go
text := string(v.Segment.Value(src))
if r.proseEnabled {
	b.WriteString(highlightProseText(text, &r.proseBudget))
} else {
	b.WriteString(text)
}
```

Keep the existing hard/soft line-break handling after this write. Do not call the helper from `ast.CodeSpan`, fenced code, link destinations, table code-span paths, or copy markers. If a value receiver is preferable to avoid pointer churn, preserve the same observable four-fragment budget and reset boundaries.

- [ ] **Step 4: Run Markdown tests and the full CLI package.**

Run: `go test ./internal/cli -run 'TestMarkdownProse|TestRender|TestHighlightCodeLine|TestTable|TestInlineCode' -count=1`

Expected: PASS; then run `go test ./internal/cli -count=1` and expect the complete package to pass.

- [ ] **Step 5: Commit Markdown integration.**

```bash
git add internal/cli/md.go internal/cli/md_test.go
git commit -m "feat(cli): highlight restrained technical prose tokens"
```

### Task 4: Lock transparent composer and history behavior across themes

**Files:**
- Modify: `internal/cli/composer_selection_test.go`
- Modify: `internal/cli/chat_tui_test.go`

- [ ] **Step 1: Add light/TrueColor parameterized regression assertions.**

Extend the existing `TestComposerFieldStaysTransparent` and `TestUserBubbleIsLightweightTranscriptLine` cases to run for dark/light and ANSI256/TrueColor. For every case assert:

```go
if strings.Contains(rendered, bgSGR(activeCLITheme.inputBoxBG)) ||
	strings.Contains(rendered, bgSGR(activeCLITheme.userBubbleBG)) {
	t.Fatalf("transparent rendering emitted a surface background: %q", rendered)
}
```

Also assert one visible user row, preserved textarea selection SGR, and exact passthrough under `colorprofile.NoTTY`.

- [ ] **Step 2: Run the focused transparency suite.**

Run: `go test ./internal/cli -run 'TestComposerField|TestUserBubble' -count=1`

Expected: PASS without production changes; if any case fails, keep the current transparent renderer and correct only the regression uncovered by the test.

- [ ] **Step 3: Commit the cross-theme regression coverage.**

```bash
git add internal/cli/composer_selection_test.go internal/cli/chat_tui_test.go
git commit -m "test(cli): lock transparent composer and user history"
```

### Task 5: Independent validation and real TUI acceptance

**Files:**
- Test/evidence only; modify source only if a preceding test exposes a defect.

- [ ] **Step 1: Ask Claude CLI to execute Tasks 1-4 from this plan in the repository.**

Use a non-interactive prompt that includes the spec and plan paths, requires TDD order, forbids unrelated refactors, and asks Claude to report changed files and test output. Before dispatch, confirm the CLI is installed with `command -v claude` and record its version.

- [ ] **Step 2: Review Claude's diff before accepting it.**

Run:

```bash
git diff --check
git diff --stat
git diff -- internal/cli/toolcard.go internal/cli/md.go internal/cli/md_keywords.go
```

Reject unrelated theme rewrites, hardcoded SGR/hex values outside the theme system, broad regex coloring of ordinary words, any background painting in composer/history, and changes to execution/state logic.

- [ ] **Step 3: Run focused and package tests.**

```bash
go test ./internal/cli -run 'TestToolCardSemanticVerbColors|TestHighlightProseText|TestMarkdownProse|TestComposerField|TestUserBubble' -count=1
go test ./internal/cli -count=1
go build -o /tmp/corvus-tui-keyword-colors ./cmd/corvus
```

Expected: all focused/package tests pass and the binary builds.

- [ ] **Step 4: Run the repository baseline and classify unrelated failures.**

Run: `go test ./... -count=1`

Record failures outside `internal/cli` separately; do not modify unrelated packages to make this visual change pass.

- [ ] **Step 5: Exercise the real binary at two viewport sizes.**

Use a PTY at `80x30` and `40x14` with a fixture conversation containing `Read`, `Edited`, `Ran`, `MCP`, `PASS`, `Error`, paths, `Function()`, `renderer`, and one inline/fenced code example. Verify ANSI-stripped line widths, no composer/history background SGR, readable colors, and no rainbow-like consecutive fragments.

- [ ] **Step 6: Capture final evidence and report acceptance.**

Run `git status --short`, `git diff --check`, and the focused/package tests again after any visual-only correction. Report the exact commit(s), commands, pass/fail output, visual sizes checked, and any pre-existing repository failures.
