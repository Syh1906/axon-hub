# 成本追踪

AxonHub 为每一次请求提供实时的成本计算和用量追踪。

## 概述

AxonHub 的成本追踪功能帮助您监控和控制跨所有供应商和模型的 AI 支出。每个请求都会被自动分析，并根据可配置的定价计算成本。

### 用量统计与消耗排行榜

项目中的「用量统计」默认按密钥分组汇总，也可切换用户或 API Key；分组可展开模型的请求、Token、缓存命中率和成本。管理区的「消耗排行榜」支持用户、密钥、渠道的消耗榜、缓存榜和模型展开。两页提供今天、昨天、本周、本月、全部及指定日期，按系统时区计算，本周从周一开始。

项目用量统计需要当前项目的 `read_requests` 读取权限；全站排行榜需要系统级「查看消耗排行榜」权限（`read_leaderboard`），没有单独管理权限。整体缓存命中率为缓存读取词元总量除以全部输入词元；缓存榜仍只统计触发缓存读取或写入的请求。缓存系数约滞后 15–20 分钟；历史缺失采样显示「—」。详细字段、公式、删除对象及升级行为见[统计说明](../development/consumption-statistics.md)。

### 仪表盘成本卡

成本统计位于词元统计右侧、今日请求左侧，支持全部、本月、本周和今日；今日按系统时区的自然日统计。金额累加已保存的用量费用，不重算历史；卡片仅显示金额和周期切换，全部缺价时显示「—」。读取权限沿用仪表盘的 `read_dashboard`。

## 功能特性

### Token 类型追踪

AxonHub 追踪以下 Token 类型：

| Token 类型 | 说明 | 代码标识 |
|------------|------|----------|
| **输入 (Prompt)** | 用量记录为总输入（含缓存）；计价项使用扣除缓存后的非缓存输入 | `prompt_tokens` |
| **输出 (Completion)** | 模型生成的 Token | `completion_tokens` |
| **缓存读取** | 从缓存读取的 Token | `prompt_cached_tokens` |
| **缓存写入** | 写入缓存的 Token | `prompt_write_cached_tokens` |
| **推理** | 推理 Token（支持该功能的模型）| `completion_reasoning_tokens` |
| **缓存写入 (5分钟)** | Anthropic 5分钟 TTL 缓存写入 | `prompt_write_cached_tokens_5m` |
| **缓存写入 (1小时)** | Anthropic 1小时 TTL 缓存写入 | `prompt_write_cached_tokens_1h` |

### 定价模式

AxonHub 支持以下定价模式：

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| **固定费用 (flat_fee)** | 每次请求收取固定费用 | 固定成本场景 |
| **图片规格固定费用 (image_fixed)** | 每张最终图片匹配规格或宽高条件后计费，再相加 | 生图渠道按分辨率定价 |
| **按单位计费 (usage_per_unit)** | 按每百万 Token 计费 | 大多数模型 |
| **分层计费 (usage_tiered)** | 根据用量分层定价 | 用量折扣场景 |

### 图片规格固定费用

在渠道的「模型价格」或模型编辑的「模型定价」中，直接把现有价格行的计费模式切换为「图片规格固定费用」。默认开启自动识别，填写 1K、2K、4K 的每张价格；金额可为零，未填写不能保存。分时价格覆盖也使用相同编辑器。渠道规则只影响对应渠道和模型；引用标准价格会复制完整规则，之后可独立修改。

自动分档先使用响应中的明确规格标签，其次使用实际发往上游的请求标签，兼容 `2K`、`2k` 等大小写。请求标签会显示为「上游未确认档位」，不能证明上游最终账单。没有标签时，优先读取最终 PNG、JPEG、WebP 图片尺寸，其次读取响应中的宽高或 `宽x高`：Gemini 的已知尺寸表先匹配，其余按总像素依次归入不超过 `1024²`、`2048²`、`4096²` 的档位。这是本站自动分档口径，**不是任意一条边超过 1024 就算 2K**。例如 `2048×512` 按面积属于 1K。超过最大面积或缺少可用信息时不猜测价格。

关闭自动识别后，可用「宽或高超过」或「指定宽高」快速填入条件，再调整数字与比较方式。支持宽度、高度、`>`、`≥`、`=`、`≤`、`<`，以及「任一满足」「全部满足」。拖动规则手柄调整顺序，每张图片从上到下只采用第一条匹配规则；自动档位和手动规则分别保留，切换、保存后重开仍保留各自的价格、条件和顺序；另一模式尚未填写完整的草稿不参与当前计费。自定义条件使用结果宽高，不把请求中的期望尺寸当作实际尺寸。

例如手动规则从上到下为：宽或高大于 2048 收 0.08，宽或高大于 1024 收 0.04，宽高都等于 1024 收 0.02。同一请求实际生成一张 `1024×1024` 和一张 `2048×2048` 时，基础费用为 `0.02+0.04=0.06`。这是示例价格，不代表任何供应商报价。图片费用沿用渠道手动倍率及请求时冻结的价格版本，不叠加上游 Token 分组倍率。

