# Codex CLI 渲染与视觉设计调研

> 面向 Reasonix（Go + Bubble Tea v2）的专项调研。调研对象：openai/codex（Rust + Ratatui + crossterm），基于 main 分支源码、`styles.md`、`clippy.toml`、PR 与官方文档。「事实：」为源码/文档可验证内容；「推断：」为基于事实的分析。

## 1. 渲染机制

### 1.1 帧渲染：全量重画 + buffer diff 最小化输出
事实：
- `custom_terminal.rs`：Ratatui 双缓冲（previous/current 两个 `Buffer`）。每帧渲染代码完整重画 buffer，再由 `diff_buffers()` 只把变化的单元格输出为 `DrawCommand::{Put, ClearToEnd}`。
- 行尾优化：每行从右向左扫描，只有与行尾 bg 不同的格子才重画；其后整段用单个 `ClearToEnd` 清掉，避免发多个空格 `Put`（源码注释明确是性能优化）。
- 输出状态跟踪：发送时记住当前 fg/bg/modifier，仅变化时重发；相邻格子不重复发 `MoveTo`。
- 宽字符缩小时，Ratatui `ForcedWidth` 路径会跳过尾部单元格失效，diff 逻辑补了额外失效处理。
- 整帧输出包在 crossterm `SynchronizedUpdate` 里（`tui.rs` 引入确认），防止绘制撕裂。

推断：
- 「渲染逻辑永远画全量、输出层做最小化 diff」：正确性简单、性能由输出层保证。Bubble Tea 没有等价 buffer diff 层，但该分层思想可借鉴。

### 1.2 流式渲染：稳定前缀 + 只重渲染尾部
事实：
- `streaming/render.rs`：`StreamingRender` 维护 `stable_source_len` / `stable_rendered_len` 边界；已完成的顶层 markdown 块只渲染一次并缓存，每帧仅重渲染最后一个未完成块。
- 出现引用式链接定义或 inline visualization 指令时回退全量渲染（后续内容可能影响已渲染块）。
- `history_cell/markdown_render_cache.rs`：单条渲染缓存，键 = width + theme_revision + fg/bg + color_level。

推断：
- 流式 markdown 不卡的核心理由：整页重渲染被摊到「未完成块」上，已完成块零成本。

### 1.3 滚动：不做虚拟滚动，直接写终端 scrollback
事实：
- `insert_history.rs`：完成的 history 行通过 escape 序列直接写入终端原生 scrollback，不占用 Ratatui buffer，TUI 只负责可视区。
- 换行策略 `HistoryLineWrapPolicy::{PreWrap, Terminal}`；Zellij 特判 `ZellijRaw`（Zellij 不把软换行限制在 scroll region 内）。
- PR #1685 原话："skip ratatui for writing into scrollback, because its primitives are wrong"。

推断：
- 长会话流畅的关键是「把长历史交给终端」：终端 scrollback 天然支持海量行滚动，TUI 不需要虚拟滚动组件。

### 1.4 事件与绘制循环
事实：
- `tui.rs`：事件 `TuiEvent::{Key, Paste, Resize, Draw, Resume}`；动画靠定时 `Draw` 事件驱动，与按键事件解耦。

## 2. 流畅度手段

事实：
- `tui/frame_requester.rs`：`FrameRequester` 采用 actor 模式（设计参考 https://ryhl.io/blog/actors-with-tokio/），mpsc 合并帧请求 + broadcast 通知，提供 `schedule_frame()` / `schedule_frame_in()`。
- `tui/frame_rate_limiter.rs`：上限 120 FPS（`MIN_FRAME_INTERVAL ≈ 8.33ms`），多个 draw 请求合并到最早 deadline，避免渲染线程空转。
- `streaming/chunking.rs`：`AdaptiveChunkingPolicy` 双模式：`Smooth`（每个 commit tick 排 1 行，稳定节奏）↔ `CatchUp`（积压时排空队列）。
- 模式切换带磁滞：进入 CatchUp 用高阈值（队列深度 / 最老行年龄），退出用低阈值 + `EXIT_HOLD` 保持窗口，`REENTER_CATCH_UP_HOLD` 防刚退出又进入的抖动；严重积压绕过 hold。
- `streaming/commit_tick.rs`：`run_commit_tick → stream_queue_snapshot → apply_commit_tick_plan`，产出 `DrainPlan::{Single, Batch}`。
- `live_wrap.rs`：`LiveWrap::RowBuilder` 增量换行，流式行逐行 wrap 不整块重排；`wrapping.rs` 做 URL 感知断行（长 URL 整体不断行）。
- 快照测试：insta 快照大量覆盖 UI（`src/snapshots/`、`streaming/snapshots/`、`render/snapshots/`）；`test_backend.rs` 的 `VT100Backend` 包装 `vt100::Parser` 模拟真实终端；PR #9359 处理 WSL 下快照不确定性。

