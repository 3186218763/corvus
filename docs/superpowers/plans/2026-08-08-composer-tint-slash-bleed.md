# Composer Soft Tint & Slash-Menu SGR Bleed — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the composer-field background SGR so slash/`@` completion rows never inherit the input-box tint, and soften the dark composer fill (lighter fallback + gentler relative-bg lift) so it reads as a Codex-like translucent field.

**Architecture:** Two pure `internal/cli` edits. (1) `renderComposerField` appends `ansiReset` after every painted line so the open `48;…` background cannot cross the newline into the first completion row. (2) `inputBoxTintFromBackground` uses named lift/sink ratios (dark 0.16, light 0.10) and dark fallback `inputBoxBG` becomes curated `#2a3140`/236. No layout, completion, or accent changes.

**Tech Stack:** Go (module toolchain), Bubble Tea v2 / bubbles textarea (unchanged), `github.com/charmbracelet/x/ansi` for probed-tint 256 conversion only. Tests: `go test ./internal/cli/`.

**Design spec:** `docs/superpowers/specs/2026-08-08-composer-tint-slash-bleed-design.md`

## Global Constraints

- Touch only `composer_selection.go`, `theme.go`, and the matching tests (`composer_selection_test.go`, `theme_test.go`) plus any hard-coded `#1c2534` / `0.32` pins found by search under `internal/cli/`.
- Do **not** change `renderCompletion`, popup order, raised-composer hold, lipgloss borders, accent palette, or light fallback `#eceff4`/255.
- Dark fallback xterm **236** is hand-picked (curated palette rule). Do **not** run `ansi.Convert256` on `#2a3140` for the palette slot (it maps to low index 23).
- Probed-path xterm still uses `ansi.Convert256` on the *computed* hex (existing rule).
- TDD: failing test first, then minimal implementation, then commit per task.
- `ansiReset` is the package constant `"\033[0m"` in `style.go` — reuse it; do not invent a second reset string.

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/cli/composer_selection.go` | Per-line composer painter; add trailing reset |
| `internal/cli/composer_selection_test.go` | Painter / bleed regression tests |
| `internal/cli/theme.go` | `inputBoxBG` fallback; `inputBoxDarkLift` / `inputBoxLightSink`; `inputBoxTintFromBackground` |
| `internal/cli/theme_test.go` | Fallback pins, pure tint fixtures, probe vs fallback |

---

### Task 1: Trailing SGR reset on composer field lines (bleed fix)

**Files:**
- Modify: `internal/cli/composer_selection.go` (`renderComposerField`, ~552–570)
- Test: `internal/cli/composer_selection_test.go`

**Interfaces:**
- Consumes: `composerFieldBackground() string`, `rearmFieldBackground(s, bg string) string`, `ansiReset`, `bgSGR`, `visibleWidth`, `ansi.Strip`
- Produces: `renderComposerField(view string, width int) string` — same signature; every painted line now ends with `ansiReset`

- [ ] **Step 1: Extend the continuous-background test with trailing-reset + no-bleed asserts**

In `internal/cli/composer_selection_test.go`, update `TestComposerFieldPaintsContinuousBackground` and add a sibling test. Keep existing re-arm / pad / width checks; add end-of-line reset and multi-line bleed guards:

```go
func TestComposerFieldPaintsContinuousBackground(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	bg := composerFieldBackground()
	if bg == "" {
		t.Fatal("composerFieldBackground should be non-empty with color on")
	}
	view := "\x1b[2m❯ \x1b[0mhello\x1b[m"
	got := renderComposerField(view, 12)
	if !strings.HasPrefix(got, bg) {
		t.Fatalf("painted field must open with the background SGR: %q", got)
	}
	if !strings.Contains(got, "\x1b[0m"+bg) {
		t.Fatalf("field must re-arm the background after \\x1b[0m: %q", got)
	}
	if !strings.Contains(got, "\x1b[m"+bg) {
		t.Fatalf("field must re-arm the background after \\x1b[m: %q", got)
	}
	if !strings.Contains(got, bg+"   ") {
		t.Fatalf("right padding must be background-armed: %q", got)
	}
	if w := visibleWidth(ansi.Strip(got)); w != 12 {
		t.Fatalf("painted field visible width = %d, want 12: %q", w, got)
	}
	// Spec: every painted line ends with a full reset so bg cannot leak downward.
	if !strings.HasSuffix(got, ansiReset) {
		t.Fatalf("painted field must end with ansiReset, got tail %q", got[max(0, len(got)-20):])
	}
}

