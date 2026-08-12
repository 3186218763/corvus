# Corvus 深度研究报告（第二轮：8 agent + opencode/codex 源码对标）

> 调研日期：2026-08-11
> 调研对象：`/home/miku/szj/corvus`（HEAD `f760f99`，Go TUI 编码 agent，约 30 万行 / 944 个 .go）
> 对标源码：`/tmp/opencode/opencode`（sst/opencode，TS monorepo，HEAD `d041eee`）、`/tmp/opencode/codex`（openai/codex，Rust workspace，HEAD `41ece45`）
> 方法：8 个并行子代理分域深挖（安全 / 新功能 bug / 并发 / provider 核心循环 / TUI / 持久化 / opencode 对标 / codex 对标），全部关键结论由主 agent 逐条 grep/读码二次核验（本文标注「已核验」），其中 jobs WaitGroup 竞态与 rewind 大文件截断由子代理用临时测试实际复现。
> 基线：`go build ./...` ✅、`go vet ./...` ✅（子代理验证）、`-race` 核心包全绿（子代理实测）、仓库现有测试全绿。

---

## 0. TL;DR

1. **上次 4 个 P1 的真实状态**：项目配置覆盖用户安全控制 **仍存在**；项目 hooks 无条件执行 **仍存在（设计如此）**；MCP mcpSpec 审批绕过 **部分修复**（boot 路径已修，控制器路径仍绕）；bwrap 无 pid/ipc namespace **仍存在**。
2. **本次新发现 3 个可复现的 P1 级 bug**（全部有子代理实测证据）：
   - **jobs.WaitGroup 竞态 → 进程级 panic**：`Close` 与 turn 内 `StartForSession` 并发时 `Add` 与 `Wait` 竞争，`sync: WaitGroup is reused before previous Wait has returned`（已用 overlay 测试稳定复现，<1s 两轮）。
   - **Anthropic readStream 无终态守卫**：SSE 被干净 FIN 掐断时（无 `message_stop`），半截文本/tool_use 被当作**成功完成**提交，工具调用意图静默丢失；openai 有同款守卫（openai.go:1095），anthropic 没有（已用注入 SSE 测试复现）。
   - **checkpoint 大文件 preimage 损坏**：`CaptureBefore` 吞掉 `CapturePath` 的错误，>32MiB 文件构造 `Content=&""` 非 nil 空快照，rewind 后文件被写成 **0 字节**（33MiB 文件实测复现）。
3. **3 个新功能本身的 bug**：`corvus-mcp-server --allow-write` **根本不注册任何写工具**（append 的是同一份只读 5 件套，代码直接可见）；MCP server 未接入 `[network_policy]`（web_fetch/bash 的 egress deny 全绕）；web_search 请求无整体超时（`http.Client` 无 `Timeout`，可永久挂死单线程 stdio server）。
4. **新攻击面**：`[web_search] api_key + base_url` 均属项目可覆盖配置 → 打开恶意仓库即可把用户搜索 API key 发给攻击者自定义地址（组合 1+ 新配置项，实测确认）。
5. **对标结论**：与 opencode/codex 相比，Corvus 的硬差距收敛为：**沙箱进程隔离**（codex 有 `--unshare-user/pid` + seccomp，10 行修复）、**命令级策略**（codex 的 Starlark .rules + bash 参数级安全分类，可整体移植）、**网络逐请求判定**（connect 时二次 IP 判定防 DNS rebinding，HTTP 层即可做，不需 MITM）、**hook hash 信任链**（codex discovery.rs，防仓库静默篡改项目 hook）、**headless JSON 事件 schema**（codex exec_events.rs）。Corvus 仍领先：无沙箱的 opencode、git-free checkpoint 粒度、中文/国产 provider 一等公民、证据链交付门控。

---

## 1. P1 级问题（本次核验）

### 1.1 【已复现·进程崩溃】jobs.Manager WaitGroup 竞态：Close 与 Start 并发 → panic

