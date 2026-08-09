# OpenCode 调研笔记

> 调研时间:2026-08 · 信息来源:[opencode.ai/docs](https://opencode.ai/docs)、[github.com/anomalyco/opencode](https://github.com/anomalyco/opencode)

## 一、概述

**OpenCode** 是一个开源的 AI 编程代理(terminal AI coding agent),目前由 **Anomaly** 公司维护(仓库已从 `sst/opencode` 迁移至 `anomalyco/opencode`)。它提供终端 TUI、桌面应用、IDE 扩展和 Web 界面等多种形态,核心是让 AI 直接在本地仓库中读写代码、执行命令、完成开发任务。

- 官网:https://opencode.ai
- 仓库:https://github.com/anomalyco/opencode(约 195k stars / 25k forks,15k+ commits)
- 许可证:MIT
- 技术栈:基于 TypeScript/Bun 的 monorepo
- 模型支持:通过 [models.dev](https://models.dev) 的提供商列表,几乎所有主流 LLM 提供商均可接入(Anthropic、OpenAI、Gemini、DeepSeek、本地 Ollama 等)

## 二、核心特性

- **终端优先**:TUI 交互,支持拖拽图片进提示、`@` 模糊搜索项目文件、Tab 切换模式
- **Plan / Build 双模式**:Tab 键切换;Plan 模式只读、只做分析与方案,不修改文件;Build 模式全权限执行
- **内置 Agents**:
  - `build` —— 默认 agent,完整权限的开发工作
  - `plan` —— 只读 agent,默认拒绝文件编辑、执行 bash 需询问,适合探索陌生代码库和制定方案
  - `@general` —— 通用子代理(subagent),用于复杂搜索和多步任务
- **快照与撤销**:基于内部 git 仓库的快照系统,支持 `/undo`、`/redo` 回滚 agent 的改动
- **上下文压缩(compaction)**:会话接近上下文上限时自动压缩
- **可扩展生态**:
  - MCP 服务器(Model Context Protocol)
  - Agent Skills(类似 Claude Code 的 skills,兼容读取 `.claude/skills`)
  - LSP 服务器(自动下载并用于代码语义)
  - Plugins(本地文件或 npm 包,扩展自定义工具/hooks)
  - ACP(Agent Client Protocol)支持
  - SDK / Server(HTTP API,可 headless 调用)
- **团队协作**:会话可分享(默认关闭,`/share` 生成链接)
- **GitHub / GitLab 集成**:`opencode github install` 可在仓库中安装 GitHub Action,实现 issue → PR 自动化
- **AGENTS.md**:`/init` 分析项目并生成 `AGENTS.md`,作为项目上下文规范(与 Claude Code、Cursor 等生态兼容)

## 三、安装

```bash
# 官方安装脚本(推荐)
curl -fsSL https://opencode.ai/install | bash

# npm / bun / pnpm / yarn
npm install -g opencode-ai
bun install -g opencode-ai
pnpm install -g opencode-ai
yarn global add opencode-ai

# macOS / Linux(Homebrew,官方 tap 更新最及时)
brew install anomalyco/tap/opencode
brew install opencode          # 官方 formula,更新较慢

# Arch Linux
sudo pacman -S opencode        # stable
paru -S opencode-bin           # AUR 最新版

# Windows
choco install opencode
scoop install opencode
mise use -g opencode           # 任意平台

# Docker
docker run -it --rm ghcr.io/anomalyco/opencode
```

安装脚本的安装路径优先级:`$OPENCODE_INSTALL_DIR` → `$XDG_BIN_DIR` → `$HOME/bin` → `$HOME/.opencode/bin`。

> 注意:升级前建议移除早于 0.1.x 的旧版本。

另有 **Desktop App(BETA)**:可从 [releases](https://github.com/anomalyco/opencode/releases) 或 opencode.ai/download 下载(macOS dmg / Windows exe / Linux deb·rpm·AppImage),或 `brew install --cask opencode-desktop`。

## 四、快速上手

```bash
# 1. 配置 LLM 提供商
opencode auth login            # 选择任意提供商,填入 API key(存入 ~/.local/share/opencode/auth.json)
# 或者启动 TUI 后执行 /connect

# 2. 进入项目目录并启动
cd /path/to/project
opencode

# 3. 初始化项目上下文(生成 AGENTS.md,建议提交到 git)
/init

# 4. 开始使用
#    - 用 @ 模糊搜索引用项目文件: "How is auth handled in @src/api/index.ts"
#    - Tab 切换到 Plan 模式先要方案,再切回 Build 执行
#    - /undo、/redo 回滚改动
#    - /share 分享会话
#    - 拖拽图片到终端可作为提示参考
```

## 五、CLI 命令一览

不带参数运行 `opencode` 即启动 TUI;也支持脚本化/非交互调用。

| 命令 | 作用 |
|---|---|
| `opencode run "<prompt>"` | 非交互模式直接执行提示(可 `-c` 续会话、`-f` 附带文件、`--format json` 输出 JSON 事件、`--auto` 自动批准权限) |
| `opencode [project]` | 启动 TUI(`--continue` 续上次会话、`--session` 指定会话、`--model provider/model`) |
| `opencode serve` | 启动 headless HTTP 服务器(API 访问,`OPENCODE_SERVER_PASSWORD` 开启 basic auth) |
| `opencode web` | 启动带 Web 界面的服务器并打开浏览器 |
| `opencode attach <url>` | 将 TUI 连接到已运行的远程 backend |
| `opencode agent list / create` | 查看/创建自定义 agent(create 可非交互:`--path --description --mode --permissions --model`) |
| `opencode auth login / list / logout` | 管理提供商凭据 |
| `opencode models [provider]` | 列出可用模型(`provider/model` 格式;`--refresh` 刷新 models.dev 缓存) |
| `opencode mcp add / list / auth / logout / debug` | 管理 MCP 服务器 |
| `opencode github install / run` | 安装/运行 GitHub agent(用于 GitHub Actions 自动化) |
| `opencode plugin <module>` | 安装 npm 插件并写入配置 |
| `opencode pr <number>` | 拉取并 checkout GitHub PR 分支后运行 |
| `opencode session list / delete` | 会话管理 |
| `opencode stats` | 查看 token 用量与费用统计(`--days`、`--models`) |
| `opencode export / import` | 导出/导入会话(JSON 或分享链接;`--sanitize` 可脱敏) |
| `opencode db` | 数据库工具(`db path` 查看路径) |
| `opencode debug` | 排查问题 |
| `opencode uninstall` | 卸载(`--keep-config` / `--keep-data` / `--dry-run`) |
| `opencode upgrade [version]` | 升级到最新版或指定版本 |
| `opencode acp` | 启动 ACP(Agent Client Protocol)服务器 |

全局 flags:`--help/-h`、`--version/-v`、`--print-logs`、`--log-level`、`--pure`(不加载外部插件)。

常用环境变量:`OPENCODE_CONFIG`(自定义配置路径)、`OPENCODE_CONFIG_DIR`(自定义配置目录)、`OPENCODE_SERVER_PASSWORD/USERNAME`(serve/web 认证)、`OPENCODE_DISABLE_AUTOUPDATE`、`OPENCODE_DISABLE_CLAUDE_CODE`(禁用读取 `.claude` 的 prompt 与 skills)等;另有大量 `OPENCODE_EXPERIMENTAL_*` 实验性开关。

## 六、配置

格式为 **JSON / JSONC**,schema 见 https://opencode.ai/config.json(TUI 专用配置见 tui.json,schema 为 https://opencode.ai/tui.json)。

### 配置位置与优先级(后加载覆盖前者,非冲突键合并)

1. Remote config(`.well-known/opencode`,组织默认配置)
2. 全局 `~/.config/opencode/opencode.json`
3. 自定义路径(`OPENCODE_CONFIG` 环境变量)
4. 项目根 `opencode.json`(向上查找到最近 git 目录)
5. `.opencode/` 目录(agents、commands、plugins、skills、tools、themes 等)
6. 内联配置(`OPENCODE_CONFIG_CONTENT`)
7. 受管配置(macOS `/Library/Application Support/opencode/`、Linux `/etc/opencode/`、Windows `%ProgramData%\opencode`;macOS 还支持 MDM 的 `.mobileconfig` 强制下发)——优先级最高,用户不可覆盖

### 常用配置项示例

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "model": "anthropic/claude-sonnet-4-5",
  "small_model": "anthropic/claude-haiku-4-5",   // 轻量任务(如生成标题)用小模型
  "default_agent": "plan",                        // 默认 agent(build/plan/自定义)
  "subagent_depth": 2,                            // 子代理嵌套深度,默认 1
  "permission": { "edit": "ask", "bash": "ask" }, // 默认全部放行,可改为 ask/deny
  "instructions": ["CONTRIBUTING.md", "docs/*.md"], // 附加规则文件
  "formatter": true,                              // 启用内置代码格式化器
  "lsp": true,                                    // 启用内置 LSP
  "snapshot": false,                              // 关闭快照(大仓库可省磁盘,但无法回滚)
  "autoupdate": false,                            // 关闭自动更新
  "compaction": { "auto": true, "prune": false, "reserved": 10000 },
  "share": "manual",                              // manual | auto | disabled
  "tools": { "write": false, "bash": false },     // 禁用某些工具
  "disabled_providers": ["openai"],
  "enabled_providers": ["anthropic", "openai"],
  "server": { "port": 4096, "hostname": "0.0.0.0", "mdns": true, "cors": ["http://localhost:5173"] },
  "mcp": {},                                      // MCP 服务器配置
  "plugin": ["opencode-helicone-session"],        // npm 插件
  "agent": { /* 自定义 agent,也可用 .opencode/agents/*.md 定义 */ },
  "command": { /* 自定义命令,也可用 .opencode/commands/*.md 定义 */ }
}
```

配置支持变量替换:`{env:VAR_NAME}` 引用环境变量、`{file:path}` 引用文件内容(适合放 API key、大段指令)。

## 七、生态与开发

- **Providers**:凭 models.dev 的提供商目录,支持 OpenAI、Anthropic、Google、DeepSeek、本地模型(Ollama 等)、Amazon Bedrock(支持 region/profile/VPC endpoint 等 AWS 选项)等
- **Skills**:Agent Skills 机制,与 Claude Code 的 skills 兼容
- **Plugins**:`.opencode/plugins/` 或 npm 包,可加自定义工具、hooks、集成
- **Server/SDK**:headless HTTP API,适合集成到其他工具;`opencode run --attach` 可复用常驻 server 避免 MCP 冷启动
- **IDE**:VS Code 扩展等(`sdks/vscode`);Zed 也有内置支持
- **GitHub/GitLab agent**:`opencode github install` 一键配置 GitHub Actions,实现自动化开发流程

## 八、定位小结

OpenCode 是当前(2026 年)最活跃的开源终端 AI 编程代理之一(≈195k stars),与 Claude Code 定位直接竞争,优势在于:

- 完全开源(MIT)+ 提供商中立(不绑定单一模型)
- TUI 体验好,支持 Plan/Build 模式、图片输入、快照回滚
- 生态开放:MCP、Skills、LSP、插件、SDK、headless server 一应俱全
- 支持团队/组织级配置下发(remote config、MDM 受管配置)

## 九、参考资料

- 文档:https://opencode.ai/docs
- 仓库:https://github.com/anomalyco/opencode
- CLI 参考:https://opencode.ai/docs/cli
- 配置参考:https://opencode.ai/docs/config
- 配置 schema:https://opencode.ai/config.json
- 下载:https://opencode.ai/download
- Discord:https://opencode.ai/discord
