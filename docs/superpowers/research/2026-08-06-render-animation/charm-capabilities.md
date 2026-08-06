# Bubble Tea v2 / Lip Gloss v2 / bubbles v2 渲染能力与终端协议调研

调研对象：Reasonix（Go agent CLI）。版本：`charm.land/bubbletea/v2@v2.0.7`、`charm.land/lipgloss/v2@v2.0.4`、`charm.land/bubbles/v2@v2.1.0`（本地模块缓存 `/home/miku/go/pkg/mod`）。标注：【事实】= 源码/官方文档已验证；【推断】= 基于源码的合理推导，需自行验证。

---

## 1. 渲染器机制

- 【事实】v2 只有 `cursedRenderer`（`bubbletea/v2@v2.0.7/cursed_renderer.go`，约 845 行）与 `nilRenderer`，不存在 v1 的 standard renderer。v2.0.0 发布说明称其为 "all-new Cursed Renderer"，基于 ncurses 渲染算法重建。
- 【事实】cursed renderer 底层是 `github.com/charmbracelet/ultraviolet` 的 `TerminalRenderer`：`Render(newbuf)` 先检查 `newbuf.TouchedLines() == 0` 则直接 return（`ultraviolet@.../terminal_renderer.go:1129-1133`），即只把「变脏的行」写入终端；滚动与换行另有 `terminal_renderer_hardscroll.go`、`terminal_renderer_hashmap.go` 优化（滚动整块移动、hash 加速行比较）。
- 【事实】行级 diff 在 cell buffer 层完成：`RenderBuffer.TouchedLines()`（`ultraviolet@.../buffer.go:690-691`）统计脏行数。官方 README 主张"只重绘变化、SSH 场景最小带宽"。
- 【事实】渲染循环与事件循环分离：事件循环（`bubbletea/v2@v2.0.7/tea.go` eventLoop，约 834 行）做 Msg→Update→render；渲染循环 `startRenderer()`（`tea.go:1393`）按 ticker 以 `time.Second/p.fps` 调 `p.flush()`（`tea.go:1221`）+ `renderer.flush(false)`。
- 【事实】FPS 常量：`defaultFPS = 60`、`maxFPS = 120`（`renderer.go:13-14`）；程序选项 `WithFPS(fps int)`（`options.go:139-142`），超出 1–120 会被钳制。
- 【事实】帧级跳过：若新 View 与上一帧相同（`viewEquals`，`cursed_renderer.go:287、803`），整帧跳过不写终端——动画中只有"内容真变了"才会输出。
- 【事实】alt-screen 不再是程序选项：v2 没有 `WithAltScreen`/`WithMouseCellMotion`（已 grep 确认不存在），全部改为声明式 `tea.View` 字段（`tea.go:84-190`）：`AltScreen bool`（149-161）、`MouseMode`（None/CellMotion/AllMotion，175-177、287-299）、`KeyboardEnhancements`（189）、`ReportFocus`（163-170）、`DisableBracketedPasteMode`（172-173）、`Cursor`、`BackgroundColor/ForegroundColor`（133-135）、`WindowTitle`、`ProgressBar`（145-147）。
- 【事实】inline 模式帧高 = View 内容高度，内容变多时终端会滚动补行；alt-screen 模式使用独立屏幕缓冲、程序退出时自动恢复。
- 【推断】alt-screen 适合"整页重绘"型界面（TUI 仪表盘）；inline 适合与 shell 输出混排的流式 agent 日志——两者渲染路径相同（同一个 renderer），只差是否切屏。
- 【事实】防闪烁：程序启动时自动查询 synchronized output（`tea.go:1113` 发送 `ansi.RequestModeSynchronizedOutput`）；支持 2026 时用其包裹更新，否则回退隐藏/显示光标策略（v2.0.0-rc.2 发布说明 "Smooth Operator (Mode 2026)"，默认启用）。

## 2. 动画 API 清单（含源码路径）

