# Qwen Code × Grok Build：UI 议题与视觉/动效调研

> 调研日期：2026-08-06。对象：QwenLM/qwen-code（Ink 渲染的 TUI）、xAI Grok Build（Rust TUI，xai-org/grok-build）。
> 事实 = 官方 issue/PR/文档/源码可见内容；推断 = 基于上述材料的分析，已显式标注。
> Corvus 定位：Go + Bubble Tea v2 的单会话、纯键盘线性聊天 TUI（README.zh-CN.md、PROJECT_DEEP_DIVE.zh-CN.md）。

## 1. Qwen Code Issue #4588（Epic）逐条

- 元信息：chiga0 于 2026-05-28 创建，已关闭；PR #5003 标注 "Closes #4588"。
- 动机（事实）：与 Claude Code / Gemini CLI / Codex CLI / Opencode 同环境对比，发现五类问题：正常回答暴露推理/进度痕迹；回合垂直空间过大；工具输出边框过重；SubAgent 与首屏品牌感弱；默认主题灰暗。
- 方法论（事实）：拆成多个"单轨道 PR"评审（官方设计文档 tui-spacing-density-pr1 说明"只处理间距密度，便于纯粹对比行数"）。

### Track 1：默认隐藏思考/进度痕迹
- 目标（事实）：默认只显示用户面答案；verbose/debug 或显式 toggle 才显示；隐藏后不得引起布局跳动、重复文本或空白缺口。
- 落地（事实）：大 PR #4422（compact-first、Ctrl+O transcript、subagent rework）**未合并**，改为分拆：
  - #4598（2026-06-16 合并）：流式时固定 4 行尾窗 + 实时计时 `∴ Thinking… 8s`；完成后折叠为一行 `∴ Thought for 15s`；Ctrl+O 展开完整推理（dim 渲染）。删除旧 thinkingDisplayMode 设置。
  - #5627（合并）：Alt+T 思维块查看器；#6678（合并）：流式展开时显示完整推理而非 4 行尾窗。
  - #8077（2026-08-01 合并，最终形态）：流式推理默认完全不显示正文，只保留固定 1 行头 `∴ Thinking… Xs`；Ctrl+O 改为就地展开全部 thinking/tool 块（对齐 Claude Code），取代全屏 transcript 覆盖层。
  - #6793（合并）：亚秒推理显示 "Thought briefly"，不显示误导性数字。
- 动机补充（事实）：issue 评论（zzzhenyao）反馈日常使用中布局跳动与过度重渲染，稳定性是主要诉求。
- 推断：根因是推理块可变高度（1-5 行随流式内容跳动）+ 未折叠；最终方案"固定 1 行头 + 显式展开"比"隐藏并预留空白"更稳，也解释了为何从 4 行尾窗再退到 1 行头。

### Track 2：紧凑间距与密度
- 目标（事实）：回合间 1 空行替代 2 空行；块间距收紧；输入区上方保留一点空间；每回合耗时指示 `X.Xs`。
- 落地（事实）：#4595（2026-06-12 合并）集中 `getHistoryItemMarginTop()`——多数消息类型默认 0，仅模型输出返回 1（thinking→answer 空隙）；ToolGroupMessage `gap 1→0`；ToolMessage 结果容器 `marginTop 1→0`；thinking 文本 `trimEnd()` 防尾随换行造成双空行。
- #4595 附带用户消息半行色带（事实）：真彩色终端上三行 `▄ / > msg / ▀`；色带颜色 = 终端背景向白/黑做 6% 亮度偏移（`subtleBandColor`），无色调变化；非真彩/读屏/零宽全部降级为纯 `> text`。
- 回合级 `⏱ X.Xs` 指示（事实）：仅存在于未合并的 #4422；2026-08-06 的 main 源码中未找到。已合并的近似物：#4598 思考时长、#3155 工具耗时（执行 3s 后右对齐显示）、#6533 状态行耗时固定小数位宽度（0.5s/1.0s…，修 `Xs↔X.Xs` 每秒两次的两列抖动）、#5287 亚分钟舍入。
- 推断：回合耗时指示被"思考时长 + 工具耗时"组合替代，未按原样落地；Corvus 若做 `X.Xs` 必须固定宽度（#6533 教训）。

