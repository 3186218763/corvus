# Agent Harness 设计复盘：Corvus 与 pi / Codex / Crush / OpenCode / Goose

- Date: 2026-08-17
- Status: 调研笔记（非 ADR）
- Corvus snapshot: `9b0bf0e01a77b93d5ed4921ec7a1ea46ca3d1f50`
- External snapshots:
  - Codex: `21cfd369efca2df70c904c580b2e7e2e3eddb3c3`
  - Crush: `240c487ee22017921db343b490b4e95be2441e3b`
  - pi: `87205484bf749c2140fef5d1bea68995d57e739c`
  - OpenCode: `2cba7e227d68a7e7e4a2aa9c85b808e8ecb14daf`
  - Goose: `92c0fe902addb77178352111d2e533fd158444a6`
- Input: `.scratch/research/external-agent-design.md`，并以固定 commit 的源码重新核验

## 结论

外部报告是一份很好的设计候选清单，但已经不适合作为 Corvus 的缺口清单。报告提出的
spill locator、结构化 compaction、避免孤立 tool result、会话分支、权限与沙箱正交、
skills、per-model pricing、lazy capability loading 等能力，Corvus 已经实现，部分实现还比
报告描述的参考项目更重视恢复、审计和持久化正确性。

Corvus 当前最需要的不是继续横向加功能，而是收紧安全边界和控制复杂度：

1. **P0：项目 hooks 缺少真实信任边界。** 项目配置会被自动加载，`Trusted` 参数被忽略，
   hook 最终以宿主进程环境执行 shell。"项目 hook 只能 deny"只限制返回值，不限制 hook
   进程读凭据、写文件或联网。
2. **P1：compaction 的 summarizer 接口没有真正接通。** 默认摘要复用 executor provider；
   对 stateful Responses provider，这会覆盖 continuation state。应注入独立、无状态的
   summarizer/provider，并做回归测试。
3. **P1：`Agent` / `Controller` 已经成为状态所有权不清的控制面。** 两个包分别有
   21,610 / 13,522 行生产 Go 代码，`Agent` 有 130 个生产 receiver method，`Controller`
   有 272 个。下一轮设计工作应先把 run/session/runtime 状态按生命周期分开，再提取
   delivery supervisor，而不是继续给大结构体加字段和 reset 逻辑。
4. **P2：可以吸收 pi 的“语义分支交接 + 确定性工作集”**，但不应重做 Corvus 已有的
   session-level branch tree。
5. **P3：client/server、OpenAPI、SQLite 等平台化工作应等真实第二客户端出现。** 现在
   提前 daemon 化只会把内部复杂度升级为协议兼容负担。

## 外部报告校正

| 报告中的候选 | Corvus 现状 | 判断 |
|---|---|---|
| spill 后告诉模型完整输出路径 | `internal/spill` 和 `Agent.boundToolResult` 已返回 locator 与检索提示 | 已完成 |
| compaction 不从 tool result 切、结构化摘要、保留旧摘要 | `internal/compaction` 与 `internal/agent/compact.go` 已实现 | 已完成 |
| session tree / fork / branch navigation | `BranchMeta.ParentID/ForkTurn/ForkMessageIndex` 与 controller 分支操作已存在 | 已完成，不重构存储模型 |
| sandbox mode 与 approval policy 正交 | `internal/config`、`internal/permission`、`internal/sandbox` 已分层 | 已完成；默认网络策略仍可评估 |
| skills 体系和跨生态发现 | 已支持 `.corvus/.agents/.agent/.claude`、手动/自动触发、profiles、requires 等 | 已完成且范围更广 |
| per-model pricing | `ProviderEntry.Prices` / `PriceForModel` 已存在 | 已完成 |
| 动态工具加载 | MCP lazy placeholder、`use_capability`、tool search / economy routing 已存在 | 已完成 |
| per-file mutation queue | 同批 writer 当前基本串行；后台 subagent 有 path claims、parent reservation、workspace lease | 暂无直接缺口，先定义未来并发契约 |
| branch summary 累积文件工作集 | 摘要依赖模型生成 `Files & code`，没有确定性累计 | 部分缺口，值得做投影 |
| hooks 全生命周期、可改参数/结果 | Corvus 是 typed fixed hooks | 不建议泛化为任意 middleware |
| `/session/:id/diff` | 没有统一 query/API | 等真实 UI/客户端消费者 |
| client/server + OpenAPI | 当前没有第二客户端压力 | 延后 |

