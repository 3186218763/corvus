# Claude Code TUI 渲染与操作设计调研（供 Reasonix 渲染/动画/美术专项）

- 日期：2026-08-06；调研对象：anthropics/claude-code（闭源，v2.1.89+）
- 标注：**【事实】** = 官方 docs/官方仓库 issue/PR 或本地源码可证；**【推断】** = 基于公开资料的合理判断
- 重要区分：Claude Code 有两条渲染路径——经典渲染器（Ink/React fork，有社区逆向资料）与 2026 年新全屏渲染器（官方 docs 为主，闭源）

## 一、实现机制

1. 【事实】2026-03 末 v2.1.89 引入全屏渲染器（"flicker-free alt-screen rendering with virtualized scrollback"），经 `CLAUDE_CODE_NO_FLICKER=1` 或 `/tui fullscreen` 开启；2026-05-06 起新用户默认启用（research preview），`/tui default` 可切回，切换会保留对话并重启进程。
2. 【事实】全屏渲染画在终端的**备用屏幕缓冲区**（DEC 1049 alt screen，官方明确类比 vim/htop）：输入框固定屏幕底部不随输出移动；**只渲染可见消息**（官方原文 "only renders messages that are currently visible"）；渲染树只保留可见消息，因此长对话内存平稳（"memory stays constant regardless of conversation length" = 官方所说 flat memory）。
3. 【事实】帧间**只发送变化的单元格**（"sends only the cells that changed between frames"）；Windows Terminal 等 ConPTY 终端会错误合并定位写入导致残留，可用 `CLAUDE_CODE_ALT_SCREEN_FULL_REPAINT=1` 强制每帧全量重绘（官方给出的兜底开关）。
4. 【事实】使用 DEC 2026 synchronized output（BSU/ESU 标记）把一帧批量原子提交（issue #35580 明言 "Claude Code uses DEC 2026 synchronized output (BSU/ESU markers) to batch screen updates atomically"）；Claude 团队还向 tmux 上游提交了 sync output 支持（tmux PR #4744，作者自述 "I work on Claude Code"）。
5. 【事实】滚动跳顶根因（issue #35580 的 root-cause 分析）：长响应全量重绘时在 sync block 内发 `CSI 2J`+`CSI H`，终端的滚动位置被重置；修复是剥离全量重绘 sync block 里的 `CSI 2J`（保留 BSU/ESU 包裹），短响应的增量更新本就正常。
6. 【事实】全屏模式交互变化：原生 Cmd+F/tmux 搜索不可用 → 改 `Ctrl+O` 进 transcript 模式后 `/` 搜索、`[` 把全文写回终端原生 scrollback；原生拖选复制 → 应用内选择（释放自动复制）；剪贴板走 pbcopy/wl-copy/xclip/OSC 52/tmux paste buffer；`Shift` 按住可让位原生选择；`CLAUDE_CODE_DISABLE_MOUSE`/`_DISABLE_MOUSE_CLICKS` 可降级。
7. 【事实】经典渲染器架构（社区逆向资料，DeepWiki 2026-04 快照 + claude-code-from-source）：fork 自 Ink，`react-reconciler` 自定义 host + `ConcurrentRoot`；Yoga flexbox 布局并做 ANSI 感知测量；`VirtualScroll` 组件做窗口化渲染（只渲染可见项、按内容换行动态算高、维护离屏滚动缓冲）；鼠标 SGR 序列做 hit-test 触发 onClick；`@alcalzone/ansi-tokenize` 分词并在虚拟滚动边界裁剪。
8. 【事实】经典渲染器性能层（claude-code-from-source ch13，社区逆向、单源、含推断成分）：自定义 DOM 仅 7 种元素（root/box/text/virtual-text/link/progress/raw-ansi）；**双缓冲 Screen**：每格两个 `Int32` 打包字（charId | styleId/hyperlinkId/width），`BigInt64Array` 整行清零；CharPool/StylePool/HyperlinkPool 三池 interning（ASCII 128 表快路径；StylePool 用 bit0 标记"空格上是否可见"）；damage rectangle 限定 diff 行范围；blit 快路径把上一帧未脏子树单元格直接拷贝；diff 典型帧输出 <1KB ANSI（全量约 100KB+）；16ms throttle + 滚动 4ms 独立调度；滚动热路径绕过 React（直接改 DOM + microtask，<1ms）；池每 5 分钟 generational 重置；alt-screen 进入用 `useInsertionEffect` 保证首帧前切屏；resize 同步处理但把擦屏延迟进下一个 BSU/ESU 原子块。
9. 【事实】本仓库栈现状（本地源码）：bubbletea v2.0.7 已内置 DEC 2026——启动时按 TERM_PROGRAM/SSH_TTY/WT_SESSION/ghostty/wezterm/alacritty/kitty/rio 决定是否 DECRQM 探测（`tea.go:963`），渲染时用 `SetModeSynchronizedOutput`/`ResetModeSynchronizedOutput` 包裹，不支持时降级 hide/show cursor（`cursed_renderer.go:528`）；底层 ultraviolet `ScreenBuffer` 已有行/单元格级 dirty 跟踪；但每帧仍全量重算 `View()` 字符串。
10. 【推断】全屏渲染器内部实现未开源：docs 只描述行为（alt screen + 虚拟化 + 增量 cell 写 + 鼠标）；从 issue 证据看其写屏策略 ≈ 增量定位写 + DEC 2026 原子包裹，与经典渲染器共用部分渲染管线（BSU/ESU 写法一致）。

