# Codex 风格工具卡渲染（diff + bash 高亮）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 corvus 工具卡渲染对齐 Codex：bash 命令 chroma 语法高亮（catppuccin 主题 + flags 补色 + `⎿` 续行），文件卡标题改 `● Added/Deleted/Edited path (+N -M)`，diff 背景色对齐 Codex 调色板、删除行内容变暗。

**Architecture:** 全部改动在 `internal/cli`。`diffview.go` 负责 chroma 主题选择（catppuccin-mocha/latte）、`tokeniseBash`（bash 词法 + flags 重标为 `NameAttribute`）、`highlightBash`、文件卡 header 构造与删除行 dim；`toolcard.go` 的 `toolCard` 对 `bash` 工具分流到新的 `bashToolCard`（高亮 + 多行 `⎿` 续行）；`theme.go` 只改 4 个 diff 背景色值。事件结构不动。

**Tech Stack:** Go 1.24、chroma v2.27.0（已依赖）、charmbracelet/x/ansi（已依赖）、Bubble Tea TUI。

**Spec:** `docs/superpowers/specs/2026-08-07-codex-diff-bash-render-design.md`（已批准，commit `0264092`）

**已核实的 chroma v2.27.0 API 事实**（写代码前先看，避免走弯路）：
- `chroma.Iterator` 是 `func() Token` 闭包，末尾返回 `chroma.EOF`；`(i Iterator).Tokens() []Token` 存在。
- `styles.Get("catppuccin-mocha")` / `styles.Get("catppuccin-latte")` 均存在（v2.27.0 内置）。
- 自定义 token 颜色：`style.Builder().AddEntry(chroma.NameAttribute, chroma.StyleEntry{Colour: chroma.MustParseColour("#f9e2af")}).Build()`，返回 `(*Style, error)`。
- chroma bash lexer 把 `&&`/`=`/`[`/`]` 标为 `Operator`，`if/then/fi` 标为 `Keyword`，`export/test/true` 标为 `NameBuiltin`，`GOFLAGS` 标为 `NameVariable`，字符串标为 `LiteralStringDouble/Single`，`;`/`|` 标为 `Punctuation`（Punctuation 无样式定义，保持默认前景）；`--flag`/`-x` 留在 `Text`（需要本计划的重标规则）。
- catppuccin-mocha（dark）：Keyword `#cba6f7`、NameBuiltin `#89dceb`、NameVariable `#f5e0dc`、String `#a6e3a1`、Number `#fab387`、Operator 加粗 `#89dceb`；catppuccin-latte（light）：Keyword `#8839ef`、NameBuiltin `#04a5e5`、NameVariable `#dc8a78`、String `#40a02b`、Number `#fe640b`、Operator 加粗 `#04a5e5`。
- Codex 调色板：dark 背景 `#213A2B`/`#4A221D`（256 索引 22/52），light 背景 `#dafbe1`/`#ffebe9`（256 索引 194/224）。
- 测试辅助：`configureCLITheme("dark"|"light")`（`theme.go:154`）、`restoreThemeForTest(prevProfile, prevTheme)`（`theme_test.go:441`）、`activeColorProfile`（`style.go`）、`colorprofile.ANSI256` / `colorprofile.ASCII`。
- 现有测试常量：`ansiBold`（`style.go`）、`fgSGR`/`bgSGR`（`theme.go`）。本计划新增 `ansiDim = "\033[2m"`。

---

### Task 1: diff 语法主题切到 catppuccin

**Files:**
- Modify: `internal/cli/diffview.go`（`activeDiffChromaStyle`）
- Test: `internal/cli/diffview_test.go`（`TestActiveDiffChromaStyleFollowsCLITheme`）

- [ ] **Step 1: 改测试期望（先红）**

把 `internal/cli/diffview_test.go` 里 `TestActiveDiffChromaStyleFollowsCLITheme` 的 `want` 改为：

```go
		{name: "dark", theme: cliDarkTheme, want: "catppuccin-mocha"},
		{name: "light", theme: cliLightTheme, want: "catppuccin-latte"},
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run TestActiveDiffChromaStyleFollowsCLITheme -v
```
Expected: FAIL（`diff syntax style = "github-dark", want "catppuccin-mocha"`）。

- [ ] **Step 3: 实现**