这个差异很重要：若直接按原报告排期，会重复建设已经完成的能力，同时把真正的 hook
安全问题和状态生命周期问题留在后面。

## Corvus 最有意思的设计

### 1. 事件日志是会话权威，不只是导出格式

`internal/agent/save.go` 与 `internal/agent/session_events.go` 组成的持久化设计，比常见的
"把 messages 序列化成 JSON"更完整：

- append/replace 事件日志是权威历史，`.jsonl` transcript 是发现锚点和兼容 checkpoint；
- 保存前修复 torn tail，但遇到未来 schema 会 fail hard，避免旧版本静默破坏新数据；
- digest、revision、writer identity 和基线检查组成乐观并发控制；
- snapshot 与显式 rewrite 使用不同写入语义，陈旧 runtime 不能覆盖更新历史；
- 保存冲突会进入 recovery branch，普通 fork 与冲突恢复共享同一 lineage；
- replay 对字节数、record、message 和嵌套 collection 都有限额；
- `CORVUS_SESSION_ASSERT=1` 可验证 model-visible history 能从 log 重建。

这是 Corvus 的核心资产。不要为了 SQLite 或服务化把事件日志降级为附属审计表。未来即使
增加数据库索引或远程 transport，也应继续让 event log 保持会话语义权威。

### 2. “完成”由宿主观察，而不是由模型自述

Corvus 的 evidence ledger、canonical todo、delivery criteria、final readiness、结构化
`review_report`、verification/review freshness 共同形成 delivery supervisor 的雏形。它
解决了 agent harness 最难的一类问题：模型说“完成”不等于工作真的完成。

特别好的地方是验证状态具有新鲜度：后续 mutation 会使先前 review/verification 失效，
最终回答必须重新满足条件。这比只在 prompt 里要求“请测试”强得多，也比在最终文本里搜
“tests passed”可靠。

这个设计应该被提取和命名，而不是继续散落在 `Agent` 字段、`beginRunTurn` reset 和工具
结果观察逻辑里。

### 3. provider 可见调用稳定，宿主执行目标可解析

`use_capability` 对 provider 保持稳定的工具名、schema 和 transcript；宿主在 permission、
hooks、evidence 和执行前，通过 `internal/tool/resolved.go` 解析真正的 MCP target。resolved
metadata 只供本地 UI 和策略使用，不污染 provider-visible history。

这同时服务了三个目标：prompt cache 稳定、权限判断针对真实能力、历史可重放。以后如果
需要兼容旧参数或增加目标别名，也应扩展 resolved-call 阶段，而不是允许通用 hook 在权限
检查前后任意改写参数。

### 4. prompt cache 被当作协议约束管理

Corvus 不只是传一个 cache key：它稳定 system/tool schema，保持 session sticky cache key，
对 DeepSeek 明确省略不兼容字段，并在 endpoint 拒绝后 process-local fail open。`PrefixShape`
还能解释 cache churn 来源。

这种可观测性很实用：cache miss 不再只是一个百分比，而能追到 system prompt、tool schema、
消息前缀或 provider workaround 的变化。

### 5. provider 协议恢复先于工具执行

Corvus 会修复 incomplete tool pair、截断 JSON arguments、被中断的 stream，并对缺失的
DeepSeek reasoning 做精确重放恢复。speculative tool UI event 会先缓冲，只有响应被采用才
提交，因此 malformed completion 不会闪出重复工具卡片，更不会在恢复前误执行工具。

这个顺序非常关键：先恢复 provider 协议，再提交 transcript，再执行副作用。许多 harness
把流式渲染、消息提交和工具执行交织在一起，出错后只能依赖整轮重试。

### 6. 并发采用 fail-closed 能力契约

