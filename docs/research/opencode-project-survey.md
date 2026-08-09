# OpenCode 项目调研报告

> 调研对象：**sst/opencode**（现 **anomalyco/opencode**，仓库已迁移）—— 开源 AI coding agent。  
> 目的：回答「opencode 是什么、怎么做到的、Corvus 可借鉴什么？」  
> 方法：GitHub 仓库页 / GitHub API / release / 源码（raw.githubusercontent.com dev 分支）/ 官方文档 opencode.ai/docs 交叉阅读；Corvus 对照本仓实现。  
> 日期：2026-08-08

---

## 1. 结论摘要

### 1.1 一句话定位

**"The open source AI coding agent."** —— 一个以终端 TUI 为主要交互形态的开源 AI 编程 agent（对标 Claude Code），但内核是 **client-server 架构的 agent 引擎**：`opencode server` 是无头 HTTP 服务，TUI / Web / Desktop / IDE 扩展都是它的客户端。

### 1.2 关键事实（2026-08-08 实测）

| 项 | 值 |
|---|---|
| 仓库 | `anomalyco/opencode`（原 sst/opencode，自动重定向；Anomaly 公司，anoma.ly） |
| Star / Fork | **195k** / 24.9k |
| 许可证 | MIT |
| 主要语言 | TypeScript（Bun 运行时） |
| 最新版本 | v1.18.15（2026-08-07） |
| 活跃度 | dev 分支 15k+ commits，提交至 2026-08-08，release 由 `opencode-agent[bot]` 自动生成 |
| 安装 | `curl -fsSL https://opencode.ai/install | bash`；npm/bun/pnpm/yarn、brew、pacman、mise、nix、scoop/choco、Desktop app |
| 技术栈 | Vercel AI SDK + models.dev 模型目录、SQLite（drizzle-orm）、Effect 函数式运行时、**OpenTUI（SolidJS）** 终端 UI（早期曾用 Ink/React，已迁移）、yargs CLI、@clack/prompts |

### 1.3 对 Corvus 的总体判断

1. **架构分叉是最大差异**：opencode 是 server/client 分离（TUI 只是客户端，另有 serve/web/attach/SDK/ACP 多形态）；Corvus 是单进程 TUI 直连 agent 循环，非 TTY 直接退出。Corvus 的 agent 核心已与 UI 解耦（`internal/agent/run_loop.go` + `internal/event`），加 headless server 是最高杠杆的借鉴项。
2. **功能广度 opencode 更大**（75+ provider、JS 插件生态、自定义 agent/command、share、GitHub/GitLab 集成）；**安全纵深 Corvus 更强**（checkpoint 事务、失败恢复门控、seatbelt 沙箱、脱敏、i18n、代码索引等 opencode 没有）。
3. **Corvus 已有能力不弱于 opencode**：checkpoint + `/rewind` 覆盖并超越其 `/undo`；MCP 宿主（transport/lazy/热加载）比 opencode 的 MCP 更工程化；记忆系统是 opencode 没有的产品级能力。
4. 注意事实校正：Corvus 的 `internal/plugin/` 实际是 **MCP 服务器宿主**，不是 JS 插件系统；真正的通用扩展点是 `internal/hook`（外部 shell 钩子，Claude Code 式）。

---

## 2. 项目概况

- **定位口号**：README 顶部 "The open source AI coding agent."；文档标题 "AI coding agent built for the terminal"。
- **是 agent 还是 TUI？** 两者都是。核心是 client-server agent 引擎；`opencode` 无参数运行默认启动 TUI。
- **品牌保护**：README 要求第三方同名项目（如 opencode-dashboard）必须声明"非官方构建、与 OpenCode 团队无关"。
- **YOLO 安装**：README 对一键脚本的戏称，强调零摩擦上手。

---

## 3. 功能全景

### 3.1 CLI 命令（`opencode [project]`，支持 `--continue/-c`、`--session/-s`、`--fork`、`--prompt`、`--model/-m`、`--agent`、`--auto`、`--port`、`--hostname`、`--mdns`、`--cors`）

