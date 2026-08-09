# OpenCode 快速上手教程

> 本文是一份面向新用户的 **opencode 入门指南**:从安装、配置到日常使用。
> 仓库:`anomalyco/opencode`(原 `sst/opencode`)| 许可证:MIT | 语言:TypeScript(Bun)
> 若想了解 opencode 的架构与竞品对比,另见 [`docs/research/opencode-project-survey.md`](research/opencode-project-survey.md)。

---

## 1. OpenCode 是什么

**OpenCode 是一个开源的 AI 编程 agent(coding agent),以终端 TUI 为主要交互界面**,对标 Claude Code,但完全开源(195k+ stars)且不锁定任何模型供应商。

核心特点:

- **75+ 模型供应商**:Anthropic、OpenAI、DeepSeek、Gemini、Groq、Ollama、OpenRouter 等,想用哪家用哪家
- **client-server 架构**:`opencode` 本体是 TUI 客户端,背后是无头 server,因此还有 Web / Desktop / IDE 扩展等多种形态
- **多端可用**:终端 TUI、`opencode web` 浏览器界面、VS Code 等 IDE 扩展、Desktop app
- **完全可定制**:主题、快捷键、自定义 agent / 斜杠命令 / 工具、JS 插件、MCP

---

## 2. 环境要求

- 现代终端模拟器(建议):WezTerm、Alacritty、Ghostty、Kitty
- 至少一个 LLM provider 的 API key(见第 4 节)
- Windows 用户建议使用 **WSL**

---

## 3. 安装

### 一键脚本(推荐)

```sh
curl -fsSL https://opencode.ai/install | bash
```

### 包管理器

| 方式 | 命令 |
|---|---|
| npm | `npm install -g opencode-ai` |
| bun | `bun install -g opencode-ai` |
| pnpm | `pnpm install -g opencode-ai` |
| yarn | `yarn global add opencode-ai` |
| Homebrew (macOS/Linux) | `brew install anomalyco/tap/opencode` |
| Arch Linux | `sudo pacman -S opencode`(或 `paru -S opencode-bin`) |
| Scoop (Windows) | `scoop install opencode` |
| Chocolatey (Windows) | `choco install opencode` |
| Docker | `docker run -it --rm ghcr.io/anomalyco/opencode` |

> Homebrew 推荐使用官方 tap(`anomalyco/tap/opencode`),更新更及时。

### 升级

```sh
opencode upgrade
```

---

## 4. 配置 Provider(模型)

### 方式 A:`/connect` 交互式登录(推荐)

1. 在项目目录运行 `opencode` 进入 TUI
2. 输入 `/connect`,选择供应商
3. 按提示粘贴 API key(或跳转浏览器 OAuth 授权)

### 方式 B:OpenCode Zen(官方网关,新手友好)

选好模型、添加账单、复制 API key 粘贴即可,免去各家供应商注册。

### 方式 C:环境变量 / .env

```sh
# 以 Anthropic 和 OpenAI 为例
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...
```

项目根目录的 `.env` 文件同样会被读取。

### 切换模型

TUI 内输入 `/models` 切换模型;`ctrl+t` 可循环切换推理档位(variants)。

---

## 5. 初始化项目

进入你要工作的项目目录并启动:

```sh
cd /path/to/your/project
opencode
```

首次使用建议执行:

```
/init
```

opencode 会分析项目并在根目录生成 **AGENTS.md**(项目规则文件,描述项目结构、约定、命令等),之后每次会话都会自动加载。**建议把 AGENTS.md 提交到 Git** 与团队共享。

---

## 6. 日常使用

### 提问 / 理解代码

直接输入问题,用 `@` 键模糊搜索并引用项目文件:

```
How is authentication handled in @packages/functions/src/api/index.ts
```

### 加功能:先 Plan 再 Build

1. 按 `Tab` 切换到 **Plan mode**(只读,不会改文件),描述需求:

   ```
   When a user deletes a note, we'd like to flag it as deleted in the database.
   Then create a screen that shows all the recently deleted notes.
   ```

2. 与它迭代计划(也可以直接把图片拖进终端作为参考)。
3. 确认计划后按 `Tab` 切回 **Build mode**,说 "Go ahead and make the changes."

> 描述需求时像对 junior 开发者说话一样,给足上下文和示例,效果最好。

