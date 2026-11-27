# 请求列表与逐次诊断维护记录

## 请求列表加载与刷新（rc1-fork.29，2026-11-28）

PostgreSQL 为列表标记渠道「首次使用、复用、切回」时，优先在最近 128 个请求 ID 内寻找同项目、同会话的上一条有效执行。没有命中就继续查更早的历史。上一条采用的渠道已足以判断复用，只有发生切换才检查该渠道过去是否用过。

请求分页查询包含用量、速度或续接次数等扩展字段时，保留计算所需的元数据，按 GraphQL 选择读取头部、正文、响应分块和选路详情。同步上游时核对 [Ent 分页模板](../../../internal/ent/template/pagination.tmpl) 和 [字段选择](../../../internal/ent/gql_request_list_fields.go)，并覆盖显式选择头部的查询。

两站各固定最近 50 条请求，只读复测渠道历史查询。原查询在两站均达到 4 秒超时上限，新查询各执行三次，泉哥为 128–212 毫秒，云悠为 134–169 毫秒。该记录仅计数据库执行时间，页面端到端耗时待发布后测量。

页码模式单独查询相同项目和筛选条件下的请求总数。相邻页依据当前列表返回的翻页标记导航，总数到达后可跳到指定页或末页。手动刷新会更新总数；自动刷新最多每分钟更新一次。滚动模式无需查询总数。

## 请求筛选与逐次诊断

