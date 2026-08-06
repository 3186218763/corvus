# 设计审查报告：Transcript Identity & Tool-Card Coloring

**审查对象:** `docs/superpowers/specs/2026-08-06-tui-transcript-identity-design.md`（commit 09d268d，只读）
**对照:** 代码事实核查（`internal/cli/` 当前工作树 = HEAD）、既有调研 `research/2026-08-06-render-animation/`（codex.md、00-synthesis.md）、Codex CLI main 分支源码（联网，`codex-rs/tui/src/exec_cell/render.rs`）
**方法:** 逐条核对 spec 引用的函数/行号/调用点/测试锚点；按 §4.1 位掩码规则对所有提及序列逐轮推演；联网核对 §1 的 Codex 参考。

---

## 1. 内部一致性（§4 marker 模型 ↔ §4.2 渲染 ↔ §4.3 同步 ↔ §8 验收）

**结论：核心模型自洽，无需推翻；存在 1 处自相矛盾（§6.10）与若干引用错误/开放点。**

1. **位掩码与表格推演全部通过。** `markerNone=0 / markerUserCurrent=1 / markerAssistantNamed=2`，bundle 双标记 = 3，渲染分支用 `& != 0` 判定，语义正确。逐序列推演：
   - `[u1,a1]`、`[u1,a1,u2]`、`[u1,a1,tool,a2]`、`[bundle]`、`[bundle,a2]`、`[bundle,u1]`、`[u,md,u]`、`[u,md,md]`（删末 md 后前一 md 重新获名）均与 §4.1 表一致。
   - `[u1,md1,u2,md2]` 多回复轮次：u1 淡化、md1 无名、u2 满色、md2 命名 ✓；仅最后 answer 命名成立。
   - **§4.3「≤2 indices change per commit」成立**：lastUser / namedIdx 每次提交各最多移动一次（tool/reasoning/banner/fixed commit = 0 变化；remove/truncate 同理 ≤2）。
2. **§4.3 step 2 的「确定性 post-append marker」成立**：live 路径提交 user/markdown/bundle 时该块必为各自「最后一个」；bundle 提交点（首帧 chat_tui.go:916、resume branch.go:149）均在清空后的索引 0，双标记语义正确。
3. **唯一自相矛盾（P1）**：§4.2 规定 `renderUserBubble` 签名加 `current bool`，§6.10 却把 `chat_tui_test.go:1436–1451` 标为「unchanged」。`:1441` 的 `renderUserBubble("hello world", 80, false)` 是 3 参调用，签名变更后**必改（编译错误）**；§6.10 还漏了同文件 `:1411`、`:3439` 两个直接调用，以及 `transcript_test.go:153/213/280/318` 四个 `renderTranscriptSource(source, width)` 直接调用（若按 §4.2 给该函数加 marker 参数）。
4. **设计决策被推迟到 plan（P1）**：§4.2「`replaySectionsFor` 默认语义 decided in the plan」与 §6.10「specify in plan」是同一处悬空决策。`replaySectionsFor` 目前仅测试使用（transcript_test.go:47/64），若默认 all demoted，`TestReplaySectionsKeepAssistantIdentity`（:43–55，断言 `"  ◆ Corvus"` 前缀）必红；若默认命名，语义又与「生产路径都走显式 marker」不一致。应在 spec 定死（推荐 all demoted + 同步改写该测试断言），而非留给 plan。
5. **行号引用错误（P0 × 2，P1 × 1，其余 P2）**：见 §7。
6. **§4.4 引用「theme.go:30–35 comment（hand-picked 256-color fallbacks）」位置错误**：:30–35 是 `cliThemeStyle` 结构与 muted/subtle/faint 层级注释；「hand-chosen rather than computed」约定实际在 theme.go:80（P2 批量）。
7. **「one palette slot + one derived slot」（§2 non-goal 5）与「small map[styleName]xterm」有轻微张力**：8 个 accent style 各需一个 xterm 回退值，要么复用 theme.go:103–110 已有的每 style accent xterm（推荐，零新表），要么引入新 map。机制需在 spec 写清（P2）。
8. **bundle 双标记在 `[bundle, bundle]` 会双 current**（两个 bundle 均满足 `lastUser < i`）。实际不可达：branch.go:141 每次 resume/switch/rewind 先 `clearTranscriptDisplay()`，chat_tui.go:916 首帧 bundle 提交于空 transcript。建议 §4.1 补一条不变量注记「replayBundle 仅出现在索引 0」防未来路径踩坑（P2）。

## 2. 与既有 spec 路线图一致

**结论：一致，无冲突。**

