# API 密钥分组实现与验收

2026-10-29。本地代码与联调完成，未提交、推送、构建、发布或部署。使用独立 SQLite 数据库、合成账户和三把测试密钥，没有调用真实模型供应商。使用方法与取舍见[中文指南](../../../docs/zh/guides/self-usage.md)及[英文指南](../../../docs/en/guides/self-usage.md)。

## 修复后的定向复审（最新结论）

用户再次要求复审后的最终核对：**通过，未发现遗留可行动问题**。gpt-6.1-sol / max 独立审查者再次只读核对最后的日志范围同步补丁，确认前端关键代码与上次通过版本逐字一致，后端、GraphQL、空页身份、共享刷新版本及回归覆盖保持正确；并核对实际截图与报告一致。主执行者同步自检，定向 `diff --check` 通过。本次未修改业务代码，复用此前通过的测试和真实页面结果，没有重跑浏览器或复审其他已通过范围。下文发现的问题均为已修复的历史记录，并非当前待修复项。

依用户要求，本轮只复查此前四项问题的修复及直接影响，未重审已确认通过的权限、迁移、备份等范围。独立审查者仍为 **gpt-6.1-sol / max**。

复审确认全站名称辨识、失效默认选项、请求详情缓存三项修复通过；在自助用量移组路径发现一个遗漏：只改变日志状态、模型或页码会刷新日志，却不刷新汇总，出现旧组汇总与新组日志混用。现已修复并再次通过独立代码复审：Stats 和 Requests 均返回本次查询捕获的 `scopeID`、密钥及分组名称；空日志页也返回身份。前端发现日志与汇总不一致时暂不展示该日志，并同步重取两类数据，保留日志筛选和页码。使用稳定分组 ID 区分同名组，不新增轮询。新增 `scope.ts` 与 `scope.test.mjs`；其余修改限于自助业务、GraphQL、直接前端调用及对应测试和文档。

本轮定向验证：

- `go test ./internal/server/biz -run 'TestSelfUsage' -count=1` 与 `go test ./internal/server/gql/selfusage -count=1` 通过。补充状态筛选、模型与空第二页返回当前范围身份的断言。
- `node --test src/features/self-usage/scope.test.mjs` 新增 **1 项通过**，覆盖稳定身份、同名不同组、改名与单密钥范围；此前 12 项结果复用，未重复运行无关测试。
- `node node_modules/typescript/bin/tsc --noEmit --pretty false -p tsconfig.app.json` 当前仍有 172 项既有诊断，`features/self-usage/` 无诊断。未执行 lint/build。`git -c core.safecrlf=false diff --check` 通过。
- 新隔离 SQLite 库中，设计组两把合成 Key 共 2 次请求、成本 3。后台将当前 Key 移入研发组后，只把日志状态从“全部状态”改为“成功”，未改日期或范围、未刷新页面。标题、组按钮、汇总、模型统计和日志均自动更新为研发组，1 次请求、成本 1，成功筛选保留。真实截图（new）：[仅切日志状态后的范围同步](review-log-only-reassignment.jpg)。
- 页面未出现相关业务异常；浏览器日志记录了 Electron 宿主 sandbox 脚本错误，未将其视为应用回归或扩大排查。MySQL／PostgreSQL 实例升级与生产部署仍未验证。

结论：本轮出现过的问题及本次补丁直接影响范围内，未发现遗留可行动缺陷。独立代码复审与主执行者真实页面自检均通过，不代表全库审查。前后端及本轮浏览器标签已关闭，8090／5173 无监听；受管目录 `axon-key-groups-log-review-20261029` 清理返回 `cleaned=true`、`task_root_removed=true`，保留原有 scratch 根目录。

## 阶段独立复审（上一轮记录）

用户指定的 **gpt-6.1-sol / max** subagent 已独立只读复核计划目标、适用项目规范、代码、四张设计图及实际截图；主执行者完成自检和修复后的真实页面复验。审查范围限于本阶段分组能力及直接影响，没有扩大到用户体系重构、全库治理或发布。

初审确认四项直接问题，现均已闭环：

