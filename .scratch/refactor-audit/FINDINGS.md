# corvus 复杂度审计（2026-08-16）

> 只读审计，未修改任何代码。方法：6 路并行只读子代理（cli / agent+control /
> config+boot+environment / tool+plugin+provider / 12 个中型包 / 30+ 个小包）
> + 主线程对每个"死代码"主张做全仓 grep 复核（全部成立，其中中型包一路另用
> go/types 扫描器 + `deadcode` 双重验证）。共审计 ~148k 行非测试 Go 代码。
> 上一轮依赖审计（ADR 0001–0007）的结论未重复计入。
>
> **总量估计**：约 4,000–4,700 行可删/可收敛（≈3%），其中 ~2,600–3,000 行为
> 零风险纯删除。依赖本身干净：`go mod tidy` 零差异，32 个直接依赖全部在用。

---

## P0 — 纯删除，零行为风险（建议第一批执行）

### 0.1 整文件死代码（~940 行）

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/stats/query.go` | 238 | "desktop usage panel" 查询 API，该 frontend 不存在于本仓；stats 包唯一生产消费者（boot_notices.go）只用 `NewRecorder` |
| `internal/hook/inspect.go` | ~253 | hook 检查器子功能，9 个函数 8 个死（仅 `UsesToolMatcher` 存活，被 hook.go:502 调用） |
| `internal/netclient/dialer.go` | 157 | "SSH 用的 dial seam"，仓内无 SSH；唯一引用者是自己的测试 |
| `internal/command/inspect.go` | 156 | `Inspect` 零调用者（skill/hook 的平行 Inspect 实现同死） |
| `internal/skill/inspect.go` | 119 | 同上，仅 `/skill paths`（`PathStatus`）存活 |
| `internal/plugin/install.go` | 99 | `InstallAndConnect*` 零外部调用 |
| `internal/skill/profile.go` | 87 | "subagent profile editor" 整功能死——文档引用的 desktop/CLI 编辑器不存在 |
| `internal/event/fanout.go` | 28 | `NewFanOut`/`FanOut` 零引用 |

配套死簇：`hook.go` 中仅被死检查器引用的 `CheckRuntime`/`checkRuntimeForPlatform`/
`requiresWindowsBash`/`CheckPackageRuntime`/`RuntimeOptionsForShell`/`ContextFileUsable`/
`ProjectDefinesHooks`/`RuntimeIssue`（~150 行）。注意 `windows_compat.go` 的
`windowsBatch*` 在 linux 视角下"不可达"但被 `//go:build windows` 文件调用——**不要删**。

### 0.2 为"不存在的 frontend"维护的死 API（~850 行）——最大单项

代码注释反复引用 HTTP/SSE server、desktop app（Composer.tsx）、ACP、bot 等
frontend，但 git 历史中从未存在：

- **control turn 入口面（~390 行）**：
  - `internal/control/command_dispatch.go:27` `Submit`、`:34` `SubmitHTTP`、`:48` `SubmitDeliveryRecovery`、`:60` `SubmitInvocationDisplay`（+ `submitInvocations`、`InvocationRequest`）、`:125` `SubmitEditedDisplay`、`:132` `SubmitUserTurn`、`:156` `submitHTTP`
  - `internal/control/controller.go:845` `Send`、`:1248` `TrySteer`、`:1259` `Steer`、`:1273` `submitSteerFallback`、`:1283` `SteerConsumed`、`:1354` `ReplayPendingPrompts`、`:1770` `InheritLifecycleFrom`、`:1815` `CancelJob`、`:1825` `WorkspaceLeaseState`、`:574` `SetOnSessionRecovered`、`:1904` `SetBypass`、`:1910` `SetMode`（`ApplyMode` 唯一调用者且自身零调用）、`:1945` `SaveDoc`、`:1766` `ImageInputEnabled`
  - `internal/control/stats.go:121-160` `ToolResultData`+`ToolResult`
  - 其他零生产调用：`SnapshotForShutdown`（session_lifecycle.go:370）、`RecoveryMetrics`/`DrainRecoveryMetrics`（recovery.go:226/242）、`AutoResearchSummary/List/Findings`、`RecordAutoResearchEvidence`（auto_research.go）、`RunSubagentProfile`（hooks_lifecycle.go:68）、`ConnectMCPServer`（mcp_manage.go:71）、`UnregisterMCPServerTools`（mcp_manage.go:370）、`RegisterExternalFolderRef`（refs.go:202）
  - 实际活入口只有三个：`SendWithRaw`（chat_tui_session.go:90）、`SubmitDisplay`（chat_tui_commands.go:225）、`RunTurn`（cmd/corvus-exec/main.go:250）
  - 保留：`Agent.Steer`/`consumeSteer`/`closeSteerIntakeIfIdle`/`RecordUnappliedSteer`（agent 自身异常退出路径在用，recovery_gate.go:232）
