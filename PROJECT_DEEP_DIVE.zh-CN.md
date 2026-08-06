# DeepSeek-Reasonix 深度探索（以源码为准）

> 本文基于当前仓库 **源码通读** 整理（分支 `main-v2`），不依赖 README 叙事。  
> 对照包：`cmd/reasonix`、`internal/cli`、`internal/boot`、`internal/control`、`internal/agent`、`internal/evidence`、`internal/checkpoint`、`internal/permission`、`internal/recovery`、`internal/event` 等。  
> 条目速查可另见 `DESIGN.zh-CN.md`；**行为以本仓实现与测试为准**。

---

## 0. 项目是什么

**Reasonix** 是一个用 Go 实现的本地 AI 编程代理 runtime：

| 事实 | 源码依据 |
|------|----------|
| 唯一可执行入口 | `cmd/reasonix/main.go` → `cli.Run` |
| 唯一交互面 | Bubble Tea TUI；非 TTY 直接退出（`cli.Run` 检查 `cliIsInteractive`） |
| 装配单点 | `boot.Build`：配置 → `control.Controller` |
| 执行核 | `agent.Agent` 或双模型 `agent.Coordinator`（均实现 `Runner`） |
| 前后端契约 | `event.Sink` / `event.Event`（agent 不渲染，只发事件） |

包注释写明（`boot`）：历史上 TUI / HTTP / desktop 共享同一装配；当前仓库已 slim 为 TUI 核心，但 **Controller 事件驱动契约仍在**。

---

## 1. 规模与包地图（实测）

| 指标 | 约值 |
|------|------|
| `internal` 一级包 | 50+ |
| Go 文件 | ~850 |
| 测试文件 | ~440 |
| 最重实现 | `agent`（~23k 非测试行）· `cli` · `control`（`controller.go`  alone ~6k 行）· `config` · `tool` |

**按调用链读代码：**

```
cmd/reasonix/main.go
  blank-import: provider/{openai,anthropic,responses}, tool/builtin
  → cli.Run
      → boot.Build(Options) → *control.Controller
      → chat TUI 驱动 Controller.Send / Approve / …
          → turnOrchestrator
              → runner.Run (Agent | Coordinator)
                  → stream → executeBatch → executeOne
                      → permission / recovery / evidence / checkpoint
```

---

## 2. 启动与装配：`boot.Build` 实际做了什么

文件：`internal/boot/boot.go`（`Build` 约 1800+ 行装配逻辑）

### 2.1 Options（前端只传 knobs）

关键字段（代码注释原文意图）：

- `Model` / `MaxSteps` / `RequireKey` / `Sink`
- `TokenMode`：`full` | `economy` | `delivery`（`token_profile.go`）
- `WorkspaceRoot`、`AdditionalDirs`、`PermissionAllow`
- `HeadlessApprovalMode`：子代理与 headless 门控契约
- `SharedHost`：多 Controller 复用 MCP 进程
- `FileOverlay` / `TerminalRunner`：ACP 等宿主 I/O 替换（**不改工具 schema**）
- `ProviderResolver`：远程 Workbench 注入，避免本地凭证
- `DisablePlanner` / `SandboxNetworkOverride` / `WorkspaceOnly`：监督式 worker 硬边界

### 2.2 装配顺序（源码路径）

1. **工作区根** `resolveWorkspaceRoot` + `normalizeAdditionalDirs`
2. **遗留迁移**：config / agent step limits / redact_tool_output / memory_compiler 等（并 Notice）
3. `config.LoadForRoot`；`secrets.SetFilterSubprocessEnv` 等 **在任何子进程前** 武装
4. **模型解析** `resolveModelEntry`；缺 key 时交互路径 Notice、不立刻硬失败
5. `TokenMode` → `capability.Profile{Balanced,Economy,Delivery}`
6. `event.Sync(sink)`；可选 `stats.NewRecorder`
7. **Delivery 才** 建 `workspacelease.Owner` + 注入 `jobs.WithJobStartObserver`
8. 系统提示组装（**一次写入 cache-stable 前缀**）：
   - `ResolveSystemPromptForRoot`（缺文件可 fallback + warn）
   - `outputstyle.Apply`
   - `UserDecisionPolicy` + `LanguagePolicy` + workspace 行
   - economy / delivery profile 文案
   - `environment.FormatSection`（探测结果 **Snapshot 到 cache 目录**，防每 boot 抖动前缀）
   - `memory.Compose`（AGENTS.md 等 + auto-memory）
   - `skill.ApplyIndex`（Economy **不**把 skill 索引塞进前缀）
9. **工具注册表**：
   - Economy：只 `tokenEconomyCoreBuiltins`（bash/bash_output/edit_file/kill_shell/read_file/wait/write_file）
   - 否则 `addBuiltins` 全量内置
   - MCP：cache-first lazy toolset；Economy 下 MCP 走 `connect_tool_source`
   - LSP / task / parallel_tasks / fleet / sessions / memory / ask / skills …
10. **权限**：`permission.New` + `control.NewSharedHeadlessGate`
11. **Hooks**：`hook.Load` + `hook.NewRunner`
12. **TaskTool** + `SubagentScheduler` + `SubagentStore`
13. 构造 `Agent` 或 `Coordinator`，包进 `control.New`

### 2.3 三种 TokenMode（`token_profile.go`）

| Mode | 工具面 | 额外行为 |
|------|--------|----------|
| `full`（balanced） | 完整 | 默认 |
| `economy` | 核心 7 工具 | `connect_tool_source` 按需接通；MCP 不预注册 |
| `delivery` | 完整 | system 注入 delivery-profile；Agent `DeliveryProfile=true`；workspace lease |

`connect_tool_source`：**MCP connect 在锁外执行**（handshake 可能数秒），避免阻塞其他 source；同 server 并发连接在 plugin host 去重。

---

## 3. 会话编排：`control.Controller` + `turnOrchestrator`

### 3.1 Controller 职责切分（`controller.go` 字段注释）

Controller **故意**把锁拆开，避免审批/状态轮询被重 I/O 堵住：

