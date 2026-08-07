# Codex Blue Theme Design

> 状态：设计定稿（已按 subagent 联网审查修订），等待用户审阅
> 日期：2026-08-07
> 相关模拟：`docs/tui-preview.html`（用户已通过该页面选定 Codex 蓝配色并导出参数）
> 审查记录：2026-08-07 设计审查 subagent（含联网核对 256 色表、Codex 官方配色、x/ansi API）——3 个阻塞问题已在本版修订（见 §2 表注、§3、§4）

## 目标

把 corvus 的默认主题从暖橘（graphite）重新设计为 Codex 蓝风格（dark + light 两套），输入框背景改为"相对终端背景"的提亮填充并默认 2 行，fenced 代码块从"整行 accent 色"改为按 token 着色。旧主题全部保留可切换。

## 背景

- 现状默认 dark 主题 graphite 的 accent 为 `#d97757`（暖橘），代码块（`md.go renderFenced`）整行用 `accent()` 渲染，是"整体都是橘色"的主要来源。
- 用户在 `docs/tui-preview.html` 中选定 Codex 蓝主题，并导出确认参数：输入框相对背景提亮 `+8%`、与背景混合 `84%`、默认 `2` 行、宽度 `100%`；颜色表见 §2。
- 已确认决策：codex 蓝设为**新默认**（graphite 等保留）；dark 与 **light 一起改**。
- 联网参考：Codex 官方 CLI 深底 accent 为 Cyan、浅底 `#005f87`，桌面 App accent ≈ `#339cff`；本规格 `#4a9bff` 属同族蓝，作参考不照搬。

## 设计决策

### 1. 主题机制（`internal/cli/theme.go`）

- `cliThemeStyles` 新增两个条目：
  - `{name: "codex", mode: "dark", accent: #4a9bff, description: "codex blue accent"}`，插入到 graphite **之前**（dark 默认样式取 `cliThemeStyles[0]`）。
  - `{name: "codex-light", mode: "light", accent: #3b6fd4, description: "codex blue light accent"}`，插入到 sandstone 之前。
- `defaultCLIThemeStyle(mode)`：light 分支返回 `codex-light`（现为 sandstone）。
- graphite/ember/aurora/midnight/sandstone/porcelain/linen/glacier 全部保留，`/theme` 仍可切换；`CORVUS_THEME`/`CORVUS_THEME_STYLE` 环境变量路径（`theme.go:134-139`）无需改动。
- `cliPalette` struct 新增 4 个字段：`codeKeyword / codeString / codeNumber / codeComment`（dark/light 各给值）；`applyCLIThemeStyle` 只覆盖 accent/selection/userBubbleFaded，其余字段来自 base palette。
- `cliDarkTheme` 与 `cliLightTheme` 按下方色表重设计。

### 2. 色表

> **xterm 列约定**：仓库的 xterm 列是**手选 curated 表**（`fgSGR/bgSGR` 非 truecolor 下直接用 `cliColor.xterm`，全仓无运行时 hex→256 转换）。本表沿用该体系：灰阶沿用原 xterm（faint 245 / subtle 247）；深灰蓝系（border、bubble）按标准 6×6×6 欧氏最近档（237/235），**不采用 `ansi.Convert256` 的低索引映射**（237→23、235→17）。所有新值已用两种算法复核。

**dark（Codex 蓝）** — 用户确认值 → theme 槽位映射：

| 槽位 | hex | xterm | 说明 |
|---|---|---|---|
| accent / selection | `#4a9bff` | 75 | ◆、❯、光标、◆ 回答标记（`ansi.Convert256` 亦为 75） |
| info / toolRead | `#5eb0ff` | 75 | Read/Grep 类工具、链接（256 色下与 accent 同档，truecolor 区分；接受） |
| toolProc | `#c792ea` | 176 | Proc 类工具 |
| secondary | `#b18cff` | 141（保持） | thinking、辅助紫 |
| success | `#6fce8a` | 78 | 状态 ok、成功 |
| warn | `#e2b93b` | 179 | 警告 |
| err | `#f0706e` | 203 | 错误 |
| muted | `#d6dde8` | 253 | 正文 |
| faint | `#8a93a3` | 245（保持） | 弱文字 |
| subtle | `#a4adbc` | 247（保持） | 次级文字 |
| toolArg | `#b6c2d4` | 146 | 工具参数 |
| border | `#27334a` | 237 | 边框/分隔 |
| userBubbleBG | `#222631` | 235（保持现状） | 用户气泡（与输入框 fallback 区分） |
| inputBoxBG（fallback） | `#1c2534` | 235 | 无终端探测时的输入框底色 |
| codeKeyword | `#c792ea` | 176 | 代码关键字 |
| codeString | `#9ece6a` | 149 | 代码字符串 |
| codeNumber | `#e0af68` | 179 | 代码数字 |
| codeComment | `#6a7485` | 66 | 代码注释 |