- **agent `TextSink` + `Renderer`（~270 行）**：`agent/textsink.go` 全部 9 个函数（含 `NewTextSink`/`FormatUsageLine`/`CompactArgs`/`SetShowReasoning`）仅测试引用；`agent.go:76-78` `Renderer` 接口随之删。
- **`control/port.go` 子接口分类（8/11 个接口零外部消费者，~120 行）**：`Lifecycle`(:35)/`TurnControl`(:50)/`Approvals`(:76)/`Goals`(:96)/`SessionHistory`(:115)/`SessionPersistence`(:195)/`Input`(:208)/`Settings`(:218)。外部在用：`SessionAPI`/`Capabilities`/`Status`。`MemoryControl` 仅 control 包内自用。

### 0.3 死方法/死符号簇（~700 行，61 个零生产调用者的导出符号）

- `plugin.Host` 的 `Start`/`StartAll`/`StartAvailable`/`StartPolicy`（plugin.go:251-479，~195 行；生产走 boot_plugins.go 的 `EnsureConnectedWithLifecycle`+`LazyToolset`）；`EnsureConnected`(:1208)/`AddWithLifecycle`(:1233) 为零调用纯转发。
- `jobs` 9 个 legacy unscoped 包装（`jobs.go:320` `Start`、`:863` `Output`、`:961` `Kill`、`:995` `Wait`、`:1076` `Running`、`:1177` `DrainCompletedNote`、`:1213` `SetActiveSession`、`:1679` `DestroySession`、`:1974` `LeaseEvidenceForSession`——各自转发 `*ForSession("")`）+ `WithTeardownGrace`(:202) + `ListArtifactViews`(artifacts.go:68) + `DoneChannels`(:108)；23 个测试函数只测这些壳。`TryLeaseEvidenceForSession`(:1989) 是未导出孪生的纯改名转发，合并。
- **checkpoint**：`MutationRecord`(types.go:80)、`Store.SetSessionID`(:204)、`Store.LastUndoTransactionID`(:242)、`Store.InvalidateUndo`(:255)、`Store.Blobs`(:196)、`Store.SetConversationForward`(transaction.go:861)、`BlobStore.Dir`/`.Remove`(blob.go:28/88)、`MutationBarrier.TryEnterWrite`(barrier.go:104)、`MutationObserver.HasActiveWriters`(observer.go:212)、consts `CaptureLegacy`/`CaptureManual`/`ConflictTypeChange`/`ConflictBoundaryInvalid`(types.go:56,57,128,134)、`Store.RestoreCode`(checkpoint.go:996)、`MutationBarrier.EnterExclusive`/`.Busy`(barrier.go:136,176)；`Store.Snapshot`(checkpoint.go:367) 为 legacy 内部 shim，inline 或 unexport。
- **evidence**：`IsReviewSkillTool`(meta.go:31)、`ReviewReportReceipt`(review_report.go:152)、`Ledger.MergeChildren`(child.go:73)、`Ledger.HasSuccessfulAcceptanceCriteria`(evidence.go:878)、`Ledger.MatchLatestTodoStep`(:1166)、`Ledger.HasSuccessfulStructuredReviewAfter`(review_report.go:194)。
- **recovery**：`NewSession`(reviewer.go:82，`NewSessionWithSink` 的默认参数转发)、`IsHighRiskMutation`(rules.go:117)、`TaskGrantKey`(:126)、`Gate.RecordDiagnosis`(gate.go:1167)、`IsVerificationCall`(:74)、`ClassifyEmptySearch`(:164)、consts `PauseMessageEN`/`PauseMessageZH`/`FinalizationNudge`(budget.go:39-43)/`ApprovalKindTool`/`ApprovalKindPlan`(types.go:176-177)。
- **guardian**：`Session.ReviewVerdict`(guardian.go:135)、`Close`("no-op for now"，guardian.go:436)。
- **memory**：`FindOverrides`(auto_recall.go:77，38 行)、`ScopeAncestor`(doc.go:29)、`Set.Empty`(memory.go:86)。
- **hook**：见 0.1 配套死簇。
- **config** 8 个死导出：`AutoStartPlugins`(config.go:1305，被 `EnabledPlugins` 取代)、`LoadUserConfigReadOnly`(load.go:51)、`LoadBuiltinDefaultsForRoot`(:259，唯一调用者是下面的别名)、`LoadRecoveryDefaultsForRoot`(:273，唯一调用者是 safe_mode_test.go)、`LoadForEditWithoutCredentialsReadOnlyStrict`(:754)、`ValidateFile`(:760；兄弟 `ValidateBytes` 在用)、`ConfigFileDefinesCompactRatio`+`tomlFileDefinesKey`(:344/:331)、`RemoteKnownHostsPath`+`RemoteStateDir`(paths.go:328/:317)。
- **小函数**（各包零调用者）：`secrets.RedactError`(redact.go:205)、`secrets.RedactMessage`(:348)、`secrets.RedactMessages`(:376，带 nolint 注释引用"安全报告 D2"——执行前确认该报告已关闭)、`textutil.FitGraphemeBytes`(grapheme.go:12)、`outputstyle.DescribeList`(outputstyle.go:187，被 cli 的 renderOutputStyles 取代)、`netclient.Summary`(netclient.go:134)、`capability.Audit.RecordRouterUsage`(audit.go:158)、`pluginpkg.SetEnabled`(pluginpkg.go:317)。

