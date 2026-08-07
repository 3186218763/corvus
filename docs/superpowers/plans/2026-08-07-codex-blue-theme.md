# Codex Blue Theme Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 corvus TUI 默认主题从暖橘 graphite 换成 Codex 蓝（dark + light 两套，graphite 等旧主题保留），输入框背景改为"相对终端背景提亮/加深 + 84% 混合"的透明感填充并默认 2 行，fenced 代码块从整行 accent 改为按 token 着色。

**Architecture:** 所有颜色仍只进 `internal/cli/theme.go` 的 `cliPalette`/`cliThemeStyles`（仓库约定：256 色回退为手选 curated 表，非运行时换算；仅输入框相对背景的换算用现成 `charmbracelet/x/ansi` 的 `Convert256`）。新默认样式 `codex`/`codex-light` 插入样式表头部，`defaultCLIThemeStyle` 指向它们。输入框 tint 挂在 `buildCLITheme`（probe 仅在启动 TTY 路径安装），渲染链路 `renderComposerField` 不变。代码高亮在 `md.go` 新增 `highlightCodeLine`，`renderFenced` 逐行调用。

**Tech Stack:** Go（`go 1.24`）、Bubble Tea v2、Lip Gloss v2、`charmbracelet/x/ansi v0.11.7`（`ansi.Convert256`）、`charm.land/bubbles/v2 v2.1.0`（textarea）、goldmark；测试 `go test ./internal/cli/...`。

**Spec:** `docs/superpowers/specs/2026-08-07-codex-blue-theme-design.md`

---

## 计划期订正（相对已审阅 spec，实施时按本计划执行）

- **D1 · dark border hex**：spec 表 `#27334a`（chroma=35）会打破既有护栏测试 `TestThemeHierarchyBodyBrighterThanChromeBorder`（要求 border chroma ≤ 30）。改用 `#273343`（39,51,67，chroma=28），**xterm 仍 237**，观感一致。
- **D2 · 256 换算 API**：`colorprofile.Color(...).ID()` 在 `charmbracelet/colorprofile v0.4.3` 不存在。用 `int(ansi.Convert256(color.RGBA{...}))`（`IndexedColor` 是 `uint8`，直接 `int()` 转换；`image/color` 已在 theme.go 导入）。
- **D3 · textarea SetHeight 行为**：bubbles v2.1.0 的 `SetHeight` 按包常量 `minHeight=1` clamp，**不认 `Model.MinHeight`**。因此 `configureChatTextarea` 的 `SetHeight(1)` 和 `chooser.go:172` 的 `SetHeight(1)` 都要显式改成 `SetHeight(2)`，否则空态仍是 1 行。
- **D4 · userBubbleFadedXTerm**：`fadedUserBubbleColor` 对未知样式回退到 accent 自身 xterm（codex=75），会与 accent 在 256 色下无法区分。为 codex 派生色 `#688cb9`、codex-light 派生色 `#6178a6` 手选 xterm **67**（`ansi.Convert256` 最近档，也是该色系合理回退）。
- **D5 · md 测试**：`TestRenderConstructs` 的 fenced 用例断言裸文本 `"func main()"`，token 着色后 `func` 被 SGR 打断，必须 `ansi.Strip` 后再断言。
- **D6 · 文案**：sandstone 描述去掉 `default ` 前缀（默认职责已移交 codex-light）。
- light subtle `#555d6b` xterm 240 与 light toolArg 240 在 256 色下同档（spec 已如此 pin；truecolor 区分，沿用现有 faint==subtle=243 的先例，不改）。
- **D9 · 实施期订正（Task 2 质量审查）**：中文/繁中 ThemeHint 措辞按审查建议调整为「启动探测到终端背景时，输入框底色随之自动调整」（原「启动时探测到终端背景时…会随背景自动调整」两个「时」连用）；测试补 mixHex t=0/t=1 恒等、非法 hex 回退、probe 极值 0,0,0→`#111111`/233 与 255,255,255→`#eeeeee`/255 断言。
- **D10 · 实施期订正（Task 4 质量审查）**：`highlightCodeLine` 由逐段 `FindStringSubmatchIndex(line[pos:])` 改为单遍 `FindAllStringSubmatchIndex(line, -1)`——`^` 恢复真行首锚定（原实现对行中 `#` 的匹配与注释语义不一致），并加 `b.Grow(len(line))`；测试补单引号/反引号字符串、字符串屏蔽 `//`、CJK 用例与 `CORVUS_THEME` 环境守卫。精确 SGR 期望值按计划保留（token→色映射是 spec 契约）。
- **D8 · 实施期订正（Task 2 实测）**：计划行 387 的 `#dddfe2` 是计划自身的算术错误（245×0.92=225.4 → 舍入 225 = `0xe1`），正确期望为 `#dddfe1`；实现与测试均用 `#dddfe1`。
- **D7 · 实施期订正（Task 1 实测）**：`CORVUS_THEME_STYLE` 只覆盖 style 不覆盖 mode，env 用例第二段必须用 `configureCLIThemeWithStyle("light", "graphite")` 才能得到 light/codex-light；dark muted 252→253、light subtle 243→240 影响三个 footer 测试的 valueSGR/labelSGR pin（`TestStatusFooterSemanticPaletteAcrossThemes`、`TestTurnReceiptAdaptsContrastAcrossThemes`、`TestContextFooterColorsOnlyValuesByUrgency`），Task 1 一并更新。

