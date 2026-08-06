# 技术事实核查：TUI Render Animation & Art Polish 设计（2026-08-06）

核查对象：`docs/superpowers/specs/2026-08-06-tui-render-animation-design.md`（重点 §4 流畅度、§5 动效美术）。
核查方法：读仓库实际代码（commit `c91edc8` 后工作树）+ 模块缓存源码（`/home/miku/go/pkg/mod`：bubbletea/v2@v2.0.7、lipgloss/v2@v2.0.4、bubbles/v2@v2.1.0、ultraviolet）+ 复跑 `BenchmarkWrapTranscript`。标注：`【事实】`=代码直接证据；`【推断】`=由此推得的设计含义。

---

## 1. Scroll repaint（chat_tui.go:859）——【属实】（一处表述需修正）

- 【事实】`chat_tui.go:859-861` 与 spec 描述完全一致：
  `if cm.viewport.YOffset() != prevYOff && !cm.nativeScrollback && !cm.sessionSwitch { return cm, tea.Batch(tea.ClearScreen, cmd) }`。
  注释（855-858）明说 Warp 兼容 workaround。native scrollback 路径被 `!cm.nativeScrollback` 挡住，今天无 clear——与 spec「Native scrollback path is unchanged」一致。
- 【事实】「Every viewport offset change returns ClearScreen」在 Update wrapper 内成立；但仓库还有其它 `tea.ClearScreen` 调用（chat_tui.go:1067/1080/1089/1126/1187/1707/1791），属 paste/composer/session 路径，不是滚动，不构成反例。
- 【事实→修正】`tea.ClearScreen()` 的 v2 语义不是「发 `CSI 2J`」：`ClearScreen() Msg` 返回 `clearScreenMsg`（screen.go:14-26），事件循环收到后调 `renderer.clearScreen()`（tea.go:864-865）＝ `scr.MoveTo(0,0)+scr.Erase()`（cursed_renderer.go:633-639），即全缓冲标记脏 + 下一 flush 整帧重绘；且若已协商 DEC 2026，该帧会被 BSU/ESU 包裹。因此 §4.1 引用 Claude #35580 的「`CSI 2J` inside a sync block」是 v1 行为近似，用作 v2.0.7 下删除 clear 的理由时表述需改（当前 clear 是整帧重绘而非内联 2J；不过「滚动即全清」确实与 cell-diff 渲染器冲突的结论仍成立）。
- 【建议】条件按 spec 加 `&& cm.scrollRepaint` 即可；把 rationale 改为「tea.ClearScreen → 全缓冲 Erase，抵消 ultraviolet 仅写脏行的优化」。

## 2. 增量 wrap 缓存——【部分属实，2 处关键偏差】

- 【事实】`wrapTranscript(s, w)` = `lipgloss.NewStyle().Width(w).Render(s)`（transcript.go:327-332）；Update wrapper 在 `len(transcript) != prevLines || width 变化 || transcriptDirty` 时全量 `wrapTranscript(strings.Join(...))` + `strings.Split`（chat_tui.go:840-844）——「每次内容增长全量重 wrap」属实。
- 【事实】`cm.wrappedLines` 字段已存在（chat_tui.go:211）；`wrappedBlockOffsets`/`wrapBlock`/`rebuildWrappedLines` 不存在，属新增。
- 【事实】set/append/remove/truncate 四个操作存在：`appendTranscriptBlock`/`setTranscriptBlock`/`removeTranscriptBlock`/`truncateTranscriptBlocks`（transcript.go:54/60/69/78）。
- 【不属实】「流式热路径总是最后一个 block」不成立：`streamToolOutput` 显式支持「早先已派发工具的迟到 ToolProgress」→ `m.toolStreamIdx = shellTranscriptIdx[id]`（chat_tui.go:2150-2156），此时写的是**非末尾** block（新工具的 block 在其后）。
- 【不属实/缺口】`streamToolOutput`（chat_tui.go:2202）、`tickToolRunning`（2452）、`collapseShellSlot`（chat_tui.go:2318/2331，`collapseToolOutput` 的落点）都**直接改 `m.transcript[idx]`**，绕过 `setTranscriptBlock`/`transcriptSources`；`transcriptDirty` 有 8+ 个设置点（chat_tui.go:1401/2090/2203/2285/2399/2453/2491/2531/2552/3779 等）。增量缓存必须把这些直接写点全部纳入，否则行数偏移漂移——spec 只枚举了四个 helper 操作，未覆盖这些点。
- 【事实】bench 数字属实：本机复跑 `BenchmarkWrapTranscript`（-benchtime=1x）＝ 5,000 行 ≈ 28.1ms/10.0MB、10,000 行 ≈ 56.7ms/20.3MB；研究文档 28.0/57.6ms。spec「≈28ms/10MB、≈58ms/20MB per token」成立。
- 【推断】`renderTranscript` 依赖 `wrappedLines` 每行已被 lipgloss Width 补足到 cw（transcript.go:447-451 注释）——`wrapBlock` 必须逐 block 复刻该 padding，否则空白行补齐逻辑失效。
- 【建议】§4.2 补一节「直接索引写点清单」（2202/2318/2331/2452）与 `transcriptDirty` 全设置点，说明它们如何改走缓存；把「总是最后一个 block」改为「answer/reasoning 流是末尾 block；tool 迟到进度会写中间 block」。