### Track 3：去掉重型工具边框
- 目标（事实）：边框可被缩进、简洁前缀、图标、标题替代；工具名/状态/元数据仍可见；长工具输出应融入对话而非大面板。
- 落地（事实）：#5003（2026-06-22 合并）移除 ToolGroupMessage / CompactToolGroupDisplay / InlineParallelAgentsDisplay 的圆角边框；紧凑模式下已完成工具结果折叠为单行头（状态图标 + 工具名 + 耗时），进行中/错误/待确认仍展开；非紧凑模式行为不变。
- #3909（2026-05-06 合并，事实）：无边框常驻 LiveAgentPanel 泊在输入框下方，每 agent 一行，耗时 + tokens 固定在右列；视觉约定（`○` 子弹、`▶` 分隔、`name: desc (activity)`）逐字移植自 Claude Code CoordinatorTaskPanel。
- 推断：#5003 承认取舍——紧凑模式隐藏已完成输出，回顾需滚动或切非紧凑模式；单工具展开/折叠是标注的未来增强。Qwen 的"去边框"是在保留状态/名称/耗时元数据前提下的视觉减重。

### Track 4：表格内代码单元格
- 目标（事实）：表格代码单元格不做语法高亮；等宽 + 浅底色/中性处理；保持对齐紧凑；不增加行间空隙。
- 落地（事实）：#4422 的 `inTable` prop 未合并；但当前 main 的 TableRenderer 以纯 ANSI 字符串构建表格（源码注释：像 Claude Code 一样），单元格内行内代码 = 单一中性色 `theme.text.code`（LightBlue），**无 hljs 语法高亮**；单元格链接用 OSC8 包裹，窄单元格长 URL 只显示 label。
- 表格流式稳定性（事实）：格式以表头 + 首行数据锚定，流式过程不翻转横/竖布局；`maxHeight` 截断 + 提示。
- 推断："浅底色"背景未在 main 落地（`theme.text.code` 仅前景色）；实际效果是"等宽 + 单一中性色"，对齐由 ANSI 宽度计算保证。web-shell 另有表格增强（#6626/#6500 等），与 TUI 无关。

### Track 5：SubAgent 紧凑摘要
- 目标（事实）：品牌化标题行；任务 → 结果的信息层级；pass/fail/进度计数易扫读；避免大块通用容器。
- 落地（事实）：#4422 的"两行式 terminal summary + `N Agents Completed` / `(X running, Y completed)` 头（参考 gemini-cli SubAgentGroupDisplay）"未合并；当前 main 由 #4477 的 InlineParallelAgentsDisplay 承担（见第 2 节）：标题 `Parallel agents · N · X/N done`（done = completed+failed），每行状态字形（✔/✖/○）· agent 名 · 最近活动 · 耗时 · tokens；运行中 agent 由 #3909 LiveAgentPanel 负责。
- 推断：Track 5 的"任务→结果"在 main 上实现为"并行密集面板 + 结果字形计数"；单 agent 的两行摘要形态未落地。

### Track 6：首屏 branding
- 目标（事实）：wide 终端重排 banner；narrow 终端紧凑变体；品牌视觉强于小号导航/帮助文字。
- 落地（事实）：#3710（2026-05-07 合并）新增 `ui.hideBanner` / `ui.customBannerTitle` / `ui.customAsciiArt`（支持 `{small, large}` 两档宽度感知）；`pickAsciiArtTier` 选最宽可容纳档；banner 净化器剥 OSC/CSI/C0/C1 控制序列、200 行×200 列与 80 字符标题上限、所有错误路径软失败回退内置 QWEN logo。
- Header.tsx（事实）：可用宽度不足时隐藏 ASCII logo，短 logo（shortAsciiLogo）为窄终端默认；#4741（合并）：banner 与状态行显示模型名而非模型 id。
- 设计文档 customize-banner-area（事实）：操作行（版本、认证、模型、工作目录）锁定不可隐藏，属支持/安全信号。
- 推断："wide/narrow 两版"落地为"宽度门控 + 短 logo 兜底 + 可配置两档 ASCII art"，而非两套完整布局。

### Track 7：默认主题颜色刷新
- 目标（事实）：默认紫低饱和/偏灰 → 更现代的高对比紫/蓝系；保持可访问对比；启动/聊天/工具/SubAgent/状态全表面一致；不单靠颜色传达层级或状态。
- 落地（事实）：main 源码中 `DEFAULT_THEME = QwenDark`；新增 Qwen Dark / Qwen Light 主题，`theme=auto` 按终端深浅解析二者；QwenDark 调色板如 Background `#0b0e14`、AccentPurple `#D2A6FF`、AccentBlue `#39BAE6`、AccentYellow `#FFD700`。
- 状态不只靠颜色（事实）：✔/✖/○ 等字形体系与颜色并存。
- 推断：主题刷新以"新增专属主题 + auto 解析"方式落地，旧 DefaultDark 仍保留可选。