## 全局约束

- TDD：每任务"写失败测试 → 跑（确认红）→ 最小实现 → 跑（确认绿）→ 提交"。
- 提交直接在 `main` 分支（用户已同意）；每任务一个 commit。
- 颜色字面量只允许出现在 `internal/cli/theme.go`（`color_discipline_test.go` 守护）。
- 旧主题（graphite/ember/aurora/midnight/sandstone/porcelain/linen/glacier）全部保留可切换；`theme_sweep_test.go`、`TestConfigureCLIThemeStyleOverride`、`TestThemeRendersAtProfileFidelity`、`TestThemeArgCompletion`、`TestRunThemeSubcommandSwitchesAccentAndTextarea` 明确不改。
- `mixedBlocks`（`transcript_test.go`/`bench_test.go` 含 173 SGR 的 fixture）不改。
- 模式 badge 硬编码色（`gitstatus.go`）不改；`docs/tui-preview.html` 本次不改（spec 范围外）。
- 已知既有失败（**不要修**，属其他任务）：`TestRenderMCPManagerDetailCompactsConfigPath`（internal/cli）、`TestLoadCCSwitchLegacyConfigPrefersCorvusFlag`（internal/config）。
- gofmt：`$(go env GOROOT)/bin/gofmt`（gofmt 不在 PATH）。
- 全量测试命令：`go test ./internal/cli/`（预期仅上述两个已知失败）。

## 文件地图

| 文件 | 职责 |
|------|------|
| `internal/cli/theme.go` | `cliPalette` 新增 4 个 code 槽位；dark/light 色表重设计；样式表插入 codex/codex-light；`userBubbleFadedXTerm` 新条目；`defaultCLIThemeStyle` 指向新默认；`mixHex`/`inputBoxTintFromBackground`；`buildCLITheme` 探测接入 |
| `internal/cli/style.go` | 新增 `ansiCodexAccent` 常量（`ansiAccent` 保留给显式 graphite 测试） |
| `internal/cli/chat_tui.go` | `configureChatTextarea`：`MinHeight = 2`、`SetHeight(2)` |
| `internal/cli/chooser.go` | 自由文本模式 `SetHeight(1)` → `SetHeight(2)` |
| `internal/cli/md.go` | `highlightCodeLine` + 正则；`renderFenced` 逐行调用 |
| `internal/i18n/messages_en.go` / `messages_zh.go` / `messages_zh_tw.go` | `ThemeHint` 补充"启动探测终端背景 → 输入框底色随背景调整" |
| `internal/cli/theme_test.go` | 默认样式断言、tint 纯函数、probe 覆盖、env 用例、faded/toolArg pin |
| `internal/cli/md_test.go` | fenced 用例 strip + `TestHighlightCodeLine` |
| `internal/cli/statusline_test.go` / `status_footer_test.go` | info SGR pin 更新（80→75、25→26） |
| `internal/cli/chat_tui_test.go` | 2 行输入框的布局/高度断言更新 |

---

### Task 1: Codex 色表与默认样式（theme.go + style.go + pin 更新）

**Files:**
- Modify: `internal/cli/theme.go`（struct 字段、两个 palette、样式表、faded 表、defaultCLIThemeStyle）
- Modify: `internal/cli/style.go`（ansiCodexAccent）
- Test: `internal/cli/theme_test.go`、`internal/cli/statusline_test.go`、`internal/cli/status_footer_test.go`

- [x] **Step 1: 更新/新增失败测试**

`internal/cli/theme_test.go` — `TestConfigureCLIThemeSwitchesModeAndDefaultStyle` 改为：

```go
func TestConfigureCLIThemeSwitchesModeAndDefaultStyle(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	configureCLITheme("light")
	if activeCLITheme.name != "light" || activeCLITheme.style != "codex-light" {
		t.Fatalf("light theme = %s/%s, want light/codex-light", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, "\033[38;5;62m") {
		t.Fatalf("light default accent = %q, want codex-light xterm 62", got)
	}

	configureCLITheme("dark")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "codex" {
		t.Fatalf("dark theme = %s/%s, want dark/codex", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, ansiCodexAccent) {
		t.Fatalf("dark accent = %q, want %q", got, ansiCodexAccent)
	}
}
```

`internal/cli/theme_test.go` — `TestComposerTintAndCursorFollowTheme` 两处 dark 回退 pin 改为 `cliColor{"#1c2534", 235}`：

```go
		for _, theme := range cliThemeStyles {
			t.Run(theme.name, func(t *testing.T) {
				configureCLITheme(theme.name)
				wantTint := cliColor{"#eceff4", 255}
				if theme.mode == "dark" {
					wantTint = cliColor{"#1c2534", 235}
				}
```

```go
	activeColorProfile = colorprofile.NoTTY
	configureCLITheme("dark")
	if got := activeCLITheme.inputBoxBG; !reflect.DeepEqual(got, cliColor{"#1c2534", 235}) {
		t.Fatalf("dark theme must keep its tint slot even under NO_COLOR, got %v", got)
	}
```

`internal/cli/theme_test.go` — `TestUserBubbleFadedFollowsAccent` 在 midnight 块之后追加 codex 断言，并把文件末尾 dark toolArg pin 更新：

