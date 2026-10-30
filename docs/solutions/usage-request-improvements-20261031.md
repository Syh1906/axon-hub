# 请求费用、权限、时间与用量界面：实施与验收

状态：2026-10-31。原 11 项与追加的密钥交互、仪表盘成本卡均已实现；本地定向回归及下列浏览器联调通过。本阶段按用户确认提交代码；未推送、未部署，历史未计价记录没有回填。浏览器时区被外部脚本覆盖、全库存量类型诊断等限制见文末，不能将本记录理解为生产环境全部通过。

## 已完成行为

| 需求 | 实现与用户可见行为 | 主要证据 |
| --- | --- | --- |
| 请求成本 | 渠道明确价格优先，包括零价；缺省使用选路已匹配模型的标准价。价格及实际倍率冻结到请求快照，响应、用量和预算使用同一依据。 | 模型匹配、零价、持久化快照、图片与预算定向回归 |
| 新会话图标 | 新会话绿色，复用蓝色。 | 共享 `request-log-cells.tsx` 差异审查 |
| Key 用量缓存卡 | 成本旁新增整体命中率；悬浮或键盘聚焦展示模型读取／输入词元及各自命中率。 | 分组用量页复用同一组件，22.0% 及模型 0.0%／50.0% 与数据库一致 |
| 项目权限 | `read_api_keys` 可读授权项目全部 Key，`read_requests` 可读全部请求及项目用量；不再按个人 Key 创建者排除。 | 真实 Ent 查询测试覆盖同项目、跨项目、无 scope、只读不能写删 |
| 请求刷新 | 默认平稳更新，可切逐条插入；后台保留列表、处理中请求原行更新，浏览历史暂停，回顶恢复。 | 5 秒周期内暂停无轮询；完成前后 DOM 行身份相同；新增两条请求按 ID 顺序插入 |
| 默认统计维度 | 项目用量默认密钥分组，仍可切用户和密钥。 | 页面默认选中密钥分组 |
| 分组模型明细 | 分组展开模型请求数、Token、整体命中率及成本。 | 93 个已结束请求拆为 47／46，成本 0.1512＋0.1176＝0.2688 |
| 渠道今日用量 | 系统时区自然日的请求数与费用，区分无请求、零价和缺价。 | 三渠道 47／47／46 次；费用合计 0.4116；SQLite 午夜边界测试 |
| 系统时间 | 普通创建、更新、执行、同步等绝对时间统一传系统时区；预算时区及明确本地输入、纯日期字段保留原约定。 | 标准 Node 下跨日与夏令时通过；浏览器注入限制见下文 |
| 两页滚动 | 请求／渠道按 20 条增量加载；上滚收起视口下方完整批次，回顶恢复首批。 | 请求 20→40→60→80，上滚至 60；渠道 20→40→48，上滚至 40；滚动位置不变，回顶均为 20 |
| 历史倍率 | 渠道旁显示本次执行快照倍率；混合倍率明确标记，历史缺失不拿当前设置代替。 | 页面显示 ×0.24、混合倍率；后端快照回归 |
| 密钥交互追加 | 三个预算菜单补图标；组内单 Key 复用行内名称、有效期、五周期额度和启停；设置直接保存；单次添加取消人工预检。 | 名称、禁用、每日 10→增加 5→取消限额、延期、设置取消／重开／保存、同名失败保留草稿及直接创建成功 |
| 仪表盘成本卡 | 位于词元右侧、今日请求左侧，全部／月／周／日切换；按用户最后要求只显示金额，去掉时区与未计价说明。 | 四周期按钮及今日金额 0.411600 与 126 条用量记录之和一致；周期边界、缺价、空集合和权限测试 |

整体缓存命中率按缓存读取词元总数除以全部输入词元总数，不平均模型百分比；缓存排行榜继续沿用仅缓存触发请求的分母。无输入显示「—」。仪表盘成本卡的费用来源、时间边界保持不变，仅精简显示内容；全部费用未知时显示「—」。

## 费用与权限的原因及边界

原计价只查询渠道价格；模型已有价格、渠道没有复制到价格时仍会记为未计价。现在使用选路已解析的模型，避免正则匹配和上游别名丢失匹配关系。模型标准价内嵌到不可变的请求快照，渠道覆盖价继续引用价格版本；后续改价不重算历史。费用是本站按规则计算的记录金额，不是上游最终扣费凭证。

原页面权限与记录范围分别判断：角色能进入分组／Key／请求页面，但个人 Key 查询仍按创建者过滤，项目用量又要求所有者。现按已确认的项目只读权限统一可见范围；读取不授予修改／删除，也不开放其他项目。API Key 自身认证仍保持独立语义。排行榜继续使用系统 `read_leaderboard`，仪表盘沿用 `read_dashboard`。

渠道今日统计先按请求聚合用量，避免多次执行或多行用量将请求次数乘大。自然日先按系统时区计算，再转 UTC 查询；SQLite 的边界回归实际发现并修复了非 UTC 字符串比较造成的凌晨漏算。