func TestComposerFieldClosesBackgroundBeforeNextRow(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")
	bg := composerFieldBackground()
	if bg == "" {
		t.Fatal("composerFieldBackground should be non-empty with color on")
	}

	// Short line (pad path) and full-width line (no pad path) both must reset.
	for _, tc := range []struct {
		name  string
		view  string
		width int
	}{
		{name: "short", view: "\x1b[2m❯ \x1b[0mhi\x1b[m", width: 20},
		{name: "full", view: strings.Repeat("x", 12), width: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			box := renderComposerField(tc.view, tc.width)
			// Synthetic first completion row: only foreground SGR (like accent("› ")).
			menu := "\x1b[38;5;75m› \x1b[0m/compact"
			joined := box + "\n" + menu
			// Composer segment (before the newline) must end with full reset.
			composerPart, _, ok := strings.Cut(joined, "\n")
			if !ok {
				t.Fatal("joined must contain a newline between composer and menu")
			}
			if !strings.HasSuffix(composerPart, ansiReset) {
				t.Fatalf("composer line must end with ansiReset before menu, tail=%q",
					composerPart[max(0, len(composerPart)-40):])
			}
			// After the line's trailing reset, the open bg must not be the last SGR.
			// Stronger check: the last occurrence of bg in composerPart is followed
			// later by ansiReset (pad path: … reset+bg+spaces+reset).
			if idx := strings.LastIndex(composerPart, bg); idx >= 0 {
				if !strings.Contains(composerPart[idx:], ansiReset) {
					t.Fatalf("last background arm must be closed by ansiReset: %q", composerPart[idx:])
				}
			}
			// Menu line itself does not open with the composer bg SGR.
			if strings.HasPrefix(menu, bg) {
				t.Fatal("test setup error: menu should not start with bg")
			}
			_ = joined
		})
	}

	// Multi-line composer: each physical line ends reset; next line re-opens bg.
	multi := renderComposerField("line1\nline2", 10)
	parts := strings.Split(multi, "\n")
	if len(parts) != 2 {
		t.Fatalf("want 2 painted lines, got %d: %q", len(parts), multi)
	}
	for i, p := range parts {
		if !strings.HasSuffix(p, ansiReset) {
			t.Fatalf("line %d must end with ansiReset: %q", i, p)
		}
		if !strings.HasPrefix(p, bg) {
			t.Fatalf("line %d must re-open with bg: %q", i, p)
		}
	}
}
```

Also strengthen `TestComposerFieldPreservesSelectionStyle` with a one-liner at the end (same file):

```go
	if !strings.HasSuffix(got, ansiReset) {
		t.Fatalf("selection-preserving paint must still end with ansiReset: %q", got)
	}
```

Imports: ensure `"strings"` is already present (it is). `ansiReset` is same package. `max` is Go 1.21+ builtin (repo already uses it in this package).

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestComposerFieldPaintsContinuousBackground|TestComposerFieldClosesBackgroundBeforeNextRow|TestComposerFieldPreservesSelectionStyle' -count=1
```

