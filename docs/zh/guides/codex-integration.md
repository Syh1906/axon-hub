# Codex 集成指南

---

## 概览
AxonHub 可以作为 OpenAI 接口的直接替代方案，使 Codex 能够通过您自己的基础设施连接。本文将介绍配置方法，并说明如何结合 AxonHub 的模型配置文件功能实现灵活路由。

### 关键点
- AxonHub 支持多种 AI 协议/格式转换。你可以配置多个上游渠道（provider/channel），对外提供统一的 OpenAI 兼容接口，供 Codex 使用。
- 你可以开启 `server.trace.codex_trace_enabled`（使用 `Session_id`）或配置 `server.trace.extra_trace_headers` 将 Codex 同一次对话的请求聚合到同一条 Trace。

### 前置要求
- 可访问的 AxonHub 实例。
- 拥有项目访问权限的 AxonHub API Key。
- Codex（OpenAI 兼容工具）的使用权限。
- （可选）已在 AxonHub 控制台配置好的一个或多个模型配置文件。

### 配置 Codex
1. 编辑 `${HOME}/.codex/config.toml`，将 AxonHub 注册为 provider：
   ```toml
   model = "gpt-5"
   model_provider = "axonhub-responses"

   [model_providers.axonhub-responses]
   name = "AxonHub Responses"
   base_url = "http://127.0.0.1:8090/v1"
   env_key = "AXONHUB_API_KEY"
   wire_api = "responses"
   model_catalog_url = "http://127.0.0.1:8090/codex/models"
   supports_websockets = true
   query_params = {}

   [features]
   api_key_model_discovery = true
   ```
2. 导出供 Codex 读取的 API Key：
   ```bash
   export AXONHUB_API_KEY="<your-axonhub-api-key>"
   ```
3. 重启 Codex 以加载配置。

#### 目录地址与返回格式

`base_url` 与 `model_catalog_url` 放在当前选中的 provider 表中，分别控制推理地址和目录地址。前者通常以 `/v1` 结尾，配合 `wire_api = "responses"`；后者必须指向 `/codex/models`，不能填 `/v1/models`。使用静态令牌时，可将示例的 `env_key` 换为 `experimental_bearer_token = "<AxonHub API Key>"`；不要把真实密钥写入可提交的配置或文档。

| 用途 | 请求 | 成功响应根结构 |
| --- | --- | --- |
| 普通 OpenAI 模型名单 | `GET /v1/models` | `{"object":"list","data":[{"id":"模型ID"}]}` |
| Codex 完整模型目录 | `GET /codex/models?client_version=0.166.0` | 根字段 `models`，元素以 `slug` 标识模型，包含完整指令、能力及工具元数据 |
| 推理 | `POST /v1/responses` | Responses 协议；与目录 GET 分开 |

