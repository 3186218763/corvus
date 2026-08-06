# TUI Clarity & Keyboard-First — Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a clearer main chat shell with a composer mode badge, lean two-band chrome, Esc priority regression tests, empty-input `?` cheatsheet, and idle-only `Ctrl+P` command palette — without Tasks overlay, statusline schema v1, or big-bang `chat_tui.go` rewrite.

**Architecture:** Keep Bubble Tea `chatTUI` as the model. Move mode chrome from the footer interaction row onto a left-of-composer badge that reuses `modeTagText()` + existing mode colors. Thin default footer data to context/model/jobs/effort/work; keep balance, cache detail, and git porcelain on `/status` (already a transcript commit). Add two lightweight overlays: `cheatsheet` (static sections) and command palette (new `quickPicker` kind + curated actions). Esc stays an ordered if-chain; formal `overlayStack` is out of P1.

**Tech Stack:** Go, Bubble Tea v2, Lip Gloss v2, existing `internal/cli` helpers (`quick_picker`, `status_footer`, `theme`, `i18n`), `go test ./internal/cli/...`.

**Spec:** `docs/superpowers/specs/2026-08-06-tui-clarity-keyboard-design.md` (**P1 only** — §5.3–5.4, §7–8, §11–12 P1 rows).  
**Out of this plan:** P2 Tasks / `Ctrl+G`, P2.1 Attach, P3 palette↔slash registry + statusline dual-write, P4 performance.

## Global Constraints

- Keyboard-complete; mouse is optional. No primary click-only paths.
- Linear shell + modal overlays; **no** sidebar / dual-pane / Agent Dashboard.
- Mode badge text must come from **`modeTagText()`** (desktop vs classic layout parity). Shell uses literal `Shell` when `!` prefix (existing View rule).
- Footer: **at most two** built-in bands (interaction + optional data). Mode must **not** double-display after badge lands.
- Default data band keeps: **context%**, **model**, **jobs when > 0**, effort/work if space. **Not** on default chrome: balance, cache diagnostics detail, full git porcelain (move/keep on `/status`).
- Custom `[statusline].command` still replaces the **data** band; interaction row + mode badge stay independent.
- Esc never flips Plan/Ask/YOLO. Double-Esc rewind window **600ms**; double-Ctrl+C quit **1500ms** (do not change unless fixing a proven bug).
- `Ctrl+P` / `Ctrl+N`: prev/next inside completion menus, quick pickers, approval lists. **Only** main-shell idle (no modal, not running, no approval) opens the command palette.
- Empty-input `?` only when `strings.TrimSpace(composer) == ""`; non-empty inserts `?`.
- P1 palette is a **curated** action list (spec §8.2.2 minus Tasks). No invented unbounded catalog; no gray “coming soon” Tasks row.
- Prefer extending **`quick_picker`** for the palette; do not fork a second fuzzy list widget.
- New UI for help/palette lives in dedicated files under `internal/cli/`; do not split all of `chat_tui.go`.
- `View`, `computeStatusLineCount`, `bottomRows`, and `hideComposer` must stay in lockstep after layout changes.
- i18n: user-visible section titles/hints through existing `i18n` patterns; **key chords stay literal English** (`Ctrl+P`, `Shift+Tab`).
- TDD: failing test → run → implement → run → commit per task.
- YAGNI: no overlayStack, no Tasks, no statusline schema v1, no new theme names, no user keymap file.

## File map

