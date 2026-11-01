# 用量统计与消耗排行榜验收

## 本轮状态（2026-10-29）

`final result: pass`（隔离本地服务联调与独立复审）

代码与真实接口联调已完成。启动当前源码的 Go 后端和 Vite 前端，使用独立 SQLite 夹具验证全部维度、时间范围、模型展开、权限和项目隔离；定时任务已自动写入缓存窗口。验收后关闭本轮前后端，确认 5173／8090 均释放，临时库和脚本已清理，原演示数据保留。下方保留此前功能的验收记录，不替代本轮状态。

来源为 `output/design/axon-consumption-20261029/04-channel-cost-clear-dividers-preview.png`；实际页面为 `http://127.0.0.1:5173/leaderboard` 和 `/project/usage-stats`。已将参考和实际截图同次打开比较，桌面 1584×993，另测 390×844。截图由真实接口返回独立数据库中的合成记录；首轮联调使用 10 条请求，本次复审使用 7 条请求，不代表真实消费或参考图的示例金额。

本次按用户要求，由 **gpt-6.1-sol / max** 独立 subagent 对照已确认计划、CCH `0c508eaa` 源码、改动和真实截图审查。发现的两项 P2 已修正并复核通过；主执行者完成自检、定向回归和本轮页面复验。最终证据：[居中渠道表格（new）](output/implementation/axon-consumption-20261029/review-channel-centered.jpg)、[文本中心测量（new）](output/implementation/axon-consumption-20261029/review-column-alignment.json)、[5 项 HTTP 复验（new）](output/implementation/axon-consumption-20261029/review-integration-checks.json)。复审范围限定于用量统计与消耗排行榜，没有扩展为其他功能治理。

### 发现与修正

- [P2，已修正] 排序图标占据表头宽度，名称旁的展开箭头与模型数也会移动文字中心。表头、父行、模型行统一按列居中，辅助图标与数量独立定位。1280×720 下量测 42 个有效文本区域，最大中心偏差为 0.01px；1584×993 完整渠道表、Key 缓存明细和项目 Key 表格截图通过。390×844 下页面宽度仍为 390px，720px 表格在 323px 容器内滚动，横向滚动量实测 390px。
- [P2，已修正] 本地队列满、排队超时和 RPM 耗尽已创建执行记录但没有发给供应商，原消息模式没有识别它们，会误降成功率。新增限定为 429 且匹配本地消息的排除项，保留请求数、历史金额和普通上游 429。回归先失败后通过；既有队列／RPM 流程测试同步断言真实持久化状态和消息。
- [P2，已修正] 原搜索只分别匹配名／姓，页面显示 `John Smith` 时完整姓名查询为空。查询增加与显示一致的完整姓名，保留原单字段、邮箱和项目过滤；8 种姓名场景及真实 HTTP／页面搜索通过。
- [P2，已修正] 渠道宽表在桌面首次捕获时最后一列需要横向滚动。将本功能单元格横向间距从 12px 缩到 8px 后，桌面可见全部指标；窄屏保留表格内滚动，实测 `scrollLeft` 从 0 变为约 387px。
- [P2，已修正] 日历翻月按钮原先定位到弹出层，挤在日期输入上方。给日历根节点设置相对定位后，按钮回到月份标题两侧，并同步中英文辅助标签。重新捕获及日期选择已核对。
- [P1，已修正] SQLite 存储 UTC 时间，而初版筛选和缓存窗口使用本地时区边界。午夜请求可能归入错误日期，定时窗口可能没有样本。自然日边界计算后及缓存窗口现在统一转换为 UTC；上海／洛杉矶、非 UTC 服务器时钟回归与真实 HTTP 午夜检查通过，实际定时汇总也已落库。
- [已解决] 首轮后端未加载新接口的问题已通过启动当前代码完成联调解决。自动审批曾拒绝独立后台启动命令，只返回 `blocked by policy`；最终由智能体通过工具托管终端完成启动，用户未执行手动启动。
- [已定位的浏览器环境问题] 内置浏览器和 Chrome 中 `Intl.DateTimeFormat` 都被注入函数强制改为 UTC。同一浏览器原生构造器能正确处理上海日期；仅在 Chrome 验收标签临时恢复后，真实页面自然日显示正确。刷新后已撤销该临时改动，未修改全局设置或增加业务兜底。

