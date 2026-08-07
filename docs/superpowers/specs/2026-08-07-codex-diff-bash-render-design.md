# Codex 风格工具卡渲染（diff + bash 高亮）— Design

> 状态：设计已获用户批准（模拟稿 `tmp/diff-render-mockup.html` 方案 A）
> 日期：2026-08-07
> 相关模拟：`tmp/diff-render-mockup.html`（用户选定方案 A：catppuccin 配色）
> 调研来源：codex-rs `tui/src/diff_render.rs`、`tui/src/render/highlight.rs`、`tui/src/exec_cell/render.rs`（syntect + catppuccin-mocha 默认主题）；Claude Code 词级 diff（Rust ColorDiff，仅参考未采纳）

## 目标

把工具卡渲染对齐 Codex CLI 风格：

1. bash 卡命令文字做语法高亮（`&&`、`|`、`if/then/fi`、`$VAR`、引号串、flags 等有各自颜色），主题用 catppuccin（深色 mocha / 浅色 latte），随终端明暗自适应。
2. 文件卡标题从 `● Write(path) +N` 改为 Codex 式 `● Added path (+N -M)` / `● Deleted path (-N)` / `● Edited path (+N -M)`。
3. diff 行背景色对齐 Codex 调色板；删除行语法内容叠加变暗；diff 内语法高亮同步切到 catppuccin。

## 非目标

- 不做 Claude Code 词级 diff（工作量大于收益，用户已选方案 A）。
- 不改 event 结构（`FileDiff` 保持 `{Diff, Added, Removed}`）。
- 不改 Read/Write/其他非 bash 工具卡的排版（只动 bash 命令文字与文件卡标题）。
- 不做超宽换行（现状 clamp 保留；多行命令改为 `⎿` 续行展示）。

## 设计决策

### 1. bash 卡命令高亮（`internal/cli/diffview.go` + `internal/cli/toolcard.go`）

- 新增 `highlightBash(cmd string) string`（返回带 SGR 色的整串，与现有 diffBar 风格一致）：chroma `lexers.Get("bash")` + 主题 `catppuccin-mocha`（深色）/ `catppuccin-latte`（浅色），把现有 `activeDiffChromaStyle()` 改为返回 catppuccin 主题（dark→mocha / light→latte），bash 与 diff 共用。
- chroma bash lexer 实测（v2.27.0）：`&&`/`||`/`=`/`[`/`]` → Operator（mocha `#89dceb` 加粗 / latte `#04a5e5` 加粗）；`if/then/fi` → Keyword（mocha `#cba6f7` / latte `#8839ef`）；`export/test/true/echo` → NameBuiltin（mocha `#89dceb` / latte `#04a5e5`）；`$VAR`/`GOFLAGS` → NameVariable（mocha `#f5e0dc` / latte `#dc8a78`）；字符串 → mocha `#a6e3a1` / latte `#40a02b`；数字 → mocha `#fab387` / latte `#fe640b`。
- **flags 补充规则**：chroma bash lexer 把 `--flag` 归为 Text，需后处理：把 `-x`/`--xxx`（词首 `-` 后跟字母）染成参数色 mocha `#f9e2af` / latte `#df8e1d`（Codex syntect 的 `variable.parameter` 语义）。
- 多行命令：首行跟随卡头 `● Bash …`，剩余行用现有 `connectorBlock`（`⎿`）续行渲染，每行仍 clamp 到宽度。
- 单行命令保持现状的 clamp（不换行）。

### 2. 文件卡标题 Codex 化（`internal/cli/diffview.go`）

- 替换 `diffBlock` 的 header 构造：`  ` + 暗 `●` + 加粗动词 + 空格 + path（toolArg 色）+ ` (` + 绿 `+N` + 空格 + 红 `-M` + `)`。
- 动词由 diff 特征推断（与 Codex Add/Delete/Update 语义一致）：
  - `Added > 0 && Removed == 0` → `Added`
  - `Removed > 0 && Added == 0` → `Deleted`
  - 否则 → `Edited`
- `+N`/`-M` 恒显示两侧（含 `-0`/`+0`），与 Codex `render_line_count_summary` 一致；当前 `diffStat` 省略零侧，需替换。
- ● 用 `dim`（不再按工具类别着色），动词不加类别色、仅 bold；path 沿用 `toolArg` 色调。

### 3. diff 行样式（`internal/cli/diffview.go` + `internal/cli/theme.go`）

- `theme.go` 色值更新：
  - dark: `diffAddBG` `#14351d` → `#213A2B`；`diffDelBG` `#3a1619` → `#4A221D`
  - light: `diffAddBG` `#e5f3e7` → `#dafbe1`；`diffDelBG` `#fae8e8` → `#ffebe9`
- 删除行：`diffBar` 对 `-` 行在 chroma 高亮后整体叠加 `dim`（Codex 对 Delete 内容加 `Modifier::DIM`）。
- diff 语法主题：`activeDiffChromaStyle()` 从 `github-dark`/`github` 改为 `catppuccin-mocha`/`catppuccin-latte`，与 bash 高亮统一。

### 4. 主题与明暗自适应

- 深色终端 → `catppuccin-mocha`；浅色终端 → `catppuccin-latte`（沿用现有 `activeCLITheme.name == "light"` 判断）。
- chroma v2.27.0 已内置两个主题（已验证 `styles.Get` 非 nil），无新依赖。

## 改动文件

- `internal/cli/diffview.go`：header 构造、动词推断、catppuccin 主题、flags 规则、删除行 dim、`highlightBash`
- `internal/cli/toolcard.go`：bash 卡接入 `highlightBash` + 多行 `⎿` 续行
- `internal/cli/theme.go`：4 个 diff 背景色值
- 测试：`diffview_test.go`、`render_edge_test.go`、`chat_render_test.go`、`chat_tui_test.go` 中标题/颜色断言更新，新增 bash 高亮用例（含 flags 规则）

## 验证

- `gofmt`（`$(go env GOROOT)/bin/gofmt`）严格格式化
- `go test ./internal/cli/` 相关用例
- `make build` / `go build ./...`
- 功能 commit + `git push origin main`