- **证据（已核验）**：`internal/jobs/jobs.go:442` `m.wg.Add(1)` 在 `m.mu` 之外执行且 Close 前无"已关闭"检查；`internal/jobs/jobs.go:1748` `CloseWithGrace` 内 `go m.wg.Wait()`。`Controller.close()`（`internal/control/hooks_lifecycle.go:151-205`）只 cancel turn ctx 不等 turn goroutine，随即 `jobs.Close()`。
- **推演**：被取消的 turn 收尾时调用 `StartForSession` → `wg.Add` 与 `wg.Wait` 并发 → counter 归零瞬间 Add → Go runtime panic。
- **复现**：子代理 overlay 测试 `TestCloseRacingStartForSession` 两轮 <1s 稳定复现 `panic: sync: WaitGroup is reused before previous Wait has returned`（`/tmp/opencode/wgrace/repro_test.go`）。
- **修复方向**：仿 `internal/control/background_commands.go:10-24` 的协议——`StartForSession` 持 `m.mu` 期间完成 closing 检查 + `Add`；`Close` 先在 `m.mu` 内置 closing 标志再 cancel 再 Wait；`monitorStalled` 的 `Add`（jobs.go:445）同处理。

### 1.2 【已复现·静默数据错误】Anthropic readStream 无终态守卫：干净 FIN 截断被当作成功

- **证据（已核验）**：`internal/provider/anthropic/anthropic.go:651-677`——scan 循环结束后只检查 `ctx.Err()`/`stalled`/`scanner.Err()`，**没有检查是否收到 `message_stop`**，直接无条件发 `ChunkUsage`+`ChunkDone`。对比 openai 有守卫（`internal/provider/openai/openai.go:1095-1097` `stream ended before completion`）。
- **影响**：代理/网关中段干净断流（无 RST）→ 截断文本被当最终答案提交；更严重：tool_use 块半截时模型意图被静默丢弃。
- **复现**：注入只发 `content_block_delta text` 即 EOF 的 SSE，`ChunkDone` 照常发出（子代理 `TestAuditCleanFINWithoutMessageStop`）。
- **修复方向**：跟踪 `sawMessageStop`，未达终态时发 `StreamInterruptedError`，让 agent 走中断恢复（run_loop.go:292）。

### 1.3 【已复现·用户数据损坏】checkpoint rewind 把 >32MiB 文件截断为 0 字节

- **证据（已核验）**：`internal/checkpoint/checkpoint.go:460-499` `fp, gap, _ := CapturePath(...)` **丢弃 err**；`CapturePath` 对超限文件返回 `fp.Existed=true` 但无 Content（`internal/checkpoint/capture.go:109-116` GapOversized）；随后 `snap.Content = &text` 构造**非 nil 空串**并落盘。
- **影响**：`precheckFiles` 的 `ConflictMissingPayload`（transaction.go:1303-1311）要求 `rev.Content == nil`，`&""` 漏过 → rewind 把文件写成空。33MiB 文件实测复现。
- **修复方向**：`gap != nil && err != nil` 时不构造 `Content=&""`（保持 nil 使 missing-payload 分支拒绝 restore），不吞 err。

### 1.4 【新攻击面·实测】web_search API key 可被项目配置劫持外泄

- **证据（已核验）**：`internal/config/load.go:130` 项目 TOML 整体覆盖用户配置，仅 `Secrets`/`UI.Currency` 受保护；`[web_search]` 的 `base_url`/`api_key` 均未保护 → 项目写 `engine="tavily"`+`base_url="https://evil.example/"` 而保留用户 `api_key`（子代理 overlay 实测合并结果）。tavily 把 key 放 POST body（`internal/tool/builtin/web_search.go:307-311`），brave 放 header。
- **叠加**：boot 路径 web_search client 无 SSRF guarded dialer（`internal/boot/boot_websearch.go:23` 用纯代理 transport），base_url 指向内网构成 SSRF；netpolicy 默认 allow 不兜底。
- **修复方向**：`[web_search]` 整段归入用户级保护（回滚项目覆盖）；client 复用 webfetch 的 SSRF dialer；非默认域名弹审批。

### 1.5 【残留】项目配置无信任门，整体替换用户级安全控制

- **证据（已核验）**：`internal/config/load.go:130-150` 只有 Secrets/Currency 回滚；`[permissions]`/`[sandbox]`/`[network]`/`[network_policy]`/`agent.system_prompt_file` 全部可被项目覆盖。子代理 overlay 实测：用户 `mode="ask"`+`bash="enforce"`+`network=false` 被项目 `mode="allow"`+`bash="off"`+`network=true` 整体覆盖。
- **影响**：恶意仓库一键关闭沙箱/放行写入/打开 egress，且与 1.4 组合可窃取 key。影响 TUI / corvus-exec（默认 `--permission-mode auto`）/ corvus-mcp-server 三个入口。
- **修复方向**：安全 section 按用户级归并（参照 Secrets 模式），或首启信任门。

