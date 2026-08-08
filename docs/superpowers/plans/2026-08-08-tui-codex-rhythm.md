# TUI Codex Rhythm Pack Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Codex-faithful transcript rhythm: weight hierarchy, single-`└` Explored trees, user soft-bg bubbles, assistant `•` anchors, Ran preview + `✓`/`✗`, gap control, turn `─` separators, one-line banner — per `docs/superpowers/specs/2026-08-08-tui-codex-rhythm-design.md`.

**Architecture:** Render-layer only in `internal/cli`. Pure helpers for separators/cards/bubbles; gap + `hadWorkActivity` on `chatTUI` ingest; no agent/protocol changes.

**Tech Stack:** Go, Bubble Tea v2, existing `theme.go` / ANSI helpers (`bgSGR`, `dim`, `bold`, `completionPadCell`).

**Spec:** `docs/superpowers/specs/2026-08-08-tui-codex-rhythm-design.md`

## Global Constraints

- No changes under `internal/agent/*`, `internal/tool/*`, providers
- Primary markers remain `›` / `•`; add structure (`└` `│`), outcome (`✓` `✗`), rule (`─`)
- Explored: one hanging `└`, no sibling `├`
- Ran default preview ≤5 lines; Ctrl+B still expands full output
- Turn `─` only when `hadWorkActivity`; Worked for label only if elapsed >60s
- Footer stays thin `interaction` + `model · path` (no redesign)
- Banner single line; drop tip
- Live and replay share the same gap helpers
- Prefer TDD: failing test → minimal code → pass → commit per task

## File map

| File | Responsibility |
|------|----------------|
| `internal/cli/gap.go` (new) | Pure blank-line helpers if extracted; else keep on `chatTUI` |
| `internal/cli/separators.go` (new) | `finalMessageSeparator(width, elapsedSec int) string` |
| `internal/cli/toolcard.go` | Explored nest, Ran `│`/`└`, preview/outcome strings |
| `internal/cli/transcript.go` | Assistant `• ` first-line gutter |
| `internal/cli/chat_tui.go` | `ensureBlank`/`commitSpacer`, `hadWorkActivity`, TurnDone separator, collapse→preview, banner, working cleanup |
| `internal/cli/theme.go` | Optional user-bubble bg blend from probe |
| `internal/cli/*_test.go` | Unit coverage for each contract |

---

### Task 1: Gap helpers (no double blank)

**Files:**
- Modify: `internal/cli/chat_tui.go` (`commitSpacer` ~2037)
- Test: `internal/cli/transcript_test.go` (`TestCommitSpacerNeverDoubleSpaces` + new cases)

**Interfaces:**
- Produces: `func (m *chatTUI) ensureBlank()` — alias or rename of `commitSpacer` with same semantics; keep `commitSpacer` as thin wrapper calling `ensureBlank` so call sites need not all change in this task
- Consumes: `commitLine`, `m.transcript`

- [ ] **Step 1: Write the failing test**

Add to `transcript_test.go`:

```go
func TestEnsureBlankInsertsSingleBlankBetweenCells(t *testing.T) {
	m := newTestChatTUI()
	m.commitLine("cell-a")
	m.ensureBlank()
	m.commitLine("cell-b")
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "cell-a\n\ncell-b") {
		t.Fatalf("want exactly one blank between cells, got %q", m.transcript)
	}
	if strings.Contains(joined, "\n\n\n") {
		t.Fatalf("double blank: %q", m.transcript)
	}
}

func TestEnsureBlankNoOpWhenAlreadyBlank(t *testing.T) {
	m := newTestChatTUI()
	m.commitLine("a")
	m.commitLine("")
	n := len(m.transcript)
	m.ensureBlank()
	if len(m.transcript) != n {
		t.Fatalf("ensureBlank must not add second blank, got %v", m.transcript)
	}
}

func TestEnsureBlankNoOpOnEmptyTranscript(t *testing.T) {
	m := newTestChatTUI()
	m.ensureBlank()
	if len(m.transcript) != 0 {
		t.Fatalf("empty transcript should stay empty, got %v", m.transcript)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestEnsureBlank' -count=1`