| File | Responsibility |
|------|----------------|
| `internal/cli/chat_tui.go` | Wire badge into View; idle `?` / `Ctrl+P`; modal field(s); Esc order unchanged; `hideComposer`/`bottomRows`/`computeStatusLineCount`; palette dispatch; `/status` polish if needed |
| `internal/cli/status_footer.go` | Lean inventory: no mode in primary; drop git/balance/cache from default data band; keep packing helpers |
| `internal/cli/theme.go` (+ `style.go` if needed) | Hierarchy: body vs chrome contrast; quieter tool borders; badge colors reuse existing mode colors |
| `internal/cli/gitstatus.go` | Existing mode colors (`statusAutoColor` / plan / yolo / shell); optional richer compound badge color helper if tests require |
| `internal/cli/quick_picker.go` | Add `quickPickerCommand` kind (or equivalent); keep Ctrl+P/N nav |
| `internal/cli/command_palette.go` | Curated actions, open/close helpers, run selected action |
| `internal/cli/cheatsheet.go` | Static `?` overlay content + render + open/close |
| `internal/cli/help_view.go` | Optional one-line note that `?` is the keyboard cheatsheet; keep `/help` transcript dump for P1 |
| `internal/i18n/{i18n,messages_en,messages_zh,messages_zh_tw}.go` | Short hints (`? help`), cheatsheet section titles if localized |
| `internal/cli/status_footer_test.go` | Rewrite expectations for lean chrome |
| `internal/cli/chat_tui_test.go` | Badge, Esc table, `?`, palette idle vs modal, draft safety |
| `internal/cli/quick_picker_test.go` / new `*_test.go` | Palette filter + action run |
| `internal/cli/cheatsheet_test.go` | Content + empty/non-empty `?` behavior |
| `internal/cli/statusline_test.go` | Update if idle/hint strings change |

---

### Task 1: Esc stack priority regression tests (document current if-chain)

**Files:**
- Modify: `internal/cli/chat_tui_test.go`
- (No production change unless a test exposes a real Esc/mode bug)

**Interfaces:**
- Consumes: existing `update` modal if-chain order in `chat_tui.go` (~chooser → rewind → mcpImport → copyPick → resumePick → quickPick → mcp → clearConfirm → skillPick → pendingApproval → completion → main Esc)
- Produces: locked regression tests for P1 Esc rules from spec §7.2 items that already exist (completion close, cancel turn, idle clear, double-Esc rewind, no mode flip)

- [ ] **Step 1: Write the failing / locking tests**

```go
func TestEscPriorityCompletionClosesBeforeCancelWhenIdle(t *testing.T) {
	m := newTestChatTUI()
	m.completion = completion{active: true, kind: compSlash, items: []compItem{{label: "/status"}}, sel: 0}
	next, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(chatTUI)
	if m.completion.active {
		t.Fatal("Esc should close completion menu when idle")
	}
}

func TestEscIdleClearsDraftDoesNotFlipPlan(t *testing.T) {
	m := newTestChatTUI()
	// Ensure controller exists for plan mode (use newTestChatTUIWithMessages or wire ctrl like other mode tests).
	// Set planMode true, type draft, Esc once → draft cleared, planMode still true.
}

func TestDoubleEscOpensRewindWithin600ms(t *testing.T) {
	m := newTestChatTUI()
	// empty composer; first Esc arms lastEsc; second within 600ms → m.rewind != nil
	// (match existing openRewind tests' controller setup requirements)
}

func TestEscNeverChangesYoloOrPlan(t *testing.T) {
	// Extend or re-assert TestEscInPlanModeDoesNotExitPlan + YOLO idle Esc
}
```

If `TestEscInPlanModeDoesNotExitPlan` already covers plan, keep it and only add gaps (completion idle close, double-Esc if missing).

- [ ] **Step 2: Run tests**

```bash
export PATH="$HOME/.local/go/bin:$PATH"
cd /home/miku/dv_project/mk_agent
go test ./internal/cli/ -count=1 -run 'TestEsc|TestDoubleEsc' -v
```

Expected: existing green; new tests either green (lock current) or red only if they assert missing behavior you will not implement in this task.

- [ ] **Step 3: Fix only if a test proves Esc flips mode or drops draft incorrectly**

