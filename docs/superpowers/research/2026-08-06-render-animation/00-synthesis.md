# 调研综合：渲染动画 · 流畅度 · 美术（2026-08-06）

> 配套单点报告：`claude-code.md` / `codex.md` / `qwen-grok.md` / `charm-capabilities.md`。
> 本文件是决策用综合版：基线事实（带证据）→ 外部锚点 → 对 Corvus 的含义。

## 1. 内部基线（实测，2026-08-06）

### 1.1 渲染路径
- Transcript 存为「预渲染 ANSI 行」`[]string` + 语义源 `transcriptSource`（用于宽度变化时重排）。
- `Update` 每次内容增长/宽度变化 → `wrapTranscript(join(all), width)` = `lipgloss.Width(width).Render(whole)` → `viewport.SetContent`。
- `View()` 每帧组装：viewport + 底部面板 + composer + 2 行 status。
- 每次滚动（PgUp/PgDn/滚轮/尾随）且非 nativeScrollback → **`tea.ClearScreen()` 全清重绘**（chat_tui.go:859，Warp 兼容 workaround）。
- `bottomRows()` 与 `View()` 每帧**各调用一遍全部 render\* 面板**（双份渲染成本）。

### 1.2 micro-benchmark（新加 `internal/cli/bench_test.go`）
`BenchmarkWrapTranscript`（i7-14650HX，含 ANSI+CJK）：

| 行数 | width=80 | allocs |
|---|---|---|
| 500 | 2.8ms | 1.06MB / 42.7k |
| 2,000 | 11.1ms | 4.3MB / 171k |
| 5,000 | 28.0ms | 10.0MB / 427k |
| 10,000 | 57.6ms | 20.3MB / 855k |

→ 长会话下**每个 token 都付一次全量重 wrap 成本**，是流式卡顿的主因。60fps 预算 16.6ms，5000 行即超标。

### 1.3 已有动画
- 主 working 行：bubbles spinner（Dot，10fps）+ 每秒 elapsed ticker。
- 工具卡：braille 帧循环（1 帧/秒）。
- 自动滚动 tick 80ms。
- 无平滑滚动、无过渡、无 reduced-motion 门控。

### 1.4 栈内能力（bubbletea v2.0.7 源码核实）
- 渲染器只有 `cursedRenderer`（ultraviolet cell buffer）：**单元格级 diff**、`TouchedLines()==0` 整帧早退、只写脏行。
- **DEC 2026 synchronized output 默认协商启用**（启动 `\x1b[?2026$p` 探测，支持则 BSU/ESU 原子刷新，否则 hide/show cursor 回退）。
- 渲染循环独立于事件循环：默认 60fps、上限 120，`WithFPS` 可调；相同 View 整帧跳过。
- 声明式 `tea.View` 字段：`AltScreen` / `MouseMode` / `KeyboardEnhancements`（KVP）等；无 v1 的 `WithAltScreen`/`tea.Sync`。
- 动画原语：`tea.Tick`/`Every`/`Batch`/`Sequence`（一次性，需 Update 循环返回）；spinner 有过期帧防护。
- `bubbles/viewport` **无平滑滚动**（需自研 Tick 插值 + `SetYOffset`）；流式 = `SetContent`+`GotoBottom`。
- lipgloss v2 **无渐变、无渲染缓存**；`Blend1D/Blend2D` 可预生成色表；宽度走 x/ansi（runewidth/uniseg）。
- KVP、bracketed paste、OSC 52、`BackgroundColorMsg`、colorprofile 探测均现成。
- 主题已有 16 个语义色槽（accent/muted/faint/subtle/success/warn/err/danger/info/secondary/border/selection/userBubbleBG/diff*/toolRead/toolProc）+ 暗/亮两套 + 256 色回退。

## 2. 外部锚点（代理调研，细节见各单点报告）

### 2.1 Claude Code
- 无闪烁 = DEC 2026 原子刷新 + **sync block 内绝不发 `CSI 2J`**（issue #35580 教训：会滚动跳顶）。
- 长对话平稳 = 只渲染可见消息的**虚拟化窗口**（flat memory），而非全量重绘。
- 动画克制：spinner 动词+计时+shimmer（`prefersReducedMotion` 可关）、假光标、auto-follow；**流畅感来自 <16ms 低延迟重绘，不是动画**。
- 交互：Esc Esc 状态机、双键+时间窗防误触、`?` 空输入帮助、Ctrl+T 任务（≤5）、Ctrl+O transcript。

