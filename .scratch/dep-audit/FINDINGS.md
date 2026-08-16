# corvus 依赖与技术栈盘点（2026-08-15）

> **收口（2026-08-16）**：战役完成。B1–B8 全部决策并执行（B9 判定不动），C 表
> 全部关闭（C3/C9 为本地处置，不入库），13 个依赖 bump 完成。共 8 个提交 +
> 5 份 ADR（0001–0007）。合计净删 ~5700 行、go.mod −8 直接依赖（tree-sitter 5 +
> go-keyring 1 + 退役链上的 wincred/dbus 等 indirect）。D-3 的在途文档删除仍未
> 处置，待用户决定。

盘点方式：主线程核验（go toolchain / 构建 / CI / 配置文件）+ 3 个只读 Explore 子代理
（A 依赖使用普查、B 功能重复聚类、C 废弃物与过时技术）。所有行号对应 2026-08-15 工作区状态。

基线体检：`go mod tidy -diff` 无差异（**零未使用依赖**）；`go build` / `go vet` /
`go test -short` 全绿；`go list -m` 无 deprecated 模块。
规模：954 个 .go 文件（生产 ~150k 行 / 测试 ~151k 行）；32 个直接依赖 + 14 indirect；
58 个 internal 包 + 5 个 cmd。

---

## A. 未使用依赖：没有 —— ✅ 13 个 minor/patch 更新已全部执行（2026-08-16）

- 32/32 直接依赖全部有引用（普查逐一核对，含 build-tag 变体与 test-only）。
- `go.uber.org/goleak` 仅测试使用（7 个 `main_test.go` 的 `VerifyTestMain`），正常形态。
- ~~可用更新（13 个依赖，全部 minor/patch，无重大版本）~~ 已全部 bump（独立提交）：
  bubbles v2.1.0→v2.1.1 · bubbletea v2.0.7→v2.0.8 · lipgloss v2.0.4→v2.0.6 ·
  x/ansi v0.11.7→v0.11.8 · runewidth v0.0.24→v0.0.27（indirect）· jsonschema v6.0.2→v6.0.3 ·
  pflag v1.0.6→v1.0.10 · goldmark v1.8.2→v1.8.5 · x/image v0.43.0→v0.45.0 ·
  x/net v0.56.0→v0.58.0 · x/sys v0.46.0→v0.47.0 · x/term v0.44.0→v0.45.0 ·
  x/text v0.39.0→v0.41.0；x/sync v0.21.0→v0.22.0 随传递闭包更新。

## B. 决策项（需要 grill / ADR，按影响排序）

### B1 ★ tree-sitter 幽灵子系统 —— ✅ 已决策并执行（2026-08-15，commit 7b9fa68）

决策：删除（选项 B）；静态单二进制定为硬约束；ADR 见 `docs/adr/0001-static-binary-pure-go-codeindex.md`。
结果：3 个 tagged 文件 + mergeCodeSymbols/codeSymbolIdentity 死代码摘除，go.mod -5 直接依赖 -1 indirect（mattn/go-pointer），发布行为零变化。

- 5 个直接依赖（`go-tree-sitter` + js/ts/py/rust 四个 grammar）只被
  `internal/tool/builtin/codeindex_treesitter.go:12-16` 引用，文件头
  `//go:build treesitter && cgo`，另有 `codeindex_treesitter_stub.go` 兜底。
- 全仓没有任何构建入口开 `-tags treesitter`：Makefile `CGO_ENABLED=0`、CI 裸
  `go build ./...`、文档/示例配置零提及；本地 `go env CGO_ENABLED` = 0。
- 即：默认构建、`make build`、CI、发布二进制**全部走 stub**，这 5 个依赖（依赖树里
  最重的 C 绑定一族）从未进入任何产物，但 `go mod tidy` 因 tagged 文件存在而永久保留。
- 选项：A) 接入构建（若 code index 是想要的功能，需加构建入口 + cgo）；
  B) 删除 5 依赖 + 2 文件（codeindex 回落纯 Go 实现），go.mod 直接瘦 5 个直接依赖。