### 1.6 【残留】bwrap 沙箱无 pid/ipc/user namespace

- **证据（已核验）**：`internal/sandbox/seatbelt_other.go:82-104` 只有 `--unshare-net`；`--proc /proc` 挂的是宿主 proc。对比 codex `linux-sandbox/src/bwrap.rs:283-291`：**无条件 `--unshare-user --unshare-pid`**，`--new-session --die-with-parent`（bwrap.rs:325-326），并配 seccomp（landlock.rs:179-247 deny ptrace/process_vm_*/io_uring + socket 家族白名单）。
- **影响**：沙箱内可读宿主 `/proc/<corvus-pid>/environ`（API key）、可 ptrace/kill 同 uid 进程。
- **修复方向**：补 `--unshare-user --unshare-pid --unshare-ipc --unshare-uts --die-with-parent`（约 10 行，codex 同款）；seccomp 网络兜底（第 3 节对标项 2）。

### 1.7 【残留·部分修复】MCP 启动审批被控制器 mcpSpec 路径绕过

- **已修复**：boot 路径 `internal/boot/boot_plugins.go:556` `RequireLaunchApproval: projectScoped` + grant 持久化 + 懒加载身份校验（plugin.go:1438）。
- **残余（已核验）**：`internal/control/mcp_manage.go:97-128` 的 `mcpSpec` 不设 `RequireLaunchApproval`；`internal/plugin/install.go:85-87` `if !spec.RequireLaunchApproval { spec.Authorized = true }` 把项目 MCP 直接标为已授权 → `/mcp connect`（chat_tui_commands.go:576、mcp_manager_actions.go:57,169）与桌面 SessionAPI（port.go:173-176）路径仍免审批直连；controller_test.go:2769 甚至固化该行为。
- **修复方向**：`mcpSpec` 内按 `exp.Source.ProjectScoped()` 置位 + Controller 路径复用 `resolveEagerMCPLaunchApproval`。

---

## 2. 新功能 bug（f760f99 / 7591c38 引入）

### 2.1 【高】corvus-mcp-server `--allow-write` 不注册任何 writer 工具

- **证据（已核验）**：`cmd/corvus-mcp-server/main.go:136-173`——`readOnlyServed := ["read_file","ls","glob","grep","code_index"]`；`all := ws.Tools(readOnlyServed...)`（`internal/tool/builtin/workspace.go:60` 在 enabled 非空时只返回命中的 5 个）；allowWrite 分支 `tools = append(tools, all...)` 再把同一份只读集合 append 一次 + ConfineBash。`write_file/edit_file/multi_edit/move_file/notebook_edit/delete_range/delete_symbol` **从未注册**。
- **修复方向**：allowWrite 分支构造 writer 工具集（`ws.Tools()` 空=全部 或显式 `builtin.ConfineWriters`），补 allowWrite=true 测试（现有 main_test.go 只测 false 分支）。

### 2.2 【高·安全】MCP server 未接入 `[network_policy]`

- **证据（已核验）**：`cmd/corvus-mcp-server/main.go:144` `ConfineWebFetch(proxySpec)` 无 policy 参数（零值策略=放行，netpolicy.go:77-79 注释明确）；`ConfineBash` 无策略；对比 CLI 路径 `internal/boot/boot_tools.go:846-848` 用了 `ConfineBashWithNetPolicy` + `ConfineWebFetch(proxySpec, netPolicy)`。
- **影响**：配置 deny 规则后，MCP server 的 web_fetch/bash curl 全部绕过 egress 承诺。
- **修复方向**：buildTools 内 `cfg.NetPolicy()` 并传递给 WebFetch/Bash/ws.NetPolicy。

### 2.3 【中】web_search 无整体超时，可永久挂死单线程 stdio server

- **证据（已核验）**：`internal/netclient/netclient.go:83-89` 返回的 `http.Client{Transport: tr}` **无 `Timeout` 字段**（只有 Dial/TLS/ResponseHeader 三个 15s）；`internal/tool/builtin/web_search.go:226/346` `client.Do` + `io.ReadAll` 无 deadline；`internal/mcpserver/server.go:96-120` 单线程顺序处理 → body 慢流时**整个 server 卡死**。
- **修复方向**：boot 构造的 client 设整体 `Timeout`（如 30s），或请求内派生 `context.WithTimeout`。