### 0.4 Agent 补丁残留与 shim（"kept for compatibility"类，证据最典型）

- **3× `splitFrontmatter`**：`skill/skill.go:1433`、`command/command.go:216`、`memory/store.go:852`——注释自述 *"thin wrapper kept for test compatibility"*，为不改测试保留的 `frontmatter.Split` 转发壳。生产调用点仅 4 处，其余全是测试；删壳并把测试指向 `frontmatter.Split`。
- `planmode.ReadOnlyCommandTrust`（policy.go:48）——注释"retained for source compatibility"，含测试零引用。
- `netpolicy.ParseDecision`（netpolicy.go:64）——文档自述"mirroring internal/permission"，零调用者（活的 `permission.ParseDecision` 在 permission.go:179）。枚举本体两份重复保留（两个 leaf 包，不值得为 ~30 行引入依赖方向）。
- **ADR-0002 违反**：`agent/coordinator.go:757` `truncateRunes` 手写 rune 切片，会把 emoji/ZWJ 字素簇切半——ADR 明令截断必须走 `textutil.TruncateGraphemes`。另两处同名（`tool/sessiontool/sessiontool.go:205`、`tool/builtin/web_search.go:357`）已合规但是省略号不一致（`...` vs `…`），且函数名"Runes"名不副实。
- `config/load.go:1287` `normalizeLegacyStepFunBaseURLs`——注释化的 `return false`（决策："用户配置不推断重写"），4 个常量仅测试引用；决策应入 ADR，删函数+常量+两处调用+对应测试（~25 行）。
- `config/dotenv.go` 重复键检测：`detectDotEnvDuplicateKeys`(:137) 逐行 O(n²) 重解析 + 整文件重读，产出 `Duplicates` 字段唯一读者是零调用的 `warnings()`（~45 行）。
- cli test-only 转发器：`setupProfile`(cli.go:103)、`configureCLITheme`(theme.go:156)、`resolveCLITheme`(:176)、`renderCheatsheet`(cheatsheet.go:74)、`mcpManager.render`(mcp_manager_view.go:20)、`FrameLines`(select.go:510，注释"exported for testing")、`CacheInvalidationNotice`+3 常量（cache_invalidation_notice.go:19）、`eventSink.DroppedEvents`(chat_tui.go:957)。
- gitStatus 导出渲染链（`gitstatus.go:144-199` `Render`/`RenderRepo`/`RenderWithin`/`compactIdentity`/`dirtyPlain`）只互相调用，生产走未导出的 `m.gitStatus.render`（~60 行）。
- `recovery/rules.go:114-121` `IsHighRiskMutation`/`TaskGrantKey` 注释"kept for event compatibility"，零调用。
- 冗余参数显式丢弃（签名保留、逻辑已删）：`recovery/rules.go:84` (`_ = readOnly`)、`:997`、`recovery/reviewer.go:392`、`checkpoint/transaction.go:84,1551`、`cli/transcript.go:407-433` (`_ = named; _ = indent` ×2，连 `markerAssistantNamed` 都是为喂它而穿线的)。

---

## P1 — 重复逻辑收敛（零风险，条件是输出字节保持一致）

