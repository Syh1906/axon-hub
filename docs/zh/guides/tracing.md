# 追踪指南

---

### 概览
AxonHub 可以在不引入额外 SDK 的情况下，为每一次请求构建线程感知的追踪。只要客户端已经兼容 OpenAI 协议，您就可以通过传递追踪与线程请求头，或直接让 AxonHub 自动生成，实现低侵入的可观测能力。

使用追踪的主要优势包括：
- **可观测性**：清晰地查看每一条用户消息及其触发的所有 agent 请求。
- **性能优化**：AxonHub 会将同一个 Trace 的请求优先转发到同一个上游渠道，从而大幅提高提供商端的缓存命中率（例如 Anthropic 的 Prompt Caching），降低响应延迟并减少成本。
- **调试便捷**：结合线程 ID 还原完整的会话上下文，快速定位多轮对话中的问题。

### 关键概念
- **Thread ID（`AH-Thread-Id`）** – 代表用户的一个完整对话会话，将多条追踪关联起来，帮助重现完整的用户旅程。
- **Trace ID（`AH-Trace-Id`）** – 代表用户发出的一条消息以及该消息触发的所有 agent 请求。需要在需要串联多次调用时显式提供；未携带该请求头时，AxonHub 会为单次调用生成 ID 但无法自动关联其他请求。
- **Request（请求）** – 单次 API 调用的最小单元，包含完整的请求/响应数据、耗时、Token 使用量等信息。
- **额外追踪请求头** – 可配置备用请求头（如 `Sentry-Trace`），以复用已有的可观测工具链。

### Thread、Trace 与 Request 的关系

```
Thread (完整用户对话会话)
  └── Trace 1 (用户消息 1 + 所有 agent 请求)
        ├── Request 1 (agent 调用 1)
        ├── Request 2 (agent 调用 2)
        └── Request 3 (agent 调用 3)
  └── Trace 2 (用户消息 2 + 所有 agent 请求)
        ├── Request 4 (agent 调用 4)
        └── Request 5 (agent 调用 5)
```

- **Thread**：代表用户的一个完整对话会话，包含多条用户消息（每条消息对应一个 Trace）
- **Trace**：代表用户发出的一条消息以及该消息在处理过程中触发的所有 agent 请求
- **Request**：代表对 LLM 或其他服务的单次 API 调用，包含请求体、响应体、Token 使用量等详细信息

**层级关系**：
- 1 个 Thread 可以包含多个 Trace（每条用户消息一个 Trace）
- 1 个 Trace 可以包含多个 Request（该消息触发的所有 agent 调用）
- 1 个 Request 只能属于 1 个 Trace
- 1 个 Trace 只能属于 1 个 Thread（可选关联）

**实际应用场景**：
- **单条消息带 agent**：1 Thread → 1 Trace → N Request（用户发送一条消息，agent 发起多次 API 调用）
- **多轮对话**：1 Thread → 多 Trace（每条用户消息一个 Trace）→ 每个 Trace 包含 N Request
- **独立请求**：无 Thread → 1 Trace → 1 Request（无对话上下文的单次 API 调用）

### 配置
```yaml
# config.yml
server:
  trace:
    thread_header: "AH-Thread-Id"
    trace_header: "AH-Trace-Id"
    extra_trace_headers:
      - "Sentry-Trace"
    response_trace_headers:
      - "AH-Trace-Id"
      - "X-Oneapi-Request-Id"
```

- 通过 `extra_trace_headers` 复用已有的埋点请求头。
- `response_trace_headers` 会在响应开始前回写本次最终采用的 Trace ID；为空时不回写。它与入站解析配置独立，可为上游网关配置多个兼容头。
- 浏览器客户端如需读取这些头，还应将相应名称加入 `server.cors.exposed_headers`。
- 未配置 `thread_header` 或 `trace_header` 时，将采用上面的默认头名。

