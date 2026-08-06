# Coding Agent 缓存命中机制调研报告

> 调研对象：Claude Code、OpenAI Codex CLI、OpenAI Agents SDK、OpenCode，并对照本仓库 **Corvus**。  
> 目的：回答「别人怎么做 prompt cache？Corvus 是否过死板？可借鉴什么？」  
> 方法：官方文档 / 工程博文 / 开源代码（Agents SDK、Codex `client.rs`）交叉阅读；Corvus 对照本仓实现。  
> 日期：2026-08-06  

---

## 1. 结论摘要

### 1.1 共性（几乎所有成熟 agent 都同意）

1. **服务端 prompt cache = 精确前缀匹配（exact prefix match）**  
   请求从头部起与历史请求字节一致的部分可复用 KV；中途改一个字符，之后全部作废。
2. **布局纪律：静的在前、动的在后、历史 append-only**  
   tools / instructions / system → 历史 → 本轮新内容。
3. **配置变更优先「追加 delta 消息」而不是「改写早期消息」**  
   这是 Codex / Claude Code 与「死板钉死 system」的关键分叉点。
4. **工具面膨胀用 deferred / tool search 缓解**  
   不全量塞进前缀，避免 MCP 一连就 crater 整段 cache。
5. **压缩是有意的 cache reset**  
   接受下一 turn miss，换更短的可继续前缀；最好在 cache 仍热时 compact。

### 1.2 各家差异（一句话）

| 产品 | 策略气质 | 关键杠杆 |
|------|----------|----------|
| **Claude Code** | 分层 + 官方 breakpoint；动态尽量 **append**；用户可感知的 invalidation 文档 | `cache_control`、`<system-reminder>`、MCP deferred/tool search |
| **Codex CLI** | 严格 append-only + **session 级 `prompt_cache_key`**；配置变更插 developer/user 消息 | Responses API、`/responses/compact`、encrypted compaction |
| **OpenAI Agents SDK** | 框架层自动 **生成/粘住 `prompt_cache_key`**；可选 retention / explicit breakpoint / server compaction | `PromptCacheKeyResolver`、`context_management`、`ToolSearch`/`defer_loading` |
| **OpenCode** | 多 provider 适配：OpenAI 系 `setCacheKey`，Anthropic 系自动打 `cache_control` | `transform.ts` applyCaching、插件稳定 key |
| **Corvus** | **boot 钉死 system 前缀** + 历史 append-only + 本地 `PrefixShape` 诊断；Anthropic 已有 `cache_control`；动态几乎只进 user tail | cache-first 纪律很强；**缺** sticky key / 配置 delta-append / Full 默认 deferred MCP；断点已有、勿重做 |

### 1.3 对「Corvus 太死板」的判断

**死板的是「热更新策略」与「Provider 适配面」，不是「append-only 本身」。**

- 该保留：历史不改写、CreatedAt/LocalOnly 剥离、tool 注册序稳定、compact 低频、Anthropic 现有 breakpoint。  
- 显得死板的点：
  1. 会话中 **几乎禁止改 system/tools**（Economy `connect_tool_source` 会直接 `tools` churn）。
  2. 配置/权限/cwd 变化没有 Codex 式 **delta developer 消息** 路径。
  3. 没有 Codex/Agents SDK 的 **`prompt_cache_key` 粘性路由**（对网关/多副本特别关键）。
  4. ~~没有 Claude 式显式 cache breakpoint~~ **已修正：** Anthropic 适配器已在 system 末（或 tools 末）+ 末消息末块打 `cache_control` ephemeral，DeepSeek 关闭；实现计划只需巩固测试，勿从零重做。  
  5. MCP 默认 lazy schema 有，但 **Full 仍可能把 MCP schema 塞进 tools**；deferred / tool search 不如 Claude 产品化。
  6. 本地 `PrefixShape` 很好，但偏「诊断」，缺少产品级 **invalidation 提示/确认**（Claude 改 effort 会弹确认）。

下文展开证据与可借鉴清单。

---

## 2. Prompt Cache 底层机制（共享背景）

所有对象都建立在同一物理事实上：