## 3. bubbletea v2.0.7 渲染机制——【部分属实，1 条核心错误】

- 【事实】真实渲染只有 `cursedRenderer`（ultraviolet 驱动）；`nilRenderer` 仅 `disableRenderer` 时用（tea.go:1058-1062）。应用用 `tea.NewProgram(m)` 无任何选项（cli.go:563）→ 默认 cursed + 默认 60fps。
- 【事实】`defaultFPS=60`、`maxFPS=120`（renderer.go:13-14）；`WithFPS` 可用（options.go:139-144，tea.go:626-629 钳制）。「默认 60fps」属实。
- 【事实】独立 goroutine 的 ticker 循环存在（startRenderer，tea.go:1391-1405）：每 tick 调 `p.flush()` + `renderer.flush(false)`——「渲染循环独立于事件循环」属实。
- 【不属实】**`View()` 不在渲染循环里被调用**：`View()` 由事件循环每次 Update 后调用一次（tea.go:887-889 `p.render(model)` → `model.View()`）；60fps ticker 只把渲染器缓冲 flush 到终端。`viewEquals` 相同帧在 renderer.flush 早退（cursed_renderer.go:285-289）。所以 spec §1/§3 原则 4/§4.3 的「View runs on the 60fps render loop」「View runs up to 60/s」表述错误；研究文档 charm-capabilities.md:12 本身是准确的（View 构建在事件循环、flush 在 ticker），是 spec 复述时放大。
- 【事实】DEC 2026 自动协商：仅当 `shouldQuerySynchronizedOutput`（tea.go:972-989，已知良好终端名单 + 排除 SSH/Apple Terminal）时发 `\x1b[?2026$p`（tea.go:1109-1113）；应答 `ModeSynchronizedOutput` → `setSyncdUpdates(true)`（tea.go:788-792）；每帧 BSU/ESU 包裹（cursed_renderer.go:528-556）。「自动协商」属实，注意是**有条件的**协商。
- 【事实】ClearScreen 语义见第 1 条。
- 【推断】§4.3「Update 先于每个渲染帧 → 缓存不陈旧」的推理只对 View 成立（View 每事件一次）；「渲染循环里重复渲染面板」的动机本身不成立——重复来自同一事件内 `bottomRows()` 与 `View()` 各渲染一遍（见第 6 条），不是 60fps 循环。

## 4. bubbles/viewport ——【属实】

- 【事实】viewport.go 全文无任何插值/平滑滚动代码（只有 `clamp` 与 `maxYOffset`）——「无平滑滚动，需自研」属实。
- 【事实】`SetYOffset(n)` 公开、`clamp(n, 0, maxYOffset)`（viewport.go:464-466）——可作插值目标。
- 【事实】语义：`PageDown=ScrollDown(Height())`、AtBottom 时 no-op（486-500）；`ScrollUp(n)=SetYOffset(YOffset()-n)`（530-540）；`GotoTop` AtTop 早退、`GotoBottom` 无条件 `SetYOffset(maxYOffset())`（581-598）；`AtBottom = YOffset() >= maxYOffset`（187-190）。
- 【事实】应用侧 wheel=ScrollUp(3)/ScrollDown(3)（chat_tui.go:907-909）、PgUp/PgDn（1140-1143）、GotoTop/GotoBottom（1146-1149）——与 §5.2 输入映射一致。
- 【事实】bubbles v2 viewport **没有 Update 方法**（keymap 独立）——平滑滚动只能由应用层 `tea.Tick` 驱动，与 spec 方案一致。
- 【推断】插值期间 `AtBottom` 在到达 `maxYOffset` 前为 false，与 spec「动画中不在底部、tail-follow 不受影响」一致；注意 wrapper 的 ClearScreen 分支（scrollRepaint 默认关）不会打断插值。