### B2 宽度计算：4 个库、3 处实现 —— ✅ 已决策并执行（2026-08-16，commit 见 git log "width authority"）

决策（ADR-0002）：宽度权威 = x/ansi；uniseg 仅限字素切分、禁用其 StringWidth；不得直引 runewidth（降级 indirect，x/ansi 内部依赖）；截断一律走 textutil 字素助手。
实证：runewidth 把旗帜/键帽算 1 格（终端实际 2 格），agent 流式重绘因此低估行数留残影——已修。终端格语义由 `TestVisibleWidthTerminalCellPins` 钉死。

- `internal/cli/box.go:14-15` `visibleWidth` = x/ansi（TUI 主路径，注释自述薄包装）。
- `internal/agent/width.go:17-20` **同名函数** `visibleWidth` = runewidth + 自带 SGR 剥离。
- `internal/cli/composer_selection.go:73,113` uniseg + runewidth 混用，编辑器自建换行布局。
- 依赖树第 4 个：`clipperhouse/displaywidth`（indirect，经 lipgloss v2）。
- 三处对 emoji/ZWJ/ANSI 口径不一致。统一候选：x/ansi（已在树内、cli 主路径在用）。

### B3 glob/ignore：两套并行 + 零调用者的"统一层" —— ✅ 已决策并执行（2026-08-16，ADR-0003）

决策（ADR-0003，`docs/adr/0003-uniform-walk-pruning.md`）：
- grep/glob(`**`)/`ls -R` 统一走 `walkIgnorer`（ripgrep 语义：隐藏 + 噪声表 +
  git ignore；显式指名的根仍全量搜索）。**行为变化**：`glob **/…` 与 `ls -R`
  不再返回隐藏/gitignore 条目（工具描述已注明）。
- 噪声目录表收敛为 `fileutil.IsNoiseDir` 一份（VCS/依赖/语言缓存），各工具
  允许本地追加（codeindex 加构建产物、fileref 加 build/dist），构建产物刻意
  不进共享表（grep 镜像 ripgrep 搜未忽略的构建树）。
- `GlobSet`/`NewGlobSet`/`Match` 死代码删除（globset.go→globmatch.go，
  仅存 `MatchSlashGlob`）；skill/task 的 `path.Match` 判定为名字匹配非重复，
  加注释钉边界；`permission.matchGlob`（'*' 跨 '/' 是文档化契约）保持不动。
- fileref 三个外来路径（wailsjs/.astro/npm/.stage，initial commit 继承残留）删除——D1 一并关闭。

- 主实现：grep 工具的 `walkIgnorer`（`tool/builtin/gitignore.go`，go-gitignore 唯一消费者是 grep.go:238）。
- `internal/fileutil/globset.go` 自述"集中 doublestar 语义"，但 `GlobSet` **全部非测试代码零调用者**（仅 `MatchSlashGlob` 被 glob.go:201 使用）——想统一但没人用。
- 第三套语义：`internal/permission/permission.go:628-641` 手写 `matchGlob`（'*' 跨 '/'）。
- 噪声目录跳过表三份硬编码且互不一致：`tool/builtin/workspace.go:269-271`、
  `internal/fileref/search.go:10-30`（含仓库特定路径，见 D1）、`tool/builtin/ls.go:104`。
- 决策：统一到哪套（复活 GlobSet vs walkIgnorer 提升为公共层 vs 维持现状只修不一致）。

### B4 HTTP 出口：netclient 主工厂 + 5 处影子 + SSRF 守卫双份 —— ✅ 已决策并执行（2026-08-16，ADR-0004）

决策（ADR-0004，`docs/adr/0004-unified-egress-netclient.md`）：全部出口走 netclient、
构造失败响亮报错（不静默直连）、SSRF 单实现 `internal/ssrfguard`（代理感知，webfetch
与 installsource 共用）、用户配置端点（MCP/provider）只加代理不加守卫。