- 【事实】`tea.Tick(d, fn)`：`commands.go:154`。只发**一次**消息，从调用时刻计时；要在 Update 中返回新 `Cmd` 才能持续。适合等间隔帧驱动。
- 【事实】`tea.Every(d, fn)`：`commands.go:102`。按系统时钟对齐（补偿漂移），适合定时器语义。
- 【事实】`tea.Batch(cmds...)`：`commands.go:15`。并发组合、无顺序保证（结果进 `BatchMsg`）；`tea.Sequence(cmds...)`：`commands.go:25`。串行执行。两者都是"一次性"组合，持续动画仍需在 Update 中循环返回。
- 【事实】每帧成本 = Update + View() 重建成本 + 渲染器 diff 成本；写终端只发生在上帧 diff 出的脏行上（见 §1）。
- 【事实】bubbles/spinner（`bubbles/v2@v2.1.0/spinner/spinner.go`）：`Model.FPS time.Duration`（23 行），内部 `tick` 用自增 id/tag 丢弃过期帧（约 185 行 `tea.Tick(m.Spinner.FPS, ...)`），天然防"旧 tick 复活"。
- 【事实】bubbles/progress（`bubbles/v2@v2.1.0/progress/progress.go`）：基于 `github.com/charmbracelet/harmonica` 弹簧物理（14、212、289 行），`SetPercent`→`nextFrame()` 返回 `tea.Tick(time.Second/fps, ...)`（349-353 行），`FrameMsg`（181 行）驱动插值，`IsAnimating()`（435 行）到平衡点自动停。**进度条动画不要自己写**，直接用它。
- 【事实】bubbles/viewport（`bubbles/v2@v2.1.0/viewport/viewport.go`）：**没有平滑滚动、没有窗口同步、没有 `tea.Sync`**（v2 全包 grep 无 `func Sync`；v1 的 HighPerformanceRendering/同步机制在 v2 已移除）。API：`SetYOffset(int)`（464）、`ScrollDown/ScrollUp`（518/530）、`PageDown/PageUp`、`GotoTop`（581）/`GotoBottom`（591）、`SetContent`（226）/`SetContentLines`（233）、`SoftWrap`、`FillHeight`、`MouseWheelDelta`（69，默认 3，151 行）。
- 【事实】`tea.RequestWindowSize()`（`commands.go:173`）；窗口尺寸变化自动发 `WindowSizeMsg`，viewport 需在其中 `SetWidth/SetHeight`。
- 【事实】其他相关命令：`tea.RequestBackgroundColor()`（`color.go:13`）、`SetClipboard/ReadClipboard`（`clipboard.go:30/42`）、`RequestWindowSize`。

## 3. Lip Gloss v2 能力

- 【事实】**没有 `Gradient()` 方法**；渐变在 `lipgloss/v2@v2.0.4/blending.go`：`Blend1D(steps, stops...)`（18 行）与 `Blend2D(w, h, angle, stops...)`（114 行）返回色表，自己按色表逐字染色（README 有示例）。
- 【推断】lipgloss 不做动画渐变：需要在程序中预生成色表（如 Blend2D），再以 `tea.Tick` 逐帧平移偏移量/色标位置实现"流动渐变"。
- 【事实】布局：`Place`（`position.go:36`）、`PlaceHorizontal`（43）、`PlaceVertical`（90）；`JoinHorizontal`（`join.go:28`）、`JoinVertical`（116）；`Style.Width`（`set.go:286`）、`Style.MaxWidth`（`set.go:750`，截断经 `ansi.Truncate`，`style.go:505-510`）；Whitespace 按 rune 宽度填充（`whitespace.go`）。
- 【事实】**无缓存机制**：lipgloss v2.0.4 全包无 cache（grep 无命中）。每帧 `Style.Render` 都重新解析样式、重新计算宽度。
- 【推断】性能含义：高频动画里不要每帧重新构造 Style / 重复 Render 静态部分；应预构建 Style、把不变的整行字符串缓存复用，只对变化行调用 Render。
- 【事实】宽度计算委托 `charmbracelet/x/ansi`：`ansi.StringWidth`（`size.go:15-31`）→ `WcWidth`（go-runewidth）/`GraphemeWidth`（uniseg 字素簇，`canvas.go:27`）；runewidth 的 `EastAsianWidth` 默认 false，可用环境变量 `RUNEWIDTH_EASTASIAN=1` 开启（`x/ansi@v0.11.7/method.go:1-30`）——对中日韩宽字符对齐有影响。