- 头部声明「follows P1 / P1.5，无 P2/P3 依赖」属实：新颜色只走 `cliPalette` 槽 + `themeFg`/`themeStyle`，`color_discipline_test.go` 可保持绿色，不触 P2 主题重构。
- 与 P1.5（render-animation）无语义冲突：本 spec 的重渲染入口（`setTranscriptBlock` :59 → `markLiveDirty` :439）与 P1.5 的增量 wrap 轨道共用 `liveDirtyIdx` 机制，正交。
- **唯一协调点（P2）**：本 spec task 2–4 与 P1.5 都改 `transcript.go` 的 `commitTranscriptSource`/`setTranscriptBlock`/`reflowTranscript`。若 P1.5 先行合入，本 spec 的 task 3 需按新代码基线微调。plan 中注明顺序即可，不构成阻塞。

## 3. 歧义与二义性（实现会卡住、spec 未定义的开放点）

1. **Esc un-send 的 marker 语义（P1）**：`unsendPending`（chat_tui.go:3814）→ `truncateTranscriptBlocks(m.bubbleStartIdx)`（:3817）。按无状态推导，截断后**前一 assistant 自动重新获名**（它又成为 lastAssistant 且无后续 user）；用户重发后再次降级——名字随 Esc/重发「闪烁」。该行为与 liveness 模型自洽（编辑期间上一轮就是当前轮），但 spec 完全未提，UX 上需显式决策：接受推导语义（推荐，补测试）或在 truncate 时抑制重命名（引入状态，违背原则 §3.1，不推荐）。
2. **replaySectionsFor 默认语义**（P1，见 §1.4）。
3. **native scrollback 下 copy 一致性（P2）**：native 模式可见文本的 marker 在 `commitTranscriptSource` 打印时冻结（finalize :1858–1860 flush），而 `buildCopyTranscript`（transcript.go:255）现算 markers——降级块在 copy 里是 `  ◆`、屏幕上仍是 `  ◆ Corvus`。native 模式无 transcript viewport（View 不含 transcript），选择/复制大概率不可用，但 spec 的 copy parity 目标应明确限定「alt-screen 渲染路径」，并在 §4.3 native 段补一句。
4. **`toolArg` 命名碰撞（P2）**：新 palette 槽 `toolArg` 与 toolcard.go:120 现有函数 `toolArg(name, args string)` 同名（Go 中字段/函数不同命名空间，不冲突），但计划/实现中 `activeCLITheme.toolArg`（字段）与 `toolArg(...)`（函数）易混淆，建议在 plan 里显式注意或改槽名。
5. **pre-scan 措辞（P2）**：§4.2「Role assistant with non-empty Content」与实现 `strings.TrimSpace(m.Content) != ""`（chat_tui.go:4704）不一致，空格-only content 是边界歧义；统一为「trim 后非空」。
6. **streaming flush 的 marker 计算成本（P2）**：§4.3 说 streamAnswer/commitPending「compute the marker from the current state」，即每次 flush（`answerIdx >= 0` 分支，chat_tui.go:2557/2578）都 O(n) 扫 `currentTranscriptMarkers`。实际该块在 turn 内恒为 named，可缓存最后一次 commit 值；spec 的「derive from state」是安全默认，plan 里注明可选优化即可。
7. **native 模式 setTranscriptBlock 空操作（P2）**：§4.3 step 4 的 re-render 在 native 模式只改内存（`setTranscriptBlock` 不追加 `pendingCommit`），不影响已打印文本——spec 的「frozen at commit time」已隐含此意，补一句明示即可。
8. **plan mode 用户气泡（无开放点）**：历史气泡一律淡化、与 planMode 无关（`renderUserBubble` 的 planMode 参数保留现状），spec 无需额外定义。

## 4. 范围（是否适合一个实现计划 / YAGNI）

**结论：通过。适合单个实现计划，无蔓延风险。**

- §7 的 6 个任务顺序依赖合理（task 2–4 同改 transcript.go，必须顺序；task 5 toolcard 独立可后置），每个任务即 TDD 单元。
- YAGNI 干净：无新主题、无 config 段、无 bash 词法高亮（non-goal §2.2 明确）、无背景 chip、失败行不动、banner 不动。§5.1 的 diff header / replayed tool cards 是既有函数复用带来的免费收益，不算蔓延。
- 唯一需一句话确认（P2）：diff header 的 path 参数（diffview.go:72 经 `toolHead`）会自动变成 `toolArg` 色——spec 说「diff header consistent」未明确 arg 用色；确认这是预期（与工具卡一致）即可，无需新增设计。

## 5. 风险覆盖（§9）

**结论：需补 4 项。**

