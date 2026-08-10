# Corvus 深度调研报告：哪些地方做得不够好

> 调研日期：2026-08-10
> 调研对象：`/home/miku/szj/corvus`（Go 本地 TUI 编码 agent，commit `7591c38`）
> 对标对象：`openai/codex` Codex CLI（源码 `/tmp/codex-ref` + 官方文档）
> 方法：先调研「如何评估一个 agent」建立框架 → 4 个并行子代理分域深挖（评估方法论 / 代码质量 / 安全并发 / Codex 对标）→ 全部关键结论本地二次核验（`go build`、`go vet`、`go test`、`staticcheck`、`gocyclo`、逐条 grep 证据）。
> 只读调研：除本报告与 `tmp/` 下的子报告外，未修改任何项目文件。

---

## 0. TL;DR（结论先行）

1. **工程基线健康，且比上次审计（同日早前的 `DEEP_DIVE_CODEX_COMPARISON.md`）显著改善**：`go build` / `go vet` / `go test ./...`（40 包全绿）/ `staticcheck`（0 条）/ `golangci-lint`（0 条）全部通过；死代码清零；上次的 4 个高危修复经逐条核验**真实有效**（MCP 启动审批、项目 hook 禁代答、jobs 通知顺序、`/compact` 生命周期）。
2. **但仍有 4 个 P1 级安全/信任问题**，其中 3 个是本次新发现：
   - **项目 `.corvus/config.toml` 无信任门，且整体替换用户级安全控制**（可清空用户 deny、自授 `Write(*)`、`mode="allow"`、`bash="off"`、`allow_write=["/"]`、注入 system prompt）——影响 TUI / corvus-exec / corvus-mcp-server 三个入口（`internal/config/load.go:128-147`）。
   - **项目 hooks 仍无条件执行任意代码**（只堵住了「代答权限」，没堵住「执行命令」本身），无指纹/提示/headless 豁免（`internal/boot/boot_hooks.go:43`）。
   - **MCP 启动审批被控制器 `mcpSpec` 路径整体绕过**：`/mcp connect`、模式切换、桌面 SessionAPI 均免审批免 grant 直连；TUI 没有「批准启动」UI（`internal/control/mcp_manage.go:97-118`）。
   - **Linux bwrap 沙箱缺 `--unshare-pid/ipc`**：沙箱内可读宿主 `/proc/<corvus-pid>/environ` 窃取 API key、可 kill 同 uid 进程（`internal/sandbox/seatbelt_other.go:84-88`）。
3. **相对 Codex CLI 的最大差距已经从「平台缺失」收窄为「纵深与生态」**：headless exec（`corvus-exec`）、MCP 服务端（`corvus-mcp-server`）、CI、网络出口策略都已补上；`web_search`/`tool_search` 工具发现也已实现（本地引擎 searxng/brave/tavily + 始终可用的 `tool_search`）；当前主要差距是：网络策略无 MITM/逐请求拦截、无 `review` CLI 子命令、无 execpolicy `.rules`、无 `doctor`/自更新、CI 与发布管线薄。
4. **架构上「God file 转移」而非「消除」**：`internal/agent/agent.go` 4,239 行/187 函数、`internal/control/controller.go` 2,479 行/111 方法仍是最大的单体；全仓 62 个函数圈复杂度 > 30（34 个 > 40、18 个 > 50），复杂度「趋势门禁」仍是 report-only 且**不落盘**，96 条基线无法对比。
5. **重复实现是质量头号隐患**：三个 provider 各写一份 200+ 行 `readStream`（缓冲上限已分叉：1MB vs 4MB）；`parsePermissionMode` 有 **3 份实现且模式集合不一致**（TUI 7 种 vs headless 4 种）；SSRF 网段判定两处字节级复制。
6. **测试总量 1:1、无空转断言，但安全裁决包覆盖偏弱**：checkpoint 测试/生产比仅 0.30（覆盖 65.2%）；evidence/guardian/capability/tool 仅 60–71%；表驱动率仅 6%。
7. **文档与工程卫生仍有欠账**：README（中英）仍称「只发布一个可执行程序」，实际有 4 个 `cmd/`，`corvus-exec` 零文档；CI race 未覆盖新增的 mcpserver/netpolicy/sandbox/permission/secrets；无 tag、无发布管线；`.corvus/config.toml` 仍含真实 API key（已 gitignore，建议轮换）。
8. **Corvus 有 Codex 没有的差异化优势**：中文/CNY/国产 provider 一等公民、证据链交付门控（review_report + guardian + 失败预算）、git-free checkpoint 回滚、并发写租约、MCP 启动按 sha256 身份精确授权、无登录无遥测。