执行中额外实锤并修复的两个真 bug：
1. **install_source 代理冲突**：旧 `ssrfGuardClient` 包住带代理的 transport，
   `DialContext` 拦到的是**代理地址**——私网代理（clash@192.168.x）被 `IsPrivate`
   拒连，工具对这类用户必坏；且目标经代理远端解析、守卫从未检查。切到
   ssrfguard.GuardedClient（webfetch 同款代理感知设计）修复，
   `TestGuardedClientDoesNotRefusePrivateProxy` 钉死回归。
2. **balance 12s 超时失效**：boot 的 balanceClient 用 `TransportOptions{}` 构建
   （无 Timeout），billing 注释承诺的 12s 防挂死在生产路径只剩 ctx 兜底；已补
   `Timeout: 12s`，billing 删除零调用者的包级默认 client。

其余：fetch_models 加 `Proxy` 选项（config/setup 传 spec，向导走 env-auto）；
responses `New` 对齐 openai/anthropic 签名 `(Provider, error)`、删静默回退；
plugin 双 transport 走 netclient + 共享 `sameOriginRedirect`（C8 关闭）；
billing `FetchWithClient` nil client 改报错。

- 主链路：provider 三家 + boot + mcp-server 全走 `netclient.NewHTTPClient`。
- 影子（裸 `&http.Client{}`，绕过代理策略）：
  `provider/openai/fetch_models.go:56`（不走代理）、`provider/responses/responses.go:136`
  （netclient 失败**静默回退**裸 client）、`plugin/transport_http.go:58` 与
  `transport_sse.go:85`（两处**复制同一段 CheckRedirect lambda**）、
  `billing/balance.go:46`（fallback）、`installsource/install_source.go:107`（fallback）。
- SSRF 守卫实锤复制：`tool/builtin/webfetch.go:66-235` 与 `internal/installsource/ssrf.go:15-70`
  共享逐字节相同的 `cgnatRange`/`mustCIDR`/`blockedFetchIP`；ssrf.go:14-15 注释自认
  "Kept in sync by hand"。
- 决策：影子 client 是否一律走 netclient（还是区分 fallback 语义）；SSRF 抽公共包是明确改进。

### B5 持久化：锁复制 + jsonl 追加两套（中） —— ✅ 已决策并执行（2026-08-16，commit 1a1c91e，ADR-0006）

决策（ADR-0006，`docs/adr/0006-persistence-durability-tiers.md`）：
- **锁只留一份**：workspacelease 删逐字节复制的锁文件对与进程内注册表，委托
  `filelock.Acquire`；租约语义（acquire 超时/保留宽限/重入）保留，busy WaitNotice
  以 `filelock.WithWaitHook` 存续（至多回调一次）。
- **共享撕裂尾守卫**：`fileutil.EnsureTrailingNewline`（O_RDWR|O_APPEND 打开后补
  分隔换行）；stats、control 冲突日志（改 0600）、autoresearch 采纳。
- **刻意不做**统一 jsonl 包：四个追加器的差异是设计（锁+修复 / fsync+salvage /
  best-effort / os.Root jail），ADR 记录分层理由；agent 事件侧车机制保持独立。
- memory 的 MEMORY.md 索引重写改 `fileutil.AtomicWriteFile`（C7 一并关闭）。

- `internal/workspacelease/lock_unix.go` 与 `internal/filelock/lock_unix.go`
  **除 package/错误名外逐字节相同**（windows 版同样）。→ 机械修（见 C4）。✅
- O_APPEND jsonl 追加独立实现多份，各自带防损坏逻辑：
  `stats/record.go:80-96`（撕裂尾修复）vs `agent/session_events.go:659-732`（损坏尾 salvage）
  vs `control/session_lifecycle.go:594`（裸追加）vs `autoresearch/store.go:761`。
- `memory/store.go:534-536` 裸 `os.WriteFile` 非原子写，绕开 fileutil（→ C7）。✅
- `internal/store` 是纯路径布局库（无 I/O），职责清晰，非重复。

### B6 序列化：外部库分工清晰，内部有四套 .mcp.json schema（中） —— ✅ 已决策并执行（2026-08-16，commit 1d05482，ADR-0007）