## 2. Qwen PR #4477：密集内联面板 + 并行 agent 键盘导航（已合并）

- 背景（事实）：`/review` 等命令并行 9 个 agent、各跑数分钟；旧 compact 模式折叠成一行 `Agent × 9 / <last name>`（几乎零信息），非 compact 模式每个 agent 整块 ToolMessage（密度极低）；且输入框无键盘路径到运行中 agent（只能靠隐藏的 footer pill 打开后台任务对话框）。
- InlineParallelAgentsDisplay（事实）：触发条件 = 同一工具组 ≥2 个纯并行 agent 调用、组内无其他工具、无 agent 等待工具批准。每行：状态字形 · agent 名 · 活动 · 耗时 · tokens。
  - 两阶段渲染：live 阶段只把已结束（完成/失败/取消）的 agent 渲染进面板，运行中的归 LiveAgentPanel（短暂重叠后随完成而消除）；`availableTerminalHeight` 窗口化兜底，防止非 Static live 帧超高触发 ink 整屏清屏/回跳闪烁。committed 阶段全量渲染进 `<Static>` 成为永久滚动记录；`totalAgentCount` 保证子集时标题计数正确。1s tick 从 registry 读最近活动。
- LiveAgentPanel 键盘导航（事实）：输入框 ↓ → 聚焦面板并选中 `main`；↓/↑ 在 `main` 与 agent 行间移动（`▸` 指示）；Enter 直接打开 BackgroundTasksDialog 的 detail 模式；← 从详情返回面板选中态（不经过列表）；Esc / ↑ 到顶 → 回输入框；可打印字符自动取消焦点、字符进入输入框。适用于所有后台 agent，不限于并行组。
- 其他（事实）：`DEFAULT_MAX_ROWS` 5→12；头部 `Active agents (N/N)` → `Active agents (N)`；新增 `detail-from-panel` 对话框模式。
- 推断：#4477 是 Track 5 的并行侧实现；"打字即回输入框"的焦点逃逸是纯键盘线性 shell 的关键交互模式。

## 3. Grok Build Agent Dashboard 交互

- 入口（事实）：`grok dashboard` / 会话内 `/dashboard`（别名 `/agents-dashboard`、`/sessions`）/ `Ctrl+\`；minimal 模式隐藏；`GROK_AGENT_DASHBOARD=0` 或 `[dashboard].enabled=false` 禁用。来源：docs.x.ai/build/features/dashboard（2026-07-04 更新）与 grok-build 仓库 user-guide/23-dashboard.md（2026-07-13 更新）。
- 形态（事实）："Dashboard 是视图不是会话管理器"——关闭不结束会话，重开仍在原状态。
- 分组与排序（事实）：按状态 Needs input → Working → Idle → Inactive → Completed → Failed 排序，Ctrl+G 切换按工作目录分组；无内联组头，靠排序相邻 + 每行圆点/颜色表达分组；Inactive 默认折叠（`→`/点击展开）；Idle 只显示最新 8 个 + 1 小时内活跃的，其余收进 "N more" 行（Enter/`→` 展开、`←` 折叠），筛选/搜索时暂停折叠。行在后台任务/monitor/loop 仍运行时保持 Working。
- Row 内容（事实）：官方示例行 `● reviewer · audit token flow   Awaiting your input  2m`（状态点 + 名字 · 活动 + 耗时）；状态图标：`⋅`/`:`/`⸬`/`⁙` 动画 spinner（Working）、`●` 实心（Needs input/Completed/Failed/Blocked，黄/绿/红/琥珀）、`○` 空心（Idle/Inactive）；model 与 always-approve 徽标位于 peek 面板底边框，行内不重复。
- 第三方报道（事实）：中文媒体（cocoloop 2026-06-15）描述行为"名字 + 当前分支 + 权限模式 + 状态时间"——与官方示例行不完全一致；推断为版本差异或媒体演绎，行内是否含 branch 以官方为准存疑。
- Peek 面板（事实）：选中行 → 底部 dispatch 框原位替换为 peek：头部 = 最近响应类型（Thinking/Thought/Response/Edit/Read/Bash…）+ 时间；正文 = 最近响应（换行约 3 行，超出 `…`）；底部 `❯ reply` 实时输入。空闲 agent 立即收到回复，忙碌 agent 排队。回复为空时 ↑/↓ 切换选中 agent（半输入草稿被清空，防发错目标）；Esc 先清回复再取消选中；Shift+Tab 循环 peeked agent 模式（Normal→Plan→Always-approve，live 生效；dispatch 框上仅预置给下一个 agent）；Tab 在回复与列表间切焦点；`@` 文件选择器以 peeked agent 工作目录为根。
- 数字键内联授权（事实）：挂起 permission/ask 时 `❯ reply` 隐藏、选项列表代之：↑/↓ 高亮 + Enter 回答，`1`–`9` 直接回答；No/reject 与 ask 的 Other 行接受自由文本；多问题 Ask 表单逐个进行（i/N）。
- 详情视图与切换（事实）：空回复 + Enter = 全屏详情（全宽对话、无边框 modal；`[‹]`/`[›]` 循环 agent；Esc / `Ctrl+\` / `[Dashboard]` 返回）。官方 docs 表述："Ctrl+[ / Ctrl+] cycle between sessions"（对应 `[‹]`/`[›]`）。Ctrl+X：运行中 = 取消回合；两次 2s 内 = 永久删除会话；详情视图内 Ctrl+X 状态依赖。无"标记完成"命令——状态由 agent 派生。
- 底部输入框分发（事实）：底部 textarea 永远派发新会话（选中行只是导航光标，不是回复目标）；空输入 = 打开选中行 / 新建；Ctrl+S = 派发并 attach；Enter = 留在 dashboard 连续派发；Esc 从不丢草稿（Ctrl+U/Ctrl+C 才清）；Tab 切换输入↔列表焦点；无 agent 时焦点默认在输入框；Ctrl+/ 搜索模式（`a:` 名称、`s:` 状态、`#` 子串、其余按 label+工作目录子串实时过滤）。
- Esc 阶梯回退（事实）：取消搜索 → 关 peek → 清过滤 → 取消聚焦 → 取消选中 → 退出，任何层都不丢草稿。
- 持久化（事实）：`[dashboard]` 的 grouping/pinned/reorder 用 session id（非进程槽位）存 `~/.grok/config.toml`，重启保留。

