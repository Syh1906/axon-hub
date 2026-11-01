# 用量统计与消耗排行榜实现记录

2026-10-29。代码实现与隔离本地服务联调已完成，尚未提交、构建、发布或部署。验收使用真实 Go 后端、Vite 前端和临时 SQLite 数据库，没有替换接口响应，也没有调用真实模型供应商。验收后已关闭本轮前后端，确认 5173／8090 均释放，并清理临时库、进程记录、脚本和日志；原有演示库保持不变。

## 本轮独立复审与修正

用户指出列文字错位后，重新开放视觉验收，不沿用前一轮“已通过”作为结论。按指定的 **gpt-6.1-sol / max** 执行独立只读审查；审查者核对已确认计划、CCH `0c508eaa` 实际源码、统计／采集／权限实现和新截图，主执行者另行自检。修复后的最终结论为：**本地隔离环境阶段复审通过，当前范围内未发现新增确定问题。** 没有扩展到其他功能或生产发布。

| 已确认目标 | 复审结论与边界 |
| --- | --- |
| CCH 字段与计算映射 | 父行／模型字段、缓存触发分母、整数系数、首字节速度、历史金额与最终请求去重均保留 |
| 采集与数据库升级 | HTTP/SSE、WebSocket 分片、Vertex 回调、旧字段 NULL 与 SQLite 升级证据已核对；MySQL/PostgreSQL 实例升级仍未验证 |
| 查询与只读权限 | 项目用量与全站榜单范围、六种日期、系统级 `read_leaderboard`、模型明细及缓存前权限检查符合计划 |
| 两处页面与视觉 | 用户／Key、用户／密钥／渠道、消耗／缓存、展开和排序符合计划；此次补齐表头／父行／模型文字居中 |
| 验收与维护 | 复用首轮 25 项 HTTP、日期、权限和自动缓存汇总证据；增加局部回归、5 项 HTTP 复验和最终截图；文档与 fork 台账同步 |

本轮发现并闭环的三项问题：

1. **列文字错位。** 原表头右侧排序图标占宽，名称与模型旁的箭头、数量也影响文字中心。`frontend/src/features/consumption/components/consumption-table.tsx` 将表头、父行和模型行按列居中，辅助图标与数量独立定位；保留连续灰底、隔行色和双像素分隔。1280×720 下 42 个有效文本区域最大中心偏差为 0.01px；1584×993 下完整渠道表、Key 缓存明细和项目 Key 用量均已截图。
2. **本地拒绝误降成功率（独立审查 P2）。** 本地队列满、排队超时、RPM 耗尽在向供应商发请求前失败，但执行记录已创建。`consumption_query.go` 限定 429 与这三种本地错误消息排除成功率样本，保留请求数和历史金额；普通上游 429 仍可计失败。新增回归先失败后通过，覆盖只有本地拒绝、成功混合本地拒绝、普通上游失败混合三种情况，并检查父行和模型行。既有队列超时／RPM 流程测试新增实际持久化的状态、429 与消息断言。
3. **完整姓名搜索漏匹配（独立审查 P2）。** 名／姓分列保存，原查询不能匹配页面显示的 `John Smith`。查询增加同样的完整姓名表达式，保留原姓名／邮箱条件及项目过滤。8 种姓名用例覆盖完整／片段／大小写、单名、空名和邮箱回退；SQLite 已执行，MySQL CONCAT 与 PostgreSQL 拼接表达式经源码复核，不声称真实实例已执行。

本轮复审库为新建的 7 条请求夹具：两个消费用户、两把 Key、两个禁用占位渠道；主力渠道含 3 次成功、3 种本地拒绝，显示 6 次请求、约 7.2 美元、100% 成功率。它与下文首轮 10 条请求夹具不同，均不代表真实消费。

本轮实际命令与结果：

```text
go test ./internal/server/biz -run 'TestConsumption(LocalAdmissionOutcomes|UserDisplayNameSearch)$' -count=1
go test ./internal/server/biz ./internal/server/orchestrator -run 'TestConsumption|TestChatCompletionOrchestrator_Process_(QueueRejection|RPMAdmission)' -count=1
node node_modules/prettier/bin/prettier.cjs --write src/features/consumption/components/consumption-table.tsx
git -c core.safecrlf=false diff --check
```

第一条在修复前复现两项缺陷；修复后第二条通过。前端仅格式化修改文件，没有运行 lint/build。本轮没有重跑全库 TypeScript；沿用既有 172 项诊断基线，JSX 已由 Vite 加载且真实页面复验无相关控制台 error/warn。

