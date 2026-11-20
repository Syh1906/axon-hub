# Responses 渠道加速档位与计费方案

2026-11-18 · 调研基线 `c1575ab8` · 已在本地实现并完成定向验收，尚未部署。下文“当前价差”记录修复前的行为，已实现的配置及计费示例见[渠道使用说明](../zh/guides/channel-management.md#请求设置渠道超时模式覆写会话复用与工具权限)。

建议在渠道的「请求设置」增加「按支持档位处理」。管理员分别勾选 Fast 和 Ultrafast。客户端选了渠道支持的档位就原样使用，只有遇到不支持的档位才需要选择其他渠道或降档。对按上游成本记账的渠道，预算按本次准备发送的档位预估，结算优先采用上游最终确认的档位。

## 当前价差怎样产生

以客户端请求 Ultrafast、渠道强制 Fast 为例。网关向上游发送 `priority`（即 Fast），并在本次执行中分别记录客户端的 `ultrafast` 和发出的 `priority`。同一请求重试到另一渠道时，会另存一次执行。出站记录已经包含解决问题所需的信息。[转发顺序](../../internal/server/orchestrator/orchestrator_pipeline.go) · [档位采集](../../internal/server/orchestrator/service_tier.go) · [换渠道的测试](../../internal/server/orchestrator/responses_service_tier_test.go)

| 时点 | 当前实现 | 对这次请求的影响 |
| --- | --- | --- |
| 选择渠道 | 权限、模型、工具及流式设置等能筛渠道，专用模式覆写在选中渠道后才执行 | “支持 Fast 和标准”无法通过现有四档覆写表达 |
| 预留预算 | 估价时把计费依据切到客户端原始档位 | 可能按 Ultrafast 多预留。有预算限额且该档未定价时，本次渠道尝试可能被拒；反方向强制升档也可能少预留 |
| 按「优先上游返回」结算 | 有有效且一致的上游声明就采用它，缺失、无效或冲突时采用客户端档位 | 上游确认 Fast 时按 Fast；上游未报档位时却选择 Ultrafast 的价格 |
| 按「客户端请求」结算 | 始终采用客户端原始档位 | 这是管理员已明确选择的独立计费口径 |

上述价差还取决于是否开启加速模式计费、各档实际配置的价格以及计价项目。没有供应商的账单或可靠回报时，只能确认网关发出的档位，不能据此证明供应商实收金额。[渠道筛选](../../internal/server/orchestrator/select_candidates.go) · [预算估价](../../internal/server/biz/budget_pricing.go) · [预算限额判断](../../internal/server/biz/budget_admission.go) · [结算依据](../../internal/objects/service_tier.go)

## 渠道怎么配

建议在现有「不覆写」「强制标准」「强制 Fast」「强制 Ultrafast」之外新增「按支持档位处理」。选中后显示两个复选框，以及一项「请求的加速档位不受支持时」。该项默认选「跳过此渠道」，可以显式改成「允许降档」。同一渠道只启用一种处理方式，普通请求也需要升级的管理员仍可使用已有强制设置。

下表示例假设管理员已为同一模型的三个 Responses 渠道配置能力。

| 渠道配置 | 普通请求 | Fast 请求 | Ultrafast 请求 |
| --- | --- | --- | --- |
| A，两个都不勾 | 标准 | 跳过 A | 跳过 A |
| B，只勾 Fast | 标准 | Fast | 跳过 B |
| C，两个都勾 | 标准 | Fast | Ultrafast |

以上是默认行为。假设管理员允许 B 降档，一次 Ultrafast 请求仍优先找 C。没有可用的原档渠道、且既有权限和尝试次数允许使用 B 时，才向 B 发送 Fast。如果只勾 Ultrafast，收到 Fast 请求时也不能升到 Ultrafast；允许降档时只能退到标准。降档次序按服务档位 Ultrafast → Fast → 标准，与管理员填写的价格倍率无关。

普通请求保留缺省或标准语义，`auto`、`flex`、`scale` 继续按其已有含义处理。主动降到标准时移除 `service_tier`，让只接受普通请求、会拒绝该字段的上游也能接单；显式「强制标准」继续发送 `default`。诊断记录同时表达“此次选择标准”和“实际请求省略了字段”。需要固定发送 `default` 的旧渠道可继续用「强制标准」。

**升级时不推断供应商能力。** 旧渠道保留已保存的模式，新建渠道默认仍为「不覆写」。进入新模式并明确保存空勾选才代表仅支持标准。只配置了部分渠道时，其余「不覆写」渠道仍可能透传加速请求；要保证某模型只经确认支持的渠道发出，应给它的所有候选渠道完成配置。强制模式继续按管理员指定的档位发送，不标为已确认支持原档。

## 如何选渠道和核对最终请求

1. 先沿用项目、API Key、模型、出站协议等已有约束。在许可范围内，区分可原档发送的候选、原有未声明能力的候选、明确允许降档的候选，按这个顺序安排加速请求。每组内部沿用现有优先级、负载均衡、额度与重试规则。强制模式按实际目标档识别是否保留原档；不同档继续遵守已保存的显式强制意图。仅旧配置并存时保留历史选路顺序。标准请求不因支持的加速档位数量改变选路。
2. 能力作用于实际发往 Responses HTTP 或 WebSocket 的模型和端点。按候选中的实际模型逐项判断，保留模型与端点的对应关系。转换到其他协议的端点不能冒充能原档发送的 Responses 端点；原有 Chat Completions、compact、独立图片等接口的覆写契约维持现状。受上一轮响应绑定限制的续聊，只在原有合法候选中选择。会话复用、并行尝试和重试均遵守同一选择结果。
3. 选择和发送共用一份按请求、渠道和出站协议计算的档位结果。按支持档位模式对标准、Fast、Ultrafast 的处理在通用请求体覆盖之后执行，防止模板或透传把已判定的档位又改掉。重试按新渠道从原始请求重新计算。若是权限和模型都允许、确实因档位而全被排除，才返回“当前模型没有可用的 Ultrafast 渠道”等明确错误，复用项目同类能力筛选的 503 语义。已有权限、模型不可用和额度错误各自保留。
4. Codex 客户端通过模型目录决定是否显示加速档。已明确配置速度档的路线，按基础模型声明的候选档位、该渠道的实际转发策略和已保存的路线限制求交集；向当前调用方展示至少有一条可原档承接的可见路线支持的档位。只会降档的渠道不能让客户端误显 Ultrafast。未配置速度策略的路线以对应渠道和实际型号自己的档位声明为据，同时要求出站请求体覆写保留所选档位。两类速度字段和默认档位一起校正。目录不能为未获授权的渠道放开运行时访问。[候选及会话选择](../../internal/server/orchestrator/candidates.go) · [类似的能力筛选](../../internal/server/orchestrator/candidates_image_tool.go) · [Codex 目录合并](../../internal/server/biz/codex_definition.go) · [当前速度档聚合](../../internal/server/biz/codex_catalog_models.go)

## 预算和账单使用哪个档位

建议修复新产生的 Responses 执行，包括沿用旧强制覆写、透传或通过通用模板修改档位的请求。一次执行只保存一份计费证据。预算预留、成本明细和中断恢复从这份记录获取依据。

| 渠道计费选项 | 发给上游前的预算预估 | 上游处理后的结算 |
| --- | --- | --- |
| 关闭加速模式计费 | 基础价格 | 基础价格 |
| 优先上游返回（推荐） | 本次最终请求的档位 | 有效且一致的上游最终档位优先。上游缺失、值无效或声明冲突时，采用本次最终请求的档位，并标明“上游未确认”及原因 |
| 按客户端请求（已有显式选项） | 客户端原始档位 | 客户端原始档位；设置和明细都说明即使发生覆写或降档也按原始档位计价 |

本次最终请求已经移除了加速字段，或最终请求为缺省或 `auto` 时，在没有可靠上游声明的情况下按基础价估算和结算。保留原有 `flex`、`scale` 定价语义。显式 Fast 或 Ultrafast 缺价时继续显示未定价，依既有预算策略处理。固定费用、价格阶梯、分项倍率和渠道倍率均沿用现有计算。

自拟金额示例：同一份用量的标准价为 1 元，Fast 为 2 元，Ultrafast 为 6 元。客户端选 Ultrafast，B 获准降档，实际发送 Fast。在「优先上游返回」下，按这份示例用量预估应套用 Fast 的 2 元标准；上游未确认就按 2 元记账并标明依据，上游明确返回标准就按 1 元结算。实际预留金额还取决于请求预计用量。上游静默按 1 元收费却不返回档位时，网关无法从该次响应知道这 1 元；渠道已知只按标准处理时，应按标准能力配置。

在新执行快照记录采用新版判定以及出站档位是否已采集，区分采集后“字段确实为空”和历史记录“没有这份证据”。已结算账单维持原值；升级前已预留、升级后才恢复的请求按当时冻结的规则结算。请求成本详情展示客户端请求、实际发送、上游声明、最终计费依据；仅可信的上游声明标记为已确认。[预算预留入口](../../internal/server/orchestrator/budget.go) · [逐项费用](../../internal/server/biz/service_tier_cost.go) · [中断恢复用例](../../internal/server/orchestrator/service_tier_test.go)

## 实施顺序与验收

1. **先修计费。** 在现有档位判定中增加按最终请求定价的路径，共用预算和结算的请求档位选择，继续独立处理终局声明和显式按客户端计费。验证向下及向上覆写、请求字段已删除、有效回报与缺失或冲突回报，以及旧快照恢复两次只记一笔。
2. **增加渠道配置并接入选路。** 在现有渠道 settings JSON 内保存两项能力和不支持时的处理方式，区分“未配置”和“明确只支持标准”。同步 GraphQL 设置输入输出、渠道编辑、复制和导入导出；修改 schema 后用项目现有 `make generate` 生成。配置往返要验证空勾选以及编辑其他设置后仍保留。复用已有候选筛选与出站覆写位置，在排序、截取重试候选前完成能力分组。用上表的 A/B/C 测试原档优先、不可用时的降档、全跳过零上游调用及费用依据。[渠道配置对象](../../internal/objects/channel.go) · [GraphQL 配置](../../internal/server/gql/axonhub.graphql) · [请求设置表单](../../frontend/src/features/channels/components/channels-request-settings-dialog.tsx)
3. **用同一个请求验收端到端。** 覆盖 HTTP、WebSocket 终止事件、透传和通用覆盖，并让首选渠道失败后切到能力不同的备选。断言每次发出的请求体、预留采用的价格档、最终金额和成本详情一致。Codex 目录分别用仅能见 B、能见 B 和 C 的 API Key 验证。另测没有配置新策略的旧渠道，以及 compact 和其他既有出站协议。

调研阶段在仓库根目录运行以下现有测试，三个包均通过。实施后另外覆盖新策略的选路、预算及结算、目录可见性、渠道设置往返和备份恢复。通过真实本地 GraphQL 接口保存“只支持 Fast、允许降档”，刷新页面重新打开后仍显示相同配置。使用本地模拟上游，未核对真实供应商账单。

```powershell
go test -vet=off ./internal/objects ./internal/server/biz ./internal/server/orchestrator -run '^(TestServiceTierDecision|TestServiceTierBudgetEstimateUsesClientAndRejectsMissingRate|TestResponsesServiceTierOverrideProductionPipeline|TestResponsesServiceTierOverrideWebSocketFinalization|TestServiceTierRecoveryUsesFrozenFinalEvidenceAndRemainsIdempotent)$'
```

### 实施验收记录

| 项目 | 本地验证结果 |
| --- | --- |
| 渠道和请求 | 模拟上游实际收到首选渠道的 `ultrafast`、重试渠道的 `priority`。移除降档许可后零次发送。普通请求及 WebSocket 降到标准时均未携带加速字段。同一上游模型经不同别名选择不同协议时仍能找到可用的 Responses 端点。 |
| 金额和恢复 | 用同一份用量验证实际发送 Fast、上游未报档位按 Fast 结算，上游确认标准按标准结算。客户端 Ultrafast 缺价时，已获准降到有价格的 Fast 仍可估价。新旧快照经过两次恢复都只产生一笔扣费。 |
| 配置与目录 | GraphQL 保存、回读明确空勾选，渠道复制、JSON 备份恢复和前端设置合并通过。Codex 目录分别以能见单渠道和双渠道的测试 Key 验证速度档及默认值。 |
| 管理页面 | 通过隔离 SQLite 库验证四个旧覆写选项与第五项同列。保存「Fast 勾选、Ultrafast 未勾选、允许降档」后刷新并重新打开，三项均正确；切换到旧强制模式后收起能力控件。 |

本机通过 `internal/server/gql` 下的 `go generate` 执行仓库 `make generate` 目标的生成步骤。后端用 `go test -p 1 -vet=off` 定向验证 objects、biz、orchestrator、gql、backup。前端在 `frontend` 下执行 `node --experimental-strip-types --test src/features/channels/data/channels-settings-selection.test.mjs`，4 项通过。实拍见[五个覆写选项](../../output/implementation/responses-supported-tiers-20261118/five-modes.jpg)和[保存后重新打开](../../output/implementation/responses-supported-tiers-20261118/saved-fast.jpg)。

### 阶段独立复核与补验

独立审查发现四处与目标直接相关的边界。已修复历史来源检查与档位筛选的先后顺序、WebSocket 优先复用原连接导致的提前降档、Chat 入站时过早合并模型别名，以及 Codex 把不接收 Responses 的路线计入可用档位。界面沿用已确认的五项预览。上一轮刷新后重新打开的页面可访问性树显示 Fast 已勾选、Ultrafast 未勾选、策略为「允许降档」；实拍的可视区域只完整展示了两个复选框。

补验把同一请求的实际出站内容、预算记录、扣款与费用明细关联起来。以下金额均来自测试价格，按每百万输出 Token 标准价 10、Fast 20、Ultrafast 60 计算；请求允许输出 10 个 Token，成功响应实际输出 1 个。

| 本地用例 | 在同一请求中核实的结果 |
| --- | --- |
| HTTP 首选失败后重试 | 首次发 Ultrafast，预留 0.0006；备选发 Fast，预留 0.0002。成功响应未回报档位，实际扣 0.00002，费用明细采用出站档位，账户预留归零。 |
| WebSocket 终止帧 | 本机 WebSocket 服务读取到 `priority`，返回含 `default` 的完成帧。预留 0.0002，交付并关闭流后实际扣 0.00001。明细同时保留原档、发出档和上游确认档。 |
| 续聊与模型关联 | 只有原响应 ID 时保留来源限制，分别验证来源不可用、档位不支持以及同一来源不同模型剩余额度。当前连接有可展开的匹配历史时才允许改走更合适的档位；切换后发送完整输入，最终正文清除被通用模板加回的原 ID。回放大小判定与实际展开共用 128 MiB 上限。 |
| Codex 目录 | 仅 Fast、Fast 与 Ultrafast 两档、禁止 Responses 入站、模型仅能走 Chat 出站四种可见组合分别返回对应的速度选项和默认档位。 |

在仓库根目录设置 `GOMAXPROCS=2` 后执行以下定向入口。最终 WebSocket 用例使用 `httptest.NewServer` 绑定本机。调试时的首版用例曾以测试占位凭据访问一个外部握手地址，收到 401；该次失败未作为任何计费依据，也未核对供应商账单。

```powershell
go test -p 1 -vet=off ./internal/server/biz -run '^TestCodexCatalogSupportedTiersRespectVisibleRoutes$'
go test -p 1 -vet=off ./internal/server/orchestrator -run '^(TestResponses(SupportedTier|TierBound|TierContinuation|ContinuationRouting|ContinuationCredential|RecoveryAfterConfiguredCredentialRotation|TierWebSocket|Snapshot)|TestQuotaRoutingGate|TestHealthGateSelector|TestLoadBalancedSelector_TraceSticky)'
```