## 4. 动效粒度

### Grok
- 事实：`[animation] fps = 30`（1-60，越高越顺滑越耗 CPU）+ `wave_rows = 32`（accent 波纹的行周期）；thinking 块与 execute 块的 accent 线在运行中动画（`accent_enabled` + `animate`）；Working 状态 4 字形 spinner 序列；`/theme` 选择器实时预览（Esc 回退）；终端 tab 进度条（OSC 9;4，`progress_bar=true`）；标题栏可配 spinner / turn-timer / activity / action-required。
- 事实：文档与代码中未发现 skeleton/shimmer 或面板过渡动画——TUI 为即时重绘 + Esc 阶梯式步退，无平滑过渡。

### Qwen
- 事实：ink-spinner dots；tmux 下降级为 750ms 两帧 `. ` / `..`（防 tmux 状态栏闪烁）；LoadingIndicator = spinner + 短语 + 计时 + token 计数（可选 tokens/s）；`useAnimationFrame` 平滑计数（规则同 Claude Code SpinnerAnimationRow：gap<70 每帧 +3，70-200 +20%，>200 +50，50ms 轮询，下降立即 snap）；`useAnimatedScrollbar` 拇指强调 1.5s（源码注释明确"终端渲染不了平滑颜色渐变"，刻意不做逐帧 RGB 插值）；OSC 9;4 tab 进度（iTerm2/Ghostty/ConEmu/Windows Terminal 白名单）；LiveAgentPanel 1s tick；InsightProgressMessage `█░` 30 列进度条；thinking 固定 1 行头 + 时长；web-shell 有 skeleton.tsx（shimmer，仅 web 端）。
- 推断：两家共识——TUI 动效 = 低频 tick + 字形/宽度稳定，不做平滑渐变与过渡；动画服务"状态可读"而非"美观"。Qwen 防布局跳动的核心手段是固定高度/1 行占位 + 截断窗口（#8077、#4477），这比动画本身更值得学。

## 5. 适合 Corvus 的可借鉴点（按价值排序）