1. **un-send/truncate × marker 交互（P1，§9 未覆盖）**：见 §3.1，这是 marker 状态机唯一未定义的可达交互，风险表应加一行。
2. **faded 可读性缺量化判据（P1）**：§9 有「Faded color illegible」行，mitigation 是「visual check pinned in tests」，但 §8 无量化标准。真正的判据是**三档两两可区分**：`full accent` / `userBubbleFaded`（45% accent + 55% #808080）/ `faint`（#858b96，历史 assistant ◆ 用色）在同一 transcript 并存时必须能分清。浅色主题（sandstone/linen/porcelain/glacier）下 accent 本身偏深，混合 55% 灰后与 faint 的区分度、以及 dark 主题下 faded 是否过暗，都需在 §8 补验收（可对比度阈值或并排视觉 pin）。
3. **native scrollback × copy 一致性（P2）**：见 §3.3。
4. **toolArg 与既有色的区分度（P2）**：§5.2 提了要求（distinct from faint/muted/verb colors）但未进风险表；dark `#a5b0bd` 与 `faint #858b96`、`muted #cbd0d8` 属同色系灰蓝，区分度需视觉验证 + 测试 pin。另补一条 P2：与 P1.5 的合入顺序（§2）。

## 6. 成功标准（§8）

**结论：总体可测，补 3 行。**

- 自动项（fresh/second exchange/multi-answer/resume//cls/copy/tool cards/regression）均可自动化，测试设计（§6）与之一一对应。
- 人工项「themes」行（visual pass）缺终端/主题清单，建议钉死与 P1.5 审查一致的 3–4 个终端（Warp/iTerm2/Windows Terminal/konsole）与 8 个 accent style 全过。
- 缺 3 个验收行（P2）：(a) Esc un-send 场景（§3.1 决策后补对应行）；(b) bundle+user 混合多轮（§6.1 测试有 `[u,md,bundle]`/`[bundle,md,u]`，§8 无验收行）；(c) copy parity 限定 alt-screen 路径（§3.3）。

## 7. 修订清单

### P0（阻塞，均为指错函数的行号引用——计划作者会读错代码锚点）

1. **§2 non-goal 6 / §5.3 的 `tickToolRunning` 行号 :2447–2480 指错函数**：:2447–2468 是 `beginToolRunning`（chat_tui.go:2440 起），`tickToolRunning` 实际在 :2469–2480。改为 :2469–2480。
2. **§4.4 的 `applyCLIThemeStyle` 行号 :148–152 指错函数**：:148–152 是 `resolveCLIThemeWithStyle`（theme.go:149 起），`applyCLIThemeStyle` 实际在 :196–202。改为 :196–202。

### P1（应改，修完即可进 writing-plans）

1. **§6.10「chat_tui_test.go:1436–1451 unchanged」错误且清单不全**：`:1441` 的 3 参调用因签名变更必改；补 `chat_tui_test.go:1411/3439`、`transcript_test.go:153/213/280/318`。§6.10 应改写为「签名变更引起的机械更新完整清单」。
2. **§4.3 补 `commitTranscriptSource` 全部生产调用点**：除 streamAnswer(:2555)/commitPending(:2576) 外还有 7 处未列——chat_tui.go:916（首帧 bundle）、:1433（/new banner）、:1532（RunShell user）、:3782（startControllerTurn user）、:3912（tool 卡）、clear_confirm.go:64（banner）、branch.go:149（resume bundle）。并声明「marker 逻辑封装在 commitTranscriptSource 内，调用点零改动；task 3 对每个调用点补一条断言测试」。
3. **Esc un-send 重命名语义**（§3.1）：spec 明确预期行为（推荐：接受推导语义 + 测试）。
4. **`replaySectionsFor` 默认语义在 spec 定死**（§1.4，推荐 all demoted + 改写 TestReplaySectionsKeepAssistantIdentity）。
5. **§1 Codex 参考改写**（见下节联网核查）：删去「cyan read verbs vs neutral args」的事实性描述，改为定性表述。
6. **§8 补 faded 三档可区分验收**（full/faded/faint 两两可辨，两种模式 + 8 style）。

### P2（可选，随计划顺手修）

