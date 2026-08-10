# Corvus

Corvus 是一个本地 AI 编程代理终端界面。本仓库只保留一个可执行程序和一个
交互入口：基于 Bubble Tea 的 TUI。

## 构建

```sh
go build -o corvus ./cmd/corvus
```

## 配置与启动

配置统一放在 `.corvus/config.toml`：

- 本机全局：`~/.corvus/config.toml`（优先级最高）
- 项目级：`<项目根>/.corvus/config.toml`（旧版 `./corvus.toml` 已移除）

API key 直接写在供应商条目的 `api_key` 字段（参考 `corvus.example.toml`），
不再走环境变量；本机 `~/.corvus/config.toml` 里的 `api_key` 优先于项目配置。
没有可用模型配置时，首次启动 TUI 会提供供应商配置引导。

```sh
corvus
```

## 使用示例

```sh
# 指定模型与运行时档位（economy | balanced | delivery）
corvus --model <provider/model> --profile balanced

# 恢复最近的会话
corvus -c

# 按会话 ID 或关键词恢复历史会话（不带参数则打开选择器）
corvus -r "修复登录鉴权"

# 在指定目录下启动（作为项目根，配置、沙箱与文件工具都从这里解析）
corvus --dir ./cmd

# 一次性放行所有需要确认的工具调用（等价于 TUI 内的 Ctrl+Y）
corvus --yolo
```

TUI 保留会话恢复、模型选择、本地工作区工具、权限控制、MCP、技能和项目记忆等
核心能力。在交互式终端中执行 `corvus --help` 可以查看会话启动参数，启动后
在 TUI 内执行 `/help` 查看全部命令。

TUI 环境变量：
- `CORVUS_REDUCE_MOTION=1` — 关闭装饰性动画（spinner 旋转、平滑滚动、工具帧轮换）。
  elapsed 计时仍正常跳动。
- `CORVUS_TUI_SCROLL_REPAINT=1` — 每次滚动时启用旧式全屏重绘；仅用于 cell-diff
  渲染器下会残留陈旧行的终端（会禁用平滑滚动）。

## 联网搜索与工具发现

- `tool_search` 始终可用：模型可以按关键词搜索当前已注册的工具（内置工具 +
  已连接的 MCP 工具），不必靠猜名字。
- `web_search` 是可选本地工具，通过 `.corvus/config.toml` 的 `[web_search]`
  配置后端（`searxng` 需 `base_url`；`brave`/`tavily` 需 `api_key`，见
  `corvus.example.toml`）。它对所有 provider 生效，并遵守 `[network_policy]`
  的 deny 规则；启用后会自动关闭 provider 侧的 server-side web_search，
  避免出现两个同名工具。

## 开发

```sh
make fmt
make test
make build
```