Expected: FAIL (`ensureBlank` undefined) or FAIL if only wrapper missing.

- [ ] **Step 3: Minimal implementation**

In `chat_tui.go` next to `commitSpacer`:

```go
// ensureBlank guarantees a single blank line before the next cell.
// No-op at top of transcript or when a blank already trails.
func (m *chatTUI) ensureBlank() {
	if n := len(m.transcript); n > 0 && strings.TrimSpace(m.transcript[n-1]) != "" {
		m.commitLine("")
	}
}

func (m *chatTUI) commitSpacer() { m.ensureBlank() }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestEnsureBlank|TestCommitSpacer' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/transcript_test.go
git commit -m "feat(cli): ensureBlank gap helper for transcript rhythm"
```

---

### Task 2: Assistant `•` first line + one-line banner

**Files:**
- Modify: `internal/cli/transcript.go` (`assistantBlock`, `assistantBodyWidth`, ~325–393)
- Modify: `internal/cli/chat_tui.go` (`renderTUIBanner` ~4936)
- Test: `internal/cli/transcript_test.go`, `internal/cli/chat_render_test.go` (`TestRenderTUIBannerWideAndNarrow`)

**Interfaces:**
- Produces: assistant first line starts with dim `• ` (visible strip: `"• "`); continuations `"  "`
- Produces: banner = single line containing `corvus` and model; no `ChatTip` text

- [ ] **Step 1: Write/update failing tests**

Update `TestAssistantMarkdownHasIdentityAndIndentedBody` and `TestAssistantMarkdownHistoryDropsName` / replay tests to expect `•` prefix:

```go
// first line: "• A concise..." (may have ANSI when color on)
plain0 := ansi.Strip(lines[0])
if !strings.HasPrefix(plain0, "• ") {
	t.Fatalf("assistant first row should start with • , got %q", plain0)
}
for i, line := range lines[1:] {
	if line != "" && !strings.HasPrefix(line, "  ") {
		t.Fatalf("continuation row %d should use two spaces, got %q", i+1, line)
	}
}
```

Banner test (extend existing or add):

```go
func TestRenderTUIBannerSingleLineNoTip(t *testing.T) {
	got := ansi.Strip(renderTUIBanner("deepseek-v4-flash", "", 100))
	if strings.Count(strings.TrimRight(got, "\n"), "\n") != 0 {
		t.Fatalf("banner must be one line, got %q", got)
	}
	if strings.Contains(got, i18n.M.ChatTip) {
		t.Fatalf("banner must not include tip, got %q", got)
	}
	if !strings.Contains(got, "corvus") || !strings.Contains(got, "deepseek-v4-flash") {
		t.Fatalf("banner missing wordmark/model: %q", got)
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

Run: `go test ./internal/cli/ -run 'TestAssistantMarkdown|TestRenderTUIBanner|TestReplaySectionsKeepAssistant' -count=1`

- [ ] **Step 3: Implement**

`transcript.go` — change gutter constants and `assistantBlock`:

```go
const (
	assistantBulletPrefix = "• " // dim applied at render
	assistantContIndent   = "  "
)

func assistantBlock(body, indent string, named bool) string {
	_ = named
	_ = indent // use fixed Codex gutters
	lines := strings.Split(body, "\n")
	// skip leading blank lines...
	firstLine := dim(assistantBulletPrefix) + lines[first]
	// remaining: assistantContIndent + line
}
```

Adjust `assistantBodyWidth` to reserve `visibleWidth("• ")` == 2.

`renderTUIBanner`: remove the tip `WriteString` branch; always one wordmark line (truncate on narrow width as today).

- [ ] **Step 4: Run tests — PASS**

Run: `go test ./internal/cli/ -run 'TestAssistantMarkdown|TestRenderTUIBanner|TestReplay|TestTranscript' -count=1`

Fix any identity/copy tests that still assert `"  Live answer"` without bullet.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/transcript.go internal/cli/chat_tui.go internal/cli/*_test.go
git commit -m "style(cli): assistant • anchor and single-line session banner"
```