| # | 重复 | 位置 | 估计 |
|---|---|---|---|
| 1 | `selectOne`/`selectMany` 结构克隆（raw-mode 设置/搜索态/滚动钳制/redraw 闭包/搜索键处理全套×2） | `cli/select.go:81-489` | ~120 行 |
| 2 | 5 处 `pendingModelSwitch = func()` 闭包近同（model/effort/work_mode/runtime_rebuild/skill_hooks） | `cli/model.go:78`、`effort.go:108`、`work_mode.go:132`、`runtime_rebuild.go:57`、`skill_hooks.go:226` | ~70 行 |
| 3 | 8 个手写列表选择器重复 j/k/enter/esc 导航环（quick_picker 是共享件但只覆盖 4 处） | `cli/copy_picker.go:33`、`resume_picker.go:86`、`mcp_import_picker.go:40`、`skill_picker.go:145`、`rewind.go:52`、`chooser.go:88` | ~70 行 |
| 4 | 3 个遗留键迁移函数逐字节相同（仅 section/keys 不同），且在 boot 侧放大为 3 对 (bool,error) + 6 结构体字段 + 6 位置参数 + 3 段同构 notice 块 | `config/load.go:965,1006,1044` + `boot/boot_config.go:26-49` + `boot.go:166` + `boot_notices.go:30-70` | ~110 行 |
| 5 | 同包两套手写 TOML lexer（edit.go 五种字符串类型 vs load.go 多行状态机，解决同一问题） | `config/edit.go:1554-1670`、`load.go:1117-1258` | ~60 行 |
| 6 | 两条 load 路径 normalize 管道 18/20 调用同序手工同步 | `config/load.go:226-244` vs `:827-846` | 风险消除为主 |
| 7 | 三方言 provider 拷贝 SSE 脚手架：`sendChunk`（openai:691 vs responses:608 逐字同）、`cleanCustomHeaders`/`applyCustomHeaders`（anthropic:222 vs openai:397）、bufPool、`newHTTPClient`、120s 超时常量 | `provider/{anthropic,openai,responses}` | ~100 行；openai 变体额外丢弃空值，参数化保留 |
| 8 | MCP 工具三层适配器各重声明 ~10 个纯转发方法（Name/Description/Schema/ReadOnly + 5 个 MCP 元数据方法） | `agent/usecapability.go:436+`（runtimeBoundMCPTool）、`plugin/lazy.go:223+`、`plugin/plugin.go:1949+`（remoteTool） | ~60–80 行；嵌入共享 `mcpMeta` 结构，断言顺序照抄 usecapability.go:444-455 |
| 9 | 审批请求望远镜：`requestApproval`→`WithReason`→`WithReasonOptions`→`DecisionWithOptions`（4 跳 shim）；注册链 6 函数为 1 个注册器 | `control/controller.go:2336-2400`、`approval.go:229-252`；另有 `preApprovedForRequiredHuman` 重复 requireHuman 分支、`requiresFreshApprovalTool` 纯别名 | ~70 行 |
| 10 | `firstNonEmpty` ×7 且**三种语义漂移**：纯判空（recovery/types.go:235）/ TrimSpace 后返回裁剪值（boot_subagent.go:213）/ TrimSpace 判断却返回原值（control:2272、cli:12、agent/save:852；pluginpkg、installsource 待核） | 7 处 | 多数可换 stdlib `cmp.Or`（go 1.22+），纯删除不新增抽象 |
| 11 | SSRF resolve-and-vet dialer 双份（SplitHostPort→LookupIPAddr→遍历 BlockedIP→同一条错误串） | `ssrfguard/ssrfguard.go:60-75` vs `installsource/ssrf.go:41-59` | ~19 行；ssrfguard 导出 GuardedDialer(inner) |
| 12 | agent 内 3 套私有 `Flock` 循环 + Windows 镜像 vs ADR-0006 的共享 `filelock` | `agent/session_lock_unix.go:16-90`、`session_lock_windows.go` | ~90–110 行；**需单独 PR**（agent 有独立 sentinel `ErrSessionFileLockHeld`/`ErrSessionLeaseHeld` 和 unlink-under-flock 语义） |
| 13 | 杂项：`uniqueStrings`×3（cli/cli_flags:59、config/fetch:126、recovery/rules:1001）、`readJSONFile`×3（autoresearch:746、pluginpkg:648、checkpoint/atomic_json:25）、gitStatus 空值守卫×3、copy 渲染孪生（`transcript.go:365/386`、`md.go:63/83`）、`quickPickerWindow` vs `visibleRange`（quick_picker.go:167 vs mcp_manager.go:421）、`filterMenuItems`/`filterIndices` 同谓词双扫、`messageCount` 双胞胎（control/stats.go:51/174）、`emitRememberResult`/`emitPlanModeReadOnlyCommandTrustResult` 同构 | 各处 | ~80 行 |
| 14 | 24 处 `json.Unmarshal(args,…)+fmt.Errorf("invalid args: %w")` 样板（18 个文件），无共享 helper | `tool/builtin/` | ~50–70 行；错误串保持逐字一致 |
| 15 | bash 参数→命令提取三份：`permission.Subject/Subjects`（permission.go:585-620）导出但零外部调用，而 `agent/recovery_gate.go:244`、`recovery/rules.go:218` 各自手写 | 见左 | ~20 行；主值是防权限语义漂移 |
| 16 | vendor URL 嗅探散在四处（openai/host.go 180 行、schema_dialect.go:13、prompt_cache_key.go:47、responses.go:94；DeepSeek 检测了两次） | `provider/` | ~25 行；host 匹配器上收 provider 根 |

