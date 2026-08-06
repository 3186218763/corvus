# P1.5 设计审查报告：TUI Render Animation & Art Polish

**审查对象:** `docs/superpowers/specs/2026-08-06-tui-render-animation-design.md`
**对照:** 总路线图 spec（P0–P4）、P1 plan（写作惯例）、调研综述 00-synthesis.md
**方法:** 逐条对照 spec 全文 + 代码事实核查（chat_tui.go 的 Update wrapper、ClearScreen 调用点、elapsed/tool frame 动画入口、md.go/style.go/diffview.go 的 SGR 字面量、i18n fmt 串、现有测试）

---

## 1. 内部一致性（§4/§5 ↔ §10 ↔ §6 ↔ §7 ↔ §8）

**结论：需修改。** §10 决策日志与 §4/§5 大体一致（D2↔4.1、D3↔4.2、D4↔4.3、D5↔5.1、D6↔5.2、D9↔5.4、D10↔5.5、D11/D12↔5.8 均对得上），但存在以下断裂：

1. **§1 说 tool frames 动画无门控（gap），§5.1/§8 却不 gate 它（P0）。** 代码事实：`tickToolRunning()`（braille 帧循环）由 `elapsedTickMsg` 驱动（chat_tui.go:1766），elapsed ticker 在 reduce-motion 下照常运行，因此「静态 spinner + 不再调度 spinner.Tick」并不能停掉工具行 braille 动画。§8「static spinner, instant scroll, no shimmer」未含静态 tool frame。改法：§5.1 增加消费者「tool working 行：motion off 时固定第一帧字形、`tickToolRunning` 不再推进 `toolStreamFrame`」；§6/§8 同步补断言与验收。
2. **§5.4 误列 `ChatStatusRetryingFmt`（P0）。** 代码事实：`ChatStatusRetryingFmt` 的参数是 `spinner, retryAttempt, retryMax`（chat_tui.go:2818），没有秒数参数；elapsed 只出现在 `ChatStatusThinkingFmt`（2825）、`ChatStatusCancellingFmt`（2823）与 `ChatToolWorkingFmt`（两个调用点：2431、2452），共 4 处。改法：§5.4 删除 RetryingFmt；§6「both fmt call sites」改为「全部 4 个调用点（status 2 + tool 2）」并逐一列举。
3. **§5.8 与 §6 的 `TestNoHardcodedColorCodes` 自相矛盾且会红（P0）。** 代码事实：除 diffview.go 外，`style.go:29` 还有 `ansiAccent = "\033[38;5;173m"`（测试锚点，theme_test.go 依赖），所以「the only hardcoded SGR colors」说法不成立；而测试又恰好排除 style.go，等于把唯一的另一个硬编码色藏进豁免区且未给理由。另外正则 `3x` 会命中 `md.go:53` 的斜体 `\033[3m`（非颜色）→ 迁移完成后测试必红。改法：(a) 正则改为颜色专用且要求 `m` 结尾：`38;5、48;5、38;2、48;2、[34][0-7]m、9[0-7]m、10[0-7]m`；(b) 明确 style.go 豁免理由（ansiAccent 是测试钉死序列）或一并迁移；(c) 措辞改为「渲染代码中唯一的硬编码色」。
4. **§4.1 会破坏现有测试，§7 文件地图漏了 `chat_tui_test.go`（P1）。** 事实：`TestRegularForceGotoBottomScrollJumpRequestsClearScreen`（chat_tui_test.go:2917 附近）与 `TestSessionSwitchSuppressesOneClearScreen`（2921，含「later scroll jumps must still request ClearScreen」）在默认关闭后必红；§8 却承诺「full suite green」。改法：文件地图加 `internal/cli/chat_tui_test.go`，并在 §6 写明「同任务内改写这两个测试：默认无 ClearScreen、env=1 恢复、sessionSwitch 语义保留」。
5. **§3.7「唯一默认行为变化是三个 measured fixes」与 §5.4/5.5/5.6/5.7 矛盾（P1）。** 固定宽度 elapsed、窄屏 banner、密度修正、表格中性色都是默认可见变化。改法：改为「默认的*运行时/架构*变化只有三个 measured fixes；§5 的默认可见变化已逐一枚举」。
6. **§6 的「render-counting harness」未定义机制（P1）**，见第 3 节(e)。
7. **§7 文件地图遗漏：** `internal/cli/i18n.go`（fmt 注释 `%d = elapsed s` 需同步改）、`style.go`（豁免理由）、`chat_tui_test.go`（见上）、`renderMainManagerFooter` 未进 §4.3 缓存清单（它在 bottomRows:1944 与 View:2950 各渲染一次）。§5.4 说「all three locales」✓，但 i18n.go 的 Messages 结构注释没列。
8. **§8 可测量性：总体可测。** 自动项（benchmark、ClearScreen 默认/兜底、panel 单次渲染、motion gate、elapsed 宽度、branding 两档、密度、表格、颜色纪律）均可自动化；「no visible jank」「verify on user terminal」是人工项，缺一个明确的终端清单（建议钉死 3–4 个：Warp/iTerm2/Windows Terminal/konsole）。缺一条「动画不破坏键盘操作」回归项（见第 6 节）。