---

### Task 3: User bubble soft full-line background

**Files:**
- Modify: `internal/cli/chat_tui.go` (`renderUserBubble` ~4959)
- Optional: `internal/cli/theme.go` (probe blend for `userBubbleBG`)
- Test: `internal/cli/transcript_test.go`, `internal/cli/chat_tui_test.go` (`TestUserBubbleIsLightweightTranscriptLine`)

**Interfaces:**
- Produces: `renderUserBubble(line, width, planMode, current) string` multi-line block with pad rows when `colorOn()`
- Color off: plain `› text` (no leading `│`)

- [ ] **Step 1: Failing tests**

```go
func TestUserBubbleFullLineBackgroundWhenColorOn(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.TrueColor
	configureCLITheme("dark")
	// ensure theme has userBubbleBG
	got := renderUserBubble("hello rhythm", 40, false, true)
	if !strings.Contains(got, bgSGR(activeCLITheme.userBubbleBG)) {
		t.Fatalf("want userBubbleBG on bubble, got %q", got)
	}
	if !strings.Contains(got, completionPadCell) {
		t.Fatalf("want NBSP pad for full-line bg survival, got %q", got)
	}
	plain := ansi.Strip(got)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("want pad + body + pad, got %d lines: %q", len(lines), plain)
	}
	if !strings.Contains(lines[1], "›") || !strings.Contains(lines[1], "hello rhythm") {
		t.Fatalf("body line missing › message: %q", lines[1])
	}
}

func TestUserBubbleNoPipeWhenColorOff(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.NoTTY
	got := ansi.Strip(renderUserBubble("hi", 40, false, true))
	if strings.Contains(got, "│") {
		t.Fatalf("color-off must not use │ prefix, got %q", got)
	}
	if !strings.Contains(got, "›") {
		t.Fatalf("want › marker, got %q", got)
	}
}
```

Update `TestUserBubbleIsLightweightTranscriptLine` if it assumes single-line / no bg.

- [ ] **Step 2: Run — FAIL**

Run: `go test ./internal/cli/ -run 'TestUserBubble' -count=1`

- [ ] **Step 3: Implement `renderUserBubble`**

Mirror `diffBar` survival pattern:

```go
func renderUserBubble(line string, width int, planMode bool, current bool) string {
	line = displayLineForImageRefs(line)
	prefix := "› "
	if planMode {
		prefix = "› [plan] "
	}
	if width < 8 {
		width = 8
	}
	if !colorOn() {
		return prefix + line // no │
	}
	fg := activeCLITheme.accent
	if !current {
		fg = activeCLITheme.userBubbleFaded
	}
	bg := bgSGR(activeCLITheme.userBubbleBG)
	body := themeFg(fg, bold(prefix)+line) // or bold+dim on prefix only per spec
	// pad body to width with NBSP under bg
	// build: bg+padRow, bg+body+nbsp+reset, bg+padRow
}
```

Helper sketch:

```go
func paintUserBubbleRow(content string, width int, bg string) string {
	// content already styled; strip for width; pad with completionPadCell
	return bg + content + strings.Repeat(completionPadCell, pad) + ansiReset
}
```

- [ ] **Step 4: Run — PASS**

