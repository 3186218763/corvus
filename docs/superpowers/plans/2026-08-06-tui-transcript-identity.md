# TUI Transcript Identity & Tool-Card Coloring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the "Corvus" name only on the bottom-most assistant reply (history keeps a bare dim `◆`), render the latest user bubble in full accent while history fades to a derived `userBubbleFaded` tint, and color tool-call verbs by category with args in a new `toolArg` tone.

**Architecture:** (1) a stateless `transcriptMarker` bitmask derived from `transcriptSources` (`currentTranscriptMarkers`) decides which block is "live"; render functions take the marker as a parameter and `commitTranscriptSource`/remove/truncate re-render only changed blocks (≤2); (2) two new `cliPalette` slots — `userBubbleFaded` derived from the accent in `applyCLIThemeStyle` (hex mix + hand-picked xterm per style), `toolArg` fixed per mode; (3) `toolCategoryColor` extracted from `toolDot` and shared by the dot and the verb.

**Tech Stack:** Go 1.25, Bubble Tea v2, Lip Gloss v2, `charm.land/bubbles/v2`, `x/ansi`; tests pin SGR via `fgSGR(activeCLITheme.*)`.

**Spec:** `docs/superpowers/specs/2026-08-06-tui-transcript-identity-design.md` (commits `09d268d`, `b5c8c07`; subagent review applied).
**Review:** `docs/superpowers/research/2026-08-06-transcript-identity/review-design.md`
**Baseline:** HEAD includes P1.5 (`d7cc626`); line anchors verified against the working tree.

---

## Global constraints

- TDD per task: write failing test → run (verify FAIL) → implement → run (verify PASS) → commit. One commit per task.
- Do not change Esc semantics, double-Esc 600ms, double-Ctrl+C 1500ms, completion Ctrl+P/N, draft preservation, `/cls` behavior.
- No new hardcoded SGR sequences: `color_discipline_test.go` must stay green.
- `go test ./internal/cli/ -count=1` must stay green after every task; `go vet ./internal/cli/` at the end.
- Copy parity: `buildCopyTranscript` visible text (ANSI-stripped, math markers aside) must equal the rendered transcript; existing copy tests at transcript_test.go:243/323 assert this automatically.
- i18n untouched (no new user-visible strings).
- After edits: `gofmt -w <changed files>` before committing.

## File map

| File | Responsibility |
|------|----------------|
| `internal/cli/transcript.go` | `transcriptMarker` type, `currentTranscriptMarkers`, marker-aware `renderTranscriptSource`, `commitTranscriptSource` resync, remove/truncate resync, `reflowTranscript`, `buildCopyTranscript`, `renderAssistantMarkdown`/`Copy` (+named), `renderReplayBundle`/`Copy` (+marker) |
| `internal/cli/transcript_markers_test.go` (new) | marker derivation table tests |
| `internal/cli/theme.go` | `userBubbleFaded` + `toolArg` slots, `fadedUserBubbleColor`, `userBubbleFadedXTerm` map, `applyCLIThemeStyle` wiring |
| `internal/cli/theme_test.go` | faded derivation tests |
| `internal/cli/chat_tui.go` | `renderUserBubble` (+current), `replaySectionsForWithAssistantRenderer` (markers + pre-scan), `replaySectionsFor` (demoted default), `streamAnswer`/`commitPending` marker args |
| `internal/cli/toolcard.go` | `toolCategoryColor`, `toolDot` refactor, `toolHead` coloring |
| `internal/cli/toolcard_test.go` | verb/arg color pins |
| `internal/cli/diffview_test.go` | diff header color pin |
| `internal/cli/chat_tui_test.go`, `internal/cli/transcript_test.go` | mechanical signature updates + new behavior tests |

---

### Task 1: Marker computation (pure function)

**Files:**
- Modify: `internal/cli/transcript.go` (after the `transcriptSource` struct, before `ensureTranscriptSources`)
- Create: `internal/cli/transcript_markers_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/transcript_markers_test.go`:

```go
package cli

import (
	"reflect"
	"testing"
)

func TestCurrentTranscriptMarkers(t *testing.T) {
	user := transcriptSource{kind: transcriptSourceUser, raw: "u"}
	md := func(s string) transcriptSource { return transcriptSource{kind: transcriptSourceMarkdown, raw: s} }
	bundle := transcriptSource{kind: transcriptSourceReplayBundle}
	tool := transcriptSource{kind: transcriptSourceToolCard}
	fixed := transcriptSource{kind: transcriptSourceFixed}

	cases := []struct {
		name    string
		sources []transcriptSource
		want    []transcriptMarker
	}{
		{"empty", nil, nil},
		{"single user", []transcriptSource{user}, []transcriptMarker{markerUserCurrent}},
		{"one exchange", []transcriptSource{user, md("a1")}, []transcriptMarker{markerUserCurrent, markerAssistantNamed}},
		{"second exchange demotes", []transcriptSource{user, md("a1"), user}, []transcriptMarker{markerNone, markerNone, markerUserCurrent}},
		{"two answers one turn", []transcriptSource{user, md("a1"), tool, md("a2")}, []transcriptMarker{markerUserCurrent, markerNone, markerNone, markerAssistantNamed}},
		{"user between answers", []transcriptSource{user, md("a1"), user, md("a2")}, []transcriptMarker{markerNone, markerNone, markerUserCurrent, markerAssistantNamed}},
		{"bundle alone", []transcriptSource{bundle}, []transcriptMarker{markerAssistantNamed | markerUserCurrent}},
		{"bundle then answer", []transcriptSource{bundle, md("a2")}, []transcriptMarker{markerUserCurrent, markerAssistantNamed}},
		{"bundle then user", []transcriptSource{bundle, user}, []transcriptMarker{markerNone, markerUserCurrent}},
		{"tool cards never marked", []transcriptSource{tool, fixed}, []transcriptMarker{markerNone, markerNone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := currentTranscriptMarkers(tc.sources)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("currentTranscriptMarkers(%v) = %v, want %v", tc.sources, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestCurrentTranscriptMarkers -count=1`
Expected: FAIL (compile error — `transcriptMarker`/`currentTranscriptMarkers` undefined)

- [ ] **Step 3: Implement**

In `internal/cli/transcript.go`, after the `transcriptSource` struct definition, add:

```go
// transcriptMarker marks the live block in the transcript: the bottom-most
// conversation keeps full-strength styling (full accent user bubble, named
// assistant header) while everything above renders as demoted history. Markers
// are derived from transcriptSources at render time, never stored.
type transcriptMarker uint8

const (
	markerNone           transcriptMarker = 0
	markerUserCurrent    transcriptMarker = 1 // render user content full accent
	markerAssistantNamed transcriptMarker = 2 // render assistant name
)

// currentTranscriptMarkers derives per-block liveness markers. The last user
// block is "current"; the last markdown/replayBundle block is "named" unless a
// user block follows it. A replayBundle additionally keeps its last internal
// user message "current" when no user source follows the bundle (invariant:
// replayBundle only appears at index 0, so [bundle, bundle] is unreachable).
func currentTranscriptMarkers(sources []transcriptSource) []transcriptMarker {
	markers := make([]transcriptMarker, len(sources))
	lastUser := -1
	for i, s := range sources {
		if s.kind == transcriptSourceUser {
			lastUser = i
		}
	}
	lastAssistant := -1
	for i, s := range sources {
		if s.kind == transcriptSourceMarkdown || s.kind == transcriptSourceReplayBundle {
			lastAssistant = i
		}
	}
	namedIdx := lastAssistant
	if namedIdx >= 0 && lastUser > namedIdx {
		namedIdx = -1
	}
	for i, s := range sources {
		switch s.kind {
		case transcriptSourceUser:
			if i == lastUser {
				markers[i] |= markerUserCurrent
			}
		case transcriptSourceMarkdown:
			if i == namedIdx {
				markers[i] |= markerAssistantNamed
			}
		case transcriptSourceReplayBundle:
			if i == namedIdx {
				markers[i] |= markerAssistantNamed
			}
			if lastUser < i {
				markers[i] |= markerUserCurrent
			}
		}
	}
	return markers
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestCurrentTranscriptMarkers -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/transcript.go internal/cli/transcript_markers_test.go
git commit -m "feat(cli): derive transcript liveness markers"
```

---

### Task 2: Palette slots (`userBubbleFaded`, `toolArg`)

**Files:**
- Modify: `internal/cli/theme.go`
- Modify: `internal/cli/theme_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/theme_test.go`:

```go
func TestUserBubbleFadedFollowsAccent(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	configureCLIThemeWithStyle("dark", "graphite")
	if activeCLITheme.userBubbleFaded.hex == activeCLITheme.accent.hex {
		t.Fatalf("faded must differ from accent: %s", activeCLITheme.userBubbleFaded.hex)
	}
	if got, want := activeCLITheme.userBubbleFaded.hex, "#a87c6e"; got != want {
		t.Fatalf("graphite faded hex = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.userBubbleFaded.xterm, 95; got != want {
		t.Fatalf("graphite faded xterm = %d, want 95", got)
	}
	configureCLIThemeWithStyle("dark", "aurora")
	if got, want := activeCLITheme.userBubbleFaded.hex, "#5e9e91"; got != want {
		t.Fatalf("aurora faded hex = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.userBubbleFaded.xterm, 72; got != want {
		t.Fatalf("aurora faded xterm = %d, want 72", got)
	}
	configureCLIThemeWithStyle("light", "sandstone")
	if got := activeCLITheme.userBubbleFaded.hex; got == "" || got == activeCLITheme.accent.hex {
		t.Fatalf("light faded must be set and differ from accent, got %s", got)
	}
	if got, want := activeCLITheme.toolArg.hex, "#5a6470"; got != want {
		t.Fatalf("light toolArg = %s, want %s", got, want)
	}
	configureCLIThemeWithStyle("dark", "graphite")
	if got, want := activeCLITheme.toolArg.hex, "#a5b0bd"; got != want {
		t.Fatalf("dark toolArg = %s, want %s", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestUserBubbleFadedFollowsAccent -count=1`
Expected: FAIL (compile error — fields `userBubbleFaded`/`toolArg` undefined)

- [ ] **Step 3: Implement**

In `internal/cli/theme.go`:

(a) Add two fields to the `cliPalette` struct (after `toolProc`):