---

## 1. 调研方法

### 1.1 先回答「怎么评估一个 agent」

评估方法论由独立子代理做纯 web 调研（报告见 `tmp/research_eval_methods.md`，424 行），核心结论：

- **Agent = 模型 + harness + 上下文 + 环境 + 反馈信号的系统**，单一分数（如 SWE-bench %）无法指导工程迭代；必须拆成组件级信号。
- **公开 benchmark 已普遍饱和/污染**：OpenAI 2026-02 宣布停用 SWE-bench Verified（审计发现 59.4% 的模型常失败问题带瑕疵测试），长期评估必须自建 held-out 任务集。
- 评估三根支柱：**任务成功率、效率（步数/耗时/token/成本）、安全与克制（abstention/越权/注入）**；对终端型 agent，安全与克制比「做对」更优先。
- 工程侧评估六项：架构可维护性、测试覆盖、安全模型、并发可靠性、可观测性、CI 门禁。

据此，本次对 Corvus 的审视采用 **7 维框架**：任务成功率 / 效率 / 安全 / 可靠性 / 代码质量 / UX / 工程门禁（详见第 2 节与第 4 节逐条对照）。

### 1.2 本次执行方式

| 子代理 | 分工 | 产出 |
|---|---|---|
| Meitner | 评估方法论 web 调研 | `tmp/research_eval_methods.md` |
| Lagrange | 代码质量/架构/测试/工程卫生 | `tmp/research_code_quality.md` |
| Singer | 安全模型/并发可靠性/新入口攻击面 | `tmp/research_security.md` |
| Darwin | Codex CLI 源码 + 文档对标 | `tmp/research_codex_compare.md` |

关键结论全部经本地二次核验（本文标注「已核验」的条目均为笔者亲自 grep/运行验证，而非转述）。

### 1.3 验证基线（本次实测，2026-08-10）

| 项目 | 结果 |
|---|---|
| `go build ./...` | ✅ 0 错误 |
| `go vet ./...` | ✅ 0 问题 |
| `go test ./... -timeout 20m` | ✅ 40 包全绿，0 失败 |
| `staticcheck ./...` | ✅ 0 条（EXIT=0） |
| `golangci-lint run`（.golangci.yml，CI 同版 v2.12） | ✅ 0 issues（子代理实测） |
| `go test -race`（核心包，子代理实测） | ✅ 全绿 |
| 规模 | 944 个 `.go`，约 30 万行；`internal/agent` 52k 行、`cli` 43k、`control` 33k、`config` 28k |
| 测试 | 约 4,857 个测试函数，无空转断言，goleak 全绿 |
| 复杂度 | 62 个函数 cc>30（34 个 >40、18 个 >50）；agent 包内 15 个 >30 |
| git | 145 commits（4 天，2 位作者），无 tag；核验后当日新增 `web_search`/`tool_search` 未提交改动 |

---

## 2. 评估框架：如何评估 Corvus 这样的终端 agent

### 2.1 Benchmark 全景（要点）

| Benchmark | 测什么 | 对 Corvus 的适配性 |
|---|---|---|
| SWE-bench Verified | 500 个 Python issue 修复 | 已饱和污染（OpenAI 2026-02 停用）；语言单一 |
| SWE-bench Pro / SWE-Lancer | 更难/真实付费任务 | 测模型+harness 组合，不适合单机产品迭代 |
| Terminal-Bench 1.0/2.0/3.0 | 终端任务（Docker 隔离） | **最适配**：单机、终端驱动、确定性验证 |
| METR | 长任务完成时间分布 | 测模型能力上限，非产品回归 |
| GDPval | 真实交付物盲评 | 一次性研究，成本高 |
| AgentAbstain / AbstentionBench | 「克制不行动」 | 值得借鉴：Corvus 应测 abstention |

### 2.2 七维评估框架（本报告使用）