Expected: FAIL — `painted field must end with ansiReset` (and/or the new test's composer-line suffix assert). Do not implement yet if the failure is missing.

- [ ] **Step 3: Implement trailing reset in `renderComposerField`**

Replace `renderComposerField` in `internal/cli/composer_selection.go` with:

```go
// renderComposerField paints the textarea view as a borderless field: every
// line opens with the field background, re-arms it after each reset, and
// right-pads with background-armed spaces to the full box width so the tint
// reads as one continuous block. Each line ends with a full SGR reset so the
// background cannot leak into the row below (slash completion, panels, status).
// Pass-through when color is off.
func renderComposerField(view string, width int) string {
	bg := composerFieldBackground()
	if bg == "" || width <= 0 {
		return view
	}
	var out strings.Builder
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(bg)
		out.WriteString(rearmFieldBackground(line, bg))
		if w := visibleWidth(ansi.Strip(line)); w < width {
			out.WriteString(ansiReset + bg + strings.Repeat(" ", width-w))
		}
		out.WriteString(ansiReset)
	}
	return out.String()
}
```

Only change vs today: the `out.WriteString(ansiReset)` after the optional pad. Do not alter `rearmFieldBackground` or `composerFieldBackground`.

- [ ] **Step 4: Run painter tests to verify they pass**

```bash
go test ./internal/cli/ -run 'TestComposerField' -count=1
```

Expected: PASS (all `TestComposerField*` including `RespectsNoColor`).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/composer_selection.go internal/cli/composer_selection_test.go
git commit -m "$(cat <<'EOF'
fix(cli): reset composer field SGR so slash menu does not inherit tint

Each painted input line now ends with a full SGR reset so the open
background cannot leak into the first completion row under the box.
EOF
)"
```

---

### Task 2: Soften relative tint ratios + lighten dark fallback

**Files:**
- Modify: `internal/cli/theme.go` (`cliDarkTheme.inputBoxBG`, `inputBoxTintFromBackground`)
- Test: `internal/cli/theme_test.go` (`TestComposerTintAndCursorFollowTheme`, `TestInputBoxTintPureFunctions`, `TestBuildCLIThemeTintUnderProbe`)

**Interfaces:**
- Consumes: `mixHex`, `parseHexColor`, `ansi.Convert256`, `activeBackgroundProbe` (unchanged wiring in `buildCLITheme`)
- Produces:
  - `const inputBoxDarkLift = 0.16`
  - `const inputBoxLightSink = 0.10`
  - `inputBoxTintFromBackground(rgb terminalRGB, dark bool) cliColor` — same signature; uses the constants
  - dark fallback `cliColor{"#2a3140", 236}`

- [ ] **Step 1: Update failing tint / fallback pins in tests**

In `internal/cli/theme_test.go`, apply these exact pin changes:

**`TestComposerTintAndCursorFollowTheme`** — replace dark wantTint and NO_COLOR pin:

```go
			wantTint := cliColor{"#eceff4", 255}
			if theme.mode == "dark" {
				wantTint = cliColor{"#2a3140", 236}
			}
```

and:

```go
	if got := activeCLITheme.inputBoxBG; !reflect.DeepEqual(got, cliColor{"#2a3140", 236}) {
		t.Fatalf("dark theme must keep its tint slot even under NO_COLOR, got %v", got)
	}