1. **全站同名分组无法辨别。** 仅全站范围的分组行和选项追加项目名；仍按分组 ID 聚合，单项目保持原名。新增回归验证两个项目同名组的独立金额和标签。
2. **删除默认组后存在不可见的筛选 ID。** 默认弹窗增加可移除的已选项，失效 ID 显示“不可用分组 #ID”；保存前保留原过滤条件，避免静默扩大查询。新增测试覆盖失效项展示数据、显式移除和持久化。
3. **已登录自助页移组后仍显示旧组标题。** Stats 附带本次查询使用的密钥／分组名称，页面随结果更新；统计和请求不保留非活动范围缓存，切换时重新取数，无新增轮询。本轮进一步改为共享范围版本同步查询，详见上节。移出分组时整组查询仍拒绝，用户可切回单密钥，成功后组入口消失。
4. **请求详情未随分组变动刷新。** 原失效列表仅包含 requests，现同步包含实际详情前缀 request，避免详情短暂沿用旧组名。

最终独立审查结论：本阶段业务复核通过，当前范围未发现遗留可行动缺陷；三张复验截图已由独立审查者再次核对。

当前验证：相关四个 Go 包通过；自助包中一条固定夹具旧断言只期待 Key 名，已改为要求“allowed · Self Usage”，整包复跑通过。前端行为测试 **12 项通过**（默认分组 4 项）；TypeScript 基线／当前均 **172 项**、逐项新增 **0**。未执行 lint/build。

真实页面使用新隔离 SQLite 库中的两个项目、三个分组、三把合成密钥：

- 全站两个“设计组”分别显示“设计组 · Default”和“设计组 · 第二项目”。
- 自助页先显示 A 组 2 次请求、成本 3；后台将当前登录 Key 移到 B 后，不刷新页面，仅切日期，标题、组按钮和日志同步显示 B，变为 1 次请求、成本 1。随后取消分组，单密钥仍可查看，组入口消失。
- 删除已保存默认组 A 后仍精确过滤；弹窗显示不可用 #1，手动移除并保存后恢复列表中的两把无组 Key。
- 请求详情缓存前缀由代码审查确认；没有将该项声明为单独的浏览器缓存竞态测试。

复审证据（new）：[跨项目同名分组](review-group-projects.jpg)、[登录期间移组后](review-self-usage-moved.jpg)、[失效默认分组](review-removed-default.jpg)。原有 personal Key 仅本人可修改的规则保留；没有为分组功能扩大管理员接管权限。MySQL／PostgreSQL 真实升级与生产部署仍未验证，TypeScript 既有诊断仍存在。

## 已实现

- 项目内分组创建、改名、删除，创建／编辑密钥时选择分组；默认无分组。删除分组只清空成员归属。
- 密钥列表支持多选分组和无分组筛选；默认筛选按用户、项目保存在当前浏览器，首次查询即应用。取消不保存，临时重置不覆盖默认值。选项缓存五分钟，修改后刷新相关密钥、请求、统计查询。
- 项目用量和排行榜新增分组维度，可查看指定分组及其密钥；无分组不计入分组统计，筛选候选来自当前统计范围内有用量的分组。
- 自助页凭组内有效 user/personal Key 切换单密钥／整组，覆盖汇总、趋势、模型和日志。每次组查询重新核实归属并限定同项目；不能指定任意组编号。由于赋组会开放整组用量，只有项目管理员能修改归属。
- 请求调用方、密钥选择项、列表、弹窗和统计统一显示密钥名称加分组；无分组回退用户。密钥备份按项目和分组名称恢复关联；无成员的空分组不包含在密钥备份中。

## 历史归属

采用当前归属：密钥从 A 移到 B 后，保留的历史用量全部计入 B；取消分组后退出分组汇总。只在每把密钥保存一个可空编号，不在大量请求和用量记录重复保存分组，不在移组时重写历史，因此空间与写入开销更小。代价是不能还原请求发生时的旧分组。历史金额、用户归属、路由和密钥权限保持原有规则。

统计缓存加入密钥及分组更新时间，移组／改名后不复用旧结果；鉴权缓存前缀升级到 v3。GraphQL 同次操作复用分组名称查询，统计列表批量读取名称。

## 验证

