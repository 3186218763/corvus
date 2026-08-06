# Prompt Cache 多 API 格式兼容调研（开源对照）

> 目的：回答「OpenAI 系 / Anthropic 系 / 兼容网关，客户端该怎么注入缓存相关字段才不 400、又能命中？」  
> 依据：开源代码实测阅读（非猜测）。  
> 日期：2026-08-06  

---

## 1. 先分清两套完全不同的机制

| 机制 | 谁用 | Wire 形态 | 作用 |
|------|------|-----------|------|
| **Prefix sticky key** | OpenAI Responses / Chat Completions、多数兼容网关 | 请求体顶层字段 | **路由粘性**：同一 key + 相同前缀更容易打到持有 KV 的机器 |
| **Cache breakpoints** | Anthropic Messages API、Bedrock Claude、部分兼容层 | 内容块 / tool / system 上的 `cache_control` 等 | **标记可缓存前缀边界**（最多约 4 个断点） |

**不能混用：**

- 给 Anthropic 原生发 `prompt_cache_key` → 通常无效或被拒  
- 给纯 OpenAI 发 Anthropic `cache_control` 块 → 通常无效  
- DeepSeek 自动前缀：**两边字段都可不发**（发了 Anthropic 侧还可能污染 wire）

Corvus 已有：

- Anthropic 适配器：`cache_control` 断点（**非 DeepSeek**）— `internal/provider/anthropic/anthropic.go`  
- OpenAI 适配器：**尚未**发 sticky key；有 `ExtraBody` 合并通道  

---

## 2. OpenCode（anomalyco/opencode）— 最完整的多格式矩阵

源码：`packages/opencode/src/provider/transform.ts`

### 2.1 Anthropic 风格：`applyCaching`

```ts
// 对「前 2 条 system + 后 2 条非 system」打上 providerOptions
anthropic:        { cacheControl: { type: "ephemeral" } }
openrouter:       { cacheControl: { type: "ephemeral" } }
bedrock:          { cachePoint: { type: "default" } }
openaiCompatible: { cache_control: { type: "ephemeral" } }  // snake_case
copilot:          { copilot_cache_control: { type: "ephemeral" } }
alibaba:          { cacheControl: { type: "ephemeral" } }
```

触发条件（摘要）：

- provider/model id 像 anthropic/claude，或 npm 为 `@ai-sdk/anthropic` / alibaba  
- **排除** `@ai-sdk/gateway`  
- 若已配置顶层 `cacheControl` 且走 Anthropic 自动缓存 → **不再**手动 `applyCaching`  

放置层级：

- 原生 anthropic / bedrock：**message 级** `providerOptions`  
- 其它：优先 **最后一块 content** 的 `providerOptions`  

### 2.2 OpenAI 风格：sticky key 字段名 **按 SDK 分叉**

同一文件 options 构造：

| 条件 | 写入字段名 | 值 |
|------|------------|-----|
| `@ai-sdk/deepinfra` 或 `@ai-sdk/cerebras` | **`prompt_cache_key`**（snake） | `sessionID` |
| `@ai-sdk/openai` / azure / xai / mistral / venice，**或** `setCacheKey === true` | **`promptCacheKey`**（camel） | `sessionID` |
| `@ai-sdk/gateway` | `gateway: { caching: "auto" }` | — |
| 默认 | `setCacheKey !== false` 才考虑注入 | — |

要点：

1. OpenCode 走 **Vercel AI SDK**，camelCase `promptCacheKey` 是 SDK 参数名，SDK 再映射到 HTTP。  
2. **HTTP REST 官方**（OpenAI 文档、Codex、Agents SDK）用的是 **`prompt_cache_key` snake_case**。  
3. Corvus **直连 HTTP**，应对齐 **Codex / Agents SDK 的 snake_case**，而不是 AI SDK 的 camelCase（除非某网关文档明确只要 camel）。  
4. `setCacheKey: true` 可强制给「未列入白名单」的 provider 也塞 key——对应「兼容网关」场景。

---

## 3. OpenAI Agents SDK — 官方 OpenAI 客户端默认 sticky key

源码：

- `src/agents/run_internal/prompt_cache_key.py`  
- `src/agents/models/openai_responses.py`  
- `src/agents/models/openai_chatcompletions.py`  

### 3.1 谁注入

- 仅当 model 声明 `_supports_default_prompt_cache_key()` → **官方 OpenAI client** 为 true  
- 非官方 / 第三方 client：**不自动注入**（避免未知字段 400）  
- 用户可在 `extra_args` / `extra_body` 自带 `prompt_cache_key` → resolver 不覆盖  

### 3.2 字段