| 子系统 | 字段 / 包 | 职责 |
|--------|-----------|------|
| 运行态 | `mu`：running/finishing/closed/parkedTurns/rotating | 回合准入 |
| 审批 | `approval` | ask/auto/yolo、session grant、plan 自动放行窗口 |
| 检查点 | `checkpoints` + `mutationObserver` | rewind 边界与快照 |
| 记忆 | `memory` | 面板写与 turn-tail 队列 |
| MCP | `mcp` | 热插拔与 registry |
| Goal | `goals` + `autoResearch` | 多 turn 目标 FSM |
| 恢复 | `recoveryGate` | Auto Guard Episode |
| 后台 | `jobs` + `workspaceLease` | 后台任务与 Delivery 写租约 |

**Turn 准入**：`runGuarded` / `finishGuardedTurn`  
- `finishing` 窗口内新 turn **park**（FIFO），避免 TurnDone fan-out 与下一 turn 交叉  
- `closed` 后拒绝再 park  
- `rotating` 与 `running` 互斥，防 NewSession 换 session 时 TOCTOU  

### 3.2 用户输入如何变成模型输入：`compose`（`input.go`）

`composeWithGoal` 在 **user turn 尾部**（永不改 system 前缀）注入：

1. 活跃 Goal 块 + auto-research runtime  
2. Plan mode marker（`PlanModeMarker`）  
3. response / reasoning language 偏好  
4. `<memory-update>`（本会话刚 remember 的事实）  
5. `<background-jobs>` 完成说明  
6. hook SessionStart 上下文  
7. 自动 memory recall block  

Delivery runtime 契约块 **不在 compose**，而在 Agent `withTurnPreferences` 追加 `DeliveryRuntimeMarker`（byte-frozen 常量）。

### 3.3 一回合真实流程：`turnOrchestrator.runOrchestratedTurn`

文件：`internal/control/turn_orchestrator.go`

```
maybeSessionStart
  → Compose（+ capability route）
  → beginCheckpoint（仅可见用户 turn，非 synthetic）
  → UserPromptSubmit hook（可阻断）
  → DeliveryExecutionScope（Goal 作用域）
  → beginRecoveryEpisode（真实用户 turn）
  → runner.Run(ctx, modelInput)
  → persistGoalDeliveryCheckpoint
  → 取消/失败时 strip 不完整消息，保留配对 tool 历史
  → 若 planMode：requestApproval(exit_plan_mode)
       批准 → SetPlanMode(false) → seed todos
            → plan 窗口内 auto-approve writers
            → 合成 turn 执行 planApprovedMessage
```

要点：

- **Plan 审批复用** `ApprovalRequest` 通道，tool 名常量 `exit_plan_mode`  
- 批准后的执行 turn 是 **synthetic**，但仍 `beginRecoveryEpisode`  
- Goal 循环：`runGoalLoopWithRawDisplay` → 成功后 `continueGoal`；`FinalReadinessError` 会 block Goal  

### 3.4 权限模式（CLI → control）

`cli.parsePermissionMode`：

| 值 | 行为 |
|----|------|
| default/ask | `ToolApprovalAsk` |
| auto | 自动 |
| acceptedits | ask + allow 写文件工具列表 |
| dontask | DontAsk |
| plan | ask + planMode |
| yolo / bypasspermissions | Yolo |

特殊审批 tool 名：`sandbox_escape`、`config_write`（**强制真人**，YOLO 不能代答）。

---

## 4. 执行核：`Agent` 状态机

文件：`agent.go`（`Agent` 结构体 ~320 字段级注释）、`run_loop.go`、`execute_one.go`

### 4.1 Agent 持有的 host 状态（不进 prompt）

| 状态 | 作用 |
|------|------|
| `evidence *evidence.Ledger` | 本 turn 工具收据 |
| `todoState` | **跨 turn** 的 canonical 任务列表（不进 prompt，compaction 后仍在） |
| `deliveryProfile` + criteria/mutation flags | Delivery 门控 |
| `deliveryCheckpoint` | Goal 跨 run 的紧凑检查点（无原始 args） |
| `capabilityLedger` / `capabilityAudit` | require/prefer 能力路由 |
| `writeScheduler` / `workspaceLease` | 写路径与工作区租约 |
| `recoveryGate` | Auto Guard |
| `mutationObserver` | checkpoint v2 捕获 |
| `steerQueue` | 中途用户指令 |
| storm / blockedTurnStreak / loopGuard / repeatSuccess/Failure | 死循环防护 |

### 4.2 `Run` 入口（`agent.go`）

```go
// 伪流程，对应真实代码
workspaceLease.BeginRun/EndRun          // 仅 deliveryProfile
flushSteerQueue on exit
beginRunTurn → runToolLoop
// 成功后 CommitEvidenceForSession(background leases)
// scoped delivery 时 updateDeliveryCheckpoint
```

`beginRunTurn` 关键逻辑：

1. **Evidence 重置策略**  
   - `preserveEvidenceOnce`（子代理 review 重试）：只清 background leases  
   - 同 `DeliveryExecutionScope.ID`：保留收据，只清 leases  
   - 否则 `evidence.Reset()`  
2. **重新 lease 未提交的后台 job 证据**（失败 turn / 重启后必须再注入，否则背景 mutation 逃过 readiness）  
3. **Delivery 意图分类**：优先 `ClassifierTaskText` / scope.TaskText，**禁止**用带 host framing 的 Run input 分类（防子代理被 framing 动词误判为 mutation）  
4. 中断恢复摘要 `withInterruptedRecovery`  
5. repeat-failure：仅 `stateRecheck`（stale-anchor）跨 run 保留  
6. `withTurnPreferences`（语言 + delivery-runtime marker）  
7. 用户消息带 `RawContent`（当 provider input ≠ raw）  

### 4.3 主循环 `runToolLoop`

```
for step:
  consumeSteer → session + Steer event
  capturePrefixShape(tools)          // cache diagnostics
  streamWithMissingReasoningRecovery
  session.Add(assistant)
  if no tools → handleFinalResponse
  else → handleToolRound
```