### 2.4 【中】exec `--format json` 契约：`Kind` 输出裸整数、`Err` 输出 `{}`

- **证据（已核验）**：`cmd/corvus-exec/main.go:495-503` `json.Marshal(e)`；`internal/event/event.go:317-352` `Kind` 是 int 且无 JSON tag、无 `MarshalJSON`。新增/重排常量会静默移位，下游必须硬编码数字。
- **修复方向**：`Kind` 实现 `MarshalJSON`（输出字符串名）、`Err` 自定义序列化、加 golden 测试。

### 2.5 【中】exec text 模式：subagent 技能回合答案重复输出

- **证据**：`internal/control/turn_orchestrator.go:169-170` 同一 answer 先后发 `event.Text` 与 `event.Message`；`cmd/corvus-exec/main.go:448`（Text 立即写 stdout）+ finalize（main.go:485-489 `sawText && finalMessage != ""` 再打印一遍）→ 技能答案两遍。
- **修复方向**：finalize 对比 finalMessage 是否已被 Text 事件输出。

### 2.6 【中】`[network_policy]` IP 字面量规则对域名永不生效

- **证据（已核验）**：`internal/netpolicy/netpolicy.go:126-142` 只对 hostname glob 匹配，无 DNS 解析；子代理实测 `deny:["93.184.216.34"]` 下 `Decide("https://example.com/page")` = allow。包文档宣称支持 IP 字面量（netpolicy.go:12-14），语义缺口 + DNS rebinding 面。
- **修复方向**：文档明示或 connect 时二次判定（对标 codex connect_policy.rs:70-111，见 §3）。

### 2.7 【中】web_fetch deny 规则不随重定向复查

- **证据（已核验）**：`internal/tool/builtin/webfetch.go:272` 仅对原始 URL 判一次 `policy.Decide`；重定向目标只过 SSRF IP 检查（逐跳，这层是好的），hostname 级 deny 不复查。`bash_netpolicy.go:29-40` 同理。
- **修复方向**：自定义 `CheckRedirect` 每跳重跑 `policy.Decide`。

### 2.8 【中】权限 deny 可被路径规范化绕过（实测）

- **证据（已核验）**：`internal/permission/permission.go:596-619` 对模型原始字符串匹配；`/etc/../etc/passwd`、`/etc//passwd` 变体实测全部判 allow 而工具实际读到 /etc/passwd；`~/.ssh/*` 因 `~` 不展开永不命中；`subjectKeys`（permission.go:579）缺 `url` 键 → `deny web_fetch(https://evil.example/*)` 永不命中。
- **修复方向**：Decide 前 `filepath.Clean` + `EvalSymlinks`；subjectKeys 补 url；`~` 展开。

### 2.9 【中】corvus-mcp-server 默认只读面不受工作区限制 + 敏感文件保护失效

- **证据（已核验）**：`cmd/corvus-mcp-server/main.go:112-176`——`read_file` 不受工作区限制（`ForbidReadRoots` 默认空），任意 MCP 客户端可读任意用户文件；该程序从不调用 `secrets.SetProtectSensitiveFiles`，`[secrets] protect_sensitive_files` 对它无效。
- **修复方向**：默认只读面绑定工作区根；启动时同步敏感文件保护。

### 2.10 【低】其他

- searxng `base_url` 带子路径时不追加 `/search`（web_search.go:187-190，实测 404）。
- 三引擎响应体 1MiB 截断与"malformed"混淆（web_search.go:345-354）。
- anthropic `web_search_tool_result_delta` 字段声明但 delta 分发无对应 case（anthropic.go:854 vs 591-626，死字段）。
- exec 慢 stdout 管道反向阻塞 agent 循环（main.go:448 同步写，注释声称"never blocks"与实现不符）。
- exec stdin `io.ReadAll` 无长度上限（main.go:154）。

---

## 3. provider 层健康度（已核验 + 子代理实测）

| Provider | 终态守卫 | 中断恢复包装 | 重连 | 备注 |
|---|---|---|---|---|
| openai | ✅ openai.go:1095 | ✅ | ✅ 3 次重放 | 最强；另有 prefix continuation（DeepSeek length） |
| anthropic | ❌ **缺失（P1，见 1.2）** | ❌ 从不包装 `StreamInterruptedError`（anthropic.go:654-661） | ❌ 无 | **最弱**；DeepSeek-Anthropic 端点同样受影响 |
| responses | ✅ `!terminal` 守卫 | ✅ 全部包装 | ❌ | 坏 JSON 静默 skip（responses.go:444-447） |