```

**`TestInputBoxTintPureFunctions`** — replace the body with:

```go
func TestInputBoxTintPureFunctions(t *testing.T) {
	if got, want := mixHex("#0a0c10", "#ffffff", inputBoxDarkLift), "#313336"; got != want {
		t.Fatalf("mixHex dark lift = %s, want %s", got, want)
	}
	if got, want := mixHex("#f0f2f5", "#000000", inputBoxLightSink), "#d8dadd"; got != want {
		t.Fatalf("mixHex light sink = %s, want %s", got, want)
	}
	if got, want := mixHex("#000000", "#ffffff", 0), "#000000"; got != want {
		t.Fatalf("mixHex t=0 = %s, want %s", got, want)
	}
	if got, want := mixHex("#000000", "#ffffff", 1), "#ffffff"; got != want {
		t.Fatalf("mixHex t=1 = %s, want %s", got, want)
	}
	if got := mixHex("bad", "#ffffff", 0.08); got != "bad" {
		t.Fatalf("mixHex invalid hex = %q, want input returned as-is", got)
	}

	// Dark bg (10,12,16): lift inputBoxDarkLift toward white → #313336.
	if got := inputBoxTintFromBackground(terminalRGB{10, 12, 16}, true); got != (cliColor{hex: "#313336", xterm: 236}) {
		t.Fatalf("dark tint = %+v, want #313336/236", got)
	}
	// Light bg (240,242,245): sink inputBoxLightSink toward black → #d8dadd.
	if got := inputBoxTintFromBackground(terminalRGB{240, 242, 245}, false); got != (cliColor{hex: "#d8dadd", xterm: 253}) {
		t.Fatalf("light tint = %+v, want #d8dadd/253", got)
	}
	// Hue-preserving: deep purple shell stays purple-tinted, not grey.
	if got := inputBoxTintFromBackground(terminalRGB{48, 10, 36}, true); got != (cliColor{hex: "#513147", xterm: 238}) {
		t.Fatalf("purple dark tint = %+v, want #513147/238", got)
	}
	// Extremes: pure black lifted → #292929/235; pure white sunk → #e6e6e6/254.
	if got := inputBoxTintFromBackground(terminalRGB{0, 0, 0}, true); got != (cliColor{hex: "#292929", xterm: 235}) {
		t.Fatalf("black dark tint = %+v, want #292929/235", got)
	}
	if got := inputBoxTintFromBackground(terminalRGB{255, 255, 255}, false); got != (cliColor{hex: "#e6e6e6", xterm: 254}) {
		t.Fatalf("white light tint = %+v, want #e6e6e6/254", got)
	}
}
```

**`TestBuildCLIThemeTintUnderProbe`** — fallback assert only:

```go
	// Outside the probe the curated fallback colors stay.
	if got := resolveCLITheme("dark").inputBoxBG; !reflect.DeepEqual(got, cliColor{"#2a3140", 236}) {
		t.Fatalf("fallback dark inputBoxBG = %v, want #2a3140/236", got)
	}
	if got := resolveCLITheme("light").inputBoxBG; !reflect.DeepEqual(got, cliColor{"#eceff4", 255}) {
		t.Fatalf("fallback light inputBoxBG = %v, want #eceff4/255", got)
	}
```

(The probed branches already compare against `inputBoxTintFromBackground(...)` dynamically — they keep working once ratios change.)

- [ ] **Step 2: Run tint tests to verify they fail**

```bash
go test ./internal/cli/ -run 'TestComposerTintAndCursorFollowTheme|TestInputBoxTintPureFunctions|TestBuildCLIThemeTintUnderProbe' -count=1
```

Expected: FAIL — actual still `#1c2534`/235 and old 0.32/0.15 hex values; or compile error on undefined `inputBoxDarkLift` / `inputBoxLightSink`.

- [ ] **Step 3: Implement ratios + fallback in `theme.go`**

1. Change dark palette slot (~line 83):

```go
		inputBoxBG:   cliColor{"#2a3140", 236},
```

2. Replace `inputBoxTintFromBackground` (~lines 243–264) with:

```go
// Relative compositor lift/sink against the probed terminal background.
// Kept soft so the field reads translucent rather than a hard grey panel.
const (
	inputBoxDarkLift  = 0.16 // toward white on dark shells
	inputBoxLightSink = 0.10 // toward black on light shells
)

// inputBoxTintFromBackground computes the composer fill from the probed
// terminal background, keeping the background's hue: dark shells lift
// inputBoxDarkLift toward white; light shells sink inputBoxLightSink toward
// black for a recessed field. The 256 colour fallback is the nearest xterm
// index via ansi.Convert256 — unlike the curated palette slots, this value
// is computed because it tracks a live background the designer cannot pin.
func inputBoxTintFromBackground(rgb terminalRGB, dark bool) cliColor {
	bg := fmt.Sprintf("#%02x%02x%02x", rgb.r, rgb.g, rgb.b)
	ref := "#ffffff"
	ratio := inputBoxDarkLift
	if !dark {
		ref = "#000000"
		ratio = inputBoxLightSink
	}
	final := mixHex(bg, ref, ratio)
	xterm := 0
	if r, g, b, ok := parseHexColor(final); ok {
		xterm = int(ansi.Convert256(color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0xff}))
	}
	return cliColor{hex: final, xterm: xterm}
}
```