```go
	toolRead        cliColor
	toolProc        cliColor
	userBubbleFaded cliColor
	toolArg         cliColor
```

(b) Add defaults to `cliDarkTheme` (after `toolProc`):

```go
		toolRead:        cliColor{"#56b6c2", 80},
		toolProc:        cliColor{"#c678dd", 176},
		userBubbleFaded: cliColor{"#a87c6e", 95},
		toolArg:         cliColor{"#a5b0bd", 145},
```

(c) Add defaults to `cliLightTheme` (after `toolProc`):

```go
		toolRead:        cliColor{"#6f91d9", 68},
		toolProc:        cliColor{"#8a6bb8", 97},
		userBubbleFaded: cliColor{"#9e7263", 95},
		toolArg:         cliColor{"#5a6470", 240},
```

(d) Add the derivation helpers just before `applyCLIThemeStyle`:

```go
// userBubbleFadedXTerm is the hand-picked 256-color fallback for the faded
// user-bubble tint per accent style (repo convention: hand-chosen fallbacks).
var userBubbleFadedXTerm = map[string]int{
	"graphite":  95,
	"ember":     131,
	"aurora":    72,
	"midnight":  140,
	"sandstone": 95,
	"porcelain": 103,
	"linen":     131,
	"glacier":   67,
}

// fadedUserBubbleColor derives the history user-bubble tint from the accent:
// 45% accent + 55% neutral gray keeps the hue while desaturating it. The xterm
// fallback is hand-picked per accent style, falling back to the accent's own
// index for unknown styles.
func fadedUserBubbleColor(accent cliColor, style string) cliColor {
	hex := accent.hex
	if r, g, b, ok := parseHexColor(accent.hex); ok {
		mix := func(c int) int { return (45*c + 7090) / 100 }
		hex = fmt.Sprintf("#%02x%02x%02x", mix(r), mix(g), mix(b))
	}
	xterm := accent.xterm
	if v, ok := userBubbleFadedXTerm[style]; ok {
		xterm = v
	}
	return cliColor{hex: hex, xterm: xterm}
}
```

(e) Update `applyCLIThemeStyle`:

```go
func applyCLIThemeStyle(base cliPalette, style cliThemeStyle) cliPalette {
	base.style = style.name
	base.accent = style.accent
	base.selection = style.accent
	base.userBubbleFaded = fadedUserBubbleColor(style.accent, style.name)
	return base
}
```

Run `gofmt -w internal/cli/theme.go` (struct field alignment).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestUserBubbleFadedFollowsAccent -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/theme.go internal/cli/theme_test.go
git commit -m "feat(cli): add userBubbleFaded and toolArg palette slots"
```

---

### Task 3: Marker-aware render pipeline (assistant/user/bundle/copy)

This task changes every render entry point in one coherent pass — the function-type signatures are interdependent (`renderAssistantMarkdown` gains a `named` param, so `renderReplayBundle`/`replaySectionsForWithAssistantRenderer` must change in the same commit to keep the package compiling).

**Files:**
- Modify: `internal/cli/transcript.go`
- Modify: `internal/cli/chat_tui.go`
- Modify: `internal/cli/transcript_test.go`
- Modify: `internal/cli/chat_tui_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/transcript_test.go`:

```go
func TestAssistantMarkdownHistoryDropsName(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	named := renderAssistantMarkdown("Live answer", 48, true)
	if plain := ansi.Strip(named); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("named header should keep the name, got %q", plain)
	}
	history := renderAssistantMarkdown("History answer", 48, false)
	plain := ansi.Strip(history)
	if strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("history header must not carry the name, got %q", plain)
	}
	if !strings.HasPrefix(plain, "  ◆") {
		t.Fatalf("history header should keep the diamond, got %q", plain)
	}
	if !strings.Contains(history, fgSGR(activeCLITheme.faint)) {
		t.Fatalf("history diamond should be faint-colored, got %q", history)
	}
}

func TestUserBubbleFadedHistory(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	current := renderUserBubble("now", 80, false, true)
	if !strings.Contains(current, fgSGR(activeCLITheme.accent)) {
		t.Fatalf("current bubble should use accent SGR, got %q", current)
	}
	faded := renderUserBubble("then", 80, false, false)
	if !strings.Contains(faded, fgSGR(activeCLITheme.userBubbleFaded)) {
		t.Fatalf("history bubble should use userBubbleFaded SGR, got %q", faded)
	}
	if strings.Contains(faded, fgSGR(activeCLITheme.accent)) {
		t.Fatalf("history bubble must not use full accent, got %q", faded)
	}
}

func TestSecondExchangeDemotesFirst(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "first question"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "first answer"})
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("first answer should be named, got %q", plain)
	}
	if !strings.Contains(m.transcript[0], fgSGR(activeCLITheme.accent)) {
		t.Fatalf("first bubble should be current/accent, got %q", m.transcript[0])
	}

	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "second question"})
	if !strings.Contains(m.transcript[0], fgSGR(activeCLITheme.userBubbleFaded)) {
		t.Fatalf("first bubble should be faded after turn 2, got %q", m.transcript[0])
	}
	if strings.Contains(ansi.Strip(m.transcript[1]), "Corvus") {
		t.Fatalf("first answer must lose the name after turn 2, got %q", m.transcript[1])
	}

	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "second answer"})
	if plain := ansi.Strip(m.transcript[3]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("second answer should be named, got %q", plain)
	}
}