Do **not** introduce `overlayStack` in this task.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/chat_tui_test.go
git commit -m "test(cli): lock Esc priority and mode non-flip for TUI P1"
```

---

### Task 2: Theme hierarchy (body vs chrome)

**Files:**
- Modify: `internal/cli/theme.go` (palette contrast values)
- Modify: `internal/cli/style.go` if tool-card / user bubble styles live there
- Test: `internal/cli/status_footer_test.go` (`TestStatusFooterSemanticPaletteAcrossThemes` must still pass geometry)

**Interfaces:**
- Produces: slightly higher contrast `muted` (body-adjacent) vs quieter `subtle`/`border` for chrome; no new theme **name**

- [ ] **Step 1: Write a small regression test that hierarchy intent is preserved**

```go
func TestThemeHierarchyBodyBrighterThanChromeBorder(t *testing.T) {
	// For dark graphite: muted/faint used for body-ish text should not equal border.
	// Assert activeCLITheme.muted.hex != activeCLITheme.border.hex and border stays low-chroma.
	// Keep this structural — do not hardcode every hex if themes already have helpers.
}
```

- [ ] **Step 2: Run — may pass already; if so, still apply intentional contrast tweaks and keep geometry tests green**

```bash
go test ./internal/cli/ -count=1 -run 'TestStatusFooterSemanticPalette|TestThemeHierarchy' -v
```

- [ ] **Step 3: Adjust dark/light palette**

In `theme.go` `cliDarkTheme` / `cliLightTheme`:
- Raise body-adjacent text slightly if currently too close to chrome.
- Soften `border` if tool cards feel loud (existing tool styles that use `border`).
- Do **not** add a new style name to `cliThemeStyles`.

- [ ] **Step 4: Re-run palette + footer geometry tests**

```bash
go test ./internal/cli/ -count=1 -run 'TestStatusFooter' -v
```

Expected: PASS (geometry identical across themes still holds).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/theme.go internal/cli/style.go internal/cli/*_test.go
git commit -m "style(cli): improve TUI visual hierarchy without new themes"
```

---

### Task 3: Composer mode badge + remove mode from footer primary

**Files:**
- Modify: `internal/cli/chat_tui.go` (`View`, `computeStatusLineCount`, composer render path)
- Modify: `internal/cli/status_footer.go` (`primaryStatusLine` signature / body — stop prefixing `modeTag`)
- Modify: `internal/cli/status_footer_test.go`, `internal/cli/statusline_test.go`, `internal/cli/chat_tui_test.go`

**Interfaces:**
- Consumes: `modeTagText()`, `modeTagStyle`, `statusAutoColor` / plan / yolo / shell colors
- Produces:
  - Composer row shows badge **left of** input box (same bottom region)
  - `primaryStatusLine` no longer starts with mode pill
  - Idle hints may keep `shift-tab` wording without repeating current mode name

- [ ] **Step 1: Write failing tests**

```go
func TestComposerModeBadgeUsesModeTagText(t *testing.T) {
	m := newTestChatTUI()
	// Wire ctrl + planMode like existing mode tests so modeTagText() == "Plan"
	// Render View or a pure helper renderComposerWithBadge() and assert ansi.Strip contains "Plan"
	// Assert primaryStatusLine / renderStatusBlock does NOT start with the mode pill text.
}

func TestShellPrefixBadgeIsShell(t *testing.T) {
	m := newTestChatTUI()
	m.input.SetValue("!ls")
	// badge text Shell; modeTagText not required
}

func TestDesktopModeTagParityOnBadge(t *testing.T) {
	// When UIShortcutLayout desktop, Don't Ask / Ask defaults match modeTagText()
}
```

- [ ] **Step 2: Run — expect FAIL (badge not on composer yet)**

```bash
go test ./internal/cli/ -count=1 -run 'TestComposerModeBadge|TestShellPrefixBadge|TestDesktopModeTag' -v
```

- [ ] **Step 3: Implement badge layout**

Recommended pure helper (keeps View thinner):

```go
// renderModeBadge returns the styled composer-left mode chip.
func (m chatTUI) renderModeBadge(shellMode bool) string {
	if shellMode {
		return modeTagStyle(statusShellColor, modeTagLight).Render("Shell")
	}
	bg, fg := statusAutoColor, modeTagDark
	switch {
	case m.ctrl != nil && m.ctrl.AutoApproveTools():
		bg, fg = statusYoloColor, modeTagLight
	case m.planMode:
		bg, fg = statusPlanColor, modeTagLight
	}
	return modeTagStyle(bg, fg).Render(m.modeTagText())
}
```

In `View`:
- Build `badge := m.renderModeBadge(shellMode)`.
- Compose `badge + " " + box` (or lipgloss join) so focus/cursor math accounts for badge width **or** place badge on its own left column with fixed width.
- Update cursor X offset if badge shares the composer row (critical: `composerCursor` offsets).

