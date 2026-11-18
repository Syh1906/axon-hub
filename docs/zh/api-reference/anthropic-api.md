# Anthropic API 参考

## 概述

AxonHub 支持原生 Anthropic Messages API，适用于偏好 Anthropic 特定功能和响应格式的应用程序。您可以使用 Anthropic SDK 访问 Claude 模型，也可以访问 OpenAI、Gemini 和其他支持的模型。

协议兼容以目标模型及接口支持的能力为限。同协议重组会保留
`thinking.block_binding`、`container` 和 `output_config.format`；携带思考绑定选项时，
会添加所需的 `thinking-binding-controls-2026-08-01` beta 标头。
JSON Schema 输出约束通过统一结构映射到 OpenAI Chat、Responses 和 Gemini；Anthropic
没有可直接等价表达的无 schema `json_object` 请求会明确拒绝，不再静默忽略。
`block_binding` 和服务器容器不能迁移到其他协议，因此仅限制携带这些选项的请求；
`container: null` 不表示需要保留容器状态。

未映射到统一函数的原生工具定义保留原始选项与顺序，仅在 Anthropic 协议下转发；
没有跨协议映射的服务端工具历史同样不能改成客户端函数调用。Anthropic 入站会排除当前
不支持原生工具的渠道（含当前 Vertex 转换器），没有合适候选时不会删除工具再降级。
这只保证数据完整转发，具体工具仍需目标供应商支持。Claude Code 出口会合并必需的
beta 标头，并保持原生工具的选择名称；普通函数继续沿用既有名称前缀。

普通及流式响应保留 `pause_turn`、`stop_sequence`、`model_context_window_exceeded`
原始停止原因、匹配的停止文本和容器信息；聚合流式分片时同样保留。
`pause_turn` 表示服务端工具需要继续，客户端应将助手内容和容器编号带回；网关不会自动
增加模型调用。具体模型／供应商是否支持所请求的选项仍由其 API 决定。

## Token 计数

`POST /v1/messages/count_tokens` 与 `POST /anthropic/v1/messages/count_tokens`
接收原生 `model`、`messages`、`system`、`tools` 等计数输入，返回
`{"input_tokens":27}`。不要求生成接口的 `max_tokens`，不接受流式请求。

使用调用方 API Key，沿用项目、模型、渠道协议权限及并发限制。仅选择支持原生
Anthropic Messages 协议的渠道，并请求其 `/messages/count_tokens`；不转换为模型生成，
不返回固定数字或本地粗估值。不支持原生计数的供应商会返回错误，Bedrock Messages
出口不参与计数路由；同一模型的其他可用原生出口仍可接手。Vertex 使用其专用计数地址。

计数保留原生字段并传递 Anthropic beta 标头。发往供应商的内容使用映射后的模型名，并应用已启用的提示词保护规则；
开启原样透传也不会恢复脱敏前的文本。计数不注入项目生成提示词；结果表示实际发出内容的供应商估算，不保证等于随后经过提示词注入、工具
处理后的生成请求用量。计数保留请求诊断记录，但不创建消费日志、不结算生成费用。
上游缺失、负数或非法 `input_tokens` 视为错误。

## 核心优势

- **API 互操作性**：使用 Anthropic Messages API 调用 OpenAI、Gemini 和其他支持的模型
- **零代码变更**：继续使用现有的 Anthropic 客户端 SDK，无需修改
- **自动转换**：AxonHub 在需要时自动在 API 格式之间进行转换
- **提供商灵活性**：使用 Anthropic API 格式访问任何支持的 AI 提供商

## 支持的端点

**端点：**
- `POST /anthropic/v1/messages` - 文本生成
- `POST /v1/messages` - 文本生成 (可选)
- `GET /anthropic/v1/models` - 列出可用模型
- `GET /anthropic/v1/models/{id}` - 查询当前密钥可见的单个型号或别名

模型列表按发布日期倒序，同日按对外 ID 排序。每页默认 20 个，`limit` 支持 1–1000。向后翻页传 `after_id=上一页的last_id`，向前传 `before_id=当前页的first_id`；`has_more` 表示请求方向是否还有数据。先按项目和密钥过滤，再分页。

`created_at` 是模型卡精确到日的发布日期或完整时间戳。只知道月份或缺少日期时使用 Unix 纪元。旧字段 `created` 保留平台注册时间。

聊天模型有当前密钥可用的 Messages 路线时，目录分别返回已知的最大输入 `max_input_tokens` 和最大输出 `max_tokens`，未知值为 `null`。目录逐个检查实际型号的协议覆盖和关联中的请求协议条件。只允许 Responses 请求的关联不会为 Messages 提供上限或能力。

模型卡提供完整的 Anthropic 原生声明，且所有可选路线都使用原生 Messages 时，目录才返回 `capabilities`。包含转换路线或缺少完整声明时，该字段为 `null`。如果某条原生路线无法使用服务端工具，目录将 `server_tools` 和 `code_execution` 标为不支持。常规 Messages 转发会重新组装请求，无法完整保留 PDF 文档、文档引文设置和上下文编辑指令，因此目录将 `pdf_input`、`citations` 和 `context_management` 标为不支持。搜索工具返回的来源引用仍按工具结果传递。