1. 批量行号漂移：transcript.go `appendTranscriptBlock` :53（spec 50–54）、`setTranscriptBlock` :59（66–72）、`removeTranscriptBlock` :70（74–96）、`truncateTranscriptBlocks` :95（98–109）、`renderReplayBundle` :130（129–140）、`renderReplayBundleCopy` :147（142–156）、`reflowTranscript` :221（214–223）、`commitTranscriptSource` :231（225–230）；chat_tui.go `streamAnswer` :2543（spec 2557，实际 2557 是 else 分支渲染调用）、`commitPending` :2567（spec 2578）、`replaySectionsForWithAssistantRenderer` :4681（4682–4728）；toolcard.go `toolDot` :81（78–105）、`toolHead` :174（172–181）；theme.go `cliPalette` 字段 :28–48（36–48，type 在 :27）、hand-chosen 注释 :80（30–35）。
2. **`toolCategory` 符号引用错误**：spec 列为函数 :107–114，实际是 `var toolCategory = map[string]string`（toolcard.go:98–107）；§5.1 正文（「extract from the switch inside toolDot」）已自足，改行号/符号描述即可，另注意新函数名 `toolCategoryColor` 与 map 并存。
3. §4.1 加「replayBundle 仅出现在索引 0」不变量（防 `[bundle,bundle]` 双 current）。
4. copy parity 限定 alt-screen 路径 + native 模式 setTranscriptBlock 空操作说明。
5. `toolArg` 槽名与函数名混淆注记。
6. `userBubbleFaded` xterm 回退机制写清（推荐复用 accent 的 xterm，theme.go:103–110 已有 8 个）。
7. pre-scan「non-empty Content」→「trim 后非空」。
8. streaming flush 的 marker 可缓存优化注记。
9. 与 P1.5 合入顺序协调（§2）。

---

## 联网核查：Codex CLI 工具行分色（§1 参考校验）

**结论：spec §1 的参考描述与 Codex 实际行为不符，但设计本身成立（见 P1-5）。**

Codex CLI main 分支 `codex-rs/tui/src/exec_cell/render.rs` 事实：

- **单个工具调用行**：状态点 `•`（成功绿、失败红、运行中动画）+ 「Running / Ran / You ran」标题 bold（**无色**）+ 命令文本做 **bash 语法高亮**（`highlight_bash_to_lines` + `extract_bash_command`）。没有「按 read/write/exec/proc 分类给动词着色」。
- **探索型分组行**（多个 read/list/search 命令合并的 cell）：`Read/List/Search/Run` 标签**统一 cyan**（`title.cyan()`，不是只有 read 才 cyan），文件名/参数为默认前景色。这是与 spec §1「cyan read verbs vs neutral args」最接近的真实形态，但标签不分类、参数也不着色。
- transcript 输出：命令前 `$ ` 提示符 magenta、输出 dim、结束行 `✓` green bold / `✗` red bold + 耗时 dim。
- 佐证：issue #6531 用户抱怨 Codex TUI「output looks almost uniform」、颜色过于单调——Codex 不是「关键词分色」的强参考；`clippy.toml` 甚至硬禁 yellow/blue，与 Corvus 的 exec→warn（黄）taxonomy 不同源。
- 本设计（动词按既有 dot taxonomy 分色 + 参数专用 toolArg 色）是 Corvus 自身分类体系的合理延伸，与 dot 分类一致、成本低，**不因参考失实而失效**；只是 §1 的引用措辞需改为定性（「Codex 用语义色区分工具行状态与命令文本；本设计沿用 Corvus 既有 dot taxonomy 做动词分色」），§9 的「Codex parity is qualitative、验收是 distinct keyword colors」保留。

---

## 总结

- **事实核查**：绝大多数引用精确（renderUserBubble/renderTUIBanner/replaySectionsFor/clearTranscriptDisplay/失败行/buildCopyTranscript/cliPalette/8 accent style/全部测试锚点均吻合）；2 处指错函数（tickToolRunning、applyCLIThemeStyle）= P0；若干行号漂移 = P2。
- **内部一致性**：marker 位掩码、§4.1 表、§4.3 ≤2 变化、bundle 双标记在全部可达序列下自洽；仅 §6.10「unchanged」与签名变更自相矛盾（P1）。
- **P0（2）**：tickToolRunning、applyCLIThemeStyle 行号指向不同函数。
- **P1（6）**：§6.10 测试清单错误且不全；commitTranscriptSource 缺 7 个生产调用点枚举；Esc un-send 重命名语义未定义；replaySectionsFor 默认语义应进 spec；Codex 参考失实需改写；faded 缺三档可区分验收。
- **Codex 参考**：联网核实为 bash 语法高亮 + 状态色 bullet + 统一 cyan 标签 + magenta `$`，无「按类动词分色」；设计本身成立，仅措辞需改。
- **范围/YAGNI**：适合单个实现计划，无蔓延；§9 风险表缺 4 项（1 P1 + 3 P2）。
- **建议**：修完 P0 引用与 P1 缺口后**可以进入 writing-plans**；设计无结构性缺陷，无需回到 brainstorming。