保持不动：diffAddBG `#14351d`/22、diffDelBG `#3a1619`/52、danger `#e5484d`/167、userBubbleFaded（由 accent 派生）。

**light（Codex 蓝）** — 同系浅色版重设计（冷色蓝灰替换暖色砂岩系）：

| 槽位 | hex | xterm | 说明 |
|---|---|---|---|
| accent / selection | `#3b6fd4` | 62 | 深蓝，保证浅底对比度 |
| info / toolRead | `#2f6fd4` | 26 | Read 类工具、链接 |
| toolProc | `#8a6bb8` | 97（保持） | Proc 类工具 |
| secondary | `#7d63c8` | 104（保持） | thinking、辅助紫 |
| success | `#4d8f57` | 65（保持） | 重设计（hex 由 `#5d9b66` 调冷） |
| warn | `#a97c1a` | 136（保持） | 重设计（hex 由 `#b68120` 调冷） |
| err | `#c94f4d` | 131（保持） | 重设计（hex 由 `#b94b4d` 调冷） |
| muted | `#3d4552` | 238（保持） | 重设计（hex 由 `#4a453e` 调冷） |
| faint | `#6a7280` | 243（保持） | 重设计（hex 由 `#82796f` 调冷） |
| subtle | `#555d6b` | 240（保持） | 重设计（hex 由 `#7a7269` 调冷） |
| toolArg | `#5a6470` | 240（保持） | 保持 |
| border | `#d9dde4` | 253（保持） | 重设计（hex 由 `#e6ddd0` 调冷） |
| userBubbleBG | `#eef1f6` | 255（保持） | 重设计（hex 由 `#f5f0e8` 调冷） |
| inputBoxBG（fallback） | `#eceff4` | 255（保持现状） | 输入框底色 |
| codeKeyword | `#7d63c8` | 104 | 代码关键字 |
| codeString | `#4d8f57` | 65 | 代码字符串 |
| codeNumber | `#b68120` | 136 | 代码数字 |
| codeComment | `#6a7280` | 243 | 代码注释 |

保持不动：diffAddBG `#e5f3e7`/254、diffDelBG `#fae8e8`/255、danger `#e5484d`/167。

### 3. 输入框背景：相对终端背景（`internal/cli/theme.go`）

- 新增纯函数：
  ```go
  // inputBoxTintFromBackground 按探测到的终端背景计算输入框填充色：
  // dark 提亮 8%，light 加深 8%，再按 84% 与背景混合（模拟半透明叠加）。
  // 实际有效提亮为 6.72%（0.84 × 0.08）。
  func inputBoxTintFromBackground(rgb terminalRGB, dark bool) cliColor
  ```
  计算：`tint = dark ? mixHex(bg, "#ffffff", 0.08) : mixHex(bg, "#000000", 0.08)`；`final = mixHex(bg, tint, 0.84)`；`xterm = ansi.Convert256(colorprofile.Color(final)).ID()`。
- 新增辅助函数 `mixHex(a, b string, t float64) string`（hex 按比例混合，纯函数可测）。**不自写 256 近似**——运行时换算直接用现成依赖 `github.com/charmbracelet/x/ansi` 的 `ansi.Convert256`（go.mod 已有，colorprofile 的 ANSI256 分支同源）。
- 接入点：`buildCLITheme` 在 base palette 与 style 覆盖确定后、`return` 前：
  ```go
  if rgb, ok := activeBackgroundProbe(); ok {
      base.inputBoxBG = inputBoxTintFromBackground(rgb, base.name == "dark")
  }
  ```
  与 `resolveCLIThemeMode` 的现有全局 probe 用法一致；`withTerminalProbe` 只在启动/`/theme auto` 的 TTY 路径安装（`cli.go:119-124`），`/theme` 手动切换与全部测试走 `noTerminalBackground` → fallback 色。`TestRuntimeAutoThemeDoesNotProbeStdin` 继续守护。