| 子命令 | 作用 |
|---|---|
| `agent create / list` | 交互式创建自定义 agent（mode/permissions/model） |
| `attach [url]` | 把 TUI 挂到已运行的 serve/web 后端（支持远程、basic auth） |
| `auth login / list / logout` | provider 凭据管理（存 `~/.local/share/opencode/auth.json`） |
| `github install / run` | 安装 GitHub App + Actions workflow，CI 中运行 |
| `mcp add / list / auth / logout / debug` | MCP 管理（含 OAuth） |
| `models [provider]` | 列出模型（`--refresh`、`--verbose`） |
| `run [message..]` | **非交互单次运行**：`--format json` 原始事件、`--attach` 复用常驻 server、`--share`、`--thinking`、`--variant`、`--auto` |
| `serve` | 无头 HTTP server（OpenAPI 接口），`--port/--hostname/--mdns/--cors` |
| `session list / delete` | 会话管理（`--format table|json`） |
| `stats` | token 用量/费用统计（`--days/--tools/--models/--project`） |
| `export / import` | 会话 JSON 导出（`--sanitize` 脱敏）/ 从文件或分享链接导入 |
| `web` | 无头 server + 浏览器 Web UI |
| `acp` | Agent Client Protocol server（stdio nd-JSON），供 Zed/JetBrains/Neovim 调用 |
| `plugin <module> / plug` | 安装插件（`--global`、`--force`） |
| `pr <number>` | 拉取并 checkout GitHub PR 后运行 |
| `db [query]` | 数据库工具 |
| `upgrade [target]` | 自升级 |
| `debug / uninstall` | 调试工具 / 卸载 |

全局 flags：`--pure`（不带插件运行）、`--print-logs`、`--log-level`；30+ 环境变量（`OPENCODE_SERVER_PASSWORD/USERNAME`、`OPENCODE_CONFIG`、`OPENCODE_PERMISSION`、`OPENCODE_EXPERIMENTAL_*` 系列：plan mode、background subagents、parallel web search、workspaces、scout 等）。

### 3.2 TUI 界面

- **斜杠命令**：`/connect`、`/compact`（=/summarize）、`/details`、`/editor`、`/exit`、`/export`、`/help`、`/init`（引导生成 AGENTS.md）、`/models`、`/new`、`/redo`、`/sessions`、`/share`、`/themes`、`/thinking`、`/undo`、`/unshare`
- **@ 文件引用**：模糊搜索插入文件（`@File#L37-42` 行号引用），引用配置也进 @ 补全
- **! bash 命令**：`!ls -la` 把 shell 输出直接注入对话
- **leader key**：默认 `ctrl+x`，组合键（`c` compact、`n` new、`l` sessions、`u` undo、`r` redo、`t` themes、`m` models、`e` editor、`x` export、`q` exit），全部可重绑/禁用
- **命令面板** `ctrl+p`（切换持久设置）；`Tab` 切换 build/plan
- **tui.json 配置**：`theme`、`keybinds`、`leader_timeout`、`scroll_speed`、`scroll_acceleration`、`diff_style`（auto/stacked）、`cursor` 样式、`mouse`、`attention`（桌面通知+音效）
- **/undo 与 /redo 基于 Git** 回滚文件改动（需 Git 仓库）

### 3.3 Provider 与认证

- 基于 AI SDK + models.dev，**75+ providers**（Anthropic、OpenAI、DeepSeek、Groq、Ollama、LM Studio、OpenRouter、GitHub Copilot、Bedrock、Vertex AI、OpenCode Zen、OpenCode Go 等；自定义 OpenAI-compatible provider）
- 推荐模型：GPT 5.2、GPT 5.1 Codex、Claude Opus 4.5、Claude Sonnet 4.5、Minimax M2.1、Gemini 3 Pro
- **模型变体（variants）**：推理 effort 档位，`ctrl+t` 循环切换
- 认证：`auth login`（models.dev 驱动）、TUI `/connect` 交互式（含 "Other" 自定义）、浏览器 OAuth（xAI、Snowflake、GitHub Copilot）、API key、`.env`、MCP server OAuth、server basic auth