| 检查 | 结果 |
| --- | --- |
| 业务、GraphQL、自助 GraphQL、备份包 | 全部通过 |
| 分组回归 | 聚合、无分组排除、跨项目隔离、非管理员拒绝、删除清空成员、备份编号重映射通过 |
| SQLite 升级 | 旧 Key 保持无分组、旧数据保留、重复升级、项目内名称唯一通过 |
| 前端行为测试 | 12 项通过，含默认缓存隔离、损坏缓存、筛选与自助入口 |
| TypeScript | 基线 172 项、当前 172 项，逐项比较新增 0；全库检查仍未通过 |
| 真实 HTTP | 10 项通过，见 [结果](http-checks.json) |
| 浏览器 | 创建分组、创建赋组、编辑移组及回读、默认值保存／刷新／取消／重置、单 Key／整组切换、分组榜／模型展开／指定分组／项目统计及请求调用方通过 |
| 布局 | 1280×720 桌面与 390×844 窄屏；修复新增按钮挤压标题。窄屏弹窗宽 358px、左边距 16px，按钮可用 |
| 差异 | diff --check 通过；修改涉及的手写源文件均不超过 2200 行 |

合成夹具：组内两把 Key 各一次请求，成本分别为 1、2；无分组 Key 成本 3。单密钥结果为 1 次／120 Token／成本 1，整组为 2 次／240 Token／成本 3。移动第二把 Key 后，两组分别为成本 1 和 2；改名后缓存立即返回新名称。

请求页和最终密钥页控制台无相关错误或警告。自助页图表挂载时有 Recharts 初始尺寸 -1 警告，随后图表正常显示；不声称完全无警告。开发环境更新检查有 GitHub 403，分组接口正常。截图日期显示受浏览器既有时间格式环境影响，本任务未修改日期算法。

## 命令与限制

```text
# internal/server/gql、internal/server/gql/selfusage 分别执行
 go generate
# 仓库根目录
 go test ./internal/server/biz ./internal/server/gql ./internal/server/gql/selfusage ./internal/server/backup -count=1
 go test ./internal/server/biz ./internal/server/gql/selfusage -run 'Test.*(SelfUsage|APIKeyGroup|ConsumptionAPIKeyGroups)' -count=1
 git -c core.safecrlf=false diff --check
# frontend
 node --test src/features/apikeys/default-groups.test.mjs src/features/self-usage/*.test.mjs
```

用已安装 TypeScript 编译器执行 noEmit，并以内存文件覆盖读取 HEAD 作同环境基线。没有运行 lint/build 或重装依赖。开发后端在受管临时模块执行 `go run github.com/looplj/axonhub/cmd/axonhub`；前端执行 `node node_modules/vite/bin/vite.js --host 127.0.0.1 --port 5173 --strictPort`。

Ent 托管 schema，已核对直接上游 `v1.0.0-beta10-fork.7`／`b205ae8e` 无同名定义；没有新增 data migration 版本。MySQL／PostgreSQL 真实实例升级、生产部署未验证。

## 主要文件

- new：`internal/ent/schema/api_key_group.go`、`internal/server/biz/api_key_group.go`、`api_key_labels.go`、分组与升级测试；对应 Ent 文件为生成结果。
- new：`internal/server/gql/api_key_group.graphql`、`api_key_group.resolvers.go`、`api_key_identity.go`。
- new：`frontend/src/features/apikeys/components/key-group-controls.tsx`、`key-group-field.tsx`、`data/groups.ts`、`data/default-groups.ts`、默认筛选测试及 `frontend/src/components/api-key-identity.tsx`。
- 同步：统计、自助、备份恢复、请求展示、密钥弹窗、中英文指南及 Fork 台账。

## 实际截图与收尾

[密钥管理](key-management.jpg)、[窄屏密钥页](key-management-mobile.jpg)、[默认筛选](default-group-filter.jpg)、[窄屏默认筛选](default-group-mobile.jpg)、[分组排行榜](group-leaderboard.jpg)、[分组自助用量](group-self-usage.jpg)、[请求调用方](request-callers.jpg)。这些均为真实接口页面截图，设计图另存于 output/design。

本轮前后端已停止，8090／5173 监听已释放，临时标签关闭，设备尺寸覆盖撤销。verification-cleanup 返回 `cleaned=true`、`task_root_removed=true`；保留原有 scratch 根目录。用户原有 `design-qa.md` 和其他 output 内容保持不变。

本轮独立复审收尾：前后端和浏览器标签已关闭，8090／5173 均无监听；受管目录 `axon-key-groups-review-20261029` 清理返回 `cleaned=true`、`task_root_removed=true`，保留预先存在的 scratch 根目录。