In `primaryStatusLine`:
- Change signature to drop `modeTag` **or** ignore it.
- Start with indent + interaction state only (rewind / approval / idle / short hints).
- Remove long mode cycle prose that restates the current mode; keep compact `Shift+Tab` / `Ctrl+Y` hint strings (update i18n if shortened).

Update `computeStatusLineCount` so mode padding is **not** fake-injected into the status primary line.

- [ ] **Step 4: Fix all footer tests that assumed mode pill in row 0**

Search:

```bash
go test ./internal/cli/ -count=1 -run 'TestStatusFooter|TestStatusline|TestComposerMode' -v
```

Rewrite assertions: mode may appear in View composer region; footer primary should not duplicate badge text.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/chat_tui.go internal/cli/status_footer.go internal/cli/*_test.go internal/i18n/*.go
git commit -m "feat(cli): move mode chrome to composer badge"
```

---

### Task 4: Lean default chrome inventory + `/status` host for moved detail

**Files:**
- Modify: `internal/cli/status_footer.go` (`statusTelemetryGroups`, `layoutGitTelemetry` / callers)
- Modify: `internal/cli/chat_tui.go` (`showStatusDetails` only if a field is missing)
- Modify: `internal/cli/status_footer_test.go` (BAL/git/cache no longer on default chrome)
- Modify: `internal/cli/chat_tui_test.go` (`TestStatusCommandShowsRuntimeDetails` — ensure git/cache/balance still listed)

**Interfaces:**
- Produces: default data band = context (+ compact headroom) + jobs if > 0; model/effort/work stay on interaction row right group as today **or** move model into data band per packing — match spec §5.3: data row holds context%, model, jobs; effort/work if space.
- `/status` remains transcript commit; must still show balance, cache, git, mode, model, context, jobs, mouse.

- [ ] **Step 1: Write failing inventory tests**

```go
func TestStatusFooterDefaultOmitsBalanceGitCache(t *testing.T) {
	m := newTestChatTUI()
	// attach ctrl stub with context if needed
	m.balance = "¥12.34"
	m.gitStatus = gitStatus{Repo: "Corvus", Branch: "main", Added: 1}
	// force cache metrics if test helpers exist; otherwise set fields used by cacheStatus()
	plain := ansi.Strip(m.renderStatusBlock(m.primaryStatusLine(/*no mode*/ false, false), 100))
	for _, banned := range []string{"BAL", "¥12.34", "Corvus", "main"} {
		// After lean chrome, these must not appear on default footer.
		// Cache label: i18n.M.ChatStatusCacheLabel
	}
	// Still expect CTX / MODEL / WORK as applicable
}

