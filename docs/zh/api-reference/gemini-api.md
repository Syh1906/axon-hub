# Gemini API 参考

## 概述

AxonHub 原生支持 Gemini API，可访问 Gemini 强大的多模态功能。您可以使用 Gemini SDK 访问 Gemini 模型，也可以访问 OpenAI、Anthropic 和其他支持的模型。

GenerateContent 请求也接受 `system_instruction`、`inline_data` 和 `mime_type`，分别作为 `systemInstruction`、`inlineData` 和 `mimeType` 的兼容别名。两种写法都有值时，优先使用 camelCase 字段的值。Gemini 输出仍使用标准的 camelCase 字段名。

同协议转换保留思考内容的分段，以及签名所在的文字、图片、文件引用或工具调用块。
带签名的内容不会被合并到其他块；完整响应、流式事件和流式聚合遵循相同约束。
文件引用保留地址及媒体类型；独立的签名分片和结束分片不会因没有正文而被跳过。
无签名的文字／工具混合块也保持原顺序；普通纯文本仍沿用原有轻量处理。
内部只保存块位置和签名引用，不额外复制图片正文，提示处理修改后的文本仍然生效。

跨协议只转换有对应实现的能力。Google `codeExecution`、`urlContext` 保持在 Gemini
协议上；普通文本和函数工具仍可跨协议。不能因目标供应商也提供文本生成，就假定它拥有
相同的内置工具。现有入口仍是 `generateContent`／`streamGenerateContent`，未实现
Interactions API，也未改变默认透传设置。

## 核心优势

- **API 互操作性**：使用 Gemini API 调用 OpenAI、Anthropic 和其他支持的模型
- **零代码变更**：继续使用现有的 Gemini 客户端 SDK，无需修改
- **自动转换**：AxonHub 在需要时自动在 API 格式之间进行转换
- **多模态支持**：通过 Gemini API 格式访问文本和图像功能

## 支持的端点

**端点：**
- `POST /gemini/v1beta/models/{model}:generateContent` - 文本和多模态内容生成
- `POST /v1beta/models/{model}:generateContent` - 文本和多模态内容生成 (可选)
- `GET /gemini/v1beta/models` - 列出可用模型
- `GET /v1beta/models` - 列出可用模型 (可选)
- `GET /gemini/v1beta/models/{model}` 或 `GET /v1beta/models/{model}` - 读取单个模型的目录信息。将列表返回的 `name`（如 `models/my-alias`）接到相同的版本前缀后即可。详情与该密钥的列表项一致，模型不可见或不存在时均返回 404。查询详情不产生推理费用。

**示例请求：**
```go
import (
    "context"
    "google.golang.org/genai"
)

// 使用 AxonHub 配置创建 Gemini 客户端
ctx := context.Background()
client, err := genai.NewClient(ctx, &genai.ClientConfig{
    APIKey:  "your-axonhub-api-key",
    Backend: genai.Backend(genai.APIBackendUnspecified), // 使用默认后端
    HTTPOptions: genai.HTTPOptions{
			BaseURL: "http://localhost:8090/gemini",
	},
})
if err != nil {
    // 适当处理错误
    panic(err)
}

// 使用 Gemini API 格式调用 OpenAI 模型
modelName := "gpt-4o"  // 通过 Gemini API 格式访问 OpenAI 模型
content := &genai.Content{
    Parts: []*genai.Part{
        {Text: genai.Ptr("Hello, GPT!")},
    },
}

// 可选：配置生成参数
config := &genai.GenerateContentConfig{
    Temperature: genai.Ptr(float32(0.7)),
    MaxOutputTokens: genai.Ptr(int32(1024)),
}

response, err := client.Models.GenerateContent(ctx, modelName, []*genai.Content{content}, config)
if err != nil {
    // 适当处理错误
    panic(err)
}

// 从响应中提取文本
if len(response.Candidates) > 0 &&
   len(response.Candidates[0].Content.Parts) > 0 {
    responseText := response.Candidates[0].Content.Parts[0].Text
    fmt.Println(*responseText)
}
```

**示例：多轮对话**
```go
// 创建带有对话历史的聊天会话
modelName := "claude-3-5-sonnet"
config := &genai.GenerateContentConfig{
    Temperature: genai.Ptr(float32(0.5)),
}

chat, err := client.Chats.Create(ctx, modelName, config, nil)
if err != nil {
    panic(err)
}

// 第一条消息
response1, err := chat.SendMessage(ctx, genai.Part{Text: genai.Ptr("My name is Alice")})
if err != nil {
    panic(err)
}

// 后续消息（模型记住上下文）
response2, err := chat.SendMessage(ctx, genai.Part{Text: genai.Ptr("What is my name?")})
if err != nil {
    panic(err)
}

// 提取响应
if len(response2.Candidates) > 0 {
    text := response2.Candidates[0].Content.Parts[0].Text
    fmt.Println(*text)  // 应该包含 "Alice"
}
```

## CodeGateway 显式缓存

此入口供由 sub2api 结算缓存存储费的集成使用。将专用 AxonHub Key 的数据库编号写入 `server.gemini_explicit_cache.api_key_ids`，将已确认支持缓存的 `gemini` 渠道编号写入 `channel_ids`，重新加载服务配置。两项默认均为空。专用 Key 需要 `write_requests` 和目标模型的使用权限。支持 AI Studio API Key 及采用相同原生协议的地址，使用标准 v1beta 路径。Vertex、OAuth 和仅支持聊天的转换渠道不在本期范围内。

