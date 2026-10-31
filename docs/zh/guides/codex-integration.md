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
- 两项均留空时，官方 OpenAI API 渠道和官方 Codex 渠道自动使用 `https://chatgpt.com/backend-api/codex/models`，无需再配置后台目录来源。第三方 Codex 渠道使用其 `/models`，其他 Responses 渠道尝试同源默认 `/v1/models`（已有 `/v1` 或 `#` 地址规则继续生效）。第三方上游如果仅返回普通 OpenAI 模型列表，仍需提供完整目录地址或 JSON；不会凭模型名虚构能力，也不会将第三方渠道凭据发送到 OpenAI。

目录按当前项目和 API Key 的模型、渠道与别名规则筛选，对外 `slug` 使用客户端可调用的名称。关联多个上游时只公布共同能力；上下文长度取较小值，模型指令或工具格式冲突的模型不公布。缺少元数据的模型被省略，数量见 `X-AxonHub-Catalog-Omitted`；全部缺失返回 `503 codex_catalog_unavailable`，没有可见模型则返回空列表。目录失败不阻塞 Responses 调用。

远程目录最大 1 MiB，获取超时 10 秒。缓存按渠道、凭据、地址和客户端版本隔离，遵循 `no-store`、`no-cache`、`max-age` 与 `Age`，最长 5 分钟；权限每次重新判断，不返回过期缓存。自定义 JSON 随渠道配置生效。

### WebSocket 与 HTTP 回退

后台的 **支持 WebSocket** 只声明渠道具备该能力，不强制客户端切换协议，也不改写渠道地址。客户端 provider 的 `supports_websockets` 和客户端版本决定是否使用 WS；上述示例允许 WS，也可以关闭该客户端选项继续使用 HTTP。

| 客户端连接 | 渠道勾选支持 WS | 上游请求 |
| --- | --- | --- |
| HTTP(S) | 任意 | HTTP(S) |
| WebSocket | 否 | HTTP(S)，仍通过原 WS 连接向客户端返回事件 |
| WebSocket | 是 | 优先 WS；可安全回退时使用同一渠道的 HTTP(S) |

建连失败且尚未发送 `response.create` 时，连接错误或 `404/405/426/501/502/503/504`（以及返回 JSON 的 `200`）可回退 HTTP；鉴权、限流和普通参数错误不触发协议回退。同一客户端连接对已降级的上游继续使用 HTTP，避免每轮重复握手。

续聊回退前恢复历史用户输入和模型输出，无法恢复 `previous_response_id` 时明确返回 `previous_response_not_found`，不发送缺少历史的请求。WS 开始发送后不再自动重发或换渠道；连接中断、超时及解码失败均保留这一限制，避免重复执行和计费。请求详情的 **渠道过程** 展示客户端协议、实际上游协议和回退原因。

旧渠道的 `transport=websocket` 和 WS 地址仍按支持能力读取；新保存写入 `supportsWebSockets`，显式取消勾选优先于旧字段。HTTP 和 WS 共用 Responses 地址，上游应同时支持相应的 POST 与 Upgrade 请求。

### 完整续聊历史与模式计费

恢复沿 `previous_response_id` 逐轮追溯，直到完整起点或完整缓存祖先，每次读取保持当前 Key / 项目范围。1 MiB 是内存缓存准入限制，不是可恢复历史的大小上限；超限记录直接用于本次请求。缺失、循环或不可解析的祖先不会被拼成残缺输入。

普通原生 HTTP 可保留编号交上游恢复；Codex HTTP 和 WS 转 HTTP 等要求完整输入的路径，在历史缺失时返回已有的不可恢复错误。存储读取故障保留真实错误并停止无效重试，取消请求立即结束；不会把数据库故障全都伪装成历史不存在。

恢复只拼接历史输入和输出，保持本轮模型、`service_tier` 与其他参数；不复制旧用量或旧计费档位。Fast / Ultrafast 的配置与成本标记见[费用追踪](cost-tracking.md#fast--ultrafast-与长上下文费用)。

### 相关文档
- [追踪指南](tracing.md)
- [OpenAI API 文档](../api-reference/openai-api.md)
- README 中的 [使用指南](../../../README.md#使用指南--usage-guide)
