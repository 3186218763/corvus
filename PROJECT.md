# Corvus 项目概览

> 生成时间：由当前工作区分析生成

## 简介

**Corvus** 是一个面向 AI 编程代理（coding agent）的本地终端 UI（TUI）。仓库只发布一个可执行文件和一种交互界面：基于 **Bubble Tea** 构建的 TUI。

## 技术栈

| 类别 | 技术 |
|------|------|
| 语言 | Go 1.25（toolchain go1.26.5） |
| TUI 框架 | `charm.land/bubbletea/v2`、`charm.land/bubbles/v2`、`charm.land/lipgloss/v2` |
| 配置 | TOML（`corvus.toml`）+ `.env`（godotenv） |
| 语法分析 | tree-sitter（JavaScript / Python / Rust / TypeScript） |
| 其他 | goldmark（Markdown）、chroma（高亮）、go-keyring（凭据存储）、mvdan.cc/sh（shell 解析） |

## 构建与开发

```sh
make build   # 构建到 bin/corvus
make test    # 运行全部测试
make vet     # go vet ./...
make fmt     # gofmt ./cmd ./internal
```

手动构建：

```sh
go build -o corvus ./cmd/corvus
```

## 配置

- 复制 `corvus.example.toml` → `corvus.toml` 进行本地配置
- API key 放在 `.env`（项目根目录），优先级：项目 `.env` > Corvus 凭据存储（`corvus setup` / 用户配置目录）
- 当前配置中声明了两个 provider：`deepseek`（直连 DeepSeek API）和 `opencode-go`（聚合网关）

## 目录结构

```
cmd/corvus/          入口（main.go，blank import 注册 provider 与内置工具）
internal/
├── agent/           代理核心：任务调度、工具执行循环、会话管理、压缩、迁移
├── cli/             TUI 主体：聊天界面、主题、渲染、技能选择器、模型管理
├── boot/            启动引导：配置加载、模型解析、token profile
├── config/          配置加载/编辑/迁移、凭据管理、.env 处理
├── provider/        模型提供商：anthropic、openai、responses 协议适配
├── tool/            工具层：内置工具（bash、read/write、grep、edit 等）+ 注册表
├── skill/           技能系统：内置技能、索引、执行
├── mcp/             自定义工具层
├── plugin/          插件系统：stdio/HTTP/SSE 传输、安装、启动
├── checkpoint/      检查点与事务（安全写入）
├── recovery/        恢复门控：失败决策、指纹、规则
├── permission/      权限与 bash 审批
├── sandbox/         shell 沙箱（seatbelt、逃逸防护）
├── memory/          项目记忆存储、自动回忆
├── i18n/            国际化（en / zh / zh-TW）
├── billing/         余额查询
├── stats/           用量统计
├── lsp/             LSP 客户端
├── hook/            hook 系统
├── secrets/         敏感信息脱敏
├── proc/            进程管理
├── jobs/            任务/工件
└── ...              其他支撑包
```

## 主要功能

- **会话管理**：会话恢复、重命名、并发/租约锁、侧车元数据
- **工具集**：本地工作区工具（bash、文件读写、grep、glob、edit、todo、代码索引等）
- **MCP 与技能**：MCP 服务器管理、技能安装/索引、`/` 斜杠命令
- **权限系统**：bash 命令审批、只读命令分类、沙箱逃逸防护
- **记忆**：项目级记忆存储、自动回忆/召回
- **插件**：stdio/HTTP/SSE 传输、热加载、安装与缓存
- **Provider 抽象**：anthropic / openai / responses 多协议、重试、提示词缓存
- **恢复机制**：失败恢复门控、重复失败防护、断点恢复
- **多语言**：内置中英文等多语言 UI
- **检查点**：原子 JSON 事务、安全文件路径回退

## 环境变量

- `CORVUS_REDUCE_MOTION=1` — 关闭装饰动画（spinner、平滑滚动、工具框轮播）
- `CORVUS_TUI_SCROLL_REPAINT=1` — 滚动时整屏重绘（旧终端兼容，禁用平滑滚动）

## 文档资产

- `docs/superpowers/plans/` — 功能开发计划
- `docs/superpowers/specs/` — 设计规格文档
- `docs/superpowers/research/` — 调研报告（渲染动画、会话身份等）
- `docs/research/` — 提示词缓存相关调研