## 文件入口

- 费用：`internal/server/biz/request_cost_snapshot.go`、`budget_pricing.go`、`budget_recovery.go`、`image_cost.go`；`internal/server/orchestrator/candidates.go`、`request_execution.go`；`internal/objects/channel_upstream.go`。
- 权限：`internal/scopes/rule_personal_apikey.go`、`rule_user_project_scope_requests.go`；`internal/server/biz/consumption.go`；项目统计路由和权限配置。
- new：`internal/server/biz/channel_usage.go`、`system_display_timezone.go`、`dashboard_cost.go`；`internal/objects/channel_usage.go`；对应 GraphQL schema／resolver。新输出沿用现有字段与用量表，没有新增数据库迁移。
- new：`frontend/src/hooks/use-cursor-list.ts`、`use-incremental-scroll.ts`、`use-system-time.ts`，`components/incremental-list-footer.tsx`、`system-time.tsx`，`utils/system-time.ts`。
- new：`features/self-usage/components/cache-hit-card.tsx`、`features/dashboard/components/cost-stats-card.tsx`、`features/channels/components/channel-today-usage-cell.tsx`、`features/channels/data/channel-usage.ts`。
- new：`features/key-groups/member-row.tsx`、`credential.tsx`、`key-settings-dialog.tsx`；复用 `quick-edit.tsx`，调整 `create-dialog.tsx`、`submission.tsx`、`data.ts` 与 API Key 菜单。
- 同步中英文成本、统计、自助用量文档及 `docs/fork-maintenance.md`。前端路径均位于 `frontend/src/`。

GraphQL 按仓库生成器生成。Windows 原目录曾发生映射文件占用，随后在同源受管副本完成生成并回写结果；没有手改生成器输出。Ent schema 快照仅因价格类型新增 `CurrentPrice` 方法更新反射元数据，没有新增字段或迁移。

## 验证命令与结果

Go 命令在仓库根运行；Node 命令在 `frontend/` 运行。

```text
go test ./internal/server/biz ./internal/server/orchestrator ./internal/server/gql ./internal/scopes -run 'Test(DashboardCosts|ChannelTodayUsage|Consumption|ModelPriceFallback|MatchedModelPrice|RequestCostSnapshot|BudgetZero|ProjectOwnerCanReadMemberPersonalKeyRequests|KeyManagementAuthorizationWithoutBypass|UserHasProjectScope|UserIsProjectOwner)' -count=1
node --test src/utils/system-time.test.mjs src/features/self-usage/date-range.test.mjs src/features/key-groups/quick-edit-date.test.mjs src/features/key-groups/drafts.test.mjs
git -c core.safecrlf=false diff --check
```

四个 Go 包通过。最初记录的 Node 命令误写了日期测试路径，该轮实际只执行 8 项；独立复审已按上方正确路径重跑四文件，11 项全部通过，另运行列表／刷新两文件 13 项全部通过，共 24 项。此前日期／列表／草稿的 21 项定向行为测试也通过。改动 Go 文件已 gofmt；差异检查通过，手写源码均未超过 2200 行。TypeScript 编译器 API `noEmit` 检查为 172 项，与 HEAD 覆盖基线及前一次检查一致，没有新增文件／错误码计数；不是全库类型检查通过。依项目规则未运行 lint 或 build。

新增持久回归：`request_cost_snapshot_test.go`、`channel_usage_test.go`、`dashboard_cost_test.go`、`frontend/src/utils/system-time.test.mjs`；现有权限和 Consumption 测试补充对应行为。

## 浏览器证据

使用本地 127.0.0.1 服务和独立 SQLite 夹具；48 个禁用渠道指向不可调用的本地假地址，未调用真实供应商。测试过程中添加了两个请求及一把测试 Key，所有数据均在受管临时数据库内。以下图片均为实际页面截图（new），不是设计预览：

- [最终仪表盘成本卡](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/dashboard-cost-final.png)
- [请求列表](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/requests.png)
- [渠道今日用量](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/channels.png)
- [分组模型展开](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/usage-groups.png)
- [缓存卡与模型明细](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/cache-hit.png)
- [Key 行内编辑](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/key-inline-edit.png)、[390 像素宽设置弹窗](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/key-settings-mobile.png)、[预算菜单图标](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/apikey-menu.png)。

小屏弹窗边界实测 x=16、宽 358、高 634，落在 390×844 视口内，底部操作可见。大表格继续使用内部横向滚动。日期输入自动化的 `fill` 曾只改 DOM 未触发 React；改用标准 input/change 事件后正常提交，未为此修改产品代码。

## 限制与剩余事项