Run: `go test ./internal/cli/ -run 'TestUserBubble|TestTranscriptMarkers' -count=1`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/theme.go internal/cli/*_test.go
git commit -m "style(cli): Codex-style soft full-line user message bubble"
```

---

### Task 4: Explored single-`└` nest + weights

**Files:**
- Modify: `internal/cli/toolcard.go` (`exploredCard` ~219–271, drop `exploreBranchMid` sibling tree)
- Test: `internal/cli/toolcard_test.go` (`TestExploredCardCoalescesReads`, `TestExploredCardTreeHierarchy`)

**Interfaces:**
- Produces: first leaf line starts with `  └ `; subsequent leaves start with `    ` (four spaces); **no `├`**
- Header: `  ` + dim `•` + ` ` + bold `Explored`

- [ ] **Step 1: Rewrite failing hierarchy tests**

```go
func TestExploredCardSingleHangingBranch(t *testing.T) {
	leaves := []exploreLeaf{
		{Verb: "Search", Arg: "foo"},
		{Verb: "Read", Arg: "a.go"},
		{Verb: "Read", Arg: "b.go"},
	}
	plain := ansi.Strip(exploredCard(leaves, 80))
	if strings.Contains(plain, "├") {
		t.Fatalf("must not use sibling ├ tree, got %q", plain)
	}
	lines := strings.Split(plain, "\n")
	// lines[0] header Explored
	if !strings.Contains(lines[1], "└") || !strings.Contains(lines[1], "Search") {
		t.Fatalf("first leaf under └, got %q", lines[1])
	}
	// merged reads on one leaf under four-space indent
	if !strings.Contains(plain, "a.go, b.go") {
		t.Fatalf("merged reads missing: %q", plain)
	}
	// any line after the └ line should not introduce another └ for mid leaves
	// (only one └ in the whole card for the leaf block start is OK)
}
```

Replace `TestExploredCardTreeHierarchy` expectations that require `├`.

- [ ] **Step 2: Run — FAIL** (still has `├`)

Run: `go test ./internal/cli/ -run 'TestExplored' -count=1`

- [ ] **Step 3: Implement `exploredCard`**

```go
func exploredCard(leaves []exploreLeaf, width int) string {
	head := "  " + toolBullet() + " " + bold("Explored")
	rows := coalesceReadLeaves(leaves)
	// cap exploreMaxLeaves...
	var body []string
	for _, leaf := range show {
		line := treeVerbColor(leaf.Verb)
		if leaf.Arg != "" {
			line += " " + clampPlain(leaf.Arg, avail)
		}
		body = append(body, line)
	}
	// prefix_lines semantics:
	// first: dim("  └ ") + body[0]
	// rest:  "    " + body[i]
	return head + "\n" + joinPrefixed(body)
}
```

Remove `exploreBranchMid` / per-leaf `├` logic.

- [ ] **Step 4: Run — PASS**

Run: `go test ./internal/cli/ -run 'TestExplored|TestToolCard' -count=1`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/toolcard.go internal/cli/toolcard_test.go
git commit -m "style(cli): Explored tree uses single hanging └ like Codex"
```

---

### Task 5: Ran layout — `│` wrap, preview, `✓`/`✗`

**Files:**
- Modify: `internal/cli/toolcard.go` (`bashToolCard`, `renderToolOutputBlock`, new outcome helper)
- Modify: `internal/cli/chat_tui.go` (`collapseToolOutput`, `toggleShellOutput`, tool result path ~4082)
- Test: `internal/cli/toolcard_test.go`, `internal/cli/chat_render_test.go`

**Interfaces:**
- Produces:
  - `bashToolCard`: multi-line command uses `  │ ` continuation (not `└`)
  - `renderToolOutputPreview(output string, width, maxLines int) string` with `  └ ` / `    `, max 5
  - `toolOutcomeLine(ok bool, exitHint string, durationSec float64) string` → `  ✓ · 0.41s` / `  ✗ · …`
  - `collapseToolOutput`: rewrite card block to `card + preview + outcome` instead of stripping all output; store full text in `shellOutputs` for Ctrl+B

Constants:

```go
const toolCallPreviewMaxLines = 5
const ranCmdContinuationMaxLines = 2
```

- [ ] **Step 1: Failing pure tests**

```go
func TestBashToolCardCommandContinuationUsesPipe(t *testing.T) {
	card := toolCard("bash", `{"command":"go build ./...\ngo test ./..."}`, 60)
	plain := ansi.Strip(card)
	if !strings.Contains(plain, "│") {
		t.Fatalf("command wrap should use │, got %q", plain)
	}
	// first continuation line is command, not output
	if strings.Count(plain, "└") != 0 {
		t.Fatalf("card-only render should not use └ for cmd wrap, got %q", plain)
	}
}

func TestRenderToolOutputPreviewCapsFiveLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString(fmt.Sprintf("line-%d\n", i))
	}
	block := renderToolOutputPreview(b.String(), 80, 5)
	plain := ansi.Strip(block)
	if strings.Count(plain, "line-") > 5 {
		t.Fatalf("preview should cap at 5 content lines, got %q", plain)
	}
	if !strings.Contains(plain, "└") {
		t.Fatalf("output should start with └, got %q", plain)
	}
	if !strings.Contains(plain, "+") && !strings.Contains(plain, "more") && !strings.Contains(plain, "…") {
		t.Fatalf("want ellipsis for omitted lines, got %q", plain)
	}
}

func TestToolOutcomeLineSuccessAndFail(t *testing.T) {
	ok := ansi.Strip(toolOutcomeLine(true, "", 0.41))
	if !strings.Contains(ok, "✓") {
		t.Fatalf("success marker: %q", ok)
	}
	bad := ansi.Strip(toolOutcomeLine(false, "1", 1.5))
	if !strings.Contains(bad, "✗") {
		t.Fatalf("fail marker: %q", bad)
	}
}
```

Add integration-style test: after ToolResult, transcript card contains preview line when output non-empty (update existing collapse tests that expect output fully removed).

- [ ] **Step 2: Run — FAIL**

Run: `go test ./internal/cli/ -run 'TestBashToolCard|TestRenderToolOutput|TestToolOutcome' -count=1`

- [ ] **Step 3: Implement**

1. Change `bashToolCard` continuation from `connector` (`  └ `) to `  │ ` (max 2 extra lines; remainder ellipsis if needed).  
2. Add `renderToolOutputPreview` (default max 5); keep `renderToolOutputBlock` for full expand (Ctrl+B) using same gutters but `shellExpandMaxLines`.  
3. `toolOutcomeLine`.  
4. `collapseToolOutput` (viewport path): instead of only `removeTranscriptBlock`, set card live block to:

```go
card := toolCard(name, args, width)
if preview := renderToolOutputPreview(full, width, toolCallPreviewMaxLines); preview != "" {
	card += "\n" + preview
}
card += "\n" + toolOutcomeLine(err == "", exitHint, dur)
m.setLiveBlock(cardIdx, card)
// keep shellOutputs[id] = full for expand
m.shellExpanded[id] = false // collapsed = preview mode
```

5. `toggleShellOutput`: expanded = full `renderToolOutputBlock`; collapsed = preview.  
6. On success bullet: optional green `toolBulletOK` on Ran header when finalizing (if easy via re-render).

Wire duration: if ToolResult does not carry duration, use `time.Since(toolStreamStart)` when id matches, else omit duration segment.

- [ ] **Step 4: Run — PASS**

Run: `go test ./internal/cli/ -run 'TestBash|TestTool|TestShell|TestCollapse|TestCtrl' -count=1`

Fix any tests that required “output completely gone after result”.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/toolcard.go internal/cli/chat_tui.go internal/cli/*_test.go
git commit -m "feat(cli): Ran │ wrap, default 5-line preview, and ✓/✗ outcomes"
```

---

### Task 6: Turn `─` separator (`hadWorkActivity`)

**Files:**
- Create: `internal/cli/separators.go`
- Modify: `internal/cli/chat_tui.go` (field `hadWorkActivity bool`; set on tool cards; `event.TurnDone` emit)
- Test: `internal/cli/separators_test.go`, `internal/cli/chat_render_test.go`

**Interfaces:**
- Produces: `func finalMessageSeparator(width int, elapsedSec int) string`
  - `elapsedSec <= 60` or `< 0`: `strings.Repeat("─", width)` dimmed
  - `elapsedSec > 60`: `─ Worked for {compact} ─` + fill `─` to width, dimmed
- Produces: `hadWorkActivity` set true when committing explore/ran/edited/mcp/error tool cards; reset on TurnDone after separator

- [ ] **Step 1: Failing tests**

```go
func TestFinalMessageSeparatorPlainUnder60s(t *testing.T) {
	s := ansi.Strip(finalMessageSeparator(40, 12))
	if strings.Contains(s, "Worked") {
		t.Fatalf("no Worked label under 60s: %q", s)
	}
	if strings.Trim(s, "─") != "" {
		t.Fatalf("want only ─ chars, got %q", s)
	}
	if visibleWidth(s) != 40 {
		t.Fatalf("width %d want 40", visibleWidth(s))
	}
}

func TestFinalMessageSeparatorWorkedForOver60s(t *testing.T) {
	s := ansi.Strip(finalMessageSeparator(80, 75))
	if !strings.Contains(s, "Worked for") {
		t.Fatalf("want Worked for, got %q", s)
	}
}

func TestTurnDoneEmitsSeparatorAfterTools(t *testing.T) {
	m := newRenderTestChatTUI() // existing helper if named differently use newTestChatTUI + width
	m.width = 60
	// simulate: dispatch bash tool + result + TurnDone
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.ToolInfo{ID: "1", Name: "bash", Args: `{"command":"true"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.ToolInfo{ID: "1", Name: "bash", Output: "ok"}})
	m.ingestEvent(event.Event{Kind: event.TurnDone})
	joined := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(joined, "─") {
		t.Fatalf("want turn rule after tool work, got %q", joined)
	}
}

func TestTurnDoneNoSeparatorPureChat(t *testing.T) {
	m := newTestChatTUI()
	m.width = 60
	m.ingestEvent(event.Event{Kind: event.Text, Text: "hello"})
	m.ingestEvent(event.Event{Kind: event.TurnDone})
	joined := ansi.Strip(strings.Join(m.transcript, "\n"))
	// allow other chars; assert no full-width rule line of ─ only
	for _, block := range m.transcript {
		plain := strings.TrimSpace(ansi.Strip(block))
		if plain != "" && strings.Trim(plain, "─") == "" {
			t.Fatalf("pure chat must not emit ─ rule, got %q", m.transcript)
		}
	}
}
```

Adapt event field names to actual `event.Event` / `event.Tool` structs in this repo (inspect `internal/event`).

- [ ] **Step 2: Run — FAIL**

Run: `go test ./internal/cli/ -run 'TestFinalMessage|TestTurnDone' -count=1`

- [ ] **Step 3: Implement**

`separators.go`:

```go
func finalMessageSeparator(width, elapsedSec int) string {
	if width < 8 {
		width = 8
	}
	if elapsedSec > 60 {
		label := fmt.Sprintf("─ Worked for %s ─", formatElapsedCompact(elapsedSec))
		// pad with ─ to width; dim whole line
		return dim(padRule(label, width))
	}
	return dim(strings.Repeat("─", width))
}
```

Reuse or copy compact elapsed formatting (`1m 15s`) from status code if present; else small local helper.

`chatTUI`:

```go
hadWorkActivity bool
```

Set `m.hadWorkActivity = true` when appending explore leaves / tool cards / edited / bash.

On `event.TurnDone` after `commitPending`:

```go
if m.hadWorkActivity {
	m.ensureBlank()
	sec := m.elapsed // already tracked during run
	m.commitLine(finalMessageSeparator(m.width, sec))
}
m.hadWorkActivity = false
```

- [ ] **Step 4: Run — PASS**

Run: `go test ./internal/cli/ -run 'TestFinalMessage|TestTurnDone|TestIngest|TestExplored' -count=1`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/separators.go internal/cli/separators_test.go internal/cli/chat_tui.go internal/cli/*_test.go
git commit -m "feat(cli): dim turn separator after tool-bearing turns"
```

---

### Task 7: Working-line cleanup (no transcript working wall)

**Files:**
- Modify: `internal/cli/chat_tui.go` (`beginToolRunning`, `tickToolRunning`, `runningWorkingLine`)
- Test: `internal/cli/chat_render_test.go`, `internal/cli/status_footer_test.go` if needed

**Interfaces:**
- Default: do not commit braille `working · Ns` connector lines into transcript for quiet tools
- Live progress stays on `runningWorkingLine` above composer
- Optional: keep streaming real stdout under card when `ToolProgress` arrives (existing stream path)

- [ ] **Step 1: Failing test**

```go
func TestBeginToolRunningDoesNotPaintWorkingWall(t *testing.T) {
	m := newTestChatTUI()
	m.width = 80
	// dispatch a bash tool that would previously open working line
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: /* bash */})
	joined := ansi.Strip(strings.Join(m.transcript, "\n"))
	if strings.Contains(joined, "working") || strings.Contains(joined, "⠋") {
		t.Fatalf("must not paint working wall into transcript, got %q", joined)
	}
}
```

- [ ] **Step 2: Run — FAIL** (if current code paints working)

- [ ] **Step 3: Implement**

- `beginToolRunning`: stop `commitLine(connectorBlock(working…))`; only set stream ids / timers.  
- `tickToolRunning`: no-op for empty-output tools, or update ambient only.  
- Keep `streamToolOutput` when real progress arrives.  
- Optionally tighten `runningWorkingLine` copy toward `• Working (Ns · esc…)` using existing i18n keys if available; do not add new locales unless required for compile.

- [ ] **Step 4: Run — PASS**

Run: `go test ./internal/cli/ -run 'TestBeginTool|TestToolStream|TestReasoning|TestWorking' -count=1`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/*_test.go
git commit -m "fix(cli): keep tool working status ambient, not in transcript"
```