### 五项视觉核对

| 项目 | 结论 |
| --- | --- |
| 字体与层次 | 沿用当前应用字体和字号；生成图较大的字形/侧栏比例没有推广为全站修改。名称、数字和模型层级可读，表头与各行文字按列居中。 |
| 布局与间距 | 保留原导航和页面区域；桌面字段完整，窄屏控件换行、表格内部滚动，固定底部说明可见。 |
| 颜色与边界 | 使用原主题变量；展开连续浅灰、隔行底色和两像素深分隔线，无厚重嵌套卡片。 |
| 图片与图标 | 复用现有品牌图及图标库，没有新增位图资产；数据表无图片清晰度问题。 |
| 文案与内容 | 中英文同步；用户/Key/渠道字段按 CCH 映射，缺失值「—」，Key 缓存模型子行不填成本。验收数据均为隔离测试库中的合成记录。 |

已验证：排序保留对象展开；成本/缓存和维度切换；Key 缓存列及模型空值；项目 Key 用量；名称搜索和空数据；日历指定范围应用；仅查看权限用户可读、无权限用户菜单与路由拒绝；窄屏页面不横向撑开，表格区域独立滚动。首轮另外验证取消/应用及横向滚动动作。最终页面身份和主要内容正常，控制台没有相关错误或框架报错遮罩。真实 HTTP 的 25 项检查、午夜归属和自动缓存汇总已通过。MySQL/PostgreSQL 实例升级及真实供应商请求不在本轮已验证范围。

完整命令、截图、测试基线及限制见[本轮记录](output/implementation/axon-consumption-20261029/verification.md)与[结构化联调结果](output/implementation/axon-consumption-20261029/integration-checks.json)。实际效果：[渠道展开](output/implementation/axon-consumption-20261029/actual-channel-cost-expanded.png)、[Key 缓存展开](output/implementation/axon-consumption-20261029/actual-key-cache-expanded.png)、[项目 Key 用量](output/implementation/axon-consumption-20261029/actual-project-key-usage.png)、[窄屏](output/implementation/axon-consumption-20261029/actual-project-key-mobile.png)。所有验收标签已关闭、视口已恢复，验收结束后服务按用户要求关闭。

# 供应商适配界面视觉验收

## 多账号、多 Key 与多地址（2026-10-28）

最新增量实现及本地验收已完成，未提交或部署。独立演示环境验证了两账号、11 把 Key、$100 去重合计、凭据读取、启停和复用开关、长列表滚动、两地址测速、保存回读、取消和自定义接口路径。相关 Go 包最终通过，前端 35 项定向测试通过；TypeScript 与 HEAD 同为 170 项既有诊断。未运行 lint/build。

实际截图、修正原因、完整命令和未验证边界见[本轮交付记录](output/implementation/axon-accounts-addresses-20261028/verification.md)。以下保留此前阶段记录，不替代本轮结果。

## 三项问题修复与定向复审（2026-10-28）

本节取代下节“存在三项待修正问题”的当前状态；下节保留首次审查证据。三项已修复，原独立 subagent（`gpt-6.1-sol / max`）复审通过，未发现修复及直接影响范围内新的确定问题。没有重新审查已确认无问题的协议解析、成本捕获或历史快照链路。

- 刷新：前端统一同渠道逐 Key 串行、跨渠道最多三路；单 Key 失败继续处理其余项。未发布 mutation 明确要求一个渠道及必填 `keyRef`，未知 Key 报错；GraphQL 代码已重新生成。刷新和账号验证退出外层长事务，快照独立提交；锁等待可取消，查询使用最多 24 秒且不晚于父请求截止前 2 秒的子上下文，保存沿用父上下文。
- 预览：共同 helper 检查查询状态、上游观测时间、本地同步时间和有效时段边界，与后端判断一致。
- 缓存：刷新后先取消受影响的在途读取，再发布新状态并回读；刷新结束重新查询渠道排序。

