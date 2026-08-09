# Corvus TUI 关键词配色与透明输入框设计

日期：2026-08-09

## 1. 目标

Corvus TUI 当前的主题基础设施已经具备蓝、灰、绿、黄、红、紫等语义色，但主聊天路径中工具动词大多统一为白色，助手正文也缺少技术关键词层级，实际观感接近“蓝 + 白”。本设计在保持 Codex 式低噪声和透明输入体验的前提下，补足少量高价值语义色。

本次目标：

1. 为工具动作、执行结果和协调类关键词建立稳定的语义颜色。
2. 为助手正文增加“精选词典 + 结构识别”高亮，而不是对普通英文文本做泛化着色。
3. 保持 composer 与历史 user message 透明、无整行背景，不引入新的灰色卡片感。
4. 保证已有主题、TrueColor、ANSI 256、浅色主题和 `NO_COLOR` 行为不回归。

## 2. 范围与非目标

### 范围

- `internal/cli/toolcard.go` 的工具动词与结果行。
- `internal/cli/md.go` 的 Markdown 普通文本节点。
- `internal/cli/theme.go` 现有语义色槽位和样式映射。
- composer/user-message 的透明渲染回归测试。

### 非目标

- 不新增用户可编辑的关键词配置、主题文件格式或新主题名称。
- 不实现通用自然语言实体识别，不按词频或模型内容动态生成颜色。
- 不改变 Markdown 内容、换行、复制文本、工具状态机或命令执行逻辑。
- 不给整段助手正文、工具参数、树线、耗时或 footer 添加背景填充。

## 3. 视觉语义

颜色全部复用 `cliPalette` 已有槽位，经 `themeFg`/`themeStyle` 输出；实现中不写新的硬编码 ANSI 颜色。

| 语义 | 关键词示例 | 颜色槽位 | 说明 |
|---|---|---|---|
| 信息获取 | `Explored`, `Read`, `Search`, `List`, `Fetch` | `info` | 青色，表示只读/探索动作 |
| 文件变更 | `Edited`, `Created`, `Updated`, `Moved`, `Wrote` | `success` | 绿色，表示已完成的写入动作 |
| 执行过程 | `Ran`, `Build`, `Test`, `Run` | `warn` | 琥珀色，只强调动作词，不染整行 |
| 协调/后台 | `Task`, `MCP`, `Agent`, `Wait` | `secondary` | 柔和紫色，和执行结果区分 |
| 正向结果 | `PASS`, `Success`, `Done`, `Completed`, `Ready`, `Passed` | `success` | 绿色 |
| 失败结果 | `FAIL`, `Error`, `Failed`, `Blocked`, `Invalid`, `Panic` | `danger` | 红色 |
| 注意状态 | `Warn`, `Warning`, `Retry`, `Skipped`, `Pending` | `warn` | 琥珀色 |
| 结构化技术 token | 文件路径、`Function()`、`pkg.Symbol`、命令名 | `accent` | 沿用当前主题强调色，蓝色 Codex 风格 |
| 精选技术概念 | `renderer`, `parser`, `theme`, `cache`, `API`, `TUI`, `model`, `tool` 及对应中文词 | `secondary` | 只选高价值术语，避免彩虹色正文 |

普通正文、参数值、树线、耗时和未命中的词保持 `muted`/默认前景。工具结果的 `✓`/`✗` 继续使用现有 success/danger 颜色。

## 4. 正文匹配规则

1. 在 Goldmark AST 的普通 `ast.Text` 节点上处理精选词；`CodeSpan`、fenced code、URL destination、HTML 和复制标记内容不经过正文词典。
2. 英文关键词按完整 token 匹配，大小写不敏感；中文词按明确字符串匹配，不依赖 Unicode `\\b`。
3. 结构化 token 使用有限规则识别：相对/绝对路径、带扩展名的文件名、`Identifier()`、`pkg.Symbol`、常见命令名。规则只在明显的技术形态成立时触发。
4. 每个段落最多产生 4 个自动高亮片段；同一词在同一段落重复出现时只高亮第一次。超过预算的匹配保留原文。
5. 词典是静态、短小、可测试的 Go 数据表，不支持运行时正则或配置注入。
6. `NO_COLOR`/NoTTY 下不产生 ANSI，输出文本与原文完全一致。

## 5. 组件与数据流

### 5.1 主题层

`theme.go` 仍是颜色唯一来源。工具和 Markdown 渲染器只接收语义类别或 `cliColor`，不能直接写 ANSI 字面量。已有旧主题继续通过同一槽位呈现自己的色相；浅色主题只使用其现有高对比值。

### 5.2 工具卡

`toolcard.go` 增加一个语义动词映射函数。普通工具卡只给动词和完成标记上色，参数保持中性；bash 命令继续使用既有命令/flag/string 高亮。`Explored` 树的子动词保持 `info` 色，树线保持 `faint`。

### 5.3 Markdown

`md.go` 在 inline AST 展平阶段调用纯函数高亮器。高亮器接收纯文本和剩余段落预算，返回带主题 SGR 的文本；Markdown 结构、换行和可复制内容不变。预算在每个段落/列表项开始时重置，避免一条长回复后半段完全失去可读层级。

### 5.4 透明 composer 与历史消息

`renderComposerField` 保持透明透传，`composerFieldBackground` 保持空字符串。`renderUserBubble` 只绘制 `›` 与 accent/faded 前景，不绘制 `inputBoxBG`/`userBubbleBG` 的整行背景、侧边填充或上下 padding。textarea 的光标、选择反白和 mode badge 保留。

## 6. 错误与兼容行为

- 词典或结构规则没有匹配时直接返回原文本，不影响渲染。
- 颜色解析/主题切换失败沿用现有主题回退路径，不新增错误提示。
- 渲染器不修改源 Markdown；`RenderCopy` 仍能剥离 ANSI 并返回原始可复制文本。
- ANSI 256 下使用现有每槽位 xterm fallback；不能为新 token 临时计算或写死 SGR。
- 窄屏只按现有 `visibleWidth`/`ansi.Wrap` 逻辑计算，颜色不得改变布局宽度。

## 7. 测试与验收

### 自动化

- 工具动词映射覆盖 read/write/exec/proc/MCP，并断言颜色槽位。
- 正文高亮覆盖中英文状态词、技术概念、路径/函数 token、重复词预算和排除区域（inline code、fenced code、URL）。
- `RenderCopy`、`NO_COLOR`、TrueColor/ANSI 256、dark/light theme 均有回归断言。
- composer/user-message 输出不得包含 `bgSGR(activeCLITheme.inputBoxBG)` 或 `bgSGR(activeCLITheme.userBubbleBG)`。
- `go test ./internal/cli -count=1` 必须通过；`go test ./... -count=1` 的既有非 TUI 失败单独记录。
- `go build ./cmd/corvus`、`git diff --check` 必须通过。

### 手工视觉验收

使用构建后的二进制检查 `80x30` 与 `40x14`：

1. 工具动词能一眼区分只读、写入、执行和协调，但一行不出现连续彩虹色。
2. 助手正文技术关键词有层级，普通句子仍以白/灰为主。
3. composer 与历史输入和终端底色融合，无灰色整行背景或额外上下空白。
4. 窄屏换行不超出画布，颜色切换不改变视觉宽度。

## 8. 验收标准

本设计视为完成，当且仅当上述自动化命令通过、透明背景回归通过，并且手工检查确认颜色语义清晰、正文不炫、透明 composer 与历史消息符合 Codex 风格。