## 2. 与总路线图一致（§2/§3/§12 总 spec）

**结论：通过，需加注记。** 

- 未违反：总 spec §2 non-goal 5/6/8、§3 hard rules（P1.5 §3.5/3.6 与总 spec 键盘完整、复用运行时真相一致）、§12「P4 must not block P1 merge」——P1.5 在 P1 合并（c91edc8）之后启动，P4 项提前不阻塞任何合并，方向合理。
- 「P4 拉前」与总 spec 不冲突，因为总 spec 只规定 P4 不阻塞 P1，并未禁止提前实现；§13.4 的 P4 性能约束（避免 O(n²) 全量重渲）正是 P1.5 §4.2 的落点。P1.5 §2 non-goal 1/5 也正确保留了 P4 剩余（copy reliability、doc parity、scrollback 外置）。
- **需修改（P1）：** 总 spec 没有 P1.5 这个阶段标签，§18.3「Do not start P2/P3/P4 UI until the corresponding plan exists」的语义需要一条注记。改法：在总 spec §12 加一行「P1.5（单独审批）：渲染流畅度/动画/美术——提前实现 P4 的 streaming/long-scroll 性能与窄屏 polish 子集；P4 剩余项不变」，并在 §18.3 注明 P1.5 的 plan 已独立接受；P1.5 spec 头部也应显式列出「拉前 = streaming/long-scroll perf + narrow polish，剩余 = copy reliability/doc parity/scrollback 外置」，避免路线图双源。
- 微小冲突：总 spec §11 P1 theming 是「body vs chrome 对比」，P1.5 不动 hierarchy ✓；总 spec §2 goal 5「每阶段可独立合并、用户可见」——P1.5 是完整垂直切片 ✓。

## 3. 歧义与二义性

**结论：需修改，以下每项指定一种理解：**