### 3.4 Agent 模式

- **build**（默认，全权限）与 **plan**（只读，编辑拒绝、bash 需询问），`Tab` 切换
- **general 子代理**：内部用于复杂搜索/多步任务，`@general` 调用；`@agent` 唤起自定义 agent
- 内置 subagents：build、plan、general、explore、scout、compaction、title、summary
- **自定义 agent**：JSON 或 Markdown frontmatter（`name/description/temperature/max_steps/model/permissions/mode/hidden/color/top_p`），`mode: all|primary|subagent`；`agent create` 交互式生成
- 实验性：background subagents、plan mode

### 3.5 会话 / 工具 / LSP / MCP / 插件

- **会话**：列表/切换/重命名/删除/fork/时间线、自动 compact、`export/import`、`stats`、SSE 事件流
- **工具**（12 个内置）：bash、edit、write、read、grep、glob、lsp（实验）、apply_patch、skill、todowrite、webfetch、websearch（Exa 托管，免 key）、question；可自定义（JS/TS）、MCP 扩展
- **LSP**：28+ 内置 LSP server（gopls、pyright、rust、clangd、eslint 等），自动安装、自定义、按语言禁用
- **MCP**：本地（stdio）与远程（HTTP）、OAuth、自动发现预注册（Sentry、Context7、Vercel Grep）、全局/按 agent 启用
- **插件**：`.opencode/plugins/`（项目）与 `~/.config/opencode/plugins/`（全局）自动加载 JS/TS 文件或 npm 包；事件钩子（`tool.execute.before/after`、`permission.asked/replied`、`session.*`、`shell.env`、`tui.*`、`experimental.session.compacting` 等 20+ 事件）、自定义工具（`tool()` + Zod schema）、通知、环境变量注入、compaction hooks；`--pure` 禁用
- **Server / SDK**：HTTP + OpenAPI 3.1（`/doc`）、SSE 事件（`/global/event`）、sessions/messages/config/provider/files/agents/logging/TUI/auth API 分组；`@opencode-ai/sdk`（npm，类型安全 TS 客户端）+ Go SDK

### 3.6 生态形态

- **Share**：`/share` 生成 `opncd.ai/s/<id>` 公链，manual/auto/disabled 三模式，企业可禁用/SSO/自托管
- **Zen**：官方 AI 网关，精选模型、基准测试、按成本价 + 手续费、可 BYOK、无锁定（"no lock-in by allowing you to use it with any other coding agent. And always let you use any other provider with OpenCode"）
- **GitHub / GitLab**：GitHub App（comments 里 `@opencode` 或 `/oc`），Actions runner 中 issue triage / 修 bug 开 PR / 代码评审
- **IDE / Web / Desktop**：VS Code/Cursor/Windsurf/VSCodium 扩展（`Cmd+Esc` 快速启动、选区上下文共享）、`opencode web` 浏览器界面（mDNS 发现、CORS、basic auth）、Desktop app（BETA）
- **Rules / Commands**：AGENTS.md（项目/全局），**兼容 Claude Code 的 CLAUDE.md 与 ~/.claude/skills**；`/init` 引导生成；自定义斜杠命令（JSON 或 frontmatter MD，`$ARGUMENTS`/`$1`、`!` shell 注入、`@file` 引用）

---

## 4. 架构设计（dev 分支快照）

### 4.1 进程模型：server/client 分离