```go
	configureCLIThemeWithStyle("dark", "midnight")
	if got, want := activeCLITheme.userBubbleFaded.xterm, 140; got != want {
		t.Fatalf("midnight faded xterm = %d, want 140", got)
	}

	configureCLIThemeWithStyle("dark", "codex")
	if got, want := activeCLITheme.userBubbleFaded.hex, "#688cb9"; got != want {
		t.Fatalf("codex faded hex = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.userBubbleFaded.xterm, 67; got != want {
		t.Fatalf("codex faded xterm = %d, want 67", got)
	}

	configureCLIThemeWithStyle("light", "codex-light")
	if got, want := activeCLITheme.userBubbleFaded.hex, "#6178a6"; got != want {
		t.Fatalf("codex-light faded hex = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.userBubbleFaded.xterm, 67; got != want {
		t.Fatalf("codex-light faded xterm = %d, want 67", got)
	}
```

```go
	configureCLIThemeWithStyle("dark", "graphite")
	if got, want := activeCLITheme.toolArg.hex, "#b6c2d4"; got != want {
		t.Fatalf("dark toolArg = %s, want %s", got, want)
	}
	if got, want := activeCLITheme.toolArg.xterm, 146; got != want {
		t.Fatalf("dark toolArg xterm = %d, want 146", got)
	}
```

`internal/cli/theme_test.go` — 新增 env 用例（放在 `TestConfigureCLIThemeHonorsEnvOverride` 之后）：

```go
func TestConfigureCLIThemeHonorsCodexEnvNames(t *testing.T) {
	t.Setenv("CORVUS_THEME", "codex")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	configureCLIThemeWithStyle("light", "glacier")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "codex" {
		t.Fatalf("CORVUS_THEME=codex resolved %s/%s, want dark/codex", activeCLITheme.name, activeCLITheme.style)
	}

	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "codex-light")
	configureCLIThemeWithStyle("dark", "graphite")
	if activeCLITheme.name != "light" || activeCLITheme.style != "codex-light" {
		t.Fatalf("CORVUS_THEME_STYLE=codex-light resolved %s/%s, want light/codex-light", activeCLITheme.name, activeCLITheme.style)
	}
}
```

`internal/cli/statusline_test.go` — `TestEffortTagExplicitValueUsesThemeInfo` 表格：

```go
	for _, tt := range []struct {
		mode, infoSGR string
	}{
		{mode: "dark", infoSGR: "\033[1;38;5;75m"},
		{mode: "light", infoSGR: "\033[1;38;5;26m"},
	} {
```

`internal/cli/status_footer_test.go` — `TestStatusFooterSemanticPaletteAcrossThemes` 表格：

```go
	for _, tt := range []struct {
		mode, labelSGR, valueSGR, infoSGR, secondarySGR string
	}{
		{mode: "dark", labelSGR: "\033[38;5;247m", valueSGR: "\033[38;5;252m", infoSGR: "\033[38;5;75m", secondarySGR: "\033[38;5;141m"},
		{mode: "light", labelSGR: "\033[38;5;243m", valueSGR: "\033[38;5;238m", infoSGR: "\033[38;5;26m", secondarySGR: "\033[38;5;104m"},
	} {
```

- [x] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestConfigureCLIThemeSwitchesModeAndDefaultStyle|TestComposerTintAndCursorFollowTheme|TestUserBubbleFadedFollowsAccent|TestConfigureCLIThemeHonorsCodexEnvNames|TestEffortTagExplicitValueUsesThemeInfo|TestStatusFooterSemanticPaletteAcrossThemes'
```

Expected: FAIL（`ansiCodexAccent` 未定义 / 默认样式仍是 graphite|sandstone / pin 值不匹配）。

- [x] **Step 3: 实现 theme.go + style.go**

`internal/cli/style.go` — const 块追加（`ansiAccent` 保留）：

```go
const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiReverse = "\033[7m"
	// ansiAccent is the dark graphite accent as a literal escape, for tests that
	// pin the concrete sequence instead of the active theme.
	ansiAccent = "\033[38;5;173m"
	// ansiCodexAccent is the dark codex accent as a literal escape, for tests
	// that pin the concrete sequence instead of the active theme.
	ansiCodexAccent = "\033[38;5;75m"
)
```

`internal/cli/theme.go` — `cliPalette` struct 末尾（`toolArg` 之后）追加 4 个字段：

```go
	toolArg         cliColor
	codeKeyword     cliColor
	codeString      cliColor
	codeNumber      cliColor
	codeComment     cliColor
}
```

`internal/cli/theme.go` — 整体替换 `cliDarkTheme`（graphite 色 → codex 蓝；border 用 D1 的 `#273343`）：

