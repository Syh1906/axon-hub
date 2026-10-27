# Fork 维护台账

> Parent: [AGENTS.md](../AGENTS.md)

本台账按 feature 和 bugfix topic 记录本 fork 相对上游 AxonHub 的差异，供新增 fork 改动和
同步上游时核对。这里只记录已确认的行为、代码归属和证据；是否保留、调整或删除
fork 改动，由用户决定。

本仓库继承 ChouUn 的维护版本；下列既有功能保留原提交归属。直接上游与原始上游
分别记录在 `release.config.json`，自己的 `.syh.N` 发版规则见[独立发版与部署](zh/development/release.md)。

## 维护检查表

### 新增或实质修改 Fork Topic

- [ ] 新增或更新一个独立 topic，不把无关改动混在同一条目中。
- [ ] 按功能来源分类：fork 自有功能的行为、实现和验证统一维护在原 Feature Topic 中，不按修复批次另立 topic 或修订小节；Bugfix Topics 记录对上游已有功能的修正，不按提交的 `feat:` / `fix:` 前缀分类。
- [ ] 记录用户可观察行为、边界、相关提交和主要代码路径。
- [ ] Bugfix 若为上游回补，记录上游来源、fork 中对应的提交及 fork 适配。
- [ ] 明确 schema migration、data migration、测试和 fixture 的影响；没有也要注明。
- [ ] 删除已经失效的描述，保证台账反映当前实现。
- [ ] 已与上游完全一致且无兼容负担的条目只保留在上游同步记录；已发布迁移等仍须维护的差异继续保留归属条目。

### 同步上游 Release

- [ ] 逐项阅读当前 topic，并对照上游在相关代码路径和行为上的变化。
- [ ] 同时核对 schema migration、data migration 及其版本顺序。
- [ ] 报告语义重叠、兼容性变化和迁移冲突，不只依赖 Git 冲突判断。
- [ ] 涉及保留、调整或删除 fork 改动时，先取得用户决定。
- [ ] 按最终实现更新 topic 中的事实、路径和相关提交。

## 上游同步记录

### v1.0.0-beta10（2026-09-09）

- 基线：`v1.0.0-beta7` → `v1.0.0-beta10`（上游 124 个提交），以 merge 合入，
  merge 提交 `7fdbd2ae`。
- 冲突：16 个文件。catalog 主题四个文件与两份 `providers.json` 快照直接取上游；
  `generated.go`、`internal/ent/internal/schema.go` 取上游后 `make generate`；
  `routeTree.gen.ts` 用 `@tanstack/router-generator` 重新生成；其余按 topic 手工拼接。
- 迁移：上游新增 `v1.0.0-beta9` data migration；schema 变化由 ent 托管，无冲突。
  注册顺序 `fork.1` → `beta8` → `beta9`，见「上游模型价格时间戳修复回补」。
- 验证：`go test ./...`（主模块）通过；前端 `node --test` 88 个通过；`tsc --noEmit`
  错误集合与上游 beta10 干净树一致（190 个上游既有错误，未新增）。未运行 lint、build
  与仓库 e2e 套件。
- 真机验收：本机 Docker（`axonhub:local`，SQLite）灌入三个验收 fixture 后，用一次性
  Playwright 脚本逐项核对渠道 tab 分组与计数、生图 tab 过滤与徽章、翻页无重复、
  新建/编辑对话框的 API 格式、两个分析页与模型页，21 项通过。过程中发现生图拆分的两处
  回归（列表字段、编辑同步），已在 `e18cc308` 修复。
- 逐项核对：

| Topic | 结果 |
| --- | --- |
| API 密钥费用分析、模型详情分析 | 上游改了分析筛选组件，自动合并无冲突 |
| 渠道 tab 按供应商聚合 | 上游 `aa8e7c81` 也补了 `ollama_anthropic`，已合一 |
| 生图渠道拆分 | 在上游重构之上重接；真机发现列表字段与编辑同步两处回归，已修 |
| Docker 镜像发布 | latest manifest 步骤冲突，保留 `${IMAGE}` |
| 模型目录后端热加载 | 上游版本为超集，fork 版本被取代 |
| 排序字段为零值时分页错乱 | entgql 版本未变，绕过仍需要；上游无新增分页入口 |
| Provider 模型成本可为空 | 上游未改，仍为 fork 差异 |
| 模型价格时间戳修复回补 | 上游 `beta9` 迁移与 `fork.1` 并存，测试 helper 改名 |
| 模型目录 schema 修复回补 | 与上游一致，无差异 |

- 模型目录后端热加载：上游 `4483c2e4`（PR #2263），fork 提前回补 `d6a605a1`；
  `7fdbd2ae` 合入后采用上游完整实现，不再有 fork 差异。`catalog_filter.go`、系统配置与
  provider 快照均以上游为准；模型页使用后端 `providersCatalog(filtered)`。
- 模型目录 `experimental` schema：上游 `6f729f7c`（PR #2305），fork 提前回补 `8c978c6b`；
  `7fdbd2ae` 合入后与上游一致，无 fork 适配、迁移或待维护差异。

## Feature Topics

### 标签优先级、渠道快捷设置与表单增强

- 版本：纳入 `v1.0.0-beta10-fork.7.syh.3`。标签优先级保存于 `ChannelSettings.tagPriorities`，不新增数据库列或 schema/data migration；旧 JSON 无该字段时继承关联优先级。
- 契约：密钥显式覆盖 → 请求使用的标签默认 → 模型关联；密钥标签优先于项目，排除型标签不继承，多标签取最小配置值。先完成原权限与模型过滤，覆盖不能添加候选。
- 排序：所有负载均衡策略在同优先级内按权重分层，同权重再评分；保留硬不可用、近期失败、熔断恢复和轮询健康退避以及会话复用。此更新取代下方旧记录中的“四种策略行为不变”和跨权重比例分流；同步上游时须核对比较器与健康评分。
- 界面：标签批量编辑优先级及权重；标签默认可见，列表直接点击三列数值；未筛选标签时优先级直接取渠道自身标签最小配置值，表头不重复标签名。优先级／倍率按全筛选结果排序后分页，默认展示排序保存在浏览器。倍率专用 mutation 保留成本项、历史版本及事务锁。
- 模型表单：右侧从目录导入整张模型卡片（能力、模态、价格、限制和日期），不改左侧标识／规则；左侧仅空价自动填充，含零价配置及切换开发者均保留已填价格。姓选填且清空可保存；渠道从管理模型列表多选具体 ID，按手动模型保存，不自动关联。
- 代码：`internal/server/biz/channel_routing{,_query}.go`、`channel_price.go`、`internal/server/orchestrator/candidates_priority.go`、`load_balancer.go`、`frontend/src/features/channels/components/channel-routing-value-cell.tsx`、`channels-tag-priorities-dialog.tsx`、`channels-model-picker.tsx`、`frontend/src/features/models/components/models-catalog-import.tsx`。渠道大表单按 hook／纯函数／渲染拆分，手写文件均低于 2200 行。
- 验证：`channel_routing_test.go` 覆盖事务整体回滚、普通编辑保留标签设置、倍率保留原价格以及含零值／继承项的跨页排序；`candidates_tag_priority_test.go` 覆盖标签与密钥继承、权限集合不扩张和权重。相关四个后端包全部测试通过；前端价格用例和浏览器验证覆盖导入、零价保护、空姓回读及筛选多选。没有真实供应商调用，无新增持久测试 fixture。
- 维护注意：JSON 值排序沿用筛选后内存分页方式，会读取全部匹配渠道；非常大规模渠道列表需另行评估数据库表达式排序。启用渠道使用现有提交后刷新与 live 更新；无跨版本共享的渠道序列化缓存结构变更。


### 模型入口校验与渠道分发

- 行为：正式请求在 API Key 模型映射后，必须解析到已注册且启用的模型，再按开发者与
  模型的有效关联选择渠道；模型不存在、禁用、归档或无可用关联时拒绝，不回退直连渠道。
- 规则入口：模型配置支持精确、字面前缀、字面后缀、正则；启用精确配置优先，其他规则按
  `match_priority`、ID 升序取首个匹配。创建／编辑内的匹配测试与选路共用后端匹配器，测试不发上游请求。
- 创建交互：默认只创建并返回列表；“创建并关联”保留在创建方式菜单。菜单沿用外层 Dialog
  的页面交互限制，避免创建完成并关闭弹窗后残留鼠标禁用状态；打开创建／编辑表单时先聚焦标题。
- 公开 ID：`public_model_ids` 可为零／一／多个符合规则的具体 ID；显式空数组隐藏发现但不禁用调用。
  旧精确模型的 null 沿用原 ID；规则 null 不公开。三种协议列表及 OpenAI 详情按实际胜出配置与密钥范围展开，
  不泄露表达式或隐藏 ID，不因低优先级规则公开同名 ID 而改变胜出配置的可见性。
- 开发者身份：内置 ID 在单个／批量创建、编辑与开发者规则保存时统一大小写；列表分组、规则编辑、实际继承及决策计数使用相同身份。已有大小写规则条目全部保留参与继承，自定义名称不转小写。无数据库迁移；`model_developer_test.go`、`candidates_model_rules_test.go` 和前端 `developer.test.mjs` 覆盖写入、旧值继承及输入识别。
- 关联：新增 `channel_request` 沿用本次 API Key 映射后的 ID，不要求渠道目录预列此 ID；固定模型关联保留。
  规则继承开发者配置也使用具体请求 ID；规则及动态关联逐请求解析，避免共享缓存串用不同请求 ID。
- 映射与列表：API Key 映射采用首个匹配、不递归映射；公开模型列表只下发启用的注册
  目标及有效别名，不再合并渠道独有模型。OpenAI 模型详情使用映射目标元数据，保留对外别名。
- 设置：删除渠道回退、渠道模型列表合并及对应黑名单设置；旧 JSON 键被忽略，不能恢复旁路。
- 管理边界：Playground 指定渠道要求系统级 `write_channels`，项目所有者权限不能代替；显式查询渠道模型要求
  `read_channels`。已创建视频任务的查询、删除仍使用任务记录中的渠道，不重新选择推理模型。
- 同步上游注意：保留注册准入及映射目标校验，不能恢复按渠道支持模型直接选路；升级前
  核对未注册的渠道模型及映射目标，先补齐所需模型与关联，不自动注册或放宽准入。
- 提交：`569152cc`（`feat(models): 强制注册模型准入并按关联分发渠道`）。
- 代码：`internal/server/orchestrator/candidates.go`、`internal/server/orchestrator/model_mapper.go`、
  `internal/objects/apikey.go`、`internal/server/biz/model.go`、`internal/server/api/`、
  `internal/server/gql/model.resolvers.go`、`internal/server/gql/system.graphql`、
  `frontend/src/features/models/components/models-settings-dialog.tsx`、
  `frontend/src/features/models/components/models-action-dialog.tsx`。
- 迁移：Ent schema 新增 `match_type`（默认 exact）、`match_priority`（默认 100）、`public_model_ids`（可空）；
  唯一索引改为 `(model_id, match_type, deleted_at)`。已对照直接上游 `v1.0.0-beta10-fork.7`
  对应提交 `b205ae8` 的模型 schema；沿用启动时自动 schema 迁移替换索引，无新增 data migration 版本，
  不修改已发布迁移。SQLite 升级测试覆盖旧索引存在／缺失、旧行默认值、公开行为、唯一索引和软删除后身份复用；
  MySQL/PostgreSQL 实库升级尚未执行。
  静态关联缓存键包含匹配类型；新规则不使用共享关联缓存。
- 测试与 fixture：`internal/server/orchestrator/model_admission_test.go`、候选渠道测试、
  `internal/server/biz/model_test.go`、`internal/server/biz/model_list_routability_test.go`、
  `internal/server/api/openai_retrieve_test.go`、`internal/server/api/chat_test.go`、
  `internal/server/gql/model_resolvers_test.go`、`internal/objects/apikey_test.go`，
  覆盖模型状态、关联限制、映射、Responses 续接和管理权限边界；无新增 fixture。