## 二、动画

1. 【事实】思考阶段：spinner + 动词（第三方 spinnerverb 观察：Thinking/Reasoning/Pondering…）+ 秒数计时器（issue #35987 提到 thinking 阶段 spinner/seconds counter）。
2. 【事实】v1.0.73-1.0.81 起思考动画加了 shimmer 微光效果（issue #6038 用户抱怨，说明存在且默认开）。
3. 【事实】`prefersReducedMotion` 设置可"减少/禁用 UI 动画（spinners、shimmer、flash effects）"（settings docs，无障碍向）。
4. 【事实】流式光标：真实终端光标被隐藏，TUI 用 `chalk.inverse()` 渲染假光标（issue #39245 用户分析；IME 合成文本仍显示在终端底部 = 已知局限）。
5. 【事实】流式输出是**增量更新**（每帧重算+diff，只写变化行/cell），但部分终端（ConPTY/VS Code）会合并写入导致"成块出现而非逐 token"（issue #29213 用户报告；官方文档也承认渲染吞吐是瓶颈，全屏模式主要解决此问题）。
6. 【事实】`/powerup`：终端内交互式动画演示课程（"Interactive lessons ... through animated demos"，v2.1.90 加入）。
7. 【事实】滚动动画/手感：滚轮快速甩动有加速度（`wheelScrollAccelerationEnabled`，v2.1.174+）；`/scroll-speed` 提供实时标尺对话框（边滚边调、←/→ 调档、r 重置，v2.1.172+ 支持 0.25 档）。
8. 【事实】auto-follow：滚轮上翻即暂停跟随，屏幕底部浮出 "Jump to bottom" 按钮并显示 `3 new messages` 计数；`Ctrl+End` 或滚到底恢复跟随（v2.1.206+ 按钮提示随键盘能力自适应）。
9. 【推断】动画粒度整体克制：只有 spinner/计时/闪烁光标/按钮浮层，无转场动画；"流畅感"主要来自低延迟重绘（<16ms）而非动画本身。

## 三、美术/密度

1. 【事实】footer 常驻一行：模型/版本/成本信息 + 可点击 badge（`footerLinksRegexes` 把 PR/工单 ID 渲染为徽标链接，`prUrlTemplate` 配 PR badge）；用户抱怨占 2+ 行（issue #23708）→ 官方趋势是更薄（一行状态行 + `statusLine` 自定义）。
2. 【事实】`/focus` 低噪模式：只显示最后一条用户 prompt + 工具调用**一行摘要（含 edit diffstats）** + 最终回复。
3. 【事实】工具卡片折叠：MCP 调用默认折叠为一行（"Called slack 3 times"）；全屏模式下**点击折叠的工具结果展开/收起**，调用与结果一起展开，只有还有内容可看的才可点（fullscreen docs 原文）。
4. 【事实】transcript（Ctrl+O）密度更高：每条 assistant 消息带时间戳与所用模型；可展开详细工具调用。
5. 【事实】主题体系：`/theme` 提供 auto（跟随终端明暗）/dark/light/色盲友好 daltonized/ANSI 主题（用终端调色板）/自定义主题（`~/.claude/themes/` 或插件）；代码块语法高亮可在主题选择器内 Ctrl+T 开关。
6. 【事实】首屏：启动横幅含吉祥物 ASCII（issue #41965 的终端样例显示 `▐▛███▜▌` 横幅，且全量重绘时会反复重印）。
7. 【推断】"thin footer + 折叠卡片"是同一设计哲学：默认低信息密度，细节按需展开（点击/transcript/`[` 导出），减少视觉噪声。

## 四、操作设计