```python
PROMPT_CACHE_KEY_FIELD = "prompt_cache_key"  # 固定 snake_case
extra_args[PROMPT_CACHE_KEY_FIELD] = key
# responses.create / chat.completions 经 create_kwargs.update(extra_args)
```

另有：

- `prompt_cache_retention`: `"in_memory" | "24h"`  
- `prompt_cache_options`: 如 `{"mode":"explicit","ttl":"30m"}`  
- content 上 `prompt_cache_breakpoint`（Chat/Responses 内容部件）  

Key 形状：

- `agents-sdk:{kind}:{sha256[:32]}`（conversation/session/group）  
- 或 `agents-sdk:run:{run_id}`（仅本 run 多轮 tool loop）  

### 3.3 对 Corvus 的启示

| SDK 做法 | Corvus 映射 |
|----------|---------------|
| 仅官方 OpenAI 默认开 | 官方 openai.com / 已知兼容列表默认 on；未知默认可配置 |
| 字段名 `prompt_cache_key` | OpenAI/Responses HTTP 体用 snake_case |
| 用户可 override | `Request` 或 config 允许自定义 key |
| 非支持模型不注入 | DeepSeek / 纯 Anthropic 不注入 key |

---

## 4. Codex CLI — Responses API + session_id

源码：`codex-rs/core/src/client.rs`

```rust
fn prompt_cache_key(...) -> String {
    override.unwrap_or_else(|| session_id.clone())
}
// ResponsesApiRequest { prompt_cache_key: Some(...), ... }
// compact 请求同样带 prompt_cache_key
```

- **始终**（对 Responses）带 sticky key  
- 字段：**snake_case `prompt_cache_key`**  
- 值：**session_id**（可 override）  
- 无 AI SDK camelCase 分叉  

Corvus `internal/provider/responses` 应对齐此形状。

---

## 5. LiteLLM — 透传 + usage 归一

Anthropic transform（`litellm/llms/anthropic/chat/transformation.py`）：

- 透传 message/tool 上的 `cache_control`  
- 支持顶层 `cache_control`（automatic caching）  
- tool 上可选 `cache_control`  
- usage 把 `cache_read` / `cache_creation` 折进 prompt 计量  

策略是 **网关透传调用方字段**，自己不瞎发明 provider 矩阵——和 OpenCode「按 npm/provider 分支」互补。

---

## 6. Corvus 现状对照

| 能力 | 现状 | 与开源差距 |
|------|------|------------|
| Anthropic `cache_control` | ✅ 已有（system 末 + 末消息；DeepSeek 关闭） | 与 OpenCode 类似；OpenCode 还打 openrouter/bedrock/copilot 变体名 |
| OpenAI `prompt_cache_key` | ❌ 未发 | 落后 Codex/SDK/OpenCode |
| Responses sticky key | ❌ | 落后 Codex |
| 按 host/kind 决策矩阵 | 仅 deepseek 特判 anthropic | 需显式 CacheProfile |
| 网关 force key | ❌ | OpenCode `setCacheKey: true` |
| camel vs snake | N/A（直连 HTTP） | **应 snake**；勿照抄 AI SDK camel |

---

## 7. 推荐兼容矩阵（给 Corvus 设计用）

### 7.1 决策输入

- `provider.kind`: `openai` | `anthropic` | `responses`  
- `base_url` / model 指纹（是否 deepseek、是否 api.openai.com、是否 azure…）  
- 用户配置：`prompt_cache_key = auto | on | off | custom`（custom 时用 `prompt_cache_key_value`）

### 7.2 Wire 动作

| 判定 | Sticky key | Breakpoints |
|------|------------|-------------|
| kind=anthropic 且 **非** deepseek | 不发 | **保持** 现有 `cache_control` ephemeral |
| kind=anthropic 且 deepseek | 不发 | **不发** cache_control（现有） |
| kind=openai 且 DeepSeek-shaped（`IsDeepSeek` / 现有 host 检测） | 不发 | 不发 |
| kind=openai 且 OpenAI/Azure 官方或已知兼容 | 发 **`prompt_cache_key`** = session 稳定 id | 不发 |
| kind=openai 且未知 base_url | `auto`→发 key（网关粘性优先）+ **Phase 1 fail-open**；`off` 可关 | 不发 |
| kind=responses 且 DeepSeek-shaped | **不发**（Corvus 上 DeepSeek 走 Responses；不可照抄 Codex「一律发」） | 不发 |
| kind=responses 且非 DeepSeek | 发 **`prompt_cache_key`**（对齐 Codex） | 不发 |
| 用户 `prompt_cache_key=off` | 永不发 | Anthropic 断点仍可保留（独立开关可选） |

### 7.3 字段名规范（Corvus 直连 HTTP）