```go
	cliDarkTheme = cliPalette{
		name:         "dark",
		style:        "graphite",
		accent:       cliColor{"#4a9bff", 75},
		muted:        cliColor{"#d6dde8", 253},
		faint:        cliColor{"#8a93a3", 245},
		subtle:       cliColor{"#a4adbc", 247},
		success:      cliColor{"#6fce8a", 78},
		warn:         cliColor{"#e2b93b", 179},
		err:          cliColor{"#f0706e", 203},
		danger:       cliColor{"#e5484d", 167},
		info:         cliColor{"#5eb0ff", 75},
		secondary:    cliColor{"#b18cff", 141},
		border:       cliColor{"#273343", 237},
		inputBoxBG:   cliColor{"#1c2534", 235},
		selection:    cliColor{"#4a9bff", 75},
		userBubbleBG: cliColor{"#222631", 235},
		diffAddBG:    cliColor{"#14351d", 22},
		diffDelBG:    cliColor{"#3a1619", 52},
		toolRead:     cliColor{"#5eb0ff", 75},
		toolProc:     cliColor{"#c792ea", 176},
		// userBubbleFaded is derived per accent style by applyCLIThemeStyle;
		// this literal only documents the graphite value.
		userBubbleFaded: cliColor{"#a87c6e", 95},
		toolArg:         cliColor{"#b6c2d4", 146},
		codeKeyword:     cliColor{"#c792ea", 176},
		codeString:      cliColor{"#9ece6a", 149},
		codeNumber:      cliColor{"#e0af68", 179},
		codeComment:     cliColor{"#6a7485", 66},
	}
```

`internal/cli/theme.go` — 整体替换 `cliLightTheme`（冷色蓝灰替换暖色砂岩系）：

```go
	cliLightTheme = cliPalette{
		name:         "light",
		style:        "sandstone",
		accent:       cliColor{"#3b6fd4", 62},
		muted:        cliColor{"#3d4552", 238},
		faint:        cliColor{"#6a7280", 243},
		subtle:       cliColor{"#555d6b", 240},
		success:      cliColor{"#4d8f57", 65},
		warn:         cliColor{"#a97c1a", 136},
		err:          cliColor{"#c94f4d", 131},
		danger:       cliColor{"#e5484d", 167},
		info:         cliColor{"#2f6fd4", 26},
		secondary:    cliColor{"#7d63c8", 104},
		border:       cliColor{"#d9dde4", 253},
		inputBoxBG:   cliColor{"#eceff4", 255},
		selection:    cliColor{"#3b6fd4", 62},
		userBubbleBG: cliColor{"#eef1f6", 255},
		diffAddBG:    cliColor{"#e5f3e7", 254},
		diffDelBG:    cliColor{"#fae8e8", 255},
		toolRead:     cliColor{"#2f6fd4", 26},
		toolProc:     cliColor{"#8a6bb8", 97},
		// userBubbleFaded is derived per accent style by applyCLIThemeStyle;
		// this literal only documents the sandstone value.
		userBubbleFaded: cliColor{"#9e7263", 95},
		toolArg:         cliColor{"#5a6470", 240},
		codeKeyword:     cliColor{"#7d63c8", 104},
		codeString:      cliColor{"#4d8f57", 65},
		codeNumber:      cliColor{"#b68120", 136},
		codeComment:     cliColor{"#6a7280", 243},
	}
```

`internal/cli/theme.go` — 整体替换 `cliThemeStyles`（codex 插到 graphite 前、codex-light 插到 sandstone 前，D6 去掉 sandstone 描述里的 `default `）：

```go
	cliThemeStyles = []cliThemeStyle{
		{name: "codex", mode: "dark", accent: cliColor{"#4a9bff", 75}, description: "codex blue accent"},
		{name: "graphite", mode: "dark", accent: cliColor{"#d97757", 173}, description: "warm clay accent"},
		{name: "ember", mode: "dark", accent: cliColor{"#f06d38", 209}, description: "hot orange accent"},
		{name: "aurora", mode: "dark", accent: cliColor{"#34c3a6", 79}, description: "cool teal accent"},
		{name: "midnight", mode: "dark", accent: cliColor{"#b18cff", 141}, description: "quiet violet accent"},
		{name: "codex-light", mode: "light", accent: cliColor{"#3b6fd4", 62}, description: "codex blue light accent"},
		{name: "sandstone", mode: "light", accent: cliColor{"#c2613f", 173}, description: "warm light accent"},
		{name: "porcelain", mode: "light", accent: cliColor{"#7d63c8", 104}, description: "soft violet light accent"},
		{name: "linen", mode: "light", accent: cliColor{"#bd5d4d", 167}, description: "muted coral light accent"},
		{name: "glacier", mode: "light", accent: cliColor{"#357fa8", 74}, description: "cool blue light accent"},
	}
```

`internal/cli/theme.go` — `userBubbleFadedXTerm` 追加（D4）：

```go
var userBubbleFadedXTerm = map[string]int{
	"graphite":    95,
	"ember":       131,
	"aurora":      72,
	"midnight":    140,
	"codex":       67,
	"sandstone":   95,
	"porcelain":   103,
	"linen":       131,
	"glacier":     67,
	"codex-light": 67,
}
```

`internal/cli/theme.go` — `defaultCLIThemeStyle` light 分支指向 codex-light：

```go
func defaultCLIThemeStyle(mode string) cliThemeStyle {
	if mode == "light" {
		for _, st := range cliThemeStyles {
			if st.name == "codex-light" {
				return st
			}
		}
	}
	return cliThemeStyles[0]
}
```