1. **smooth scroll 的 lerp（P1）。** §5.2 只写「lerp + ease-out」，未定义曲线、取整、终值。指定：固定 `dur=150ms`（单一常量，不要区间 120–180ms）、tick 16ms；`t=(now-start)/dur` 截断 [0,1]；ease-out cubic `e=1-(1-t)^3`；`y=from+round((to-from)*e)`；最后一拍强制 `y=to`（防取整停滞）。目标页距：PgUp/PgDn = `±(viewport.Height()-1)`（与现 PageUp/PageDown 一致），wheel = ±3 行。
2. **offsets 数组精确维护与 O(1) 断言（P1）。** §4.2 的「O(1) truncate」与不变量「len==nBlocks+1、offsets[0]==0、last==total」矛盾：头截断后剩余 offsets 需整体减去基址（O(nBlocks) rebase，或放弃首元素恒 0 的不变量）。指定：不变量保持 `offsets[0]==0`；Append = wrap 新块 + 追加 lines + `offsets=append(offsets, len(wrappedLines))`；Set i = 替换 `[offsets[i],offsets[i+1])` + 对 `j>i` 全部 `offsets[j]+=delta`（允许 O(nBlocks)）；头截断 = `lines=lines[offsets[L]:]`、丢弃 `offsets[:L]`、剩余全部减去 `offsets[L]`（写为 O(nBlocks) rebase，压实测即可，别再宣称 O(1)）。
3. **reduce_motion 判定位置（P1）。** `motionEnabled()` 未定义求值位置。指定：包级函数、每次调用读 env（t.Setenv 可测、无启动缓存状态）；`scrollRepaint` 则在 `newChatTUI()` 内读一次存字段（与 `REASONIX_DISABLE_MOUSE` 同一处，chat_tui.go:612）。§4.1 的「(cli.go, same place other env toggles are read)」与事实不符——现有 TUI env 开关都在 chat_tui.go/theme.go 读，cli.go 只负责 API key；改法：改为「newChatTUI 内、与 REASONIX_DISABLE_MOUSE 同处」。
4. **窄屏 branding 阈值（P2）。** 「≥60 cols」未说明是哪个宽度。指定：以 `renderTUIBanner` 收到的 width 参数（= `transcriptContentWidth(m.width, m.nativeScrollback)`）为准，≥60 宽版、<60 窄版；窄版截断规则（如超宽省略号）由实现定。
5. **渲染计数 harness（P1）。** 指定机制：包级测试钩子 `var panelRenderHook func(name string)`（生产仅一行 `if panelRenderHook != nil { panelRenderHook(name) }`），`renderTodoPanel` 等经 `m.renderPanel("todo", fn)` 走钩子；测试断言每轮 Update 后各面板计数为 1。明确「不引生产计数器字段」。
6. **motion off 的静态 spinner 字形（P2）。** 指定：固定显示 `spinner.View()` 当前帧且不再调度 `spinner.Tick`（保持现有帧字形，避免引入新字符）；tool 行同理固定第一帧。
7. **smooth scroll 生命周期边界（P1）。** 未定义：动画中内容增长（SetContent）时目标越界、Esc 与动画的关系、动画结束是否回到 AtBottom。指定：每拍重新 clamp 到当前 max；内容变化不清动画但 clamp；Esc/任意键不取消动画（tick 链自终止，最长 150ms，保证无卡键）；动画结束若 `to==max` 则 `AtBottom()` 为真、tail-follow 自然恢复（补测试）。
8. **i18n 固定宽度单位（P1）。** `formatElapsedFixed` 返回 `"  3s"`（含英文 s）会在 zh/zh_tw 拼出「  3s 秒」冗余。指定：函数只返回右对齐 4 宽数字（`"  3"`），单位留在各 locale fmt 串里（en `s`、zh `秒`）；每 locale 宽度各自稳定即满足「无抖动」。

## 4. 范围（是否适合一个实现计划 / YAGNI）

**结论：通过，需补顺序与边界。** 

- 三个轨道可以在**一个 plan 内按顺序**拆分（4.1→4.2→4.3 → 5.1→5.2→5.4 → 5.5→5.6→5.7→5.8），因为三个轨道都落在 `chat_tui.go` 的 Update wrapper 与 View 上，并行会冲突；§11 的「three tracks」应写成「plan 内的三组顺序任务」而非并行轨道。每个 §4.x/§5.x 对应一个任务（含 failing test → 实现 → commit），shimmer 是唯一例外（spike-gated）。
- **YAGNI 基本干净：** 无 overlayStack、无新主题、无 config 段、无 scrollback 外置。两处越界风险：(a) §5.6 密度审计范围未封口——「fix inconsistencies found」可无限蔓延；改法：边界限定为「连续空行/块间 margin 不一致，以回归测试（mixed transcript 无连续两个空行）为唯一验收」，不重排布局。(b) shimmer 的 spike 无人认领（谁、何时、go/no-go 判据）——改法：在 plan 里作为 0.5 天的可选 spike 任务并写明「无明确增益则丢弃」（§10 D7 已有判据，补执行位即可）。
- §5.7 措辞是条件式（「verify…if syntax-highlighted」）而 §8 是无条件验收，二者需对齐：改法为「先查证，若已中性则只加测试；若带色则改渲染」，§8 相应写成「table code cell 中性（或查证后已中性 + 测试存在）」。

## 5. 风险覆盖（§9）

**结论：需修改，缺 4 项关键风险：**