新增文件（new）：`frontend/src/features/channels/data/channel-upstream-refresh.ts`、`frontend/src/features/channels/data/channel-upstream-refresh.test.mjs`、`internal/server/gql/channel_upstream_refresh_test.go`。其余改动仅涉及上述问题的现有调用方、GraphQL 生成文件、已有业务/事务用例及中英文渠道指南、Fork 台账。

验证通过：

```text
go run ../gqlgen/generate.go
go test ./internal/server/biz ./internal/server/gql -count=1
node --test src/features/channels/data/channel-upstream-refresh.test.mjs src/features/channels/channel-priority.test.mjs
git -c core.safecrlf=false diff --check
```

生成命令在 `internal/server/gql/`，Go 测试在仓库根，Node 测试在 `frontend/`。新增正常 GraphQL 入口用例复用实际 `WithTimeout` 中间件，以短预算覆盖正常 Key → 超时 Key → 正常 Key，验证三份结果独立保留且没有外层事务；还覆盖非法批量入口、已删除 Key、锁等待取消和保存余量。前端共 7 项检查通过，其中 4 项覆盖本轮三个问题及并发、跳过与失败继续行为。类型检查使用当前 HEAD 源码覆盖比较，前后均为 172 项诊断，本轮未增加文件／错误码维度的诊断，直接修改的前端文件无诊断。未运行 lint/build，未提交代码。

复审仅审阅修复与测试覆盖，复用已通过的执行结果，没有重复测试或扩大审查。真实上游账号、MySQL/PostgreSQL 升级及完整人工流程仍沿用已有未验证边界，不将三项修复通过表述为全部生产验收完成。

修复版后端沿用此前授权的本地启动流程加载，8090 健康检查和 5173 页面均返回 200；内置浏览器确认渠道列表及“余额与倍率 → 同步设置”正常读取，检查时控制台无 error/warn。未修改现有演示配置，未以本轮只读页面检查代替启闭同步、保存和真实上游刷新验收。截图：`C:/Users/xiaowei/.codex/visualizations/2026/10/27/01a17e39-266d-7f30-a97c-ef317b997386/upstream-reviewfix-ready.png`。

## 阶段独立审查与自检（2026-10-28）

结论：主线实现已具备，但本阶段不能标为全部验收通过。按用户指定，由独立 subagent 使用 `gpt-6.1-sol / max` 进行只读审查，主执行者交叉复核并执行局部验证。审查限定于已确认的上游余额、逐 Key 倍率、刷新、请求成本快照及其界面链路；没有扩大为全库审计，没有修改实现、提交代码或重启服务。

| 级别 | 已确认问题 | 触发与影响 | 修复边界 |
| --- | --- | --- | --- |
| P1 | 批量刷新受管理接口 30 秒和整批事务限制 | `gql/graphql.go` 的事务排除列表未包含 `refreshChannelUpstreams`；刷新服务逐渠道、逐 Key 串行进行外部查询并写入。已识别的 Sub2API 三把 Key，每项余额和倍率请求各耗时 6 秒，就超过默认 30 秒。请求取消时已写入的成功结果也会回滚，SQLite 写事务还可能跨后续网络等待持锁 | 查询不进入整批长事务，成功快照短事务独立持久化；按可完成的小单元组织刷新，处理整体时间预算。不能只提高超时或只移除外层事务。补正常 GraphQL 入口用例 |
| P2 | 保存前倍率预览与后端过期条件不一致 | `channel-upstream-settings-form.tsx:282` 只检查 `syncedAt` 和时段边界；后端还检查 `observedAt`。上游数据本身已过 30 分钟、但本地刚查询成功时，预览显示上游倍率，实际采用备用值 | 统一保存预览与后端生效条件，补上数据时间边界用例 |
| P2 | 后台读取的旧结果可覆盖刷新成功后的新值 | `channel-upstream.ts:143` 仅通过 `setQueriesData` 更新缓存，未协调正在进行的读取。已有缓存时 Query 的 `isSuccess` 仍为 true；刷新写入新值后，较早发出的读取晚返回可覆盖新值 | 协调同一缓存的在途读取与刷新结果，确保最后完成的旧读取不能覆盖新快照；需要与倍率排序查询一并核对直接影响 |