- [x] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestConfigureCLIThemeSwitchesModeAndDefaultStyle|TestComposerTintAndCursorFollowTheme|TestUserBubbleFadedFollowsAccent|TestConfigureCLIThemeHonorsCodexEnvNames|TestEffortTagExplicitValueUsesThemeInfo|TestStatusFooterSemanticPaletteAcrossThemes|TestThemeHierarchyBodyBrighterThanChromeBorder|TestToolArgPairwiseDistinct'
```

Expected: PASS（`TestThemeHierarchyBodyBrighterThanChromeBorder` 必须过——D1 保证 chroma 28 ≤ 30）。

- [x] **Step 5: gofmt + 全量 + 提交**

```bash
$(go env GOROOT)/bin/gofmt -w internal/cli/theme.go internal/cli/style.go internal/cli/theme_test.go internal/cli/statusline_test.go internal/cli/status_footer_test.go
go test ./internal/cli/
git add internal/cli/theme.go internal/cli/style.go internal/cli/theme_test.go internal/cli/statusline_test.go internal/cli/status_footer_test.go
git commit -m "feat: make codex blue the default CLI theme"
```

Expected: 全量仅剩 `TestRenderMCPManagerDetailCompactsConfigPath` 一个已知失败。

---

### Task 2: 输入框背景相对终端背景（tint 计算 + 探测接入）

**Files:**
- Modify: `internal/cli/theme.go`（imports、`mixHex`、`inputBoxTintFromBackground`、`buildCLITheme`）
- Modify: `internal/i18n/messages_en.go` / `messages_zh.go` / `messages_zh_tw.go`（`ThemeHint`）
- Test: `internal/cli/theme_test.go`

- [x] **Step 1: 写失败测试**

`internal/cli/theme_test.go` 末尾追加（预期值已用真实实现核对过）：

```go
func TestInputBoxTintPureFunctions(t *testing.T) {
	if got, want := mixHex("#0a0c10", "#ffffff", 0.08), "#1e1f23"; got != want {
		t.Fatalf("mixHex dark lift = %s, want %s", got, want)
	}
	if got, want := mixHex("#0a0c10", "#1e1f23", 0.84), "#1b1c20"; got != want {
		t.Fatalf("mixHex 84%% blend = %s, want %s", got, want)
	}
	if got, want := mixHex("#f0f2f5", "#000000", 0.08), "#dddfe1"; got != want {
		t.Fatalf("mixHex light sink = %s, want %s", got, want)
	}

	// Dark bg (10,12,16): lift toward white then 84% blend → #1b1c20.
	if got := inputBoxTintFromBackground(terminalRGB{10, 12, 16}, true); got != (cliColor{hex: "#1b1c20", xterm: 234}) {
		t.Fatalf("dark tint = %+v, want #1b1c20/234", got)
	}
	// Light bg (240,242,245): sink toward black → #e0e2e4.
	if got := inputBoxTintFromBackground(terminalRGB{240, 242, 245}, false); got != (cliColor{hex: "#e0e2e4", xterm: 254}) {
		t.Fatalf("light tint = %+v, want #e0e2e4/254", got)
	}
}

func TestBuildCLIThemeTintUnderProbe(t *testing.T) {
	t.Setenv("CORVUS_THEME", "")
	t.Setenv("CORVUS_THEME_STYLE", "")
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256

	defer func(prev func() (terminalRGB, bool)) { terminalProbe = prev }(terminalProbe)
	terminalProbe = func() (terminalRGB, bool) { return terminalRGB{10, 12, 16}, true }

	withTerminalProbe(func() {
		got := resolveCLITheme("dark")
		want := inputBoxTintFromBackground(terminalRGB{10, 12, 16}, true)
		if !reflect.DeepEqual(got.inputBoxBG, want) {
			t.Fatalf("probed dark inputBoxBG = %v, want tint %v", got.inputBoxBG, want)
		}
		gotLight := resolveCLITheme("light")
		wantLight := inputBoxTintFromBackground(terminalRGB{10, 12, 16}, false)
		if !reflect.DeepEqual(gotLight.inputBoxBG, wantLight) {
			t.Fatalf("probed light inputBoxBG = %v, want tint %v", gotLight.inputBoxBG, wantLight)
		}
	})

	// Outside the probe the curated fallback colors stay.
	if got := resolveCLITheme("dark").inputBoxBG; !reflect.DeepEqual(got, cliColor{"#1c2534", 235}) {
		t.Fatalf("fallback dark inputBoxBG = %v, want #1c2534/235", got)
	}
	if got := resolveCLITheme("light").inputBoxBG; !reflect.DeepEqual(got, cliColor{"#eceff4", 255}) {
		t.Fatalf("fallback light inputBoxBG = %v, want #eceff4/255", got)
	}
}
```

`internal/i18n/messages_en.go` / `messages_zh.go` / `messages_zh_tw.go` — `ThemeHint` 追加启动探测说明：

```go
	ThemeHint: "switch with /theme <auto|light|dark|style>; the input box tint follows the terminal background detected at startup",
```

```go
	ThemeHint: "使用 /theme <auto|light|dark|style> 切换；启动时探测到终端背景时，输入框底色会随背景自动调整",
```

```go
	ThemeHint: "使用 /theme <auto|light|dark|style> 切換；啟動時探測到終端背景時，輸入框底色會隨背景自動調整",
```

- [x] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestInputBoxTintPureFunctions|TestBuildCLIThemeTintUnderProbe'
```

Expected: FAIL（`mixHex`/`inputBoxTintFromBackground` 未定义）。

- [x] **Step 3: 实现**

`internal/cli/theme.go` — imports 追加 `"math"` 和 `"github.com/charmbracelet/x/ansi"`（现有 import 块内，保持 gofmt 分组顺序）。

`internal/cli/theme.go` — `buildCLITheme` 替换（probe 命中时覆盖 `inputBoxBG`，D2 用 `int(ansi.Convert256(...))`）：