---

## P2 — 结构性简化（零风险，但工作量大、需测试钉住）

1. **boot 装配参数雪崩**：`buildToolSourceConnector` 48 参、`buildExecutorAndPlanner` 26 参、`buildSkillTools` 23 参、`buildController` ~45 参调用点……合计 ~213 个位置参数在 15 个 helper 间穿线（`boot.go:158-229`）。同一批参数（root/reg/proxySpec/pluginHost/execProv/subagentStore/headlessGate/keepPolicy/workspaceLease/skillStore/tokenEconomy…）在三个 build* 间重复出现。一个**包内未导出** `assembly` 结构体逐步累积即可消掉 ~150–200 行纯管道（不新增跨包抽象），与包文档"frontends only pass a sink and knobs"的定位相符。
2. **`RenderTOMLForScope`/`RenderTOMLProjectDelta` 手写字段枚举**（render.go:32-549 共 518 行、:576-1020 共 445 行）：同一结构集两遍 `if c.X != d.X`，新增选项必须两处同步否则某 scope 静默 round-trip 错误。表驱动（name/getter/formatter + scope 谓词）可省 ~250 行——**先确认存在 golden test 钉住输出字节**再动。
3. **`PlanModeReadOnlyTrustGate` 三包写死链（~150 行）**：agent 侧接口+字段+setter+wiring（agent.go:211-223,367,683,1065,1219；coordinator.go:282）只写不读；control 侧 approver+持久化（controller.go:2071,2142-2177,2463-2479；approval.go:279-332；boot_controller.go:203-210；boot_hooks.go:127-131）持久化的 map 永远不会有条目；config render 已自标"legacy compatibility only"。连 TUI 死分支（chat_tui_approval.go:81,325）。
4. **config 每文件 5–6 次 TOML 解码**：`mergeTOMLPlugins`/`mergeTOMLProviders`/`mergeTOMLProviderAccess` 各自重复 stat+decode 前奏（load.go:92→903/906/912、469、510、661、711）。decode 一次进 `map[path]decodedSource` 供三个 merge 读。**注意**：`mergeFileSnapshotWithRead` 内部的多次解码是防 BurntSushi 局部突变的刻意设计（load.go:897-901 注释），保留。
5. **`envFileValue` 每凭据键 5–7 次全文件重读重析**（dotenv.go:168，经 credentials.go:607-848 多层调用），乘以 provider 数——一次 `Load()` 的主要成本。按路径缓存 `dotEnvFile` 即可（~15 行改动）。
6. **control 小结构项**：`turnOrchestrator` 空壳（turn_orchestrator.go:23-50，每方法体都是 `c := o.c`，无自有状态，摊平为 Controller 方法）；`RuntimeStatus.Cancellable`/`.CancelRequested`（stats.go:314-317）计算后无读者；`pendingApproval.kind` 文档三值只用两值；两段 turn 前奏 ~30 行相同（turn_orchestrator.go:100-141 vs 178-232）；`runGoalLoopWithRawDisplay` 双胞胎（:349-373）；`withCallContext`/`WithToolCallContext` 完全相同（agent.go:110-123）；5 处 `c.runner.(interface{ Set… })` 鸭子类型断言实际恒成功（controller.go:1047-1071，换命名接口）。
7. **cli 小结构项**：`resumePicker` 生产不可达 fallback（resume_picker.go:86-105，仅测试构造无 quick 的实例）+ `sessions`/`sel` 与 `quick.items`/`quick.selected` 双份状态手工同步（:70-81）；`renderToolCardCollapsed`/`renderToolCardExpandedWithOutcome` 只差一个调用（toolcard.go:421-443）；`footerHint`/`compactFooterHint` 平行 5 路 switch（mcp_manager_view.go:45-72）；rewind 5 个字母快捷键 4 行重复（rewind.go:94-118）；theme.go 样式默认守卫重复（:180-191 vs 215-231）。
8. **provider/vendor 杂项**：`NewCoordinator`（coordinator.go:143，自述"compatibility adapter for older tests"）、`TaskWarrantsPlanner`/`NewPlannerGate`（planner_gate.go:673/684）、`NewReadOnlyAgent`（task.go:1914）均零生产调用；`NormalizeSession`（normalize.go:24）26 行包装单调用点，unexport。
9. **测试专用生产代码**：`provider/resolver.go:46` `StaticResolver`（注释"small deterministic test double"）、`tool/contract.go:67` `RenderContractMarkdown`（注释"Tests use"）——移入 `_test.go`。