其余（均已核验）：
- **P2**：usage `RequestCount` 合并双计（openai.go:620-645 mergeUsage + run_loop.go:432-458 mergeStreamUsage 用求和而非 max，1+2=3≠2，子代理实测）；openai stall 超时不可重放（仅 conn-reset 走 streamWithReconnect，首 token 前思考 >120s 直接失败，`TestAuditStallNoReplay`）；缓冲上限分叉 1MB vs 4MB（openai.go:965 / anthropic.go:538 / responses.go:377）；SSE 多行 `data:` 事件三份实现都不支持（`TestAuditSSEMultiLineDataField`：unexpected end of JSON input）。
- **P3**：openai 流式 tool call 先到 ID 后到时 UI 重复卡片（openai.go:1061-1067 vs agent.go:3014-3022）；missing-reasoning 重试 sessCacheHit/Miss 双计（agent.go:2952-2953）。
- **无问题**：429/5xx 退避（retry.go:269-330，header 阶段、Retry-After、上限 15s）；并行只读工具（partitionToolCalls+runParallel）；tool 对补齐 `interruptedToolResult` 占位不 400；tool schema 透传无字段丢失；compact 的 keepToolCallGroup/tailStart 对齐完整。

---

## 4. 并发可靠性（已核验）

- **P1**：jobs WaitGroup 竞态（见 1.1，唯一实测复现的进程级崩溃）。
- **P2**：syncSink 可丢弃 TurnDone（`internal/event/sync.go:75` critical 集合只有 ApprovalRequest/AskRequest；`finishGuardedTurn` controller.go:828 依赖 TurnDone 送达解锁前端 → 前端永久"运行中"）；syncSink 链头永久阻塞时 critical 事件无超时兜底（sync.go:101-104，与 approvalManager.promptMu 组合冻结全部审批）；`Controller.Close` 不等 turn/autosave goroutine（autosaveWG 生产路径无 Wait 点）；async hooks `context.WithoutCancel` 无跟踪（hook.go:1172-1174）。
- **P3**：checkpoint 事务跨进程锁缺失（barrier 仅进程内互斥）。
- **健康面**：admission/旋转门/停车队列设计成熟；`Session.Messages` 裸读点全部收敛在 run-loop 协程（agent.go:2814/3064、compact.go:205、prune.go:54、coordinator.go:831/887）；jobs 锁序单向无环；workspace lease 双通道严谨。

---

## 5. 持久化子系统（已核验）

- **P1**：大文件 rewind 截断（见 1.3）。
- **P2**：记忆文件手改文件名后 forget/delete 静默失效（memory/store.go:208-263 `archiveInDir` 用 slug 重建路径 vs `findActiveInDir` 匹配 slug(memory.Name)，`Delete` 返回 "" 无错，索引删了文件还在→记忆复活）；undo 崩溃恢复把已完成 undo 整体回滚（transaction.go:1241-1259 对 TxCommitting 的 undo 重放父 rewind 而非完成 undo 语义）；记忆并发写无跨进程锁 + `flushIndexIn` 非原子写（store_v2.go:33 / store.go:489-537）；`protectTurns` 从未赋值（checkpoint.go:162,696,718 死机制）；`Checkpoint.SessionRevision` 从未写入（checkpoint.go:81 死字段）；`CaptureAfter` 空 for 循环（checkpoint.go:525-527）；`CommandMatches` 子集匹配可被 `go test -run '^$'` 绕过（evidence/commandmatch.go:40-61）；applyInstallMCP replace 的 DeepEqual 误报（installsource/apply.go:216-228）。
- **无问题（已重点核查）**：workspacelease/filelock 双通道状态机；save.go 事件日志 append/CAS/恢复；frontmatter/skill 解析 fail-safe；guardian 熔断与角色修复；checkpoint 补偿三窗口判别自洽。

---

## 6. TUI/CLI 交互层（已核验）