**MaxSteps**：`<=0` 表示无上限（靠 compact 保窗口）；耗尽时 **graceRound** 强制只出最终答案。

### 4.4 DeepSeek 缺 reasoning 静默恢复

`streamWithMissingReasoningRecovery` + `observeMissingToolCallReasoning`：

- 仅当 provider 声明 `WarnOnMissingToolCallReasoning`  
- **工具执行前** 最多静默重放同一请求一次  
- `deferredStreamSink`：无 reasoning 时缓冲 ToolDispatch，避免 UI 闪双重卡片  
- 跨进程 cooldown：`missingReasoningWarnState`（指纹 #7059）  
- 健康 turn 连续 3 次才 resolve incident（anti-flapping）  
- 已有可见 text 时 **不重试**（防重复流式文本）  

Protocol recovery 事件：`event.RecordProtocolRecovery`（detected / retry / recovered / fallback…）。

### 4.5 最终答案门控 `handleFinalResponse`

顺序（源码）：

1. `recoveryGraceRound` → `RecoveryPauseError`  
2. `finalReadinessCheckFor(finalizeTask)`  
   - Goal 用 `[goal:continue]` / `[goal:blocked:…]` 决定是否 finalize  
   - 阻断时：用 **progressSignature**（缺项类型组合）判断是否“真进展”；无进展 3 次 / 有进展最多 6 次后 `FinalReadinessError` + `deliveryRecoveryPending`  
3. 空 final：`emptyFinalBlocks`；但 DeepSeek **reasoning-only finish** 可被 `reasoningOnlyFinishHonoured` 接受  
4. Coordinator executor handoff：无工具却 defer → 最多 nudge 1 次  
5. `closeSteerIntakeIfIdle`：队列空才结束；否则继续 loop  
6. **最终答案也 `maybeCompact`**（防下一 turn 爆窗）  

### 4.6 工具轮 `handleToolRound`

- grace / recovery grace 下拒绝再执行工具（但仍写 tool result 配对）  
- `executeBatch` → 写 tool messages  
- **todo 停滞**：无进展 8 轮 nudge，16 轮 `todoStallPause`  
- recovery Episode 耗尽 → 一轮 summarize-only  
- maxSteps 耗尽 → grace 合成 user nudge  

---

## 5. 单次工具调用：`executeOne` 流水线

文件：`execute_one.go`  
注释：**parse → policy → prepare → finish**；对 sink 纯函数，可并行。

### 5.1 parse

- `tools.ResolveCall`：歧义 MCP 名 / unknown / 刚 connect 完成的提示  
- **repeatedSuccessBlock**：同一成功写操作循环  
- **repeatedFailureBlock**：同类失败循环  
- **staleAnchorEditBlock**：锚点编辑在文件已变后仍用旧 anchor → 要求先 read  

### 5.2 policy：`resolveToolPolicy`

**A. Plan mode**（`planmode`，**不是权限边界**）

- 工具可实现 `PlanModeClassifier`  
- proxy 解析后对 **真实 target** 再 check 一次  
- MCP：非 planner-trusted 的 writer/destructive/未授权 reader 在 Plan 中阻断  

**B. CallResolver（`use_capability`）**

- 解析真实 MCP target → 改 perm/evidence/exec 名与 args  
- `SkipExecute` 路径：list/inspect 直接返回，记 meta receipt  
- `readOnlyExecution` / `plannerMCPExecution` 边界  

**C. Delivery policy gates**

在执行前 **静态分析 bash**：

| 阻断 | 原因 |
|------|------|
| `BashToolCallMasksVerificationExit` | `check; echo $?` 掩盖失败退出码 |
| `BashToolCallMixesMutationAndVerification` | 验证与写状态混在一条命令 |
| `BashToolCallUsesOpaqueInlineInterpreter` | `node -e` / `python -c` 等不可审计 |
| 无 criteria 的 state-changing | 必须先 `todo_write` |
| 无 active in_progress todo 的 mutation | 每次变更必须挂在当前 todo |

`ToolCallMutates` / `ToolCallRequiresDeliveryCriteria` 在 `evidence/evidence.go`：  
- meta 工具（task/run_skill…）本身不算 mutation  
- bash 用 shell 解析 + `shellsafe` 证明只读/验证，否则 **保守视为 mutation**  

**D. Recovery + Permission**

- Auto Guard **在 permission 与 write lease 之前**（等审批卡不占租约）  
- 触发：mutation / verification / plan transition / Episode 已 stop  

### 5.3 prepare

顺序严格：

1. Delivery：`workspaceLease.AcquireWrite`（仅 mutates）  
2. `reserveParentWrite`（防后台 subagent 写冲突）  
3. checkpoint **Barrier.EnterWrite**  
4. `observeBeforeMutation`（Previewer 抓 preimage；bash/MCP 记 **coverage gap**，不猜路径）  
5. PreToolUse hook（可阻断）  
6. 注入 ctx：callContext、ledger、delivery profile、todo state、jobs、escape/config approver、progress emitter…  

### 5.4 finish

1. Execute（可选 ImageTool）  
2. **Evidence 记账**：  
   - `complete_step` → 成功则 `advanceCanonicalTodo`  
   - proxy：meta receipt + 真实 target receipt（带 `OutputBytes`）  
   - `todo_write` 成功 → `setTodoState` + `deliveryCriteriaEstablished`  
3. capability 记账  
4. PostToolUse / Failure hooks  
5. `observeAfterMutation`（成功失败都打 after fingerprint）  
6. recovery 观察  
7. 错误时若 args 非法 JSON → **附带 schema 提示重试**  
8. truncate 输出（默认 **32KB** `maxToolOutputBytes`）  

### 5.5 批处理 `executeBatch`

- 先按 call 顺序全部 `ToolDispatch`  
- `partitionToolCalls`：**连续只读** 可并行；writer / unknown 串行  
- 前序 writer 跑过后刷新后续 writer 的 file diff preview  
- **每轮最多一个成功 `complete_step`**  
- recovery stop 后批量填 blocked result，保持 tool-call/result 配对  
- 串行 `ToolResult` 事件  
- `applyStormBreaker`  