推断：
- 流畅度是「多级削峰」：渲染限帧（120FPS 上限）+ 行节奏节流（Smooth/CatchUp 磁滞）+ 缓存（markdown cache、增量 wrap）。
- 快照 + VT100 后端把「渲染正确性」做成回归测试，不靠人眼验收。

## 3. 动画

事实：
- `frames.rs`：10 个 ASCII art 变体 × 36 帧，`include_str!` 嵌入；`FRAME_TICK_DEFAULT = 80ms`。
- `ascii_animation.rs`：时间驱动取帧（`elapsed / tick % len`）；`schedule_next_frame()` 精确计算到下一帧的延迟并 `schedule_frame_in()`；`pick_random_variant()` 随机换变体。
- `shimmer.rs`：2 秒周期 cos 扫掠高亮带（半宽 5 字符），颜色从默认 fg/bg 混出 + BOLD；无 TrueColor 时按 DIM → default → BOLD 阶梯降级。
- `motion.rs`：集中式 `MotionMode::{Animated, Reduced}`；reduced-motion 下 spinner 隐藏或显示静态 `•`（dim）；另有 600ms 闪烁 `•`/`◦` 兜底。
- `status_indicator_widget.rs`：composer 上方固定一行「Working + spinner + 耗时 + Esc 中断提示」，布局稳定不跳动。
- 有一个测试用正则扫描全部源码，禁止绕过 `crate::motion` 直接调用 `spinner()` / `shimmer_spans()`，保证所有动画走 reduced-motion 门控。

推断：
- 动画全部是「自绘 ASCII + 时间驱动重绘」，无第三方动画库；帧率与输入事件解耦。
- reduced-motion 是架构内置而非事后补丁（motion 模块 + 测试强制）。

## 4. 语义色与美术

事实（`styles.md` + `clippy.toml`）：
- 规范：标题 = bold（markdown 保留 `#` 符号）；primary = 终端默认色；secondary = dim。
- 语义前景色：用户输入提示 / 选中 / 状态 = ANSI cyan；成功 / 新增 = green；错误 / 失败 / 删除 = red；Codex 身份 = magenta。
- 避免：自定义 RGB / Indexed 颜色（不保证任何终端主题下对比度）；black / white（默认主题色更好，需要时用 reset）；blue / yellow（规范未定义）。
- `clippy.toml` 的 `disallowed-methods` 硬禁 `Color::Rgb`、`Color::Indexed`、`Stylize::white/black/yellow`，纪律由构建工具强制。
- `terminal_palette.rs`：`stdout_color_level()` 探测 TrueColor / Ansi256 / Ansi16 / Unknown；`best_color()` 用 CIELAB `perceptual_distance` 把颜色量化到最近的 indexed 色；WindowsTerminal（`WT_SESSION`）修正；16 色环境退回默认色。
- `style.rs`：accent = cyan + bold（暗/未知背景），亮背景改用更暗青色 RGB(0,95,135)（带测试）；user 消息背景 = 终端 bg 与白/黑按 4–12% 混合；表格分隔线 fg/bg 混合 20%。
- 唯一 RGB 例外：`shimmer.rs`（基于默认 fg/bg 调级，`styles.md` 明确认可）。

推断：
- 目标不是「像素级品牌设计」，而是「任何终端主题下可读 + 语义可区分」：AI 产物用默认色，系统 UI 用语义 ANSI 色。
- 「禁止 RGB」与「少数经过论证的例外」并存：例外被显式化、测试化，而不是一刀切。

## 5. 操作设计

事实：
- `keymap.rs`：context 分组 global / chat / composer / editor / vim_normal / vim_operator / vim_text_object / pager；解析优先级 context → global 回退 → defaults；冲突校验器保证 app 与 composer 键位不冲突。
- 配置：`config.toml` 的 `[tui.keymap]`；`/keymap` 命令可视化 inspect / edit（PR #18593、#18595 加入 vim mode）。
- `key_hint.rs`：处理 C0 控制字符与跨终端键差异（如 Enter / Ctrl+M 归一）。
- `?`：footer 内置 `plain('?')` 打开 shortcut overlay。
- Esc：composer 为空时 `edit_previous`（编辑上一条消息）；`FooterMode::EscHint` 显示「再按一次」瞬态提示；退出需再按 Ctrl+C 确认提醒。
- 状态行：`/statusline` 可配置（model、context%、token、cwd、branch、limits 等）并持久化到 config；footer 纯渲染（FooterProps → Line），空间不足时按优先级丢 hint（collapse 策略）。

推断：
- 操作设计关键词：可发现（`?` + footer hints）、可配置（keymap / statusline 均持久化）、可撤销（Esc 进入编辑上一条，退出需二次确认）。

## 6. 可借鉴点（具体技术名）

