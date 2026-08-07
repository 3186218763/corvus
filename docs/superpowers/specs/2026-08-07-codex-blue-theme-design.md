# Codex Blue Theme Design

> 状态：设计定稿，等待用户审阅
> 日期：2026-08-07
> 相关模拟：`docs/tui-preview.html`（用户已通过该页面选定 Codex 蓝配色并导出参数）

## 目标

把 corvus 的默认主题从暖橘（graphite）重新设计为 Codex 蓝风格（dark + light 两套），输入框背景改为"相对终端背景"的提亮填充并默认 2 行，fenced 代码块从"整行 accent 色"改为按 token 着色。旧主题全部保留可切换。

## 背景

- 现状默认 dark 主题 graphite 的 accent 为 `#d97757`（暖橘），代码块（`md.go renderFenced`）整行用 `accent()` 渲染，是"整体都是橘色"的主要来源。
- 用户在 `docs/tui-preview.html` 中选定 Codex 蓝主题，并导出以下确认参数：
  - 输入框：相对背景提亮 `+8%`、与背景混合 `84%`、默认 `2` 行、宽度 `100%`（保持全宽）。
  - 颜色：accent `#4a9bff`、info `#5eb0ff`、toolproc `#c792ea`、secondary `#b18cff`、success `#6fce8a`、warn `#e2b93b`、err `#f0706e`、kw `#c792ea`、str `#9ece6a`、num `#e0af68`、com `#6a7485`、muted `#d6dde8`、faint `#8a93a3`、subtle `#a4adbc`、toolarg `#b6c2d4`、border `#27334a`、bubble `#1c2534`。
- 已确认决策：codex 蓝设为**新默认**（A）；dark 与 light **一起改**（B）。

## 设计决策

### 1. 主题机制（`internal/cli/theme.go`）

- `cliThemeStyles` 新增两个条目：
  - `{name: "codex", mode: "dark", accent: #4a9bff, description: "codex blue accent"}`，插入到 graphite **之前**（dark 默认样式取 `cliThemeStyles[0]`）。
  - `{name: "codex-light", mode: "light", accent: #3b6fd4, description: "codex blue light accent"}`，插入到 sandstone 之前。
- `defaultCLIThemeStyle(mode)`：light 分支返回 `codex-light`（现为 sandstone）。
- graphite/ember/aurora/midnight/sandstone/porcelain/linen/glacier 全部保留，`/theme` 仍可切换。
- `cliDarkTheme` 与 `cliLightTheme` 两套 palette 按下方色表重设计（accent/selection/userBubbleFaded 仍由 `applyCLIThemeStyle` 按 style 覆盖）。

### 2. 色表

**dark（Codex 蓝）** — 用户确认值 → theme 槽位映射：

| 槽位 | hex | xterm | 说明 |
|---|---|---|---|
| accent / selection | `#4a9bff` | 75 | badge、◆、❯、光标、模式标签 |
| info / toolRead | `#5eb0ff` | 75 | Read/Grep 类工具、链接 |
| toolProc | `#c792ea` | 177 | Proc 类工具 |
| secondary | `#b18cff` | 141 | thinking、辅助紫 |
| success | `#6fce8a` | 114 | 状态 ok、成功 |
| warn | `#e2b93b` | 179 | 警告 |
| err | `#f0706e` | 203 | 错误 |
| muted | `#d6dde8` | 252 | 正文 |
| faint | `#8a93a3` | 245 | 弱文字 |
| subtle | `#a4adbc` | 247 | 次级文字 |
| toolArg | `#b6c2d4` | 146 | 工具参数 |
| border | `#27334a` | 237 | 边框/分隔 |
| userBubbleBG | `#1c2534` | 235 | 用户气泡 |
| inputBoxBG（fallback） | `#1c2534` | 235 | 无终端探测时的输入框底色 |
| codeKeyword | `#c792ea` | 177 | 代码关键字 |
| codeString | `#9ece6a` | 150 | 代码字符串 |
| codeNumber | `#e0af68` | 179 | 代码数字 |
| codeComment | `#6a7485` | 60 | 代码注释 |