```
请求 = [静的前缀][累积历史][本轮新增]
         ▲ 可缓存（命中时只付 cache read 价）
                          ▲ 必算（cache miss / write）
```

| 提供方 | 典型字段 | 客户端额外手段 |
|--------|----------|----------------|
| DeepSeek | `prompt_cache_hit/miss_tokens` | 自动前缀（无 sticky key） |
| OpenAI | `cached_tokens` / `cache_write_tokens` | **`prompt_cache_key`**、retention、GPT-5.6+ explicit breakpoint |
| Anthropic | `cache_read/creation_input_tokens` | **`cache_control: ephemeral`**（最多约 4 断点）、TTL 5m/1h |

**精确匹配**意味着：客户端的「缓存策略」= **如何组织请求字节 + 如何路由到同一缓存实例**，而不是本地存答案。

---

## 3. Claude Code

### 3.1 来源

- 官方文档：[How Claude Code uses prompt caching](https://code.claude.com/docs/en/prompt-caching)  
- API：[Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)  
- 社区逆向：system 分块 + tools 末尾 breakpoint、CLAUDE.md 不塞死 system 等

### 3.2 请求分层（官方表）

| 层 | 内容 | 何时变 |
|----|------|--------|
| System | 核心指令、**工具定义**、output style | 工具集合变、升级 Claude Code |
| Project context | CLAUDE.md、auto memory、rules | 会话开始、`/clear`、`/compact` |
| Conversation | 消息、工具结果 | 每 turn |

**改 conversation → 前两层仍可 hit。**  
**改 system → 整段作废。**

另外 **model / effort** 不在文本里，但仍是 cache key 的一部分；改 effort 会弹确认。

### 3.3 灵活之处（相对 Corvus）

1. **Plan mode / Skills / Commands：只 append 消息**  
   不换 system、不换 tools → 前缀完整保留。  
2. **文件变更：`<system-reminder>` 追加**  
   不回写历史 read 结果；模型需要时再 read。  
3. **CLAUDE.md 中途编辑：不立刻应用**  
   保持当前 session 的 project 层字节稳定；`/clear`/`/compact`/重启才加载新内容。  
   → **用「延迟生效」换 cache 稳定**，而不是「热改 system」。  
4. **MCP Tool Search / deferred tools（默认，支持的模型上）**  
   - 延迟加载：连/断 MCP **只 append**，不炸已缓存前缀  
   - 前缀加载：连/断会 invalidation  
5. **Rewind：截断到旧前缀**  
   可 **重新命中更早的 cache entry**（TTL 内且路径曾被后续 turn 刷热）。  
6. **Warm compact**  
   摘要请求与会话共享 system+tools+history，热 cache 时 compact 本身很便宜。

### 3.4 会 invalidation 的操作（产品文档明确列出）

换 model、改 effort、fast mode、MCP 前缀加载时的连断、deny 整个内置工具、compact、升级 CLI 等。

### 3.5 对 Corvus 的启示

| Claude 做法 | Corvus 现状 | 可借鉴 |
|-------------|---------------|--------|
| 动态用 append / reminder | plan/delivery/memory 已在 user tail | 可产品化「system-reminder」统一通道 |
| 配置延迟到 clear/compact | memory 已类似；部分 UI 热改会触 tools | 热改 tools 前警告；或 deferred |
| MCP deferred | Economy connect 改 tools | Delivery/Full 也应对 MCP 默认 deferred |
| effort 变更确认 | 较少用户可见 invalidation 教育 | 模式/模型切换时提示 cache 代价 |
| rewind 命中旧 cache | checkpoint 有 rewind | 保证截断后 wire 字节与历史一致 |

---

## 4. OpenAI Codex CLI

### 4.1 来源

- 工程博文：[Unrolling the Codex agent loop](https://openai.com/index/unrolling-the-codex-agent-loop/)（Michael Bolin）  
- Cookbook：[Prompt Caching 201 – Learnings from Codex](https://developers.openai.com/cookbook/examples/prompt_caching_201)  
- 开源：`openai/codex`，`codex-rs/core/src/client.rs` 等

### 4.2 Agent loop 与 cache

Codex **故意不用** `previous_response_id`（ZDR / 无状态友好），每轮重发完整 `input`。  
因此 **唯一** 让采样从 O(n²) 变 O(n) 的手段是：**旧 prompt 必须是新 prompt 的精确前缀**。

工具轮：append `reasoning` + `function_call` + `function_call_output`  
用户轮：append 上一 assistant + 新 user  

博文原意：*the old prompt is an exact prefix of the new prompt — intentional for prompt caching*.

### 4.3 `prompt_cache_key`（源码）

```rust
// codex-rs/core/src/client.rs
fn prompt_cache_key(&self, responses_metadata: &CodexResponsesMetadata) -> String {
    self.prompt_cache_key_override
        .clone()
        .unwrap_or_else(|| responses_metadata.session_id.clone())
}
// 每个 Responses 请求带 Some(prompt_cache_key)
```

含义：

- **key = session_id**（可 override）  
- 作用不仅是「标记会话」，更是 **路由粘性**：同一 key + 相同前缀更容易打到持有 KV 的后端  
- compact 请求同样带该 key  

Corvus / DeepSeek 路径 **目前没有等价物**（DeepSeek 自动 cache 也不暴露 key；但经 OpenAI 兼容网关时 sticky key 往往决定命中率）。

### 4.4 配置变更：delta-append（关键差异）

Codex 明确避免中途改早期 messages，而是：

| 变更 | 做法 |
|------|------|
| sandbox / approval | **append** 新 `developer` 消息（同格式 permissions block） |
| cwd | **append** 新 `user` 消息（同格式 environment_context） |
| tools 集合 / 顺序 | 尽量固定；MCP `list_changed` 中途应用会 miss（已知风险） |
| model | 会 miss（instructions 模型相关） |

这与 Corvus「权限/模式尽量不进 prompt 或只进 gate」不同：**Codex 把可变配置放进 transcript 尾部**，用一次 miss 的局部代价换 **可热改且不重写前缀**。

### 4.5 Compaction

- 早期：手动 `/compact` + 摘要  
- 现：自动阈值后调 **`/responses/compact`**  
- 返回含 `type=compaction` + **encrypted_content** 的 item 列表 → 替换 `input`  
- 保留模型对历史的 latent 理解（ZDR 下 encrypted 仍可服务端解密）  
- 第一 post-compact turn miss，同 `prompt_cache_key` 下后续再 warm  

Corvus compact 是 **本地 summarize 重写 messages**（`<compaction-summary>`），无 encrypted latent、无服务端 compact 端点。

### 4.6 对 Corvus 的启示

1. **OpenAI/Responses 路径必须支持 `prompt_cache_key=sessionID`**  
2. **热配置用 append delta，而不是改 system 或要求重启**  
3. 工具顺序稳定性（Codex 曾修 MCP 枚举序 bug）— Corvus 已有类似测试，应继续  
4. 评估 Responses compact / encrypted content（若走 OpenAI 生态）  

---

## 5. OpenAI Agents SDK（开源重点）

仓库：[`openai/openai-agents-python`](https://github.com/openai/openai-agents-python)  
调研版本：main 分支源码（本地 sparse clone + raw 文件）。

### 5.1 自动 `prompt_cache_key`（`PromptCacheKeyResolver`）

路径：`src/agents/run_internal/prompt_cache_key.py`

设计要点（源码注释直译）：

- Runner **每个 model turn** 都要 key  
- **同一 Runner 调用内** key 固定；写入 `RunState` 供 resume  
- 若用户已在 `ModelSettings.extra_args/extra_body` 提供 `prompt_cache_key` → **不覆盖**  
- 仅当 model 声明 `_supports_default_prompt_cache_key()`（官方 OpenAI client）才自动生成  

Key 生成：

| 分组 | 格式 |
|------|------|
| 有 conversation_id / session / group_id | `agents-sdk:{kind}:{sha256[:32]}` |
| 都没有 | `agents-sdk:run:{run_id}`（**仅本 run 内多轮 tool loop 共享**） |

`run_loop.py` 在真正 `stream_response` 前：

```python
prompt_cache_key = prompt_cache_key_resolver.resolve(...)
model_settings = model_settings_with_prompt_cache_key(model_settings, prompt_cache_key)
```

**这是 SDK 相对「裸循环」最大的 cache 工程化：默认粘性，零配置。**

### 5.2 ModelSettings 上的 cache 旋钮

`model_settings.py`：

| 字段 | 作用 |
|------|------|
| `prompt_cache_retention` | `"in_memory"` / `"24h"` 延长前缀存活 |
| `prompt_cache_options` | e.g. `{"mode":"explicit","ttl":"30m"}` + content breakpoint |
| `context_management` | e.g. `[{"type":"compaction","compact_threshold":200000}]` **服务端 compaction** |
| `extra_args["prompt_cache_key"]` | 用户显式 key |

`openai_responses.py` 把 `prompt_cache_retention` / `prompt_cache_options` / `context_management` 原样传入 `responses.create`。

### 5.3 Session 与 compaction

- `Session`：客户端历史存储（SQLite/Redis/…），保证多 `Runner.run` 共享同一 transcript → 共享前缀  
- `OpenAIResponsesCompactionSession`：历史变长后调 **`responses.compact`**  
  - 默认阈值：≥10 个 compaction candidate（排除 user 消息）  
  - 可自定义 `should_trigger_compaction`  
  - 与本地 tool 输出协调：可 **defer compaction** 避免丢未入服务端的本地结果  

另有 sandbox `context_management` compaction capability：收到 compaction item 后 **截断到该 item 之后**。

### 5.4 工具面：`defer_loading` + `ToolSearchTool`

SDK 导出 `ToolSearchTool`、`ToolSearchCallItem`；function tools 可 `defer_loading=True`。  
与 Claude MCP tool search 同构：**大工具集不进稳定前缀，按需加载**。

### 5.5 Agents SDK **不**强制的部分

- 不帮你把动态配置改成 delta-append（那是应用层 / Codex 产品逻辑）  
- 不强制 instructions 不可变——**动态 instructions callable 会破坏前缀**（文档建议静态）  
- 跨非 OpenAI provider：默认 **不** 注入 `prompt_cache_key`  

### 5.6 对 Corvus 的启示

| SDK 能力 | 建议映射到 Corvus |
|----------|---------------------|
| `PromptCacheKeyResolver` | `sessionPath` / session id → OpenAI/Responses 请求 |
| `prompt_cache_retention` | 长会话可选 24h |
| `prompt_cache_options` + breakpoint | Anthropic/新 OpenAI 显式断点：tools 末、system 末、历史末 |
| `context_management` / Responses compact | 若支持 Responses，可混合本地 compact |
| `defer_loading` + ToolSearch | 对齐 `use_capability` / Economy，但默认 Full 也 deferred MCP |
| RunState 持久化 key | resume 后继续同一 key |

---

## 6. OpenCode

### 6.1 来源

- 官方配置文档、社区文章、GitHub `anomalyco/opencode`（`provider/transform.ts` applyCaching 等描述）  
- 插件如 `opencode-context-cache`（稳定 SHA256 key）

### 6.2 多 Provider 适配策略

| Provider 类型 | 做法 |
|---------------|------|
| OpenAI 兼容 / 代理 | `setCacheKey: true` → 发送 `promptCacheKey`；缺 key 时网关负载均衡导致 **0% hit** |
| Anthropic / Claude | 自动注入 `cache_control: {type: ephemeral}`（system 与近端消息） |
| Bedrock 等 | `cachePoint` 等变体 |

社区报告：代理场景打开 `setCacheKey` 后 hit 从近 0 到 90%+、费用大幅下降——说明 **路由粘性与前缀稳定同等重要**。

### 6.3 上下文

- Plan / Build 双 agent、session SQLite、阈值 compact  
- 已知 issue：动态 tools / 合并 system 仍会破前缀（与各家相同）

### 6.4 对 Corvus 的启示

Corvus 已服务 DeepSeek 等多后端：

1. **按 provider kind 分支 cache 策略**（现主要是 usage 归一，缺请求侧注入）  
2. 经第三方网关时 **session sticky key + 文档说明**  
3. Anthropic 路径 **已主动打 breakpoint**；保持回归测试，勿重复实现  

---

## 7. Corvus 现状对照（简）

详见 `PROJECT_DEEP_DIVE.zh-CN.md` §11。此处只列与调研相关的点：

| 维度 | Corvus | 业界更灵活做法 |
|------|----------|----------------|
| 历史 | append-only ✅ | 同 |
| System | boot 钉死；中途改走 tail 或下 session | Claude 延迟生效；Codex delta append |
| Tools | 顺序固定 ✅；Economy connect 改集合 | deferred / tool search |
| Sticky key | 无 | Codex/SDK/OpenCode 标配（OpenAI 系） |
| Breakpoint | Anthropic 路径已有 `cache_control`（DeepSeek 关）；OpenAI 无 explicit | Claude / SDK explicit |
| Compact | 本地 summarize + rewriteVersion | + 服务端 compact / encrypted |
| 诊断 | `PrefixShape` + UI cached/new ✅ | + 用户确认 invalidation |
| 配置热更 | 偏重启/重建 Controller | append permissions/env |

**合理的「死板」**：DeepSeek 自动前缀 + 无 sticky key API 时，客户端只能靠字节稳定——Corvus 在这条路上做得很彻底。  
**不合理的「死板」**：把「字节稳定」推到「任何热能力扩展都必须炸 tools 前缀」或「永远不能 session 内改配置」——Claude/Codex 已证明有第三路径。

---

## 8. 对比总表

| 能力 | Claude Code | Codex | Agents SDK | OpenCode | Corvus |
|------|-------------|-------|------------|----------|----------|
| Append-only 历史 | ✅ | ✅ | ✅（Session） | ✅ | ✅ |
| 静前动后布局 | ✅ 分层文档化 | ✅ | 应用负责 | 部分 | ✅ 很强 |
| 配置 delta-append | 部分（reminder） | ✅ 明确 | 可选 | 弱 | ❌ 弱 |
| Sticky `prompt_cache_key` | N/A（Anthropic） | ✅ session_id | ✅ 自动生成 | ✅ setCacheKey | ❌ |
| 显式 breakpoint | ✅ | 新模型可选 | ✅ options | ✅ Anthropic 自动 | ✅ Anthropic 已有；OpenAI 无 |
| Deferred tools / search | ✅ 默认 MCP | 有限 | ✅ ToolSearch | 插件/配置 | 部分（capability/economy） |
| 服务端 compact | 否（本地摘要） | ✅ `/responses/compact` | ✅ | 本地 | 本地 |
| Invalidation 产品化 | ✅ 文档+确认 | 工程纪律 | 框架默认 | 社区踩坑 | 诊断有、教育少 |
| 多 Provider | Anthropic 中心 | OpenAI 中心 | OpenAI 优先 | 多后端 | 多后端但策略偏 DeepSeek |

---

## 9. 可借鉴路线图（建议优先级）

### P0 — 低风险、高收益（对齐 SDK/Codex/OpenCode）

1. **OpenAI / Responses / 兼容网关：注入 `prompt_cache_key`**  
   - 参考 Agents SDK `PromptCacheKeyResolver` + Codex `session_id`  
   - 配置：`[agent] prompt_cache_key = auto|on|off|custom` + 可选 `prompt_cache_key_value`  
   - **DeepSeek 全 kind hard-omit**（含 DeepSeek Responses，不可照抄「responses 一律发 key」）  
   - 未知网关：`auto` 可发 key，但 **Phase 1 必须 fail-open**（400 拒识后按 fingerprint 关闭）  
2. **Anthropic breakpoint：巩固测试 + 文档，不重做**  
   - 现有：`internal/provider/anthropic` system/tools 末 + 末消息；DeepSeek 关闭  
3. **模型/TokenMode/工具面热切换：UI Notice「将降低 cache 命中」**  
   - Phase 1 Notice-only；确认框可后续对齐 Claude effort  

### P1 — 降低「死板感」且不牺牲长会话命中

4. **MCP 默认 deferred（tool search / use_capability 已有雏形）**  
   - Full/Balanced 默认不把全部 MCP schema 塞进首前缀；eager 可 opt-in  
   - 会改现有 Full「不得暴露 use_capability」契约，以 design Phase 3 为准  
5. **配置热更 delta-append 通道**  
   - 权限/plan/cwd 等 **不改 tools 集合** 的 knobs → `<runtime-update>`  
   - **Token mode 不在「SystemHash 稳定」验收内**（会改 tools/system）  
   - **禁止** 改写已发送 system 消息  
6. **统一 `<system-reminder>` 风格注入（可选）**  
   - 文件变更、hook、goal 心跳、memory 更新可收敛标签；非阻塞  

### P2 — 结构升级

7. **分层 cache 文档与测试**  
   - 像 Claude 一样定义 L0 tools+system / L1 project / L2 conversation  
   - e2e：delta-append 后 common-prefix 仍覆盖 L0+L1  
8. **Responses compact 可选**（仅 OpenAI 生态）  
9. **`prompt_cache_retention` / OpenAI explicit breakpoint** 配置透出  

> 实现契约以 `docs/superpowers/specs/2026-08-06-prompt-cache-hit-optimization-design.md` 为准。  

### 不建议照搬

- 为了「灵活」而每 turn 重写 system（必 miss）  
- 无 deferred 时中途狂加 MCP  
- 用百分比单独判断 cache 健康（Corvus `(cached/new)` 绝对值更好）  

---

## 10. 针对「太死板」的产品表述建议

对外/对内可改成：

> Corvus 的 cache 策略是 **「前缀纪律 + 尾部弹性」**。  
> 当前实现把弹性主要放在 user-turn tail，Anthropic 断点已就位；下一步补齐 **sticky key、deferred 工具、配置 delta-append 与 invalidation UX**，在不牺牲 DeepSeek 长会话命中的前提下，对齐 Claude Code / Codex 的热更新体验。

而不是：

> 一切必须 boot 钉死，中途不能变。

---

## 11. 参考链接与源码锚点

### 文档 / 博文

- https://code.claude.com/docs/en/prompt-caching  
- https://platform.claude.com/docs/en/build-with-claude/prompt-caching  
- https://openai.com/index/unrolling-the-codex-agent-loop/  
- https://developers.openai.com/cookbook/examples/prompt_caching_201  
- https://developers.openai.com/api/docs/guides/prompt-caching  
- https://openai.github.io/openai-agents-python/  

### 源码

| 项目 | 路径 |
|------|------|
| Agents SDK | `src/agents/run_internal/prompt_cache_key.py` |
| Agents SDK | `src/agents/model_settings.py`（retention/options/context_management） |
| Agents SDK | `src/agents/run_internal/run_loop.py`（注入 key） |
| Agents SDK | `src/agents/memory/openai_responses_compaction_session.py` |
| Codex | `codex-rs/core/src/client.rs`（`prompt_cache_key`、`compact_conversation_history`） |
| Corvus | `internal/agent/cache_shape.go`、`compact.go`、`stream`、`provider/anthropic`（breakpoints）、`provider/*/normaliseUsage` |

### 本仓相关文档

- `PROJECT_DEEP_DIVE.zh-CN.md` §11 缓存命中机制  
- `DESIGN.zh-CN.md`  

---

## 12. 附录：Agents SDK key 解析伪代码

```
if model_settings 已含 prompt_cache_key:
    不生成
elif model 不支持 default key:
    不生成
elif 已有 generated（同 run / RunState）:
    复用
elif conversation_id | session | group_id:
    agents-sdk:{kind}:{sha256(value)[:32]}
else:
    agents-sdk:run:{run_id}   # 仅本 run 多轮
→ 写入 ModelSettings.extra_args["prompt_cache_key"]
→ responses.create(..., **extra_args)
```

Codex 更简单：

```
prompt_cache_key = override ?? session_id
```

---

*本报告为调研笔记，实现变更请以各仓库最新源码与官方文档为准。*