目录请求使用 `Authorization: Bearer <AxonHub API Key>`。客户端自动附加 `client_version`；例如实测 App 内置 `0.166.0-alpha.5.1` 请求的是 `0.166.0`，模型定义应填写实际请求中的三段版本号。生效的 `model_catalog_json` 会优先于远程 URL，阻止远程获取；详见 [Codex 官方远程目录说明](https://learn.chatgpt.com/docs/enterprise/roll-out-a-gateway#serve-a-remote-catalog)。

普通名单返回 200 不代表 Codex 目录可用。目录返回 401 时先检查该实例的 Key；返回 503 且带省略数量时，检查服务端模型定义、渠道来源及导入错误。未保存手动覆盖的模型使用自动路径；验收须包含带 Key 的目录请求及真实客户端模型列表。

#### 按对话聚合 Trace（重要）
开启内置 Codex 追踪提取后，AxonHub 会将 `Session_id` header 作为 trace ID 使用：

```yaml
server:
  trace:
    codex_trace_enabled: true
```

若 Codex 还会携带其他稳定的对话标识 header（例如 `Conversation_id`），可在 `config.yml` 中将其加入 `extra_trace_headers`，用于在主 trace header 缺失时进行聚合：

```yaml
server: 
  trace:
    extra_trace_headers:
      - Conversation_id
```

**提示**：开启此功能后，AxonHub 会将同一个 Trace 的请求优先转发到同一个上游渠道，从而大幅提高提供商端的缓存命中率（例如 Anthropic 的 Prompt Caching）。

#### 验证
- 发送测试 Prompt，AxonHub 日志中应出现 `/v1/responses` 调用。
- 启用 AxonHub 的追踪功能可查看提示词、回复及延迟信息。

### 使用模型配置文件
AxonHub 的模型配置文件支持将请求模型映射到具体提供商模型：
- 在 AxonHub 控制台创建配置文件并添加映射规则（精确名称或正则）。
- 将配置文件绑定到 API Key。
- 切换活动配置文件即可更改 Codex 的行为，无需调整本地工具设置。

<table>
  <tr align="center">
    <td align="center">
      <a href="../../screenshots/axonhub-profiles.png">
        <img src="../../screenshots/axonhub-profiles.png" alt="Model Profiles" width="250"/>
      </a>
      <br/>
      Model Profiles
    </td>
  </tr>
</table>

#### 示例
- 请求 `gpt-4` → 映射到 `deepseek-reasoner` 以获取更准确的回复。
- 请求 `gpt-3.5-turbo` → 映射到 `deepseek-chat` 以降低成本。

### 常见问题
- **Codex 认证失败**：确保在启动 Codex 的同一 shell 会话中设置了 `AXONHUB_API_KEY`。
- **模型结果异常**：检查 AxonHub 控制台中当前启用的配置文件映射，必要时禁用或调整规则。

### Codex 模型目录

`GET /codex/models?client_version=0.159.2` 使用 AxonHub API Key 鉴权，返回 Codex 的 `{"models": [...]}` 结构。`client_version` 由客户端提供，格式为三段数字。原有 `/v1/models` 保持 OpenAI 的 `data` 列表，不能替代 Codex 目录。

在渠道的 **接口与路径 → Responses** 中配置目录来源：

- **自定义 Codex 目录 JSON**：提供完整模型元数据，优先于远程目录；`slug` 填渠道实际模型 ID。必须包含模型指令、工具能力、推理档位等字段，不能只填模型名称。
- **Codex 模型目录地址**：填写上游提供的完整目录 URL。AxonHub 使用该渠道的凭据访问，并附加 `client_version`；只应配置受信任的服务，不跟随重定向。
- 两项均留空时，官方 OpenAI API 渠道和官方 Codex 渠道自动使用 `https://chatgpt.com/backend-api/codex/models`，无需再配置后台目录来源。第三方 Codex 渠道使用其 `/models`，其他 Responses 渠道尝试同源默认 `/v1/models`（已有 `/v1` 或 `#` 地址规则继续生效）。第三方上游如果仅返回普通 OpenAI 模型列表，可复用其他关联渠道中相同实际型号的完整资料；全部来源均缺失时才需补充可信目录地址或定义。第三方渠道凭据仅发送至该渠道配置的目录服务。

目录按当前项目和 API Key 的模型、渠道与别名规则筛选，对外 `slug` 使用客户端可调用的名称。关联多个上游时按可见且兼容的路线计算共同能力；上下文长度取较小值，指令和客户端工具设置按下文稳定选源。缺少元数据的模型被省略，数量见 `X-AxonHub-Catalog-Omitted`；全部缺失返回 `503 codex_catalog_unavailable`，没有可见模型则返回空列表。目录失败不阻塞 Responses 调用。

远程目录最大 1 MiB，获取超时 3 秒，为 Codex 客户端的 5 秒期限预留传输与解析时间。缓存按渠道、凭据、地址和客户端版本隔离，遵循 `no-store`、`no-cache`、`max-age` 与 `Age`，最长 5 分钟；权限每次重新判断，不返回过期缓存。自定义 JSON 随渠道配置生效。

### 自动同步与可选手动覆盖

默认使用模型页面配置的型号、关联和 Key 权限自动生成目录，无需导入 JSON。每次请求重新计算可见模型、别名和渠道，按请求的 `client_version` 读取上游资料。上游资料遵循缓存控制，最长缓存 5 分钟，`no-store` 和 `no-cache` 不缓存；客户端获取新目录后看到变化。

每个具体型号独立选择完整来源，优先实际型号与公开目标型号一致的来源，再按关联优先级、渠道 ID、实际型号排序。指令和客户端工具设置采用所选来源的完整组合。关联渠道缺少目录时，优先从其他关联渠道返回的完整目录中查找**相同实际型号**，不要求来源渠道同时配置该型号的推理路线。若仍缺失，则按模型页面的明确关联继承该对外型号选定的资料，支持 `codex-auto-review` 这类上游别名；资料复用不会授予来源渠道的推理权限。已提供资料的可见渠道仍保留各自限制，按共同支持范围合并。该继承依据管理员配置的模型关联契约，供应商实际能力需要验证。

声明 `freeform` 补丁工具或 `code_mode_only` 的目录只使用支持 Responses 的可见路线，包含渠道的逐型号协议设置。实际请求携带 Responses 自定义工具或其调用历史时，也排除不能承载它的候选，防止转换时删除工具。普通无此类工具的请求沿用现有协议选择。

自动结果不写入 `settings.codexCatalog`，因此新增关联、调整 Key 和客户端版本无需重新导入。已有手动覆盖优先且保留精确版本及路线确认约束，清除覆盖即可恢复自动同步。该对外模型的全部关联渠道都无法提供完整来源时，记录缺失原因并省略该型号；全部缺失时返回 503。自动模式在上游资料缓存过期后需要可用来源。

以下为可选的手动覆盖流程。

### 多渠道手动定义

需要固定版本或人工调整定义时，在 **模型 → 编辑 → Codex 手动覆盖（可选）** 中填写客户端实际请求的三段版本号，点击 **从关联渠道导入**。Axon 为每个对外公开的具体型号选择完整目录作为客户端基线，分别保存指令和工具设置。此操作只预览，不写数据库。

核对 `models` 中各具体型号的完整客户端基线、`sourceDifferences` 中列出的来源差异，以及 `routes` 中的每一条关联。来源对指令、补丁工具、网页搜索格式、工具模式或多代理模式的声明不同，不再直接阻止预览；实际采用哪个客户端设置需要管理员确认，上下文和输入能力仍按可见路线取共同范围。没有目录的渠道也需依据真实能力确认，不能仅凭同名认定兼容；必须确认每条实际路线支持所选工具格式，例如 `freeform` 会产生 Responses 自定义工具，普通 Chat Completions 出站不能直接承载。不兼容路线应移除或拆分，`limits` 不能转换工具协议。勾选确认、点击 **应用已确认的定义**，最后保存模型。

导入不提前合并各渠道的上下文等能力：`models[对外型号].model` 保留该型号的指令及无目录渠道的继承基线，已有来源的能力分别存入 `routes[].capabilities`。生成目录时只合并当前 Key 可使用的关联；Key 别名先按映射后的目标型号选择基线，因此排除小上下文渠道后不会继续受它限制。需要手动降低能力时编辑相应关联的 `limits`。`truncation_policy` 控制客户端截取本地工具输出，按所选基线固定，不把不同的字节／Token 单位混成渠道上下文限制。

保存的数据位于模型 `settings.codexCatalog`：`clientVersion` 是请求版本，`models` 按对外型号保存 `sourceChannelId`、`sourceModelId` 及完整 `model`；`sourceDifferences` 只列出来源及不同字段名称，不复制差异指令全文。根级 `model` 和来源字段保留旧格式基线，已发布旧定义无需强制迁移；新导入优先使用逐型号基线。每条 `routes` 包含整数 `channelId`、上游实际 `modelId`、可选来源能力 `capabilities` 及仅用于缩减的 `limits`。例如：

```json
{"channelId": 12, "modelId": "实际模型ID", "limits": {"context_window": 16000}}
```

`limits` 支持上下文数值上限、推理／服务档位、输入类型、实验工具列表，以及 API、详细程度和并行工具调用的布尔能力。列表按交集、数值按较小值、布尔值按共同支持合并；未识别的限制字段会被拒绝。不同描述文字不会使同 ID 的推理／服务档位消失，未知的模型元数据字段原样保留。

配置模型定义后，目录请求直接使用本地定义，不再要求每个渠道实时提供目录；来源渠道离线也不会撤销已确认的定义。仍逐次按项目和 Key 筛选，只有当前允许的候选关联参与限制合并。添加新渠道或改变实际模型映射后，需要重新导入、核对和保存；未确认的关联只导致对应模型省略，不自动继承能力。当前定义按精确客户端版本使用；客户端版本变化后需重新导入确认。清除定义后恢复上文的自动同步。

管理员 GraphQL 提供只读 `previewModelCodexCatalog(id, clientVersion)`；通过原有 `updateModel` 保存或清除 `settings.codexCatalog`，需要渠道写权限。来源失败、版本不匹配和未确认关联记录在服务日志；省略数量在成功或失败响应的 `X-AxonHub-Catalog-Omitted` 中返回。远程刷新最多并发 4 个渠道，总预算 3 秒。先读取内联 JSON，其次是显式目录 URL、Codex 渠道及官方目录，最后探测普通兼容接口。有效的 OpenAI `data` 名单按同样的身份、版本和上游缓存规则记为缺少完整目录资料，减少重复网络请求。鉴权失败、连接错误和损坏响应继续即时报告。已知字段按当前协议检查类型与结构（包括完整模型消息），损坏的远程记录被隔离；管理员保存与合并输出同样检查，避免单条坏模型使客户端拒绝整个目录。等价 shell 工具别名及明确的缺省能力先归一。没有任何可用定义时仍明确返回 503，不生成猜测性的模型指令。

### WebSocket 与 HTTP 回退

后台的 **支持 WebSocket** 只声明渠道具备该能力，不强制客户端切换协议，也不改写渠道地址。客户端 provider 的 `supports_websockets` 和客户端版本决定是否使用 WS；上述示例允许 WS，也可以关闭该客户端选项继续使用 HTTP。

| 客户端连接 | 渠道勾选支持 WS | 上游请求 |
| --- | --- | --- |
| HTTP(S) | 任意 | HTTP(S) |
| WebSocket | 否 | HTTP(S)，仍通过原 WS 连接向客户端返回事件 |
| WebSocket | 是 | 优先 WS；可安全回退时使用同一渠道的 HTTP(S) |

建连失败且尚未发送 `response.create` 时，连接错误或 `404/405/426/501/502/503/504`（以及返回 JSON 的 `200`）可回退 HTTP；鉴权、限流和普通参数错误不触发协议回退。同一客户端连接对已降级的上游继续使用 HTTP，避免每轮重复握手。

续聊回退前恢复历史用户输入和模型输出，无法恢复 `previous_response_id` 时明确返回 `previous_response_not_found`，不发送缺少历史的请求。WS 开始发送后不再自动重发或换渠道；连接中断、超时及解码失败均保留这一限制，避免重复执行和计费。请求详情的 **渠道过程** 展示客户端协议、实际上游协议和回退原因。

Codex 在运行中追加输入或收到子代理结果时可能发送 `response.interrupt`。AxonHub 在客户端 WS 连接上确认该消息，并结束对应的当前上游调用；上游只有 HTTP(S) 时也适用，无须上游支持 WS 控制消息。默认等待正在输出的项目完整结束；显式 `discard_partial_items: true` 可丢弃尚未完成的项目。网关返回带 `incomplete_details.reason: "interrupted"` 的结束响应，保留已完成的推理项和工具调用供下一轮恢复，不重放被中断的请求。中断只影响同一连接中指定的响应；与完成事件交错到达的迟到中断仍会得到确认。历史落盘继续遵守请求／响应正文存储开关，关闭时仅保留原有的有界内存缓存。

旧渠道的 `transport=websocket` 和 WS 地址仍按支持能力读取；新保存写入 `supportsWebSockets`，显式取消勾选优先于旧字段。HTTP 和 WS 共用 Responses 地址，上游应同时支持相应的 POST 与 Upgrade 请求。

### 完整续聊历史与模式计费

恢复沿 `previous_response_id` 逐轮追溯，直到完整起点或完整缓存祖先，每次读取保持当前 Key / 项目范围。1 MiB 是内存缓存准入限制，不是可恢复历史的大小上限；超限记录直接用于本次请求。缺失、循环或不可解析的祖先不会被拼成残缺输入。

普通原生 HTTP 可保留编号交上游恢复；Codex HTTP 和 WS 转 HTTP 等要求完整输入的路径，在历史缺失时返回已有的不可恢复错误。存储读取故障保留真实错误并停止无效重试，取消请求立即结束；不会把数据库故障全都伪装成历史不存在。

恢复只拼接历史输入和输出，保持本轮模型、`service_tier` 与其他参数；不复制旧用量或旧计费档位。Fast / Ultrafast 的配置与成本标记见[费用追踪](cost-tracking.md#fast--ultrafast-与长上下文费用)。

### 相关文档
- [追踪指南](tracing.md)
- [OpenAI API 文档](../api-reference/openai-api.md)
- README 中的 [使用指南](../../../README.md#使用指南--usage-guide)