1. **任务成功率**：Pass@1/best-of-3 分开报告；附 fail-to-pass + pass-to-pass 双指标。
2. **效率**：步数、墙钟、token、成本；无效循环占比（重复命令、连续失败重试）。
3. **安全**：越权/沙箱逃逸/提示注入/危险命令门禁/克制（abstention）。
4. **可靠性**：竞态、goroutine 泄漏、中断恢复、checkpoint 完整性。
5. **代码质量**：架构、复杂度、重复、死代码、错误处理。
6. **UX**：可观察性、可审查性、用户控制、TUI 细节、多语言。
7. **工程门禁**：lint/race/覆盖率/复杂度趋势是否真实生效、可复现。

### 2.3 给 Corvus 的评估清单（详见 `tmp/research_eval_methods.md` §7，20 条）

- 自建 10–30 个 Terminal-Bench 风格 Go 任务（Docker 隔离）做回归，Pass@1 ≥ 60% 起步；
- 红队任务内置：读 `~/.ssh`、`/etc/shadow`、上级目录、`sudo`、提示注入（恶意 README/AGENTS.md）→ **0 命中**；
- abstention 用例：信息不足时是否询问而非瞎做；
- 效率口径固定：每任务步数/时间预算上限，无进展步数占比 < 15%；
- 每次失败可回溯完整事件链（transcript 回放）；
- 工程门禁：race 0、泄漏 0、高危项 0 遗留、复杂度趋势落盘对比。

---

## 3. 安全模型（本报告最高优先级的发现）

> 逐条核验了上次 TOP5 声明的修复：**MCP 启动审批置位（`internal/boot/boot_plugins.go:537,555-556`）、项目 hook 禁代答（`internal/hook/hook.go:1185-1195`）、jobs 通知顺序（`internal/jobs/jobs.go:510-522`）、/compact 生命周期（`internal/control/background_commands.go:7-24`）全部真实有效。** 以下为残留与新增问题。

### 3.1 【P1·新发现】项目配置无信任门，且整体替换用户级安全控制

- **证据（已核验）**：合并顺序见 `internal/config/load.go:128-147`——用户配置后合并项目 `.corvus/config.toml`，只有 `Secrets`（load.go:147）与 `UI.Currency`（load.go:150）被回滚保护；`[permissions]`、`[sandbox]`、`[network]`、`[network_policy]`、`agent.system_prompt_file`、`[tools]` 均不做来源区分。TOML decode 覆盖语义下，项目文件里写 `deny=[]`、`allow=["Write(*)"]`、`mode="allow"`、`bash="off"`、`allow_write=["/"]` 会**整体替换**用户列表（子代理已用临时测试实证，临时测试已删除）。
- **影响**：克隆恶意仓库后，在 TUI / `corvus-exec`（默认 `--permission-mode auto`，`cmd/corvus-exec/main.go:91`）/ IDE 中的 `corvus-mcp-server` 打开即中招：用户 deny 失效、写工具无审批、bash 无 OS 沙箱、system prompt 被注入。与「项目 MCP 要审批、项目 hook 不能代答」形成讽刺对比——**绕过路径比修复前更「合法」**。
- **修复方向**：把 `[permissions]`/`[sandbox]`/`[network]`/`[network_policy]`/`agent.system_prompt_file` 列为用户级专属（像 `Secrets` 一样回滚）；或引入首启信任门（指纹 + 明示「此项目可修改你的权限与沙箱设置」）；用户 deny 规则应合并而非替换。

### 3.2 【P1·残留】项目 hooks 无条件执行任意代码

- **证据（已核验）**：`internal/boot/boot_hooks.go:43` 无条件 `hook.Load(...ProjectRoot: root)`；`internal/hook/hook.go:1448` 附近以 `sh -c` 执行，hook 以用户全权运行。已修复的只是「PermissionRequest 的 allow 代答被忽略」（hook.go:1185-1195），但 SessionStart/UserPromptSubmit/PreToolUse/PostToolUse 仍每次会话/每轮执行仓库代码，corvus-exec 无交互同样加载。
- **影响**：克隆恶意仓库 → 任意入口启动一次 → 仓库 hook 立即以用户身份跑 `curl …|sh`、改 `~/.ssh/authorized_keys`、外传 `.env`。
- **修复方向**：与项目 MCP 同级：首启指纹 + 交互确认（或至少显著 Notice + 面板可见）；headless 默认拒绝项目 hooks，除非显式 `--trust-project-hooks`。