## 5. lipgloss v2 ——【属实】

- 【事实】v2.0.4 无 `Gradient` 方法（UPGRADE_GUIDE_V2.md 只列 `BorderForegroundBlend`/`BorderForegroundBlendOffset` 用于边框渐变）；无渲染缓存（模块内无 render.go/cache 文件，style.go 无 cache/sync 字段）。
- 【事实】`Blend1D(steps int, stops ...color.Color) []color.Color` 存在（blending.go:14-67），CIELAB 插值；另有 `Blend2D`。可预生成 shimmer 色表，属实。
- 【推断】shimmer 需把 `color.Color` 经 lipgloss Style 转 SGR（主题已有 256 色回退路径，theme.go:324-336），实现成本低。

## 6. bottomRows/View 面板双渲染与缓存方案——【部分属实，方案有 2 个时序缺陷】

- 【事实】`bottomRows()`（chat_tui.go:1917-1964）渲染 10 个面板（todo/approval/chooser/rewind/mcpImport/resume/quick/copy/cheatsheet/completion）+ native 模式下 mainManager + mainManagerFooter；`View()`（2863+）对同样 10 个面板**再渲染一遍**（2895-2925）+ 无条件再渲 mainManager（2964，alt-screen 分支也要）+ mainManagerFooter。
- 【事实·修正数字】每事件实际是 **3 遍**：Update wrapper 内 `syncInputHeightLimit→inputHeightLimit`（829→3530）与 `transcriptHeight→bottomRows`（831→1978）各调一次 `bottomRows()`，加上 View 一遍；native-scrollback 提交路径 `finalize→scrollChunkHeight→bottomRows`（1817→1852）在 `update()` **内部**还要再算。spec「render twice per frame」低估为 2 遍，且「View 在 60fps 循环」前提错误（见第 3 条）。
- 【存疑】「refreshBottomPanels() 在 Update wrapper 末尾执行」与 lockstep **不兼容**：
  1. wrapper 顺序是 computeStatusLineCount（826）→ syncInputHeightLimit（829）→ SetHeight(transcriptHeight())（831），都在 m.update 之后、wrapper 末尾之前。若 refresh 在末尾，同一事件内 transcriptHeight/bottomRows 读到**上一事件**的面板 → 面板出现/消失（如 approval banner、chooser 打开）的帧 viewport 高度错位一帧，下一事件才纠正。
  2. native-scrollback 的 `finalize` 在 `update()` 内部消费 `bottomRows()`（1817），refresh 必然在其后 → chunk 高度用旧状态。
  3. 启动路径 `prepareNativeScrollback(os.Stdout, m.bottomRows())`（cli.go:552）在 Update 循环之外。
- 【事实】`computeStatusLineCount`（3385-3411）与 `hideComposer`（1968-1974）只读状态字段（spinner/elapsed/chooser 等），不读面板渲染——缓存不破坏它们；只要 refresh 顺序正确，「View/bottomRows 读同一缓存」可保住 lockstep。
- 【建议】refresh 紧跟 `m.update` 之后、任何 bottomRows/statusLineCount 消费之前（甚至放在 wrapper 开头对 `update` 前的状态刷新亦可，但面板状态在 update 内变化，所以必须在 update 后、消费前）；native-scrollback 的 finalize 路径要么改为惰性刷新，要么明确接受「chunk 高度滞后一事件」。

## 7. env 开关读取位置——【不属实】（位置）