```go
func buildCLITheme(mode, style string) cliPalette {
	base := cliDarkTheme
	if mode == "light" {
		base = cliLightTheme
	}
	st, ok := cliThemeStyleByName(style)
	if !ok || st.mode != base.name {
		st = defaultCLIThemeStyle(base.name)
	}
	theme := applyCLIThemeStyle(base, st)
	if rgb, ok := activeBackgroundProbe(); ok {
		theme.inputBoxBG = inputBoxTintFromBackground(rgb, base.name == "dark")
	}
	return theme
}
```

`internal/cli/theme.go` — 在 `buildCLITheme` 之后追加两个纯函数：

```go
// mixHex blends two hex colours by t in [0,1] (t is the weight of b) and
// returns the rounded result as "#rrggbb". Pure and testable.
func mixHex(a, b string, t float64) string {
	ar, ag, ab, okA := parseHexColor(a)
	br, bg, bb, okB := parseHexColor(b)
	if !okA || !okB {
		return a
	}
	mix := func(x, y int) int {
		return int(math.Round(float64(x)*(1-t) + float64(y)*t))
	}
	return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
}

// inputBoxTintFromBackground computes the composer fill from the probed
// terminal background: dark shells lift 8% toward white, light shells sink 8%
// toward black, then the result is blended 84% with the background to mimic a
// translucent overlay (effective lift/sink is 6.72% = 0.84 × 0.08). The 256
// colour fallback is the nearest xterm index via ansi.Convert256 — unlike the
// curated palette slots, this value is computed because it tracks a live
// background the designer cannot pin.
func inputBoxTintFromBackground(rgb terminalRGB, dark bool) cliColor {
	bg := fmt.Sprintf("#%02x%02x%02x", rgb.r, rgb.g, rgb.b)
	ref := "#ffffff"
	if !dark {
		ref = "#000000"
	}
	final := mixHex(bg, mixHex(bg, ref, 0.08), 0.84)
	xterm := 0
	if r, g, b, ok := parseHexColor(final); ok {
		xterm = int(ansi.Convert256(color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0xff}))
	}
	return cliColor{hex: final, xterm: xterm}
}
```

- [x] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestInputBoxTintPureFunctions|TestBuildCLIThemeTintUnderProbe|TestRuntimeAutoThemeDoesNotProbeStdin|TestAutoThemeFallsBackToColorFGBG'
```

Expected: PASS（`TestRuntimeAutoThemeDoesNotProbeStdin` 守护不回归：运行时 `/theme auto` 不触发 probe）。

- [x] **Step 5: gofmt + 全量 + 提交**

```bash
$(go env GOROOT)/bin/gofmt -w internal/cli/theme.go internal/cli/theme_test.go internal/i18n/messages_en.go internal/i18n/messages_zh.go internal/i18n/messages_zh_tw.go
go test ./internal/cli/
git add internal/cli/theme.go internal/cli/theme_test.go internal/i18n/messages_en.go internal/i18n/messages_zh.go internal/i18n/messages_zh_tw.go
git commit -m "feat: tint composer background from the probed terminal background"
```

---

### Task 3: 输入框默认 2 行

**Files:**
- Modify: `internal/cli/chat_tui.go`（`configureChatTextarea`）
- Modify: `internal/cli/chooser.go`（自由文本模式）
- Test: `internal/cli/chat_tui_test.go`

- [x] **Step 1: 更新失败测试**

`internal/cli/chat_tui_test.go` — `TestTranscriptViewportSizing`：

```go
	if got := m.bottomRows(); got != 3 {
		t.Fatalf("bottomRows with an empty composer = %d, want 3 (input 2 + footer 1)", got)
	}
```

```go
	if want := m.transcriptHeight(); m.viewport.Height() != want || want != 21 {
		t.Errorf("viewport height = %d, transcriptHeight = %d, want 21 (24-3)", m.viewport.Height(), want)
	}
```

`internal/cli/chat_tui_test.go` — `TestPasteMsgFoldsBeforeTextareaConsumesNewlines`：

```go
	if got.input.Height() != 2 {
		t.Fatalf("folded paste should keep the two-row minimum, got %d", got.input.Height())
	}
```

`internal/cli/chat_tui_test.go` — `TestManualNewlineGrowsComposerWithoutHidingFirstLine` 在 `SetValue` 前补空态断言：

```go
	if got := m.input.Height(); got != 2 {
		t.Fatalf("empty composer height = %d, want 2 (two-row field)", got)
	}
	m.input.SetValue("first line")
```

`internal/cli/chat_tui_test.go` — `TestEmptyComposerShowsOnlyPrompt` 改为断言 2 行：

```go
	lines := strings.Split(ansi.Strip(m.renderComposerInput()), "\n")
	if len(lines) != 2 {
		t.Fatalf("empty composer should render %d rows, want 2", len(lines))
	}
	if strings.TrimSpace(lines[0]) != "❯" {
		t.Fatalf("empty composer = %q, want only the prompt", lines[0])
	}
```

- [x] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestTranscriptViewportSizing|TestPasteMsgFoldsBeforeTextareaConsumesNewlines|TestManualNewlineGrowsComposerWithoutHidingFirstLine|TestEmptyComposerShowsOnlyPrompt'
```

Expected: FAIL（bottomRows=2/viewport=22、Height=1 等断言）。