1. 【事实】Esc 语义分层：单按 = 打断当前响应/关对话框；**Esc Esc** = 输入框有文字时清空草稿（存入历史，Up 可召回）、为空时打开 rewind 菜单（v2.1.216 修复长会话中失效 bug）。
2. 【事实】双按防误触模式：Ctrl+D 退出需 800ms 内按两次；Ctrl+X Ctrl+K 杀后台 subagent 需 3s 内按两次；Ctrl+L 两次（2s 内）才执行 /clear。
3. 【事实】Ctrl+T = 任务清单（to-do checklist）开关，最多显示 5 项，放在状态区；Ctrl+O = transcript 查看器。
4. 【事实】transcript 查看器（less 风格）：`/` 搜索（Esc 取消并还原滚动位置）、`n`/`N` 下/上匹配、`{`/`}` 跳到上/下一条用户 prompt、`[` 全文写回原生 scrollback、`v` 打开 $EDITOR、`q`/`Ctrl+C`/`Esc` 退出；全屏模式下 `?` 打开完整快捷键面板。
5. 【事实】`?` 在空输入时切换快捷键帮助面板（v2.1.211 修复"残留 ? 误触发"边界）；非空时正常输入 `?`。
6. 【事实】斜杠命令入口 `?` 帮助体系之外，还有 `/tui default|fullscreen`（无参打印当前渲染器）、`/feedback`（带会话上下文上报）、`/terminal-setup`（按终端装 Shift+Enter 等按键）、`/scroll-speed`、`/focus`、`/theme`、`/powerup`。
7. 【事实】`/tui default` 切回经典渲染器时可选弹 feedback prompt（问为什么切换，Enter 发送 / Esc 跳过）——官方借此收集渲染器流失原因。
8. 【事实】快捷键可重绑（keybindings 系统）：`transcript:toggleShowAll`、`transcript:exit`、`scroll:bottom` 等；vim 模式 + `vimInsertModeRemaps`（如 `jj`→Esc，v2.1.208+）。
9. 【事实】鼠标操作设计：点输入框定位光标、点 `/` 命令与 `@` 文件建议项、双击按 iTerm2 词边界选词（文件路径整选）、三击选行、滚轮滚动、Shift+方向键扩展选区；终端能力提示按平台自适应（macOS 提示点击或 Fn+↓）。
10. 【推断】防误触的总原则：**低代价操作单键、高代价操作双键+时间窗**；全局帮助入口（`?`/Ctrl+O）与"导出到原生 scrollback"（`[`）始终兜底。

## 五、可借鉴点（Bubble Tea 栈，具体到技术名）

1. 全屏渲染路径 = `tea.WithAltScreen()` + 自研虚拟化消息窗口 + 固定高度底部输入区（纯布局）；代价是失去原生 scrollback，必须配套内置搜索（transcript 模式）与"写回 scrollback"逃生口。**可复制**。
2. DEC 2026 sync output：bubbletea v2.0.7 已内置（DECRQM 探测 + BSU/ESU 包裹 + 无支持降级 hide/show cursor），直接用；若自写渲染路径，记得**不要在 sync block 内发 `CSI 2J`**（issue #35580 的教训）。**可复制（且已在栈内）**。
3. 增量写屏：本栈已有 ultraviolet `ScreenBuffer` 行/cell 脏跟踪 + bubbletea 的 `WithANSICompressor` 式合并；差距在每帧全量重算 `View()`——借鉴 Claude 的"脏子树路径 + damage rectangle"思路：把流式区域与静态区拆成独立渲染段，静态段做缓存。**可复制（思路）/部分**。
4. 虚拟化滚动 + flat memory：自建"模型层只保留可见行窗口（offset/height/wrap 索引），历史消息存磁盘/紧凑数组"；bubbles `viewport` 持有全量内容不满足 flat memory。这是全屏模式长对话不卡的内存关键。**可复制（需自研）**。
5. 滚动热路径：Claude 让滚动绕过 React 直接改 DOM+microtask（<1ms）；Bubble Tea 对应做法 = 滚轮 msg 直接改 model 滚动偏移并立即触发渲染（`tea.Render`），不走复杂状态机。**可复制**。
6. 交互双键协议：Esc Esc（清草稿/rewind）、Ctrl+D 800ms 双按、Ctrl+X Ctrl+K 3s 双按、Ctrl+L 两次 2s 内 /clear——Bubble Tea 里用按键时间戳状态机即可。**可复制**。
7. `?` 空输入帮助面板、Ctrl+T 任务清单（≤5 项浮层）、Ctrl+O transcript 覆盖层（less 键位 + `/` 搜索 + `{`/`}` 跳 prompt + `[` 导出 scrollback + `v` 打开编辑器）。**可复制**。
8. 工具卡片折叠摘要（"Called X 3 times"、一行 diffstat）、点击展开、`/focus` 低噪模式、footer 徽标（`footerLinksRegexes` 思路）。**可复制**。
9. 假光标（隐藏真光标 + 高亮块）与 spinner 动词+计时+shimmer：bubbles/spinner + lipgloss 可实现；必须接 `prefersReducedMotion` 门控（无障碍）。**可复制**。
10. auto-follow 浮层："Jump to bottom" + `N new messages` 计数，滚动离开底部即暂停跟随（scroll offset 状态即可）。**可复制**。
11. 滚动速度调节：滚轮加速度 + `/scroll-speed` 实时标尺对话框（←/→ 调档、r 重置）。**可复制**。
12. 鼠标：`tea.WithMouseCellMotion()`（SGR 1006）已支持点击/滚轮；Shift 让位原生选择需检测终端能力并显示正确提示键。**可复制**。
13. 主题体系：auto/dark/light/daltonized/ANSI/自定义——仓库已有 `colorprofile` + lipgloss，可完整复刻；首屏吉祥物 ASCII 是低成本记忆点。**可复制**。
14. 渲染器热切换 + 流失反馈：`/tui` 命令切换渲染路径（重启进程保留会话）+ 切换时可选 feedback prompt。**可复制（会话持久化较麻烦，可简化为同进程切换）**。