- 当前浏览器的 `Intl.DateTimeFormat` 被外部注入包装，无条件把时区改为 UTC；传 `Asia/Shanghai` 后实际仍解析 UTC，`Date` 也被包装。项目源码无该注入，标准 Node 同样输入按上海显示正确。这会影响显示与前端“今天”日期计算；未擅自改变扩展或加入反制代码，注入来源未确认。因此不能声称所有页面在该浏览器下已正确显示系统时区。
- 本次时间显示覆盖来自代码清单与共享格式化测试，没有逐一打开所有管理页面。预算保留自身时区，输入控件保留明确标注的本地输入约定。
- 窄屏切换时图表出现尺寸警告，最终图表可见；没有把控制台称为完全无警告。后端版本检查曾遇 GitHub 403，与本轮功能接口无关。
- 未执行生产发布、真实供应商扣费或 MySQL／PostgreSQL 实例验收；旧未计价请求没有回填，本次仅提交代码。
- 本轮前后端服务及子进程已退出，8090／5173 无监听；临时 IAB 页面已关闭，屏幕尺寸覆盖已清除。verification-cleanup 返回 `cleaned=true`、`task_root_removed=true`，移除 2185 项；预先存在的 scratch 根目录保留。实际截图与本文保留。

## 独立复审与自检（2026-10-31）

按用户要求由独立 subagent `gpt-6.1-sol / max` 只读审查，主执行者同步自检。依据原 11 项与追加 4 项、最终确认图、实际源码及验证证据逐项核对，遵循 `axon-frontend-iteration`，未将既有存量问题扩展为全库治理；未重新设计已确认布局或修改已发布迁移。追加密钥交互沿用用户指定的已确认分组方案，成本卡沿用词元卡的周期切换与指定位置。

发现并关闭两处 P2：

1. **无设置读取权限时渠道费用币种错误。** 新增今日用量单元格原先依赖只在 `read_settings` 启用的前端查询，缺少该权限时默认 USD。现由今日用量接口返回 `currencyCode`，完整同步 Go 对象、GraphQL、查询、Zod 与显示；不扩大用户设置权限。新增 `TestChannelTodayUsageReaderGetsSystemCurrency` 覆盖 CNY 和无设置权限用户，并验证无渠道读取权限被拒绝。
2. **刷新模式存储被拒绝时请求页报错。** 新增模式在挂载和切换时直接读写 localStorage。现读取失败默认平稳更新，写入失败保留当前内存选择，沿用项目已有自动刷新设置约定。独立审查从当前源代码提取实际函数做 6 项无文件断言通过；主执行者在真实页面使该设置的读写同时抛错，页面正常挂载并可切换逐条插入，故障模拟已恢复。

复审补验使用新的隔离 SQLite 库。初次直接调整夹具用户权限后发现服务仍缓存此前所有者身份，因此该轮页面没有作为受限账号验收证据；重启清除缓存后重新查询真实 `me`，确认系统与项目 `isOwner=false`、scope 仅 `read_channels`／`read_requests`、无 `read_settings`，再完成页面验收。实际页面显示 `¥0.000000`，接口返回 `CNY`。此过程未修改产品的身份缓存逻辑。

复审新增／修订文件：`internal/objects/channel_usage.go`、`internal/server/biz/channel_usage.go`、`channel_usage_test.go`、`internal/server/gql/channel_usage.graphql`、生成的 `generated.go`；`frontend/src/features/channels/data/channel-usage.ts`、`schema.ts`、`components/channel-today-usage-cell.tsx`；`frontend/src/features/requests/components/requests-table.tsx`。原文件清单与其他实现不变。

追加检查：

```text
go test ./internal/server/biz ./internal/server/gql -run 'Test(ChannelTodayUsage|DashboardCosts)' -count=1
node --test src/hooks/animated-list.test.mjs src/hooks/use-auto-refresh-interval.test.mjs
```

两个 Go 包通过，列表／刷新 13 项通过；当前与 HEAD 覆盖基线的 TypeScript `noEmit` 均为 172 项，按文件／错误码计数没有新增；其中渠道 `schema.ts` 仍有原有 5 项诊断，其余三处复审前端文件无诊断。GraphQL 已按同源生成流程重新生成，未运行 lint/build。文档日期测试路径已修正，保留原轮次实际执行数而不追改成从未运行的结果。

实际补验截图（new）：[受限账号渠道人民币](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/review/channel-cny-reader.png)、[存储读写被拒绝仍可切换](C:/Users/xiaowei/.codex/visualizations/2026/10/30/01a18d80-9d45-7712-9566-d5bb0b12d8a7/axon-usage-20261031/review/request-storage-blocked.png)。

结论：原 15 项均有对应实现与验证依据；两处本轮缺陷已修复并独立复核，没有剩余阻断本阶段代码与本地功能验收的问题。前述浏览器强制 UTC、存量类型诊断及生产未验收边界继续保留，不能将其表述为所有环境无条件通过。独立审查已再次核对更新后的人民币截图并关闭证据缺口。复审临时页面与服务已关闭，8090／5173 端口无监听；存储故障注入已恢复。verification-cleanup 返回 `cleaned=true`、`task_root_removed=true`，清理 1920 项，保留既有 scratch 根目录与正式截图。