- 【事实】现有 TUI env 开关读在 **chat_tui.go / theme.go**，不是 cli.go：`REASONIX_DISABLE_MOUSE`（chat_tui.go:612，`mouseCaptureOffByDefault` 610-613）、`REASONIX_THEME`/`REASONIX_THEME_STYLE`（theme.go:130/138）；Termux 检测也在 chat_tui.go:659-662。cli.go 只有 API-key env（1224/1245）。
- 【事实】构造期读取模式：`newChatTUI`（chat_tui.go:552-595）里 `detectTermuxTerminal()`（561）与 `mouseCaptureOffByDefault()` 都是构造时求值——`scrollRepaint`/`reduceMotion` 加同款字段+构造读取即可。
- 【事实】「no config section exists」属实：config 与 internal/cli 无 motion/animation/reduce 配置键。
- 【建议】spec 的「cli.go, same place」改为「chat_tui.go newChatTUI / mouseCaptureOffByDefault 同款位置」。

## 8. i18n `%d`→`%s` ——【部分属实，RetryingFmt 前提错误】

- 【事实】三个语言文件里 `ChatStatusThinkingFmt`/`ChatStatusCancellingFmt`/`ChatToolWorkingFmt` 的秒参数都是 `%d`（en 写作 `%ds`：messages_en.go:45/46/48；zh:46/47/49；zh_tw:42/43/45）——属实。
- 【不属实】**`ChatStatusRetryingFmt` 没有秒参数**：`%d/%d` 是 attempt/max（i18n.go:72 注释；调用点 chat_tui.go:2818 传 `m.retryAttempt, m.retryMax`）。若按 spec 把它也切 `%s`，输出变 `%!s(int=3)`，并破坏 `retry_indicator_test.go:33/37`（断言含 `retrying (3/10)`）。RetryingFmt 应排除在改动外。
- 【事实】测试影响：`i18n_test.go` 的 `TestCatalogsAgreeOnPlaceholders` 只比对每语言 `%s/%d/%q` 等**动词个数**（countVerbs，i18n_test.go:28-46），`%d→%s` 不改变计数 → 三语言同步改不会挂。
- 【不完整】「both fmt call sites」低估：elapsed 实际出现在 6 个调用点（chat_tui.go:2823 cancelling、2825 thinking、2431/2452 tool working、2465/2480 `ChatThoughtForFmt`）；`ChatThoughtForFmt`（"thought for %ds"）有同样的列宽抖动但 spec 未纳入。
- 【事实】`renderTurnReceipt` 无 elapsed（status_footer.go:50-101）——属实。

## 9. banner / md 表格代码 / diff 颜色 ——【部分属实】

- 【事实】`renderTUIBanner(label, missing, width)` 是包级函数（chat_tui.go:4696-4706）：两行（`◆ reasonix · label` + tip）+ 可选 missing 警告行；以 `transcriptSourceBanner` 提交为 transcript block（chat_tui.go:4062、transcript.go:97/113），resize 时 `reflowTranscript` 会按新宽度重渲染（transcript.go:129-139）——宽窄屏（≥60/<60）改造可行，宽度参数现成；需处理窄屏下 missing 警告行的取舍。
- 【不属实·前提】md.go **没有任何语法高亮**：全文件无 chroma；fenced code 也只是 accent 单色（md.go:322-331 renderFenced）。表格 cell 走 `collectCells→collectInline→appendInline`，`CodeSpan` 一律 `accent()`（md.go:362-364），与正文相同。所以「table code cells may carry full syntax colors」不成立；实际是「表格内 code span 带 accent 前景色」。「如果语法高亮就中性化」的前提不存在，但「表格内 code span 改中性色」本身可实现（在 appendInline 增加 inTable 上下文或 collectCells 后处理）。
- 【事实】diffview.go:29-32 确有 4 个硬编码 256 色常量（48;5;22 / 48;5;52 / 1;38;5;46 / 1;38;5;203）——但全仓库**无任何引用，是死代码**；实际渲染早已用主题槽位：`bgSGR(activeCLITheme.diffAddBG)`/`fgSGR(activeCLITheme.success)` 等（diffview.go:116-119）。theme.go:34-44/67-98 确有 diffAddBG/diffDelBG/success/err 槽位。「migrate」实为「删除 4 个死常量」；另注意若真迁移，fg 46→success(108)、203→err(167) 色值会变（语义映射而非等价替换）。
- 【事实】internal/cli 非测试、非 theme/style 代码中其余 SGR 字面量只有 select.go 的 `\033[K` 擦行序列（10 处）与 OSC 标记（见第 10 条），删掉死常量后颜色纪律测试可绿。