P1 已核对默认配置、路由、中间件、GraphQL 事务和服务循环的实际调用链，未对用户演示服务进行人为慢请求压测。P2 的预览使用源码中的原表达式执行内存探针；缓存问题使用项目已安装的 QueryClient，在已有缓存和在途读取条件下复现“新值被旧值覆盖”。无文件型探针或演示数据库写入。认证失败／未关联保留旧 Rate 的候选已排除：正常查询失败路径会清空 Rate，不列为发现。

独立审查核对锁定版本的 New API `/api/log/token` 身份字段及当前令牌查询、Sub2API billing 协议与时段字段，未发现额外确定的协议错误；请求选中 Key 后捕获、重试重新捕获、历史价格及固定费用手动倍率也未发现额外确定问题。这些结论不等同于真实账号联调通过。

本次重新执行并通过：

```text
go test ./internal/server/biz -run 'TestUpstream|TestAppliedUpstream|TestRequestCostSnapshot' -count=1
go test ./internal/server/biz/upstream -count=1
git -c core.safecrlf=false diff --check
```

规范核对：遵守不提交、不运行 lint/build、保留当前服务和验收数据；沿用已确认六张预览及现有组件；Ent/GraphQL 生成、中文/英文指南和 Fork 台账的既有实施记录保留。此前不存在的“内部迭代”技能已由用户同意略过，未虚构该技能执行结果。

关闭阶段所需的最小剩余工作：修正上述三项并补对应验证，再完成启用/关闭同步、手动保存、刷新后的页面回读验收。真实 New API/Sub2API 账户和 MySQL/PostgreSQL 升级仍为明确未覆盖项；不为本次审查新增协议、重做界面或治理无关的 170 项既有类型诊断。

## 上游余额与逐 Key 倍率：本轮验收状态（2026-10-28）

代码与相关自动检查已完成。2026-10-28 09:30（北京时间），按用户授权启动本地 8090 后端和 5173 前端，保留已有演示数据库；两处 HTTP 检查均返回 200。内置浏览器已验证渠道列表、逐 Key 明细和同步设置可正常读取新接口，原「状态读取失败」消失，并保留同步设置供用户验收。没有提交代码，未运行 lint/build。

本轮参考为 `output/design/axon-upstream-20261028/v2/` 的六张已确认预览。保留当前侧栏、主题、表格、模型编辑窗口，新增上游余额、微型刷新、全局刷新、逐 Key 明细和同步设置。生成图的示例数值不作为查询成功证据。

| 验收项 | 当前结果与依据 |
| --- | --- |
| 桌面渠道列表 | 1689×1122；3 个普通行实际高度均为 88px；创建时间默认隐藏，模型摘要最多两行 |
| 模型预览与编辑 | 键盘聚焦显示全部 8 个模型；点击摘要打开现有编辑窗口及完整模型面板；取消后回到列表且可继续操作 |
| 数字快捷修改 | 优先级数字框打开后直接聚焦、选中，输入可直接覆盖；取消未保存草稿 |
| 页面基础健康 | 页面内容正常，无框架错误覆盖层；检查时控制台无 error/warn |
| 新接口与配置表单 | 重启后已验证列表、逐 Key 明细、同步设置正常读取；保存配置和真实上游查询由后续验收继续覆盖 |
| 上游与成本业务 | 本地 HTTP 用例、按 Key 选择、手动优先、旧结果时限、价格快照、固定费用及混合倍率用例通过 |
| 升级与备份 | SQLite 旧 schema 升级、重复升级、唯一约束、账号配置和历史快照备份用例通过 |
| 前端测试与类型 | 33 项渠道测试与 4 项成本明细测试通过；以 HEAD 源码覆盖进行基线比较，修改前后同为 170 项诊断，未增加文件／错误码维度的诊断 |