`internal/cli/diffview.go` 的 `activeDiffChromaStyle()`（当前在文件顶部、`activeDiffChromaStyle` 处）：

```go
// Resolve on each render so runtime theme switches and theme-sweep preview
// frames cannot retain syntax colours from the previous light/dark mode.
// catppuccin matches Codex's adaptive default (mocha dark / latte light).
func activeDiffChromaStyle() *chroma.Style {
	if activeCLITheme.name == "light" {
		return styles.Get("catppuccin-latte")
	}
	return styles.Get("catppuccin-mocha")
}
```

同时删掉函数上方现在没用的 `chroma.Dark`/`chroma.Light` 相关注释引用（`mode := chroma.Dark ... styles.GetForMode("github-dark", mode)` 整段替换）。`styles` import 不变。

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run TestActiveDiffChromaStyleFollowsCLITheme -v
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/cli/diffview.go internal/cli/diffview_test.go
git commit -m "feat(cli): switch diff syntax theme to catppuccin (mocha/latte)"
```

---

### Task 2: bash 词法 + flags 重标（tokeniseBash）

**Files:**
- Modify: `internal/cli/diffview.go`
- Test: `internal/cli/diffview_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/cli/diffview_test.go` 末尾追加：

```go
func TestTokeniseBashFlags(t *testing.T) {
	tokens := tokeniseBash(`git add . && git commit -m "fix" --no-verify`)
	var flags []string
	for _, tk := range tokens {
		if tk.Type == chroma.NameAttribute {
			flags = append(flags, tk.Value)
		}
	}
	found := false
	for _, f := range flags {
		if f == "--no-verify" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --no-verify re-tagged as NameAttribute, flags=%v tokens=%+v", flags, tokens)
	}
}

func TestTokeniseBashSkipsFlagsInsideStrings(t *testing.T) {
	for _, tk := range tokeniseBash(`echo "--keep" '--also'`) {
		if tk.Type == chroma.NameAttribute {
			t.Fatalf("flag inside a quoted string must stay plain: %+v", tk)
		}
	}
}

func TestTokeniseBashLeavesOperatorTokens(t *testing.T) {
	tokens := tokeniseBash(`a && b || c`)
	ops := 0
	for _, tk := range tokens {
		if tk.Type == chroma.Operator {
			ops++
		}
	}
	if ops != 2 {
		t.Fatalf("expected 2 operator tokens (&&, ||), got %d: %+v", ops, tokens)
	}
}
```

需要在 `diffview_test.go` 顶部加 import：`"github.com/alecthomas/chroma/v2"`。

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestTokeniseBash' -v
```
Expected: FAIL（`undefined: tokeniseBash`）。

- [ ] **Step 3: 实现**

在 `internal/cli/diffview.go` 的 `highlightCode` 之后追加（`regexp` 已 import）：

```go
// bashFlagRE matches "-x" / "--flag" style arguments, which the chroma bash
// lexer leaves as plain Text; Codex's shell grammar colours them as
// variable.parameter, so we re-tag them as NameAttribute for the style to tint.
var bashFlagRE = regexp.MustCompile(`-{1,2}[A-Za-z0-9][A-Za-z0-9_-]*`)

// tokeniseBash runs the chroma bash lexer and re-tags "-x"/"--flag" arguments
// (outside quoted strings) as NameAttribute. Returns a single Text token for
// lexer errors so callers always get a renderable stream.
func tokeniseBash(cmd string) []chroma.Token {
	it, err := lexers.Get("bash").Tokenise(nil, cmd)
	if err != nil {
		return []chroma.Token{{Type: chroma.Text, Value: cmd}}
	}
	var out []chroma.Token
	for _, t := range it.Tokens() {
		if t.Type != chroma.Text {
			out = append(out, t)
			continue
		}
		last := 0
		for _, loc := range bashFlagRE.FindAllStringIndex(t.Value, -1) {
			if loc[0] > last {
				out = append(out, chroma.Token{Type: chroma.Text, Value: t.Value[last:loc[0]]})
			}
			out = append(out, chroma.Token{Type: chroma.NameAttribute, Value: t.Value[loc[0]:loc[1]]})
			last = loc[1]
		}
		if last < len(t.Value) {
			out = append(out, chroma.Token{Type: chroma.Text, Value: t.Value[last:]})
		}
	}
	return out
}
```

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestTokeniseBash' -v
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/cli/diffview.go internal/cli/diffview_test.go
git commit -m "feat(cli): re-tag bash -x/--flag arguments as NameAttribute tokens"
```

---

### Task 3: highlightBash（catppuccin + flags 补色）

**Files:**
- Modify: `internal/cli/diffview.go`
- Test: `internal/cli/diffview_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/cli/diffview_test.go` 末尾追加：

```go
func TestHighlightBashPreservesTextAndAddsColor(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	cmd := `git add . && git commit -m "fix" --no-verify`
	got := highlightBash(cmd)
	if plain := ansi.Strip(got); plain != cmd {
		t.Fatalf("highlight changed command text: got %q, want %q", plain, cmd)
	}
	if !strings.Contains(got, "\033[") {
		t.Fatalf("expected SGR colours, got %q", got)
	}
}