- 渲染链路不变：`renderComposerField` → `composerFieldBackground()` → `bgSGR(activeCLITheme.inputBoxBG)`；NO_COLOR 下仍返回空。
- 已知表现：256 色终端下深色背景经 `ansi.Convert256` 会落到低索引深色（与手选体系差异大），且 dark 分支结果随真实背景变化——可接受，与现有降级行为一致。

### 4. 输入框默认 2 行（`internal/cli/chat_tui.go`）

- `configureChatTextarea`：`ti.MinHeight = 1` → `ti.MinHeight = 2`（约 `chat_tui.go:656`）。**只改 `SetHeight(2)` 无效**：`DynamicHeight=true` 时空内容会 `recalculateHeight` 回 1，必须由 `MinHeight` 托底；`SetHeight(1)` 保持（被 clamp 到 2）。
- `maxInputRows` 保持 8；`inputHeightLimit`/`syncInputHeightLimit` 逻辑不变。
- 连锁：`chooser.go:172` 自由文本模式 `m.input.SetHeight(1)` 会被 clamp 到 2 行（接受；测试确认无布局回归）；极短终端 `MaxHeight < MinHeight` 时仍可能为 1 行（textarea 内部 clamp，注明）。
- 影响：composer 初始占 2 行 → `bottomRows()` +1，`transcriptHeight` -1；相关布局测试更新（见测试矩阵）。

### 5. fenced 代码块按 token 着色（`internal/cli/md.go`）

- `renderFenced` 改为逐行调用新函数：
  ```go
  // highlightCodeLine 对单行代码做轻量 token 着色：
  // 注释/字符串/数字/关键字 → 对应主题色，其余文本 → muted。
  func highlightCodeLine(line string) string
  ```
- token 规则（正则，按优先级）：
  1. 注释：`//...` 到行尾；兼容行首 `#` 注释。
  2. 字符串：双引号 `"(?:\\.|[^"\\])*"`、单引号 `'(?:\\.|[^'\\])*'`、反引号 `` `[^`]*` ``。
  3. 数字：`\b\d[\w.]*\b`（含 0x 前缀）。
  4. 关键字（Go 常用子集）：`func|return|if|else|for|range|go|defer|select|switch|case|default|break|continue|package|import|var|const|type|struct|interface|map|chan|nil|true|false|new|make|len|cap|append|string|int|error|bool|byte|rune`（`\b` 边界）。
- 颜色：`activeCLITheme.codeKeyword / codeString / codeNumber / codeComment`；普通文本 `muted()`。
- 兼容性（已核实）：`renderFenced` 不走 `wrapAnsi`（直接写 buf），嵌入 SGR 无换行破坏风险；token SGR 各自 reset（`style.go` 的 `sgr` 包裹）无嵌套问题；超长行仍会溢出（现状行为）。inline code（`CodeSpan`）保持 `accent()`（表格内为 muted），不在此次改动。
- 其余 md 渲染（标题、列表、引用、表格）不改。

### 6. 自动联动的元素（无需单独改动）

accent 变蓝后自动变化的元素：输入框 ❯ 提示与光标、◆ 回答标记、用户气泡 ❯、列表符号、分隔线、`userBubbleFaded`（派生）、tool 卡 shell 工具 `●` 与动词、banner wordmark、选择高亮。footer 中 model 用 info（变蓝）、cache 用 secondary（紫）、状态 ok 用 success（绿）。

**不在联动范围**：模式 badge（Ask/Plan/Yolo/Shell）的颜色来自 `gitstatus.go:127-132` 硬编码（`#f59e0b`/`#2563eb`/`#e5484d`/`#16a34a`），不随 theme accent 变化——本次不改。

## 测试矩阵