### 5.6 Storm / Loop Guard

`applyStormBreaker`：

- 签名 = 有序 `(tool, error/blocker)`，**不含 args**（防模型改措辞绕过）  
- 或连续 N 轮 **全部 blocked**  
- 注入 `[loop guard]` 文案 + `armLoopGuardPass`  
- **loopGuard 通过后 final readiness 可放行**（避免“被拦却仍要求证据”的死锁）；但有新 progress receipt 会撤销 pass  

---

## 6. Final Readiness 与 Evidence（交付质量核心）

### 6.1 Ledger（`evidence.Ledger`）

- **仅内存**、本 turn（或同 delivery scope 跨 run）  
- `Receipt`：tool、command、paths、read/write/mutation、todos、OutputBytes…  
- 失败 receipt 保留审计，**HasSuccessful* 永不采纳**  
- BackgroundLease：后台 job 证据 **临时 merge**，turn 成功后才 `CommitEvidence`  

### 6.2 Todo 状态机

`ValidateSerialTodos`：

- 全局最多一个 `in_progress`  
- completed 必须是前缀  
- 两级：level-0 阶段 + level-1 子步骤；**阶段最后签收**  
- `complete_step` 成功 → host `AdvanceSerialTodo`，模型不必再 todo_write 标完成  

### 6.3 `finalReadinessCheckFor`（`agent.go`）

Plan mode 下 **直接跳过** delivery 完成检查（规划提案由 controller 审批；执行 turn 再 enforce）。

Delivery 下检查（摘要）：

| 条件 | 含义 |
|------|------|
| incomplete todos + 有 progress receipt | 未签收步骤 |
| taskExpected 无 work receipt | 技术任务无 host 可观察工作 |
| mutationExpected 无 mutation | 要求改状态但无成功 mutation |
| capability gate | require/prefer 未满足 |
| 有 writer/mutation 后 | criteria / complete_step / verification signoff / review |
| projectChecks | AGENTS 等提取的 host checks 须在最新 write 后成功跑过 |

意图分类启发式：`deliveryTaskNeedsEvidence` / `NeedsMutation` / advisory 等（大量子串与否定检测），并用 `ClassifierTaskText` 防 framing 污染。

### 6.4 验证命令分类

`IsDeliveryVerificationCommand` 与 `complete_step` **共用** `bashCommandIsVerification`。  
推荐集见 `VerificationCommandSummary()`（go test、pytest、tsc --noEmit、cargo test、node --check…）。  
**grep/cat 不算 verification**；只读管道进 verifier 可接受。

---

## 7. 双模型 Coordinator

文件：`coordinator.go`、`planner_route.go`

### 7.1 结构

- **独立** planner Session 与 executor Session（互不污染 prefix cache）  
- planner 可用只读 + `use_capability` 的 `NewPlannerAgent`  
- executor 开 `executorHandoffGuard`  

### 7.2 路由 `PlannerDecision`

| Route | 行为 |
|-------|------|
| `executor_only` | 直接 executor |
| `plan_and_execute` | 规划后执行；研究预算耗尽可降级 executor |
| `plan_for_approval` | 必须 host 批准；planner 失败 **不**降级执行 |
| `plan_only` | 只出计划 |

Depth：`light` | `full`；可 `MaxResearchRounds` 注入 `withRunStepLimit`。

### 7.3 计划协议

- `[no_changes]` → 不执行  
- `[planner_requires_approval]` 或 **中英文 approval 短语**（含“用户已批准”也重新门控，因 planner 不知 host 状态）  
- `<planner-ask>…</planner-ask>` → 结构化 Ask  
- handoff 格式带 `Reasonix executor handoff` marker  

降级策略：**plan_and_execute** 下 planner 失败可 executor-only；**plan_only / plan_for_approval** fail-closed。

---

## 8. 子代理、Fleet、写声明

### 8.1 Task / Skills（boot 装配）

- `task` / `read_only_task` / `parallel_tasks` / `fleet` 固定注册顺序（**保 tool schema 顺序 → cache**）  
- 默认 `DefaultMaxSubagentDepth = 2`  
- 默认并发：总 6 / 并行写者 3（`NormalizeConcurrencyLimits`，上限 32）  
- 子代理 **永远 headless gate**（无 UI）  
- 只读 skill：剥离 writer；review 技能可 `AttachReviewReportTool`；Delivery 下 `RequireReviewReportKind`  
- 可写 skill 无 `write_paths` → **WholeWorkspace claim**  
- 父会话只收最终答案；中间推理不进 parent prompt  

### 8.2 Fleet（`fleet.go`）

- 2–64 任务  
- 多写者必须 **非重叠 write_paths**；省略 = whole workspace  
- preflight 失败则 **一个都不启动**  
- 默认可独立失败；可 background + `wait`  

### 8.3 Write claims（`write_claims.go`）

- 规范化绝对路径、禁 glob、禁逃逸 symlink  
- 父 agent `reserveParentWrite` 覆盖晚加载的 Economy/MCP 工具（不改 schema）  

### 8.4 Workspace lease（`workspacelease`）

- Delivery 会话从首次 mutation 持有到所有 run/job 结束  
- 读者不拿锁；Owner re-entrant；锁文件在 workspace **外**  

---

## 9. Checkpoint 与 Rewind

包：`internal/checkpoint`（注释：git-free，对齐 Claude Code rewind）

- 仅对能 `Preview` 的 writer 做 preimage  
- v2：content-addressed blob、after fingerprint、coverage gaps、事务恢复  
- Gap 类型：bash 副作用、MCP 外部写、hook 可能写…  
- Controller `MsgIndex` 绑定对话回滚边界  
- 取消/崩溃时尽量保留 **已配对** tool 历史  

---

## 10. 权限、沙箱、Guardian、Recovery

### 10.1 Permission（`permission`）