- 新增验证：`internal/objects/model_match_test.go`、`internal/server/biz/model_routing{,_upgrade}_test.go`、
  `internal/server/orchestrator/candidates_model_rules_test.go`、`internal/server/api/model_rules_test.go`，
  覆盖四种匹配、重叠胜出、隐藏／多公开 ID、别名权限、动态与继承关联、三协议列表及升级路径。
- 创建交互验证：本地浏览器验证默认创建、可选创建并关联、关闭后连续操作，以及长表单打开时
  保持顶部且不自动展开候选列表。

### 密钥渠道优先级与渠道请求设置

- 行为：`APIKeyProfile.channelPriorities` 只覆盖已经过项目和密钥渠道／标签过滤的候选；数值越小越优先，
  无覆盖时先继承请求标签默认值，再继承模型关联优先级。同渠道同有效优先级合并并去重实际模型，同时保留每个模型的协议，
  避免重复计权或重试时沿用另一模型的协议；不修改全局渠道权重。
  同级权重分层、同权重负载均衡及既有会话粘性继续生效，不新增用户分组实体。
- 模板与缓存：优先级参加完整 profile 的深拷贝、比较、保存、模板导入导出与发布同步；直接修改仍脱离模板。
  API Key 序列化缓存键升为 `api_key:v2:`，避免旧缓存遗漏新增字段；启用渠道缓存为进程内对象，不跨版本反序列化。
- 渠道设置：`streamFirstEventTimeoutSeconds`、`nonStreamResponseTimeoutSeconds` 可空继承、0 取消该项渠道限制、
  正整数按秒生效；每次选中渠道后读取，即使禁用重试也应用显式渠道值，外层请求 deadline 继续约束。
  `disableSessionReuse` 同时否决普通粘性与健康门控 owner 绑定／复用，不影响正常候选或 Responses 上下文恢复。
  旧 owner 关闭复用后，后续成功的正常渠道可接管，决策原因记为 `session_reuse_disabled`。
- 界面：现有密钥配置与模板复用 `channel-priority-editor.tsx`；渠道行菜单新增独立“请求设置”，不重排渠道编辑页。
  渠道查询的静态字段选择归入 `data/channel-query-fields.ts`，原查询构造和导出接口不变，避免继续扩大已超长的数据文件。
- 迁移：复用既有 profile/settings JSON，新字段零值保持旧行为，无新 schema 或 data migration。
- 代码：`internal/server/orchestrator/candidates_priority.go`、`channel_request_settings.go`、
  `health_gate_selector.go`、`health_gate_session.go`、`llm/pipeline/pipeline.go`、
  `frontend/src/features/apikeys/components/channel-priority-editor.tsx`、
  `frontend/src/features/channels/components/channels-request-settings-dialog.tsx`。
- 验证：`internal/server/orchestrator/channel_request_settings_test.go`、`channel_response_timeouts_test.go`、
  `internal/server/biz/api_key_profile_template_test.go`，覆盖密钥并发隔离、重复候选合并、模板同步／脱离、
  每次尝试超时、0 与外层 deadline、普通粘性及健康门控归属；协议合并回归覆盖第二模型重试和健康门控裁剪。
  未向真实供应商发送验收请求。
- 同步上游注意：保留范围过滤先于优先级覆盖、每次尝试读取超时、两条会话复用途径同时否决的契约。
- 局部顺带修复：完整测试复现同渠道快速重建归属时 `time.Now()` 相同，旧请求可能误认新归属；
  新归属 generation 必须晚于缓存保留的旧标识，续租仍保持原标识。原晚完成回归用例加入时钟回退场景，
  避免依赖平台时钟粒度；不改变会话缓存形状或多实例串行化边界。

### 健康门控路由策略

- 状态：已实现。相关提交：`6298a0cb`、`328cb528`、`dd5e17d3`。会话归属与决策记录见
  「健康门控会话归属迟滞与决策记录」。
- 动机：管理端无法直观看到哪个渠道×模型坏了。现有 `(渠道, 模型)` 熔断器只挂在
  `circuit-breaker` 策略下，阈值写死，查询与重置方法无调用方
  （`internal/server/biz/model_circuit_breaker.go:144-149,359-435`）；默认 `adaptive`
  只按渠道扣分并在 5 分钟内衰减，坏渠道仍留在候选中，一个模型失败也会拖累同渠道其他
  模型（`internal/server/orchestrator/lb_strategy_bp.go:49-90`）；粘性渠道不经健康评分
  （`internal/server/orchestrator/candidates.go:709-721`），已坏的粘性渠道每个请求都会先
  失败一次再转移（粘性候选本身不做同渠道重试，`internal/server/orchestrator/outbound.go:694-699`）。
- 参考：CCH（`ding113/claude-code-hub` v0.9.5）的失败口径、全部不可用返回及管理端
  徽章/筛选/重置；不采用其按供应商整体熔断的粒度。
- 业务场景（用户确认）：
  - 长期红：某渠道×模型持续失败，不应频繁成为候选。
  - 间歇红：偶发失败、长期黄绿，本次请求需要绕开，下次仍可使用。
  - 单个渠道×模型损坏时，同渠道其他模型不受影响。
- 已确认决策：
  - D1 新增第五种负载均衡策略 `health-gated`（健康门控），与上游 `adaptive`、`failover`、
    `circuit-breaker`、`round-robin` 平行，可在系统重试策略、API Key profile 与模型设置
    中选择。上游四种策略的行为不变，以降低同步上游的冲突面。
  - D2 粒度：熔断主体为 `(渠道, 上游实际模型)`，即经渠道模型映射后发往上游的模型
    （候选项 `ActualModel`），不是请求模型或别名。
  - D3 全部候选熔断：选熔断最早到期的一个试一次；该次为计数失败或试探名额被占时返回 503，
    错误体为通用的服务不可用文案，不暴露渠道名（沿用 CCH，
    `src/app/v1/_lib/proxy/forwarder.ts:3214-3216,8626`）；结果为「不计入」类（如 400/404/422、429）
    时原样返回上游错误，不改写为 503。
    503 仅适用于尚未向客户端提交响应的失败；流式响应头已发出后
    （`internal/server/api/chat.go:190-197`）发生的中断沿用现有流内错误与终止语义，不重试、
    不改写 HTTP 状态码，健康结果按 R4、R6 记录；客户端取消后不再写响应。
  - D4 单机部署：健康状态只存进程内存，不做跨实例共享，重启后清零。
  - D5 统计范围：只统计走 `health-gated` 策略的流量；其他策略的请求不产生、不改变
    健康状态，保证管理端看到的熔断即实际生效的熔断。
  - D6 健康候选排序：模型关联优先级分组保持小值先；组内硬不可用
    候选后置，可用候选按渠道 OrderingWeight 大值先。WeightRoundRobin、LatencyAware、
    RateLimitAware、QuotaAware 综合分仅在同权重内排序，避免旧渠道历史样本压过新增高权重渠道。
  - D7 现有 `circuit-breaker` 策略及其熔断器保留原样，不改动、不复用其实例。
- 健康状态规则：
  - R1 状态：健康、不稳、熔断、试探。熔断与试探是互斥且优先的门控状态；仅在非熔断、
    非试探时，最近窗口 W 内有计数失败或首个 token 后断流才显示为不稳，否则为健康。健康
    与不稳同等参与候选（不稳仅用于展示，不降权，沿用 CCH）。熔断的组合在本次选路中不参与
    候选，也跳过粘性优先，但不删除、不改写粘性缓存。普通试探只放行无粘性渠道的新会话，
    同一渠道×模型同时只放一个请求（试探名额）。「无粘性渠道」以 trace/thread 粘性缓存原值
    为准（`internal/server/biz/request.go:1652-1671`），不以粘性渠道是否仍在候选中推断。
    有试探资格时，试探组合与健康候选按同一优先级分组和负载均衡评分正常排序，不前置也不后置。
  - R2 转换：连续计数失败达到 N 进入熔断；熔断到期进入试探；试探连续计数成功 M 次回到
    非门控状态（按 R1 的窗口规则显示健康或不稳），计数失败则重新熔断且时长翻倍（有上限）。
    健康或不稳下计数成功一次即清零连续失败。翻倍级数在恢复后保留，恢复后持续无计数失败
    超过熔断时长上限才清零；其间再次熔断从已达级数继续翻倍。
  - R3 计数单位：一次请求在某 `(渠道, ActualModel)` 上的尝试（含对该组合的同渠道重试）
    结束时记一次结果；中途失败、最终成功记为成功（沿用 CCH），最终失败按该组合最后一次尝试
    的分类记录。同渠道重试可能切换到该
    渠道的另一个 ActualModel（`internal/server/orchestrator/outbound.go:761-765`），各组合分别记录。
  - R4 结果分类：计数成功、计数失败、仅不稳、不计入四类（口径见 R6）。流式请求以完整
    终态为成功，收到 HTTP 200 或首个 token 不算成功。试探遇到「仅不稳」或「不计入」时释放
    试探名额，只更新最近异常信息，试探进度不增不减，保持试探状态等待下一个请求，不重新
    熔断也不延长熔断。
  - R5 无健康候选时的最后一试（D3 的细化）：经模型、权限等既有准入过滤后，若没有健康或
    不稳的候选，先按普通试探规则尝试取得试探名额；因本请求无试探资格（已有粘性渠道）、
    全部仍处于熔断或试探名额已被占用而无法取得时，进入最后一试。最后一试优先选已到期的
    试探组合，没有则选熔断最早到期的组合，按试探处理，计数失败按 R2 翻倍。最后一试与普通
    试探共用试探名额，选中组合的名额已被占用则立即返回 D3 的通用 503，不记健康失败。最后一试
    只尝试一次，不做同渠道重试。该
    出口不得返回模型不存在错误（现有空候选会映射为 `ErrInvalidModel`，
    `internal/server/orchestrator/select_candidates.go:111-115`）。
- 失败口径（R6，参考 CCH `src/app/v1/_lib/proxy/errors.ts:992-1064` 并按 AH 适配）：
  - 计数失败：上游 5xx、网络错误、超时（含 408 与首事件/非流式响应超时）、首个 token 前的
    流中断、空响应检测命中、401/403（按普通失败计数，与 CCH 一致；现有自动禁用规则照常独立
    运作）。
  - 仅不稳：首个 token 后的流中断，不计入连续失败。
  - 不计入：429、400/404/422 及其他未列出的 4xx、客户端取消、本地 RPM 与排队拒绝、熔断
    自身跳过（含试探名额被占）、无状态码且非网络/超时的本地错误（如请求转换失败）。
  - 429 边界：429 不计入本熔断器。仅当上游带可解析的 `Retry-After` 时沿用现有渠道冷却
    （`internal/server/orchestrator/rate_limit_tracking.go:90-102`）；无有效 `Retry-After` 时只影响
    本请求的故障转移，后续请求仍可正常选择该渠道，本 topic 不新增兜底冷却。
  - 与 CCH 的差异：CCH 将 429 及未命中客户端输入白名单的 400 计为供应商失败；AH 中
    429 按上一条处理，400/422 视为请求问题，均不计入。
- 可观测与管理规则：
  - R7 渠道列表：按渠道汇总徽章（N 个模型熔断 / N 个模型不稳），提供「只看异常」筛选。
  - R8 渠道健康详情：每个上游模型一行，含状态、连续失败次数、最近错误与状态码、熔断
    剩余时间、下次试探时间及重置操作。查看要求 `read_channels`，重置要求 `write_channels`。
    重置完全清零该组合：连续失败、试探进度、翻倍级数、最近异常及各截止时间全部清除，回到
    健康，并释放试探名额。重置开启该组合的新统计周期：重置前已发出的请求照常完成业务响应，
    但其迟到结果不改变新周期的健康状态，也不得释放新周期持有的试探名额。
  - R9 统一视图：凭证自动禁用在健康详情中一并展示，机制保持独立。429 冷却不展示：其状态
    由各接口编排器各持一份（`internal/server/orchestrator/orchestrator.go:37`），合并为全局会连带
    改变上游 RPM/TPM 计数行为。