1. **增量 wrap 与现有 live tool/reasoning 更新路径的交互（P1，§9 未覆盖）。** 现有 re-wrap 由 `len(transcript)!=prevLines || width!=prevWidth || transcriptDirty` 触发（chat_tui.go:841），而 mutation 点不止 append：`transcript[i]` 原地改写（tool working 2452、reasoning tail）、collapse、/clear（chat_tui.go:1832–1833 同时清 wrappedLines）、session switch replay、banner commit——任一漏接都会让 offsets 缓存漂移。改法：§9 加风险行，并规定 plan 开头必须有「transcript mutation inventory」章节（先例：总 spec §16.1 要求 P2 plan 先做 runtime inventory），逐点列出 cache 接线。
2. **smooth scroll × ClearScreen env 兜底并存（P1，审查重点点名）。** env=1 时每个 16ms 滚动 tick 都改变 YOffset → 每次 Update 触发一次 ClearScreen（16ms 风暴，闪烁反而更严重）。指定：`REASONIX_TUI_SCROLL_REPAINT=1` 时禁用平滑滚动（退回瞬时跳），并在 §8 加一条测试。
3. **`tea.Batch` 中 tick 调度 × motion gate 的测试可行性（P1，审查重点点名）。** tea.Cmd 是不透明闭包，无法断言「没有调度 tick」。指定测试缝：门控时调度函数直接返回 `nil` cmd（`spinner` 分支只返回 `elapsedTick()`；scroll 分支返回 nil），测试断言 `cmd == nil`；动画推进用合成消息（如 `scrollTickMsg`）驱动 + 可注入时钟（`from,to,start,dur,now` 纯函数），不用真实 time.Sleep。
4. **panel cache 首帧/无 Update 的 View（P2）。** bubbletea 首帧 View 可能先于任何 Update；指定 cache 为空时回退为「按需渲染且不缓存」或「渲染一次并缓存」，与 bottomRows 现有 fallback（statusLineCount==0 → +2）一致。

## 6. 成功标准（§8）

**结论：需修改。** 每条都可验证（自动为主），缺两条回归项：

1. **「动画不破坏键盘操作」（审查重点点名，缺）。** 增补：动画中按任意键（Esc/输入/翻页）后键盘行为与现有 Esc 栈、draft、composer 焦点完全一致；smooth scroll tick 链自终止（≤150ms 无残留 tick）。对应 §6 加测试。
2. **env 组合矩阵（缺）。** 增加：`SCROLL_REPAINT=1 + REDUCE_MOTION=1`、`SCROLL_REPAINT=1 + 平滑滚动`（断言为瞬时跳）、native scrollback + 平滑滚动（断言无插值，native 路径不参与）。
3. §8「Append cost ~58ms → <2ms per token」依赖 `BenchmarkAppendBlock` 的定义——补一句「10k 行基线追加 1 个新块（模拟 1 个 token 的增量）」，并说明这是基准报告（非断言，与 §6 一致）。

## 7. P1 plan 惯例对齐

**结论：需修改，spec 需先补以下约定（让 writing-plans 一次到位）：**

1. **任务粒度与顺序（P1）：** 1 任务 = 1 个 §4.x/§5.x 项；顺序 = fluidity → motion → art；shimmer spike 独立可选任务。沿用 P1 plan 的「Files / Interfaces / Steps（failing test → run → implement → run → commit）」模板。
2. **现有测试改写并入同一任务（P1）：** §4.1 任务必须包含改写 chat_tui_test.go 的 ClearScreen 测试（先改测试再实现，天然 TDD），保证 §8「suite green」成立。
3. **plan 开篇章节（P1）：** 「transcript mutation inventory」（第 5 节第 1 条）+「测试缝清单」（tick 合成、cmd==nil、panelRenderHook、可注入时钟），先例即总 spec §16.1。
4. **提交与分支（P2）：** §7 已写 TDD/小提交/worktree ✓；补提交信息前缀约定（P1 用 `feat(cli):`/`test(cli):`/`style(cli):`）与「一个任务一个提交」。
5. **spec 覆盖自查表（P2）：** P1 plan 有「Spec coverage (self-review)」表，P1.5 spec 可在 §7 后补一张 §4/§5 各项 → 任务映射，防漏项。
6. **环境变量文档落点（P2）：** §9 只说「document」，未指目标；指定 `internal/cli/README` 或 `?` cheatsheet 的 env 段（择一）。

---

## 修订清单（按严重度）