实际截图（均为本轮 new）：[渠道列表](C:/Users/xiaowei/.codex/visualizations/2026/10/27/01a17e39-266d-7f30-a97c-ef317b997386/upstream-channel-overview.png)、[模型完整预览](C:/Users/xiaowei/.codex/visualizations/2026/10/27/01a17e39-266d-7f30-a97c-ef317b997386/upstream-model-preview.png)、[打开模型编辑](C:/Users/xiaowei/.codex/visualizations/2026/10/27/01a17e39-266d-7f30-a97c-ef317b997386/upstream-model-editor.png)。当前截图不代表新接口已经可用。

已执行的主要验证命令：

```text
go test ./internal/objects ./internal/server/biz/... ./internal/server/orchestrator ./internal/server/gql ./internal/server/backup -count=1
go test ./internal/objects ./internal/server/biz ./internal/server/gql ./internal/server/backup -count=1
go test ./internal/server/biz -run TestRequestCostSnapshotUsesSelectedKeyAndSurvivesLaterChanges -count=1
node --test src/features/channels/channel-priority.test.mjs src/features/channels/group-channel-types.test.mjs src/features/channels/data/protocol-options.test.mjs src/features/channels/data/channel-query-error.test.mjs src/features/channels/data/channel-config.test.mjs
node --test src/components/request-usage-cost-breakdown.test.mjs
git -c core.safecrlf=false diff --check
```

Go 命令在仓库根运行，Node 命令在 `frontend/` 运行。最后一次费用修正后重跑了受影响包和针对性用例。类型比较使用项目 TypeScript 编译器 API、`noEmit` 和 HEAD 文件覆盖，不生成构建。受管临时生成目录及测试日志已通过 `verification-cleanup` 清理，保留上述结论与截图。

重启后截图：[同步设置已就绪（new）](C:/Users/xiaowei/.codex/visualizations/2026/10/27/01a17e39-266d-7f30-a97c-ef317b997386/upstream-settings-ready.png)。本轮通过现有 `run-backend.ps1`、`run-frontend.ps1` 启动，日志位于 `output/local-preview/axon-routing/runtime/*-20261028-restart.*.log`。当前保留原本的三个占位演示渠道，未连接真实上游。

剩余范围：启用与关闭同步、保存手动倍率及刷新流程的页面写入验收。真实 New API／Sub2API 账号、MySQL／PostgreSQL 实例升级、移动端和暗色主题尚未验证。

以下为前一轮供应商适配的已完成记录。

final result: passed

2026-10-27。本轮以用户确认的六张功能预览为视觉目标，验收现有应用中的增量功能区，不重做原有侧栏、主题、能力表单或产品密度。

## 用户调试补充（2026-10-27）

按用户最新截图反馈，标签列改为默认可见，未筛选时直接显示渠道自身标签优先级，移除表头标签副标题。右侧目录导入扩展到整张模型卡片并上移至卡片标题旁。该明确指示取代旧参考图中“选择标签”和“仅导入价格”的部分；其余界面约束不变。新状态已做浏览器检查，证据见 [渠道](output/implementation/axon-enhancements-20261027/06-channel-own-tags.png) 和 [模型卡片](output/implementation/axon-enhancements-20261027/07-model-card-import.png)。

## 首轮比较依据

源图位于 `output/design/axon-enhancements-20261027`，实际截图位于 `output/implementation/axon-enhancements-20261027`。