---

## P3 — 需要决策或有微小行为边界（不动则保持现状）

| 项 | 位置 | 说明 |
|---|---|---|
| `web_search` nil-client 兜底 | `tool/builtin/web_search.go:136-139` | 仓内唯一的裸 `&http.Client{}`，生产不可达（boot 与 mcp-server 都注入 netclient 客户端）但违背 ADR-0004；建议构造时拒绝 nil、删兜底。改 web_search_test.go 的 nil 用例 |
| PyPI 解析走 `http.DefaultClient` | `plugin/launcher_lock.go:275-286` | **真实影子出口**——uvx 包解析绕过用户代理策略（ADR-0004 漏网）。改 netclient 对代理用户**是**行为变化（即 ADR 意图），需拍板 |
| 死弃用 config 字段 | `config/config.go:731-732,742,770,777`（`MaxSteps`/`PlannerMaxSteps`/`RecoveryTemperature`/`AutoPlan`/`AutoPlanClassifier`） | 删除后旧配置的迁移 notice（`IgnoredLegacyAgentStepLimits` 等）出现时机会变。严格零变化则只删 `SetAutoPlan`/`SetUIShortcutLayout` 不删字段。另：`agent.temperature` 放在"Deprecated compatibility fields"注释块里但实际在用——注释误导，改注释 |
| `ui.shortcut_layout` | `config/config.go:195`；`cli/chat_tui_modes.go:34-36` | 唯一作用是 Auto 模式标签显示 "Auto" vs "Auto+Approve"——用户可见文案，删除即变化，需拍板 |
| `.scratch/dep-audit/FINDINGS.md` 在库内 | 仓库根 | 上轮审计标注 D-3"待用户决定"；决策记录可移 `docs/adr` 或删 |
| Makefile↔CI 双份 race 包列表 + gofmt 检查块 | `Makefile` / `.github/workflows/ci.yml` | Makefile 注释声明刻意镜像；可让 CI 调 `make check` 消重，价值一般 |
| `ProviderEntry.Price` 派生字段 | `config/config.go:824-833` | `applyModelPrice`(:1020) 每次解析都从 `Prices[Model]` 覆写——字段含义随生命周期变化。改 `PriceForModel(model)`，消费点（boot/resolver.go:36、render 路径）需逐一确认模型作用域 |
| `billing`/`sysproxy`/`autoresearch`/`installsource`/`lsp`/`history`/`stats`/`mcpserver` 单消费者包 | — | 审计结论：**全部保留**。platform 拆分（sysproxy）、协议边界（lsp/mcpserver）、内聚引擎（autoresearch/installsource）、工具形态（history，同 tool/builtin）、注册中心在 boot 的必然单导入（stats）——折叠均为负价值 |
| ~312 个仅包内使用的导出符号 | recovery 56、checkpoint 67、hook 45、memory 16、installsource 17、jobs 13、evidence 12、skill 12、guardian 10、lsp 8；另 plugin/capability/provider/pluginpkg/permission/tool ~80 个 | 机械 unexport，可分批，不减行数只减 API 面。高信号例：`hook.Run`（15 内部调用 0 外部）、`checkpoint.Digest`/`FingerprintPath` 等 |
| agent ~100 个仅包内导出 + 3 个全死 | `SessionEventIndexPath`/`SessionEventLogPath`、control 的 `SaveAttachmentFile`（test-only） | 同上机械处理 |
| `capability` 的 `SkillEntries` 等过导出 | capability 包 | 同上 |

**明确不动**（各路验证为合理设计，避免过度重构）：