## 4. 终端协议（常量均在 `x/ansi@v0.11.7`）

- 【事实】DECSET 2026 synchronized output：`SetModeSynchronizedOutput = "\x1b[?2026h"`、reset `"\x1b[?2026l"`、查询 `"\x1b[?2026$p"`（`mode.go:620-624`）。bubbletea 启动自动查询并按需包裹（见 §1）。
- 【事实】Kitty Keyboard Protocol（KVP）：`kitty.go` 提供 `KittyKeyboard(flags, mode)` 生成 `\x1b[=flags;mode u`（43 行）、`RequestKittyKeyboard = "\x1b[?u"`（22 行）、push/pop/disable（62/77/75 行）。flags 位掩码：1=disambiguate、2=event types、4=alternate keys、8=all keys as escape codes、16=associated text。bubbletea 侧用 `View.KeyboardEnhancements` 声明（`tea.go:189-264`），终端应答后收到 `KeyboardEnhancementsMsg`（`keyboard.go:7-57`，含 `SupportsAllKeysAsEscapeCodes()` 等）。
- 【事实】bracketed paste：`SetModeBracketedPaste = "\x1b[?2004h"`（`mode.go:608-611`）；bubbletea 默认启用，`View.DisableBracketedPasteMode` 可关；消息 `PasteStartMsg/PasteMsg/PasteEndMsg`（`paste.go`）。
- 【事实】OSC 52 剪贴板：`RequestSystemClipboard = "\x1b]52;c;?\x07"`（`clipboard.go:70`）；bubbletea 命令 `SetClipboard/ReadClipboard` 已封装（`clipboard.go:30/42`）。
- 【事实】背景色/暗亮：`tea.RequestBackgroundColor()` 经 OSC 11 查询（`color.go:13`），`BackgroundColorMsg.IsDark()`（`color.go:67-75`）判断暗亮背景。
- 【事实】色彩能力探测：`github.com/charmbracelet/colorprofile@v0.4.3`，`Detect` 综合 TERM/COLORTERM/NO_COLOR/CLICOLOR/tmux/terminfo（`env.go`），Profile 分 `NoTTY/ASCII/ANSI/ANSI256/TrueColor`（`profile.go`）；bubbletea 选项 `WithColorProfile(profile)`（`options.go:153`）。lipgloss 渲染时按 profile 降级颜色。
- 【事实】鼠标：`View.MouseMode` 用 `MouseModeCellMotion`/`MouseModeAllMotion`（`tea.go:287-299`），底层 1000/1003/1006 序列（`x/ansi/mode.go:484-488`）。

## 5. 性能边界

- 【事实】官方无 benchmark 数字：bubbletea/ultraviolet 模块内没有 benchmark 测试文件；官方只有定性主张（"highly optimized"、SSH 最小带宽）。
- 【事实】两处天然节流：渲染循环按 FPS ticker（默认 60、上限 120）；`viewEquals` 相同帧跳过输出。
- 【推断】CPU 成本主要由「每帧重建 View 的 Go 代码 + lipgloss 渲染 + diff 比较」组成，Tick 频率只决定这些代码被调用的次数；行数越多、样式越复杂，每帧越贵。60fps 下每帧只有 ~16.7ms 预算。
- 【推断】减少动画成本的手段：只动画小区域（spinner/progress 是单行）、缓存静态行、避免整屏 View 重建、避免高频 lipgloss 渐变全行染色。
- 【推断】大量行同时变化（如整页日志刷新）即使有行级 diff，首次全量输出仍昂贵；应限制可见行数、用滚动替代重绘。
- 【事实】`WithFPS` 可主动降到 30/24 换取 CPU；progress 弹簧动画自带 ~60fps 帧率常量（`time.Second/fps`，`progress.go:352`）。