### 直接改代码

简单改动可以直接提,不用先做计划:

```
We need to add authentication to the /settings route. Take a look at how this is
handled in the /notes route in @packages/functions/src/notes.ts and implement
the same logic in @packages/functions/src/settings.ts
```

### 撤销改动

改得不满意时:

```
/undo
```

opencode 会通过 Git 回滚文件改动并恢复你的原消息,可多次 `/undo`;后悔了用 `/redo` 重做。

### 分享会话

```
/share
```

生成公链(如 `opencode.ai/s/<id>`)并复制到剪贴板,可发给同事。默认不自动分享。

### 注入 shell 输出

```
!ls -la
```

把 shell 命令输出直接注入对话作为上下文。

---

## 7. 常用命令速查

### TUI 斜杠命令

| 命令 | 作用 |
|---|---|
| `/init` | 生成项目 AGENTS.md |
| `/models` | 切换模型 |
| `/connect` | 配置/切换 provider |
| `/undo` `/redo` | 撤销 / 重做文件改动 |
| `/compact` | 压缩上下文(长会话时用) |
| `/new` | 开启新会话 |
| `/sessions` | 会话列表(切换/重命名/删除) |
| `/share` `/unshare` | 分享 / 取消分享会话 |
| `/export` | 导出会话 |
| `/themes` | 切换主题 |
| `/help` | 帮助 |

### 快捷键

| 按键 | 作用 |
|---|---|
| `Tab` | Build / Plan 模式切换 |
| `@` | 文件模糊引用(`@File#L37-42` 可引用行号) |
| `!` | 执行 shell 命令并注入输出 |
| `ctrl+p` | 命令面板 |
| `ctrl+x` | leader key,再按 `c/n/l/u/r/t/m/e/x/q` 触发对应操作 |
| `ctrl+t` | 切换模型推理档位(variant) |

### CLI 子命令

```sh
opencode                     # 启动 TUI
opencode run "修一下这个 bug" # 非交互单次运行(脚本/CI 可用)
opencode run "..." --model gpt-5.2 --auto
opencode serve               # 无头 HTTP server(供 attach/web/IDE 使用)
opencode web                 # 无头 server + 浏览器 Web UI
opencode attach [url]        # 把 TUI 挂到已运行的 server
opencode auth login          # 管理 provider 凭据
opencode session list        # 查看会话
opencode stats               # token 用量 / 费用统计
opencode mcp add/list        # 管理 MCP server
opencode upgrade             # 自升级
```

---

## 8. 进阶扩展(选读)

- **MCP**:`opencode mcp add` 接入外部工具服务器(stdio / HTTP)
- **自定义 agent**:`opencode agent create`,或写 JSON / Markdown frontmatter 定义
- **自定义斜杠命令**:配置文件中用 JSON 或 frontmatter MD 定义
- **规则**:兼容 Claude Code 的 `CLAUDE.md` 与 `~/.claude/skills`
- **插件**:`.opencode/plugins/`(项目)与 `~/.config/opencode/plugins/`(全局)放 JS/TS 文件即可自动加载
- **配置**:`opencode.json`(全局 `~/.config/opencode/` 或项目根目录),含 `model`、`provider`、`permission`、`theme` 等
- **权限**:`permission` 配置可做 allow / ask / deny 三态与通配符匹配(`git *`、`rm *`)

---

## 9. 常见问题

| 问题 | 解决 |
|---|---|
| 安装后提示没有模型 | 执行 `/connect` 或设置 `ANTHROPIC_API_KEY` 等环境变量 |
| Windows 体验差 | 使用 WSL 运行 |
| 撤销改动无效 | `/undo` 基于 Git 回滚,需要在 Git 仓库内 |
| 想用其他模型供应商 | `/models` 或配置文件 `provider` 字段,支持 75+ 供应商 |
| 长会话变慢/超限 | 执行 `/compact` 压缩上下文 |

---

## 10. 参考链接

- 官方文档:https://opencode.ai/docs/
- GitHub:https://github.com/anomalyco/opencode
- 安装脚本:https://opencode.ai/install
- 社区 Discord:https://opencode.ai/discord
- 本仓调研报告:[`docs/research/opencode-project-survey.md`](research/opencode-project-survey.md)