`ConcurrencySafe(args)` 是一元纯分类器；panic 会退化为 exclusive；结果即使并行完成也按
provider 顺序提交。后台 writer 另有 symlink-safe `WritePathSet`、父 agent reservation 和
workspace lease。

这比按工具名字硬编码并行策略更可演进。当前限制也很清楚：一元分类器不能表达“调用 A
与调用 B 写不同路径所以彼此兼容”。在没有 writer 并行需求和冲突 telemetry 前，这个限制
是合理的保守选择。

### 7. 正常分支与恢复分支共享 lineage

`internal/agent/branch.go` 已记录 `ParentID`、`ForkTurn`、`ForkMessageIndex`，controller 提供
fork、branch、switch 和 tree rendering。保存冲突自动生成的 recovery branch 也进入同一
metadata tree。

因此不应照搬 pi 的 per-entry `parentId` 重建存储。Corvus 真正缺的是切回祖先并开新支时，
是否把被放弃分支的语义成果以可选 handoff 带入新支，而不是“有没有树”。

## 外部项目真正值得吸收的设计

### pi：先解决项目扩展信任，再谈 hook 能力

pi 的 `project_trust` 在项目扩展加载前运行；参与决策的只有 user/global/CLI 扩展，项目本地
扩展在信任完成前根本不会加载。这个顺序比“加载后限制它只能 deny”更接近真实安全边界。

参考：