- 配置规则（R10）：系统级参数（N、首次熔断时长、上限、M、W）放入重试策略；渠道级只覆盖
  阈值 N，放入渠道 `settings` JSON（沿用现有可选指针字段惯例，
  `internal/objects/channel.go:220-240`），留空沿用系统值。阈值为 0 时关闭该渠道熔断：
  该渠道各组合既不门控也不计数，管理端显示「熔断已关闭」而非残留状态。阈值由正数改为 0
  时结束原统计周期；由 0 改回正数时按空白健康状态开启新周期，关闭前及关闭期间发起的请求
  结果不回写新周期（与 R8 重置同一语义）。阈值切换在该组合下一次被选路、记录结果或被管理端
  读取时观察并生效；不为配置变更增加跨服务通知。不为配置新增表字段。
- 生命周期（R11）：渠道停用、删除、归档或模型映射变更时不主动清理健康状态，只随时间演进
  或由管理员重置。条目数以实际用过的渠道×上游模型组合为上限，不做定期回收。
- 取消与收尾：选路、请求准入和管理端状态读取遵守调用方 context，渠道观察锁可取消等待，
  配置读取保持串行顺序。取消返回原 context 错误，不应用失败读取的默认配置，也不误计
  上游失败。尝试健康结果登记使用独立 10 秒预算；超时只释放匹配代次与票据的自身探测占位，
  不改变新代次、其他探测或恢复进度。
- 默认值：N=5；首次熔断 5 分钟，试探失败翻倍，上限 60 分钟；M=2；W=5 分钟。
- 明确不做：不改变上游四种策略的行为；不引入跨实例共享或健康状态持久化；不向上游发
  合成探测请求（试探只用真实业务请求：普通试探仅放行无粘性渠道的请求，R5 最后一试除外）；
  不替代 429 冷却与自动禁用；不稳状态不降权。
  会话归属、请求决策记录与 Webhook 见「健康门控会话归属迟滞与决策记录」。
- 代码：新策略与健康状态使用新文件，不改上游熔断实现：
  `internal/server/biz/health_gate.go`（状态机）、`internal/server/biz/system_health_gate.go`（配置）、
  `internal/server/orchestrator/health_gate_{selector,middleware,outcome,errors,load_balancer}.go`、
  `internal/server/gql/channel_health_gate{.graphql,.go,.resolvers.go}`、
  `frontend/src/features/channels/components/channels-health-gate-dialog.tsx`。
  上游文件只做追加式接入：`internal/objects/routing.go`、`internal/objects/channel.go`（渠道阈值字段）、
  `internal/server/biz/system.go`（策略常量、`RetryPolicy.HealthGate`、归一化调用）、
  `internal/server/biz/channel.go`（状态字段）、`internal/server/biz/channel_query.go`（筛选入参）、
  `internal/server/orchestrator/orchestrator.go`（负载均衡器、中间件、`finalize` 改写最后一试错误）、
  `internal/server/orchestrator/candidates.go`（选路钩子）、`internal/server/orchestrator/load_balancer.go`、
  `internal/server/orchestrator/outbound.go`（最后一试不做同渠道重试）、`internal/server/gql/` 的
  `system.graphql`、`axonhub.graphql`、`gqlgen.yml`、`axonhub.resolvers.go`（`QueryChannels` 一行筛选钩子）
  与生成文件；前端策略下拉（`frontend/src/features/system/components/retry-settings.tsx`、
  `frontend/src/features/apikeys/components/apikeys-*-dialog.tsx`、
  `frontend/src/features/models/components/models-association-dialog.tsx`、
  `frontend/src/features/models/data/schema.ts`）、`frontend/src/features/channels/`、
  `frontend/src/locales/`；用户文档 `docs/{zh,en}/guides/load-balance.md`。
- 迁移：无 schema 或 data migration；重试策略新增可选 `health_gate` 对象、渠道 `settings`
  新增可选 `healthGateFailureThreshold`，旧数据缺省走系统默认。
- 测试与 fixture：`internal/server/biz/health_gate_test.go`（状态机、翻倍与清零、代次失效、
  阈值 0 切换、归一化）、`internal/server/orchestrator/health_gate_outcome_test.go`（失败口径边界）、
  `health_gate_selector_test.go`（门控、粘性跳过、试探资格、最后一试选择）、
  `health_gate_orchestrator_test.go`（端到端熔断、503 与透传、流式首 token 后断流）、
  `internal/server/gql/channel_health_gate_test.go`（状态映射与计数）。
  `health_gate_context_test.go` 覆盖取消、配置代次与探测释放；
  `health_gate_load_balancer_weight_test.go` 覆盖新增高权重渠道、硬不可用候选后置、同权重分配、
  调试/生产排序一致及其他策略不变。真实 SQLite 连接池占满时，配置读取按 120ms deadline
  退出，同渠道另一请求按 20ms deadline 退出锁等待。相同新会话测试使用旧排序时选 10、
  当前实现选 15；biz/orchestrator/gql 完整测试及健康门控三轮 race 检查通过；无新增 fixture。
- 同步上游注意：策略枚举与前端策略下拉为追加式冲突点；上游若新增策略、改动负载均衡
  组装或自行实现熔断可视化，需逐条对照 D1–D7、R1–R11。

### 健康门控会话归属迟滞与决策记录

- 状态：已实现。相关提交：`a4ffb86d`、`328cb528`、`dd5e17d3`。
- 依赖：建立在「健康门控路由策略」之上，仅在 `health-gated` 策略下生效；其他策略保持
  上游粘性语义。
- 动机：会话粘性在每次尝试前改写（`internal/server/orchestrator/request_execution.go:105-113`、
  `internal/server/biz/request.go:1269-1283`），故障转移后又被下一次波动切回，每次切换
  都可能落到冷缓存、推高费用。管理端也看不到某次请求为何跳过或转移渠道，熔断发生时
  无通知。
- 参考：CCH 的会话绑定同样只在成功时写入，但故障转移成功即改绑、健康时迁回更高优先级
  供应商（`src/lib/session-manager.ts:1659-1757`），无迁移迟滞，R1 的 thread 主键与 R3–R5
  为 AH 自有需求；
  决策记录参考 CCH 请求表的 `provider_chain` / `routing_trace` jsonb 列
  （`src/drizzle/schema.ts:571-575`）；Webhook 参考 CCH 开闸告警。
- 业务场景（用户确认）：会话不因单次波动在渠道间来回切换，也不长期停在失败渠道上。
- 已确认决策：
  - D1 有效缓存保护窗口内不主动迁回，窗口过期后按当前健康候选与权重重新选择。
  - D2 v2 归属必须有明确有效截止时间；缺失、过期或无
    截止时间的记录均按新会话处理，不再从旧 v1/previous-channel 记录恢复归属。
  - D3 K 为系统级参数，放在重试策略的健康门控参数中，不提供渠道覆盖。
  - D4 决策记录包含：请求开始时的归属渠道×模型、因熔断被跳过的渠道×模型、是否临时转移、
    是否最后一试、本次是否迁移（迁出、迁入与原因：归属熔断或连续 K 次转移）。
  - D5 Webhook 只在首次熔断与恢复时发送；熔断期间试探失败、翻倍延长不发送。
- 会话归属规则：
  - R1 会话主键：有 thread 时，归属与连续转移计数只以 thread 缓存为权威，选路不读取、不被
    任何 trace 旧值覆盖（现有选路 trace 优先，`internal/server/orchestrator/candidates.go:749-776`，
    `health-gated` 下需改为 thread 优先）；没有 thread 时才用 trace。成功提交迁移时同步改写
    当前 trace 的归属，仅用于一致性与展示，不是 thread 归属生效的前提。归属保护窗口默认
    5 分钟，最终出站 Anthropic 请求含合法 1 小时缓存断点时为 1 小时；窗口结束按新会话处理。
    只有原归属自身成功或实际迁移才续期，临时故障转移只更新计数，不续期旧归属。
    该窗口依据请求声明，不代表已确认上游命中；D1 不迁回只在有效窗口内保证。
    归属以渠道为单位，「归属渠道×模型」指归属渠道上本次请求选用的上游实际模型。仅在
    `prefer_previous_channel` 粘性模式下生效；粘性关闭时不读写归属。
  - R2 归属只在请求成功时写入，失败尝试不改写。提交时以会话当前归属为准重新计算（按会话主键进程内串行），
    归属新建或迁移时建立 Generation，正常续期保持不变；完成回调校验所选世代，旧请求
    不得改变新归属，即使过期后重新选择的是同一渠道。多实例共享缓存时不保证跨实例串行。
  - R3 单次故障转移：由备用渠道完成本次请求，归属不变且不续期；下个请求仅在保护窗口
    仍有效时回归属渠道，过期后重新选择。
  - R4 迁移条件：本次请求结束时归属渠道×模型处于熔断或试探（未获本次准入，含本次请求自身触发的熔断），或同一会话连续 K 次
    请求都需故障转移才完成；归属渠道不在本次候选中也按故障转移计数。迁往完成该次请求的渠道，
    计数清零。归属渠道直接成功时连续转移计数清零；请求整体失败或客户端取消时计数不变。
  - R5 迁移后不迁回（D1）。
  - R6 归属渠道×模型已熔断时不参与粘性优先及同渠道重试，但仍可按阶段一 R5 成为最后一试；
    未熔断时沿用现有粘性候选零次同渠道重试（`internal/server/orchestrator/outbound.go:694-699`），
    失败即转移。
- 归属存储与收尾：配置两级缓存且有 Redis 时，归属直接以 Redis 为权威读写，不经过异步
  L2→L1 回填，避免旧值覆盖新归属；未配置 Redis 时使用内存。逻辑有效期以 ExpiresAt 为准，
  物理缓存额外保留 30 分钟有限宽限期用于迟到结果判断，不延长黏性。会话/路由记录登记使用
  独立 10 秒预算，包含可取消的会话锁等待；其他策略的缓存模式不变。
- 决策记录与通知规则：
  - R7 请求决策记录：请求表新增可空 JSON 字段，内容见 D4，请求详情页展示；仅 `health-gated`
    请求写入，请求结束（非流式完成或失败、流式关闭）时写一次；渠道名称按选路时快照保存（归属渠道
    不在候选中时按当时的渠道记录补齐）。
  - R8 Webhook：新增渠道×模型熔断（`channel.health_gate_opened`）与恢复
    （`channel.health_gate_recovered`）事件，复用现有通知器（按事件名匹配订阅，
    `internal/server/biz/webhook_notifier.go:144-170`），异步发送，不在健康状态锁内调用；
    系统 Webhook 设置界面需增加事件选项。事件为进程内转移，多实例不去重。