## 六、不可照搬

1. Ink/React reconciler + `ConcurrentRoot` + Suspense 懒加载语法高亮：Bubble Tea 是 Elm 式消息循环，无并发组件树；近似方案 = goroutine + `tea.Cmd` 异步高亮 + 占位帧（仓库已有 tree-sitter/ultraviolet，但管道需自建）。
2. Yoga flexbox + ANSI 感知测量：Bubble Tea 无 flexbox 引擎，布局靠 lipgloss 手动排版；Claude 的 7 元素 DOM 模型无法迁移。
3. 内存池设计（`Int32Array` 打包格 + CharPool/StylePool/HyperlinkPool interning + 5 分钟 generational reset）：面向 JS GC 的优化，Go 值类型 + 每帧复用切片即可达到类似效果，照搬收益低。
4. blit 子树缓存（上一帧未脏区域单元格直接拷贝）：依赖 DOM 层节点缓存，Bubble Tea 无此层；只能靠 View 构造优化近似。
5. 事件 capture/bubble + React 优先级调度：架构不兼容。
6. 逐终端启发式（JetBrains 滚轮 bug 检测、Ghostty/Warp Cmd+click 特判、Apple Terminal 排除 sync）：厂商特定行为，可借鉴"能力探测+降级"思路，代码不可复用。
7. 全屏渲染器本体（虚拟化 scrollback 内部实现、flat memory 细节）未开源：只能按 docs/issue 行为复刻，不能搬代码。
8. 经典渲染器的流式输出由真实滚动行为承载（输出进 scrollback）：alt-screen 下无 scrollback，必须重构为"应用内滚动 + 导出"，交互模型不同，不可直接照搬。

## 七、证据链接

- 官方 fullscreen 渲染 docs：https://code.claude.com/docs/en/fullscreen
- 官方交互模式 docs（快捷键表）：https://code.claude.com/docs/en/interactive-mode
- 官方命令参考（/tui、/powerup、/theme）：https://code.claude.com/docs/en/commands
- 官方设置参考（prefersReducedMotion、footerLinksRegexes、theme）：https://code.claude.com/docs/en/settings
- What's New W14（v2.1.89 全屏渲染器发布说明）：https://code.claude.com/docs/en/whats-new/2026-w14
- DEC 2026 sync block 内 CSI 2J 滚动跳顶 root cause：https://github.com/anthropics/claude-code/issues/35580
- tmux 下缺 DECSET 2026 闪烁分析：https://github.com/anthropics/claude-code/issues/37283
- v2.1.89 默认启用全屏破坏 scrollback 回归：https://github.com/anthropics/claude-code/issues/41965
- 假光标/IME 合成文本问题：https://github.com/anthropics/claude-code/issues/39245
- thinking shimmer 动画用户反馈：https://github.com/anthropics/claude-code/issues/6038
- 分块渲染 vs 逐 token 流式：https://github.com/anthropics/claude-code/issues/29213
- footer 占行抱怨：https://github.com/anthropics/claude-code/issues/23708
- tmux sync output 上游 PR：https://github.com/tmux/tmux/pull/4744
- Ink fork 渲染引擎（DeepWiki，二级）：https://deepwiki.com/xorespesp/claude-code/4.1-ink-fork-and-rendering-engine
- 虚拟滚动/输入处理（DeepWiki，二级）：https://deepwiki.com/xorespesp/claude-code/4.3-hooks-and-input-handling
- 终端 UI 逆向（claude-code-from-source ch13，二级）：https://github.com/alejandrobalderas/claude-code-from-source/blob/main/book/ch13-terminal-ui.md
- spinner 动词观察（第三方）：https://www.npmjs.com/package/spinnerverb
- 本栈源码：bubbletea v2.0.7 `tea.go:963`（sync 探测）、`cursed_renderer.go:528`（BSU/ESU 包裹）、ultraviolet `ScreenBuffer`（cell dirty）