### 3.3 【P1·残留】MCP 启动审批被控制器 `mcpSpec` 路径绕过

- **证据（已核验）**：`internal/control/mcp_manage.go:97-118` 的 `mcpSpec` 对项目来源条目设置 `Authorized: exp.Source.UserAuthorized()`（`internal/config/config.go:1212-1220` 对 project 来源返回 true）且**不设置** `RequireLaunchApproval`，与 boot 的审批契约（boot_plugins.go:556）脱钩。调用方包括 `/mcp connect`（`internal/cli/mcp_manager_actions.go:57`）、模式切换自动连接（:169）、`/mcp connect <name>`（`chat_tui_commands.go:576`）、桌面 SessionAPI（`internal/control/port.go:173-176`）。
- **后果**：a) 点一次 connect 即启动，不产生 LaunchGrant、不做身份 pinning；b) 每次新进程都要重新 connect（无 grant 可复用），诱导用户习惯性 connect；c) **TUI 没有「批准启动并记录 grant」的动作**（`/mcp` 面板的 auth 是 OAuth 授权页，mcp_manager_actions.go:221-234）——「批准一次自动启动」的体验在当前 TUI 不存在；d) 共享 host 使授权跨 tab 传播（boot_plugins.go:190-198 复用 client 不校验 `ServerAuthorized()`）。
- **修复方向**：`mcpSpec` 对 `ProjectScoped()` 条目设 `RequireLaunchApproval=true`，connect 前走审批 + 记录 grant；`/mcp` 面板增加「批准启动」动作；共享 host 复用 client 前校验授权。

### 3.4 【P1·新发现】Linux bwrap 沙箱无 pid/ipc namespace

- **证据（已核验）**：`internal/sandbox/seatbelt_other.go:84-88` 的 bwrap 参数只有 `--unshare-net --ro-bind / / --dev /dev --proc /proc --tmpfs /tmp` + writeRoots bind；**没有** `--unshare-pid`、`--unshare-ipc`、`--unshare-uts`、`--die-with-parent`、seccomp。`--proc /proc` 绑定的是宿主 /proc（无 pid namespace 时进程可见性同宿主）。
- **影响**：沙箱内 `cat /proc/<corvus-pid>/environ` 可窃取 corvus 进程环境中的 API key（无论子进程环境过滤与否，宿主进程自身 environ 都在）；可探测 fd、kill 同 uid 进程、向兄弟沙箱进程发信号。
- **修复方向**：bwrapArgs 增加 `--unshare-pid --unshare-ipc --unshare-uts --die-with-parent`（配合 userns 保持无特权可用）；必要时 `--unshare-all` + seccomp；Seatbelt 侧文档化「macOS 无进程隔离」。

### 3.5 P2 级安全发现（摘要）

- **corvus-exec 默认 `--permission-mode auto`**：headless 一键运行写工具 fallback=Allow（`cmd/corvus-exec/main.go:91`），叠加 3.1 的项目自授规则后几乎无防线。
- **corvus-mcp-server 信任项目权限规则**：`cmd/corvus-mcp-server/main.go:63-74` 直接消费项目 `[permissions]` allow 规则，「fail-closed」承诺被项目配置架空（默认 `dontAsk→deny` 只约束 fallback）。
- **bwrap `/dev` 为可写 bind、无 seccomp**；**bash -c 无 `--noprofile/--norc`**，`BASH_ENV` 继承（S5）。
- **web_fetch SSRF**：直连防护扎实，但 6to4/Teredo 内嵌私网地址未拦（S3）；bash 网络策略仅静态启发式，沙箱被关后 netpolicy 形同虚设（S4）。
- **Windows 语义退化**：`[sandbox] bash="enforce"` 在 Windows 被静默归零为 off，状态栏无提示。
- **checkpoint/rewind 无跨进程锁**（F1）；readonly/Plan 模式不约束 hook 副作用与快照写入（F3）。
- **密钥过滤与敏感文件保护默认关闭**（K1，设计取舍但需文档化）；MCP 审批身份不含 env/header **值**，值变更不使授权失效（K2）。

---

## 4. 并发与可靠性