## 6. 推荐技术路径（无闪烁流式输出 + 平滑滚动 + 轻量动画）

1. 【事实+推断】流式输出：用 `bubbles/viewport` 模型，新内容 `SetContent/SetContentLines`，需要自动跟随时 `GotoBottom()`；`WindowSizeMsg` 中 `SetWidth/SetHeight`。整页交互界面 `View.AltScreen = true`；要与 shell 日志混排则保持 inline（帧高自适应）。→ 基础组合，框架原生支持。
2. 【推断】平滑滚动：框架无内置，自行实现——`tea.Tick(30-60fps)` 驱动 float 目标偏移插值（如 ease-out），每帧 `viewport.SetYOffset(int(progress))`。只有视图行变化，diff 只重绘脏行，成本可控。标注：这是应用层设计，不是 bubbles 能力。
3. 【事实】防闪烁：依赖 v2 默认的 mode 2026 查询与包裹（`tea.go:1113`），无需手写序列；仅当终端不支持时才会用光标隐藏回退。
4. 【事实】轻量动画：状态指示用 `bubbles/spinner`（自带 FPS 与过期帧防护）；进度用 `bubbles/progress`（harmonica 弹簧，平衡自动停）；渐变装饰用 `lipgloss.Blend2D` 预生成色表 + `tea.Tick` 移偏移实现"流动"效果。
5. 【推断】性能纪律：预构建 lipgloss Style、缓存静态行；每帧只重建变化行；`WithFPS(30-60)` 按需下调；避免每帧全屏重排/重新染色。
6. 【事实】终端能力适配：`WithColorProfile` 显式指定探测结果；`BackgroundColorMsg.IsDark()` 适配暗亮主题；可选启用 `KeyboardEnhancements`（KVP）提升按键响应与可访问性。

## 7. 证据链接与源码路径

- 模块缓存（本机已核验）：`/home/miku/go/pkg/mod/` 下：
  - `charm.land/bubbletea/v2@v2.0.7/`：`tea.go`（View 字段/事件循环/2026 查询）、`renderer.go`（FPS）、`cursed_renderer.go`（diff/帧跳过）、`options.go`（WithFPS 等）、`commands.go`（Tick/Every/Batch/Sequence/RequestWindowSize）、`keyboard.go`、`clipboard.go`、`paste.go`、`color.go`、`UPGRADE_GUIDE_V2.md`。
  - `charm.land/lipgloss/v2@v2.0.4/`：`blending.go`、`position.go`、`join.go`、`size.go`、`set.go`（Style.Width/MaxWidth）、`whitespace.go`、`canvas.go`。
  - `charm.land/bubbles/v2@v2.1.0/`：`spinner/spinner.go`、`progress/progress.go`、`viewport/viewport.go`。
  - `github.com/charmbracelet/ultraviolet@v0.0.0-20260601155805-6cf7526a1b3f/`：`terminal_renderer.go`、`terminal_renderer_hardscroll.go`、`terminal_renderer_hashmap.go`、`buffer.go`。
  - `github.com/charmbracelet/x/ansi@v0.11.7/`：`mode.go`、`kitty.go`、`clipboard.go`、`method.go`。
  - `github.com/charmbracelet/colorprofile@v0.4.3/`：`env.go`、`profile.go`。
- 官方发布说明（GitHub，搜索快照已确认）：
  - `https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.0`（Cursed Renderer 基于 ncurses 算法；mode 2026 默认尝试启用）。
  - `https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.0-rc.2`（"Smooth Operator (Mode 2026)"：默认启用同步输出）。
- 注：v2.0.0 release 页面直接访问超时（2026-08-06），以上 URL 内容以搜索结果片段与本地模块源码交叉验证为准。