---

### Task 8: Full package verification + checklist note

**Files:** none required beyond fixes

- [ ] **Step 1: Run full cli package tests**

```bash
go test ./internal/cli/ -count=1
```

Expected: PASS. Fix regressions only within rhythm scope.

- [ ] **Step 2: Manual checklist (from spec §7.1)**

Against a real `bin/corvus` session (or scripted PTY if available), verify items 1–10 in the spec. Note any residual gaps in the commit message body if discovered and fixed.

- [ ] **Step 3: Final commit only if fixups landed**

```bash
git add -u internal/cli
git commit -m "test(cli): finish Codex rhythm pack verification"
```

(Skip empty commit if already green with no extra changes.)

---

## Spec coverage checklist (plan self-review)

| Spec requirement | Task |
|------------------|------|
| ensureBlank / no double blank | 1 |
| Assistant `•` first line | 2 |
| Banner single line, no tip | 2 |
| User soft bg + pad; no `│ ›` | 3 |
| Explored single `└`, no `├` | 4 |
| Ran `│` wrap | 5 |
| Default ≤5 preview + `✓`/`✗` | 5 |
| Turn `─` / Worked for >60s | 6 |
| hadWorkActivity gating | 6 |
| Ambient working only | 7 |
| Footer unchanged | (no task — leave alone) |
| Full test + manual checklist | 8 |

## Placeholder scan

No TBD/TODO steps; concrete tests and function names included. Adapt `event.Tool` field names to the live `internal/event` package during Task 5–7 if they differ slightly from sketches.

## Type consistency

- `ensureBlank()` used in Tasks 1, 6  
- `finalMessageSeparator(width, elapsedSec int) string` used in Task 6  
- `toolCallPreviewMaxLines = 5` used in Task 5  
- `renderUserBubble` signature unchanged for callers  

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-08-tui-codex-rhythm.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks  
2. **Inline Execution** — this session with executing-plans and checkpoints  

Which approach?