在 sub2api 为指向 AxonHub 的 Gemini API Key 账号启用 `explicit_cache_upstream`，配置模型存储价格和允许的有效期。客户存储费用由 sub2api 按 token 数及保存时间结算。AxonHub 的普通生成仍沿用现有用量和预算处理。

| 操作 | 路径与行为 |
|---|---|
| 创建 | `POST /v1beta/cachedContents`，发送 `model`、`contents` 和 `ttl` 或 `expireTime`。返回公开的 `name`、供应商报告的到期时间和 `usageMetadata.totalTokenCount`。 |
| 列表与读取 | `GET /v1beta/cachedContents` 按当前项目和 Key 分页返回有效资源，接受 `pageSize` 和上页的 `nextPageToken`（作为 `pageToken` 传入）。`GET /v1beta/{name}` 读取所属供应商的元数据。 |
| 续期与删除 | `PATCH /v1beta/{name}?updateMask=ttl` 可发送 `{"ttl":"120s"}`，也可更新 `expireTime`。`DELETE /v1beta/{name}` 删除该资源。 |
| 生成引用 | 在同一模型的 `generateContent` 或 `streamGenerateContent` 正文中将 `cachedContent` 设为创建时返回的完整 `name`。读缓存的 token 数见 `usageMetadata.cachedContentTokenCount`。 |

`/gemini/v1beta` 同样支持上述操作。其他调用方、已删除或已到期的资源返回 404。已授权 Key 的候选渠道未声明缓存能力时返回 501，缺少可用路由或原账号不可用时返回 503。缓存正文保存在供应商侧，AxonHub 只保存资源归属和元数据。

## API 转换能力

AxonHub 自动在 API 格式之间进行转换，实现以下强大场景：

### 使用 Gemini SDK 调用 OpenAI 模型
```go
// Gemini SDK 调用 OpenAI 模型
content := &genai.Content{
    Parts: []*genai.Part{
        {Text: genai.Ptr("什么是人工智能？")},
    },
}

response, err := client.Models.GenerateContent(
    ctx,
    "gpt-4o",  // OpenAI 模型
    []*genai.Content{content},
    nil,
)

// 访问响应
if len(response.Candidates) > 0 &&
   len(response.Candidates[0].Content.Parts) > 0 {
    text := response.Candidates[0].Content.Parts[0].Text
    fmt.Println(*text)
}
// AxonHub 自动转换 Gemini 格式 → OpenAI 格式
```

### 使用 Gemini SDK 调用 Anthropic 模型
```go
// Gemini SDK 调用 Anthropic 模型
content := &genai.Content{
    Parts: []*genai.Part{
        {Text: genai.Ptr("解释神经网络")},
    },
}

response, err := client.Models.GenerateContent(
    ctx,
    "claude-3-5-sonnet",  // Anthropic 模型
    []*genai.Content{content},
    nil,
)

// 访问响应
if len(response.Candidates) > 0 &&
   len(response.Candidates[0].Content.Parts) > 0 {
    text := response.Candidates[0].Content.Parts[0].Text
    fmt.Println(*text)
}
// AxonHub 自动转换 Gemini 格式 → Anthropic 格式
```

## 认证

Gemini API 格式使用以下认证方式：

- **头部**：`X-Goog-API-Key: <your-api-key>`

API 密钥通过 AxonHub 的 API 密钥管理系统进行管理，无论使用哪种 API 格式，都提供相同的权限。

## 流式支持

Gemini API 格式支持流式响应以进行实时内容生成。

## 错误处理

Gemini 格式错误响应遵循标准 Gemini API 错误格式。

## 工具支持

AxonHub 通过 Gemini API 格式支持**函数工具**（自定义函数调用）。但是，**不支持**各提供商特有的工具：

| 工具类型 | 支持状态 | 说明 |
| -------- | -------- | ---- |
| **函数工具（Function Tools）** | ✅ 支持 | 自定义函数定义可跨所有提供商使用 |
| **网页搜索（Web Search）** | ❌ 不支持 | 提供商特有功能 |
| **代码解释器（Code Interpreter）** | ❌ 不支持 | 提供商特有功能 |
| **文件搜索（File Search）** | ❌ 不支持 | 提供商特有功能 |
| **计算机使用（Computer Use）** | ❌ 不支持 | Anthropic 特有功能 |

> **注意**：仅支持可跨提供商转换的通用函数工具。提供商特有工具需要直接访问提供商的基础设施，无法通过 AxonHub 代理。

## 最佳实践

1. **使用追踪头部**：包含 `AH-Trace-Id` 和 `AH-Thread-Id` 头部以获得更好的可观测性
2. **模型选择**：在请求中明确指定目标模型
3. **错误处理**：为 API 响应实现适当的错误处理
4. **流式处理**：对于长响应使用流式处理以获得更好的用户体验
5. **多模态内容**：在处理图像时利用 Gemini API 的多模态功能

## 迁移指南

### 从 Gemini 迁移到 AxonHub
```go
// 之前：直接 Gemini
ctx := context.Background()
client, err := genai.NewClient(ctx, &genai.ClientConfig{
    APIKey: "gemini-api-key",
})

// 之后：使用 Gemini API 的 AxonHub
ctx := context.Background()
client, err := genai.NewClient(ctx, &genai.ClientConfig{
    APIKey: "your-axonhub-api-key",
    HTTPOptions: genai.HTTPOptions{
        BaseURL: "http://localhost:8090/gemini",
    },
})
// 您的现有代码继续工作！
```