## 10. TestNoHardcodedColorCodes 误伤面——【可行，但 spec 的模式写法会误伤】

- 【事实】若按 spec 的裸模式（`38;5`/`48;5`/`38;2`/`3x`/`4x`/`9x`/`10x`）扫描字符串字面量，存在真实误伤：
  1. `select.go:57/121/123/135/137/143/148/150/156/159` 的 `"\r\033[K..."` 擦行序列：八进制 `\033` 含子串 `"33"`，命中 `3x` 类模式（SGR 33=黄）——误伤 10 处。
  2. `transcript.go:231-232` 的 OSC 1337 copy-math 标记 `"\x1b]1337;reasonix-copy-math=..."`：`"1337"` 含 `"33"`，命中 `3x`——误伤。
  3. `theme_osc_unix.go:44` 的 `"\x1b]11;?\x07"`（OSC 11 查询）不含目标模式，安全；`\x1b[?2026` 类 DEC 序列目前 internal/cli 无字面量，`2026` 也不命中列出的模式，安全（但若将来按 `3x` 裸模式加 `\x1b[?25l` 等也不会命中——`25` 不匹配）。
  4. `theme.go:324-336` 的 SGR 构造器在排除名单内（theme.go/style.go/`*_test.go`），OK。
- 【建议】用 `go/ast` 提取字符串字面量，模式锚定 CSI+SGR 终结符：`\x1b\[(?:[0-9;]*m)` 且参数含颜色段（`38;5;`/`48;5;`/`38;2;` 或 `(?:3[0-9]|4[0-9]|9[0-9]|10[0-9])` 后接 `;` 或 `m`）；或更简单：排除 `\033[K` 与 OSC（`\x1b]`）后按现有列表扫。删除 diffview 死常量后应绿。

---

## 总结（按严重度排序）

1. **§3/§4.3「View() 在 60fps 渲染循环被调用」错误**——View() 每事件调用一次（tea.go:888），60fps ticker 只 flush；面板重复渲染来自同一事件内 bottomRows()×2 + View()×1（共 3 遍/事件，非 2 遍/帧）。需重写 §4.3 的动机与推理。
2. **§4.2「流式热路径总是最后一个 block」不成立**——streamToolOutput 的 shellTranscriptIdx 复用路径显式写中间 block，且 3 处直接改 `m.transcript[idx]`（2202/2318/2331/2452）绕过 setTranscriptBlock；增量缓存需覆盖这些写点。
3. **§4.3 缓存刷新时序缺陷**——「wrapper 末尾 refresh」会让同事件内 transcriptHeight/bottomRows 读旧面板，native-scrollback 的 finalize 路径（update 内部）与启动路径（cli.go:552）更读不到新缓存；refresh 须在 m.update 后、所有消费点之前。
4. **§5.4 RetryingFmt 无秒参数**（%d/%d=attempt/max），切 %s 会破坏输出与 retry_indicator_test；应只改 Thinking/Cancelling/ToolWorking（并考虑补 ChatThoughtForFmt）。
5. **§5.8 diff「迁移」实为删除 4 个死常量**（diffview.go:29-32 无引用），实际渲染已用主题槽位；若按 fg→success/err 真迁移颜色会变。
6. **§5.7 前提错误**——md.go 无语法高亮，表格 code span 只是 accent 单色（md.go:362-364）；「中性化表格 code span」仍可做。
7. **§4.1 的 CSI 2J 表述**是 v1 近似；v2 ClearScreen = 缓冲 Erase 整帧重绘（cursed_renderer.go:633-639）。
8. **§5/§7 env 位置**——TUI env 开关读在 chat_tui.go:612（REASONIX_DISABLE_MOUSE）与 theme.go:130，不是 cli.go；「no config section」属实。
9. **§6 TestNoHardcodedColorCodes 模式会误伤** `\033[K`（select.go 10 处）与 OSC 1337 标记（transcript.go:231）——需 CSI 锚定 + go/ast 解析。
10. 其余（bubbletea 渲染器/DEC 2026/WithFPS/ClearScreen 语义、viewport 无平滑滚动及 PgUp/PageDown/ScrollUp(3)/GotoBottom 语义、lipgloss 无 Gradient/无缓存/Blend1D、bench 数字 28/58ms、i18n 三语言 %d、renderTUIBanner 位置）——属实，仅按上文细节微调表述。