```
OpenAI / Responses / 多数 OpenAI 兼容 REST:
  JSON: "prompt_cache_key": "<session-stable-id>"

Anthropic Messages:
  system[].cache_control = {"type":"ephemeral"}
  messages[-1].content[-1].cache_control = {"type":"ephemeral"}
  （已实现）

不要默认发:
  promptCacheKey          // AI SDK 层参数，不是 REST 标准
  copilot_cache_control   // 除非明确 GitHub Copilot 端点
  cachePoint              // 除非明确 Bedrock 原生请求形状
```

若未来接入 **Bedrock 原生** 或 **OpenRouter 特殊路径**，再按 OpenCode 表扩展 `providerOptions` 映射；当前 Corvus 只有 openai/anthropic/responses 三套 HTTP 客户端，**先做这三套**。

### 7.4 400 / 拒识策略（兼容网关）

开源里：

- Agents SDK：**默认不对非官方 client 注入**（最保守）  
- OpenCode：**白名单 + setCacheKey 强制**（网关友好）  

Corvus 建议折中（**以实现 design 为准**）：

1. `auto`：官方 OpenAI/Azure/非 DeepSeek Responses → on；DeepSeek-shaped（任意 kind）→ off；其它 openai-compatible → **on**（网关命中是主诉求）  
2. **Phase 1 必做 fail-open**：provider fingerprint（kind+host）若 400 明确拒识 `prompt_cache_key` → 进程内关闭该 fingerprint 并 Notice  
3. 配置强制 `on`/`off`/`custom` 覆盖 auto；DeepSeek hard-omit 仍优先  
4. 配置用 `prompt_cache_key` + 可选 `prompt_cache_key_value`（避免 `custom:…` 冒号歧义）

### 7.5 Session ID 形态

对齐开源：

| 项目 | key 内容 |
|------|----------|
| Codex | `session_id` 原样 |
| Agents SDK | 带命名空间 hash：`agents-sdk:session:…` |
| OpenCode | `sessionID` 原样 |

Corvus 建议：

- 默认：`corvus:session:<BranchID>`（`BranchID` = session 文件 path stem，resume 不变）  
- Subagent：`corvus:session:<parentBranchID>:sub:<subID>`（避免共用一个 OpenAI 路由桶）  
- 或后期配置 `prompt_cache_key_format = raw | namespaced`

---

## 8. 与「方案 B」设计的衔接（修订）

原设计写「openai 兼容默认 on」方向正确，但需补上开源已验证的细节：

1. **字段名用 `prompt_cache_key`（snake）**，不要抄 OpenCode 的 `promptCacheKey`（那是 AI SDK）。  
2. **Anthropic 断点已存在** → Phase 1 以「巩固测试 + 文档」为主，不是从零实现。  
3. **Responses 必须单独接线**；**DeepSeek Responses 不发 key**（与 openai/anthropic DeepSeek 一致）。  
4. **DeepSeek 三协议（openai + responses + anthropic）都禁止多余 cache 字段**。  
5. **能力探测** 用 kind+host 矩阵，而不是「所有 openai/responses 一刀切」。  
6. **未知网关 fail-open 属于 Phase 1**，不要推到「见到再做」。  
7. 可选后期：Bedrock/OpenRouter 字段变体、`prompt_cache_retention`——**非本期**。

---

## 9. 源码锚点

| 项目 | 路径 | 内容 |
|------|------|------|
| OpenCode | `packages/opencode/src/provider/transform.ts` | `applyCaching`、`prompt_cache_key`/`promptCacheKey` 分叉 |
| Agents SDK | `run_internal/prompt_cache_key.py` | 默认 key 生成与 opt-out |
| Agents SDK | `models/openai_responses.py` | `extra_args` 合并、`prompt_cache_retention` |
| Codex | `codex-rs/core/src/client.rs` | `prompt_cache_key = session_id` |
| LiteLLM | `litellm/llms/anthropic/chat/transformation.py` | cache_control 透传与 usage |
| Corvus | `internal/provider/anthropic/anthropic.go` | 已有断点 |
| Corvus | `internal/provider/openai/openai.go` | `MarshalJSON` + ExtraBody；缺 sticky key |

---

## 10. 一句话

**兼容 = 按协议族分支，而不是「开一个开关全发」。**  
OpenAI 族（含非 DeepSeek Responses）发 **sticky key（snake_case）**；Anthropic 族发 **ephemeral breakpoints**；DeepSeek 族（openai / responses / anthropic）**两边都别乱发**。未知网关可默认发 key，但必须能 fail-open。这与 OpenCode / Agents SDK / Codex 的精神一致，并修正「Responses 一律 Codex」在 DeepSeek 上的误套。