决策（ADR-0007，`docs/adr/0007-canonical-mcpjson-schema-and-frontmatter-writers.md`）：
- 新叶包 `internal/mcpjson` 为唯一 wire schema（ServerSpec/Document/Parse +
- NormalizeType/NormalizeTier 别名表）；config / installsource / pluginpkg / ccswitch
  六个解析方全部经它解码，各自策略叠加其上。
- 实锤三处丢字段已修：**包导入丢三个超时字段**（manifest 写 5s 启动超时实际拿到
  30s 默认）、**Claude 格式导入强制 auto_start=false**、tier 对主读取方不可见
  （tier 是已退役设置，`normalizeLegacyMCPTiers` 仍在唯一一处统一抹除——
  解析层忠实解码、策略单点化）。新增 3 个钉死测试。
- frontmatter：`Encode` 为唯一写入方（memory render / RenderSkillFile / stub，
  输出逐字节不变）；`Raw` 取代 skill.go 手写围栏扫描（修 CRLF 漏判）；
  `ParseError` 取代 installsource 的 Decode+Split 双解析；Decode 删除。

- TOML=config 专属、YAML=frontmatter、JSON=payload/状态——按文件类型分工，**不算库级重复**。
- 但：`.mcp.json` schema 4 套独立定义：`config/mcpjson.go:22`（主）、
  `installsource/mcp.go:171-194`、`pluginpkg/pluginpkg.go:397`、`config/migrate.go:27`（legacy）。✅
- frontmatter 包内 Split（宽松，10 处调用）vs Decode（typed，仅 1 处）双路并存；
  `skill/skill.go:997-1022` 手写重复了围栏扫描；memory 与 skill 各自维护"写 frontmatter"。✅

### B7 v0.x legacy 退役时点（产品决策） —— ✅ 已决策并执行（2026-08-16，commit 935a99f，ADR-0005）

决策：现在退役。前提核实：本机无 `~/.corvus/config.json`、无迁移 marker、
archive 均为现代格式——v0.x 人口为零；keyring 构建标签在 Linux 上本就排除。
执行：删 `internal/migration` + agent 迁移三件套（约 2000 行）、go-keyring 依赖
（−3 模块）、legacy config.json 全部读取路径与 /migrate 命令；保留现役行为
（v1 TOML 读取回退、1.9.1 MCP 回填去掉 v0.x 源、housekeeping 升级、parseLegacyMCPSpec）。
未迁移的 v0.x 数据留在磁盘不再被读取。

- `internal/migration`：v0.x（TypeScript 时代）legacy rescue，boot 时执行；
  `go-keyring` 仅剩 legacy 读（无 Set/Delete）；`legacy config.json` 读取同族。
- 何时退役取决于"还有没有 v0.x 用户"。→ 本机核实为零。

### B8 构建配置一致性 —— ✅ 已决策并执行（2026-08-16，commit de06add）

决策：Makefile = 对齐 CI 的本地入口（test 带 -timeout 20m；新增 race/lint/fmt-check
目标镜像 CI 步骤，fmt 保留改写形态；check 聚合本地门禁）；CI 1.25 腿加
`GOTOOLCHAIN=local`（`toolchain go1.26.5` 此前让它影子下载 1.26.5，两腿等价，
矩阵腿从未真正测过 1.25）。本地无 golangci-lint，lint 门禁仍由 CI 承担。

- Makefile vs CI 漂移：CI 有 race/lint/20m timeout 而 Makefile 全无；Makefile build 带
  ldflags 版本注入而 CI 裸建；fmt 一个 `-w` 一个 `-l`。决策：Makefile 定位（薄入口 vs 本地全功能）。✅
- CI 矩阵 1.25 腿名义失效：`toolchain go1.26.5` + `GOTOOLCHAIN=auto` 使两条腿等价。
  要么 `GOTOOLCHAIN=local`，要么砍 1.25 腿。✅
- 两个 golangci 配置是有意的 gate/report 分工，**不是漂移**，保留。

### B9 终端库双份（低优先） —— ✅ 决策：不动

- `golang.org/x/term`（direct，5 文件）与 `charmbracelet/x/term`（indirect，经 bubbletea v2）
  功能重叠。可在自然触碰时收敛，不值得专票。