1. **FrameRequester + FrameRateLimiter**（事实）：actor 式帧请求合并 + 120FPS 上限。→ Bubble Tea v2 用 `tea.Tick` 做统一帧驱动，把多个渲染请求合并到最早 deadline。
2. **StreamingRender 稳定前缀**（事实）：已完成 markdown 块只渲染一次，仅重渲染尾部；遇 reference link 定义等再全量回退。→ Reasonix 渲染器可缓存已完成块、只重渲染尾部。
3. **Scrollback 外置（insert_history_lines）**（事实）：完成的历史行直接写终端 scrollback。→ Bubble Tea 同样可用 ANSI 序列实现，比 viewport 虚拟滚动更适合长会话。
4. **语义 ANSI 色 + clippy 硬禁**（事实）：cyan/green/red/magenta 语义化；禁 `Rgb` / `Indexed` / black/white；亮背景用 RGB(0,95,135) 例外并带测试。→ Reasonix 已有 `charmbracelet/colorprofile`，可做能力探测 + 类似 lint 规则。
5. **AdaptiveChunkingPolicy（Smooth/CatchUp + 磁滞）**（事实）：每 tick 1 行 vs 排空队列，进出阈值不同 + hold 防抖。→ 对应 Bubble Tea 的流式行排队器。
6. **VT100Backend + insta 快照**（事实→推断）：终端仿真 + 快照回归。→ Go 侧用 vt100 类模拟 screen + golden 文件。
7. **motion.rs 集中门控 + 测试正则**（事实）：所有动画必须走 reduced-motion 门控。→ 可移植为「动画工具模块 + lint/测试强制」。
8. **LiveWrap::RowBuilder**（事实→推断）：增量换行 + URL 感知断行。→ Go 侧用 go-runewidth + 自定义 wrap。
9. **SynchronizedUpdate**（事实）：整帧输出包同步更新防撕裂。→ Bubble Tea v2 可在写入 view 时启用同步输出。
10. **可配置 statusline + footer collapse**（事实）：/statusline 持久化；空间不足按优先级丢 hint。

## 7. 不可照搬

1. **Ratatui buffer diff（`[Buffer;2]` + `diff_buffers()`）**（事实）：Ratatui 专属；Bubble Tea 的 view 是纯文本模型，无等价 diff 层。思想可借鉴，实现不可照搬。
2. **FrameRequester 的 tokio actor**（事实）：Rust async 实现；Go 用 channel + `tea.Cmd` 即可，无需 actor 框架。
3. **insta + vt100::Parser**（事实）：Rust 生态工具；Go 用 `cmp.Diff` + golden 文件，语义相同、工具不同。
4. **crossterm API（SynchronizedUpdate 等）**（事实）：crossterm 专属；Bubble Tea 有自己的输出层（termenv/lipgloss），只迁移概念、不迁移 API。
5. **静态 keymap 结构体 + 宏**（推断）：Rust 类型系统实现；Bubble Tea 用 map + 字符串 action 即可。
6. **120 FPS 常量**（推断）：Ratatui 场景参数；Bubble Tea 具体值按平台与能耗再调。

## 8. 证据链接

源码（main 分支，raw 前缀 `https://raw.githubusercontent.com/openai/codex/main/`）：
- `codex-rs/tui/src/custom_terminal.rs`（diff_buffers）、`codex-rs/tui/src/tui.rs`、`codex-rs/tui/src/tui/frame_requester.rs`、`codex-rs/tui/src/tui/frame_rate_limiter.rs`
- `codex-rs/tui/src/streaming/render.rs`、`codex-rs/tui/src/streaming/chunking.rs`、`codex-rs/tui/src/streaming/commit_tick.rs`
- `codex-rs/tui/src/insert_history.rs`、`codex-rs/tui/src/history_cell/markdown_render_cache.rs`、`codex-rs/tui/src/live_wrap.rs`、`codex-rs/tui/src/wrapping.rs`
- `codex-rs/tui/src/frames.rs`、`codex-rs/tui/src/ascii_animation.rs`、`codex-rs/tui/src/shimmer.rs`、`codex-rs/tui/src/motion.rs`、`codex-rs/tui/src/status_indicator_widget.rs`
- `codex-rs/tui/styles.md`、`codex-rs/clippy.toml`、`codex-rs/tui/src/terminal_palette.rs`、`codex-rs/tui/src/style.rs`
- `codex-rs/tui/src/keymap.rs`、`codex-rs/tui/src/key_hint.rs`、`codex-rs/tui/src/bottom_pane/footer.rs`、`codex-rs/tui/src/test_backend.rs`

PR：#1685（scrollback 外置）、#9359（WSL 快照修复）、#18593（/keymap inspect/edit）、#18595（keymap vim mode）。其他相关：#1810、#10546、#11447（内容未逐条核对）。

文档与参考：
- 交互模式说明：https://mintlify.wiki/openai/codex/concepts/interactive-mode
- FrameRequester 设计参考：https://ryhl.io/blog/actors-with-tokio/