- ADR 认可的薄包装：`visibleWidth`（ADR-0002）、`netclient.ProxyFunc`/`Validate`（公共 API 边界）、`gitcmd.Command`/`Args`（GOOS seam + 便捷）、`billing.Display`（文档化 legacy）、`retrieval.QueryTerms`（含真实校验）、`proc.StartTrackedRequired`（平台对等）。
- 单实现但为打破 import cycle 的接口：`ToolHooks`、`ConversationApplier`（checkpoint→control 边界）、`memory.Queue`（活上下文管道）、`recovery.UsageSink`（10+ 实现，真接口）、`tool/builtin/clientio.FileOverlay`/`TerminalRunner`（文档引用 ACP host transport，消费点存在，仅缺生产者——保留 ~15 行管道）。
- 多实现真接口：`Previewer`(6)、`ImageTool`(4)、`PlanModeClassifier`(5)、`provider.Provider`(3)、`plugin.transport`(3)、`agent.Runner`(2)、`Gate`(3+)。
- 包体量合理：`i18n`（47 个导入者）、`evidence`（22）、`proc`（真实平台层）、`shellparse` vs `shellsafe`（解析器 vs 分类表，非重复）、`filelock` vs `workspacelease`（后者调用前者，即 ADR-0006 本身）、`sysproxy` vs `netpolicy`（正交）、`secrets` vs `config`（只有迁移）、原子写单一实现（fileutil/atomicwrite.go，16 文件 10 包在用）。
- Windows 专属代码、`hook.ParseOutput` 654 行大函数（活行为，分解非零风险）、`run_loop.go` 缺失推理恢复 + `reasoning_warn_state.go`（DeepSeek 网关怪癖的对症处理，有审计事件，活行为）、`plugin.go:286` 空 tools/list 重试（有生产者有测试的已接受 workaround）。
- TUI 的 "defensive/legacy" 注释（chat_tui_session.go:98,129 等 7 处）：各有具体历史原因，非可删创可贴。

---

## 建议执行顺序

1. **P0 一次战役**（净删 ~2,600–3,000 行 + ~30 个孤儿测试函数）：按包分 commit
   （dead-files → control/agent 死 API → jobs/plugin 死簇 → config 死导出 → shim 清理），
   每步 `make check`。风险最低、收益最大，并彻底兑现上轮 ADR 未竟的收口。
2. **P1 按包分批**：先做有字节级钉子测试的（select、TOML 迁移、provider SSE）；
   `firstNonEmpty`→`cmp.Or` 顺手清；#12（文件锁）单独 PR。
3. **P2 各自独立 PR**：boot assembly struct 与 RenderTOML 表驱动是两个最大结构项，
   各自先补/确认 golden test 再动；其余按包分批。
4. **P3 逐项拍板**：优先两个网络出口项（web_search nil 兜底、launcher_lock DefaultClient），
   再处理死 config 字段与 shortcut_layout。

---

## 附：审计覆盖与验证方式

- 6 个子代理 + 主线程复核；死代码主张 100% 经全仓 grep（生产+测试分开计）亲验，
  中型包一路另用 go/types `packages.Load` Uses 图 + `golang.org/x/tools/cmd/deadcode`
  （从 4 个 main 做 RTA）双验；12 个中型包无反射使用，零引用即可证死。
- 未覆盖：仅测试代码的质量问题（孤儿测试随对应死代码一并删除，未单独审计）。
- 行号对应 2026-08-16 工作区（main @ 8582ea9）。

---

## 执行记录（2026-08-16 收尾，main @ 90d8377）

14 个 commit（8582ea9..90d8377），净 **−7,853 行**（+2,933 / −10,786）。每批 gofmt + `go build` +
分包 `go test` + `go vet` 后提交；收尾 `make check` 的 vet / fmt-check / 全量 test 通过
（`-race` 需 cgo + gcc，本机 WSL 无 gcc，该腿由 CI 执行——环境限制，非代码问题）。

| 批 | commit | 覆盖 |
|---|---|---|
| P0.1 | 3db5d2d | 死文件簇 + hook 检查器依赖簇（−2,122 净） |
| P0.2 | 7fbfe39 | control 死 turn-entry API、port.go 子接口、scoped-ref 机器（−2,222 净） |
| P0.3a | 1346656 | agent 测试专属面 + truncateRunes ADR-0002 修复（−655 净） |
| P0.3b | 8e4787d | jobs/plugin 旧包装与死符号（−765 净） |
| P0.3c | 79cc5e3 | 61 符号死导出表（checkpoint/evidence/recovery/guardian/memory…，−613 净） |
| P0.4+P1 | 4aa2d6a | config 死导出、ADR-0008 base_url 收归用户、迁移三胞胎合一、TOML lexer 统一 |
| P0.4+P1 | c62c5db | cli 死转发、select 引擎合一、modelSwitch 闭包、窗口计算去重 |
| P1 | b7c01b5 | provider SSE 脚手架上收、vendor 嗅探合一、decodeArgs |
| P1 | 69f9634 | firstNonEmpty 三语义收敛、GuardedDialContext 合一、审批望远镜折叠 |
| P2 | 206058e | boot assembly 阶段总线（~120 个位置参数 → 嵌入字段提升） |
| P2 | d0834f0 | PlanModeReadOnlyTrustGate 三包写死链删除 + turnOrchestrator 拍平（410da8c） |
| P2 | 9cc6087 | TOML 每次加载只解码一次、dotenv 按 mtime+size 缓存、render 字节钉子测试 |
| P3 | 90d8377 | 网络出口收口、死 setter 删除、文档化保留项 |