- 纯 `Policy`：Allow / Ask / Deny  
- 规则：`Tool`、`Tool(glob)`、`Tool=literal`  
- 只读默认 Allow；writer 走 Mode fallback  
- Deny 可扩展 PowerShell 写 cmdlet 兼容  

### 10.2 Sandbox

- `sandbox.Spec`：mode / write roots / forbid read / network  
- 平台 seatbelt 等；escape 需单独审批  

### 10.3 Guardian

- 独立 Agent session + 嵌入 `guardian_policy.md`  
- 前缀稳定；熔断；定期 compact  
- 事件：`GuardianAssessment`  

### 10.4 Auto Guard（`recovery.Gate`）

- 共享 Episode 预算（根 + 子代理）  
- TaskID 隔离失败计数；Episode 共享 hard stop  
- 高风险 mutation / 失败恢复需 reviewer 或 human  
- Headless：不等人，结构化阻断  

---

## 11. 缓存命中机制（深度）

Reasonix 的 cache-first **不是**客户端本地缓存 LLM 回复，而是：  
**把发往 Provider 的请求做成「字节稳定的长前缀 + 每轮变长的尾部」**，吃服务端 **prompt cache / automatic prefix cache**（DeepSeek 的 `prompt_cache_hit_tokens`、OpenAI 的 `prompt_tokens_details.cached_tokens`、Anthropic 的 `cache_read_input_tokens` 等）。

核心文件：

| 层 | 路径 | 作用 |
|----|------|------|
| 计量归一 | `provider.Usage` + `openai.normaliseUsage` / `anthropic` | 多协议 cache 字段 → `CacheHitTokens` / `CacheMissTokens` |
| 请求构造 | `agent.stream` + `provider.ModelMessages` + openai `buildRequest` | 决定「什么字节进前缀」 |
| 前缀诊断 | `agent/cache_shape.go` | 本地解释 *为什么* miss（不替代服务端 hit） |
| 压缩策略 | `agent/compact.go` | 低频重置点：尽量 append-only，必要才 rewrite |
| 会话累计 | `Agent.sessCacheHit/Miss` | 状态栏 session 级 hit rate |
| 展示 | `FormatUsageLine` / CLI `renderTurnReceipt` | `(cached / new)` + prefix changed 原因 |
| 回归 | `agent/cachehit_e2e_test.go`、`openai/realcache_test.go`（live） | 前缀稳定性与真实 API 探针 |

---

### 11.1 服务端缓存到底命中什么

**模型侧语义（以 DeepSeek 自动前缀缓存为代表）**：  
同一会话连续请求里，若 **messages（及 tools 等）从头部起有一段与历史请求字节一致**，一致的那一段可计为 cache hit；新增尾部计为 miss。  
粒度通常按 token block（live 测试注释提到 DeepSeek 约 64-token 块），前缀太短可能 hit=0。

Reasonix **不自己实现 KV cache**，只做两件事：

1. **尽量让「上一请求的完整内容」成为「下一请求的前缀」**（append-only 历史）  
2. **把服务端回报的 hit/miss 归一、累计、诊断、定价**

`provider.Usage`（`provider.go`）：

```text
CacheHitTokens   // 从缓存读出的 prompt tokens
CacheMissTokens  // 未命中 / 新写入侧
```

定价（`Pricing.Cost`）：

```text
cost ≈ (hit * cache_hit + miss * input + completion * output) / 1e6
```

若只有 prompt 没有 hit/miss 字段，**整段 prompt 按 miss（全价 input）计**。

---

### 11.2 多 Provider 的 usage 归一（`normaliseUsage`）

`internal/provider/openai/openai.go` 把多种 wire 形状折叠成统一 Usage：

| 来源 | hit 字段 | miss 推导 |
|------|----------|-----------|
| DeepSeek 顶层 | `prompt_cache_hit_tokens` | `prompt_cache_miss_tokens` |
| OpenAI / MiMo | `prompt_tokens_details.cached_tokens` | `prompt - hit`（若 miss 未给） |
| Anthropic 风格兼容 | `cache_read_input_tokens` | `input_tokens + cache_creation_input_tokens`（写入仍算 uncached） |

Anthropic 原生路径（`anthropic.go`）同理：`CacheRead` → hit，`input + cache_creation` → miss。

流式路径里 usage 可能分多 chunk 到达：`total.CacheHitTokens += next...` 累加。

**RequestCount**：missing-reasoning 静默重试会 merge 两次 usage，并正确计 2 次 API 调用（`mergeStreamUsage`）。

---

### 11.3 请求如何保持「前缀可缓存」

#### （1）历史 append-only，整段重放

每个 tool 轮 / 用户 turn 只在 **末尾追加** messages，下一请求把 `session.Messages` 几乎原样再发一遍。  
e2e `TestCacheHitPrefixStable` 用 mock DeepSeek 证明：

> request *i* 的 common-prefix 字节数 == request *i-1* 的完整 messages 字节数  

即：**客户端没有在历史中间改字节**。

#### （2）上线前清洗：只剥「本地元数据」，不改对话正文

`agent.stream`：

```go
requestMessages := provider.ModelMessages(a.session.Messages)
// 再把 CreatedAt 清零 —— UI 时钟绝不能进 wire，否则前缀每 turn 抖
```

`provider.ModelMessages`：

- 丢掉 `LocalOnly`（中断半截 reasoning/tool args，只给 UI 回放，**永不进模型**）  
- 用 `Content` 作为 provider 可见内容；`RawContent` 不发送（host 注入与 raw 分离存在 session 里）  
- 不重排、不重写历史正文  

`NormalizeMessages` 对 **健康历史零分配原样返回**（注释写明：保持 prefix-cache key 稳定）。

#### （3）system + tools 在 boot 钉死

`boot.Build` 把下列内容 **一次** 写进 system message，之后 turn 不改：

- system prompt / output style / language & user-decision policy  
- workspace 行  
- economy / delivery **profile 文案**（注意：delivery **runtime** marker 不在这里）  
- environment 探测段（见下）  
- `memory.Compose`（AGENTS.md 等）  
- skill **索引**（名+描述；Economy 模式故意不塞，减小 tools/system）  