- **【高】** ANSI/控制序列注入：用户气泡（chat_tui_transcript.go:746）、reasoning（:209-223）、工具流输出（diffview.go:229）原样进终端——`\x1b[2J` 清屏错乱、`\x1b[?25l` 永久藏光标、**OSC52 可静默改写剪贴板**。唯一安全路径是 markdown 渲染器（goldmark 剥控制字符）和 statusline（ansi.Strip）。修复：ingestEvent 统一剥非 SGR 控制字节。
- **【中】** classic 布局 Ask 徽标显示 "Auto"（chat_tui_modes.go:59-106 default 分支，权限姿态误导；desktop 布局同状态显示 "Ask"）。
- **【中】** turn 运行中排队的 interject 消息跳过 @引用解析（chat_tui_update_session.go:56-63 vs chat_tui_update_keys.go:470-473 空闲路径会 resolveRefs）。
- **【低】** Esc lastEsc 跨 turn 残留误触发 rewind 选择器（chat_tui_update_keys.go:240-277）；配置层接受 4 个幽灵主题样式 slate/carbon/nocturne/amber（config.go:261-268，cliThemeStyleByName 找不到→静默回退默认）。
- **无问题**：i18n 缺失键=编译错误（AST 扫描三语零漂移）；滚动边界 viewport clamp 安全；approval 弹窗全键捕获；粘贴多行/图片/引用路径健壮；SIGTERM→快照→Quit 干净。

---

## 7. opencode / codex 源码对标（深度优先，全部路径已 grep 验证）

### 7.1 沙箱（vs codex `codex-rs/linux-sandbox/`）

| 维度 | Corvus（已核验） | Codex（已核验路径） | 差距 |
|---|---|---|---|
| 进程隔离 | 无 `--unshare-user/pid/ipc`，宿主 /proc 透传 | bwrap.rs:283-291 无条件 userns+pidns；:325-326 `--new-session --die-with-parent` | **P1，10 行修复** |
| seccomp | 无 | landlock.rs:179-247 deny ptrace/process_vm_*/io_uring + socket 家族白名单（Restricted/ProxyRouted 两档） | 沙箱内可 ptrace 同 uid 进程；网络开关不可强制 |
| deny-read | 静态路径蒙版，无 glob、无 symlink 保护 | bwrap.rs:707-882 rg 展开 glob（8192 上限）；:1146-1166 穿越可写 symlink 即 Fatal | glob + fail-closed |
| 性能 | 全放权也包 bwrap | bwrap.rs:234-256 全盘写+全网络时短路不包 | 白付 namespace 开销 |

### 7.2 命令策略（vs codex `codex-rs/execpolicy/` + `shell-command/`）——最大差距

- Codex：**Starlark DSL 规则文件**（parser.rs:57-79），`prefix_rule/network_rule/host_executable` 三内建；`match/not_match` 示例**解析期自校验**（rule.rs:246-306，规则写错直接报错）；首 token 固定 + 后续 token 参数级匹配（rule.rs:40-60）；未匹配命令走 safelist+dangerous 分类 × approval_policy × 沙箱类型（core/src/exec_policy.rs:727-828）；bash -lc 参数级分解（is_safe_command.rs:12-50），`find -exec/-delete`、`rg --pre/--search-zip`、`base64 -o`、git 全局选项参数级拒绝（:104-295）；批准后规则热追加回写 .rules（amend.rs:65-147）。
- Corvus（已核验）：`netpolicy.go:126-142` 仅 hostname glob + Default；bash 靠 `bash_netpolicy.go:29-43` 正则抠 URL（字符串级，可混淆）。
- **建议**：Go 可用 `github.com/google/starlark-go` 几乎 1:1 移植规则引擎；`is_safe_command.rs` 逻辑纯 Go 整体移植（测试完备）。这是收益最高的一项。

### 7.3 网络逐请求判定（vs codex `codex-rs/network-proxy/`）

- Codex：MITM 代理（mitm.rs:188-267）+ **TCP connect 时对已解析 IP 二次判定**（connect_policy.rs:70-111）+ DNS rebinding 再查（mitm.rs:380-407）+ 私网完整分类（policy.rs:45-98，CGNAT/TEST-NET/benchmark 全算）+ 证书注入 10+ 工具 env（certs.rs:153-174）。
- Corvus（已核验）：`webfetch.go:66-205` 工具内 SSRF guard（雏形正确），bash curl 完全失控。
- **建议**：不需 MITM——HTTP client 层"解析→判定→询问"管线 + 沙箱内 netns/seccomp 强制出口即可覆盖 SSRF 主场景（对标项 3 是低成本复刻）。