### P0（阻塞，须在 plan 前修）
1. §5.4 误列 `ChatStatusRetryingFmt`（无秒数参数）；elapsed 实为 4 个调用点（Thinking/Cancelling + ToolWorking×2），§5.4/§6 全改。
2. §5.8/§6 颜色纪律测试：正则 `3x/4x` 误伤 `md.go:53` 斜体 `\033[3m`；style.go 的 `ansiAccent`（38;5;173）使「only hardcoded」不成立且豁免无理由。修正则为颜色专用正则 + 写明 style.go 豁免依据（测试锚点）。
3. §1 点名 tool frames 无门控，§5.1/§8 却漏 gate `tickToolRunning`（其挂在 elapsedTickMsg 上，reduce-motion 下仍会动）。补消费者 + 测试 + 验收。
4. §4.1 破坏 chat_tui_test.go 现有 ClearScreen 测试（2917/2921 两族），§7 文件地图漏 `chat_tui_test.go`；补文件 + 同任务改写。

### P1（应改，写 plan 前定稿）
5. §4.2 offsets 头截断的 O(1) 断言与不变量冲突；指定算法（O(nBlocks) rebase）并修正复杂度表述。
6. smooth scroll 规格补全：单一 dur=150ms、ease-out cubic 公式、终拍 snap、页距定义、每拍 reclamp、内容增长/键盘/Esc/AtBottom 边界语义。
7. smooth scroll × `SCROLL_REPAINT=1`：legacy 下禁用平滑滚动，§8 补 env 组合矩阵。
8. 运动门控测试缝：调度函数 gate 时返回 nil、tick 合成消息、可注入时钟；`motionEnabled()` 定为每次调用读 env，`scrollRepaint` 定在 `newChatTUI()` 读（纠正「cli.go」说法）。
9. §4.3：refresh 时序钉在 m.update 之后、computeStatusLineCount 之前；cache 空首帧回退；`renderMainManagerFooter` 进缓存清单。
10. §5.4 i18n：`formatElapsedFixed` 只回 4 宽数字，单位归各 locale fmt（避免 zh「  3s 秒」）；§7 文件地图补 `i18n.go`。
11. §3.7 措辞改为「默认运行时/架构变化仅 three fixes；§5 默认可见变化已枚举」。
12. §4.1 env 读取位置修正 + §9 加「增量 wrap mutation inventory」风险行（plan 开篇章节）。
13. 总 spec §12/§18 加 P1.5 注记（拉前项/剩余项），P1.5 头部显式列出拉前清单。
14. §8 补「动画不破坏键盘操作」回归项与自终止断言。
15. §5.6 密度审计封口（仅空行/margin 不一致 + 回归测试为验收）；shimmer spike 定执行位（plan 内 0.5 天可选任务 + go/no-go）。

### P2（可选）
16. 终端验证清单（Warp/iTerm2/Windows Terminal/konsole）写入 §9 或 §8 人工项。
17. 提交信息前缀约定、spec-coverage 自查表进 §7。
18. env 变量文档落点（README 或 cheatsheet env 段）。
19. §5.7 条件式措辞与 §8 对齐；窄屏截断规则；静态 spinner 字形定义。
20. `BenchmarkAppendBlock` 定义补「1 块 = 1 token 增量」；颜色测试同时锁定 `48;2`。

---

## 总结（≤15 行）

1. **P0-1**：§5.4 把 `ChatStatusRetryingFmt` 当 elapsed 站点是事实错误（它是 attempt/max），elapsed 实际 4 个调用点，需改 §5.4/§6。
2. **P0-2**：颜色纪律测试正则会误伤 md.go 斜体 `\033[3m`，且 style.go 的 `ansiAccent` 使「唯一硬编码色」不成立——修正则、给 style.go 豁免理由。
3. **P0-3**：§1 点名的 tool frame 动画没进 §5.1 门控（它挂在 elapsed tick 上），reduce-motion 下仍会动，需补 gate + 测试。
4. **P0-4**：§4.1 会红掉 chat_tui_test.go 现有 ClearScreen 测试，文件地图漏 `chat_tui_test.go`，须同任务改写。
5. **P1 重点**：钉死 smooth scroll 公式与边界（单一 150ms、ease-out cubic、终拍 snap、内容增长 reclamp、legacy env 下禁用）、offsets 截断算法（O(nBlocks) rebase）、运动门控测试缝（gate 时返回 nil cmd、合成 tick），并给总 spec §12 加 P1.5 注记。