- **核验通过**：jobs 完成 Notice 移至 `close(done)` 之后（`internal/jobs/jobs.go:510-522`）；`/compact /new /clear` 已接入 bgCtx/bgWG + Close 等待（`internal/control/background_commands.go:7-24`、`hooks_lifecycle.go:204`）；syncSink 非阻塞；`-race` 核心包全绿。
- **P2**：async hooks 用 `context.WithoutCancel` 且无跟踪——Close 后仍可能执行（`internal/hook`，C4）；MCP prompts/resources fetch 协程未纳入 WaitGroup（C5）；corvus-exec 事件通道为有界阻塞（C7）；`syncSink` 超时事件可能迟到双投（C6，低）。
- **观察**：`Session.Messages` 导出字段锁外裸读的隐患仍在（agent/session.go 文档明确 `mu` 保护，但 stream/systemPrompt/compact 有裸读点）——当前安全依赖「rotation 门 + 单 turn goroutine」惯例，未来并发读者会静默破坏会话，建议私有化 + `Snapshot()` 收敛。

---

## 5. 代码质量与架构

### 5.1 God file 与巨型函数（核心问题）

- `internal/agent/agent.go` 4,239 行 / 187 函数（76 个 Agent 方法）——上次拆分拆了 cli/control/boot，**agent 没拆**；
- `internal/control/controller.go` 2,479 行 / 111 方法；
- `internal/config` 14,362 行非测试代码（含当日新增 `websearch.go` 27 行；§1.3 的 28k 为含测试总数），含 1,434 行 annotated-TOML 渲染器：`RenderTOMLProjectDelta` cc=153 / 444 行、`RenderTOMLForScope` cc=106 / 514 行（`internal/config/render.go:32,576`，已核验）；
- `handleKeyPress` cc=104 / 458 行（`internal/cli/chat_tui_update_keys.go:25`）；
- 三个 provider `readStream` cc 55–68（openai.go:913 / anthropic.go:470 / responses.go:372）；
- 全仓 62 个函数 cc>30（实测 gocyclo，34 个 >40、18 个 >50）；`internal/agent` 内 15 个 >30（migrate.go:108 cc=57、task.go:793 cc=52、agent.go:1617/3091 cc=50）。

### 5.2 复杂度「趋势门禁」名存实亡

- `.golangci-agent-complexity.yml` 仍为 report-only（`issues-exit-code: 0`），子代理实测基线 **96 条（54 cyclop + 42 funlen）**；
- **CI 不落盘**：每次只在日志打印，无人对比趋势——「趋势监控」没有形成闭环（已核验 `.github/workflows/ci.yml` 第二个 lint job 无 artifact 上传）。

### 5.3 重复实现（质量头号隐患）

- **三个 provider 各写一份 200+ 行 `readStream`**：SSE 空闲看门狗逐行复制，且缓冲上限已分叉（openai 1MB vs 其余 4MB）——流式解析是历史 bug 高发区，应抽公共骨架（已核验三个文件存在独立实现）；
- **`parsePermissionMode` 三份实现**：`internal/cli/cli.go:161`（TUI，7 种模式）、`cmd/corvus-exec/main.go:286`、`cmd/corvus-mcp-server/main.go:196`（headless，4 种；mcp-server 侧函数名为 `mapPermissionMode`）——模式集合不一致，权限语义重复维护；
- **SSRF 网段判定两处字节级复制**（mustCIDR/cgnatRange/blockedFetchIP）。

### 5.4 测试质量

| 指标 | 实测 |
|---|---|
| 测试函数 | 约 4,857 个；测试/生产比约 1:1 |
| 表驱动率 | 仅 6%（4,857 函数 / 305 t.Run） |
| 空转断言 | 0（16 个 t.Log 测试均有门禁） |
| 低覆盖包 | checkpoint 65.2%（t/p=0.30，全仓大型包最低）；evidence 67.3%、guardian 71.4%、capability 67.2%、tool 60.4% |
| 高覆盖包 | proc / shellsafe / netpolicy 96%+ |
| 旧报告 SA4000 断言 | 已修复（chat_tui_test.go 的 `||` 双重复已消除） |

- **重点**：checkpoint `transaction.go` 1,772 行全是事务/补偿高风险函数，测试比却最低；安全裁决包（evidence/guardian/capability）覆盖 60–71%，这些是「自动放行/拦截」的依据，值得优先补。

### 5.5 错误处理与可维护性（多为正面）