1. 思考痕迹默认折叠为 1 行 `∴ Thinking… Xs` → 完成后 `Thought for Xs`，一键就地展开全部（Ctrl+O 式）；固定 1 行头从根上防回流（#8077 最终形态）。
2. 并行/子 agent 密集行面板：#4477 每行"状态字形·名·活动·耗时·tokens" + `X/N done` 计数 + 从输入框 ↓ 进入导航、可打印字符自动回输入框的焦点逃逸（Corvus 的 Coordinator 双模型场景直接适用）。
3. 紧凑间距体系：#4595 marginTop 集中管理（默认 0）、回合间 1 空行、用户消息半行色带（真彩色 6% 亮度偏移，非真彩/读屏降级）；#5003 去边框 + 紧凑模式折叠已完成工具为单行头。
4. 表格内代码单元格单一中性色 + 纯 ANSI 构建保证对齐 + 格式锚定首行防流式翻转 + OSC8 链接（TableRenderer）；耗时显示学 #3155（工具 3s 后显示）+ #6533（固定宽度防抖动）。
5. 数字键 1-9 内联授权（Grok）：仅当选项列表可见时激活，避免与输入冲突；配合 ↑/↓ + Enter。
6. 动效克制：计时、Grok fps=30 与可选波纹 accent、OSC 9;4 tab 进度、count-up 防跳变、滚动条拇指强调 1.5s——低成本高感知；明确不做逐帧颜色渐变。
7. branding：宽度门控短 logo + 可配置两档 ASCII art + 操作行（版本/模型/目录）锁定；状态用字形 + 颜色双通道（#3710、#4741、Track 7）。
8. Esc 阶梯回退（Grok）：搜索→面板→过滤→聚焦→选中逐层步退且不丢草稿，可作统一退避模型。

## 6. 不该照搬（Grok dashboard 特有）

1. 全屏多会话 dashboard（状态分组、peek、dispatch）：Corvus 是单会话线性 TUI，没有并行多会话管理面；并行子代理应在会话内用 #4477 式内联面板解决，而不是另开管理平面。
2. 底部输入框"永远派发新会话"语义、Ctrl+S attach、Ctrl+[ / Ctrl+] 会话循环、Ctrl+\ dashboard 切换——全部依赖多会话存在。
3. Inactive/Idle "N more" 折叠、Ctrl+T pin、Ctrl+R rename、Shift+↑/↓ 重排 pinned——多会话列表管理，单会话无意义。
4. Peek 面板的"选中即回复、切行清草稿"状态机——没有行列表就没有这套交互。
5. 实时主题预览可学，但逐项 RGB 插值/波纹动画在 Bubble Tea 终端不值得（Qwen 源码注释亦承认终端渲染不了平滑渐变）。
6. skeleton/shimmer 是 web 端概念；TUI 用 1 行占位/固定高度替代，避免闪烁与重绘成本。

## 7. 证据链接

- Qwen #4588：https://github.com/QwenLM/qwen-code/issues/4588
- Qwen #4477：https://github.com/QwenLM/qwen-code/pull/4477
- Qwen #4595：https://github.com/QwenLM/qwen-code/pull/4595 ；#4598：https://github.com/QwenLM/qwen-code/pull/4598
- Qwen #5003：https://github.com/QwenLM/qwen-code/pull/5003 ；#3909：https://github.com/QwenLM/qwen-code/pull/3909
- Qwen #8077：https://github.com/QwenLM/qwen-code/pull/8077 ；#4422：https://github.com/QwenLM/qwen-code/pull/4422
- Qwen #3710：https://github.com/QwenLM/qwen-code/pull/3710 ；#4741：https://github.com/QwenLM/qwen-code/pull/4741
- Qwen #2770：https://github.com/QwenLM/qwen-code/pull/2770 ；#5627 / #6678 / #6793 / #3155 / #6533 / #5287：同仓库 PR 编号
- 源码（main，2026-08-06）：InlineParallelAgentsDisplay.tsx、LiveAgentPanel.tsx、TableRenderer.tsx、InlineMarkdownRenderer.tsx、semantic-colors.ts、themes/{theme-manager,qwen-dark,qwen-light}.ts、Header.tsx、customBanner.ts、LoadingIndicator.tsx、useAnimationFrame.ts、useAnimatedScrollbar.ts、useTerminalProgress.ts
- Grok 官方文档：https://docs.x.ai/build/features/dashboard ；x.ai 公告：https://x.ai/news/agent-dashboard
- grok-build user guide：https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/23-dashboard.md（及 03-keyboard-shortcuts.md、05-configuration.md、06-theming.md、16-subagents.md）
- Qwen 设计文档（站点已 404，仅搜索引擎快照）：tui-spacing-density-pr1、tui-user-message-half-line-pr2、customize-banner-area（qwenlm.github.io/qwen-code-docs/en/design/…）
- 第三方报道（含 branch/权限模式行描述）：https://news.cocoloop.cn/2026/06/grok-build-agent-dashboard/ 、https://dev.to/akaranjkar08/grok-build-agent-dashboard-run-8-parallel-coding-agents-from-one-screen-32bf