保持不动：diffAddBG `#14351d`/22、diffDelBG `#3a1619`/52、danger `#e5484d`/167、userBubbleFaded（由 accent 派生）。

**light（Codex 蓝）** — 同系浅色版（保证浅底对比度）：

| 槽位 | hex | xterm |
|---|---|---|
| accent / selection | `#3b6fd4` | 27 |
| info / toolRead | `#2f6fd4` | 27 |
| toolProc | `#8a6bb8` | 97（保持） |
| secondary | `#7d63c8` | 104（保持） |
| success | `#4d8f57` | 65（保持） |
| warn | `#a97c1a` | 136（保持） |
| err | `#c94f4d` | 131（保持） |
| muted | `#3d4552` | 238（保持） |
| faint | `#6a7280` | 243（保持） |
| subtle | `#555d6b` | 240（保持） |
| toolArg | `#5a6470` | 240（保持） |
| border | `#d9dde4` | 253（保持） |
| userBubbleBG | `#eef1f6` | 255（保持） |
| inputBoxBG（fallback） | `#eceff4` | 255（保持现状） |
| codeKeyword | `#7d63c8` | 104 |
| codeString | `#4d8f57` | 65 |
| codeNumber | `#b68120` | 136 |
| codeComment | `#6a7280` | 243 |

保持不动：diffAddBG `#e5f3e7`/254、diffDelBG `#fae8e8`/255、danger `#e5484d`/167。

> xterm 值为手选近似（256 色表最近档）；实现时可用新增的 `closest256` 辅助函数重新计算并与表中值核对。

### 3. 输入框背景：相对终端背景（`internal/cli/theme.go`）

- 新增纯函数：
  ```go
  // inputBoxTintFromBackground 按探测到的终端背景计算输入框填充色：
  // dark 提亮 8%，light 加深 8%，再按 84% 与背景混合（模拟半透明叠加）。
  func inputBoxTintFromBackground(rgb terminalRGB, dark bool) cliColor
  ```
  计算：`tint = dark ? mix(bg, white, 0.08) : mix(bg, black, 0.08)`；`final = mix(bg, tint, 0.84)`；`xterm = closest256(final)`。
- 新增辅助函数（theme.go，纯函数可测）：
  - `mixHex(a, b string, t float64) string` — 两个 hex 按比例混合。
  - `closest256(hex string) int` — 标准 256 色表最近索引（6×6×6 立方体 + 灰阶）。
- 接入点：`buildCLITheme` 在 base palette 与 style 覆盖确定后、`return` 前：
  ```go
  if rgb, ok := activeBackgroundProbe(); ok {
      base.inputBoxBG = inputBoxTintFromBackground(rgb, base.name == "dark")
  }
  ```
  `activeBackgroundProbe` 仅在 `withTerminalProbe`（启动/`/theme auto` 的探测路径）下可用；**手动 `/theme` 切换时无探测 → 使用 palette fallback 值**。此行为写入 `/theme` 帮助文案的说明（若已有提及背景探测的文案，保持）。
- 现有渲染链路不变：`renderComposerField` → `composerFieldBackground()` → `bgSGR(activeCLITheme.inputBoxBG)`；NO_COLOR 下仍返回空（`colorOn()` 门控已有）。

### 4. 输入框默认 2 行（`internal/cli/chat_tui.go`）

- `configureChatTextarea`：`ti.SetHeight(1)` → `ti.SetHeight(2)`。
- `maxInputRows` 保持 8；`inputHeightLimit`/`syncInputHeightLimit` 逻辑不变。
- 影响：composer 初始占 2 行 → `bottomRows()` +1，`transcriptHeight` 相应 -1；相关布局测试更新（见测试矩阵）。

### 5. fenced 代码块按 token 着色（`internal/cli/md.go`）

- `renderFenced` 改为逐行调用新函数：
  ```go
  // highlightCodeLine 对单行代码做轻量 token 着色：
  // 注释/字符串/数字/关键字 → 对应主题色，其余文本 → muted。
  func highlightCodeLine(line string) string
  ```