Leave `buildCLITheme` probe hook unchanged. Leave light `inputBoxBG` `#eceff4`/255 unchanged.

- [ ] **Step 4: Run tint + theme tests**

```bash
go test ./internal/cli/ -run 'TestComposerTint|TestInputBoxTint|TestBuildCLIThemeTint|TestComposerField' -count=1
```

Expected: PASS.

If any other test still pins `#1c2534` or `0.32` under `internal/cli/`:

```bash
grep -rn '#1c2534\|0\.32.*white\|inputBoxBG.*235' internal/cli --include='*.go'
```

Update those pins to `#2a3140`/236 or the new fixture table; do not leave stale expects.

- [ ] **Step 5: Full package smoke + commit**

```bash
go test ./internal/cli/ -count=1
```

Expected: PASS (or only pre-existing failures unrelated to this change — if any fail on `inputBoxBG` pins, fix them in this commit).

```bash
git add internal/cli/theme.go internal/cli/theme_test.go
# include any other test files that needed pin updates
git commit -m "$(cat <<'EOF'
style(cli): softer composer tint and lighter dark fallback

Dark relative lift is 16% (light sink 10%) so the field reads translucent;
no-probe dark fallback moves to #2a3140/236 instead of a near-black slab.
EOF
)"
```

---

### Task 3: Verification checklist (no code unless something fails)

**Files:** none expected

- [ ] **Step 1: Targeted regression suite**

```bash
go test ./internal/cli/ -run 'TestComposerField|TestComposerTint|TestInputBoxTint|TestBuildCLIThemeTint' -count=1
```

Expected: PASS.

- [ ] **Step 2: Optional manual TUI check**

```bash
# Ensure color is on in the shell (unset NO_COLOR; TERM not dumb)
go build -o bin/corvus ./cmd/corvus
./bin/corvus
```

Manual:
1. Empty composer: field is a soft lift, not a near-black rectangle.
2. Type `/`: first completion row does **not** share the composer slab background.
3. Esc to dismiss menu: no residual tint on the status row.

- [ ] **Step 3: Confirm git log**

```bash
git log --oneline -5
```

Expected: two feature commits from Tasks 1–2 (plus earlier design commit if present).

---

## Spec coverage (self-review)

| Spec requirement | Task |
|------------------|------|
| Trailing `ansiReset` per composer line | Task 1 |
| No bg bleed into first slash row | Task 1 tests + impl |
| Multi-line re-open bg after reset | Task 1 multi-line assert |
| `NO_COLOR` passthrough | Existing `TestComposerFieldRespectsNoColor` (Task 1 suite) |
| dark lift 0.16 / light sink 0.10 named constants | Task 2 |
| dark fallback `#2a3140`/236 curated | Task 2 |
| light fallback unchanged | Task 2 (explicit non-change) |
| probed path still `Convert256` | Task 2 (unchanged branch) |
| No completion/layout/accent changes | Global constraints + file map |
| Fixture pins for known RGBs | Task 2 test body |

## Placeholder / consistency check

- No TBD/TODO steps.
- Constants named `inputBoxDarkLift` / `inputBoxLightSink` consistently in impl and tests.
- Hex/xterm pins computed with the same `mixHex` + `ansi.Convert256` rules as production.
- `ansiReset` is the existing package constant, not a new string.