Tools schema：`a.tools.Schemas()` 每请求发送，但注册顺序被钉死（如 task → parallel_tasks → fleet），测试 `boot_test` 断言 **tool order 不可乱**（schema 顺序影响 cache shape）。

#### （4）会变的东西只进 **user turn 尾部**

| 内容 | 注入点 | 为何不进 system |
|------|--------|-----------------|
| Plan mode marker | `control.compose` | 开关切换不炸 system hash |
| response/reasoning 语言 | compose + `withTurnPreferences` | 偏好可热改 |
| `<delivery-runtime>` | `Agent.withTurnPreferences` | 常量 `DeliveryRuntimeMarker` **字面冻结**（改一字破坏 steer 回放匹配） |
| `<memory-update>` / recall | compose | 会话中 remember 立即生效 |
| background-jobs / hook-context | compose | 运行时通知 |
| capability route 块 | `withCapabilityRoute` | Delivery 混合路由 |
| mid-turn steer | `runToolLoop` 追加 user | **故意** miss 一次；注释写明 cache miss 不可避免但限一次 |

#### （5）Environment 探测：防「每 boot 改前缀」

`environment.RunProbesWithOptions`：

- 结果 **持久化到 SnapshotDir**（`config.CacheDir()`）  
- TTL 内直接复用旧字节  
- 过期后 merge：瞬时失败（timeout）**保留上次成功观测**，避免 PATH 抖动重写 system  

注释原文意图：*provider prefix cache survives app relaunches*。

#### （6）reasoning 回放与 cache 的张力

DeepSeek thinking + tool_calls：API **要求** assistant 历史带 `reasoning_content` key（可空串）。  
`openai.buildRequest`：

- deepseek + tool_calls：按策略回传 `ReasoningContent`  
- thinking 从 on→off：若历史里已有 reasoning，**仍发送**以免混合会话前缀字节变化  
- 缺失 reasoning 时发空 key，避免 400，同时不靠改历史来修  

`cachehit_e2e` 的 `TestReasoningRoundTripCost` 量化：  
CoT 回放会 **增大每轮 fresh tail**，拉低 hit% 爬升速度——这是协议正确性与 cache 率的显式权衡，不是实现疏漏。

#### （7）Coordinator：双 session 隔离前缀

Planner 与 Executor **独立 Session**（`coordinator.go` 注释）：  
规划轮的只读工具 transcript 不会污染 executor 的 cache 前缀，反之亦然。

Guardian 同理：专用 session + 稳定 policy 前缀，每次审查只 append delta user。

#### （8）Economy / MCP：控制 tools 面抖动

- Economy 启动时 tools 集合小 → ToolsHash 小且稳  
- `connect_tool_source` 接通后 tools **变** → 下一请求 ToolsHash 变 → `CacheDiagnostics` 报 `tools`；这是有意的能力扩展代价  
- MCP lazy placeholder 与 fixed registration order 减少无意义的 schema 乱序  

#### （9）子代理不污染父前缀

子代理中间 reasoning/tool 只嵌套事件；父 session 通常只追加最终答案。  
Skill/subagent system prompt 用 skill body，**不复用父 system**（`profile_spec` 注释：prompt-cache stability of parent）。

---

### 11.4 本地诊断：`PrefixShape` / `CacheDiagnostics`

**服务端 hit** 与 **客户端 prefix shape** 是两层信号。

每轮 `runToolLoop`：

```text
schemas := tools.Schemas()
prefixShape := capturePrefixShape(schemas)   // system + tools + rewriteVersion
stream...
cacheDiagnostics := CompareShape(prev, cur, usage)
emit Usage{ CacheDiagnostics, SessionHit, SessionMiss }
```

`PrefixShape` 字段（`cache_shape.go`）：

| 字段 | 含义 |
|------|------|
| `SystemHash` | 当前 session 内所有 system 消息拼接的短 hash |
| `ToolsHash` | 规范化排序后的 tool schema JSON hash |
| `PrefixHash` | system+tools 合成 hash |
| `LogRewriteVersion` | session 被 compact/fold 改写次数 |
| `ToolSchemaTokens` | schema 体量粗估（~4 chars/token） |

`CompareShape` 产出 `PrefixChangeReasons`：

- `"system"` — system 文本变了（极少见，通常是换 session）  
- `"tools"` — 工具面变了（Economy connect、热插 MCP…）  
- `"log_rewrite"` — 发生过 compaction/rewrite  

注意：

- **PrefixChanged=false 仍可能 miss**：例如本轮新增了很长的 user/tool 尾部（正常）  
- **PrefixChanged=true 几乎必然伤 cache**：历史/system/tools 头变了  
- 诊断 **不** 声称「服务端一定 miss」；它解释 **客户端可控的 churn**  

CLI 展示（`FormatUsageLine` / `renderTurnReceipt`）：

```text
· N tok · in P (H cached / M new) · out C · ¥x.xxxx
· cache prefix changed: tools+log_rewrite   // 仅当 PrefixChanged
```

刻意用 **绝对量 (cached / new)** 而不是只显示百分比：  
百分比随 fresh tail 变大而下降，容易误报「cache 坏了」；绝对值仍显示前缀在命中。

Session 级：`Agent.SessionCache()` 累计所有 API call 的 hit/miss（**compact 不重置**；`SetSession` 才清零）。  
状态栏用 Σhit/(Σhit+Σmiss) 作为更稳的成本向指标。

---

### 11.5 压缩：低频 cache-reset，而不是每轮整理

`compact.go` 开篇注释：

> Compaction is a low-frequency **cache-reset point**: the prompt grows **append-only** (high cache hits) until near compactRatio, then compacted to a tail budget.

阶梯：

| 水位（占 context_window） | 行为 | 对 cache 的影响 |
|---------------------------|------|-----------------|
| soft 50% | Notice：「preserving cache until cleanup」 | **零 rewrite** |
| snip 60% | `SnipStaleToolResults` 廉价裁剪旧 tool 输出 | 有限 rewrite（旧 tool 体） |
| compact 80% | 先 prune；不够再 summarize fold | **log_rewrite++**，前缀从 summary 处重建 |
| force 90% | 强制 compact | 同上 |

