# DeepSeek Harness 阅读笔记：对 Corvus 的启发

- Date: 2026-08-16
- Source: https://github.com/deepseek-ai/deepseek-harness.git (read-only clone at /tmp/deepseek-harness)
- Status: 调研笔记（非 ADR）

## 结论

deepseek-harness（`dsh`）是 DeepSeek 的开源 agent harness：TypeScript + Cordis，
"everything is a plugin"。与 Corvus（Go 单静态二进制）的对比结论：

- Corvus 强在**工程硬化**：ADR 驱动的正确性工作（重放限额、耐久性分层、统一 egress/SSRF、
  宽度权威、filelock 统一）。
- dsh 强在**可组合架构 + 契约纪律**：能力接缝、事件分类、生成式文档。
- 值得搬的是具体机制，不是插件化理念本身——Go 单二进制里照搬 Cordis 不现实。

## 最值得借鉴的 5 个点

### 1. Spill seam（工具输出溢出落盘）

- 现状：`internal/agent/agent.go:4134` 的 `truncateToolOutput` 固定字节截断 + 提示
  "rerun with narrower args"，中段内容直接丢失。
- dsh 做法：`tools/post-execute` 策略把超预算文本存成 session 私有文件，给模型返回
  locator + 检索指引（如"用 grep 读这个文件"），全文随时可取回。
- 参考：`docs/subsystems/spill.md`（dsh 仓库）。
- 收益场景：长 `web_fetch`、大日志、`code_index` 全量扫描。改动面小，收益直接。

### 2. 并行工具调用的分类器契约

- 现状：Corvus 整批并行只发生在连续 `ReadOnly` 工具上，`maxParallel` 硬编码 8
  （`internal/agent/agent.go:3360` 的 `partitionToolCalls`）。
- dsh 做法：一元 `isConcurrencySafe(args)` 分类器——纯函数、只看自己的参数、`true`
  才入并行池；exclusive 调用成为屏障分组；结果按模型顺序经提交游标写回 log；
  pre/post 中间件保持串行。
- 参考：`.agents/notes/implemented/feature/2026-07-10-parallel-tool-call-execution.md`（dsh 仓库）。
- 对 Corvus 的落地：加 `ConcurrencySafe(args) bool` 可选接口，让 `editfile` 这类工具
  按参数声明安全（如不同路径），而不是一刀切全串行。

### 3. 生成式工具目录 + 事件地图

- dsh 的 `docs/tool-catalog.md` 是真实 boot 插件树、读 `ctx.tools.schemas()` 生成的，
  每行记录模型可见名、schema、依赖 seam、副作用；CI 有 completeness guard，新工具无法漏登记。
- 另有 event-producer-consumer.md（每个事件的 producer/consumer）和 persistence-catalog。
- Corvus 现状：60+ 内置工具、40+ 事件 Kind，全靠散落的包注释，没有目录。
- 建议：加等价生成器 + 事件 producer/consumer 地图。

### 4. "Model-visible means logged" 运行时不变量 + log-only 事件类别

- Corvus 的 session 已是 append/replace 事件日志，但没有断言"推导出的历史可从 log 重建"；
  hook 调用也不落 log。
- dsh 有专门的 invariants 包 + `log-only` 事件类别（如 `hook/invoked` 审计记录：可重放、
  但不可见、不给模型）。
- 落地建议：debug 构建加重建断言；hook 调用写入 log-only 事件。

### 5. 能力接缝分层（optional capability 不进 loop 主干）

- 现状：`internal/agent` 49k 行 / 126 文件，compact 逻辑 814 行直接内嵌。
- dsh 把 compaction、spill、subagent、workflow 全做成接缝（Service Definition / Provider /
  Consumer 三角色），loop 只依赖接口。
- 落地建议：抽 `Compactor`、`SpillStore`、`SubagentProvider` 三个 interface，压低 agent
  包体积，测试可换 provider。

## 次要但有价值的点

- Subagent 能力静态声明 + fail-loud（`UNSUPPORTED_CAPABILITY`，绝不 accept-then-ignore）——
  可参考 `internal/agent/subagent_store.go`。
- Approval 通过 `callId` 关联已流式的调用、不复制 args，防渲染漂移——可对照
  `internal/permission` 的 Gate。
- `.agents/notes/` 设计笔记惯例（日期 + 状态 + 分类 implemented/proposed/rejected，
  含 AGENTS.md）+ 仓库根 README/AGENTS.md——Corvus 根目录目前无任何 .md，只有 `docs/adr`。
- Workflow（模型写编排脚本 spawn subagent）：不推荐近期做——攻击面大（执行模型生成的
  脚本）；`parallel_tasks` + `task` 已覆盖主要场景。

## Corvus 明显更强、别动的部分

- Bash 审批分类（shellparse 特征分析 + project hook 只能 deny 不能 allow 的信任语义）。
- 缓存优先的 prompt 前缀、缺失 reasoning 的恢复协议、证据账本（evidence ledger）。
- 最近 8 个 ADR，尤其 0006 耐久性分层。

## 建议落地顺序

1. Spill（改动最小、收益最大）
2. 工具目录 / 事件地图生成器
3. `ConcurrencySafe(args)` 接口
4. Hook log-only 事件 + 重建断言
5. 抽接缝接口 + 根 README/AGENTS.md

## 实施记录（2026-08-16）

按优先级落地了以下各项，全部测试通过（`go test ./...` 70 packages ok）：

1. **Spill**：新增 `internal/spill`（`Store` 接缝 + `Local` 本地实现），
   `store.SessionSpillDir`（`<id>.spill/`），Agent 侧 `boundToolResult`
   溢出落盘并给模型 locator + 检索指引；`BindSession` 原子绑定 session 与
   spill 目录；会话删除时清理。见 `internal/agent/agent.go`、
   `internal/agent/execute_one.go`、`internal/control/session_lifecycle.go`。
2. **并行分类器**：新增 `tool.ConcurrencySafe` 一元接口；调度器
   `parallelisable` 对写工具按参数分类（panic fail-closed），保持 provider
   顺序与屏障语义。见 `internal/tool/tool.go`、`internal/agent/agent.go`。
3. **Hook 审计 + 重建断言**：`store.SessionHookLog`（`<id>.hooks.jsonl`，
   log-only、best-effort、ADR-0006 诊断层）；Runner 每次 handle 写一条记录；
   `verifySessionEventRoundTrip`（`CORVUS_SESSION_ASSERT=1`）在保存后校验
   "model-visible means logged"。
4. **工具目录 + 事件地图**：新增 `cmd/corvus-catalog`，生成
   `docs/tool-catalog.md`（从 `tool.Builtins()`）与 `docs/event-map.md`
   （从 `internal/event/event.go` 的 Kind 常量 AST 提取）；`make tool-catalog`
   / `make event-map` / `make verify-catalog` 已接入 `make check` 与 CI。
5. **接缝抽取**：新增 `internal/compaction`（纯策略函数 + `Summarizer`
   接缝 + `KeepPolicy`），agent 保留循环粘合与 provider 摘要实现，
   `compactSummarizer` 可注入替换后端；`KeepPolicy` 以类型别名保持源兼容。
6. **README.md / AGENTS.md**：仓库根文档补齐，AGENTS.md 记录持久化权威、
   接缝纪律、并发契约与目录生成约定。