- [x] **Step 3: 实现**

`internal/cli/chat_tui.go` — `configureChatTextarea`（D3：`MinHeight` 托底 + 显式 `SetHeight(2)`）：

```go
	ti.DynamicHeight = true
	ti.MinHeight = 2
	ti.MaxHeight = maxInputRows
	ti.MaxContentHeight = ti.CharLimit
	ti.SetHeight(2)
```

`internal/cli/chooser.go:172` — 自由文本模式：

```go
		m.input.Reset()
		m.input.SetHeight(2)
		m.refreshInputPlaceholder()
```

- [x] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestTranscriptViewportSizing|TestPasteMsgFoldsBeforeTextareaConsumesNewlines|TestManualNewlineGrowsComposerWithoutHidingFirstLine|TestManualNewlineCanExceedVisibleComposerRows|TestComposerHeightReflowsWhenTerminalShrinksAndGrows|TestEmptyComposerShowsOnlyPrompt|TestSoftWrappedInputGrowsComposerAndShrinksTranscript|TestStatusLineRenderedHeightMatchesBudget|TestComposerBadgeJoinDoesNotExceedFrameWidth|TestInputOwnedOverlaysKeepComposerBox'
```

Expected: PASS（短终端用例：MinHeight=2 ≤ MaxHeight clamp 无异常）。

- [x] **Step 5: gofmt + 全量 + 提交**

```bash
$(go env GOROOT)/bin/gofmt -w internal/cli/chat_tui.go internal/cli/chooser.go internal/cli/chat_tui_test.go
go test ./internal/cli/
git add internal/cli/chat_tui.go internal/cli/chooser.go internal/cli/chat_tui_test.go
git commit -m "feat: give the composer a two-row minimum height"
```

---

### Task 4: fenced 代码块按 token 着色

**Files:**
- Modify: `internal/cli/md.go`（imports、`highlightCodeLine`、`renderFenced`）
- Test: `internal/cli/md_test.go`

- [x] **Step 1: 更新/新增失败测试**

`internal/cli/md_test.go` — `TestRenderConstructs` 的用例结构加 `strip bool` 字段（D5），fenced 用例置 `strip: true`，循环内 strip 后再断言：

```go
	cases := []struct {
		name     string
		in       string
		contains []string
		notRaw   []string // substrings that must NOT appear (raw markdown leaking through)
		strip    bool     // assert on ANSI-stripped output (token styling splits plain text)
	}{
```

```go
		{
			name:     "fenced code",
			in:       "```go\nfunc main() {}\n```\n",
			contains: []string{"func main()"},
			strip:    true,
		},
```

```go
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := r.Render(tc.in)
			if tc.strip {
				out = ansi.Strip(out)
			}
			for _, want := range tc.contains {
```

`internal/cli/md_test.go` — 新增 `TestHighlightCodeLine`（放在 `TestRenderConstructs` 之后；期望值已用参考实现核对，dark codex：keyword 176 / string 149 / number 179 / comment 66 / muted 253）：

```go
func TestHighlightCodeLine(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "keyword and plain text",
			in:   "func main() {}",
			want: "\x1b[38;5;176mfunc\x1b[0m\x1b[38;5;253m main() {}\x1b[0m",
		},
		{
			name: "number before comment",
			in:   "x := 1 // note",
			want: "\x1b[38;5;253mx := \x1b[0m\x1b[38;5;179m1\x1b[0m\x1b[38;5;253m \x1b[0m\x1b[38;5;66m// note\x1b[0m",
		},
		{
			name: "double-quoted string shields comment chars",
			in:   `s := "hi"`,
			want: "\x1b[38;5;253ms := \x1b[0m\x1b[38;5;149m\"hi\"\x1b[0m",
		},
		{
			name: "hash comment at line start",
			in:   "# todo",
			want: "\x1b[38;5;66m# todo\x1b[0m",
		},
		{
			name: "keywords and literals",
			in:   "return err == nil",
			want: "\x1b[38;5;176mreturn\x1b[0m\x1b[38;5;253m err == \x1b[0m\x1b[38;5;176mnil\x1b[0m",
		},
		{
			name: "hex number",
			in:   "total := count + 0x1F",
			want: "\x1b[38;5;253mtotal := count + \x1b[0m\x1b[38;5;179m0x1F\x1b[0m",
		},
		{
			name: "empty line",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := highlightCodeLine(tc.in); got != tc.want {
				t.Fatalf("highlightCodeLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

- [x] **Step 2: 运行确认失败**

```bash
go test ./internal/cli/ -run 'TestRenderConstructs|TestHighlightCodeLine'
```

Expected: FAIL（`highlightCodeLine` 未定义；fenced 用例裸文本断言失败）。

- [x] **Step 3: 实现**

`internal/cli/md.go` — imports 追加 `"regexp"`。

`internal/cli/md.go` — `renderFenced` 替换为：

```go
func (r *mdRenderer) renderFenced(buf *strings.Builder, n ast.Node, src []byte, indent int) {
	prefix := strings.Repeat(" ", indent) + dim("│ ")
	for i := 0; i < n.Lines().Len(); i++ {
		l := n.Lines().At(i)
		line := strings.TrimRight(string(l.Value(src)), "\n")
		buf.WriteString(prefix)
		buf.WriteString(highlightCodeLine(line))
		buf.WriteString("\n")
	}
	buf.WriteString("\n")
}
```

`internal/cli/md.go` — `renderFenced` 之后追加：

```go
// codeHighlightRe tokenizes one fenced-code line for highlightCodeLine. The
// alternatives are tried left-to-right at the earliest position, so a string
// literal containing "//" or a keyword is consumed whole before the comment or
// keyword rules can see inside it. Capturing-group order is load-bearing:
// 1-2 comment, 3-5 string, 6 number, 7 keyword.
var codeHighlightRe = regexp.MustCompile(strings.Join([]string{
	`(//.*$)`,             // 1: // comment to end of line
	`(^[ \t]*#.*$)`,       // 2: hash comment at line start
	`("(?:\\.|[^"\\])*")`, // 3: double-quoted string
	`('(?:\\.|[^'\\])*')`, // 4: single-quoted string
	"(`[^`]*`)",           // 5: backtick string
	`(\b\d[\w.]*\b)`,      // 6: number literal (int, float, 0x hex)
	`(\b(?:func|return|if|else|for|range|go|defer|select|switch|case|default|break|continue|package|import|var|const|type|struct|interface|map|chan|nil|true|false|new|make|len|cap|append|string|int|error|bool|byte|rune)\b)`, // 7: keyword
}, "|"))

// highlightCodeLine applies light token colouring to a single fenced-code line:
// comments, strings, numbers and keywords get their theme colours and every
// other segment renders in the muted body colour. Segments are consumed
// left-to-right so an earlier token (e.g. a string) shields its content from
// later rules (e.g. "//" inside a string).
func highlightCodeLine(line string) string {
	if line == "" {
		return ""
	}
	var b strings.Builder
	pos := 0
	for pos < len(line) {
		loc := codeHighlightRe.FindStringSubmatchIndex(line[pos:])
		if loc == nil {
			b.WriteString(muted(line[pos:]))
			break
		}
		if loc[0] > 0 {
			b.WriteString(muted(line[pos : pos+loc[0]]))
		}
		tok := line[pos+loc[0] : pos+loc[1]]
		var c cliColor
		switch {
		case loc[2] >= 0 || loc[4] >= 0: // comment
			c = activeCLITheme.codeComment
		case loc[6] >= 0 || loc[8] >= 0 || loc[10] >= 0: // string
			c = activeCLITheme.codeString
		case loc[12] >= 0: // number
			c = activeCLITheme.codeNumber
		default: // keyword
			c = activeCLITheme.codeKeyword
		}
		b.WriteString(themeFg(c, tok))
		pos += loc[1]
	}
	return b.String()
}
```

- [x] **Step 4: 运行确认通过**

```bash
go test ./internal/cli/ -run 'TestRenderConstructs|TestHighlightCodeLine|TestTableCodeSpanNeutral|TestRenderEmpty|TestRenderCopy'
```

Expected: PASS（`TestTableCodeSpanNeutral`/inline code 用例确认未受影响）。

- [x] **Step 5: gofmt + 全量 + 提交**

```bash
$(go env GOROOT)/bin/gofmt -w internal/cli/md.go internal/cli/md_test.go
go test ./internal/cli/
git add internal/cli/md.go internal/cli/md_test.go
git commit -m "feat: token-highlight fenced code blocks"
```

---

### Task 5: 全量回归与收尾检查

**Files:**
- 无代码改动预期；若回归发现 pin 遗漏，回到对应 Task 修复并补 commit。

- [x] **Step 1: 全量验证**

```bash
$(go env GOROOT)/bin/gofmt -l internal/
go build ./...
go test ./internal/cli/ ./internal/i18n/...
```

Expected: `gofmt -l` 无输出；build 通过；测试仅剩两个已知失败（`TestRenderMCPManagerDetailCompactsConfigPath`、`TestLoadCCSwitchLegacyConfigPrefersCorvusFlag`）。

- [x] **Step 2: spec 覆盖自查**

逐条核对 spec §1–§6 是否全部落地：
- §1 两个新样式 + 默认指向 codex/codex-light ✓（T1）
- §2 dark/light 色表全部槽位 ✓（T1，border 按 D1）
- §3 输入框相对背景 tint + 探测接入 ✓（T2）
- §4 输入框默认 2 行 ✓（T3）
- §5 fenced token 着色 + 兼容性 ✓（T4）
- §6 自动联动元素（无需改动）——确认本计划未改 `gitstatus.go` badge、未改 `composer_selection.go` 渲染链路 ✓
- 测试矩阵全部条目已覆盖（`theme_test.go`、`md_test.go`、`statusline_test.go`、`status_footer_test.go`、`chat_tui_test.go`、`toolcard_test.go` 复跑、`color_discipline_test.go` 复跑）✓

- [ ] **Step 3: 手工冒烟（可选，需真终端）**（未执行：需真实 TTY，留待用户验证）

```bash
go run .  # 或 go build -o /tmp/corvus . && /tmp/corvus
```

Expected: 默认主题为 Codex 蓝；输入框为相对背景的提亮透明框（2 行）；fenced 代码块有 token 颜色；`/theme graphite` 仍可切回暖橘。

- [x] **Step 4: 收尾**

- 确认 `git log --oneline -5` 显示本计划 4 个 commit（T1–T4）。
- 向用户汇报完成情况，并按 superpowers 流程（`finishing-a-development-branch`）决定是否需要 push/PR。
*** End Patch