- token 规则（正则，按优先级）：
  1. 注释：`//...`（行尾）；保留对 `#` 注释的语言兼容（仅当行首为 `#`）。
  2. 字符串：双引号 `"(?:\\.|[^"\\])*"`、单引号 `'(?:\\.|[^'\\])*'`、反引号 `` `[^`]*` ``。
  3. 数字：`\b\d[\w.]*\b`（含 0x 前缀数字）。
  4. 关键字：Go 常用子集 `func|return|if|else|for|range|go|defer|select|switch|case|default|break|continue|package|import|var|const|type|struct|interface|map|chan|nil|true|false|new|make|len|cap|append|string|int|error|bool|byte|rune`（`\b...\b` 边界）。
- 颜色来源：`activeCLITheme.codeKeyword / codeString / codeNumber / codeComment`（新增 palette 字段，dark/light 各有值）；普通文本 `muted()`。
- inline code（`CodeSpan`）保持 `accent()`（accent 变蓝后自然变为蓝）。
- 其余 md 渲染（标题、列表、引用、表格）不改。

### 6. 自动联动的元素（无需单独改动）

accent 变蓝后自动变化的元素：输入框 ❯ 提示与光标、模式 badge、◆ 回答标记、用户气泡 ❯、列表符号、分隔线、`userBubbleFaded`（派生）、tool 卡 shell 工具 `●` 与动词、banner wordmark、选择高亮。footer 中 model 用 info（变蓝）、cache 用 secondary（紫）、状态 ok 用 success（绿）。

## 测试矩阵

- `internal/cli/theme_test.go`：
  - 默认 dark style 断言：`graphite` → `codex`；默认 light style：`sandstone` → `codex-light`。
  - dark accent 断言（`ansiAccent` 相关处）：改为 codex 蓝的 escape（新增 `ansiCodexAccent` 常量或字面量；`ansiAccent` 保留给 graphite 显式测试）。
  - `TestThemeRendersAtProfileFidelity`（显式 `graphite`）：**不改**，仍 pin `#d97757`。
  - `TestComposerTintAndCursorFollowTheme`：`inputBoxBG` fallback pin 更新为 `cliColor{"#1c2534", 235}`（dark）/ `cliColor{"#eceff4", 255}`（light 保持）。
  - `toolArg` pin：dark `#a5b0bd`/240 → `#b6c2d4`/146。
  - `userBubbleFaded` pin：`#a87c6e` → 由新 accent `#4a9bff` 派生值（实现时按 `fadedUserBubbleColor` 计算后回填）。
  - 新增：`inputBoxTintFromBackground` 纯函数测试（dark 提亮、light 加深、84% 混合、closest256 值）。
- `internal/cli/md_test.go`：
  - 现有 fenced code 用例只断言内容，应仍通过。
  - 新增 `highlightCodeLine` 测试：四类 token 各自 SGR、普通文本 muted、混合行。
- 布局测试（输入框 2 行）：`TestTranscriptViewportSizing`、`TestStatusLineRenderedHeightMatchesBudget`、`TestInputOwnedOverlaysKeepComposerBox`、`TestManualNewlineGrowsComposerWithoutHidingFirstLine`、`TestComposerBadgeJoinDoesNotExceedFrameWidth`、composer 光标 Y 相关（`composer_selection_test.go` / `chat_tui_test.go`）——按实际失败逐一更新期望值。
- 其余 pin 旧色的测试（若有遗漏）按失败更新。

## 范围外

- `docs/tui-preview.html` 不改（纯设计工具）；如时间允许可同步其 codex 主题默认值。
- `/status`、diff 语法高亮、MCP/技能等 manager 界面：跟随 palette 自动变化，不做单独设计。
- 输入框宽度：保持现状全宽；`maxInputRows` 保持 8。
- 不在本次范围：浅色模式下输入框"加深"的视觉验证（与 dark 对称设计，模拟页未覆盖 light）。

## 风险与说明

- 大量测试 pin 旧色值，计划阶段需逐文件枚举（上述矩阵 + 全仓 `rg` 补充）。
- 终端背景探测只在启动/`/theme auto` 路径生效；手动切换主题时输入框用 fallback 色（已在决策 3 说明）。
- 256 色终端下相对背景计算使用 `closest256` 近似，与 truecolor 效果有轻微色差（可接受，与现有 `bgSGR` 降级行为一致）。