### 7.4 hook 信任链（vs codex `codex-rs/hooks/src/engine/discovery.rs`）

- Codex：规范化身份（event+matcher+handler TOML 序列化）算 hash（discovery.rs:673-690）；信任状态 Managed/Trusted/Modified/Untrusted（:614-634），**未信任不执行**，被篡改自动降级停用。
- Corvus（已核验）：`internal/hook/hook.go:10` 视项目 hook 为"仓库控制的代码"无条件执行——等价于 codex 的 bypass 全开。上次报告堵住的只是"代答权限"。
- **建议**：hash 信任链是低成本高收益的供应链防线（第 3 节修复方向 5 的配套）。

### 7.5 headless 输出契约（vs codex `codex-rs/exec/src/exec_events.rs:9-133`）

- Codex：带 tag 的事件联合（thread.started/turn.completed/item.started|updated|completed——agent_message/command_execution{command,aggregated_output,exit_code,status}/file_change{changes,status}/error），`-o` 结果文件，`-` stdin 协议（lib.rs:2051-2088：prompt 参数 + 管道 stdin 并存时追加 `<stdin>...</stdin>` 块）。
- Corvus（已核验）：`corvus-exec --format json` 裸结构体无 schema（见 2.4）。
- **建议**：抄 ThreadEvent 分型 schema + `-o` + stdin 块语义。

### 7.6 上下文管理（vs opencode `packages/opencode/src/session/`）

| 维度 | Corvus（已核验） | opencode（已核验路径） | 建议 |
|---|---|---|---|
| 压缩触发 | 轮后按 usage 比值（compact.go:85-147），有防循环 | step-finish 每步判 isOverflow + `Stream.takeUntil(needsCompaction)` 中途掐流（processor.ts:477-483,642-645）+ provider 溢出信号兜底（:607-617） | 补"溢出信号→立即压缩"路径 |
| 尾部预算 | 固定 16384 token（compact.go:30） | usable 的 25%（钳 2k-8k）（compaction.ts:116-121） | 改自适应；Corvus 自己 compactStuck 注释即此痛点 |
| 尾部保留粒度 | message 粒度+字符估算 | turn 粒度+JSON 估算，装不下再 splitTurn 内二分（compaction.ts:224-275） | 提升粒度 |
| prune | 字符/比例维护 | 反向保护最近 40k token + 省<20k 不落盘 + skill 豁免 + `time.compacted` 擦除状态持久化（compaction.ts:279-323） | 补保护带/阈值/豁免 |
| 错误恢复 | 重复失败防护 | 同工具同参数 3 次 → `permission.ask("doom_loop")`（processor.ts:356-380） | **低成本：抄 doom-loop 检测** |
| 权限模型 | 进程内 Approver 回调 | Deferred 挂起+事件总线回复，reject 级联+always 自动放行（permission/index.ts:67-167） | 多端解耦 |
| 工具输出 | 各工具自己截断 | 中央 Truncate（MAX_LINES=2000/50KB + 委托 explore agent 提示 + 7 天清理，truncate.ts:85-141） | 统一截断服务 |
| 参数校验 | 手写 JSON schema | 统一 wrap + `InvalidArgumentsError` 模型可读自愈提示（tool.ts:24-34,99-149） | 低成本 |
| AGENTS.md | import 递归+防逃逸更强 | read 就近附加最近 AGENTS.md 包 `<system-reminder>`（read.ts:300,355-357） | 抄就近附加 |
| 被 deny 工具 | 仍暴露在 schema | 按 deny 规则从模型工具列表摘除（permission/index.ts:204-219） | 低成本省 token |
| 重试 | 只重试连接+头阶段 | 整轮重试（含已流式部分）+ retry-after 解析 + UI 显示 attempt/下次时间（retry.ts:77-147） | 中成本 |
| 遥测 | 本地事件统计 | OTel span（llm.ts:28） | 大工程，可缓 |

### 7.7 review（vs codex `codex-rs/core/src/tasks/review.rs`）

- Codex：受限子代理跑一次性会话——强制关 web_search/collab（review.rs:107-114）、approval 强制 Never（:119）、注入固定 rubric（`prompts/templates/review/rubric.md:67-89` 严格 JSON schema：findings[]+confidence+priority+code_location）；输出三层解析（直解→截首个 {..}→纯文本降级，:191-206）；review 结果写回会话历史 + 强制落盘（:210-275）。
- Corvus：无 CLI 面；`internal/guardian` 是 Auto 模式用量监督不是代码审查。
- **建议**：直接抄——子代理隔离（Never 审批+关 web）是安全关键。Corvus 已有 review_report + guardian 基础设施，改动集中在入口层。