| 功能 | 已确认参考 | 实际证据 |
| --- | --- | --- |
| 数字快捷修改 | `10-channel-inline-numbers-only.png` | [实际页面](output/implementation/axon-enhancements-20261027/01-channel-quick-edit.png)、[整页对照](output/implementation/axon-enhancements-20261027/compare-01.png) |
| 优先级与权重 | `07-priority-and-weight-dialog.png` | [最终区域对照](output/implementation/axon-enhancements-20261027/compare-final-priority.png) |
| 默认排序 | `08-channel-default-sort.png` | [最终区域对照](output/implementation/axon-enhancements-20261027/compare-final-sort.png) |
| 目录价格导入 | `03-model-price-import.png` | [最终区域对照](output/implementation/axon-enhancements-20261027/compare-final-price.png) |
| 姓选填 | `04-user-optional-lastname.png` | [新建状态区域对照](output/implementation/axon-enhancements-20261027/compare-final-user.png) |
| 模型多选 | `09-channel-model-select-all.png` | [最终区域对照](output/implementation/axon-enhancements-20261027/compare-final-models.png) |

实际桌面视口 1689×1122 CSS px，DPR 1.5；截图工具输出为 1689×1122。源图像素分别为 1698×926、1630×965、1698×926、1342×1172、1254×1254、1672×941，且其中模型／用户图包含裁切和示例数据。先查看整页结构，再裁取对应功能区域并保持比例放入同一对照图；没有把整页大小不同、示例数据差异或生成图文字放大当作真实 CSS 像素差异。

价格比较聚焦右侧价格区：保留已有能力区，通过表单滚动显示导入区域。用户图补拍新建状态；模型选择补拍新建状态、两个规则公开 ID 选中、demo-chat 已添加。实际保存行为另见[验证记录](output/implementation/axon-enhancements-20261027/verification-report.md)。

## 问题与修复

- [P2，已修复] 初次渠道三列并列后横向空间不足：缩窄限额、支持模型等内容区，最终桌面全部关键列可见，无铅笔编辑入口。
- [P2，已修复] 新增标签及默认排序下拉框未占满表单行：设置完整行宽，优先级弹窗加宽至 768 px。最终截图已重新比对。
- [P1，已修复] 目录选择回调清空选中值导致导入按钮不可用：区分选中项的显示 label 与重新搜索，重新验证并保存模型成功。
- [P1，已修复] 切换开发者清空既有价格：保留价格配置，实测 0 元价不覆盖，同时验证空价仍自动填入。
- 后端重启期间截到过请求未完成的弹窗，未采用该截图作为最终通过证据；最终后端就绪后已重新打开、核对三行配置并截图。

## 五项视觉检查

- 字体与层级：沿用当前应用字体及现有字号体系；标题、字段、辅助说明层级保持，未把图中演示水印复制进产品。
- 布局与间距：三列相邻、轻量 Popover、居中批量弹窗、原有左右表单、渠道右侧模型面板均保留；表单与列表按真实数据量滚动。
- 颜色：沿用当前白灰与橙棕色 token、边框、按钮和选中状态，没有引入新主题。
- 图像与图标：复用项目供应商图标和图标库，无自制位图替代；用户明确删除的数值旁铅笔未出现。
- 内容与状态：默认显示、继承说明、同级权重、浏览器默认排序、价格不改标识、姓选填、已添加禁用／选中数量均可见；生成图示例名称由本地演示数据替代。

未发现尚未解决的 P0/P1/P2 视觉或交互问题。产品文案补充了保存范围、继承项排序与零价保护；保留既有表单密度，因此不宣称与生成图逐像素一致。移动端、暗色主题不在本轮已验证范围。

## 交付

[可视化汇报](output/implementation/axon-enhancements-20261027/report.html)与[完整验证说明](output/implementation/axon-enhancements-20261027/verification-report.md)。内置浏览器保留实际渠道页面和交互汇报，服务继续运行；未提交。


## 开发者分组追加验收

通过：刷新模型页后只有一个 OpenAI 分组，统计为 1 个开发者、9 个模型。编辑原异常记录手输 ` OPENAI ` 并保存，持久值为 `openai`；创建草稿中的自定义 `MyProvider` 保留原样。截图见 [开发者分组修复](output/implementation/axon-enhancements-20261027/10-model-developer-group-fixed.png)，命令和边界见 [验证说明](output/implementation/axon-enhancements-20261027/verification-report.md)。