- **`opencode` = CLI 入口**（`packages/opencode/src/index.ts`）：yargs 注册全部子命令；middleware 设置 `AGENT=1`、`OPENCODE=1`、`OPENCODE_PID`
- **`opencode serve` = 无头 HTTP server**：OpenAPI 3.1 端点、SSE 事件总线（`server.connected`、`session.*`、`message.*` 等）；TUI / Web / Desktop / IDE 都是客户端，可 `attach` 远程复用（MCP 冷启动优化）
- **存储**：SQLite（drizzle-orm）；会话为 JSON message/part 结构；auth 凭据存 auth.json
- **monorepo**：约 35 个包（turbo + Bun）；agent 执行逻辑在 `packages/core` 与 `packages/opencode/src/agent/`（旧 `packages/agent` 已并入 core）；另有 `packages/tui`（OpenTUI）

### 4.2 扩展方式（四层）

1. **JS/TS 插件**：进程内事件 hooks + 自定义工具（Zod schema 声明），npm 生态分发、bun 自动安装
2. **配置驱动扩展**：自定义 tools / agents / commands（JSON/MD frontmatter）
3. **MCP**：stdio/HTTP + OAuth
4. **ACP**：Agent Client Protocol（编辑器集成）

### 4.3 安全模型

- **权限**：allow/ask/deny 三态、`--auto` 全自动、按工具输入 pattern 匹配（`git *`、`rm *`、`myprefix_*` 通配）、`~` 展开、external_directory 策略、doom_loop（同工具重复 3 次即 ask）；v1.1.1 起 tools 配置合并进 permission
- `.env` 默认禁读；无沙箱（依赖权限规则）

> 注：protocol/sdk 包内部与 TUI 进程模型细节为本次调研未深入部分（标注待确认）。

---

## 5. TUI 设计

- **框架**：OpenTUI（SolidJS）—— SST 团队自研终端 UI 库（`@opentui/core`、`@opentui/keymap`、`@opentui/solid`、`solid-js`、`opentui-spinner`）；早期用 Ink/React，已迁移
- **布局**：类 Claude Code 对话式界面——消息列表 + 输入框 + 侧边栏（`<leader>b`）；diff 渲染（edit 可视化）、tool 执行详情折叠、thinking 块、时间戳切换、状态栏（`<leader>s`）
- **主题**：内置 10+（tokyonight、everforest、catppuccin、gruvbox、kanagawa、nord、matrix、one-dark 等）；自定义主题 JSON（UI 色 + 语法高亮双色板，tree-sitter 高亮）；要求 truecolor；`/themes` 切换
- **与 Claude Code 异同**：明显对标——布局、`/init`、`/undo`/`/redo`、`@` 引用、AGENTS.md/CLAUDE.md 双向兼容、skills 目录兼容、Tab 切换模式；差异——完全开源 MIT、75+ provider 无锁定、自研 OpenTUI、headless server/SDK/插件/Web/Desktop 多形态、内置 LSP 反馈循环

---

## 6. 与 Corvus 对比

### 6.1 opencode 有、corvus 没有

| 维度 | opencode | corvus 现状 |
|---|---|---|
| Server/API | `serve` 无头 HTTP + OpenAPI 3.1 + SSE | 无；单进程 TUI，非 TTY 退出（`internal/cli/cli.go`） |
| SDK | `@opencode-ai/sdk`（TS + Go） | 无编程接口 |
| 多客户端 | TUI/Web/Desktop/IDE 共用 server | 唯一交互面是 Bubble Tea TUI |
| 非交互 CLI | `run "prompt"` + `--auto` | 无 run 子命令（只有 help/version） |
| Share | `/share` 公链（manual/auto/disabled） | 无（仅本地复制导出） |
| 自定义 agent | MD/JSON frontmatter 定义（subagent/primary） | 有内置 plan/subagent 路由，但不可用户扩展 |
| JS 插件生态 | 进程内 hooks + npm 分发 | 无进程内脚本插件（`internal/plugin/` 是 MCP 宿主） |
| 自定义工具/命令 | config 定义工具 + 斜杠命令 | 工具 Go 内置（blank import 注册）、slash 内置注册表 |
| websearch | Exa 托管（免 key） | 只有 webfetch |
| OAuth /connect | 浏览器 OAuth 流程 | 只有 API key（.env / keyring） |
| ACP / GitHub / GitLab | 编辑器协议 + CI 集成 | 无 |
| 自定义斜杠命令 | 用户可定义 | 只有内置 |
| Formatters | 写后格式化配置 | 无 |