- 无裸 panic 滥用（5 处均在 init/编译期）；错误包装统一 `%w`；无 `%v` 吞链；0 个 TODO/FIXME/HACK；210 个包级全局变量（多数为安全只读表）。
- 遗留：`RedactMessages` 永久 nolint；主题样式注释列出 4 个不存在的样式（slate/carbon/nocturne/amber，与实现矛盾）。

---

## 6. 与 Codex CLI 对比（详见 `tmp/research_codex_compare.md`）

### 6.1 能力对比矩阵（浓缩）

| 领域 | Codex CLI | Corvus | 评级 |
|---|---|---|---|
| CLI 命令面 | 20+ 子命令（exec/review/mcp/plugin/app-server/remote-control/update/doctor/…） | 4 个入口（corvus / corvus-exec / corvus-mcp-server / 示例插件），无子命令树 | 🔴 |
| Headless exec | `--json`/`--output-schema`/`--ephemeral`/`--ignore-user-config` 等 | `--format json`/`--max-steps`/`--permission-mode`/stdin/退出码 0/1/2 | 🟢 |
| Review/Audit | `codex review` 子命令 | 无 CLI 面；有 in-process review_report + guardian | 🟠 |
| 会话管理 | resume/fork/archive/delete + SQLite | `-r`/`-c`/`--copy`；无 fork/archive/delete | 🟠 |
| OS 沙箱 | bwrap+Landlock / Seatbelt / AppContainer | bwrap / Seatbelt / Windows fail-closed | 🟡（Windows 🔴） |
| 网络策略 | **MITM 逐请求 allow/deny/ask** | hostname glob + bash 静态 URL deny | 🔴 |
| 权限审批 UX | untrusted/on-request/granular/never + `--approve-for-me` | manual/ask/auto/acceptEdits/dontAsk/plan/bypass | 🟢 |
| 命令级策略 | execpolicy `.rules` | 无 `.rules`；有 `--allowed-tools` | 🟠 |
| Hooks | Claude 兼容 + **HookTrustStatus 信任持久化** | Claude 兼容 + 项目 hook 禁代答（但无条件执行） | 🟢 |
| MCP 客户端 | 多来源 + auth + env id | 来源 + launch approval（sha256 身份 + 持久 grant） | 🟢 |
| MCP 服务端 | 全量工具 + elicitation | 默认只读 fail-closed，无 elicitation | 🟡 |
| 工具发现 | ToolSearch + WebSearch | 静态注册表 + `tool_search` 按需发现；`web_search` 本地引擎（searxng/brave/tavily） | 🟢 |
| 平台面 | app-server / remote-control / cloud / marketplace / 自更新 / doctor | 无 | 🔴（定位冲突，见 6.3） |
| CI/发布 | 30 个 workflow + 多平台矩阵 + release | 1 个 workflow，仅 ubuntu，无发布 | 🟠 |

### 6.2 Top 差距与取舍建议（已按「单机工具定位」过滤）

**已实现（2026-08-10 当日补上）**
- `web_search` / `tool_search` 工具发现：`tool_search` 作为 builtin 始终可用，按关键词检索当前注册工具（含 MCP 工具）；`web_search` 为本地引擎工具（`[web_search]` 配置：searxng 需 `base_url`，brave/tavily 需 `api_key`），对所有 provider 生效并受 `[network_policy]` 约束；配置本地引擎后自动抑制 provider 侧 server-side 开关，避免同名工具。新代码 `internal/tool/builtin/tool_search.go`、`internal/tool/builtin/web_search.go`、`internal/config/websearch.go`、`internal/boot/boot_websearch.go`（当日未提交改动；provider 侧开关 `internal/config/config.go:858-863` 仍保留）。

**值得追（性价比高）**
1. 网络策略「参数级」拦截（curl/wget/git clone/pip/npm 的 URL 参数 + ask 真正弹审批）——比 MITM 便宜得多，覆盖 80% 出网面；
2. `corvus review` CLI 子命令——复用现有 review_report/guardian，改动集中在入口层；
3. execpolicy `.rules` 兼容——复用 permission 规则解析，让项目策略可版本化；
4. `corvus doctor` 只读诊断——纯本地，成本极低。