- 默认值：K=2（`owner_failover_threshold`，≤0 归一为 2）；归属渠道同渠道重试 0 次（沿用现状）。
- 代码：新逻辑放在新文件：`internal/server/biz/session_owner.go`（归属缓存读写、决策持久化）、
  `internal/server/biz/health_gate_notify.go`（熔断转移异步通知）、
  `internal/server/orchestrator/health_gate_session.go`（归属判定与决策提交）、
  `internal/objects/request_routing.go`（决策记录类型）、`internal/server/gql/request_routing.graphql`。
  `internal/server/orchestrator/session_owner_ttl.go`（最终出站声明的保护期）、
  `internal/server/orchestrator/outbound.go`（保存最终传输请求对象）。
  阶段一文件扩展：`internal/server/biz/health_gate.go`（转移回调，锁外调用）、
  `internal/server/biz/system_health_gate.go`（K）、`internal/server/orchestrator/health_gate_selector.go`
  （thread 优先归属、跳过项与最后一试快照）、`internal/server/orchestrator/health_gate_middleware.go`（成功时提交）。
  上游文件只做追加：`internal/server/biz/request.go`（归属缓存字段）、`internal/server/biz/webhook_notifier.go`
  （两个事件、`.Model.ActualModel` 与 `.Trigger.OpenUntil` 模板变量）、`internal/ent/schema/request.go`（字段）、
  `internal/server/orchestrator/request_execution.go`（`health-gated` 下尝试前不写粘性缓存）、
  `internal/server/orchestrator/orchestrator.go`（每个 Process 的决策槽位）、`internal/server/gql/system.graphql`、
  `internal/server/gql/gqlgen.yml` 与 ent/gqlgen 生成物；前端 `frontend/src/features/requests/`（路由决策卡片）、
  `frontend/src/features/system/components/{retry-settings,webhook-settings}.tsx`、`frontend/src/features/system/data/system.ts`、
  `frontend/src/locales/`；用户文档 `docs/{zh,en}/guides/load-balance.md`。
- 迁移：请求表新增可空 JSON 列 `routing_decision`，由 ent 自动迁移，无 data migration。
  归属缓存为 `axonhub:routing:session-owner:v2:{thread|trace}:%d`，包含 ExpiresAt 与 Generation；
  不读取缺乏可证明有效期的 v1/previous-channel 记录。首次升级后原会话重新选路一次，
  避免继承已无缓存收益的旧黏性。旧 `previous-channel:v1` 保持整数值形状，供其他策略使用，
  不作为健康门控恢复归属的来源。无新增 schema/data migration。
- 测试与 fixture：`internal/server/biz/session_owner_test.go`（thread 权威、过期与旧值拒绝、
  显式截止时间、活跃续期与临时转移不续期、缓存读错误、决策读写）、
  `internal/server/biz/health_gate_test.go`（只在首次熔断与恢复产生转移、回调锁外可重入）、
  `internal/server/biz/health_gate_notify_test.go`（事件订阅匹配、渲染字段、异步桥接）、
  `internal/server/orchestrator/health_gate_session_test.go`（单次转移保留归属、连续 K 次迁移、归属熔断立即迁移、
  归属成功清零、失败与取消不写、流式仅完成后提交、粘性关闭不读写）、
  `internal/server/orchestrator/health_gate_selector_test.go`（跳过项、最后一试剔除、归属优先）。
  `health_gate_product_test.go` 覆盖只有渠道 10 → 新增 15 → 新 thread 选 15，有效旧会话仍选 10，
  空闲 6 分钟或 31 分钟后选 15，合法 1 小时声明仍有效时保持 10；
  `session_owner_ttl_test.go` 覆盖最终出站断点及覆盖操作。另覆盖临时转移不续期、旧请求迟到
  不改变新世代、旧键不继承及两级配置下 Redis 权威读写。空闲用绝对截止时间推进，旧
  30 分钟物理过期用 Redis 模拟时钟验证。三包完整测试及归属三轮 race 检查通过；无新增 fixture。
- 同步上游注意：在 `request_execution.go`、`candidates.go`、`biz/request.go` 中按策略分支
  改变粘性写入时机，为冲突高发点；上游改动粘性缓存或请求表结构时需逐条对照 R1–R8。

### 渠道标准价格引用与持久倍率

- 行为：渠道定价引用模型模块维护的完整价格，支持按渠道模型 ID 一键匹配，也可为
  指定渠道模型选择其他标准价格；带入基础单价、全量阶梯、缓存变体及时间覆盖，
  引用后仍可修改。无标准价格的模型不覆盖既有渠道价格，不再从外部目录填充。
- 倍率：按渠道独立保存，缺省为 1，支持显式零值；基础价格不随倍率改写，各类单价
  输入框右侧同一行只读展示实际价格，不另占一行。保存时将倍率写入价格版本快照，
  倍率变更生成新版本；历史版本不重算，普通渠道设置修改不覆盖倍率，自动补缺及
  渠道复制继承倍率。
- 交互：定价窗口使用视口高度，编辑区独立滚动，顶部引用与倍率、底部操作区固定；
  各行价格输入框等宽，每档提供基础价倍数及整档填充，包含缓存变体，填充后可单项修改；
  新版导出同时保存倍率与基础价格，旧数组格式导入按倍率 1 承接。
- 提交：`2eca7c59`（渠道标准价格引用、持久倍率与价格编辑交互）。
- 代码：`frontend/src/features/channels/components/channels-model-price-dialog.tsx`、
  `frontend/src/components/model-price-editor.tsx`、`internal/server/biz/channel_price.go`、
  `internal/server/biz/cost_calc.go`、`internal/objects/channel.go`、`internal/objects/price.go`、
  `internal/server/gql/price.graphql`。
- 迁移：无新增表字段或迁移；渠道设置、当前价格及历史版本使用兼容的 JSON 可选
  字段，旧数据缺省倍率为 1。备份保留倍率快照，不从当前渠道设置重算历史价格。
- 测试与 fixture：`internal/server/biz/channel_price_test.go`、`internal/server/biz/cost_calc_test.go`、
  `internal/server/biz/channel_auto_model_price_test.go`、`internal/server/biz/channel_model_sync_test.go`、
  `frontend/src/features/models/pricing.test.mjs`；无新增 fixture。

### 模型权威定价与全量阶梯

- 行为：模型新建、编辑及目录批量导入统一保存完整标准价格，列表和详情展示基础价格、
  全量阶梯及缓存写入变体。直接展示并编辑已有价格，不设「模型定价」开关，不因开关
  清空或重建价格；零价与未填写价格区分。
- 全量阶梯：输入总 tokens（含缓存）严格超过所填阈值时，全部计费项使用命中档位
  单价；支持多档，以及一次按基础价倍数填满整档后单独改价。填充倍数只是编辑操作，
  不作为另一项计费倍率存储。既有时间覆盖命中时仍优先整组替换价格。