- 项目服务账号只读查询（已随 fork.16 发布，`289a243f`）：`read_requests` 可读取本项目的请求与执行尝试，CodeGateway 同时读取渠道名称时还需 `read_channels`。模型调用在记录请求和联系供应商前检查 `write_requests`。本地联调用两个项目检查可见范围，并用同一把只读密钥验证模型调用和管理写入均被拒绝。无需数据库结构或数据迁移。[Issue #7](https://github.com/Syh1906/axon-hub/issues/7) · [项目权限规则](../../../internal/scopes/rule_apikey_scope.go) · [服务账号权限](../guides/permissions.md) · [联调测试](../../../internal/server/codegateway_integration_test.go)

- 2026-11-08 流式交付取消诊断修复（本地验收与独立复审通过，未发布）：保留实际发送错误和关闭前的客户端取消原因，避免上游完成后把交付取消误归为无原因的 `failed`。取消记为 `canceled`；写入错误、超时、停机和无交付确认分别保留原因，使用既有 `requests.failure` 的 `delivery` 阶段展示。取消后先停止上游连接，等待实际消费者退出，再按原有 1 秒／256 事件／1 MiB 收集预算处理已缓冲事件；该预算不等于整个关闭流程的时限。修复后台晚读完成事件时漏记完成与用量的问题，保证尾部收集不提前接管仍在读取的源。成功交付后的断开不撤销完成；已有明确失败不被清理覆盖，不增加重试或改写历史数据。`gpt-6.1-sol / max` 独立审查发现的停止原因丢失、透传发送绕过记录、消费者结束信号提前三个问题已修复并复审。16 项持久化场景、普通／心跳 Responses 六场景各 10 次、真实消费者交接 10 次及相关流式／透传／重试／WebSocket 回归通过。无 schema/data migration。同步上游时核对 `stream_outcome.go`、`inbound.go`、`outbound.go`、`pass_through.go`、`request_failure.go`、`stream_lifecycle.go` 及请求失败保存；行为说明见[请求处理流程](../getting-started/request-processing.md)，命令及限制见[本轮记录](../../../.agent/summary/request57765-delivery-fix-20261108.md)。

- beta12-fork.2 请求诊断校验修复（已发布）：Zod 升至 4.6.7 后，`z.any()` 不再隐含对象字段可缺省；路由／错误诊断及会话补查的 `includePayload=false` 与执行正文校验发生冲突。仅将 `requestExecutionSchema` 的 `requestBody`、`responseBody` 显式设为可缺省，保留必要字段、项目／权限、取消信号和按需查询，不强制下载正文。真实 schema＋queryFn 的 9 项回归修复前 3 项失败、修复后通过；新锁文件环境全部前端单元测试 554 通过、1 原有跳过。无迁移，入口及生产核查详见本地 `.agent/summary/beta12-production-validation-20261107.md`。

- 2026-11-04 思考级别标签配色（已随 `.syh.17` 发布）：请求列表模型下方的思考级别沿用 claude-code-hub 配色，`none`／`minimal`／`low` 分别使用中性灰／暖灰／蓝灰，`medium` 为琥珀黄，`high` 为玫瑰红，`xhigh`／`max` 为逐步加深的红色，`auto` 为青蓝靛渐变；浅色、深色主题均适配，未知级别显示中性样式。实现为 `request-reasoning-badge.tsx`，接入共用的 `requests-columns.tsx`；不改变原有级别取值、文案、接口或数据库迁移。

- 2026-11-03 状态筛选计数修复（已随 `.syh.14` 发布）：状态菜单展开时通过 `requestLogStatusCounts` 一次分组统计当前时间、项目、渠道、模型、来源、Key 等条件下的全部匹配请求，忽略本菜单已选状态与分页；空状态显示 `0`，加载和统计失败分别提示，不再使用已加载列表的局部数量。展开期间跟随页面自动刷新设置更新，关闭后停止自动统计，翻页与滚动不重复查询；重新获得焦点或网络恢复不额外触发统计。列表与统计共用日志筛选及权限策略，不新增数据库迁移。接口／权限／SQL 定向测试、前端参数测试、本地浏览器组件联调和十万条 SQLite 内存数据性能采样见[验证记录](../../../.agent/summary/request-status-counts-20261103.md)；生产规模磁盘与并发性能未验证。

- 2026-11-02 分组用量日志修复（已随 `.syh.18` 发布）：主 GraphQL 补齐 `SelfUsageImageOutput` 的 `requestedWidth`、`requestedHeight`、`inspectionError` 及 `SelfUsageCostItem.unpricedReason`，与自助接口和共享前端查询保持一致，修复分组用量请求日志返回 422。`internal/server/gql/usage_cost_test.go` 的查询契约用例扩展为完整请求日志字段，覆盖图片规格、未定价原因和分页／筛选参数。不新增数据库迁移。

- 2026-11-02 列表回顶与行悬浮修复（未发布）：请求／渠道回顶保留已有首批，后台从最新游标刷新，避免清空数据后整表显示占位色块；超过八批缓存后的历史首批不会被误当作最新首批。程序回顶产生的滚动不再重复取消／触发刷新。所有共享表格行及单元格即时切换底色，保留原高亮颜色、键盘焦点与选中提示，避免跨行退色残留；提示浮层不变，无接口或数据库迁移变化。真实页面诊断、定向回归及本地组件验证见[回顶修复验收](../../../.agent/summary/list-return-top-20261102.md)；此前仅请求行动画修复见[首轮记录](../../../.agent/summary/request-row-hover-20261102.md)。

- 列表与正文加载（未发布）：计算字段声明必要 SQL 依赖，摘要不读取正文、错误全文和完整候选诊断；Trace／Thread 首条提问不连带加载响应及分片。完整详情／侧栏按标签读取正文，执行记录和音频延后到对应标签，关闭取消并回收。请求／渠道滚动页最多保留 8 批且支持向前恢复；渠道显式选择独立保留，切换显示列不清选择，真正切换筛选才清空。实现涉及 `request.go`、`trace.go`、`use-cursor-list.ts` 和相关详情／表格组件；无新增 migration，SQL、缓存及浏览器证据见[本阶段汇总](../../../.agent/summary/request-stage-review-20261101.md)。

- 2026-10-31 补充（未发布）：每次进入请求页重新获取列表；词元第三行显示新输入＝输入总量−缓存命中，悬浮展示完整明细，分组／自助请求日志同步。补齐主 GraphQL `SelfUsageCostItem.pricingEvidence`，修复分组用量请求日志422；同步上游时同时核对主 schema 和 selfusage schema，查询契约回归在 `internal/server/gql/usage_cost_test.go`。不新增数据库迁移。

- 状态：2026-10-30 实现、自检、独立限定复审及用户人工验收完成，提交 `30c46355`，纳入 `.syh.7`；沿用已确认的请求筛选、错误详情、渠道官网和分组用量入口方案。渠道名称保持原全局唯一约束。
- 请求页默认“今天”，支持昨天、本周、本月、全部及含秒的指定时间，按系统报表时区计算；渠道标签、渠道、密钥组（含未分组）和 Key 可组合筛选，同项多选为 OR，不同项为 AND。条件保存在 URL，改变条件回到第一页；调用方名称可直接筛选。
- 表格将 HTTP 状态及重试数放入状态区域；渠道以两行展示，会话状态、全部标签和实际 Key 的备注／末四位可查看；服务商为独立列。模型 ID 可完整复制；缓存读写分别展示，命中率不把写入相加；流式速度扣除首字耗时，非流式为整次平均，缺失数据不补造。
- 2026-11-04 渠道会话标记（已随 `.syh.17` 发布）：同一会话按每条已结束请求最后一次派发的渠道判断「首次使用」「连续复用」「切回复用」。上游失败仍计入；被本地并发、限流、预算或 Responses 续接检查拦截的尝试不计。日志列表、路由详情和日志渠道筛选使用同一渠道；没有派发的请求仍可查看尝试过程，无会话标记。缺少派发记录的旧日志沿用此前的判断，无需回填。实际选路、重试策略和候选诊断不变。
- 每次执行保存选路与密钥快照、错误来源、脱敏错误响应和实际重试／停止动作。原始错误响应遵循响应体存储策略，最多 64 KiB；旧记录显示“未记录”。返回客户端的状态与上游状态分开，选路快照受 `read_channels` 保护。流中只有转换后的结构化错误时不伪称拿到了原始上游响应。
- 独立复审修正：请求报表时区通过 `read_requests` 权限单独查询，项目查询携带项目标识；每次尝试清理前次 Key，避免跨凭据类型重试残留；已返回客户端的流断开或取消时记录停止原因，预读失败仍沿原重试流程处理。三项问题已由独立审查复核关闭。
- 渠道官网独立于 API 地址，选填且只接受无账号密码的 HTTP(S) URL；未填时名称不跳外链。保存、复制渠道和备份恢复保留官网字段。
- 分组行新增用量入口，管理账号直接查询当前项目的当前成员 Key 历史，停用 Key 与空组均可查看，不借用有效 Key 登录自助端。分组、单 Key 及自助用量页现默认“近 7 天”（2026-11-05 调整）；复用自助页的统计组件，管理查询缓存按账号、项目、分组隔离；自助端认证和按调用 Key 限定范围的机制不变。
- 代码：`internal/objects/request_diagnostics.go`、`internal/server/biz/request_log_filters.go`、`managed_group_usage.go`、`internal/server/orchestrator/request_diagnostics.go`、`diagnostic_redaction.go`、对应 GraphQL schema，以及 `frontend/src/features/requests/`、`frontend/src/features/key-groups/usage.tsx` 和渠道表单。
- 升级：新增可空 `channels.website_url`、`requests.client_status_code`、`request_executions.selection/failure/retry_decision`，由 Ent 托管；无独立 data migration、无名称索引变化，不改已发布迁移。历史快照不回填。SQLite 已完成旧表升级、重复升级和旧值保留验证，MySQL／PostgreSQL 实例尚未验证。
- 验证：主模块新增筛选组合／夏令时边界、分组权限／停用 Key、选路快照／换 Key、错误脱敏和官网 URL 用例；`llm/` 的 `go test ./pipeline` 覆盖实际停止重试原因；前端 Node 用例验证流速缺失值。真实页面使用隔离 SQLite 示例数据验收，不访问真实供应商。截图与本轮记录在 `output/implementation/axon-request-filters-20261030/`。
- 同步上游注意：核对 Ent 字段／GraphQL 同名类型、执行中间件记录顺序、停止重试观察接口，以及分组权限是否再次引入用户／角色管理的额外条件。