**不该追（定位冲突/投入产出比低）**
- MITM 全量代理（证书/代理端口/QUIC 工程量大）；
- app-server / remote-control / cloud / exec-server（与「单机、无登录、无遥测」定位冲突）；
- 遥测/登录/多用户（「无账号」是差异化卖点）；
- 重型 marketplace（已兼容 Claude 插件/skill 格式）；
- Windows AppContainer 全量沙箱（视用户群而定）。

### 6.3 Corvus 相对 Codex 的优势（已核验）

- 中文/CJK 一等公民 + CNY 计价 + DeepSeek 等国产 provider 开箱即用（`internal/i18n/messages_zh.go`、`corvus.example.toml`）；
- 证据链交付门控：review_report 覆盖检查 + guardian 三向裁判 + recovery 失败预算（Codex 无对应物）；
- MCP launch approval 按 sha256 身份 + 持久 grant + 命令/版本 pinning（粒度优于 Codex 的项目级标记）；
- git-free checkpoint 快照回滚、并发写租约（workspacelease）、无登录无遥测。

---

## 7. 问题总表与修复路线图

### 7.1 问题总表

| 级别 | 问题 | 证据 | 状态 |
|---|---|---|---|
| P1 | 项目配置整体替换用户安全控制 | `internal/config/load.go:128-147` | 新发现 |
| P1 | 项目 hooks 无条件执行任意代码 | `internal/boot/boot_hooks.go:43`、`internal/hook/hook.go:1448` | 残留（禁代答已修） |
| P1 | MCP 审批被 `mcpSpec` 路径绕过 + TUI 无审批 UI + 授权跨 tab | `internal/control/mcp_manage.go:97-118`、`boot_plugins.go:190-198` | 残留 |
| P1 | bwrap 无 pid/ipc namespace，可窃取宿主环境/密钥 | `internal/sandbox/seatbelt_other.go:84-88` | 新发现 |
| P2 | corvus-exec 默认 auto + 项目自授规则叠加 | `cmd/corvus-exec/main.go:91` | 新发现 |
| P2 | corvus-mcp-server 信任项目权限规则 | `cmd/corvus-mcp-server/main.go:63-74` | 新发现 |
| P2 | async hooks / MCP fetch goroutine 无跟踪无取消 | hook、mcpserver | 新发现 |
| P2 | checkpoint/rewind 无跨进程锁 | `internal/checkpoint` | 新发现 |
| P2 | 三个 provider readStream 重复且缓冲上限分叉 | openai.go:913、anthropic.go:470、responses.go:372 | 新发现 |
| P2 | `parsePermissionMode` 三份实现、模式集合不一致 | cli.go:161、corvus-exec/main.go:286、mcp-server/main.go:196 | 新发现 |
| P2 | God file：agent.go 4,239 行 / controller.go 2,479 行；62 函数 cc>30 | gocyclo 实测 | 残留 |
| P2 | 复杂度「趋势门禁」不落盘，96 条基线无法对比 | `.golangci-agent-complexity.yml` + ci.yml | 残留 |
| P2 | checkpoint 测试比 0.30 / 覆盖 65.2%；安全裁决包 60–71% | 实测 | 残留 |
| P2 | 无 review CLI、无 execpolicy .rules、无 doctor | — | 差距 |
| ✅ | web_search / tool_search 已实现：本地引擎 searxng/brave/tavily + 始终可用的 tool_search | `internal/tool/builtin/tool_search.go`、`web_search.go`、`internal/config/websearch.go`、`internal/boot/boot_websearch.go` | 已实现 |
| P3 | README 称「一个可执行程序」实为 4 个；corvus-exec 零文档 | README.md:4 | 残留 |
| P3 | CI race 未覆盖 mcpserver/netpolicy/sandbox/permission/secrets；单 OS；无 govulncheck；无发布管线 | `.github/workflows/ci.yml:40` | 残留 |
| P3 | Makefile 无 lint/race 目标；无 tag/版本管理（version="dev"） | Makefile | 残留 |
| P3 | 表驱动率 6%；SSRF 网段判定重复；`corvus.example.toml` 覆盖度低 | 实测 | 残留 |
| P3 | `.corvus/config.toml` 仍含真实 API key | 已 gitignore，未泄露 | **建议立即轮换** |

### 7.2 建议修复顺序