当前识别 OpenAI Images 兼容响应（含每张独立尺寸）、Gemini 图片输出、Responses 已完成的生图结果和 Chat Completions 的 `image_url` 内容。流式请求在最终结果聚合后记账，不计算中间预览图或 Gemini 标记为思考过程的图片。只有异步任务 ID、尚未返回图片的接口不据此计费；未适配的响应格式仍返回原结果并标记未计价。URL 图片只尝试读取公开 HTTP(S) 地址的有限图片头，不携带渠道密钥，不访问私网地址；读取失败时仍可使用响应尺寸或明确规格标签。

上游没有返回 Token 用量时也能记录图片费用，不伪造 Token 用量。费用明细保留每张图片的尺寸、命中规则及识别来源。有任何图片无法匹配时，总费用显示「未计价」，已识别图片的明细仍保留；不能将这类记录当作零费用。

### 成本明细

每个请求都包含详细的成本明细：

```json
{
  "costItems": [
    {
      "itemCode": "prompt_tokens",
      "quantity": 1000,
      "subtotal": 0.0015
    },
    {
      "itemCode": "completion_tokens",
      "quantity": 500,
      "subtotal": 0.0020
    },
    {
      "itemCode": "prompt_cached_tokens",
      "quantity": 2000,
      "subtotal": 0.0005
    }
  ],
  "totalCost": 0.0050
}
```

### 分层定价明细

使用分层定价时，成本明细包含层级分解：

```json
{
  "itemCode": "prompt_tokens",
  "quantity": 150000,
  "tierBreakdown": [
    {
      "upTo": 100000,
      "units": 100000,
      "subtotal": 0.1000
    },
    {
      "upTo": null,
      "units": 50000,
      "subtotal": 0.0750
    }
  ],
  "subtotal": 0.1750
}
```

## 配置

### 按 Key 固定成本倍率