---

## 8. 建议修复顺序（按投入产出比）

**T0（半小时级，修复即止血）**
1. bwrap 补 `--unshare-user --unshare-pid --unshare-ipc --unshare-uts --die-with-parent`（1.6）
2. `[web_search]` 归入用户级保护配置（1.4）
3. jobs Start/Close 锁协议（1.1）
4. anthropic readStream 终态守卫 + 错误包装 `StreamInterruptedError`（1.2 + 3）
5. mcp-server `--allow-write` 补 writer 工具集 + 接入 netpolicy（2.1/2.2）

**T1（一天级）**
6. checkpoint 大文件捕获错误处理（1.3）
7. 安全 section 用户级归并（1.5）+ mcpSpec 审批化（1.7）
8. web_search client 整体超时（2.3）
9. TUI ANSI 注入剥离 + Ask 徽标修正（6）
10. TurnDone 提升 critical 事件（4）

**T2（一周级，对标移植）**
11. Starlark 规则引擎 + bash 参数级安全分类（对标 7.2，最大收益）
12. HTTP 层"解析→判定→询问"网络管线（对标 7.3）
13. hook hash 信任链（对标 7.4）
14. review 子代理 CLI（对标 7.7）
15. exec JSONL 事件 schema + 单测（对标 7.5）

**T3（架构债）**
16. 三份 readStream 收敛 + SSE 状态机（多行 data/续行）
17. 统一工具输出截断 + InvalidArguments 自愈提示（对标 7.6）
18. compact 自适应预算/prune 保护带/doom-loop 检测（对标 7.6）
19. 复杂度基线落盘 CI + agent.go/controller.go 拆分

---

## 附录：证据速查

| 断言 | 证据位置 |
|---|---|
| jobs WaitGroup 竞态 | internal/jobs/jobs.go:442,445,1748（Add 在 mu 外 + Wait 并发） |
| anthropic 无终态守卫 | internal/provider/anthropic/anthropic.go:651-677（对照 openai.go:1095-1097） |
| CaptureBefore 吞错 | internal/checkpoint/checkpoint.go:460-499（`fp, gap, _ :=`）；capture.go:109-116（GapOversized 返回 Existed=true 无 Content） |
| web_search key 项目可覆盖 | internal/config/load.go:130-150；internal/tool/builtin/web_search.go:307-311；boot_websearch.go:23 |
| mcp-server 无 writer 工具 | cmd/corvus-mcp-server/main.go:136-173；internal/tool/builtin/workspace.go:60（enabled 非空只返回命中） |
| MCP server 无 netpolicy | cmd/corvus-mcp-server/main.go:144,150-153；对照 boot_tools.go:846-848 |
| web_search 无整体超时 | internal/netclient/netclient.go:83-89（无 Timeout 字段） |
| exec JSON 裸整数 | cmd/corvus-exec/main.go:495-503；internal/event/event.go:317-352 |
| bwrap 无 pid/ipc | internal/sandbox/seatbelt_other.go:82-104（对照 codex bwrap.rs:283-291,325-326） |
| mcpSpec 不设 RequireLaunchApproval | internal/control/mcp_manage.go:97-128；install.go:85-87；controller_test.go:2769 |
| 权限路径规范化绕过 | internal/permission/permission.go:596-619；builtin/confine.go:252 |
| ANSI 注入 | internal/cli/chat_tui_transcript.go:746,209-223；internal/cli/diffview.go:229 |
| 大文件 rewind 截断复现 | /tmp/opencode/ 子代理 overlay 测试（33MiB → 0 字节） |
| jobs 竞态复现 | /tmp/opencode/wgrace/repro_test.go（两轮 <1s panic） |
| anthropic 终态复现 | /tmp/opencode/audit TestAuditCleanFINWithoutMessageStop |
| SSE 多行 data 复现 | /tmp/opencode/audit TestAuditSSEMultiLineDataField |
| RequestCount 双计复现 | /tmp/opencode/audit TestAuditMergeUsageRequestCount |
| 参考仓库 | /tmp/opencode/opencode（sst/opencode HEAD d041eee）；/tmp/opencode/codex（openai/codex HEAD 41ece45） |