真实 HTTP 的 5 项复验通过，覆盖本地拒绝后的父／模型成功率及请求数／金额、三种姓名查询、Key 维度。浏览器验证排序后展开仍属于主力渠道、完整姓名搜索、Key 缓存明细、项目 Key 用量。390×844 下页面宽度 390px，720px 表格在 323px 容器内滚动，实测横向滚动量 390px。

本轮交付证据（new）：[渠道表格居中](review-channel-centered.jpg)、[Key 缓存居中](review-key-cache-centered.jpg)、[项目 Key 居中](review-project-key-centered.jpg)、[窄屏](review-key-cache-mobile.jpg)、[完整姓名搜索](review-full-name-search.jpg)、[中心位置测量](review-column-alignment.json)、[HTTP 复验](review-integration-checks.json)。独立审查复用了主执行者测试结果，没有重复执行全套测试。

复审收尾已完成：内置浏览器视口恢复、验收标签关闭；后端通过本轮终端句柄停止，前端核对本轮 PID／启动时间／程序路径后停止。`Get-NetTCPConnection` 确认 5173／8090 监听数为 0，本轮记录中的进程均已退出。受管目录 `C:/Users/xiaowei/.codex/verification-scratch/axon-consumption-review-20261029` 在列出全部内容、核对路径边界后使用原 cleanup 脚本清理，返回 `cleaned=true`、`task_root_removed=true`，保留预先存在的 scratch 根目录。没有修改原演示库。

## 首轮真实联调结果

- 临时库写入 10 条请求、11 次执行、8 条用量，覆盖两个项目、两个消费用户、三个 Key、两个渠道和五组日期；另有只读／无权限账户。测试数据均为合成值，渠道禁用且地址指向本机占位端口。
- 25 项真实 HTTP 检查全部通过：三维度 × 六种日期、父行／模型汇总、失败重试去重、成功率排除项、首字节速度、平均成本、缓存触发分母、搜索、项目范围和权限隔离。修复日期边界后重新执行仍通过。
- 新增午夜记录验证：上海今天对应 `2026-10-28T16:00:00Z` 到 `2026-10-29T16:00:00Z`；今天／昨天分别只返回应归属的请求，金额为 2／1。边界测试记录验证后删除，恢复基础夹具。
- 实际定时任务自动写入缓存窗口。主力渠道样本 4、有效样本 3、理论量 3000、读取量 1100，系数为 274；备用渠道系数为 200。页面按 0–1 显示并取两位小数，分别为 0.03／0.02。没有手工写入窗口结果。
- 内置浏览器验证日期快捷切换、自定义日期应用、排序后展开归属、Key 缓存明细、全站项目筛选、项目 Key 用量、名称搜索与空结果。仅有 `read_leaderboard` 的用户可以读取项目选项、榜单和模型；无权限用户菜单无入口，直接访问显示拒绝页面。
- 桌面 1584×993、窄屏 390×844；窄屏页面宽度仍为 390，720px 表格限制在 333px 滚动区域内。最终页面身份与内容正常，没有框架错误遮罩，检查时控制台没有相关 error／warn。

结构化结果见 [integration-checks.json（new）](integration-checks.json)。所有金额均为验收夹具，不代表真实供应商消费。

## 联调发现与修复

SQLite 中 Ent 保存 UTC 时间，初版日期筛选却直接使用系统时区边界，缓存汇总使用服务器本地时钟。文本比较把“今天凌晨”的 UTC 记录排除，并可能纳入次日记录；非 UTC 服务器的缓存窗口也读取不到样本。回归用例在修复前将预期金额 2 算为 3，缓存窗口预期 1 个、实际 0 个。

现已在 `consumptionRange` 完成自然日计算后将查询边界统一为 UTC，缓存时钟和写入窗口也统一为 UTC。`consumption_test.go` 新增上海／洛杉矶午夜记录和 UTC+8 服务器时钟用例，并使测试夹具按实际存储契约保存 UTC。定向 Go 测试、真实 HTTP 午夜检查和真实定时汇总均已通过。

另一个显示异常来自浏览器环境：内置浏览器和 Chrome 中的 `Intl.DateTimeFormat` 都被注入函数改写，强制使用 `Date.prefs[0]` 的 UTC，覆盖应用传入的 `Asia/Shanghai`。同一浏览器的原生格式器返回正确日期。仅在 Chrome 验收标签临时恢复原生构造器后，页面正确显示 `10/29/2026 — 10/29/2026`；随后刷新撤销，并确认原环境已恢复。没有为该环境注入问题修改业务代码或全局浏览器设置。

## 已实现