## C. 机械项（事实清楚，可直接开票） —— ✅ 全部关闭（2026-08-16）

| # | 内容 | 证据 |
|---|---|---|
| C1 | ~~删 `cmd/spike-shimmer`~~ ✅ commit f6bb3ce | main.go:12-16；motion.go 仅注释提及 |
| C2 | ~~`.gitattributes` 裁剪~~ ✅ commit f6bb3ce：删 15 条零匹配规则，存留 13 条全部有对应文件 | 全文 28 行 vs 实际文件类型 |
| C3 | ~~`.grok/` 空目录处置~~ ✅ 本地删除（未跟踪目录，无需入库；skill loader 不扫它） | config/paths.go:536 |
| C4 | ~~workspacelease 复用 filelock~~ ✅ 随 B5（commit 1a1c91e） | 见 B5 |
| C5 | ~~`fileutil.GlobSet` 零调用者~~ ✅ 随 B3 删除（globset.go→globmatch.go） | globmatch.go |
| C6 | ~~`corvus.example.toml` 补齐漂移字段~~ ✅ commit f6bb3ce：补 `language`/`ui.show_reasoning`/provider `balance_url`/`max_output_tokens`/`price`/`prices`/`model_overrides`；corvus.toml 头部矛盾为未跟踪本地文件，已改写其注释（产品只读 `.corvus/config.toml` 与 `~/.corvus/config.toml`） | config.go:50,198,824,830,883 |
| C7 | ~~`memory/store.go:534` 改 fileutil 原子写~~ ✅ 随 B5 | — |
| C8 | ~~合并 plugin 两处重复 CheckRedirect lambda~~ ✅ 随 B4（`sameOriginRedirect`） | transport_http.go |
| C9 | ~~本地清理~~ ✅ 已删 `bin/reasonix`、过期 `bin/corvus`（gitignored，不入库） | Makefile 只产 corvus |

## D. 存疑待确认（grill 问题清单素材）

1. ~~`internal/fileref/search.go:25-30` 的 `skipDirPaths` 含外来路径~~ ✅ 已关闭：
   `git log -S` 实锤三个路径（wailsjs/.astro/npm/.stage）来自 initial commit 的
   原项目布局残留，随 B3 删除。
2. `netclient` 的 `ForceIPv4` 注释提到 "desktop updater"，本仓无此代码——是否有仓外消费者？
3. **工作区在途未提交改动**：6 个文档删除（README.md、README.zh-CN.md、两份 RESEARCH_REPORT、
   TOP5_ISSUES.md、DEEP_DIVE_CODEX_COMPARISON.md）+ `.gitignore` 删掉 superpowers/SDD 两行。
   需用户先处置（提交或恢复），避免与清理工作混入同一 diff。

## 附：重复聚类总览（子代理 B）

| 聚类 | 重叠 | 主实现 | 影子/重复 |
|---|---|---|---|
| 宽度 | 高 | cli/box.go (x/ansi) | agent/width.go、composer_selection.go、4 个宽度库 |
| glob/ignore | 中-高 | grep 的 walkIgnorer | GlobSet（零调用）、permission.matchGlob、三份噪声表 |
| 持久化 | 中 | fileutil 原子写 + filelock | workspacelease 锁拷贝、多份 jsonl 追加、memory 直写 |
| HTTP | 中 | netclient | 5 处裸 client、SSRF 守卫双份 |
| 序列化 | 中 | frontmatter pkg + config toml | Split/Decode 双路、4 套 .mcp.json schema |
| 文件工具 | 低 | fileutil/fileref/filelock/nilutil/textutil 各司其职 | 仅 workspacelease 复制 |
| diff | 低 | internal/diff (go-udiff) | 无 |
| 进程 | 低 | internal/proc（12 处消费） | 无（mcplaunch/mcpdiag 非进程管理） |
| 密钥 | 低 | config/credentials.go 单入口 | keyring 仅 legacy 读 |
| 日志 | 统一 | slog（27 文件，0 个裸 log 包） | fmt.Print* 集中在 CLI 输出层，属预期 |