func TestStatusCommandStillShowsMovedFields(t *testing.T) {
	m := newTestChatTUI()
	m.balance = "$10.00"
	m.gitStatus = gitStatus{Repo: "Corvus", Branch: "feature"}
	// set cache if possible
	m.runSlashCommand("/status")
	out := ansi.Strip(strings.Join(m.transcript, "\n"))
	for _, want := range []string{"$10.00", "feature", "Session status"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in /status:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL on omit test**

```bash
go test ./internal/cli/ -count=1 -run 'TestStatusFooterDefaultOmits|TestStatusCommandStillShows' -v
```

- [ ] **Step 3: Implement lean inventory**

In `statusTelemetryGroups`:
- Keep context groups + jobs.
- **Remove** balance append.
- **Remove** cache status metric from default footer (detail stays in `/status` via `cacheTag()`).

In `layoutGitTelemetry` / `renderStatusBlock`:
- Stop rendering git on the default data band (skip `layoutGitTelemetry` git branch; only pack telemetry groups).
- Preserve packing helpers (`packStatusGroups`, narrow wrap) for remaining metrics.

Custom statusline path: unchanged contract (script replaces data band).

`showStatusDetails`: verify git/cache/balance already present (they are). Optionally add a one-line note `details: /status` is not required. If git empty-string when repo unset, fine.

- [ ] **Step 4: Rewrite `status_footer_test.go` cases that assert BAL/git on default chrome**

Every test that set `m.balance` / `m.gitStatus` expecting them in `renderStatusBlock` must either:
- assert they are **absent**, or
- call `showStatusDetails` / a dedicated detail renderer for those fields.

Keep narrow-width packing tests but with context/model/jobs only.

- [ ] **Step 5: Full cli package test slice**

```bash
go test ./internal/cli/ -count=1 -run 'TestStatusFooter|TestStatusCommand|TestStatusline' -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/status_footer.go internal/cli/chat_tui.go internal/cli/*_test.go
git commit -m "feat(cli): lean default status chrome; keep detail on /status"
```

---

### Task 5: Empty-input `?` cheatsheet overlay

**Files:**
- Create: `internal/cli/cheatsheet.go`
- Create: `internal/cli/cheatsheet_test.go`
- Modify: `internal/cli/chat_tui.go` (field `cheatsheetOpen bool` or `cheatsheet *cheatsheetView`, key handling, View render, `hideComposer`, `bottomRows`)
- Modify: `internal/i18n/*` for section titles if localized

**Interfaces:**
- Produces:
  - `func cheatsheetContent() string` or structured sections
  - Open when idle, no higher modal, composer empty, key `?`
  - Esc closes; does **not** clear draft (draft already empty)
  - Non-empty composer: `?` inserts via normal textarea path

- [ ] **Step 1: Write failing tests**

```go
func TestCheatsheetOpensOnQuestionWhenComposerEmpty(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiIdle
	m.input.SetValue("")
	next, _ := m.update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = next.(chatTUI)
	if !m.cheatsheetOpen { // exact field name per implementation
		t.Fatal("expected cheatsheet open")
	}
	// draft still empty
	if m.input.Value() != "" {
		t.Fatalf("draft changed: %q", m.input.Value())
	}
}

func TestCheatsheetInsertsQuestionWhenComposerNonEmpty(t *testing.T) {
	m := newTestChatTUI()
	m.input.SetValue("hello")
	next, _ := m.update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = next.(chatTUI)
	if m.cheatsheetOpen {
		t.Fatal("must not open cheatsheet when non-empty")
	}
	// After full Update path, value should contain '?' — if textarea needs Update plumbing,
	// drive the same path production uses.
}

func TestCheatsheetEscCloses(t *testing.T) {
	m := newTestChatTUI()
	m.cheatsheetOpen = true
	next, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(chatTUI)
	if m.cheatsheetOpen {
		t.Fatal("Esc should close cheatsheet")
	}
}

func TestCheatsheetListsCriticalBindings(t *testing.T) {
	body := ansi.Strip(renderCheatsheet(80))
	for _, want := range []string{"Ctrl+P", "Shift+Tab", "Ctrl+Y", "Ctrl+B", "Ctrl+O", "Esc", "/status", "?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("cheatsheet missing %q:\n%s", want, body)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
go test ./internal/cli/ -count=1 -run 'TestCheatsheet' -v
```

- [ ] **Step 3: Implement `cheatsheet.go`**

Static sections (P1, no search):
1. Navigation / composer (Enter, Esc, PgUp/PgDn)
2. Modes (`Shift+Tab`, `Ctrl+Y`)
3. Transcript (`Ctrl+B`, `Ctrl+O`)
4. Discoverability (`?`, `Ctrl+P`, `/`, `/status`)
5. Session (`/resume`, `/new`, … short list)

Render with `choicePanelStyle` (same family as pickers).

- [ ] **Step 4: Wire into `chat_tui.go`**

- Field: `cheatsheetOpen bool` (or pointer).
- In modal if-chain: after completion? Prefer **before** main key switch: if `cheatsheetOpen`, only Esc/scroll close — block palette.
- In idle key path: if key is `?` / Text `?` and `strings.TrimSpace(m.input.Value())==""` and no higher modal and not running → open and return (do not insert).
- `hideComposer`: **false** recommended (composer stays visible under overlay) **or** true if panel replaces bottom — pick one and keep draft. Spec: opening overlays must not destroy draft (already empty for `?`).
- View: if open, render cheatsheet panel above status block (same place as completion/quickPick).
- Esc stack: closing cheatsheet is higher priority than cancel/clear (spec §7.2 item 2).

- [ ] **Step 5: Run tests**

```bash
go test ./internal/cli/ -count=1 -run 'TestCheatsheet|TestEsc|TestInputOwned' -v
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli/cheatsheet.go internal/cli/cheatsheet_test.go internal/cli/chat_tui.go internal/i18n/*.go
git commit -m "feat(cli): empty-input ? keyboard cheatsheet overlay"
```

---

### Task 6: Idle `Ctrl+P` command palette via `quick_picker`

**Files:**
- Modify: `internal/cli/quick_picker.go` (add kind `quickPickerCommand`)
- Create: `internal/cli/command_palette.go`
- Create: `internal/cli/command_palette_test.go`
- Modify: `internal/cli/chat_tui.go` / `provider.go` (`handleQuickPickerKey` dispatch for command kind)
- Modify: `internal/cli/quick_picker_test.go` if needed

**Interfaces:**
- Produces:
  - `func (m *chatTUI) openCommandPalette()`
  - `func commandPaletteItems() []quickPickerItem` — curated IDs from spec §8.2.2 **without Tasks**
  - On confirm: run action (open cheatsheet, `/status`, model picker, resume, verbose, mouse, mcp, skills, compact, clear/new as available today)
  - Ctrl+P only when: `state==tuiIdle`, no modal fields, `!completion.active`, `!cheatsheetOpen` (or cheatsheet closes first — prefer not open while cheatsheet open)

Curated IDs (stable strings for tests):

| ID | Action |
|----|--------|
| `help` | Open cheatsheet |
| `status` | `showStatusDetails` / run `/status` |
| `model` | existing model picker entrypoint |
| `resume` | existing resume picker entrypoint |
| `verbose` | `toggleVerboseReasoning` |
| `mouse` | `toggleMouseCapture` if exposed |
| `mcp` | open MCP manager if exposed |
| `skills` | open skill picker if exposed |
| `compact` | trigger compact flow if safe when idle |
| `clear` / `new` | only if existing slash flows are callable without surprise |

Omit any action whose entrypoint does not exist yet — **do not** stub Tasks.

- [ ] **Step 1: Write failing tests**

```go
func TestCommandPaletteOpensOnCtrlPWhenIdle(t *testing.T) {
	m := newTestChatTUI()
	m.state = tuiIdle
	next, _ := m.update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(chatTUI)
	if m.quickPick == nil || m.quickPick.kind != quickPickerCommand {
		t.Fatalf("expected command palette, got %#v", m.quickPick)
	}
}

func TestCommandPaletteDoesNotOpenWhenCompletionActive(t *testing.T) {
	m := newTestChatTUI()
	m.completion = completion{active: true, items: []compItem{{label: "/model"}, {label: "/mcp"}}, sel: 1}
	next, _ := m.update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(chatTUI)
	if m.quickPick != nil {
		t.Fatal("Ctrl+P must move completion, not open palette")
	}
	if m.completion.sel != 0 {
		t.Fatalf("expected completion sel 0 after ctrl+p, got %d", m.completion.sel)
	}
}

func TestCommandPaletteEscClosesPreservesDraft(t *testing.T) {
	m := newTestChatTUI()
	m.input.SetValue("keep me")
	m.quickPick = &quickPicker{kind: quickPickerCommand, title: "Commands", items: commandPaletteItems()}
	next, _ := m.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(chatTUI)
	if m.quickPick != nil {
		t.Fatal("Esc should close palette")
	}
	if m.input.Value() != "keep me" {
		t.Fatalf("draft lost: %q", m.input.Value())
	}
}

func TestCommandPaletteStatusAction(t *testing.T) {
	m := newTestChatTUI()
	// open palette, select status item, enter → transcript contains Session status
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
go test ./internal/cli/ -count=1 -run 'TestCommandPalette|TestCompletionMenuCtrlPN' -v
```

- [ ] **Step 3: Implement palette**

`quick_picker.go`:

```go
const (
	// ...
	quickPickerCommand quickPickerKind = "command"
)
```

`command_palette.go`:

```go
func commandPaletteItems() []quickPickerItem { /* curated */ }

func (m *chatTUI) openCommandPalette() {
	m.quickPick = &quickPicker{
		kind:  quickPickerCommand,
		title: "Commands",
		hint:  "Type to filter · ↑/↓ · Enter run · Esc cancel",
		items: commandPaletteItems(),
	}
}

func (m *chatTUI) runCommandPaletteItem(id string) tea.Cmd {
	switch id {
	case "help":
		m.cheatsheetOpen = true
	case "status":
		m.showStatusDetails()
	// ...
	}
	return nil
}
```

In `handleQuickPickerKey` (or equivalent): when kind is `quickPickerCommand` and choice non-nil, call `runCommandPaletteItem`.

In main key switch (only after modal if-chain fails to claim the key):

```go
case "ctrl+p":
	if m.state == tuiIdle && m.quickPick == nil && !m.completion.active && /* no other modals */ {
		m.openCommandPalette()
		return m, nil
	}
```

Because completion already handles `ctrl+p` earlier, idle-only open is automatic if placed after the completion block.

- [ ] **Step 4: Run full palette + completion + quick_picker tests**

```bash
go test ./internal/cli/ -count=1 -run 'TestCommandPalette|TestCompletionMenuCtrlPN|TestQuickPicker' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/quick_picker.go internal/cli/command_palette.go internal/cli/command_palette_test.go internal/cli/chat_tui.go internal/cli/provider.go internal/cli/*_test.go
git commit -m "feat(cli): idle Ctrl+P command palette via quick_picker"
```

---

### Task 7: Integration polish + package verification

**Files:**
- Modify: any remaining View/bottomRows mismatches
- Modify: `help_view.go` optional note
- Test: broad `go test ./internal/cli/...`

**Interfaces:**
- Produces: P1 acceptance checklist green

- [ ] **Step 1: Manual acceptance checklist as tests where possible**

| Check | Automated? |
|-------|------------|
| Keyboard: cycle plan, YOLO, status, help | partial (existing + new) |
| Mode on badge only | Task 3 tests |
| Footer lean | Task 4 tests |
| `?` + `Ctrl+P` | Task 5–6 |
| Ctrl+P in completion still nav | Task 6 |
| Custom statusline still replaces data | existing `TestStatusFooterCustomLineStillReplacesBuiltInData` |
| `computeStatusLineCount` matches View | existing height tests — re-run |

- [ ] **Step 2: Run full package tests**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
cd /home/miku/dv_project/mk_agent
go test ./internal/cli/ -count=1
```

Expected: PASS. Fix any failures before claiming done.

- [ ] **Step 3: Build binary**

```bash
make build
# or: CGO_ENABLED=0 go build -o bin/corvus ./cmd/corvus
```

- [ ] **Step 4: Commit any residual fixes**

```bash
git add -A internal/cli internal/i18n
git commit -m "test(cli): finish TUI clarity P1 verification polish"
```

---

## Spec coverage (self-review)

| Spec requirement (P1) | Task |
|----------------------|------|
| Composer mode badge + taxonomy via `modeTagText` | Task 3 |
| Footer lean inventory; mode not doubled | Tasks 3–4 |
| balance/cache/git → `/status` | Task 4 |
| Esc stack tests; no mode flip; 600ms / 1500ms | Task 1 |
| Empty-input `?` cheatsheet | Task 5 |
| Idle `Ctrl+P` palette; modal Ctrl+P/N unchanged | Task 6 |
| Theme hierarchy, no new skin | Task 2 |
| `/status` enhanced host for moved detail | Task 4 |
| No overlayStack required | All tasks |
| No Tasks / P2–P4 | Explicitly omitted |

## Placeholder / consistency scan

- No TBD tasks; curated palette list is explicit.
- `quickPickerCommand` kind name is the single name used in tests and wiring.
- Cheatsheet field name: implement as `cheatsheetOpen bool` unless a richer struct is needed for scroll — if scroll is needed, use `cheatsheet struct{ open bool; scroll int }` and update tests accordingly in the same task.
- Desktop vs classic `modeTagText` defaults (`Ask` vs `Auto`) are intentional product behavior — badge must not “normalize” them.

## Execution notes

- Work on a feature branch or worktree (`using-git-worktrees`) before large edits.
- Prefer small commits per task as listed.
- If `status_footer_test.go` churn is large, keep behavior changes in Task 4 only so Task 3 diffs stay reviewable.
- Do not start P2 Tasks overlay in this plan.

---

Plan complete and saved to `docs/superpowers/plans/2026-08-06-tui-clarity-keyboard-p1.md`.