- 项目用量统计增加用户／Key 切换，与全站排行榜共用六种时间筛选。
- 管理区新增用户／密钥／渠道消耗榜、缓存榜，展开模型明细；仅系统级 `read_leaderboard` 查看权限。
- CCH 固定版本 `0c508eaa` 的缓存样本分母、整数缓存系数、首字节输出速度和结果分类；保留 AxonHub 的历史成本和最终渠道归属，重试不重复计数。
- 首字节采集覆盖 HTTP/SSE、Responses WebSocket 的消息分片及 Vertex 转换请求。它与首个有效输出时间分开保存。
- Ent 采样列和缓存窗口表、每 5 分钟的内部汇总、旧记录空值处理、查询缓存和范围隔离。
- 展开区采用连续浅灰底、隔行底色及更深的双像素分隔线。窄屏在表格内横向滚动。

主要新文件（new）：

| 职责 | 文件 |
| --- | --- |
| 页面、日期和表格 | `frontend/src/features/consumption/`、`frontend/src/routes/_authenticated/leaderboard/index.tsx`、中英文 `consumption.json` |
| 查询、权限、分组和窗口 | `internal/server/biz/consumption*.go` |
| 采样与 CCH 固定夹具 | `internal/pkg/usageanalytics/` |
| GraphQL | `internal/server/gql/consumption.graphql`、`consumption.resolvers.go`、`consumption_test.go` |
| Ent 定义 | `internal/ent/schema/cache_effectiveness_window.go`；相关 Ent 文件为生成结果 |
| 首字节 | `internal/server/biz/first_byte.go`、`llm/httpclient/first_byte.go`；HTTP、WebSocket、Vertex 和请求生命周期同步接入 |
| 说明 | [中文统计契约](../../../docs/zh/development/consumption-statistics.md)、[英文统计契约](../../../docs/en/development/consumption-statistics.md) |

同时更新已有用量统计入口、侧栏、路由权限、scope 列表、请求执行字段、FX 注入、中英文成本追踪指南和 fork 维护台账。未修改发布版本或已发布迁移。

## 检查结果

| 检查 | 结果 |
| --- | --- |
| CCH 原实现生成的协议夹具 | 通过；验证摘要、规范化字节估算、中文／工具／多模态 |
| 统计与 GraphQL 测试 | 通过；缓存分母、最终请求去重、模型/TTL 汇总、日期边界、权限和项目隔离 |
| SQLite 旧 schema 升级 | 通过；旧数据保留、新字段 NULL、重复升级、窗口唯一键 |
| 首字节与直接调用 | 通过；先有字节、后有完整 SSE／WebSocket 事件；Vertex 不丢失观察回调 |
| TypeScript | HEAD 与当前均 172 项诊断，新增 0；全库检查仍未通过 |
| 差异空白检查 | `git -c core.safecrlf=false diff --check` 通过 |
| 浏览器交互 | 真实接口已验证维度/指标切换、展开、排序后展开归属、搜索、空数据、日历选择及项目筛选；首轮另外验证取消/应用及横向滚动；最终控制台无相关错误 |
| 实际后端联调 | 通过；25 项 HTTP 检查、UTC 午夜归属、自动缓存窗口落库及权限页面检查完成 |

新数据库列由 Ent 托管，已对照直接上游 `v1.0.0-beta10-fork.7`／`b205ae8e`，无同名 schema 或 data migration 冲突。不新增数据迁移版本。MySQL／PostgreSQL 的真实实例升级未执行。

Windows 上既有 `TestWebSocketStreamReturnsErrorWhenReusedConnectionClosesBeforeEvent` 失败：预期关闭消息，实际收到 `wsarecv` 连接被本机中止错误。使用 Go overlay 将 `websocket_executor.go` 替换为 HEAD 原文件后，同一用例 5 次均得到相同失败；本轮没有修改这项既有错误分类或测试断言。其他被选中的 WebSocket 检查以及本次新增采集测试通过。

## 实际命令

根目录执行：

```text
go generate ./internal/ent
go test ./internal/pkg/usageanalytics -count=1
go test ./internal/server/biz ./internal/server/orchestrator ./internal/server/gql ./internal/scopes -run 'Test.*(Consumption|RequestExecution|Performance|OutboundPersistent)|TestExtractOutboundReasoningEffort|TestScope' -count=1
go test ./internal/server/biz ./internal/server/gql -run 'TestConsumption' -count=1
git -c core.safecrlf=false diff --check
```

Ent 生成实际入口是仓库 `Makefile` 的原有 `go generate` 命令；本机没有 `make`，使用原生成器，没有更换工具链。GraphQL 在 `internal/server/gql/` 执行 `go generate`。没有运行 lint/build；本地服务按用户授权启动，UTC 修复后重新加载后端，验收后关闭。

`llm/` 执行：