设计细节：

1. **tail 预算固定 ~16K tokens**（非窗口比例）→ 大窗口少 compact，小窗口也能落到触发线以下，打断 re-compact 死循环  
2. **连续 2 次 compact 仍过线 → `compactStuck`**，暂停 auto-compact 并发 Notice；之后恢复 append-only（`TestCacheHitSurvivesTooSmallWindow`：尾部 hit 率应回升 ≥85%）  
3. soft 区 **禁止** 为了整洁而 compact（会无谓 crater cache）  
4. 用户小 turn **verbatim pin**；fold 只吃 assistant/tool 工作区；摘要包在 `<compaction-summary>`  
5. `activeTurnCreatedAt`：当前进行中的 turn 整段不进 fold（取消/崩溃要保留 tool 配对）  
6. foldEconomics：可折叠区 < ~400 tokens 跳过 summarize（省一次付费且避免无意义 rewrite）  

**Manual `/compact`**：用户主动接受 cache reset。

---

### 11.6 哪些操作会「打穿」cache（清单）

| 操作 | 机制 | 信号 |
|------|------|------|
| 正常新 user / tool 尾部 | append | hit 升、new 为尾部大小 |
| Steer 中途注入 | 追加 user | 下一 API 一次 miss 增大 |
| Compact / snip / prune | `session.Rewrite` | `log_rewrite` + hit 骤降一截后回升 |
| Economy `connect_tool_source` / 热加 MCP | tools 集合变 | reasons 含 `tools` |
| 换 model / 新 session | 新 system/session | SessionCache 重置 |
| Plan 开关 | 只动 user marker | **不应**动 system hash |
| remember 本会话 | `<memory-update>` 尾部 | system 不变；下 session 才 fold 进 prefix |
| CreatedAt / LocalOnly 泄漏进 wire | 被 strip | 若漏 strip 会系统性 miss（有测试护栏） |
| 损坏历史 Normalize 修复 | 可能改 messages | 应尽量只发生在 load 时一次 |
| DeepSeek 缺 reasoning 静默重试 | 同一请求重放 | 第二次应高 hit；usage merge 两请求 |

---

### 11.7 与 Delivery / 证据的关系

Evidence ledger、todoState、deliveryCheckpoint **不进 prompt**（host 状态）。  
因此 Delivery 门控 **不**以修改 system 为代价；契约文案用：

- boot 时稳定的 `<delivery-profile>`（system）  
- 每 user turn 尾部冻结的 `<delivery-runtime>`（`DeliveryRuntimeMarker` 常量）  

两者都避免「每轮改 system → 永久 cold cache」。

---

### 11.8 测试与 live 探针

| 测试 | 证明 |
|------|------|
| `TestCacheHitPrefixStable` | 多 tool 轮下 mock 前缀字节完全嵌套 |
| `TestCacheHitClimbsWithoutCompaction` | 无 compact 时 hit% 随历史爬升 |
| `TestCacheHitSurvivesTooSmallWindow` | stuck guard 后不再每步 crater |
| `TestReasoningRoundTripCost` | CoT 回放对 hit 曲线的代价 |
| `TestSessionAggregateCacheRate` | SessionCache == Σ per-turn |
| `TestSetSessionResetsSessionCache` | 换 session 清零累计 |
| `openai.TestRealDeepSeekCacheProbe`（`-tags live`） | 真实 API：重复前缀是否 hit、tool-call reasoning 回放是否仍可 cache |
| tool schema/order 稳定性测试 | 防无意改 Description/Schema/注册序 |

Release 可选：`REASONIX_RELEASE_CACHE_GUARD=1` 门槛化 hit 率回归。

---

### 11.9 一句话架构图

```
┌─────────────────────────────────────────────────────────────┐
│ Provider prompt cache (DeepSeek / OpenAI / Anthropic …)     │
│   hit = 与历史请求头部字节一致的 token 段                     │
└───────────────────────────▲─────────────────────────────────┘
                            │ Stream(Request{Messages, Tools})
┌───────────────────────────┴─────────────────────────────────┐
│ Wire hygiene                                                 │
│  ModelMessages 去 LocalOnly · 清 CreatedAt · Normalize 快路径 │
│  reasoning_content 按协议回放（DeepSeek tool_calls）           │
└───────────────────────────▲─────────────────────────────────┘
                            │ append-only session.Messages
┌───────────────────────────┴─────────────────────────────────┐
│ Cache-stable head (boot 一次)                                │
│  system: prompt+memory+skills index+env snapshot+profiles    │
│  tools: 固定注册序 schema                                     │
├─────────────────────────────────────────────────────────────┤
│ Growing middle (append-only history)                         │
│  直到 soft/snip/compact 阶梯；compact = 有意的 cache reset    │
├─────────────────────────────────────────────────────────────┤
│ Volatile tail (每 turn / 每 steer)                           │
│  plan/delivery-runtime/memory-update/jobs/hooks/lang/route   │
└─────────────────────────────────────────────────────────────┘
         │                              │
         ▼                              ▼
  CacheDiagnostics (本地)         Usage hit/miss (服务端)
  system|tools|log_rewrite        定价 + SessionCache + UI
```

**产品含义**：长会话下，大部分 prompt 费用应落在 **cache_hit 单价**；客户端的首要义务是 **不无故改写已发送过的前缀字节**，并把必要的动态状态赶到 tail 或低频 compact。

> **横向对比**：Claude Code / Codex / OpenAI Agents SDK / OpenCode 的缓存策略与「是否过死板」评估，见  
> [`docs/research/agent-prompt-caching-survey.md`](./docs/research/agent-prompt-caching-survey.md)。

---

## 11A. 上下文压缩（与 cache 配套的实现细节）

（与上节一体；此处补操作级细节。）