### 在 OpenAI 兼容客户端中使用追踪
```bash
curl https://your-axonhub-instance/v1/chat/completions \
  -H "Authorization: Bearer ${AXONHUB_API_KEY}" \
  -H "Content-Type: application/json" \
  -H "AH-Trace-Id: at-demo-123" \
  -H "AH-Thread-Id: thread-abc" \
  -d '{
    "model": "gpt-4o",
    "messages": [
      { "role": "user", "content": "Diagnose latency in my pipeline" }
    ]
  }'
```

- 当需要让多次请求落在同一追踪中时，请显式提供 `AH-Trace-Id`；若缺失该请求头，AxonHub 会分别记录这些调用，即便会为单次请求生成 ID。
- 任何 OpenAI 兼容 SDK 均可直接使用，只需根据需要添加请求头即可。

### SDK 示例
如需完整可运行的样例，可参考 `integration_test/openai/trace_multiple_requests/trace_test.go` 与 `integration_test/anthropic/trace_multiple_requests/trace_test.go`。以下片段展示了在生产代码中最核心的部分。

#### OpenAI Go SDK
```go
package traces

import (
    "context"

    "github.com/openai/openai-go/v3"
    "github.com/openai/openai-go/v3/option"
)

func sendTracedChat(ctx context.Context, apiKey string) (*openai.ChatCompletion, error) {
    client := openai.NewClient(
        option.WithAPIKey(apiKey),
        option.WithBaseURL("https://your-axonhub-instance/v1"),
    )

    params := openai.ChatCompletionNewParams{
        Model: openai.ChatModel("gpt-4o"),
        Messages: []openai.ChatCompletionMessageParamUnion{
            openai.UserMessage("请帮我诊断一下管道的延迟问题"),
        },
    }

    // 在请求级别传递追踪和线程请求头
    return client.Chat.Completions.New(ctx, params,
        option.WithHeader("AH-Trace-Id", "trace-example-123"),
        option.WithHeader("AH-Thread-Id", "thread-example-abc"),
    )
}
```

#### Anthropic Go SDK
```go
package traces

import (
    "context"

    anthropic "github.com/anthropics/anthropic-sdk-go"
    "github.com/anthropics/anthropic-sdk-go/option"
)

func sendTracedMessage(ctx context.Context, apiKey string) (*anthropic.Message, error) {
    client := anthropic.NewClient(
        option.WithAPIKey(apiKey),
        option.WithBaseURL("https://your-axonhub-instance/anthropic"),
    )

    params := anthropic.MessageNewParams{
        Model: anthropic.Model("claude-3-5-sonnet"),
        Messages: []anthropic.MessageParam{
            anthropic.NewUserMessage(
                anthropic.NewTextBlock("请帮我诊断一下管道的延迟问题"),
            ),
        },
    }

    // 在请求级别传递追踪和线程请求头
    return client.Messages.New(ctx, params,
        option.WithHeader("AH-Trace-Id", "trace-example-123"),
        option.WithHeader("AH-Thread-Id", "thread-example-abc"),
    )
}
```

### 追踪数据存储
- 可在系统策略中决定是否保存完整请求／响应体：若仅需指标，可关闭以减少敏感数据留存。
- 在管理后台配置默认数据存储，若该存储不可用会自动回退到主存储，保障访问稳定。
- 大体量内容可放在外部存储（本地磁盘、S3、GCS），追踪页面仍能快速加载。

### Claude Code 追踪支持
- 将 `server.trace.claude_code_trace_enabled` 设为 `true`，AxonHub 会自动读取 Claude Code 产生的追踪 ID。
- `/anthropic/v1/messages` (及 `/v1/messages`) 的 `metadata.user_id` 会作为追踪 ID 使用，同时不会影响请求体给后续逻辑的读取。
- 如果请求已经带有追踪请求头，系统会优先使用该值，与自动提取机制兼容。

### Codex 追踪支持
- 将 `server.trace.codex_trace_enabled` 设为 `true`，AxonHub 会将 `Session_id` header 作为追踪 ID 使用。
- 如果请求已经带有追踪请求头，系统会优先使用该值，与自动提取机制兼容。