```text
go test ./httpclient -count=1
go test ./httpclient ./vertex ./transformer/openai/responses -run 'TestFirstByte|TestHTTPFirstByte|TestTransformRequestPreservesFirstByteObserver|TestWebSocketFirstByte' -count=1 -v
go test ./httpclient ./vertex ./transformer/openai/responses -run 'TestFirstByte|TestHTTPFirstByte|TestTransformRequestPreservesFirstByteObserver|TestWebSocket|TestPrepareHTTP' -count=1
```

第三条扩大检查时出现上述既有 Windows 用例失败；随后以 HEAD overlay 单独复测确认。

前端使用已安装的 TypeScript、Prettier：`node node_modules/typescript/bin/tsc --noEmit -p tsconfig.app.json --pretty false`；仅格式化本轮新增/修改组件。类型基线通过 TypeScript compiler host 在内存中读取 HEAD 对应文件，未重置工作区，比较得到 `baseline=172, current=172, newDiagnostics=[]`。最初 `pnpm exec` 因本机启动器触发安装检查而停止，改用同一项目已安装的编译器；没有安装或更换依赖。

两轮验证临时目录均通过 verification-cleanup 的归属与路径检查并清理，结果为 `cleaned=true`、`task_root_removed=true`；保留预先存在的 scratch 根目录。截图、正式测试夹具和本文为交付材料，不在临时清理范围内。

前端实际通过工具托管终端执行 `node node_modules/vite/bin/vite.js --host 127.0.0.1 --port 5173 --strictPort`，进程级 `VITE_API_URL` 指向本机 8090。后端复用本地预览脚本的启动方式，将运行目录限定到受管临时目录并执行 `go run github.com/looplj/axonhub/cmd/axonhub`。没有新增系统服务或启动项。最初独立后台启动前端的命令被自动审批拒绝，只返回 `blocked by policy`；工具托管终端启动成功，未让用户代为操作，也未更改执行权限。

仓库 `AGENTS.md` 及前后端细则已统一：授权推进包含必要本地联调，验收后关闭本轮服务。内部 `axon-frontend-iteration` skill 同步记录内置浏览器优先、服务句柄与结束清理、失败后先核对原因和范围。`quick_validate.py` 以 Python UTF-8 模式执行通过；`git check-ignore -v` 确认该内部 skill 继续被忽略。

## 页面证据与限制

以 Codex 内置浏览器为主要验收入口，访问 `http://127.0.0.1:5173`；Chrome 仅补充权限账户和时区复核。下列实际页面由临时数据库和真实后端驱动，区别于首轮替换查询响应的界面截图。所有临时浏览器改动已撤销、视口已恢复、验收标签已关闭。

- [实际渠道消耗榜展开（new）](actual-channel-cost-expanded.png)
- [实际 Key 缓存榜展开（new）](actual-key-cache-expanded.png)
- [实际项目 Key 用量（new）](actual-project-key-usage.png)
- [实际窄屏页面（new）](actual-project-key-mobile.png)
- [仅查看权限账户（new）](actual-readonly-user.png)
- [无权限直接访问（new）](actual-permission-denied.png)
- [临时恢复原生格式器后的时区显示（new）](chrome-native-timezone.png)

首轮界面测试证据保留：

- [渠道消耗榜展开（测试数据）](channel-cost-expanded.png)
- [Key 缓存榜展开（测试数据）](key-cache-expanded.png)
- [项目 Key 用量（测试数据）](project-key-usage.png)
- [窄屏横向滚动（测试数据）](project-key-usage-mobile.png)

首次联调前的旧接口报错及停止服务状态已由后续真实服务验收解决。最终使用 `Get-NetTCPConnection -State Listen` 只读核对 5173／8090，确认验收服务退出后均未监听。

已与[第四版参考图](../../design/axon-consumption-20261029/04-channel-cost-clear-dividers-preview.png)同次打开比较。保留实际应用字体、主题和原生控件，重点核对连续分组、模型缩进和行分隔，不将生成图的放大字体推广到其他页面。详细状态见根目录 [design-qa.md](../../../design-qa.md)。

日期输入的浏览器 `fill` 曾只改变 DOM 值而未触发 React 状态，后续改用实际日历按钮完成了起止日期选择；未将该次输入尝试记为校验通过。真实自定义范围 `2026-10-27` 到 `2026-10-28` 返回两条请求、合计 11 美元。

仍未覆盖 MySQL／PostgreSQL 实例升级、真实供应商请求和生产升级；本次完成的是本地隔离环境的功能验收。原演示库仍为 0 条请求、没有本轮新缓存表，未被本次隔离联调升级或填入测试数据。