- Anthropic 官方目录导入：provider 为 `anthropic` 且基础价同时含输入价与写缓存价时，
  追加 `one_hour` 写缓存变体，单价为输入价的 2 倍（十进制精确乘法）；每档阶梯按自己的
  输入价计算。5 分钟写入使用基础写缓存价，不额外添加 `five_min`。其他 provider、缺任一
  基础价时不推算；导入后可编辑或删除变体，已有模型不自动回填，需要时手动补价或重新导入。
  该规则补足上游目录仅提供 5 分钟写缓存价的缺口，倍数依据 [Anthropic 官方定价](https://platform.claude.com/docs/en/build-with-claude/prompt-caching#pricing)。
- 编辑交互：新增计费项和缓存写入变体的价格留空，明确填写的零价保留；新增阶梯阈值
  留空且填写有效阈值前不能保存，已有阶梯照常回显。添加按钮显示「添加计费项」，
  四项齐全时禁用并显示「已添加全部计费项」，删除一项后恢复可用。
- 目录选模：切换模型时同步重置价格及阶梯表单数组，无阶梯时清空数组，价格行与阶梯开关
  按已导入数据正确回显，不残留旧模型档位。
- 兼容：旧模型卡价格在 JSON 读取时转换为完整价格，旧成本字段仅作只读摘要；明确
  清除后不会从旧摘要恢复。已有渠道价格、历史价格版本和旧逐项阶梯计算不变。
- 渠道：既有自动补缺逻辑复制完整模型价格（含阶梯和缓存变体），不覆盖已有渠道价格；
  渠道引用、编辑及倍率衔接见「渠道标准价格引用与持久倍率」。
- 提交：`93e5878e`（权威模型定价与全量阶梯）；`2eca7c59`（渠道完整价格引用、倍率衔接与阶梯填充交互）。
  `bf37dae1`（Anthropic 官方目录一小时写缓存价）。
- 代码：`frontend/src/features/models/components/models-price-editor.tsx`、
  `frontend/src/features/models/data/pricing.ts`、`frontend/src/components/model-price-editor.tsx`、
  `internal/objects/model.go`、`internal/objects/price.go`、`internal/server/biz/model.go`、
  `internal/server/biz/cost_calc.go`、`frontend/src/features/models/components/models-batch-create-dialog.tsx`；
  定价说明见 `docs/{zh,en}/guides/cost-tracking.md`。
- 迁移：无新增表字段或迁移；兼容既有数据库及缓存中的模型卡 JSON。已重新生成接口代码。
- 测试与 fixture：`internal/objects/model_test.go`、`internal/objects/price_test.go`、
  `internal/server/biz/model_validation_test.go`、`frontend/src/features/models/pricing.test.mjs`，
  覆盖旧价格兼容、全量阶梯校验、价格保存与清除、整档填充、零价与未填写价格的区分，以及
  Anthropic 来源的一小时价格、各阶梯独立计算、其他来源与缺价时不推算；无新增 fixture。
- 同步上游注意：上游若调整目录导入或提供独立一小时写缓存价，应核对 `priceFromCatalog`
  并优先使用目录值，避免重复推算。

### API 密钥费用分析

- 行为：仪表盘按 API 密钥汇总请求数、Token 数和费用，以费用排名，并可展开查看
  模型明细；默认全部收起。
- 筛选：支持密钥模板，以及今天、昨天、本周、本月和全部时间；自然日边界使用
  系统设置中的时区。
- 提交：`d3d4160e`（新增功能）；`7415d058`（统一系统时区日期边界）。
- 代码：`frontend/src/features/analytics/`、`internal/server/gql/analytics.graphql`、
  `internal/server/gql/analytics_helpers.go`。
- 迁移：无。
- 测试与 fixture：`internal/server/gql/analytics_helpers_test.go`、
  `frontend/src/features/analytics-date-range.test.mjs`、
  `scripts/e2e/fixtures/api-key-analytics.sql`。

### API Key 自助用量页

- 状态：已实现。相关提交：`58f73057`、`1c5509ae`。
- 动机：只持有 API Key 的使用者无法查看自己的用量。普通推理 Key 可达的路由只有推理与模型列表
  （`internal/server/routes.go:170-259`）；上游 beta10 的 `apiKeyQuotaUsages` 只接受
  service_account 类型的 Key，需要 `read_api_keys`，可查同项目内隐私规则允许的非 personal Key，
  且只返回已配额度的 profile（`internal/server/middleware/auth.go:129-151`、
  `internal/server/gql/openapi/openapi.graphql:146-182`、`internal/scopes/rule_apikey_scope.go:78-87`，
  上游 PR #1779、#1838）；管理端的 Key 统计与请求列表需要用户 JWT，按项目授权后可见项目内
  非 personal Key 及本人的 personal Key，项目 owner 权限更宽
  （`internal/scopes/rule_user_project_scope_requests.go:19-22`）。
- 参考：CCH 用 Key 登录后进入独立只读页 `/my-usage`，页面有额度、按日期统计与逐条日志，
  日志顶层字段隐藏用户、Key、供应商名称和错误详情（`src/app/[locale]/my-usage/page.tsx:38-79`、
  `src/actions/my-usage.ts:90-110`）；CCH 的额度与按模型统计会一并返回所属用户名下所有 Key 的
  聚合（`src/actions/my-usage.ts:337-360,1014-1049,1112-1121`）。本 topic 只取页面形态，范围与
  口径按下列决策。
- 业务场景（用户确认）：拿到 Key 的人自己能看到用了多少、花了多少、哪些请求失败，
  不需要管理员账号，也看不到别人的数据。
- 已确认决策：
  - D1 只做页面：新增用 Key 登录的独立只读用量页，不进入管理后台；页面背后的查询接口
    不作为对外契约，不写入用户文档的 API 说明。
  - D2 只看本 Key：统计、趋势、日志只含登录所用 Key 自身的请求；不汇总创建者名下的其他
    Key，也不汇总项目。
  - D3 内容：用量统计、按天趋势、逐条日志；不展示额度。
  - D4 可用范围：所有 `user` 与 `personal` 类型的 Key 默认可用，不设开关。
  - D5 登录保持：Key 只保存在当前标签页，同一标签页刷新后保持登录，关闭标签页或登出即结束
    页面登录状态（不吊销 Key）；页面每次查询携带该 Key 认证，后端不新增登录凭证类型。
  - D6 失败请求：日志列出本 Key 的全部请求并标出成功或失败；请求数分成功、失败两项。
    Token 与费用按已落库的用量记录计入，失败或取消的请求若留有用量记录也计入（用户确认，
    与上游实际消耗一致）。
  - D7 时间范围：预设今天、昨天、近 7 天、近 30 天、本月，另可自定义起止日期，跨度最长
    90 天；自然日边界使用系统设置中的时区。
  - D8 查询接口为独立 GraphQL 入口 `POST /self-service/v1/graphql`（独立 gqlgen 包，不改管理端与
    OpenAPI 的 schema），只注册 POST，不开 playground。
  - D9 页面地址 `/self-usage`，管理端登录页追加一行指向该页的链接。
  - D10 限流：同一 IP 每分钟认证失败 20 次后，该 IP 在窗口剩余时间内提交错误 Key 一律 429；正确 Key
    不受该计数影响，认证失败计数表已满时也照常放行（用户确认：避免伪造转发头锁死他人，轮换 IP 的爆破
    由 Key 长度抵御）。认证成功后每个 Key 每分钟最多 60 次查询，查询计数表另计。两张计数表各上限 10000，
    进程内计数，多实例不共享。
  - D11 只统计 `source=api` 的请求，不含 Playground 与测试请求。
  - D12 请求日志与管理端对齐：请求日志复用管理端请求列表的共享单元格组件，
    列依次为 ID（`#编号` + 请求原始状态徽章 + 流式 / 非流式徽章）、模型、Token（总计 / 输入 | 输出 /
    推理）、读缓存（数量与命中率）、写缓存（数量与写入率）、成本（悬浮明细见「请求成本明细悬浮」）、
    耗时（仅已完成请求；流式显示首字与总耗时，非流式显示总耗时）、创建时间；不再设单独的状态列与流式列。
    管理端专有列（渠道、客户端 IP、调用方、API 格式、透传与协议标记、详情入口）不出现。管理端请求列表
    对应列改用同一组件，显示不变。
  - D13 措辞：本页金额统一称「成本」（汇总卡、趋势、按模型统计、请求日志），与管理端请求列表一致；
    不改管理端与分析页文案。
- 规则：
  - R1 认证：只接受启用状态、所属项目为 active 的 `user` / `personal` Key；`service_account`、
    `noauth`、禁用或归档的 Key 拒绝，不回退到系统 noauth Key（现有 `WithAPIKeyConfig` 会回退，
    `internal/server/middleware/auth.go:36-42`，需专用认证入口，不改推理路由）；Key 的 IP 白名单
    照常生效。Key 只经 `Authorization: Bearer` 请求头传输，不进入 URL，日志不记录明文。
    用户被停用或删除时其 personal Key 随之禁用或归档（`internal/server/biz/user.go:227-231,626-629`），
    沿用该联动。
  - R2 隔离：后端以认证 Key 的 ID 作为所有查询的显式条件（即使查询走系统 bypass 也保留该条件），
    查询参数不接受 Key 的 ID、名称或明文；访问不属于本 Key 的请求按不存在处理。
  - R3 统计口径：
    - 时间锚点统一为请求创建时间（`requests.created_at`）：用量经 `request_id` 归属到所属请求，
      跨日完成的请求其用量计在请求创建日，保证统计、趋势与日志可对账。
    - 请求数按请求表每行一次，`completed` 计成功，`failed` 与 `canceled` 计失败，进行中的请求
      不计入；不与用量表连接计数（一次请求可有多条用量记录）。
    - Token（输入、输出、缓存读取、缓存写入、推理）与费用为该请求全部用量记录之和，
      不按请求最终状态过滤（D6）；缺价不影响 Token 统计。
    - 模型统一使用 `requests.model_id`，即 API Key 模型映射后的请求模型，不是客户端原始别名；
      不使用 `usage_logs.model_id`（上游实际模型）展示、分组或筛选。
  - R4 趋势：后端按系统时区的自然日分桶并补零，返回日历日期；前端不按浏览器时区重新分桶。
    展示请求数（成功、失败）、Token 与成本。
  - R5 日志：按请求创建时间倒序、同时间按 ID 倒序分页，默认每页 20 条，后端上限 100 条；列见 D12；
    时间按系统时区显示并标注时区；可按时间范围、模型与状态筛选（筛选仍为成功、失败、进行中三类，
    行内徽章显示请求原始状态）。不展示渠道、上游实际模型、请求与响应正文、错误详情。
  - R6 成本：按系统设置的币种格式显示。单条用量未定价不按 0 计入；汇总（每日、每模型、总计）无用量为 0，
    有用量但全部未定价为「—」，部分未定价时显示已知成本并标注未定价记录数；真实零价显示为 0。日志行按 D12
    与管理端一致：无用量记录时 Token、缓存与成本列为「-」，全部未定价时成本为「-」，部分未定价时显示已知成本，
    未定价记录数在成本悬浮中标注。
  - R7 页面：独立公开路由，不经管理端登录；提供登出；中英文界面。页面使用独立的 Key 认证状态、
    请求与错误处理：不附加管理端 JWT 与项目上下文，不清除或改写管理端登录（现有全局 401 处理会
    清除管理登录并跳转 `/sign-in`，`frontend/src/main.tsx:59-64`、`frontend/src/gql/graphql.ts:96-101,139-143`）；
    认证失败只清除本页 Key 并回到本页登录；根布局的管理端查询与命令菜单不在本页触发。Key 只存于
    本页的 sessionStorage 与内存，不进入管理端 `authStore`、localStorage 或 URL；登出或换 Key 时
    清除旧查询缓存。
  - R8 元数据：页面查询接口随结果提供系统币种与时区，不要求 `read_settings`，不暴露其他系统设置
    （管理端 `useGeneralSettings` 需要 `read_settings`，`frontend/src/features/system/data/system.ts:1128-1149`）。
  - R9 查询开销：时间跨度由后端校验（不超过 90 天）；过滤条件为 Key ID、项目 ID 与 `source=api`
    等值加 UTC 半开时间范围，时区换算只用于分桶；请求查询须命中 `requests_by_api_key_id_created_at`，
    用量经 `request_id` 关联（`usage_logs_by_request_id`）。
- 代码：新逻辑放在新文件：`internal/server/middleware/self_service_auth.go`（严格 Bearer、仅 user/personal、
  无 noauth 回退、IP 白名单）、`internal/server/middleware/self_service_rate_limit.go`（D10，进程内固定窗口，
  计数表上限 10000）、`internal/server/biz/self_usage.go`（按 Key、项目与 `source=api` 过滤后窄 bypass 查询；
  UTC 15 分钟桶按系统时区归日并补零）、`internal/server/gql/selfusage/`（独立 gqlgen 包，仅 POST）；
  前端 `frontend/src/routes/self-usage.tsx`、`frontend/src/features/self-usage/`（独立 fetch、sessionStorage、
  页面内 QueryClient）、`frontend/src/locales/{en,zh-CN}/selfUsage.json`。
  上游文件只做追加：`internal/server/server.go`（handler 构造器）、`internal/server/routes.go`（`/self-service`
  路由组，`Handlers` 字段因 gofmt 对齐整体缩进）、`internal/server/biz/fx_module.go`、`internal/server/static/embed.go`
  （API 前缀）、`frontend/src/routes/__root.tsx`（`/self-usage` 跳过标题同步、初始化守卫与命令菜单）、
  `frontend/src/features/auth/sign-in/index.tsx` 与 `frontend/src/locales/{en,zh-CN}/base.json`（登录页链接）、
  `frontend/vite.config.ts`（开发代理）、`frontend/src/routeTree.gen.ts`（Vite 插件生成）。
- 文档：`docs/{zh,en}/guides/self-usage.md`，并在 `docs/{zh,en}/index.md` 指南表格追加链接；不列内部查询接口。
- 迁移：无 schema migration、data migration 与缓存键变化；已生成 `internal/server/gql/selfusage/` 的 gqlgen 代码。
- 测试与 fixture：`internal/server/middleware/self_service_auth_test.go`（认证矩阵：缺失、非 Bearer、伪造、
  AllowNoAuth 开启、service_account、禁用 Key、归档项目、IP 白名单；失败超限后错误 Key 429 而正确 Key 放行，
  伪造转发头与失败计数表已满均不影响正确 Key）、`internal/server/middleware/self_service_rate_limit_test.go`
  （失败计数、Key 查询上限、窗口重置、计数表上限）、`internal/server/biz/self_usage_test.go`（隔离、多条用量、
  失败与取消用量、进行中请求、跨日归属、费用空值与部分未定价、补零、Asia/Kolkata 与 America/Los_Angeles 夏令时、
  参数校验、日志分页不加载请求与响应正文等大字段）、`internal/server/gql/selfusage/graphql_test.go`（经真实中间件链的
  401/403/429、GET 拒绝、BAD_USER_INPUT、alias 与 fragment 重复根字段在执行前拒绝）、
  `internal/server/static/embed_test.go`（`/self-service` 未知路径返回 JSON 404）、
  `frontend/src/features/self-usage/{date-range,cost}.test.mjs`（时区与夏令时的日期预设、费用展示）；无新增 fixture。
  MySQL 与 PostgreSQL 的分桶表达式未在真实数据库上运行验证。
- 同步上游注意：与上游 OpenAPI `apiKeyQuotaUsages` 功能不重叠，不修改其权限；新路由组在
  `internal/server/routes.go` 追加。上游 PR #2510 重做管理端分析页，本页不复用管理端分析组件，
  避免交叉冲突。

### 请求成本明细悬浮

- 状态：已实现。提交：`1c5509ae`（`feat(requests): 请求成本明细悬浮并对齐自助页请求日志`）、`b61412dc`
  （`fix(requests): 成本悬浮始终显示倍率`）、`e28ec340`（`fix(requests): 成本悬浮基础合计与倍率行对齐`）。
- 动机：管理端请求列表「成本」列与自助用量页请求日志只显示合计
  （`frontend/src/features/requests/components/requests-columns.tsx:521-542`、
  `frontend/src/features/self-usage/index.tsx:464-466`），看不出由哪几项、按什么单价、是否乘了倍率构成；
  分项只在请求详情页展示，且不含单价与倍率
  （`frontend/src/features/requests/components/request-detail-content.tsx:465-569`）。
- 参考：CCH 请求日志「成本」列悬浮「计费详情」：输入、输出、缓存写（5m / 1h）、缓存读逐项列出落库金额，
  每项下方按「金额 × 1,000,000 ÷ Token 数」折算每百万 Token 单价；倍率不为 1 时先列基础合计与各倍率，
  再以删除线基础合计对比最终合计
  （`src/app/[locale]/dashboard/logs/_components/virtualized-logs-table.tsx:318-425,564-649`）。
  CCH `/my-usage` 复用同一表格，但后端置空分项与倍率，悬浮只剩合计（`src/actions/my-usage.ts:87-109,781-813`）；
  本 topic 按用户决定对 Key 持有者展示与管理端相同的内容。
- 已确认决策：
  - D1 范围：管理端请求列表与自助用量页请求日志的「成本」列，两处使用同一悬浮组件。请求详情页、
    用量日志页、自助页汇总卡、趋势与按模型统计不在本 topic。
  - D2 管理端内容：分项、折算单价、阶梯与倍率。
  - D3 自助页内容：与管理端完全一致，含基础合计与倍率（用户确认，不采用 CCH 的隐藏做法）。
  - D4 行结构对齐：已定价的悬浮一律依次列分项、基础合计、倍率、合计，不因倍率取值增减行；1 倍也显示基础合计与
    「×1」。合计行只显示最终金额，无删除线基础合计。自助页合并记录的倍率不一致时倍率显示「混合」。
- 规则：
  - R1 分项来源：只读已落库的 `usage_logs.cost_items`，不按当前价格重算。顺序为输入、输出、读缓存、
    写缓存（有 5m / 1h 变体时分行标注）、其他项（显示项目代码原名）；只列数量或金额大于 0 的项。
  - R2 每项显示 Token 数、金额（系统币种，6 位小数）与折算单价（金额 × 1,000,000 ÷ 数量，每百万 Token，
    保留 2 至 6 位小数，使 0.0028 这类低单价不被舍为 0；数量为 0 时不显示单价）；`tierBreakdown` 多于一段时
    逐段列出区间上限、数量与金额。
  - R3 倍率：取用量记录 `cost_price_reference_id` 对应价格版本（`channel_model_price_versions.reference_id`）
    的 `price.multiplier`，缺省按 1。无价格引用（引用上线前的记录）或价格版本已不存在时同样按 1：计费当时不存在倍率，
    实际按 1 倍计费；代码只归档、不删除价格版本。未定价记录（`total_cost` 为空）不返回倍率。基础合计 = 合计 ÷ 倍率，
    1 倍时与合计相同；各项金额为已含倍率的落库金额。倍率为 0 时落库金额全为 0，无法反推基础合计，显示「-」。
  - R4 合计：悬浮末行合计与单元格数值一致。合计为空（未定价）的单元格显示「-」且无悬浮；自助页部分
    未定价时悬浮末尾标注未定价记录数。
  - R5 多条用量记录：管理端列表沿用上游只取第一条用量记录（`usageLogs(first: 1)`）；自助页一行汇总该请求
    全部用量记录，分项按项目代码与变体合并数量和金额；各已定价记录倍率一致时列该倍率，不一致时倍率显示「混合」
    （`SelfUsageRequest.costMultiplierMixed`），基础合计显示「-」。
  - R6 可见性：管理端倍率随用量记录一并返回，可见成本即可见倍率，不额外要求渠道读取权限；自助页沿用
    「API Key 自助用量页」R2 的 Key 隔离。
- 代码：
  - 前端共享组件：`frontend/src/components/request-usage-cells.tsx`（ID、Token、读写缓存、成本悬浮、耗时单元格）、
    `frontend/src/components/request-usage-cost-breakdown.ts`（悬浮行构建）。
  - 管理端：`frontend/src/features/requests/components/requests-columns.tsx`（对应列的 cell 改用共享组件，列定义不变）、
    `frontend/src/features/requests/data/{requests.ts,usage-logs-schema.ts}`（列表与详情查询追加 `costItems`、
    `costPriceMultiplier`）、`frontend/src/locales/{en,zh-CN}/requests.json`（`requests.costBreakdown.*`）。
  - 管理端 GraphQL：`internal/server/gql/cost.graphql`（`CostItem.promptWriteCacheVariantCode`）、
    `internal/server/gql/cost.resolvers.go`、新增 `internal/server/gql/usage_cost.graphql` 与 `usage_cost.resolvers.go`
    （`UsageLog.costPriceMultiplier`）、`internal/server/gql/usage_price_loader.go`（单次 GraphQL 操作内按 reference_id
    批量查价格版本；共享查询使用操作上下文，单个字段的上下文只约束自身等待，查询失败不缓存）、
    `internal/server/biz/usage_price_multiplier.go`（窄 bypass 查询价格版本）；上游文件追加：
    `internal/server/gql/gqlgen.yml`（schema 列表）、`internal/server/gql/graphql.go`（`AroundOperations` 注入 loader）。
  - 自助页：`internal/server/biz/self_usage_cost.go`（本页请求的分项合并与倍率判定）、`internal/server/biz/self_usage.go`、
    `internal/server/gql/selfusage/{selfusage.graphql,resolver.go}`、`frontend/src/features/self-usage/{api.ts,index.tsx}`、
    `frontend/src/locales/{en,zh-CN}/selfUsage.json`、`docs/{zh,en}/guides/self-usage.md`。
- 迁移：无 schema migration、data migration 与缓存键变化；已重新生成 `internal/server/gql` 与
  `internal/server/gql/selfusage` 的 gqlgen 代码。
- 测试：`internal/server/biz/self_usage_test.go`（多条用量记录分项合并、倍率一致 / 不一致为混合 / 版本缺失按 1 参与判定、
  无引用按 1、未定价记录不参与倍率判定、其他 Key 的用量不出现）、`internal/server/gql/usage_cost_test.go`（历史价格版本
  倍率、无引用与版本缺失返回 1、未定价返回 null、同批多行只发一次版本查询且同操作内不重复查询、一个字段取消不影响
  同批其他字段、未设变体返回 null；批次窗口由测试手动关闭，不依赖调度时机）、
  `frontend/src/components/request-usage-cost-breakdown.test.mjs`（排序与零值跳过、折算单价、倍率 1 / 2 / 0.5 的
  基础合计、倍率 0 与混合无基础合计、阶梯）。
- 同步上游注意：上游若调整请求列表的 Token、缓存、成本、耗时列或 `CostItem` 类型，需同步到共享组件与
  `cost.graphql`；`graphql.go` 的 `AroundOperations` 注入与上游已有的操作钩子并列。

### 渠道强制 1 小时提示缓存

- 状态：已实现。提交：`bb25b45a`（`feat(channels): 渠道强制 1 小时提示缓存`）、`6c9c764d`
  （`fix(requests): 1 小时缓存标志改为以计费为准`）。
- 依赖：计费依赖「模型权威定价与全量阶梯」中的 Anthropic 官方目录一小时写缓存价，
  或手动配置的 `one_hour` 变体；未配置时，一小时写入按写缓存基础价计费。
- 动机：人与 agent 协作时，多个会话等待用户回复、或用户离开片刻，请求间隔常超过 5 分钟，5 分钟缓存过期后需按
  1.25× 重写全量前缀；1 小时缓存可命中并按 0.1× 计费。按当前用法，与用户对接的是 Claude 系模型，连续运行的
  子代理是 GPT，前者更容易遇到。AxonHub 目前原样转发客户端 TTL，自己补的断点不带 TTL（即 5 分钟，
  `llm/transformer/anthropic/ensure_cache_control.go:130-133`），没有任何强制 TTL 的配置。
- 参考：CCH 在 Key 与供应商上配置 `cacheTtlPreference`（`inherit` / `5m` / `1h`，Key 优先），只改写客户端已有的
  `system[]` 与 `messages[].content[]` 顶层 `ephemeral` 断点，不改 `tools`、顶层 `cache_control` 与 `tool_result`
  内嵌断点（`src/app/v1/_lib/proxy/forwarder.ts:774-850,3527-3538`）；1h 时补 `extended-cache-ttl-2025-04-11`
  beta 头（`forwarder.ts:852-863,8828-8833`）。响应只有写入总数、没有 5m / 1h 分档时，按请求偏好把总数计为 1h
  （`src/app/v1/_lib/proxy/response-handler.ts:6468-6485`），另有供应商级计费互换开关 `swapCacheTtlBilling`
  （`response-handler.ts:6444-6451`）。本 topic 不照搬后两者。
- 已确认决策：
  - D1 配置位置：只在渠道上配置，按上游是否支持 1 小时缓存开启；不在 API Key 上配置。
  - D2 强制方式：开关式，开启后全部断点改为 1h，只延长不降级；不提供「强制 5m」。
  - D3 AxonHub 自己补的断点在开启时同样为 1h。
  - D4 不补 `anthropic-beta` 头（官方文档已不要求）；中转站需要时用渠道已有的请求头覆盖自行添加。
  - D5 计费只按上游响应中的真实字段决定，不按请求的 TTL 推断，也不比较请求与响应的 TTL、不告警。
  - D6 不做计费互换。
  - D7 请求日志标记：在请求列表模型列「出站协议」「透传」图标之后加「1 小时缓存」图标，始终显示、否时灰色；请求
    详情的「用量」卡片标题旁加「1 小时缓存」徽章，仅为是时显示。标记与计费同源，与渠道开关无关。
- 规则：
  - R1 生效条件：渠道开启本开关，且请求以 Anthropic Messages 格式发往该渠道，包括由其他入站格式转换而来的请求
    与原样透传的请求体；其他出站格式不改。未开启的渠道行为不变。
  - R2 改写范围：请求中所有 `type` 为 `ephemeral` 的 `cache_control` 均设为 `ttl: "1h"`，覆盖 `tools`、`system`、
    `messages` 内容块（含 `tool_result` 内嵌内容）与顶层 `cache_control`（自动缓存）。只改已有断点的 TTL，
    不为此新增断点；客户端未使用提示缓存的请求不变。全部断点统一为 1h，满足「长 TTL 必须排在短 TTL 之前」。
  - R3 计费：写缓存 Token 按响应中的 5m / 1h 分档分别计价；响应只有写入总数、没有分档时，沿用现有逻辑按写缓存
    基础价计价（`internal/server/biz/cost_calc.go:211-241`），不因渠道开启本开关而改按 1h 计。
  - R4 标记判定：该请求任一条用量记录的 1 小时写缓存 Token（`prompt_write_cached_tokens_1h`）大于 0 为是，否则为否，
    与计费使用同一个字段，不另存标记。一个请求可能有多条用量记录（非流式响应先写用量、再做空响应检测与入站转换，
    失败后重试，`llm/pipeline/non_streaming.go:47-59`），用量记录不带执行 ID，无法归属到具体执行卡片，因此详情按
    请求显示一次。历史请求按已有用量记录直接显示，无需回填。只读 1 小时缓存、没有新写入的请求为否（上游读缓存不分 TTL）。
- 代码：`internal/objects/channel.go`（`ChannelSettings.ForceOneHourPromptCache`，JSON `forceOneHourPromptCache`）、
  `internal/server/gql/axonhub.graphql`（`ChannelSettings` 与 `ChannelSettingsInput` 追加字段，已重新生成）、
  新增 `internal/server/orchestrator/prompt_cache_ttl.go`（出站原始请求中间件：按 gjson 定位 R2 所列断点，只用 sjson
  改写 `ttl`，其余字节不变；先复制请求体，不改动重试共享的底层数组）、
  `internal/server/orchestrator/orchestrator.go`（中间件插在请求体透传之后、渠道请求体覆盖之前，覆盖操作仍最终生效）；前端
  `frontend/src/features/channels/{data/schema.ts,data/channels.ts,utils/merge.ts,components/channels-action-dialog.tsx}`
  （渠道编辑对话框「透传」下方的复选框）、`frontend/src/locales/{en,zh-CN}/channels.json`、
  `frontend/src/features/requests/{data/requests.ts,data/schema.ts,components/requests-columns.tsx,components/request-detail-content.tsx}`
  （列表与详情各加一个按 `promptWriteCachedTokens1hGT: 0` 过滤的用量计数查询；模型列第三个图标与用量卡片徽章）、
  `frontend/src/locales/{en,zh-CN}/requests.json`；
  `docs/{zh,en}/guides/channel-management.md`、`docs/{zh,en}/guides/cost-tracking.md`。
- 迁移：无 data migration 与缓存键变化；渠道设置随 settings JSON 保存。`v1.0.0-beta10-fork.3` 曾发布
  `request_executions.one_hour_prompt_cache` 列，现已从 schema 删除，Ent 自动迁移（`WithDropColumn(true)`）会删列。
- 测试：`internal/server/orchestrator/prompt_cache_ttl_test.go`（各位置断点改为 1h 且其余字节不变、非 ephemeral 与
  非断点位置不动、关闭 / 非 Anthropic 格式 / 无断点 / 字符串 system / 非法 JSON 原样返回、不改动重试共享的请求体）。
- 同步上游注意：上游若调整出站中间件顺序或 Anthropic 缓存断点处理（`ensure_cache_control.go`），需确认本中间件仍在
  请求体透传之后、覆盖之前执行。上游若改用量记录的 1 小时写缓存字段，需同步请求日志标记的判定。

### 模型详情分析

- 行为：仪表盘以模型为汇总项，以实际请求渠道为明细项，按费用排名且默认全部收起；
  支持按渠道标签筛选。
- 指标：请求数、费用、Token 数、费用/Mtok、成功率、缓存命中率、平均 TTFB 和
  平均输出速度；缓存命中率按缓存输入 Token 占输入 Token 的百分比计算，输入 Token
  为零时显示 0%。
- 输出速度口径：输出 Token 只取 `completion_tokens`（其中已含 reasoning 与 audio），
  与上游 beta8 起（`92f81b32`）的仪表盘吞吐一致，不重复累计 reasoning 与 audio。
- 列顺序：排名、模型/渠道、请求数、Token 数、成功率、缓存命中率、平均 TTFB、
  平均输出速度、费用/Mtok、费用；表格不重复显示内部标题。
- 排序：默认按费用降序。除排名外，各列通过无边框列头按钮在降序、升序和无序间
  切换；进入无序时恢复费用降序。排序同时作用于模型和各自的渠道明细，排名按当前
  顺序重算，模型的展开状态不随排序改变。
- 重试语义：`request_executions` 中每次 `completed` 或 `failed` 执行均视为一次实际请求，
  包括渠道内和渠道间重试；失败执行计入请求数和成功率，费用与 Token 来自成功用量。
- 日期：支持今天、昨天、本周、本月和全部时间；自然日边界使用系统设置中的时区。
- 提交：`7415d058`（`feat(analytics): 新增模型详情分析`）；
  `62011e01`（输出速度口径对齐上游）。
- 代码：`frontend/src/features/analytics/`、`internal/server/gql/analytics.graphql`、
  `internal/server/gql/analytics_helpers.go`。
- 迁移：无。
- 测试与 fixture：`internal/server/gql/analytics_helpers_test.go`、
  `frontend/src/features/analytics-date-range.test.mjs`、
  `scripts/e2e/fixtures/model-analytics.sql`。

### 生图渠道拆分

- 行为：`ChannelSettings.primaryApiFormat` 标记渠道主用途（JSON 列）。新建/编辑对话框对
  默认端点含 `openai/image_generation` 的供应商增加「OpenAI (图像生成)」选项，选中后
  类型仍为供应商基类型，写入 `settings.primaryApiFormat=openai/image_generation`。
  带该标记的渠道在 tab 中单独成组「<供应商> · 生图」（组 key `<provider>:image`），
  徽章追加同一后缀；供应商普通 tab 以 `excludePrimaryApiFormat` 排除生图渠道。
  渠道测试（含单 key 与批量 key）对该标记改发一次 1024x1024、`quality=low`、`n=1`
  的图像生成请求且不走流式，成功判定为响应至少含一张图。已有生图渠道需编辑一次
  并改选该 API 格式后才会分组与改测。
- 边界：该标记不影响路由，渠道仍按默认端点同时承接 `image_edit` /
  `image_variation`。选中该格式后切换供应商时，类型须由
  `getBaseChannelTypeForProvider` 推导，`getChannelTypeForApiFormat` 对图像格式恒为
  undefined，否则表单 `type` 与 baseURL 停在上一家。`excludePrimaryApiFormat`
  谓词必须兼容 `settings` 中不存在该键的旧行，否则普通渠道会被一起滤掉。
  生图测试耗时常超过后台请求的 30 秒上限，测试入口改以 `server.channel_test_timeout`
  为界，见「渠道测试受后台请求超时限制」。
  `countChannelsByType` 改为按 `(type, 是否生图)` 在内存中计数并返回
  `primaryApiFormat`，供分组函数拆组。
- 明确不做：不移除任何现有 API 格式选项（含 Anthropic Messages），不新增渠道类型，
  不改路由与端点解析。`codex` 虽有图像默认端点，但其对话框隐藏 API 格式选择器，
  未加入可选供应商。
- 依赖：建立在「渠道 tab 按供应商聚合」之上。
- 上游 beta10 适配：`getApiFormatsForProvider` 上游已委托给 `protocol-options.ts`，
  fork 只在 `config_providers.ts` 的包装层追加生图格式，不改上游文件。渠道测试的
  `marshalChannelTestBody` 接受上游新增的 `responsesWebSocket` 参数；`testSingleKey`
  同时保留 fork 的 `ch *biz.Channel` 与上游的 `responsesWebSocket`。编辑对话框的
  `settingsPatch` 与新建时 `mergeChannelSettingsForUpdate` 的参数中保留
  `primaryApiFormat`。
- 列表与编辑一致性：`CHANNEL_QUERY_LIST_NODE_BASE_SELECTION` 包含 `settings { primaryApiFormat }`，
  供列表徽章与分组读取；编辑初始化、`initialRow` 同步与打开时重置统一使用
  `getInitialApiFormatForRow`，正确回显图像格式，保存时保留生图标记。
- 提交：`d6d17d00`（`feat(channels): 拆分生图渠道`）；`7fdbd2ae`（合入 beta10 时重接）；
  `e18cc308`（补回列表字段与编辑同步）。
- 代码：`internal/objects/channel.go`、`internal/server/gql/axonhub.graphql`、
  `internal/server/gql/axonhub.resolvers.go`、`internal/server/biz/channel_query.go`、
  `internal/server/orchestrator/tester.go`、
  `frontend/src/features/channels/utils/group-channel-types.ts`、
  `frontend/src/features/channels/components/channels-type-tabs.tsx`、
  `frontend/src/features/channels/index.tsx`、
  `frontend/src/features/channels/components/channels-action-dialog.tsx`、
  `frontend/src/features/channels/components/channels-columns.tsx`、
  `frontend/src/features/channels/data/config_channels.ts`、
  `frontend/src/features/channels/data/config_providers.ts`。
- 迁移：无。
- 测试与 fixture：`frontend/src/features/channels/group-channel-types.test.mjs`、
  `internal/server/orchestrator/tester_test.go`、
  `internal/server/biz/channel_query_test.go`、
  `scripts/e2e/fixtures/channel-groups.sql`。

### GitHub 独立发布与两层上游追溯

- 当前行为：直接上游为 ChouUn/axon-hub，原始上游为 looplj/axonhub；
  `release.config.json` 分别固定两层 tag 和提交。自己的版本为 `<直接上游 tag>.syh.N`。
- 发布级别：`release.config.json.prerelease` 独立控制正式／预发布；正式版本设为 GitHub 最新，
  标题为完整版本号。上游名称中的 beta/rc 只用于追溯。更新查询按 GitHub 发布状态过滤；
  界面“包含预发布版本”纳入测试发布，修正随下一次程序构建生效。
- 发布：`.github/workflows/release.yml` 是唯一 tag 发布入口；复用现有测试，调用
  `docker-publish.yml` 向 `ghcr.io/syh1906/axon-hub` 发布 amd64/arm64 镜像，
  验证启动后由 GoReleaser 发布下载包、版本说明与 `release.json`。部署固定摘要。
- 来源与版本：`scripts/release/` 校验 tag、维护分支及基线；升级检查及 Windows 升级脚本指向本 fork，
  与迁移共享 `internal/pkg/xversion` 的数值感知比较，保护 `.syh.9 < .syh.10`
  及当前自己的版本早于下一次直接上游版本的顺序。安装和升级脚本改用本 fork。
- 独立生产机：私有 `Syh1906/axon-hub-ops` 仓库管理 PostgreSQL + Compose，
  先单台、再逐台；备份或验证失败停止后续机器，不自动回退数据库。
- 清理：删除旧每日 unstable 镜像工作流；模型目录定时同步仅原始上游启用；
  去掉上游 Homebrew 发布及构建后反写默认分支。保留 Go 模块路径和原始作者信息。
- 历史：ChouUn 的 `4dc42806` 将镜像发布到 `chouun/axon-hub`，
  `7fdbd2ae` 合入 beta10；本次独立发布替换其发布目标，不改写历史提交。
- 迁移：无新增 schema/data migration，已有迁移版本及行为不变。
- 验证：发布脚本覆盖独立编号、来源、分支与清单；版本比较补充双层 fork 顺序回归；
  ops 仓库覆盖摘要、首台凭据、备份与验证失败。执行结果以本次交付和 Actions 记录为准，
  未接入生产机前不声明真实数据库升级或恢复已验证。
- 详细规范：[独立发版与部署](zh/development/release.md)（[English](en/development/release.md)）。

## Bugfix Topics

### 自动补全候选列表按交互展开

- 归属：修正上游已有 `AutoComplete`、`AutoCompleteSelect` 的焦点与候选列表交互。
- 行为：获得焦点本身不展开候选列表；点击、输入或上下方向键可以打开，Escape、Tab 或
  切换到其他字段会关闭。切换两个自动补全字段时，不再把另一个输入框误认为当前列表内部。
  屏幕阅读器读取的展开状态与可见列表一致。
- 代码：`frontend/src/components/auto-complete.tsx`、`frontend/src/components/auto-complete-select.tsx`。
- 验证：本地浏览器检查初次打开、输入筛选、方向键打开、Tab 关闭及候选列表与输入框的对齐。
- 迁移与 fixture：无 schema、data migration 或持久 fixture 变化。
- 同步上游注意：保留输入、点击与键盘操作入口，以及仅保护当前输入框的外部交互判断。

### 渠道 tab 按供应商聚合

- 归属：修正上游已有渠道 tab 的分组与筛选；上游 `fa74d927`（PR #150）已提供 tab，
  `ea31f440`（PR #1734）调整过前缀分组。此处替换错误的分组依据，不是新增渠道 tab 功能。
- 行为：渠道列表顶部 tab 按 `CHANNEL_TYPE_TO_PROVIDER` 把同一供应商的协议变体聚成
  一组：`deepseek` / `deepseek_anthropic` 归入 DeepSeek，`moonshot` /
  `moonshot_anthropic` / `moonshot_coding` 归入 Moonshot。标签与图标取自
  `PROVIDER_CONFIGS`，tab 筛选使用该组实际类型列表而不是类型名前缀；未在映射中的
  类型保留独立分组，不会消失。顺带补齐映射缺失的 `ollama_anthropic`。徽章列原本已
  显示供应商，未改。
- 动机：上游按下划线前缀折叠，且只在裸前缀类型同时存在时才折，导致
  `moonshot_anthropic` + `moonshot_coding`、`opencode_go` + `opencode_go_anthropic`
  各自成组，而 `github_copilot` 又被误并入 GitHub。
- 同步上游注意：上游 `aa8e7c81`（beta8 起）也把 `ollama_anthropic` 映射到 ollama，
  合并后为同一行，不再是 fork 差异。
- 提交：`54fab437`（`feat(channels): 按供应商聚合渠道 tab`）。
- 代码：`frontend/src/features/channels/utils/group-channel-types.ts`、
  `frontend/src/features/channels/components/channels-type-tabs.tsx`、
  `frontend/src/features/channels/index.tsx`、
  `frontend/src/features/channels/data/config_channels.ts`。
- 迁移：无。
- 测试与 fixture：`frontend/src/features/channels/group-channel-types.test.mjs`、
  `scripts/e2e/fixtures/channel-groups.sql`。

### 透传预读队列循环等待

- 问题：启用流式透传与重试时，首个有效内容前的空 thinking 或被转换器跳过的事件可填满
  64 槽原始队列；消费者等待预读结束，生产者等待消费者，导致请求卡到取消或总超时。
- 来源：上游 [#2150](https://github.com/looplj/axonhub/issues/2150)、
  [#2580](https://github.com/looplj/axonhub/pull/2580)。参考 #2580 的分阶段暂存方案，
  fork 增加原始事件预算，不直接采用无界 backlog；保留重试前事件隔离及接入后的背压。
- 提交：`cbea376e`。
- 行为：每次尝试在消费者接入前暂存最多 4096 个原始事件、64 MiB 的 data/type/id 字节，
  超限返回不可重试的 `ErrPreCommitBufferExceeded`，不暴露失败尝试的内容；接入时有序交付，
  重试重置释放旧暂存。合法的单个 32 MiB SSE 事件仍可通过原始字节预算。
- 边界：未改变首个有效内容前不向客户端输出的语义；长思考仍可能触发客户端或代理的等待上限。
- 代码：`internal/server/orchestrator/pass_through.go`、`state.go`、`outbound.go`。
- 验证：真实 HTTP Anthropic 上游立即发送 200 个空 thinking delta，透传和重试开启时完整
  接收 206 个事件及 `message_stop`；行为回归覆盖跳过事件、事件顺序及事件数/字节上限。
- 迁移：无 schema、data migration 或持久缓存结构变化；无新增 fixture。
- 同步上游：核对 #2580 合并后的实现，不能丢失原始事件资源预算和取消时错误读取的同步保障。

### 渠道测试受后台请求超时限制

- 归属：上游已有渠道测试入口的通用超时与事务问题，慢聊天请求同样可触发；并非只属于
  fork 生图渠道拆分功能。
- 问题：渠道测试经 `/admin/graphql` 触发，该路由挂 `server.request_timeout`
  （默认 30s），正式 `/v1` 接口则用 `server.llm_request_timeout`（默认 600s）。
  生图上游约 40 秒返回时，测试在 30 秒被掐断并报 `context deadline exceeded`，
  上游却已成功并计费。聊天测试几秒即回，此前未暴露。
- 行为：新增配置 `server.channel_test_timeout`（默认 `180s`）。单个测试、单 key 测试与
  批量 key 测试入口用 `xcontext.DetachWithTimeout` 脱离请求 deadline，改以该值为上限；
  `http.Server` 的 `WriteTimeout` 取 `request_timeout`、`llm_request_timeout` 与该值的
  最大值。设为 `0` 保持原行为。测试脱离请求取消后，控制台断开也会跑到结束或超时。
  GraphQL 事务中间件对这三个 mutation 改为按字段跳过事务（上游只按操作名跳过前两个，
  匿名操作与单 key 测试仍被包进绑定请求 context 的事务，超时即回滚）。
- 边界：正式接口不受影响；反向代理（nginx、Cloudflare）的读超时需自行放宽。系统设置
  「重试策略 → 非流式传输超时」若开启，仍会在更早时刻以 pipeline 自己的错误终止。
- 提交：`321cfba5`（`fix(channels): 渠道测试改用独立的 channel_test_timeout`）；
  `ea465c71`（`fix(channels): 渠道测试 mutation 按字段跳过请求事务`）。
- 代码：`conf/conf.go`、`internal/server/config.go`、`internal/server/server.go`、
  `internal/server/orchestrator/tester.go`、`internal/server/gql/resolver.go`、
  `internal/server/gql/graphql.go`。
- 迁移与测试：无迁移；`internal/server/orchestrator/tester_timeout_test.go`
  （`TestTestChannelOutlivesRequestDeadline`，本地慢响应 mock 复现，修复前报同样错误）；
  `internal/server/gql/graphql_skip_tx_test.go`。本机 Docker（`axonhub:local`，SQLite）
  对照验证：mock 生图上游 45 秒返回，默认配置下 `testChannel` 与 `testChannelAPIKey`
  均 45 秒成功；`channel_test_timeout=0` 时两者 30 秒失败并报同样错误。
- 同步上游注意：`NewSchema`、`Dependencies` 与 `NewTestChannelOrchestrator` 多了一个
  参数，上游改动这些签名时需重接；若上游为 GraphQL 路由或渠道测试引入自己的超时，
  以上游为准并移除本配置。

### 透传时上游错误体被重包装

- 问题：Anthropic 出站 `TransformError` 把上游任何错误写死成 `api_error`，
  只保留 message，丢掉上游错误体里的其他字段。Claude Code 2.1.266 起会给对话中段
  `role: system` 消息挂
  `output_config`（按轮次 effort，beta `per-turn-control-2026-07-01`），
  sssaiapi 这类中转不认时返回 400，`raw_body` 内附 Anthropic 原文
  `messages.N.output_config: Extra inputs are not permitted`。CLI 只要在错误 message 里
  看到这段原文就会自动去掉该字段重试；经 AxonHub 后原文被吞，CLI 不重试，用户直接看到
  400。透传关闭时 AxonHub 重组请求会丢掉该字段，反而不会触发。
- 上游来源：上游 beta10 与 unstable 均未处理，fork 先行修复，可回馈。
- 行为：`llm.ResponseError` 新增 `RawUpstream`；出站包装器在请求体已透传
  （`PassThroughApplied`）时挂上上游原始 HTTP 错误；Anthropic 入站遇到带 `RawUpstream`
  且为合法 JSON 的错误时按上游状态码原样写回。系统设置「上游错误策略」为 custom 时
  仍以 custom 为准。透传关闭、OpenAI 协议入站、非 JSON 错误体保持原行为。
- 提交：`07099dd4`（`fix(anthropic): 透传时把上游错误体原样回传客户端`）。
- 代码：`llm/model.go`、`llm/transformer/anthropic/inbound.go`、
  `internal/server/orchestrator/outbound.go`。
- 迁移与测试：无迁移；`llm/transformer/anthropic/inbound_test.go`、
  `internal/server/orchestrator/outbound_test.go`、`internal/server/api/chat_test.go`。
  本机 Docker 真机链路（CLI 2.1.266 真实配置 → AxonHub → sssaiapi）验证：透传开时首个
  400 原样回传、CLI 自动重试 200；custom 策略回自定义文案；透传关时首个请求即 200。
- 同步上游注意：`llm/` 是独立模块，上游若改动 `ResponseError` 或 Anthropic 入站
  `TransformError` 需重接；若上游自行实现错误透传，以上游为准并移除本字段。

### 迁移器把 beta10 排在 beta9 之前

- 问题：`datamigrate` 用 Masterminds semver 比较版本，`beta10` 与 `beta9` 作为字母数字
  标识按字符串比较，得到 `beta10 < beta9`。beta8/beta9 实例升到
  `v1.0.0-beta10-fork.N` 后 `system_version` 抬不上去，之后所有 `beta10-fork.N`
  数据迁移被静默跳过；上游自身从 beta9 升 beta10 同样不抬版本。
- 上游来源：上游 beta10 的 `migrator.go` 比较逻辑未变，尚未处理；fork 先行修复，可回馈。
- 行为：`xversion.Parse` 把 `betaN`、`rcN` 拆成 `beta.N`，`beta7-fork.5` 拆成
  `beta.7.fork.5`，只用于比较，存储与展示的版本字符串不变。迁移版本、系统版本与
  构建版本三处比较均改用该函数。
- 提交：`6c07e14e`（`fix(datamigrate): 预发布号按数值比较`）。
- 代码：`internal/pkg/xversion/version.go`、
  `internal/ent/migrate/datamigrate/migrator.go`。
- 迁移与测试：无迁移。`version_test.go` 覆盖 beta6 到 1.0.0 的完整顺序；
  `migrator_test.go` 的 `TestMigrator_Run_NumericPrereleaseOrdering` 覆盖 beta9 系统
  执行 beta10 迁移并抬版本、beta10-fork.1 系统跳过 fork.1 与 beta10。
- 同步上游注意：若上游改为 `beta.10` 式命名或自行修复比较，本函数可退化为无害冗余。
  `internal/server/biz/version.go` 的更新检查仍用原生 semver，未改。

### 排序字段为零值时分页错乱

- 问题：entgql 的 `Cursor.Value` 带 `msgpack:"v,omitempty"`，排序字段值为该类型零值时
  （如 `ordering_weight` 为 0）游标丢掉该值，解码为 nil 后 `entgql.CursorsPredicate`
  退化为只按 id 比较，下一页会重复或漏掉行。表现为渠道页按权重排序、每页 20 条时，
  第一页末尾是权重 0，第二页开头又出现权重 5、10。
- 上游来源：ent/contrib `3625dcc2e035`（本 fork 锁定版本）仍带 `omitempty`，未见修复；
  本 fork 不改依赖，在调用侧绕过。
- 行为：`biz.RestoreZeroCursorValue` 在 `Paginate` 前用排序字段在空实体上取到的零值
  补回 `cursor.Value`（`omitempty` 只省略零值，nil 必然对应零值）。接入
  `ChannelService.QueryChannels`、`Query.channels`、`Query.prompts`（`prompt.order`
  同样默认 0）。默认 ID 排序的 `Field.String()` 为空，跳过不补。渠道页切换排序时重置
  分页游标，避免旧排序下产生的游标被带入新排序。
- 提交：`973928e3`（`fix(channels): 修复排序值为零时游标分页错乱`）。
- 代码：`internal/server/biz/cursor.go`、`internal/server/biz/channel_query.go`、
  `internal/server/gql/ent.resolvers.go`、`frontend/src/features/channels/index.tsx`。
- 触发条件：只在某页最后一行的排序值为零时触发，与 tab 无关。「全部」若带权重的渠道
  不少于 20 个，第一次翻页落在非零权重上看起来正常，翻到零权重之后的下一页同样出错；
  分类 tab 带权重渠道少，第一次翻页就出错。
- 迁移与测试：无迁移；`internal/server/biz/channel_query_test.go`
  （`TestChannelService_QueryChannels_OrderingWeightZeroCursor` 与
  `TestChannelService_QueryChannels_VendorTabZeroCursor`，游标经
  `MarshalGQL`/`UnmarshalGQL` 往返以复现，后者同时覆盖「全部」与分类 tab 逐页遍历）。
- 同步上游注意：若上游 entgql 去掉 `omitempty`，该绕过变为无害冗余，可移除；若新增按
  可为零值字段排序的分页入口，需同样接入。beta10 仍锁定 `3625dcc2e035`，
  `ent.resolvers.go` 的 `Paginate` 调用点未增加，无需改动。

### Provider 模型成本可为空

- 归属：修正上游模型目录解析；上游 beta10 的 `providerModelSchema.cost` 仅允许缺省、
  不接受 `null`，与 fork 的模型权威定价功能无关。
- 行为：模型目录中的 `cost` 可以缺失或为 `null`，远程拉取和内嵌快照的 provider 数据均可解析；
  不改变渠道价格的存储格式。
- 提交：`d4aa15c7`（`fix(models): 允许提供商成本为空`）。
- 代码：`frontend/src/features/models/data/providers.schema.ts`。
- 迁移与测试：无迁移；无专用测试。

### 上游模型价格时间戳修复回补

- 问题：上游保存渠道模型价格时使用 `time.Now()`，可能使 SQLite 持久化带 monotonic
  后缀的 `updated_at`，导致后续乐观锁更新无法匹配。
- 上游来源：`86eac3a7`（PR #2257）。
- 提交：`4896d9d6`（`fix(channels): 统一模型价格时间戳`）。
- Fork 适配：将上游 beta9 数据清理适配为 `v1.0.0-beta7-fork.1`；仅清理 SQLite，
  PostgreSQL 跳过不适用的文本处理。
- 代码：`internal/server/biz/channel_price.go`、
  `internal/ent/migrate/datamigrate/`。
- 上游 beta10：上游 `v1.0.0-beta9` 数据迁移已合入，包含同一 SQLite 清理，另加
  `settings.providerQuota` 清理，`e4bf324d` 起同样跳过 PostgreSQL。它与 `fork.1`
  同时注册，顺序 `fork.1` → `beta8` → `beta9`，两者对 `updated_at` 的清理幂等。
  `fork.1` 作为已发布迁移保持不变；其测试 helper 改为 `fork` 前缀，避免与
  `v1.0.0-beta9_test.go` 重名（`7fdbd2ae`）。
- 测试：
  `internal/ent/migrate/datamigrate/v1.0.0-beta7-fork.1_test.go`。
