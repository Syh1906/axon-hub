# 渠道配置指南

本指南介绍如何在 AxonHub 中配置 AI 服务提供商（如 OpenAI、Anthropic、DeepSeek 等）。

## 什么是渠道？

**渠道**是 AxonHub 连接 AI 提供商的通道。你可以把渠道理解为"供应商连接线"——每个渠道对应一个 AI 服务商（如 OpenAI、Claude、DeepSeek）。

通过渠道，你可以：
- 同时连接多个 AI 服务商
- 设置模型名称转换规则
- 启用或暂停某个服务商
- 配置多个 API Key 实现负载均衡

## 渠道模型映射在请求流程中的位置

渠道模型映射是三层流水线中的**最后一步**。完整说明请参阅 [请求处理流程](../getting-started/request-processing.md#核心概念三层模型设置)。

简单来说：**API Key Profile 改模型名 → 模型关联选渠道 → 渠道改模型名 → 发给上游**

## 创建渠道

### 基本步骤

1. 进入 AxonHub 管理界面 → **渠道管理**
2. 点击 **新建渠道**
3. 填写基本信息：
   - **名称**：给渠道起个名字（如"OpenAI 主账号"、"DeepSeek 国内"）
   - **类型**：选择服务商类型（OpenAI、Anthropic、DeepSeek 等）
   - **Base URL**：API 地址（一般使用默认值即可）
   - **API Key**：服务商提供的密钥

### 配置示例

**OpenAI 渠道：**

| 字段 | 值 |
|------|-----|
| 名称 | OpenAI 主账号 |
| 类型 | openai |
| Base URL | https://api.openai.com/v1 |
| API Key | sk-your-openai-key |
| 支持模型 | gpt-4o, gpt-4o-mini, gpt-5 |

**DeepSeek 渠道：**

| 字段 | 值 |
|------|-----|
| 名称 | DeepSeek 国内 |
| 类型 | deepseek |
| Base URL | https://api.deepseek.com/v1 |
| API Key | sk-your-deepseek-key |
| 支持模型 | deepseek-chat, deepseek-reasoner |

## 配置多个 API Key

当一个账号有多个 API Key 时，可以都配置到同一个渠道中，AxonHub 会自动轮流使用，提高稳定性。

在渠道编辑界面的 **API Key** 区域，逐行添加多个 Key 即可，例如：
- `sk-key-1`
- `sk-key-2`
- `sk-key-3`

### 负载均衡说明

- 相同的 Trace ID 会始终使用同一个 Key（保证会话一致性）
- 不同请求会随机选择可用的 Key
- 某个 Key 出错时，系统会自动切换到其他 Key

## 模型重命名

AxonHub 在渠道层面提供多种模型重命名和别名机制。当请求到达时，渠道按以下优先级链解析请求模型到实际上游模型：

1. **直接匹配** — 请求模型直接在支持模型列表中
2. **额外模型前缀** — 为所有支持模型添加前缀别名
3. **自动裁剪模型前缀** — 从支持模型中去除已知前缀，创建精简别名
4. **模型映射** — 显式 `from → to` 别名配对

> **注意**：如果多个机制产生了相同的请求模型名，则第一个匹配生效（按上述顺序）。

### 模型映射

**什么时候需要模型映射？**

当你想让客户端用一个名称请求，但实际发给上游的是另一个名称时。

**常见场景：**

1. **客户端用简化的名称**：客户端请求 `gpt-4`，实际发给 OpenAI 的是 `gpt-4o`
2. **统一不同渠道的模型名**：让 `claude-sonnet` 和 `gpt-4` 都指向同一个实际模型
3. **旧版兼容**：客户端请求旧版模型名，自动映射到新版

**配置方法：**

在渠道的 **Settings** → **模型映射** 中添加 `from → to` 配对：

| 客户端请求的模型名 (from) | 实际发给上游的模型名 (to) |
|--------------------------|--------------------------|
| gpt-4o-mini | gpt-4o |
| claude-3-sonnet | claude-3.5-sonnet |

**注意**：目标模型（`to`）必须在支持模型列表中。如果目标模型不在列表中，该映射将被静默忽略。

### 额外模型前缀（Extra Model Prefix）

为支持模型列表中的每个模型添加**带前缀的别名**，允许客户端使用带前缀或不带前缀的格式请求模型。

**使用场景**：你想将渠道中的所有模型归入统一前缀命名空间（如 `deepseek/`）。

**示例：**
- 支持模型：`deepseek-chat`、`deepseek-reasoner`
- 额外模型前缀：`deepseek`

渠道现在**同时**接受以下两种请求格式：
- `deepseek-chat` → 发送 `deepseek-chat` 给上游
- `deepseek/deepseek-chat` → 发送 `deepseek-chat` 给上游

当不同渠道存在同名模型时，客户端可以通过前缀来区分来源渠道。

### 自动裁剪模型前缀（Auto-Trimmed Model Prefixes）

自动**去除支持模型名中的指定前缀**，创建精简别名。这是额外模型前缀的反向操作。

**使用场景**：像 OpenRouter、SiliconFlow 等供应商会在模型名前加上供应商前缀（如 `openai/gpt-5.4`）。你想让客户端直接使用短名 `gpt-5.4` 请求，而不必为每个模型手动创建映射。

**示例：**
- 支持模型：`openai/gpt-5.4`、`anthropic/claude-sonnet-4`、`deepseek-ai/deepseek-chat`
- 自动裁剪模型前缀：`openai`、`anthropic`、`deepseek-ai`

渠道同时接受原始名和精简名：
- `gpt-5.4` → 发送 `openai/gpt-5.4` 给上游
- `claude-sonnet-4` → 发送 `anthropic/claude-sonnet-4` 给上游
- `deepseek-chat` → 发送 `deepseek-ai/deepseek-chat` 给上游
- `openai/gpt-5.4` → 作为直接匹配仍然有效

> **提示**：对于使用供应商前缀模型 ID 的供应商，推荐使用此功能。它可以批量重写模型 ID，无需逐个创建模型映射。

### 模型名称转小写（Lowercase Model Name）

启用后，模型名称的匹配键会转为小写，在渠道下发模型名包含大写字母时开启，可实现与其他小写模型名共同负载均衡。发送给提供商的模型名保留原始大小写。

**示例**：将 `GLM-5.1` 转换为 `glm-5.1`，使该渠道可与其他下发 `glm-5.1` 的渠道共同负载均衡。

> **注意**：启用此选项后，仅大小写不同的模型名（如 `GPT-4` 和 `gpt-4`）会被视为同一个模型。冲突时保留优先级最高的条目（direct > auto_trim > mapping > prefix）。

### 可见性控制

两个选项控制哪些模型名在模型列表中可见（例如客户端调用 `/v1/models` 接口时）：

| 选项 | 效果 |
|------|------|
| **隐藏原始模型** | 隐藏原始（直接匹配）的模型名。仅显示经过转换的名称（来自前缀、自动裁剪或映射）。 |
| **隐藏映射模型** | 隐藏模型映射的 `from` 名称。仅显示原始模型名。 |

**示例 — 隐藏原始模型：**
- 支持模型：`openai/gpt-5.4`
- 自动裁剪前缀：`openai`
- 隐藏原始模型：启用

`/v1/models` 响应只显示 `gpt-5.4`，不显示 `openai/gpt-5.4`。两个名称都可用于请求。

**示例 — 隐藏映射模型：**
- 支持模型：`gpt-4o`
- 模型映射：`gpt-4` → `gpt-4o`
- 隐藏映射模型：启用

`/v1/models` 响应显示 `gpt-4o` 但隐藏 `gpt-4`。两个名称都可用于请求。

## 测试和启用渠道

### 测试连接

在启用渠道前，建议先测试连接：

1. 在渠道列表中找到刚创建的渠道
2. 点击 **测试** 按钮
3. 等待测试结果
4. 如果显示成功，说明配置正确

### 启用渠道

测试通过后，点击 **启用** 按钮，渠道状态变为 **活跃**，即可开始接收请求。

## Base URL 特殊配置

### 默认地址

| 服务商 | 默认 Base URL |
|-------|--------------|
| OpenAI | `https://api.openai.com/v1` |
| Anthropic | `https://api.anthropic.com` |
| DeepSeek | `https://api.deepseek.com/v1` |
| Gemini | `https://generativelanguage.googleapis.com/v1beta` |

### 自定义地址

如果使用代理或私有化部署，可以修改 Base URL。

**禁用版本号自动追加**：在 URL 末尾加 `#`
```
https://custom-proxy.example.com/api#
# 实际请求: /api/messages（不会自动加 /v1）
```

**完全原始模式**：在 URL 末尾加 `##`
```
https://custom-gateway.example.com/api##
# 实际请求: /api（不会加版本号和端点路径）
```

## 多协议端点与模型协议覆盖

一个渠道可以同时配置多个出站端点（Endpoint），覆盖 chat、responses、messages 等协议，使不同客户端以原生协议直连。

### 端点配置

在渠道的 **端点** 对话框中管理：

- 每个 endpoint 由 `api_format`、可选 `base_url`（留空继承渠道 Base URL）和可选 `path`（替换该协议的默认路径，配置后跳过版本号追加）组成。
- 各渠道类型自带默认端点（如智谱提供 `openai/chat_completions`、`zhipu_anthropic` 提供 `anthropic/messages`），自定义端点可补充其他格式或覆盖同名格式。

### 模型协议覆盖（ModelProtocols）

在 **模型协议覆盖** 区块为单个模型强制可用的出站协议：

```json
{ "model": "glm-5.3-flash", "apiFormats": ["openai/responses", "openai/chat_completions"], "enabled": true }
```

- `apiFormats` 按配置顺序表达优先级；客户端入站协议在列表中时优先直连，否则使用列表中的第一个协议并经统一转换管线转换。
- 启用的覆盖只能引用渠道已具备的 `api_format`（默认端点或自定义端点），保存时校验；模型被移出渠道支持列表时自动清理对应覆盖。
- 端点与协议覆盖在同一次保存中原子提交。

**示例**（GLM Coding Plan 渠道，三个端点 + 覆盖）：

| endpoint | api_format | base_url |
|---|---|---|
| 1 | `openai/chat_completions` | `https://open.bigmodel.cn/api/coding/paas/v4` |
| 2 | `openai/responses` | `https://open.bigmodel.cn/api/v1` |
| 3 | `anthropic/messages` | 继承渠道 Base URL（`https://open.bigmodel.cn/api/anthropic`） |

| 客户端 | 入站协议 | 选中出站协议 | 是否转换 |
|---|---|---|---|
| Claude Code | `anthropic/messages` | `anthropic/messages` | 否 |
| Codex | `openai/responses` | `openai/responses` | 否 |
| OpenAI SDK | `openai/chat_completions` | `openai/chat_completions` | 否 |
| Codex（覆盖仅含 chat 时） | `openai/responses` | `openai/chat_completions` | 是 |

### Provider 家族路由

自定义 chat 端点按渠道家族选择原生 transformer，而非通用 OpenAI transformer（通用 transformer 按 `/v1` 约定拼接 URL，对非 `/v1` 上游会产生错误地址）：

| 渠道类型 | Transformer | URL 版本 |
|---|---|---|
| zhipu / zai（含 `*_anthropic`） | zai | v4 |
| xiaomi（含 `xiaomi_anthropic`） | zai | v1 |
| doubao / volcengine（含 `*_anthropic`） | doubao | v3 |

家族 transformer 同时应用 provider 方言处理：剥离 `metadata`、按 6–128 字符约束裁剪 `user_id`、将 `reasoning_effort` 转换为 `thinking.type`。

## 思考等级（Reasoning Effort）

统一思考等级取值：`none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`。

### 渠道级映射（ReasoningEffortMapping）

在渠道 **转换选项** 中配置 `{from, to}` 列表，第一个命中的 `from` 生效。映射在 outbound 转换前统一应用，对所有客户端与所有出站协议（chat / responses / messages）生效，并同步覆盖 Anthropic 元数据中的原生 effort。

### 协议转换规则

- Anthropic messages：优先保留客户端原生表达（`adaptive` / `budget_tokens` 原样回环）；显式 effort 原样透传（含 `max`、`xhigh`）；`minimal` 归一为 `low`（唯一自动互转）。
- OpenAI chat / responses：`reasoning_effort` / `reasoning.effort` 原样透传。
- 上游不支持某等级时，原始报错直接透传给客户端；不兼容等级可通过渠道映射兜底。

## 强制 1 小时提示缓存

Anthropic 提示缓存默认 5 分钟过期。人与 agent 协作时，请求之间常因等待回复或短暂离开超过 5 分钟，过期后需要重新写入整段前缀。在渠道编辑对话框中勾选 **强制 1 小时提示缓存** 后，发往该渠道的 Anthropic Messages 请求中已有的缓存断点（`tools`、`system`、消息内容及其中 `tool_result` 的内容、顶层 `cache_control`）统一设为 `ttl: "1h"`，包括 AxonHub 自动补充的断点和请求体透传的请求。

- 只修改已有断点的 TTL，不新增断点；未使用提示缓存的请求不变。
- 只对以 Anthropic Messages 格式发出的请求生效，其他出站格式不受影响。
- 不添加 `anthropic-beta` 请求头；上游需要时可通过请求头覆盖自行添加。
- 是否真正按 1 小时缓存取决于上游。计费按上游响应中 5 分钟 / 1 小时写入分档计算，不按请求推断；响应只有写入总数时按写缓存基础价计费。1 小时写入的价格见 [成本追踪](cost-tracking.md#缓存写入变体anthropic)。
- 请求日志中，模型列在「出站协议」「透传」图标之后显示「1 小时缓存」图标：该请求的计费中有 1 小时写入缓存时点亮，否则为灰色，与计费完全一致，也与渠道是否开启本开关无关。请求详情的「用量」卡片在该情况下显示「1 小时缓存」徽章。只读取已有 1 小时缓存、没有新写入的请求不会点亮。

## 请求设置：渠道超时与会话复用

在渠道行的操作菜单中打开“请求设置”：

| 设置 | 作用 |
| --- | --- |
| 流式首响应超时（秒） | 等待上游第一个流事件，不限制后续事件间隔，也不等同于首个可见 token。 |
| 非流式响应超时（秒） | 等待上游返回完整非流式响应。 |
| 不复用会话渠道 | 不因上一轮会话而优先选择或绑定该渠道，普通选路仍可能选中它。 |

超时留空继承系统设置；填 `0` 表示不单独设置此项渠道超时；正整数为秒数，始终受整次请求的截止时间约束。
每次尝试都会读取实际所选渠道的值，失败切换后使用新渠道的值。渠道显式超时不依赖“允许重试”开关；
未覆盖的系统超时继续遵循已有重试策略。已经开始向客户端输出内容后，不会为了超时切换渠道重放。

“不复用会话渠道”同时作用于普通会话粘性和健康门控会话归属。它不关闭 Responses 的 `previous_response_id` 对话上下文恢复，
不禁止该渠道参与请求，也不保证每次请求一定换渠道。默认关闭，保持既有行为。

## 流式透传与重试

开启重试时，流元数据在首个有效内容事件前不会交付客户端。透传在预读阶段暂存原始事件，
不再阻塞转换链路。每次尝试在消费者接入前最多暂存 4096 个原始事件、64 MiB 的事件正文、
类型和 ID 字节，包括协议转换忽略的事件。任一预算超限即失败，不重试，也不暴露该尝试的
部分内容。消费者接入后按原序交付，由有界队列施加背压。

这会消除长时间空思考流的队列死锁，但不会提前发送心跳，也不会缩短等待有效内容的时间；
客户端和代理的等待超时仍然适用。

## 常见问题

### Q: 测试连接失败怎么办？

- 检查 API Key 是否正确（复制时是否有多余空格）
- 确认 Base URL 是否可访问
- 检查服务商账户是否有余额/额度

### Q: 请求时提示"模型未找到"？

- 确认模型已在渠道的 `supported_models` 中
- 检查模型映射配置是否正确
- 确认渠道已启用

### Q: 如何设置多个 API Key？

在 `credentials.api_keys` 中列出所有 Key，系统会自动轮询使用。

### Q: API Key 被禁用了怎么恢复？

进入渠道详情，在 **禁用列表** 中找到该 Key，点击 **恢复**。

## 相关文档

- [模型管理指南](model-management.md) - 配置模型与渠道的关联关系
- [API Key Profile 指南](api-key-profiles.md) - 配置模型映射和访问权限
- [请求处理流程](../getting-started/request-processing.md) - 了解完整请求链路
