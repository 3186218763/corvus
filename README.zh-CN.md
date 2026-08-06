# Reasonix

Reasonix 是一个本地 AI 编程代理终端界面。本仓库只保留一个可执行程序和一个
交互入口：基于 Bubble Tea 的 TUI。

## 构建

```sh
go build -o reasonix ./cmd/reasonix
```

## 配置与启动

根据 `reasonix.example.toml` 配置模型供应商，并设置对应的 API Key。
密钥优先级：项目 `.env` > Reasonix 凭据存储（`reasonix setup` / 用户配置目录）。
没有可用模型配置时，首次启动 TUI 会提供供应商配置引导。

```sh
reasonix
```

TUI 保留会话恢复、模型选择、本地工作区工具、权限控制、MCP、技能和项目记忆等
核心能力。在交互式终端中执行 `reasonix --help` 可以查看会话启动参数。

TUI 环境变量：
- `REASONIX_REDUCE_MOTION=1` — 关闭装饰性动画（spinner 旋转、平滑滚动、工具帧轮换）。
  elapsed 计时仍正常跳动。
- `REASONIX_TUI_SCROLL_REPAINT=1` — 每次滚动时启用旧式全屏重绘；仅用于 cell-diff
  渲染器下会残留陈旧行的终端（会禁用平滑滚动）。

## 开发

```sh
make fmt
make test
make build
```