### 6.2 corvus 有、opencode 没有（或明显更强）

| 维度 | corvus | opencode |
|---|---|---|
| 检查点/事务 | 原子 JSON 事务、blob、secure_path 回退（`internal/checkpoint/`） | 无（只有 revert/undo） |
| 失败恢复门控 | 重复失败防护、指纹、恢复 GC（`internal/recovery/`） | 无 |
| 沙箱 | macOS seatbelt + 逃逸防护 + 受限目录（`internal/sandbox/`） | 无沙箱，靠权限规则 |
| 项目记忆 | 项目级记忆 + 自动回忆（`internal/memory/`） | 无内置记忆（靠 AGENTS.md 文件） |
| Prompt cache 纪律 | 静态前缀 + PrefixShape 诊断 + Anthropic cache_control（调研报告另述） | 有 applyCaching，无本地诊断/失效提示 |
| 并发/租约锁 | session 租约、进程锁、workspace lease | 单进程无锁需求 |
| 敏感信息脱敏 | `internal/secrets/` 统一脱敏 | 无 |
| i18n | en/zh/zh-TW | 仅英文 UI |
| billing/stats | 余额查询、用量统计、价格表 | 计费在 Zen 云端 |
| 代码索引 | tree-sitter 本地符号索引 | grep/glob 用 ripgrep，符号靠 LSP |
| 恢复/回滚 | checkpoint 每轮一个 + `/rewind` 多阶段 | 消息级 undo/redo（Git 回滚） |

### 6.3 双方都有、实现不同

| 维度 | opencode | corvus |
|---|---|---|
| 会话 | REST CRUD + fork/children + summarize；JSON 本地存储 | 恢复/重命名/侧车元数据/迁移/并发锁；checkpoint 事务 |
| 工具面 | 12 个：bash、edit、write、read、grep、glob、lsp、apply_patch、skill、todowrite、webfetch、websearch、question | 更多：bash、read/write/edit、delete_range/delete_symbol、glob/grep/ls、codeindex、todo、webfetch、ask、multiedit、notebookedit、movefile、completestep 等；缺 apply_patch/websearch/lsp 工具 |
| question/ask | question 工具（选项导航） | ask 工具（header/question/options/multiSelect，headless 自答回退）——语义几乎一致 |
| MCP | 配置驱动 + 动态添加 + OAuth；权限 `mcp_*` 通配 | 更工程化：stdio/HTTP/SSE + lazy tier + 缓存 + 安装 + 热加载 + TUI `/mcp` 管理器 |
| LSP | 实验性 lsp 工具 + servers 配置 | 内置完整 LSP 客户端（jsonrpc/manager/tool/position） |
| 权限 | 全工具规则引擎（pattern 匹配、doom_loop、external_directory、`--auto`） | bash 审批为核心（只读分类、命令分解、YOLO 模式），覆盖面窄但深度深 |
| 扩展 | 进程内 JS hooks + 自定义工具/agent/命令 | 外部 shell hooks（PreToolUse/PostToolUse/PermissionRequest/UserPromptSubmit/Stop，JSON stdin 协议）——Claude Code 式 |
| 技能 | Anthropic Agent Skills（SKILL.md） | 内置技能 embed + 索引 + profile + 安装 + 选择器 |
| provider | 75+ 目录 + OAuth + Zen 模型目录 | 3 协议适配（anthropic/openai/responses）+ TOML 自定义 + 重试 + schema 校验；corvus.toml 已接 opencode Zen 网关 |
| 配置 | opencode.json（JSON，全局+项目） | corvus.toml（TOML）+ .env + 迁移 |

### 6.4 架构对照