**示例请求：**
```go
import (
    "github.com/anthropics/anthropic-sdk-go"
    "github.com/anthropics/anthropic-sdk-go/option"
)

// 使用 AxonHub 配置创建 Anthropic 客户端
client := anthropic.NewClient(
    option.WithAPIKey("your-axonhub-api-key"),
    option.WithBaseURL("http://localhost:8090/anthropic"),
    
)

// 使用 Anthropic API 格式调用 OpenAI 模型
messages := []anthropic.MessageParam{
    anthropic.NewUserMessage(anthropic.NewTextBlock("Hello, GPT!")),
}

response, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model:     anthropic.Model("gpt-4o"),
    Messages:  messages,
    MaxTokens: 1024,
})
if err != nil {
    // 适当处理错误
    panic(err)
}

// 从响应中提取文本内容
responseText := ""
for _, block := range response.Content {
    if textBlock := block.AsText(); textBlock != nil {
        responseText += textBlock.Text
    }
}
fmt.Println(responseText)
```

## API 转换能力

AxonHub 自动在 API 格式之间进行转换，实现以下强大场景：

### 使用 Anthropic SDK 调用 OpenAI 模型
```go
// Anthropic SDK 调用 OpenAI 模型
messages := []anthropic.MessageParam{
    anthropic.NewUserMessage(anthropic.NewTextBlock("你好，世界！")),
}

response, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model:     anthropic.Model("gpt-4o"),  // OpenAI 模型
    Messages:  messages,
    MaxTokens: 1024,
})

// 访问响应
for _, block := range response.Content {
    if textBlock := block.AsText(); textBlock != nil {
        fmt.Println(textBlock.Text)
    }
}
// AxonHub 自动转换 Anthropic 格式 → OpenAI 格式
```

### 使用 Anthropic SDK 调用 Gemini 模型
```go
// Anthropic SDK 调用 Gemini 模型
messages := []anthropic.MessageParam{
    anthropic.NewUserMessage(anthropic.NewTextBlock("解释量子计算")),
}

response, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model:     anthropic.Model("gemini-2.5"),  // Gemini 模型
    Messages:  messages,
    MaxTokens: 1024,
})

// 访问响应
for _, block := range response.Content {
    if textBlock := block.AsText(); textBlock != nil {
        fmt.Println(textBlock.Text)
    }
}
// AxonHub 自动转换 Anthropic 格式 → Gemini 格式
```

## 认证

Anthropic API 格式使用以下认证方式：

- **头部**：`X-API-Key: <your-api-key>`

API 密钥通过 AxonHub 的 API 密钥管理系统进行管理，无论使用哪种 API 格式，都提供相同的权限。

## 流式支持

Anthropic API 格式支持流式响应：

```go
// Anthropic SDK 流式传输
messages := []anthropic.MessageParam{
    anthropic.NewUserMessage(anthropic.NewTextBlock("从一数到五")),
}

stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
    Model:     anthropic.Model("gpt-4o"),
    Messages:  messages,
    MaxTokens: 1024,
})

// 收集流式内容
var content string
for stream.Next() {
    event := stream.Current()
    switch event := event.(type) {
    case anthropic.ContentBlockDeltaEvent:
        if event.Type == "content_block_delta" {
            content += event.Delta.Text
            fmt.Print(event.Delta.Text) // 边传输边打印
        }
    }
}

if err := stream.Err(); err != nil {
    panic(err)
}

fmt.Println("\n完整响应:", content)
```

## 错误处理

Anthropic 格式错误响应：

```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "message": "Invalid API key"
  }
}
```

## 工具支持

AxonHub 通过 Anthropic API 格式支持**函数工具**（自定义函数调用）。但是，**不支持**各提供商特有的工具：

| 工具类型 | 支持状态 | 说明 |
| -------- | -------- | ---- |
| **函数工具（Function Tools）** | ✅ 支持 | 自定义函数定义可跨所有提供商使用 |
| **网页搜索（Web Search）** | ❌ 不支持 | 提供商特有功能（OpenAI、Anthropic 等） |
| **代码解释器（Code Interpreter）** | ❌ 不支持 | 提供商特有功能（OpenAI、Anthropic 等） |
| **文件搜索（File Search）** | ❌ 不支持 | 提供商特有功能 |
| **计算机使用（Computer Use）** | ❌ 不支持 | Anthropic 特有功能 |

> **注意**：仅支持可跨提供商转换的通用函数工具。网页搜索、代码解释器、计算机使用等提供商特有工具需要直接访问提供商的基础设施，无法通过 AxonHub 代理。

## 最佳实践

1. **使用追踪头部**：包含 `AH-Trace-Id` 和 `AH-Thread-Id` 头部以获得更好的可观测性
2. **模型选择**：在请求中明确指定目标模型
3. **错误处理**：为 API 响应实现适当的错误处理
4. **流式处理**：对于长响应使用流式处理以获得更好的用户体验
5. **使用函数工具**：进行工具调用时，请使用通用函数工具而非提供商特有工具

## 迁移指南

### 从 Anthropic 迁移到 AxonHub
```go
// 之前：直接 Anthropic
client := anthropic.NewClient(
    option.WithAPIKey("anthropic-key"),
)

// 之后：使用 Anthropic API 的 AxonHub
client := anthropic.NewClient(
    option.WithAPIKey("axonhub-api-key"),
    option.WithBaseURL("http://localhost:8090/anthropic"),
)
// 您的现有代码继续工作！
```