在[渠道的余额与倍率设置](channel-management.md#查询上游余额与跟随倍率)中可选择手动或跟随上游。假设本地模型单价算出的 Token 费用为 1 美元，同一渠道的 Key A 为 0.5×、Key B 为 0.8×：请求选中 A 时记 0.5 美元，重试选中 B 时该次执行记 0.8 美元，不使用平均倍率。

每次执行开始时保存实际 Key 的脱敏标识、价格版本、计算时点、倍率与来源。响应中的 `usage.cost` 和用量记录共用该结果；流式执行期间的同步、修改倍率或停用渠道不会重新定价。历史记录保留原值，备份恢复也保留这份成本快照。未配置本地价格时，不会仅因为查到了上游倍率就生成成本。

上游倍率只应用一次，不再与已保存的手动倍率相乘。独立固定费用保持手动倍率。自动模式在查询失败且旧值仍有效时标记缓存；超过 30 分钟、跨过上游已知时段边界或身份无法确认时使用备用手动倍率并标记估算。`UsageLog.costSnapshot` 可查询本次 Token 倍率及来源；`costPriceMultiplier` 返回费用项目共同使用的倍率，混合使用不同倍率时返回空值，明细显示「混合倍率」，不反推统一原价。旧记录仍按历史价格版本读取。

本地模型基础价需与上游的基础定价相符，且不能预先包含分组倍率。本地结果是成本估算，不应直接等同于上游最终账单。

### 模型定价

请求计价优先使用渠道中为实际出站模型保存的价格；渠道没有该项配置时，使用本次请求匹配到的模型标准价格。精确匹配和正则匹配都适用，即使渠道将模型名映射成另一个上游 ID，也使用选路时已经匹配到的模型。渠道明确配置的零价仍然优先，不会被标准价覆盖；两处都没有价格时，费用保持未知，不记为零。

默认价格在 **模型** 页面维护。需要渠道单独定价时：

1. 进入 **渠道** → **模型价格**
2. 为每个模型设置定价项目：

![模型定价](../../screenshots/axonhub-model-price.png)

   - 输入价格 (`prompt_tokens`)
   - 输出价格 (`completion_tokens`)
   - 缓存读取价格 (`prompt_cached_tokens`)
   - 缓存写入价格 (`prompt_write_cached_tokens`)

#### 缓存写入变体（Anthropic）

对于 Anthropic 模型，可以配置缓存写入的 TTL 变体：
- **5分钟** (`five_min`): 短期缓存
- **1小时** (`one_hour`): 长期缓存

模型目录只提供 5 分钟写缓存价。从目录的 Anthropic 官方来源选模导入价格时，会自动为写缓存添加 `one_hour` 变体，单价为输入价的 2 倍（Anthropic 1 小时写缓存计费倍数）；其他来源不自动添加。未配置 `one_hour` 变体时，1 小时写入按写缓存基础价计费。

渠道开启 [强制 1 小时提示缓存](channel-management.md#强制-1-小时提示缓存) 后，写缓存 Token 主要落在 1 小时档，需确保价格包含 `one_hour` 变体。

### 成本计算逻辑

1. **输入 Token 计算**: `PromptTokens - CachedTokens - WriteCachedTokens`
   - 缓存 Token 单独计费，不计入输入 Token 成本

2. **缓存写入变体处理**:
   - 如果存在 `WriteCached5MinTokens` 或 `WriteCached1HourTokens`，分别使用对应变体价格
   - 否则使用默认的缓存写入价格

3. **请求时的价格记录**:
   - 使用渠道价格时，通过 `cost_price_reference_id` 关联到不可变的价格版本。
   - 使用模型标准价格时，将完整价格及模型输出上限保存到本次执行的成本快照；不会因此新增渠道覆盖价。
   - 响应费用、请求日志、Key 用量和预算结算使用同一份价格与实际渠道／Key 倍率。后续改价不重算历史请求；已有未计价记录也不会自动回填。

### API 响应 (`usage.cost`)

是否把费用写进客户端响应，由系统设置里的 **注入 usage.cost** 开关控制，默认关闭。打开开关、且本次执行有可用的渠道价格或模型标准价格时，AxonHub 按请求保存的价格与倍率计算费用，写进客户端响应里的 `usage.cost`。

几点说明：

- 这个数字是 AxonHub 按本次采用的价格与倍率算出来的，不是上游供应商的账单。
- 上游响应自带的 `usage.cost`、`cost_details` 会被丢弃，不会转发给客户端（pass-through 请求除外）。
- 开关关闭时，普通请求路径下客户端看不到 `cost`。
- 渠道和匹配模型都没有可用价格、响应没有 `usage`、或开关关闭时，`cost` 字段会被省略。

#### Pass-through 与二进制接口

开启 pass-through 的请求原样返回上游 body，包括上游自己带上的 `cost`，AxonHub 不计算、也不改动。`POST /v1/audio/speech` 等二进制接口返回音频数据，没有 JSON `usage`，也不会返回费用。

#### 支持的响应字段

- OpenAI Chat Completions / Completions / Responses / Compact：`usage.cost`
- OpenAI Embeddings / Image Generation / Video：`usage.cost`
- Anthropic Messages：`usage.cost`（流式时在 `message_delta` 里）
- Gemini `generateContent`：`usageMetadata.cost`（AxonHub 扩展字段，官方 Gemini 响应没有该字段）
- Jina Embeddings / Rerank：`usage.cost`
- AI SDK text：`usage.cost`

```json
{
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 50,
    "total_tokens": 150,
    "cost": 0.000005
  }
}
```

#### 流式响应

`usage.cost` 在流式响应里的位置因协议而异：

- OpenAI Chat Completions / Completions：`cost` 在携带 `usage` 的那一帧，通常是 `[DONE]` 之前的最后一块；需要客户端在请求里带上 `stream_options.include_usage`。
- OpenAI Responses：`usage.cost` 在 `response.completed` 事件上。
- Anthropic Messages：`usage.cost` 在 `message_delta` 的 `usage` 里，流以 `message_stop` 结束。
- Gemini `generateContent`：`usageMetadata.cost`，流以 `candidates[].finishReason` 结束。
- AI SDK text：`usage.cost` 在 `e: finish` 事件的 `usage` 里。

## 查看成本

### 使用日志

每个请求的使用日志包含：

- **Token 用量**: prompt_tokens, completion_tokens, total_tokens
- **Token 详情**: 各类细分 Token 数量
- **成本信息**: total_cost, cost_items, cost_price_reference_id

### GraphQL API

查询使用日志及成本信息：

```graphql
query GetUsageLogs {
  usageLogs {
    edges {
      node {
        modelId
        promptTokens
        completionTokens
        totalTokens
        totalCost
        costItems {
          itemCode
          quantity
          subtotal
          tierBreakdown {
            upTo
            units
            subtotal
          }
        }
        costPriceReferenceId
      }
    }
  }
}
```

### 成本字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| `totalCost` | Float | 总成本（渠道价格优先，缺省使用匹配模型的标准价） |
| `costItems` | JSON | 成本明细项目数组 |
| `costPriceReferenceId` | String | 用于成本计算的价格版本引用 ID |

## 最佳实践

1. **配置模型价格**: 在使用前为每个渠道和模型配置准确的价格
2. **监控缓存使用**: 缓存可以显著降低成本，监控缓存命中率
3. **使用价格版本**: 价格变更时会自动创建版本，便于成本追溯
4. **定期审查**: 查看使用日志和成本报告以优化使用

## 故障排除

### 缺少成本数据

如果成本未显示：

1. 确认渠道价格或请求匹配模型的标准价已配置
2. 检查供应商是否返回 Token 使用量
3. 查看使用日志中的 `costPriceReferenceId` 是否正确

### 成本不正确

如果成本似乎不正确：

1. 验证定价配置（特别是缓存价格）
2. 检查使用日志中的 Token 数量明细
3. 确认是否使用了正确的价格版本
4. 对于分层定价，检查层级配置是否正确