`internal/cli/theme_test.go`：
- 默认 dark style 断言 `graphite` → `codex`；默认 light style `sandstone` → `codex-light`。
- dark accent 断言（`ansiAccent` 相关处）改用 codex 蓝 escape（新增 `ansiCodexAccent` 常量或字面量；`ansiAccent` 保留给显式 graphite 测试）。
- `TestThemeRendersAtProfileFidelity`（显式 graphite）：**不改**，仍 pin `#d97757`。
- `TestComposerTintAndCursorFollowTheme`：`inputBoxBG` fallback pin → `cliColor{"#1c2534", 235}`（dark）/ `cliColor{"#eceff4", 255}`（light 不变）。
- `toolArg` pin：dark `#a5b0bd/145` → `#b6c2d4/146`。
- `userBubbleFaded` pin：`#a87c6e` → 新 accent `#4a9bff` 派生值（按 `fadedUserBubbleColor` 计算后回填）。
- 新增：`inputBoxTintFromBackground`/`mixHex` 纯函数测试（dark 提亮、light 加深、84% 混合、有效值 6.72%）。
- 新增 env 用例：`CORVUS_THEME=codex`、`CORVUS_THEME_STYLE=codex-light` 解析。
- 新增：`withTerminalProbe` 下 `inputBoxBG` 被 tint 覆盖、probe 关闭时回退 fallback 的用例。
- 确认不改：`theme_sweep_test.go`（显式 graphite/sandstone）、`TestConfigureCLIThemeStyleOverride`（显式 aurora/glacier）。

`internal/cli/md_test.go`：
- 现有 fenced code 用例只断言内容，应仍通过。
- 新增 `highlightCodeLine` 测试：四类 token 各自 SGR、普通文本 muted、混合行。

`internal/cli/statusline_test.go`：
- `:443-446` info SGR pin：dark `38;5;80` → `38;5;75`、light `38;5;25` → `38;5;26`（effortTag）。
- `:157,336` badge 硬编码 `#2563eb`：**不改**。

`internal/cli/status_footer_test.go`：
- `TestStatusFooterSemanticPaletteAcrossThemes`（`:97-130`）：info pin 80/25 → 75/26；label/value/secondary xterm 不变（252/247、238/243、141/104）。

`internal/cli/chat_tui_test.go`：
- `:3429-3440` `TestPasteMsgFoldsBeforeTextareaConsumesNewlines`：折叠粘贴后 `Height()==1` → `==2`。
- `:512-529` `TestManualNewlineGrowsComposerWithoutHidingFirstLine`：补"空态默认 2 行"断言；`TestEmptyComposerShowsOnlyPrompt` 补第二行存在性。
- `:305` 附近 `TestTranscriptViewportSizing`：bottomRows `2→3`、viewport `22→21`（24 行终端）。
- `TestStatusLineRenderedHeightMatchesBudget`（:449）、`TestComposerBadgeJoinDoesNotExceedFrameWidth`：按实际失败更新。
- `TestInputOwnedOverlaysKeepComposerBox`（:1275）：相对引用，确认即可。
- 短终端（height 12）用例（`TestManualNewlineCanExceedVisibleComposerRows` 等）：复跑确认 MinHeight=2 ≤ MaxHeight clamp 无异常。

其他：
- `internal/cli/toolcard_test.go:83-110` `TestToolArgPairwiseDistinct`：复跑确认新 xterm 值互不冲突（146 vs 245/247/75/78/179/176/75）。
- `internal/cli/color_discipline_test.go`：确认新色值只进 `theme.go`（`ansi.Convert256` 调用也在 theme.go 内）。
- 确认不改：`transcript_test.go`/`bench_test.go` 的 `mixedBlocks`（含 173 SGR 模拟数据，与默认主题无关）。

## 范围外

- `docs/tui-preview.html` 不改（纯设计工具）；如时间允许可同步其 codex 主题默认值。
- `/status`、diff 语法高亮、MCP/技能等 manager 界面：跟随 palette 自动变化，不做单独设计。
- 输入框宽度：保持现状全宽；`maxInputRows` 保持 8。
- 模式 badge 硬编码色：本次不改。
- 浅色模式下输入框"加深"的视觉验证（与 dark 对称设计，模拟页未覆盖 light）。

## 风险与说明

- 大量测试 pin 旧色值，本测试矩阵已全仓 `rg` 枚举；实现时若有遗漏按失败更新。
- 终端背景探测只在启动/`/theme auto` 路径生效；手动切换主题时输入框用 fallback 色（已在 §3 说明）。
- 256 色终端下相对背景计算使用 `ansi.Convert256` 近似，与 truecolor 有轻微色差（可接受，与现有降级行为一致）。
- `/theme` 三语言文案（`messages_en.go:158`、`messages_zh.go:159`、`messages_zh_tw.go:153` 的 `ThemeHint`）：补充一句"启动时探测到终端背景时，输入框底色随背景自动调整"（复用 `themeArgItems` 中 auto 的 desc 措辞）。