- 摘要 system prompt 固定七段：Standing facts / Goal / Decisions / Files / Commands / Errors / Pending  
- 失败 summarize → mechanical fold digest，仍 rewrite 腾窗口  
- 用户 `/compact <focus>` + PreCompact hook 可追加摘要指令  
- archiveDir 保留被 fold 原文，可追溯  

---

## 12. 事件系统（`event`）

Kind 枚举（顺序 wire-stable，新 kind 只能 append）：

`TurnStarted` · `Reasoning` · `Text` · `Message` · `ToolDispatch` · `ToolResult` · `Usage` · `Notice` · `Phase` · `ApprovalRequest` · `AskRequest` · `TurnDone` · `CompactionStarted` · `CompactionDone` · `ToolProgress` · `MCPSurfaceReady` · `Retrying` · `Steer` · `GuardianAssessment`

Turn outcome 特例：`final_readiness`、`recovery_paused`（新客户端不当 send-failed）。

---

## 13. 内置工具清单（`tool/builtin` Name()）

文件工具：`read_file` `write_file` `edit_file` `multi_edit` `move_file` `delete_range` `delete_symbol` `notebook_edit` `ls` `glob` `grep` `code_index`  

Shell：`bash` `bash_output` `kill_shell` `wait`  

工作流：`todo_write` `complete_step` `web_fetch`  

装配注入（非全部在 builtin 包）：`ask` `task` `read_only_task` `parallel_tasks` `fleet` `use_capability` `connect_tool_source` `run_skill` / memory / session / history / LSP …

---

## 14. 扩展面：MCP / Skill / Memory / Plugin

| 机制 | 实现要点 |
|------|----------|
| MCP | cache schema → lazy 进程；`EnsureConnected`；Economy 按需；`use_capability` 稳定代理 |
| Capability | Catalog + 确定性路由 + 可选 SemanticRouter（3s 超时、不覆盖 require/prefer） |
| Skills | project > custom > global > builtin；索引进前缀、正文按需；兼容 `.reasonix/.agents/.claude` |
| Memory | Standing instructions + auto-memory；mid-session 只走 turn tail |
| Plugin packages | native / Codex / Claude manifest |
| Hooks | PreToolUse 可阻断；UserPromptSubmit/Stop 在 controller 边界 |

内置 skill `reasonix-guide`：用 `doctor capabilities` 做自诊断剧本。

---

## 15. Session 工程

`Session`：

- Snapshot/Save 并发安全；`rewriteVersion` / `persistedRewriteVersion`  
- Load 时规范化悬空 tool call、损坏 event log → replayable 前缀  
- session lease 防多进程抢写  
- cold resume 可 prune 陈旧 tool result  

Controller `snapshotMu`：整段 save/recovery 与 path swap 串行，防 recovery 级联。

---

## 16. 一次请求端到端（实现级）

```
TUI 输入
  → Controller.Send → runGuarded → turnOrchestrator
  → compose + capability route + beginCheckpoint
  → Agent.Run / Coordinator.Run
      beginRunTurn（evidence/scope/classification）
      loop:
        stream(+missing reasoning recovery)
        tools? executeBatch:
          每 call: parse → plan/proxy/delivery → recovery → permission
                → lease/write-claim/barrier → preimage → hooks → Execute
                → evidence → posthooks → after fingerprint
          storm breaker / todo stall
        final? readiness / empty / handoff / steer / compact
  → plan 审批? 合成执行 turn
  → goal continue?
  → TurnDone + session snapshot
```

---

## 17. 设计取舍（从代码读出的“为什么”）

| 取舍 | 代码表现 |
|------|----------|
| 安全不靠 prompt | Plan 只改 marker；真拦截在 permission/sandbox/delivery gates |
| 完成=可验证 | Ledger + complete_step + readiness 硬阻断 |
| 省钱=稳前缀 | memory/skill/env 进 prefix 一次；动态只进 tail |
| 模型会抽风 | missing-reasoning 静默重试、storm、repeat、stale-anchor、todo stall |
| 并行要安全 | write_paths preflight、parent write reserve、workspace lease |
| 回滚要诚实 | checkpoint coverage gap，不假装 bash 可全追踪 |
| 双模型可降级 | plan_and_execute 可 executor-only；审批边界 fail-closed |
| 可测优先 | agent/control/evidence 测试行数极高；行为用 e2e 钉死 |

---

## 18. 建议阅读顺序（按源码）

1. `cmd/reasonix/main.go`  
2. `internal/cli/cli.go` → `setupProfileWithOverrides`  
3. `internal/boot/boot.go` `Build` + `token_profile.go`  
4. `internal/control/controller.go` 结构体字段 + `turn_orchestrator.go`  
5. `internal/control/input.go` `composeWithGoal`  
6. `internal/agent/agent.go` `Run` / `finalReadinessCheckFor` / `Agent` 字段  
7. `internal/agent/run_loop.go` 全文  
8. `internal/agent/execute_one.go` 全文  
9. **缓存链路**：`provider.Usage` + `openai.normaliseUsage` → `agent.stream` / `ModelMessages` → `cache_shape.go` → `compact.go` → `cachehit_e2e_test.go`  
10. `internal/evidence/evidence.go` `ToolCallMutates` 与 Ledger matchers  
11. `internal/agent/coordinator.go` `Run`  
12. `internal/checkpoint/checkpoint.go`  
13. `internal/recovery/gate.go`  
14. `internal/event/event.go`（含 `CacheDiagnostics`）  

---

## 19. 结语

Reasonix 的核心不是「让模型会调工具」，而是：

1. **工具结果如何变成 host 可审计证据**  
2. **证据不足时如何拒绝“口头完成”**  
3. **写操作如何可回滚、可并行、不互踩**  
4. **上下文如何又长又稳又省**（§11：append-only 前缀吃服务端 prompt cache）  
5. **模型/协议异常时 harness 如何自愈而不重跑副作用**  

TUI 只是 `event.Sink` 的一种实现；真正的产品是 **`boot` + `control` + `agent` + `evidence` 组成的本地 agent runtime**。

---

*生成方式：逐文件阅读上述路径的实现与注释，并结合关键测试名校验行为。若实现变更，以源码为准。*