### 2.2 Codex CLI
- 全量重画 + `diff_buffers()` 最小化输出；**`StreamingRender` 稳定前缀**——已完成块只渲染一次，只重渲未完成尾部。
- 滚动不虚拟化，直接写终端原生 scrollback（PR #1685）。
- `FrameRequester`（合并帧请求）+ `FrameRateLimiter`（120fps）+ `AdaptiveChunkingPolicy`（Smooth/CatchUp 磁滞节流）。
- 动画全自绘 ASCII、时间驱动；`motion.rs` 集中门控 reduced-motion，**测试正则禁止绕过**。
- 语义色 + lint 硬禁 `Color::Rgb/Indexed`；CIELAB 量化到 indexed 色尊重终端主题。
- 回归靠 insta 快照 + VT100 终端仿真。

### 2.3 Qwen Code
- 思考痕迹默认折叠为**固定 1 行** `∴ Thinking… Xs`，Ctrl+O 就地展开（#4595/#4598/#8077）。
- 间距用 marginTop 集中收紧；工具边框移除（#5003）；并行 agent 密集行 + 「打字即回输入框」焦点逃逸（#4477）。
- 回合耗时 `⏱ X.Xs` **需固定宽度防抖**（#6533）；表格代码单元格单一中性色。
- 首屏宽度门控短 logo（#3710）。
- 共识：**终端不做平滑渐变/骨架屏/面板过渡**（Qwen 注释明言渲染不了）；低频 tick + 字形宽度稳定。

### 2.4 Grok Build
- 会话按状态分组（无内联组头）、peek 面板直回、数字键 1-9 授权、Esc 阶梯回退、Ctrl+[ / Ctrl+] 循环会话；行 = 状态点+名字·活动+耗时。
- 动效：fps=30 + 波纹 accent（有限）；Dashboard 全屏多会话形态**不应照搬**到单会话线性 shell。

## 3. 对 Corvus 的含义（按影响排序）

| # | 发现 | 含义 |
|---|------|------|
| 1 | 滚动触发 `ClearScreen` | 与 v2 cell-diff 渲染器冲突；Claude 明确「sync block 内不发 CSI 2J」→ **首要验证/移除项**（多终端验证，保留兜底开关） |
| 2 | 每 token 全量重 wrap（58ms @10k 行） | **改为增量**：只 wrap 新块并缓存；transcriptSource 已给语义源，可做 per-block 缓存 |
| 3 | `bottomRows()`/`View()` 双份渲染面板 | 缓存面板高度或渲染结果 |
| 4 | 无 reduced-motion 门控 | 加集中门控 + 测试（Codex 模式） |
| 5 | 无平滑滚动 | viewport 自研 Tick 插值（30–60fps），Qwen 反对平滑渐变但滚动插值可行 |
| 6 | 无首屏 branding / 思考头未固定 1 行 / 回合耗时未固定宽度 | 美术清单：首屏 logo（宽度门控）、`∴ Thinking… Xs` 固定 1 行、`⏱ X.Xs` 固定宽度 |
| 7 | 表格代码单元格 | 单一中性色（查 md.go 现状，可能已处理） |
| 8 | 语义色无纪律约束 | 加测试：禁止硬编码 RGB（Codex lint 模式） |
| 9 | 流式节奏 | 60fps 预算内，Update 内只做增量工作即可；暂不需要 Codex 级 chunking 策略 |
| 10 | 长会话终极方案 | 原生 scrollback 外置/虚拟化窗口（Claude flat memory / Codex PR #1685）→ **后续阶段**，本轮不做 |

## 4. Spike 计划（用户已批准，3 个）

1. **Spike-流畅**：移除/收窄滚动 ClearScreen（多终端验证 + 兜底开关）；增量 wrap（per-block 缓存 + 只 wrap 新块）；面板双渲染消除；benchmark 固化。
2. **Spike-动画**：reduced-motion 门控 + shimmer/working 行细化 + 平滑滚动插值 + 固定宽度 `⏱ X.Xs` 与 1 行思考头（含防抖测试）。
3. **Spike-美术**：首屏 branding（wide/narrow）、密度审计（marginTop/空行）、表格代码单元格中性色、语义色纪律测试。

> 验收基线：`go test ./internal/cli/ -count=1` 全绿（当前 2.9s 通过）；benchmark 作为回归护栏。