// TestNonLiveCommitsKeepMarkers covers the banner (/new, /cls) and tool-card
// commitTranscriptSource call sites: neither may demote the live exchange.
func TestNonLiveCommitsKeepMarkers(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "a"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceBanner})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceToolCard, raw: "bash", aux: `{"command":"ls"}`})
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("banner/tool commits must not demote the live answer, got %q", plain)
	}
	if !strings.Contains(m.transcript[0], fgSGR(activeCLITheme.accent)) {
		t.Fatalf("user bubble should stay current, got %q", m.transcript[0])
	}
}

func TestUnsendRegainsAssistantName(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q1"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "a1"})
	m.bubbleStartIdx = len(m.transcript)
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q2"})
	if plain := ansi.Strip(m.transcript[1]); strings.Contains(plain, "Corvus") {
		t.Fatalf("precondition: a1 should be demoted while q2 is pending, got %q", plain)
	}
	m.truncateTranscriptBlocks(m.bubbleStartIdx)
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("after un-send the previous answer should regain its name, got %q", plain)
	}
}

func TestRemoveLastAnswerRetagsPrevious(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "a1"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "a2"})
	if plain := ansi.Strip(m.transcript[2]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("a2 should be named, got %q", plain)
	}
	m.removeTranscriptBlock(2)
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("after removing a2, a1 should regain the name, got %q", plain)
	}
}

func TestReflowPreservesMarkers(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.width = 80
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q"})
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceMarkdown, raw: "answer"})
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("precondition: answer should be named, got %q", plain)
	}
	m.reflowTranscript(40)
	if plain := ansi.Strip(m.transcript[1]); !strings.HasPrefix(plain, "  ◆ Corvus") {
		t.Fatalf("reflow must preserve the named marker, got %q", plain)
	}

	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "q2"})
	m.reflowTranscript(60)
	if strings.Contains(ansi.Strip(m.transcript[1]), "Corvus") {
		t.Fatalf("reflow must preserve demotion, got %q", m.transcript[1])
	}
	if !strings.Contains(m.transcript[2], fgSGR(activeCLITheme.accent)) {
		t.Fatalf("reflow must preserve the user current marker, got %q", m.transcript[2])
	}
}