### P3 决策结果

| 项 | 结果 |
|---|---|
| `web_search` nil-client 兜底 | **已改**：构造时拒绝 nil（ADR-0004），Execute 的 nil 守卫排在引擎校验后供测试字面量 |
| PyPI 解析 `http.DefaultClient` | **已改**：走 `netclient.NewHTTPClient`（代理用户可见的行为变化 = ADR-0004 意图） |
| 死弃用 config 字段 | **删 setter 不删字段**：`SetAutoPlan`/`SetUIShortcutLayout` 删除；`MaxSteps`/`PlannerMaxSteps`/`RecoveryTemperature`/`AutoPlan*` 保留（删除会改变迁移 notice 出现时机）；`agent.temperature` 注释已改标 live |
| `ui.shortcut_layout` | **保留**（用户可见文案：Auto vs Auto+Approve 标签），getter 归一化有测试钉住 |
| `.scratch/dep-audit` | **待拍板**（D-3 沿袭上轮，未动） |
| Makefile↔CI 双份列表 | **保留**：注释声明刻意镜像，收编价值一般 |
| `Price`→`PriceForModel` | **误报拒绝**：`ResolveModel` 已跑 `applyModelPrice()`，boot 所有 `.Price` 读点（a.entry、subagent、descriptor）拿到的都是模型作用域价；`Descriptors()` 显式用 `PriceForModel(e.Model)`；render 双字段是有意序列化 |
| 单消费者包 / 机械 unexport 大表 | **按"明确不动"清单保留**；61 符号表已在 P0.3c 执行，其余经 uses 图复核为活引用或负价值折叠 |

### 误报与验证保留项（细节见各 commit message）

- `capability.RecordRouterUsage`：审计误报——SemanticRouter 活引用（79cc5e3）。
- secrets `RedactError/RedactMessage(s)`：其"保留理由"（security report D2 导出路径）在本仓不存在，已删（79cc5e3）。
- anthropic 默认 TransportOptions client 与 openai/responses 共享调优 client 的不对称：改了会变行为，**documented keep**（b7c01b5）。
- FINDINGS #8 mcpMeta embed：三个 MCP 适配器的元数据转发**不能**靠嵌入消掉——动态值需在包装层重新声明方法（b7c01b5）。
- `readJSONFile` ×3、emitRemember/emitTrust 对、guardian subject 提取：三处各有真实差异（UTF-8/错误包装/os.Root、并行 i18n 键、审查提示词关注点），合一需要开关参数，**保留**（69f9634）。
- config/fetch 的 uniqueStrings：**有意不 trim**，与 `textutil.UniqueNonBlank` 合并会改变匹配候选，保留（69f9634）。
- cli 8 个底部 picker 的 j/k/enter 循环：handler 在阶段、键集、副作用上真实分叉，**不合并**（c62c5db）。
- duck-typed runner 断言（`SetPlannerPlanApprover` 等仅 Coordinator 有）：改命名接口会失去"总是成功"语义，yolo_test.go:670 钉住，**保留**（d0834f0）。
- turn-prelude 去重、goal-loop 双胞胎合并、`withCallContext`：验证后不成立或已消失，**拒绝**（410da8c）。
- RenderTOML 表驱动重写：**拒绝**——两个序列化器输出风格真实不同（注解+回退+填充 vs 精简 delta），描述符行数 ≈ 新抽象行数；漂移风险改由 `TestRenderTOMLScopesAreByteStable` 钉住，该钉子测试还揪出两个真实字节不稳定 bug（并发 0 显式渲染、network_policy 空默认渲染）（9cc6087）。

### 新增 ADR

- **ADR-0008**（provider base_url 用户所有）：P0.4 批删除 `normalizeLegacyStepFunBaseURLs` 时记录——运行时不得改写用户配置的 base_url（4aa2d6a）。