- [pi project trust](https://github.com/earendil-works/pi/blob/87205484bf749c2140fef5d1bea68995d57e739c/packages/coding-agent/docs/extensions.md#project_trust)
- [pi branch summarization](https://github.com/earendil-works/pi/blob/87205484bf749c2140fef5d1bea68995d57e739c/packages/agent/src/harness/compaction/branch-summarization.ts)
- [pi compaction](https://github.com/earendil-works/pi/blob/87205484bf749c2140fef5d1bea68995d57e739c/packages/agent/src/harness/compaction/compaction.ts)

pi 另外两个值得吸收的细节：

- branch summary 的 `readFiles` / `modifiedFiles` 是从工具事件确定性提取并跨摘要累计，不完全
  依赖 LLM 记住文件；
- 摘要请求使用新的 session ID，并设置 `cacheRetention: "none"`，明确把 summarization 当作
  一次性、无状态旁路请求。

不应照搬的是任意 `tool_call` 参数改写和 `tool_result` 链式 middleware。Corvus 的 permission、
preview、path claims、checkpoint、evidence 和 event log 都依赖“被检查的调用就是被执行和记录
的调用”。通用改写会让这些不变量同时变复杂。

### Codex：按生命周期物理拆分状态

Codex 明确区分 session-wide `SessionState` 和 single-turn `TurnState`。这不是为了增加接口，
而是让字段的创建、复用、清理和并发规则从类型上可见。它也用 `RwLock` 表达工具并发：可并行
工具持 read lock，exclusive 工具持 write lock。

参考：

- [Codex SessionState](https://github.com/openai/codex/blob/21cfd369efca2df70c904c580b2e7e2e3eddb3c3/codex-rs/core/src/state/session.rs)
- [Codex TurnState](https://github.com/openai/codex/blob/21cfd369efca2df70c904c580b2e7e2e3eddb3c3/codex-rs/core/src/state/turn.rs)
- [Codex parallel tool runtime](https://github.com/openai/codex/blob/21cfd369efca2df70c904c580b2e7e2e3eddb3c3/codex-rs/core/src/tools/parallel.rs)
- [Codex permission protocol](https://github.com/openai/codex/blob/21cfd369efca2df70c904c580b2e7e2e3eddb3c3/codex-rs/app-server-protocol/src/protocol/v2/permissions.rs)

Codex 的 workspace-write 网络默认关闭，也值得作为 Corvus 的目标默认值评估。但 Corvus
当前 `Sandbox.Network=true` 是明确的兼容选择，不能在没有升级说明、profile/opt-in 和安装
场景验证时直接翻转。

### Crush：终态事件与 RunID 是未来 transport 的最小核心

Crush 的协议给 queued prompt 和 agent message 携带 `RunID`，并有权威 `RunComplete` 终态
事件。它解决的是多客户端/排队输入下最容易出现的歧义：某条 delta、approval、usage 或
完成事件究竟属于哪次 run。

参考：[Crush protocol](https://github.com/charmbracelet/crush/blob/240c487ee22017921db343b490b4e95be2441e3b/internal/proto/proto.go)

若 Corvus 将来出现 desktop + IDE 或 CLI + web 的真实并发客户端，第一步应是增加 `RunID`、
`SessionID` 和权威 terminal event，而不是先搬入完整 HTTP server。Crush 使用
FSL-1.1-MIT，适合学习设计，不应复制代码。

### OpenCode：复杂 server 是需求结果，不是起点

当前 OpenCode 已经有细分 HTTP route、middleware、workspace routing、session projector、
run coordinator 和 event sourcing。它展示了 OpenAPI/SDK 对多客户端的价值，也展示了这条
路线的长期维护成本。

参考：

- [OpenCode HTTP API](https://github.com/anomalyco/opencode/tree/2cba7e227d68a7e7e4a2aa9c85b808e8ecb14daf/packages/opencode/src/server/routes/instance/httpapi)
- [OpenCode session core](https://github.com/anomalyco/opencode/tree/2cba7e227d68a7e7e4a2aa9c85b808e8ecb14daf/packages/core/src/session)

对 Corvus 的启示不是“尽快 server 化”，而是先保持内部 typed events、snapshot/query 和
approval correlation 干净。真实第二客户端出现时，再在这些边界上加 versioned transport
DTO 和生成式 schema；不要直接暴露 `internal/event.Event`。

### Goose：把 operation / inference / effect 当作局部提取方法

Goose 一类 harness 把纯决策和 effectful operation 分开的思路，适合用于 Corvus run loop
的逐项简化：先把 plan/classify/transition 变成纯函数并做表驱动测试，外层继续负责 provider、
session 和工具副作用。它不构成整体重写 run loop 的理由。

参考：

- [Goose operation contract](https://github.com/block/goose/blob/92c0fe902addb77178352111d2e533fd158444a6/crates/goose-agent/src/operation.rs)
- [Goose state machine](https://github.com/block/goose/tree/92c0fe902addb77178352111d2e533fd158444a6/crates/goose/src/agents/state_machine)

## 优先级建议

### P0：恢复项目 hook 的信任门或强制隔离

直接证据：

- `internal/hook/hook.go:205-236`：`LoadOptions.Trusted` 仅为兼容保留，项目 hooks 自动加载；
- `internal/boot/boot_hooks.go:38-49`：boot 无条件加载项目 hook 设置；
- `internal/hook/hook.go:1249-1271`：hook 在项目 cwd 执行，继承 `secrets.ProcessEnv()`；
- `/hooks trust` 当前只是兼容提示，不再改变加载行为。

威胁模型不是 hook 的 JSON 返回值，而是 hook 进程本身。用户打开一个不可信仓库后，只要
触发相应 lifecycle event，项目配置指定的 shell 就可能读取 API key、修改 workspace 或
发起网络请求。

推荐实现顺序：

1. global/user-installed hooks 继续视为用户信任配置；
2. project hooks 在明确 trust decision 前不解析、不注册、不执行；
3. headless 模式无已有 trust decision 时默认禁用项目 hooks，并发出可审计 notice；
4. trust 记录绑定规范化项目根，处理 symlink/目录迁移语义，并提供 revoke；
5. defense in depth：项目 hook 清理敏感环境，限制写根和网络；若平台沙箱不可用则 fail closed；
6. 测试覆盖首次进入、resume 到另一 cwd、headless、拒绝、remember、revoke 和全局 hook 不受影响。

如果短期只能做一件事，先恢复加载前 trust gate。仅把 hook 的返回值限制为 deny，不能修复
这个问题。

### P1：完成 compaction summarizer 隔离

`internal/compaction.Summarizer` 已经定义，`Agent` 也有 `compactSummarizer` 字段，但
`agent.Options` / `New` 没有公开并赋值这条依赖。`Agent.summarize` 因此默认调用 `a.prov`。

Responses provider 是有状态的：`internal/provider/responses/responses.go:299-311` 读取
`lastResponseID` / `expectedPrefixDigest`，完成后在 `:576-588` 覆盖它们。compaction 用同一
provider 发独立摘要请求，会污染或清空 executor 的 continuation state。即便 compact 后历史
通常会导致 continuation miss，这种隐式耦合仍让行为取决于调用顺序，且已存在的 Summarizer
接口没有兑现。

建议：

1. 在 `agent.Options` 注入 `compaction.Summarizer`；
2. boot 为摘要构造独立 provider client，Responses 模式强制 stateless；
3. summary request 不携带 executor 的 prompt cache key / previous response ID；
4. 增加 stateful Responses + compaction 回归测试，断言摘要前后的 executor continuation state；
5. direct-construction 测试通过公开 Options 注入，不再依赖同包测试直接改私有字段。

### P1：按生命周期拆分 `Agent` 状态

当前 `Agent` 同时拥有：

- immutable runtime/provider/config；
- session binding、spill dir 和 session cache telemetry；
- per-session provider workaround state；
- per-Run evidence、delivery、repeat/storm/blocked guard；
- loop counters、steer concurrency；
- subagent scheduler、path claims、workspace lease；
- compaction 与 protocol recovery state。

`runLoopState` 已经是正确方向，但 `beginRunTurn` 仍需要手动重置/搬运许多 `Agent` 字段。手动
reset 的主要风险不是代码长，而是新字段很容易在下一次 `Run` 泄漏旧状态。

推荐分三步做，不要先把 `Options` cosmetic grouping：

1. 写 lifecycle ADR，定义 immutable runtime、session-bound state、per-Run state、per-call
   `toolCallPlan`、frontend/controller state 的所有权和并发规则；
2. 把 evidence、delivery criteria/expectations、repeat/storm/blocked guards、loop guard、
   capability reminder、pending review warnings 等移入构造即初始化的 `runLoopState`；
3. 提取 delivery supervisor 深模块，统一 canonical todo、evidence ledger、final readiness、
   review/security freshness、capability require/prefer、background evidence lease 和 delivery
   intent classification。

一个足够深、但不为 mock 制造薄接口的形状可以是：

```go
type DeliverySupervisor struct { /* owned run/session state */ }

func (d *DeliverySupervisor) BeginRun(input RunInput) error
func (d *DeliverySupervisor) BeforeTool(call ToolCallPlan) Decision
func (d *DeliverySupervisor) ObserveResult(result ObservedToolResult)
func (d *DeliverySupervisor) CanFinish(final FinalAttempt) Readiness
func (d *DeliverySupervisor) Commit(err error)
```

先完成状态所有权，再考虑围绕现有 parse/policy/prepare/finish 提取 tool execution pipeline，
以及围绕 streaming/recovery/deferred sink 提取 completion acquisition。不要留下只转发调用的
双层 wrapper；提取时迁移或替换旧测试。

建议增加一个简单的架构复杂度预算：`Agent` 字段数、`Options` 字段数和 agent 包 import fanout
不得继续净增长；新的 cross-cutting behavior 必须进入有明确所有权的模块。

### P1 Security：评估 sandbox 网络默认关闭

`internal/config/config.go:1361-1370` 明确让 `[sandbox].network` 默认为 true，以维持旧配置的
egress。Codex 的 workspace-write 默认禁网更符合 least privilege，但翻转 Corvus 默认值会
影响包管理器、下载、远程脚本和现有自动化。

这应单独写 ADR，包含兼容 profile、显式 opt-in、首次升级提示、headless 行为和平台沙箱
不可用时的降级策略。优先级低于项目 hook trust，因为 hook 当前甚至不经过工具 sandbox。

### P2：语义分支 handoff 与确定性 working-set projection

保留现有 session-level tree。在用户切回祖先并开新支时，可以可选生成一份 handoff：总结
被放弃分支上已验证的结论、失败尝试和未完成事项。与此同时，从 event log / tool evidence
确定性投影 host-observed working set，例如：

- read files；
- modified files；
- verified files/checks；
- unresolved mutation/review freshness。

LLM 负责语义摘要，宿主负责事实清单。该投影可以同时服务 compaction、branch handoff 和
未来 session diff，不需要先改变持久化模型。

### P2：未来 writer 并发先定义关系型契约

现在不要直接引入 pi 式 per-file queue。若 telemetry 证明 writer 串行成为瓶颈，再扩展一个
能表达调用间关系的契约，例如标准化 mutation keys / path claims：两个调用只有在 claims
可证明不冲突时才并行。继续保持 classifier 纯、panic fail closed、结果按 provider order
提交，并复用现有 symlink-safe path normalization 与 workspace lease。

### P2：逐项提取 run loop 的纯决策

适合先提取并表驱动测试的部分：

- compaction trigger / tail plan；
- readiness transition；
- recovery transition；
- repeat/storm breaker decision；
- call scheduling partition；
- branch/recovery lineage decision。

每次只提取一个 decision，让 effect layer 仍保持在原调用点。不要以“纯函数架构”为名整体
重写已经积累大量恢复语义的 run loop。

### P3：有第二客户端后再设计 transport

触发条件应是明确的第二客户端或远程附着需求，而不是“别的项目都有 server”。届时最小
协议应包含：

- `SessionID`、`RunID`、`CallID`；
- authoritative terminal event；
- approval/request-response correlation；
- versioned transport DTO；
- schema 生成 client types；
- snapshot + ordered event resume contract。

内部 `event.Event` 和 persistence record 都不应直接成为外部 wire format。

## 明确延后或拒绝

- **不重做 session tree。** 当前 branch metadata 已满足 session-level lineage。
- **不迁移 SQLite。** 查询索引可旁路增加，event log 继续是会话权威。
- **不采用 Bash 配置。** 图灵完备配置扩大执行面和可重复性问题。
- **不复制完整 TS plugin architecture。** Go 静态二进制的扩展边界不同。
- **不使用 LLM 做权限分类。** 安全决策继续依赖宿主可审计规则。
- **不开放通用 hook 参数/结果改写。** 它会破坏调用、权限、审计和重放的一致性。
- **不在无 telemetry 时并行 writer。** 当前保守串行是可接受的正确性选择。
- **不提前 daemon/OpenAPI 化。** 先等待真实客户端需求。
- **不增加只为 mock 存在的薄接口。** 接口必须隐藏复杂度或表达替换边界。

## 建议路线图

| 阶段 | 工作 | 完成标准 |
|---|---|---|
| P0 | 项目 hook trust/confinement | 未信任项目的 hook 不会被加载或执行；headless fail closed；有审计测试 |
| P1-a | compaction provider isolation | summarizer 可公开注入；Responses summary stateless；continuation 回归测试通过 |
| P1-b | lifecycle ADR + per-Run state | `beginRunTurn` 不再手动 reset delivery/evidence guards；字段所有权有文档和测试 |
| P1-c | delivery supervisor | todo/evidence/readiness/freshness 由单一模块拥有，`Agent` 只编排 |
| P1-d | sandbox network ADR | 默认值、兼容 profile、升级 UX 和平台降级有明确决策 |
| P2 | branch handoff + working-set projection | 事实清单可从事件/evidence 重建，compaction 与 branch 共用 |
| P2 | pure decision extraction | 每次提取减少主循环分支，保留或提升现有恢复测试覆盖 |
| P3 | transport（有消费者后） | RunID/SessionID/terminal event/versioned DTO/schema generation |

## 最终判断

Corvus 已经不是一个“功能不够多”的 agent。它最有价值的部分是事件日志权威、协议恢复、
provider-cache discipline、宿主证据闭环和保守并发；这些恰好是多数开源项目演示较少、但在
长期运行中最容易出事故的地方。

下一阶段的设计主题应是 **boundary and ownership**：先让不可信项目代码不能越过边界，再
让 run/session/provider/delivery 状态各自有唯一主人。完成这两件事后，pi 的语义分支交接、
Codex 的生命周期拆分、Crush 的 run correlation 和 OpenCode 的 transport contract 才能按
真实需求逐步吸收，而不会把 Corvus 变成多个项目特性的拼盘。