| 维度 | opencode | corvus |
|---|---|---|
| 语言/运行时 | TypeScript monorepo（turbo + Bun） | Go 1.25 单模块单可执行文件 |
| 进程模型 | server-client 分离，事件总线 SSE | 单进程直连，`internal/event` 包内分发，无网络层 |
| 扩展方式 | JS 插件 / 配置工具 / MCP / ACP | Go 内置 + shell hooks / MCP / 技能 |
| 存储 | SQLite + JSON 会话文件 + auth.json | `internal/store` + checkpoint 原子事务 + 侧车元数据 + keyring |
| TUI 框架 | OpenTUI（SolidJS，自研） | Bubble Tea + Bubbles + Lipgloss（charm 生态） |

---

## 7. 对 Corvus 的可借鉴清单（按优先级）

1. **headless server + SSE 事件总线（最高杠杆）**：agent 核心已与 UI 解耦（`internal/agent/run_loop.go` + `internal/event`），加 `internal/server` 包暴露 HTTP + SSE 即可解锁 CI/自动化/多客户端。参考 opencode 的 OpenAPI 3.1 + `/event` 设计。
2. **`run` 非交互子命令 + `--auto`**：`internal/cli/cli.go` 现在非 TTY 直接退出；ask 工具已支持 headless 自答回退（`internal/agent/ask.go`）。与第 1 条共用一套改造，投入产出比最高。
3. **权限规则从 bash 泛化到全部工具**：opencode 的 pattern 匹配（`git *`/`rm *`/`mcp_*` 通配）、external_directory、doom_loop 可直接借鉴；`internal/tool` 注册表是天然拦截点。
4. **项目 AGENTS.md 规则 + `/init`**：corvus 有 boot/skill 但没有"项目自述规则"加载；复用 `internal/skill` 索引与 `internal/cli/slash_registry.go`。
5. **compact 可插拔上下文注入 hook**：对应 opencode 的 `experimental.session.compacting`；`internal/agent/compact.go` + `internal/hook` 直接对接。
6. **记忆产品化**：把 `internal/memory/` 的 recall 结果沉淀为项目内可提交文档（团队共享），而不只存本地 store。
7. **自定义斜杠命令**：`internal/cli/slash_registry.go` 目前是内置注册表，开放用户自定义（TOML/settings.json 定义模板命令）成本低收益直接。
8. **会话 fork 与 summarize**：`internal/agent/branch.go` 与 checkpoint 元数据已具备原始材料，缺用户可见入口。
9. **缓存失效可感知提示**：呼应 `docs/research/agent-prompt-caching-survey.md` 的结论（"缺少产品级 invalidation 提示"），`internal/cli/cache_invalidation_notice.go` 已有雏形，可做命中/失效原因状态栏提示。
10. **导出/分享最小实现**：`/export` 命令（HTML/JSON 快照）产品化，未来再对接自托管 share。

---

## 8. 来源

- GitHub 仓库：https://github.com/anomalyco/opencode（原 https://github.com/sst/opencode）
- GitHub API：`api.github.com/repos/anomalyco/opencode`（repo / releases/latest / commits / contents）
- 官方文档：https://opencode.ai/docs/ —— Intro、CLI、TUI、Providers、Models、Config、Agents、Permissions、Keybinds、Tools、Themes、MCP、LSP、Plugins、Server、SDK、Skills、Share、Zen、GitHub、ACP、IDE、Web、Rules、Commands
- 源码：`raw.githubusercontent.com/anomalyco/opencode/dev/packages/{opencode/src/{index.ts,server/server.ts,storage/storage.ts,plugin/loader.ts}, core/src/database/schema.gen.ts}`
- Corvus 对照：本仓 `PROJECT.md`、`cmd/corvus/main.go`、`internal/` 各包、`docs/research/agent-prompt-caching-survey.md`

## 9. 调研缺口

- protocol/sdk 包内部实现与 TUI 进程模型细节（待确认）
- 未深入：Formatters、Policies、References、Custom Tools、Ecosystem、Go（云端 Go agent）、GitLab 详情、Enterprise（SSO/自托管）、Network、Windows-WSL 文档页