func TestReplayBundleInternalLiveness(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	history := []provider.Message{
		{Role: provider.RoleUser, Content: "old question"},
		{Role: provider.RoleAssistant, Content: "old answer"},
		{Role: provider.RoleUser, Content: "latest question"},
		{Role: provider.RoleAssistant, Content: "latest answer"},
	}

	// Live bundle committed through the production path: last internal
	// assistant named, last internal user full accent.
	m := newTestChatTUI()
	m.label = "model-x"
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceReplayBundle, history: history})
	live := strings.Join(m.transcript, "\n")
	if !strings.Contains(ansi.Strip(live), "◆ Corvus\n\n  latest answer") {
		t.Fatalf("live bundle should name the last assistant body, got %q", live)
	}
	if strings.Contains(ansi.Strip(live), "Corvus\n\n  old answer") {
		t.Fatalf("live bundle must not name earlier assistant bodies, got %q", live)
	}
	if !strings.Contains(live, fgSGR(activeCLITheme.accent)+"› latest question") {
		t.Fatalf("live bundle should render the last user full accent, got %q", live)
	}
	if !strings.Contains(live, fgSGR(activeCLITheme.userBubbleFaded)) {
		t.Fatalf("live bundle should fade earlier user bubbles, got %q", live)
	}

	// A new user message demotes the whole bundle.
	m.commitTranscriptSource(transcriptSource{kind: transcriptSourceUser, raw: "new question"})
	if plain := ansi.Strip(strings.Join(m.transcript, "\n")); strings.Contains(plain, "Corvus") {
		t.Fatalf("bundle must carry no name after a new user message, got %q", plain)
	}
	if strings.Contains(strings.Join(m.transcript, "\n"), fgSGR(activeCLITheme.accent)+"› latest question") {
		t.Fatalf("bundle internal user must fade after a new user message, got %q", strings.Join(m.transcript, "\n"))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestAssistantMarkdownHistoryDropsName|TestUserBubbleFadedHistory|TestSecondExchangeDemotesFirst|TestNonLiveCommitsKeepMarkers|TestUnsendRegainsAssistantName|TestRemoveLastAnswerRetagsPrevious|TestReflowPreservesMarkers|TestReplayBundleInternalLiveness' -count=1`
Expected: FAIL (compile errors — new params missing)

- [ ] **Step 3: Implement render functions**

In `internal/cli/transcript.go`, update `renderAssistantMarkdown` (full replacement):

```go
func renderAssistantMarkdown(raw string, contentWidth int, named bool) string {
	contentWidth = max(contentWidth, 1)
	indent := assistantTranscriptIndent
	if contentWidth <= visibleWidth(indent) {
		indent = ""
	}
	bodyWidth := max(contentWidth-visibleWidth(indent), 1)
	renderer := newMarkdownRenderer(bodyWidth)
	rendered := renderer.Render(raw)
	if rendered == "" {
		rendered = raw
	}
	body := strings.TrimRight(rendered, "\n")
	header := indent + dim("◆")
	if named {
		header = indent + accent("◆") + " " + bold("Corvus")
	}
	if body == "" {
		return header
	}
	return header + "\n\n" + indentTranscriptBlock(body, indent)
}
```

Update `renderAssistantMarkdownCopy` (full replacement):

```go
func renderAssistantMarkdownCopy(raw string, contentWidth int, prefix string, named bool) string {
	contentWidth = max(contentWidth, 1)
	indent := assistantTranscriptIndent
	if contentWidth <= visibleWidth(indent) {
		indent = ""
	}
	bodyWidth := max(contentWidth-visibleWidth(indent), 1)
	renderer := newMarkdownRenderer(bodyWidth)
	rendered := renderer.RenderCopy(raw, prefix)
	if rendered == "" {
		rendered = raw
	}
	body := strings.TrimRight(rendered, "\n")
	header := indent + dim("◆")
	if named {
		header = indent + accent("◆") + " " + bold("Corvus")
	}
	if body == "" {
		return header
	}
	return header + "\n\n" + indentTranscriptBlock(body, indent)
}
```

In `internal/cli/chat_tui.go`, update `renderUserBubble` (full replacement):

```go
func renderUserBubble(line string, width int, planMode bool, current bool) string {
	line = displayLineForImageRefs(line)
	prefix := "› "
	if planMode {
		prefix = "› [plan] "
	}
	if !colorOn() {
		return "│ " + prefix + line
	}
	color := activeCLITheme.accent
	if !current {
		color = activeCLITheme.userBubbleFaded
	}
	return "  " + themeFg(color, prefix+line)
}
```

- [ ] **Step 4: Implement the replay section layer**

In `internal/cli/chat_tui.go`, replace `replaySectionsForWithAssistantRenderer` and `replaySectionsFor` (full replacement of both; `replaySectionsFor` keeps its `func([]provider.Message, int) []string` signature and demotes everything):

```go
func replaySectionsFor(history []provider.Message, width int) []string {
	return replaySectionsForWithAssistantRenderer(
		history,
		width,
		renderAssistantMarkdown,
		func(raw string, width int, current bool) string {
			return renderUserBubble(raw, width, false, current)
		},
		false,
		false,
	)
}

// replaySectionsForWithAssistantRenderer renders replay history sections. When
// nameLast/lastUserFull are set, the last assistant body and the last user
// bubble of the section list carry the live markers (used when this bundle is
// the bottom-most block); every other section renders demoted history.
func replaySectionsForWithAssistantRenderer(
	history []provider.Message,
	width int,
	renderAssistant func(string, int, bool) string,
	renderUser func(string, int, bool) string,
	nameLast bool,
	lastUserFull bool,
) []string {
	lastUserSection := -1
	lastAssistantBody := -1
	for i, m := range history {
		switch {
		case m.LocalOnly:
			if strings.TrimSpace(m.Content) != "" {
				lastAssistantBody = i
			}
		case m.Role == provider.RoleUser:
			if _, isSteer := agent.SteerText(m.Content); !isSteer {
				lastUserSection = i
			}
		case m.Role == provider.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				lastAssistantBody = i
			}
		}
	}
	var out []string
	for i, m := range history {
		if m.LocalOnly {
			if reasoning := strings.TrimSpace(m.ReasoningContent); reasoning != "" {
				out = append(out, dim("  ▎ "+i18n.M.ChatThinking)+"\n"+reasoningBlock(reasoning, width, 0)+"\n\n")
			}
			if body := strings.TrimSpace(m.Content); body != "" {
				out = append(out, renderAssistant(body, width, i == lastAssistantBody && nameLast)+"\n\n")
			}
			for _, call := range m.ToolCalls {
				out = append(out, toolCard(call.Name, "", width)+"\n\n")
			}
			if m.InterruptedTurn != nil {
				out = append(out, fmt.Sprintf("  · %s\n\n", interruptedTurnDisplayNotice()))
			}
			continue
		}
		switch m.Role {
		case provider.RoleUser:
			// Steer messages are surfaced as a notice line, not a user bubble.
			if steerText, isSteer := agent.SteerText(m.Content); isSteer {
				out = append(out, fmt.Sprintf("  ↪ %s\n\n", steerText))
				continue
			}
			content := control.StripComposePrefixes(m.Content)
			out = append(out, renderUser(content, width, i == lastUserSection && lastUserFull)+"\n\n")
		case provider.RoleAssistant:
			if reasoning := strings.TrimSpace(m.ReasoningContent); reasoning != "" {
				out = append(out, dim("  ▎ "+i18n.M.ChatThinking)+"\n"+reasoningBlock(reasoning, width, 0)+"\n\n")
			}
			body := strings.TrimSpace(m.Content)
			if body != "" {
				out = append(out, renderAssistant(body, width, i == lastAssistantBody && nameLast)+"\n\n")
			}
			for _, call := range m.ToolCalls {
				out = append(out, toolCard(call.Name, call.Arguments, width)+"\n\n")
			}
		}
	}
	return out
}
```

- [ ] **Step 5: Implement the transcript.go pipeline**

In `internal/cli/transcript.go`:

(a) Add the renderers struct just before `renderReplayBundle`:

```go
// replaySectionRenderers adapts the production renderers to the section list.
// user wraps renderUserBubble with a fixed planMode=false (replay history never
// carries the plan prefix).
type replaySectionRenderers struct {
	assistant func(raw string, width int, named bool) string
	user      func(raw string, width int, current bool) string
}
```

(b) Replace `renderReplayBundle`:

```go
func (m chatTUI) renderReplayBundle(
	source transcriptSource,
	contentWidth int,
	r replaySectionRenderers,
	marker transcriptMarker,
) string {
	var b strings.Builder
	b.WriteString(renderTUIBanner(m.label, source.raw, contentWidth))
	for _, section := range replaySectionsForWithAssistantRenderer(
		source.history,
		contentWidth,
		r.assistant,
		r.user,
		marker&markerAssistantNamed != 0,
		marker&markerUserCurrent != 0,
	) {
		b.WriteString(section)
	}
	return strings.TrimRight(b.String(), "\n")
}
```

(c) Replace `renderReplayBundleCopy`:

```go
func (m chatTUI) renderReplayBundleCopy(
	source transcriptSource,
	contentWidth int,
	prefix string,
	marker transcriptMarker,
) string {
	assistantIndex := 0
	return m.renderReplayBundle(source, contentWidth, replaySectionRenderers{
		assistant: func(raw string, width int, named bool) string {
			messagePrefix := prefix + "-" + strconv.Itoa(assistantIndex)
			assistantIndex++
			return renderAssistantMarkdownCopy(raw, width, messagePrefix, named)
		},
		user: func(raw string, width int, current bool) string {
			return renderUserBubble(raw, width, false, current)
		},
	}, marker)
}
```

(d) Replace `renderTranscriptSource`:

```go
func (m *chatTUI) renderTranscriptSource(source transcriptSource, terminalWidth int, marker transcriptMarker) string {
	contentWidth := transcriptContentWidth(terminalWidth, m.nativeScrollback)
	switch source.kind {
	case transcriptSourceMarkdown:
		return renderAssistantMarkdown(source.raw, contentWidth, marker&markerAssistantNamed != 0)
	case transcriptSourceUser:
		return renderUserBubble(source.raw, terminalWidth, source.planMode, marker&markerUserCurrent != 0)
	case transcriptSourceReasoning:
		return reasoningBlock(source.raw, terminalWidth, source.maxLines)
	case transcriptSourceToolCard:
		return toolCard(source.raw, source.aux, terminalWidth)
	case transcriptSourceBanner:
		return strings.TrimRight(renderTUIBanner(m.label, source.raw, contentWidth), "\n")
	case transcriptSourceReplayBundle:
		return m.renderReplayBundle(source, contentWidth, replaySectionRenderers{
			assistant: renderAssistantMarkdown,
			user: func(raw string, width int, current bool) string {
				return renderUserBubble(raw, width, false, current)
			},
		}, marker)
	default:
		return ""
	}
}
```

(e) Replace `reflowTranscript`:

```go
func (m *chatTUI) reflowTranscript(terminalWidth int) {
	m.ensureTranscriptSources()
	markers := currentTranscriptMarkers(m.transcriptSources)
	for i, source := range m.transcriptSources {
		if source.kind == transcriptSourceFixed {
			continue
		}
		m.transcript[i] = m.renderTranscriptSource(source, terminalWidth, markers[i])
	}
}
```

(f) Replace `commitTranscriptSource` and add `resyncMarkers`:

```go
func (m *chatTUI) commitTranscriptSource(source transcriptSource) {
	oldMarkers := currentTranscriptMarkers(m.transcriptSources)
	newMarker := markerNone
	switch source.kind {
	case transcriptSourceUser:
		newMarker = markerUserCurrent
	case transcriptSourceMarkdown:
		newMarker = markerAssistantNamed
	case transcriptSourceReplayBundle:
		newMarker = markerAssistantNamed | markerUserCurrent
	}
	rendered := m.renderTranscriptSource(source, m.width, newMarker)
	*m.pendingCommit = append(*m.pendingCommit, rendered)
	m.appendTranscriptBlock(rendered, source)
	m.resyncMarkers(oldMarkers)
}

// resyncMarkers re-renders blocks whose liveness marker changed since
// oldMarkers (bounded: at most two indices change per mutation).
func (m *chatTUI) resyncMarkers(oldMarkers []transcriptMarker) {
	newMarkers := currentTranscriptMarkers(m.transcriptSources)
	n := min(len(oldMarkers), len(newMarkers))
	for i := 0; i < n; i++ {
		if oldMarkers[i] != newMarkers[i] {
			m.setTranscriptBlock(i, m.renderTranscriptSource(m.transcriptSources[i], m.width, newMarkers[i]), m.transcriptSources[i])
		}
	}
}
```

(g) Update `removeTranscriptBlock`: capture `oldMarkers := currentTranscriptMarkers(m.transcriptSources)` at the top (after the bounds check) and call `m.resyncMarkers(oldMarkers)` at the end (after the `liveDirtyIdx` re-index loop).

(h) Update `truncateTranscriptBlocks`: capture `oldMarkers := currentTranscriptMarkers(m.transcriptSources)` at the top and call `m.resyncMarkers(oldMarkers)` at the end (after the `liveDirtyIdx` trim loop).

(i) Replace `buildCopyTranscript`:

```go
func (m chatTUI) buildCopyTranscript(contentWidth int) (string, int, bool) {
	if len(m.transcriptSources) != len(m.transcript) {
		return "", 0, false
	}
	var b strings.Builder
	markers := 0
	live := currentTranscriptMarkers(m.transcriptSources)
	for i, source := range m.transcriptSources {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch source.kind {
		case transcriptSourceMarkdown:
			rendered := renderAssistantMarkdownCopy(source.raw, contentWidth, strconv.Itoa(i), live[i]&markerAssistantNamed != 0)
			markers += strings.Count(rendered, copyMathStartPrefix)
			b.WriteString(rendered)
		case transcriptSourceReplayBundle:
			rendered := m.renderReplayBundleCopy(source, contentWidth, strconv.Itoa(i), live[i])
			markers += strings.Count(rendered, copyMathStartPrefix)
			b.WriteString(rendered)
		default:
			b.WriteString(m.transcript[i])
		}
	}
	return b.String(), markers, true
}
```

- [ ] **Step 6: Wire streaming call sites**

In `internal/cli/chat_tui.go`, update `streamAnswer` (the `else` branch only):

```go
	} else {
		block := m.renderTranscriptSource(source, m.width, currentTranscriptMarkers(m.transcriptSources)[m.answerIdx])
		m.setTranscriptBlock(m.answerIdx, block, source)
		m.transcriptDirty = true
	}
```

Update `commitPending` (the `else` branch only):

```go
	} else {
		block := m.renderTranscriptSource(source, m.width, currentTranscriptMarkers(m.transcriptSources)[m.answerIdx])
		m.setTranscriptBlock(m.answerIdx, block, source)
		m.transcriptDirty = true
	}
```

- [ ] **Step 7: Fix mechanical callers (compile-driven)**

`internal/cli/transcript_test.go`:
- `TestAssistantMarkdownHasIdentityAndIndentedBody`: `renderAssistantMarkdown("A concise answer that wraps across the available width.", 32)` → add `, true`. The `"  ◆ Corvus"` assertion stays.
- The four direct `m.renderTranscriptSource(source, m.width)` calls: replace with `m.renderTranscriptSource(source, m.width, currentTranscriptMarkers([]transcriptSource{source})[0])`.
- `TestReplaySectionsKeepAssistantIdentity`: `replaySectionsFor` now demotes everything — rewrite the assertion block:

```go
	if plain := ansi.Strip(sections[1]); !strings.HasPrefix(plain, "  ◆\n\n  Version 1.2.3") {
		t.Fatalf("demoted replay should keep a bare diamond and drop the name: %q", plain)
	}
```

`internal/cli/chat_tui_test.go`:
- `:1411` → `m.commitLine(renderUserBubble("hello world", m.width, m.planMode, true))`
- `:1441` → `got := renderUserBubble("hello world", 80, false, true)`
- `:3439` → `m.commitLine(renderUserBubble("expanded JSON", m.width, m.planMode, true))`

- [ ] **Step 8: Run the new tests, then the whole package**

Run: `go test ./internal/cli/ -run 'TestAssistantMarkdownHistoryDropsName|TestUserBubbleFadedHistory|TestSecondExchangeDemotesFirst|TestNonLiveCommitsKeepMarkers|TestUnsendRegainsAssistantName|TestRemoveLastAnswerRetagsPrevious|TestReflowPreservesMarkers|TestReplayBundleInternalLiveness' -count=1`
Expected: PASS

Run: `go test ./internal/cli/ -count=1`
Expected: PASS (existing copy/parity tests at transcript_test.go:243/323 must stay green)

- [ ] **Step 9: Commit**

```bash
gofmt -w internal/cli/transcript.go internal/cli/chat_tui.go internal/cli/transcript_test.go internal/cli/chat_tui_test.go
git add internal/cli/transcript.go internal/cli/chat_tui.go internal/cli/transcript_test.go internal/cli/chat_tui_test.go
git commit -m "feat(cli): marker-aware transcript rendering (user fade, assistant name)"
```

---

### Task 4: Tool-card keyword coloring

**Files:**
- Modify: `internal/cli/toolcard.go`
- Modify: `internal/cli/toolcard_test.go`
- Modify: `internal/cli/diffview_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/toolcard_test.go` (add the import `"github.com/charmbracelet/colorprofile"` if not already present):

```go
func TestToolCardVerbAndArgColors(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	read := toolCard("grep", `{"pattern":"TODO"}`, 120)
	if !strings.Contains(read, fgSGR(activeCLITheme.toolRead)) {
		t.Fatalf("read verb should carry toolRead SGR, got %q", read)
	}
	if !strings.Contains(read, fgSGR(activeCLITheme.toolArg)) {
		t.Fatalf("read arg should carry toolArg SGR, got %q", read)
	}
	exec := toolCard("bash", `{"command":"npm test"}`, 120)
	if !strings.Contains(exec, fgSGR(activeCLITheme.warn)) {
		t.Fatalf("bash verb should carry warn SGR, got %q", exec)
	}
	write := toolCard("write_file", `{"path":"a.go"}`, 120)
	if !strings.Contains(write, fgSGR(activeCLITheme.success)) {
		t.Fatalf("write verb should carry success SGR, got %q", write)
	}
	dot := toolCard("wait", `{"job_ids":["j1"]}`, 120)
	if !strings.Contains(dot, fgSGR(activeCLITheme.toolProc)) {
		t.Fatalf("wait dot should stay toolProc-colored, got %q", dot)
	}
}
```

Append to `internal/cli/diffview_test.go`:

```go
func TestDiffHeaderUsesCategoryAndArgColors(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	d := event.FileDiff{Diff: "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n", Added: 1, Removed: 1}
	lines := diffBlock("write_file", `{"path":"x.go"}`, d, 80, 40)
	if len(lines) == 0 {
		t.Fatal("diffBlock returned no lines")
	}
	header := lines[0]
	if !strings.Contains(header, fgSGR(activeCLITheme.success)) {
		t.Fatalf("write diff header verb should carry success SGR, got %q", header)
	}
	if !strings.Contains(header, fgSGR(activeCLITheme.toolArg)) {
		t.Fatalf("diff header path should carry toolArg SGR, got %q", header)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestToolCardVerbAndArgColors|TestDiffHeaderUsesCategoryAndArgColors' -count=1`
Expected: FAIL — verb is plain bold (`bold(label)`), arg is default foreground.

- [ ] **Step 3: Implement**

In `internal/cli/toolcard.go`, replace `toolDot` with `toolCategoryColor` + `toolDot`, and replace `toolHead` (full replacement):

```go
// toolCategoryColor returns the semantic color for a tool's category: reads
// cyan, writes green, shell yellow, process control magenta, everything else
// copper. Shared by the ● dot and the card verb.
func toolCategoryColor(name string) cliColor {
	switch toolCategory[name] {
	case "read":
		return activeCLITheme.toolRead
	case "write":
		return activeCLITheme.success
	case "exec":
		return activeCLITheme.warn
	case "proc":
		return activeCLITheme.toolProc
	default:
		return activeCLITheme.accent
	}
}

// toolDot returns the "●" status glyph coloured by the tool's category so the eye
// can tell reads (cyan) from writes (green), shell (yellow), process control
// (magenta), and everything else (copper) at a glance.
func toolDot(name string) string {
	return themeFg(toolCategoryColor(name), "●")
}
```

```go
// toolHead builds "Verb(arg)" with the verb bold and category-coloured and the
// arg in the toolArg tone, clamped to fit the remaining width; shared by
// toolCard and the diff block header.
func toolHead(name, arg string, width int) string {
	label := toolDisplayName(name)
	head := themeFg(toolCategoryColor(name), bold(label))
	if arg != "" {
		avail := width - 4 - len([]rune(label)) - 2
		head += dim("(") + themeFg(activeCLITheme.toolArg, clampPlain(arg, avail)) + dim(")")
	}
	return head
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestToolCardVerbAndArgColors|TestDiffHeaderUsesCategoryAndArgColors|TestToolCard$|TestToolCardUnknownFallsBackToName' -count=1`
Expected: PASS (plain-text assertions unchanged)

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/cli/toolcard.go internal/cli/toolcard_test.go internal/cli/diffview_test.go
git add internal/cli/toolcard.go internal/cli/toolcard_test.go internal/cli/diffview_test.go
git commit -m "feat(cli): category-colored tool verbs and toolArg-colored args"
```

---

### Task 5: Full-suite verification + visual pass

**Files:** none (verification only)

- [ ] **Step 1: Full suite + vet**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS

Run: `go vet ./internal/cli/`
Expected: no findings

Run: `go test ./... -count=1`
Expected: PASS (whole repo; report any pre-existing failures without fixing them)

- [ ] **Step 2: Visual pass checklist (manual)**

Run `go run ./cmd/corvus` in a terminal and verify:
- Fresh session: first exchange shows full-accent `› question` and `◆ Corvus` answer header.
- Second exchange: first bubble faded, first answer shows bare dim `◆`, second exchange full.
- `Bash`/`Search`/`Update` tool cards: verb colored (yellow/cyan/green), arg in steel tone, `●` dot unchanged.
- `/theme ember` and `/theme aurora`: faded user tint tracks the accent; `/theme sandstone` (light) still readable.
- Four-terminal spot check: Warp / iTerm2 / Windows Terminal / konsole — full/faded/faint pairwise distinguishable.
- Termux/native scrollback (if available): no crash; printed names stay until `/cls`.

- [ ] **Step 3: Fix only genuine bugs found by the visual pass**

If the visual pass reveals behavior that contradicts the spec (§4/§5/§8), treat it as a bug: write a failing test first, then fix. If it only reveals a taste-level color issue, record the proposed hex/xterm change in the spec decision log (docs commit), do not silently change the palette.

- [ ] **Step 4: Final docs commit (only if Step 3 recorded palette changes)**

```bash
git add docs/
git commit -m "docs: record transcript identity visual pass notes"
```