### 在控制台中探索追踪
1. 在 AxonHub 管理后台进入 **Traces** 页面。
2. 按项目、模型或时间范围筛选目标追踪。
3. 展开追踪查看 span、提示/回复内容、耗时及渠道元数据。
4. 跳转关联的线程，结合追踪细节还原完整会话。

<table>
  <tr align="center">
    <td align="center">
      <a href="../../screenshots/axonhub-trace.png">
        <img src="../../screenshots/axonhub-trace.png" alt="Trace Details" width="600"/>
      </a>
      <br/>
      Trace 详情页面展示了请求的时间线、Token 使用量及缓存命中情况
    </td>
  </tr>
</table>

### 最佳实践

#### Trace 设计建议

**单个 Trace 应包含合理数量的 Request**

虽然 AxonHub 理论上支持单个 Trace 包含无限数量的 Request，但在实际生产环境中，我们建议：

- **推荐范围**：单个 Trace 包含 10-50 个 Request
- **可接受范围**：最多 100 个 Request
- **避免场景**：单个 Trace 超过 1000 个 Request

**原因**：
- **内存消耗**：每个 Request 的请求体和响应体大小通常在 1-5MB，100 个 Request 可能占用 500MB 内存
- **性能影响**：过多 Request 会导致 Trace 页面加载缓慢，影响用户体验
- **可读性**：包含大量 Request 的 Trace 难以阅读和调试

**优化建议**：
1. **拆分工作流**：将复杂的 agent 工作流拆分为多个 Trace，每个 Trace 代表一个逻辑单元
2. **使用 Thread 关联**：通过 Thread ID 关联多个 Trace，保持会话完整性
3. **控制 agent 迭代次数**：在 agent 循环中设置合理的最大迭代次数，避免无限循环
4. **定期清理**：设置数据保留策略，定期清理过期的 Trace 数据

**示例场景**：

✅ **良好实践**：
```
Thread (用户会话)
  ├── Trace 1: 用户问题分析 (5 个 Request)
  ├── Trace 2: 方案设计 (10 个 Request)
  ├── Trace 3: 代码生成 (20 个 Request)
  └── Trace 4: 结果验证 (8 个 Request)
```

❌ **避免做法**：
```
Thread (用户会话)
  └── Trace: 完整工作流 (500+ 个 Request)
```

#### Request Body 大小控制

- 对于包含大量上下文的请求，考虑使用 Prompt Caching（如 Anthropic 的缓存功能）
- 避免在 Request Body 中存储不必要的大文件（如完整的日志文件）
- 对于图片、视频等大文件，使用 URL 引用而非 base64 编码

### 故障排查
- **未生成追踪** – 确认请求已通过认证且项目 ID 正确解析（API Key 必须隶属于某个项目）。
- **缺少线程关联** – 在请求中提供 `AH-Thread-Id`，或先通过 API 创建线程。
- **追踪 ID 异常** – 检查上游代理是否覆盖了相关请求头。

#### 请求主行与执行详情的状态码

请求主行优先显示实际记录的客户端交付状态码；没有该记录时，才显示实际采用执行的响应码。并发标题原执行返回 200、后发备用返回 500 时，后者仍在渠道过程和错误详情中保留，不会让成功主行显示备用的 500。仅有请求读取权限也可以读取这项结果摘要；渠道名称、凭据序号和选路详情仍受渠道读取权限限制。

#### 后台页面提示加载失败

后台首次进入时需要读取系统状态、账号和项目资料。读取失败时页面显示具体对象与原因；网络失败不等同于登录过期。点击重试会重新读取，已经成功加载的资料不会因一次后台刷新失败被丢弃；首次账号资料未取得时仍保持权限检查。