func TestHighlightBashPlainWithoutColor(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ASCII

	cmd := `echo hi && echo there`
	if got := highlightBash(cmd); got != cmd {
		t.Fatalf("no-colour terminal should pass text through, got %q", got)
	}
}

func TestHighlightBashEmpty(t *testing.T) {
	if got := highlightBash(""); got != "" {
		t.Fatalf("empty command should stay empty, got %q", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestHighlightBash' -v
```
Expected: FAIL（`undefined: highlightBash`）。

- [ ] **Step 3: 实现**

在 `internal/cli/diffview.go` 追加：

```go
// activeBashChromaStyle is the catppuccin theme plus a NameAttribute override
// that tints "-x"/"--flag" arguments (Codex's variable.parameter yellow).
func activeBashChromaStyle() *chroma.Style {
	flag := "#f9e2af" // catppuccin-mocha yellow
	if activeCLITheme.name == "light" {
		flag = "#df8e1d" // catppuccin-latte yellow
	}
	s, err := activeDiffChromaStyle().Builder().
		AddEntry(chroma.NameAttribute, chroma.StyleEntry{Colour: chroma.MustParseColour(flag)}).
		Build()
	if err != nil {
		return activeDiffChromaStyle()
	}
	return s
}

// highlightBash returns cmd with chroma bash foreground colours (catppuccin,
// flags tinted). It emits no background, mirroring highlightCode, and passes
// plain text through when the terminal has no colour.
func highlightBash(cmd string) string {
	if cmd == "" || !colorOn() {
		return cmd
	}
	var b strings.Builder
	if diffChromaFmt.Format(&b, activeBashChromaStyle(), tokenIterator(tokeniseBash(cmd))) != nil {
		return cmd
	}
	return strings.TrimRight(b.String(), "\n")
}

// tokenIterator adapts a token slice to chroma's iterator protocol.
func tokenIterator(tokens []chroma.Token) chroma.Iterator {
	i := 0
	return func() chroma.Token {
		if i >= len(tokens) {
			return chroma.EOF
		}
		t := tokens[i]
		i++
		return t
	}
}
```

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestHighlightBash' -v
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/cli/diffview.go internal/cli/diffview_test.go
git commit -m "feat(cli): highlight bash commands with catppuccin + flag tint"
```

---

### Task 4: bash 工具卡（高亮 + 多行 ⎿ 续行）

**Files:**
- Modify: `internal/cli/toolcard.go`
- Test: `internal/cli/toolcard_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `internal/cli/toolcard_test.go`：

```go
package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestBashToolCardHighlightsAndContinues(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	card := toolCard("bash", `{"command":"go build ./...\ngo test ./..."}`, 60)
	lines := strings.Split(card, "\n")
	if len(lines) != 2 {
		t.Fatalf("want header + one continuation row, got %d: %q", len(lines), card)
	}
	if !strings.Contains(lines[0], "Bash") || !strings.Contains(lines[0], "go build ./...") {
		t.Fatalf("header should carry the first command line, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "\033[") {
		t.Fatalf("command should be syntax-highlighted, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "⎿") || !strings.Contains(lines[1], "go test ./...") {
		t.Fatalf("continuation should use the ⎿ gutter, got %q", lines[1])
	}
}

func TestBashToolCardEmptyCommand(t *testing.T) {
	card := toolCard("bash", `{}`, 60)
	if !strings.Contains(card, "Bash") {
		t.Fatalf("empty command should still name the tool, got %q", card)
	}
}

func TestBashToolCardSingleLineStaysOneRow(t *testing.T) {
	card := toolCard("bash", `{"command":"git status"}`, 60)
	if strings.Contains(card, "\n") {
		t.Fatalf("single-line command should stay one row, got %q", card)
	}
	if !strings.Contains(card, "git status") {
		t.Fatalf("command missing from card, got %q", card)
	}
}

func TestBashToolCardNarrowNoPanic(t *testing.T) {
	for _, w := range []int{1, 2, 3, 5, 8, 20} {
		_ = toolCard("bash", `{"command":"go test ./... 你好 long command"}`, w)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestBashToolCard' -v
```
Expected: FAIL（首行格式是旧的 `● Bash(command)` 带括号、无高亮）。

- [ ] **Step 3: 实现**

`internal/cli/toolcard.go`：把 `toolCard` 改为对 bash 分流，并新增 `bashToolCard`（放在 `toolCard` 上方）：

```go
// toolCard renders the dispatch line: "  ⏺ Verb(arg)", arg clamped to width.
// bash commands are syntax-highlighted via bashToolCard instead.
func toolCard(name, args string, width int) string {
	if name == "bash" {
		return bashToolCard(name, args, width)
	}
	return "  " + toolDot(name) + " " + toolHead(name, toolArg(name, args), width)
}

// bashToolCard renders "  ● Bash <command>" with the command chroma-highlighted
// (catppuccin + flag tint). Multi-line commands continue under the ⎿ connector;
// every line is clamped to the terminal width.
func bashToolCard(name, args string, width int) string {
	cmd := strings.TrimSpace(toolArg(name, args))
	dot := toolDot(name)
	label := toolDisplayName(name)
	if cmd == "" {
		return "  " + dot + " " + bold(label)
	}
	lines := strings.Split(cmd, "\n")
	headW := width - 5 - len([]rune(label)) // 2 indent + ● + space + label + space
	first := highlightBash(clampPlain(lines[0], headW))
	rest := make([]string, 0, len(lines)-1)
	for _, ln := range lines[1:] {
		rest = append(rest, highlightBash(clampPlain(ln, width-len([]rune(connector)))))
	}
	head := "  " + dot + " " + bold(label) + " " + first
	if len(rest) == 0 {
		return head
	}
	return head + "\n" + connectorBlock(rest)
}
```

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestBashToolCard' -v
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/cli/toolcard.go internal/cli/toolcard_test.go
git commit -m "feat(cli): render bash cards with highlighted command and ⎿ continuation"
```

---

### Task 5: 文件卡标题 Codex 化

**Files:**
- Modify: `internal/cli/diffview.go`
- Test: `internal/cli/diffview_test.go`

- [ ] **Step 1: 写失败测试 + 改旧断言**

`internal/cli/diffview_test.go`：
1. `TestDiffBlockHeader` 的断言 `"Update"` 改为 `"Edited"`（标题不再带工具名 `Update`，动词按 diff 形状推断）。
2. `TestDiffHeaderUsesCategoryAndArgColors` 改名 `TestToolHeadUsesCategoryAndArgColors`（它测的是普通工具卡 `toolHead`，不再用于 diff header），内容不变。
3. 末尾追加：

```go
func TestFileVerb(t *testing.T) {
	cases := []struct {
		d    event.FileDiff
		want string
	}{
		{d: event.FileDiff{Added: 3}, want: "Added"},
		{d: event.FileDiff{Removed: 2}, want: "Deleted"},
		{d: event.FileDiff{Added: 1, Removed: 1}, want: "Edited"},
		{d: event.FileDiff{}, want: "Edited"},
	}
	for _, c := range cases {
		if got := fileVerb(c.d); got != c.want {
			t.Fatalf("fileVerb(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestDiffBlockCodexHeader(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	d := event.FileDiff{Diff: "@@ -1 +1 @@\n-a\n+b\n", Added: 1, Removed: 1}
	block := diffBlock("edit_file", `{"path":"pkg/x.go"}`, d, 80, 40)
	h := block[0]
	for _, want := range []string{"Edited", "pkg/x.go", "(+1", "-1)"} {
		if !strings.Contains(h, want) {
			t.Fatalf("header %q missing %q", h, want)
		}
	}
	if !strings.Contains(h, fgSGR(activeCLITheme.success)) || !strings.Contains(h, fgSGR(activeCLITheme.err)) {
		t.Fatalf("stat sides should carry green/red SGR, got %q", h)
	}
	if !strings.Contains(h, ansiBold) {
		t.Fatalf("verb should be bold, got %q", h)
	}
}

func TestDiffBlockCodexHeaderPureAdd(t *testing.T) {
	d := event.FileDiff{Diff: "@@ -0,0 +1 @@\n+package main\n", Added: 1}
	block := diffBlock("write_file", `{"path":"new.go"}`, d, 80, 40)
	if !strings.Contains(block[0], "Added") || !strings.Contains(block[0], "(+1 -0)") {
		t.Fatalf("pure add should read 'Added ... (+1 -0)', got %q", block[0])
	}
}

func TestDiffBlockCodexHeaderPureDelete(t *testing.T) {
	d := event.FileDiff{Diff: "@@ -1 +0,0 @@\n-old\n", Removed: 1}
	block := diffBlock("delete_file", `{"path":"old.go"}`, d, 80, 40)
	if !strings.Contains(block[0], "Deleted") || !strings.Contains(block[0], "(+0 -1)") {
		t.Fatalf("pure delete should read 'Deleted ... (+0 -1)', got %q", block[0])
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestFileVerb|TestDiffBlock' -v
```
Expected: FAIL（`undefined: fileVerb` / 旧 header 断言失败）。

- [ ] **Step 3: 实现**

`internal/cli/diffview.go`：把 `diffStat` 整体替换为 `fileVerb` + `codexStat`，并重写 `diffBlock` 的 header 构造：

```go
// fileVerb maps a change's shape to its Codex-style verb: pure additions
// "Added", pure removals "Deleted", mixed edits "Edited".
func fileVerb(d event.FileDiff) string {
	switch {
	case d.Added > 0 && d.Removed == 0:
		return "Added"
	case d.Removed > 0 && d.Added == 0:
		return "Deleted"
	default:
		return "Edited"
	}
}

// codexStat renders "(+N -M)" with green/red sides, always showing both like
// Codex's line-count summary.
func codexStat(d event.FileDiff) string {
	return "(" + green("+"+strconv.Itoa(d.Added)) + " " + red("-"+strconv.Itoa(d.Removed)) + ")"
}

// diffBlock renders a file change as a Codex-style header ("● Added path (+3 -0)")
// plus the highlighted, folded diff body. Returns nil when there's no textual diff.
func diffBlock(name, args string, d event.FileDiff, width, maxLines int) []string {
	if d.Diff == "" {
		return nil
	}
	path := diffPath(args)
	verb := fileVerb(d)
	stat := codexStat(d)
	avail := width - 6 - len([]rune(verb)) - len([]rune(stat))
	displayPath := clampPlain(path, avail)
	header := "  " + dim("●") + " " + bold(verb) + " " + themeFg(activeCLITheme.toolArg, displayPath) + "  " + stat
	return append([]string{header}, diffBody(d, path, width, maxLines)...)
}
```

注意：`diffBody` 仍传原始 `path`（clamp 前的），保证 lexer 按扩展名选择正确。

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestFileVerb|TestDiffBlock|TestToolHead' -v
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/cli/diffview.go internal/cli/diffview_test.go
git commit -m "feat(cli): render file-change headers Codex-style (Added/Deleted/Edited + stat)"
```

---

### Task 6: diff 背景色对齐 Codex 调色板

**Files:**
- Modify: `internal/cli/theme.go`

- [ ] **Step 1: 改色值**

`internal/cli/theme.go`：
- dark（`cliDarkTheme`）：`diffAddBG: cliColor{"#14351d", 22}` → `cliColor{"#213A2B", 22}`；`diffDelBG: cliColor{"#3a1619", 52}` → `cliColor{"#4A221D", 52}`
- light（`cliLightTheme`）：`diffAddBG: cliColor{"#e5f3e7", 254}` → `cliColor{"#dafbe1", 194}`；`diffDelBG: cliColor{"#fae8e8", 255}` → `cliColor{"#ffebe9", 224}`

256 索引取自 Codex `diff_render.rs`（`DARK_256_ADD_LINE_BG_IDX=22`、`DARK_256_DEL_LINE_BG_IDX=52`、`LIGHT_256_ADD_LINE_BG_IDX=194`、`LIGHT_256_DEL_LINE_BG_IDX=224`）。

- [ ] **Step 2: 运行验证**

```bash
go test ./internal/cli/ -run 'TestDiffBarReappliesBackground|TestDiffTabExpansion' -v
```
Expected: PASS（这两个测试用 `bgSGR(activeCLITheme.diffAddBG)` 取值，不写死色号，应保持通过）。

- [ ] **Step 3: 提交**

```bash
git add internal/cli/theme.go
git commit -m "style(cli): align diff bar backgrounds with Codex palette"
```

---

### Task 7: 删除行内容变暗（ansiDim）

**Files:**
- Modify: `internal/cli/style.go`、`internal/cli/diffview.go`
- Test: `internal/cli/diffview_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/cli/diffview_test.go` 末尾追加：

```go
func TestDiffBarDimsDeleteContent(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	line := diffBar('-', "const x = 1", "x.go", 40, bgSGR(activeCLITheme.diffDelBG), fgSGR(activeCLITheme.err), 1, 2)
	if !strings.Contains(line, ansiDim) {
		t.Fatalf("delete row should dim its content, got %q", line)
	}
	// The dim must survive chroma resets inside the highlighted code.
	if strings.Count(line, ansiDim) < 2 {
		t.Fatalf("dim not re-armed after chroma resets, got %q", line)
	}
}

func TestDiffBarAddNotDimmed(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	line := diffBar('+', "func main() {}", "x.go", 40, bgSGR(activeCLITheme.diffAddBG), fgSGR(activeCLITheme.success), 1, 2)
	if strings.Contains(line, ansiDim) {
		t.Fatalf("add row should not be dimmed, got %q", line)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestDiffBarDimsDeleteContent|TestDiffBarAddNotDimmed' -v
```
Expected: FAIL（当前 `-` 行没有 dim）。

- [ ] **Step 3: 实现**

`internal/cli/style.go` 常量区（`ansiReverse` 后面）加一行：

```go
	// ansiDim is the SGR dim modifier, re-applied after chroma resets on
	// deleted diff lines so the whole content reads faded (Codex's DIM).
	ansiDim = "\033[2m"
```

`internal/cli/diffview.go` 的 `diffBar`：把 `hl := reapplyBG(highlightCode(path, code), bg)` 改为：

```go
	hl := highlightCode(path, code)
	if sign == '-' {
		hl = ansiDim + strings.ReplaceAll(hl, ansiReset, ansiReset+ansiDim)
	}
	hl = reapplyBG(hl, bg)
```

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestDiffBarDimsDeleteContent|TestDiffBarAddNotDimmed|TestDiffBarReappliesBackground' -v
```
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/cli/style.go internal/cli/diffview.go internal/cli/diffview_test.go
git commit -m "feat(cli): dim deleted diff-line content (Codex DIM overlay)"
```

---

### Task 8: 全量验证 + 收尾

**Files:** 无新增（只运行命令 + 可能的格式化修正）

- [ ] **Step 1: 全量测试**

```bash
go test ./internal/cli/
```
Expected: 除已知存量失败外全部通过。已知与本改动无关的存量失败：
`TestRenderMCPManagerDetailCompactsConfigPath`、`TestLoadCCSwitchLegacyConfigPrefersCorvusFlag`、`TestExamplePluginEndToEnd`（实现前先跑一次确认它们本来就红，不要顺手修）。

- [ ] **Step 2: 严格 gofmt**

```bash
$(go env GOROOT)/bin/gofmt -l internal/cli/
```
Expected: 无输出（`-l` 列出未格式化文件，空 = 干净）。若有输出，对列出文件执行 `$(go env GOROOT)/bin/gofmt -w <file>`，并单独提交：

```bash
git add -u
git commit -m "style: apply strict gofmt formatting"
```

- [ ] **Step 3: 构建**

```bash
make build && go build ./...
```
Expected: 成功。

- [ ] **Step 4: 推送**

```bash
git push origin main
```
Expected: 全部功能 commit 推送成功。

- [ ] **Step 5: 手工冒烟（可选，终端里看效果）**

```bash
go run . -p 2>/dev/null &
# 或直接跑: go run . 然后发一条让 agent 执行 "git add . && git commit -m test" / 写文件 + 删文件的指令
```
确认：bash 卡 `&&` 天蓝加粗、字符串绿色、`--flag` 黄色；文件卡 `● Added path (+N -M)`；删除行内容整体变暗。