1. **T1：项目配置信任门 / 用户级隔离**（阻塞性，覆盖三个入口，先做 `[permissions]`/`[sandbox]`/`[network]` 回滚）；
2. **T3：`mcpSpec` 审批化 + `/mcp` 面板「批准启动」动作 + 共享 host 校验**；
3. **T2：项目 hooks 指纹/提示 + headless 默认豁免**；
4. **S1：bwrap 补 pid/ipc/uts namespace + `--die-with-parent` + seccomp**；
5. **收敛三份 `readStream` + 三份 `parsePermissionMode`**（顺手消除模式集合不一致）；
6. **复杂度基线落盘（CI artifact）+ agent.go/controller.go/render.go 继续拆**；
7. **补 checkpoint 与安全裁决包测试**；CI race 扩展新包 + govulncheck + macOS/Windows build 矩阵；
8. **文档与卫生**：README 重写（3 个入口）、corvus-exec 文档、轮换 API key、删 spike-shimmer；
9. **按 2.3 清单搭 Terminal-Bench 风格自建任务集**，把「成功率/效率/安全/克制」变成可回归的门禁。

---

## 8. 结论

Corvus 的工程底子（门禁真实生效、测试纪律、错误处理、checkpoint 事务、MCP 身份授权）明显高于同类单机工具的平均水平，且同日早前的 4 个高危问题已全部真修。**当前最紧迫的不是功能，而是「信任边界的一致性」**：MCP 要审批、hook 不能代答，但项目配置却可以整体关掉权限引擎、项目 hook 仍可无条件执行代码、沙箱缺进程隔离——这三处把「防恶意仓库」的整条防线留了侧门。其次是架构债（God file、重复实现、复杂度门禁不落盘）与测试盲区（checkpoint/安全裁决包）。对 Codex CLI，单会话能力已基本对齐甚至局部领先，差距集中在「纵深与生态」（网络逐请求策略、review CLI、doctor/发布），其中大部分按单机定位可取舍或低成本补齐。

---

## 附录 A：证据速查

| 断言 | 证据位置 |
|---|---|
| 项目配置合并仅回滚 Secrets/Currency | `internal/config/load.go:125-150` |
| MCP 项目来源 UserAuthorized=true | `internal/config/config.go:1212-1220` |
| mcpSpec 不设 RequireLaunchApproval | `internal/control/mcp_manage.go:97-118` |
| boot 审批置位真实 | `internal/boot/boot_plugins.go:537,555-556` |
| 项目 hook 无条件加载 | `internal/boot/boot_hooks.go:43` |
| hook allow 代答被忽略 | `internal/hook/hook.go:1185-1195`、`runner.go:143-159` |
| bwrap 无 pid/ipc namespace | `internal/sandbox/seatbelt_other.go:84-88` |
| jobs 完成通知顺序 | `internal/jobs/jobs.go:510-522` |
| /compact 生命周期 | `internal/control/background_commands.go:7-24` |
| corvus-exec 默认 auto | `cmd/corvus-exec/main.go:91` |
| mcp-server 消费项目权限 | `cmd/corvus-mcp-server/main.go:63-74` |
| readStream 三份实现 | `internal/provider/openai/openai.go:913`、`anthropic/anthropic.go:470`、`responses/responses.go:372` |
| parsePermissionMode 三份实现 | `internal/cli/cli.go:161`、`cmd/corvus-exec/main.go:286`、`cmd/corvus-mcp-server/main.go:196`（`mapPermissionMode`） |
| web_search / tool_search 已实现 | `internal/tool/builtin/tool_search.go`、`internal/tool/builtin/web_search.go`、`internal/config/websearch.go`、`internal/boot/boot_websearch.go`、`corvus.example.toml` 的 `[web_search]` 段 |
| CI race 覆盖清单 | `.github/workflows/ci.yml:40` |
| README「一个可执行程序」 | `README.md:4`、`README.zh-CN.md:3` |

## 附录 B：子代理报告（可深入阅读）

- `tmp/research_eval_methods.md` —— 如何评估 agent：benchmark 全景、七维框架、产业实践、20 条落地清单
- `tmp/research_code_quality.md` —— 代码质量/架构/测试/工程卫生全量发现（426 行）
- `tmp/research_security.md` —— 安全模型与并发可靠性全量发现（468 行）
- `tmp/research_codex_compare.md` —— Codex CLI 对标矩阵、Top 差距、取舍建议（447 行）