如果错误响应已经到达，页面可显示 `AH-Request-Id` 对应的请求编号。管理端 `GraphQL request failed` 日志中的 `admin_request_id` 用于关联该请求，`phase` 区分 `request_body`（接收）、`decode`（解码）、`validation`（校验）和 `execution`（执行）。`body_bytes_read`、`content_length`、`body_read_duration`、`processing_duration` 分别记录应用接收字节数、声明长度和两个阶段的耗时。诊断不保存正文或凭据；读取错误保留安全的分类，不输出原始解码输入。JSON 解码失败、multipart 读取失败及原有大小限制均有分类；普通 multipart 表单语法错误继续沿用原解析器响应，不推断为读取超时。变量校验未通过时记录 `validation`，实际进入操作处理后才记录 `execution`。

管理页的 GraphQL 请求在发送前生成 `at-` 关联编号，通过 `AH-Trace-Id` 请求头传递。浏览器自行超时时仍保留该编号、操作名称、耗时和是否收到响应头；已经收到的 `AH-Request-Id` 不因响应下载超时丢失。页面优先展示 `ar-` 请求编号，否则展示 `at-` 关联编号，可分别查询日志中的 `request_id` 和 `trace_id`。未收到响应不能证明请求未到服务器；正文为 0 字节时，应继续对照代理接收与转发记录，不能直接归因于 SQL。

`REQUEST_BODY_TIMEOUT` 表示服务器接收正文超时，`REQUEST_TIMEOUT` 表示服务器处理查询超时，`CLIENT_READ_TIMEOUT` 表示浏览器等待结果超时；最后一种只有响应头已到达时，才能进一步说明结果读取阶段超时。同页多个读取连接错误合并通知，写入、权限和业务错误保持独立。渠道、请求记录、模型和用量页提供局部重试，已有数据保留并明确标记为上次数据。

渠道列表短期复用 10 秒、请求滚动列表短期复用 3 秒；仅当筛选、身份、权限和对应页面范围相同，缓存仍含真正的首页、未过期且未被写操作失效时才复用。返回较旧窗口或点击刷新仍从首页重新读取；请求页自动刷新只刷新首批，不重放全部历史页。分页参数读取当前渲染路由，避免离开页面时误用目标页参数触发多余查询。请求页的渠道／密钥／分组选项和统计页分组选项按需加载，已有选择仍读取显示名称。自动版本检查记录失败尝试，在 10 分钟内不因切页反复查询；手动检查不受此限制。

渠道上游快照仅供页面自动刷新复用，手动刷新和保存后的刷新先读取最新状态；只有自动刷新全部跳过且没有更新结果时，才省去后续重读。渠道凭据／类型／地址变更以及密钥启用、禁用、删除，会先取消相关批次未完成的旧读取，再使这些快照失效，避免按旧密钥集合刷新。首次打开筛选器仍在读取候选项时显示加载状态，不把等待显示成空结果。

后台 `/admin/graphql` 将接收和查询执行分开计时：接收正文使用 `server.read_timeout`，未设置时沿用 `server.request_timeout`；收到并解析完整请求后，才开始 `server.request_timeout` 的查询处理预算。响应写入期限在接收阶段包含接收预算，进入执行时重新按原写入预算计时，避免查询虽得到新期限、响应却被旧期限截断。读取阻塞通过连接读取截止时间终止，不只取消查询上下文。沿用 gqlgen 的正文读取和上传临时文件管理，不复制完整正文；客户端断开仍会取消执行，原事务、服务账号只读限制和渠道测试独立超时保持。该行为由应用代码实现，不要求修改 Nginx。

如后续需要在 Nginx 进一步隔离接收等待，应仅评估 `/admin/graphql` 的 `proxy_request_buffering on`，并核对同入口文件导入的大小限制和临时磁盘空间；请求缓冲与响应缓冲不同，不应批量更改推理／流式接口。代理配置须独立安排部署。当前恢复可用和健康检查成功，都不能证明历史网络或 VPN 故障已经根治。

### 相关文档
- [请求处理流程指南](../getting-started/request-processing.md)
- [负载均衡指南](load-balance.md)
