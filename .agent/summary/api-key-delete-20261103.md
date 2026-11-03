# API 密钥永久删除：关联处理与验收

状态：实现和浏览器联调完成，2026-11-03；本轮独立复审发现的四处局部问题已修复并补验，独立最终复核完成：本轮范围内未发现未修复的阻断缺陷，可以交付。未发版或部署；源码提交与推送以 Git 记录为准。

## 删除契约及原因

API 密钥页增加“永久删除”；密钥分组成员行可直接删除，批量管理的密钥模式可删除选中项。共用确认弹窗，列出名称和稳定 ID；取消不写入，提交期间禁用重复点击，失败保留选择，不自动重试。

这里永久删除的是可认证的凭据。数据库保留带删除标记的历史记录，而不是物理移除整行：现有请求通过密钥 ID 关联，分组统计按密钥 ID 取历史数据，额度账本还可能有未完成请求。直接移除整行或只在列表隐藏，会分别造成关联断裂或认证仍可使用。

凭据原文替换为 `deleted:<原记录ID>:<原缓存索引>`，状态停用、设置 `deleted_at`，清空权限和 IP 列表。保留名称、创建人、项目、分组、历史配置等统计所需元数据；普通查询和更新无法访问或恢复删除记录。保留历史配置，使按模板筛选既有用量仍可定位原密钥；模板发布仍只更新未删除密钥。

| 关联对象 | 删除时的处理与影响 |
| --- | --- |
| 认证及缓存 | 提交成功后同步清本机缓存、通知其他实例；定期刷新使用删除记录中的原缓存索引淘汰旧值，不保存原文。认证已有的数据库归属检查也会拒绝已删除记录。 |
| 普通列表、归档、编辑、轮换 | 删除项从普通列表和归档列表消失。数据库写入增加未删除条件，阻止删除前打开的编辑或轮换操作把凭据写回。名称可重新使用。 |
| 请求、执行、用量与归档统计 | 保留请求和账目，不断开密钥 ID。仅历史查询包含删除身份；不放开项目权限及个人密钥可见性。请求日志可以继续显示原名称。 |
| 分组、排行榜、仪表板、分析 | 成员数只计算当前有效成员，历史统计仍包含删除密钥的消费。管理端及自助分组用量均保留原历史归属。 |
| 额度、暂占、结算 | 不删除预算账户、扣费记录和历史账目，不退还历史消费。新请求不能再申请额度；已受理请求可完成，暂占及实际费用继续结算到原密钥和分组。 |
| 权限与原子性 | 明确目标项目并验证 `write_api_keys`；系统免认证密钥不可删，个人密钥仅创建者可删。一次选择全部在同一事务处理，任一项不存在、越权或存储失败即整批回滚。按项目、密钥预算、密钥记录的顺序加锁。 |
| 外层 GraphQL 事务 | 缓存清理挂在最终提交之后；外层事务回滚时，密钥和缓存都保持原状态。 |
| 备份、恢复 | 当前格式已有 `deleted_at`，不新增格式版本。历史请求的密钥身份按每批 500 条统一补齐，不逐请求查询。新增删除身份参与请求、用量和预算映射；恢复后仍保持删除状态，不能启用。旧式软删除且仍含原文的记录不因此被纳入导出。 |

删除不会改写已经存在的旧备份或数据库快照；本功能不执行历史备份清除。删除分组本身仍沿用原来的“未删除成员移入未分组”行为，不级联删除密钥；已删除密钥的历史身份不再编辑，分组本身删除后不再提供该分组的独立用量入口。

## 实现入口

- `internal/server/biz/api_key_delete.go`（new）：统一删除、锁顺序、凭据替换、提交后缓存清理。
- `internal/ent/schema/api_key.go`：删除后更新保护；`request.go`：历史请求密钥解析。
- `internal/server/biz/{managed_group_usage,self_usage,consumption,consumption_query}.go`：保留历史汇总。
- `internal/server/gql/{api_key_group.graphql,api_key_group.resolvers.go,ent.resolvers.go,analytics_helpers.go,dashboard.resolvers.go}`：删除接口和历史身份读取。
- `internal/server/backup/api_key_history.go`（new）及备份、恢复、预算处理：保留不可认证身份与关联映射。
- `frontend/src/features/apikeys/components/delete-keys-dialog.tsx`（new）：两处共用确认弹窗；对应菜单、分组成员行、批量工具栏与查询缓存同步。
- 中英文文案位于 `frontend/src/locales/{en,zh-CN}/apikeys.json`。

Ent 的改动只涉及写入钩子和 GraphQL 注解，没有新增数据库列或索引，不需要 schema/data migration，不修改已发布迁移。生成代码已同步。

## 自动检查及结果

按实际影响执行下列定向检查，Go 使用 `GOMAXPROCS=2`、`-p 1`；未执行 lint、build、全库测试或全量前端类型检查。

```text
make generate
# 本机没有 make，实际执行其相同入口：
cd internal/server/gql && go generate

go test -p 1 ./internal/server/biz -run '^TestDeleteAPIKeys' -count=1
go test -p 1 ./internal/server/backup -run 'TestDeletedAPIKeyBackup|TestBackupService_Backup_WithAPIKeys|TestBudgetBackupRestoresBalancesAndDetachedHistory' -count=1
go test -p 1 ./internal/server/gql -run 'TestDeleteAPIKeysGraphQL|TestManagedGroupUsageRequestsSupportsRequestLogFields' -count=1
go test -p 1 ./internal/server/biz -run 'TestAPIKeyService_NameUniqueness|TestAPIKeyGroup|TestKeyManagementAtomicRowsAndReplay' -count=1
```

上述最终结果均通过。补充仪表板统计断言后，单独重跑 `go test -p 1 ./internal/server/gql -run '^TestDeleteAPIKeysGraphQLAndHistoricalRequest$' -count=1` 也通过，验证删除前后请求数、Token 和费用排名一致。新增用例覆盖凭据不可认证与不可恢复、历史请求/用量/排行榜保留、在途结算不重复扣费、跨项目/他人个人密钥/系统密钥/缺失 ID 整批回滚、真实项目写权限、外层回滚、丢失通知后的缓存刷新，以及备份恢复后的关联和删除状态。

七个变动前端模块通过 TypeScript 转译语法检查，中英文新增文案键一致；这不是全量类型检查。使用项目现有 Prettier 格式化变动前端文件，`git diff --check` 通过。`pnpm exec` 曾触发依赖重装检查并中止，之后直接调用已安装的相同工具，没有重装依赖或修改锁文件。

## 真实浏览器联调

内置浏览器连接前端 `127.0.0.1:15173` 与后端 `127.0.0.1:18093`，使用受管临时目录中的独立 SQLite、两个示例分组和 15 个合成密钥，不使用生产数据或供应商请求。

- 分组批量删除 2 条前取消：成员仍为 13；重新确认后变为 11，两条消失，选择清零。
- 第二页剩余 1 条逐条删除：成员变为 10，自动回到第一页，展示 10 条，不停留在空页。
- API 密钥菜单可见“永久删除”，共用弹窗显示名称及 ID；确认后该条消失。
- 返回分组页，另一分组成员由 2 变为 1，证明两个页面使用真实接口和刷新后的数据。
- 页面可重复导航、关闭和重新打开弹窗；最终控制台无相关错误。最初前端先于后端启动产生连接失败，后端就绪后重载已恢复。

实际截图（不是生成预览）：

- [分组批量确认](../../output/implementation/axon-key-delete-20261103/01-group-bulk-confirm.png)
- [API 密钥确认](../../output/implementation/axon-key-delete-20261103/02-api-key-confirm.png)
- [删除后分组回读](../../output/implementation/axon-key-delete-20261103/03-delete-readback.png)

## 限制与清理

尚未在 MySQL、PostgreSQL、TiDB 实例或真实多实例 Redis 环境验证；跨实例缓存补偿用独立内存 watcher 和实际缓存刷新覆盖。没有穷举移动设备或执行收费供应商请求。

本轮内置浏览器标签、前后端服务和子进程已关闭，18093/15173 端口已释放，未结束用户原有服务。受管清理脚本返回 `cleaned=true`、`task_root_removed=true`，本轮临时数据库、日志及脚本已清理；原有 scratch 根目录保留，交付截图和本记录保留。工作区原有的费用字段及发布记录修改保留。


## 独立复审与主执行者自检（2026-11-03）

用户明确指定 `gpt-6.1-sol / max` 独立 subagent 审查，并要求主执行者自检、遵守内部迭代规范且不扩大主线。独立执行者先只读核对既定目标、关联处理与差异；主执行者复现和修复，仅补验受影响部分。

### 预览与授权来源

本阶段使用仓库 2026-10-30 的真实验收截图作增量设计，明确告知本地服务未运行、没有将历史截图称为本轮实测图。最终预览为 [永久删除设计图](../../output/design/axon-key-delete-20261103/01-key-delete-preview.png)，由 openai-compatible-imagegen 的内置 ChatGPT 路线生成并交付。用户在原对话回复“确认”，同时要求先规划删除关联影响，之后才开始前后端实现。本次四项修复只恢复既定行为和读取效率，不改变已确认界面或新增产品功能。

### 审查发现与修复

1. API 密钥页使用 cursor 分页，只刷新数据不会清掉失效位置。21 条记录、每页20条时删除第二页唯一记录，原来会显示“总计20行”但列表为空。`frontend/src/features/apikeys/index.tsx` 在真实刷新完成且当前页为空时清除分页位置，保留筛选、排序和每页数量。
2. 按密钥名搜索分组时，删除唯一匹配成员会让该组退出搜索结果。11组、每页10组时第二页因此失效。`frontend/src/features/key-groups/index.tsx` 按新总数回退到最后有效页，保留筛选和页大小；未改共享分页 hook 或筛选契约。
3. 历史请求的删除密钥不能命中普通预加载关联，同一密钥在多行重复出现时原来逐行回源。`internal/server/gql/api_key_identity.go` 复用既有请求级缓存，按项目和密钥ID去重；`ent.resolvers.go` 继续优先使用已加载关联。只缓存不可变的删除身份或不存在结果，不缓存可变活跃身份，不放开项目或个人密钥权限，也不修改持久缓存格式。不同删除密钥仍各自查询，本次不是全页批量加载改造。
4. 最终截图复核发现删除成功后还有英文错误 toast：关闭着的配置弹窗仍按共用 `selectedApiKey` 订阅详情，删除清缓存后查询已删除节点。`apikeys-dialogs.tsx` 改为只在配置弹窗打开时传有效查询 ID，避免删除流程触发无关详情读取；不修改删除成功或失败语义。

### 新增定向证据

```text
GOMAXPROCS=2 go test -p 1 ./internal/server/gql -run 'TestHistoricalRequestAPIKeyReusesDeletedIdentity|TestDeleteAPIKeysGraphQLAndHistoricalRequest' -count=1
```

通过，1.003s。实际查询计数证明：同一操作20次并发读取同一删除身份只查一次，完整历史配置仍可回读，不同操作不共享结果。删除及历史统计原断言也通过。未重复运行不受本次修复影响的业务、预算和备份测试；沿用其已通过结果。

使用内置浏览器和隔离 SQLite 补验两项分页问题及详情订阅：

- API 页修复前实际复现空页；修复后新增一条合成密钥重放删除末页唯一项，回到有效第一页并显示20条，筛选“复审密钥”和页大小20保留。
- 分组页以11个组、每组唯一匹配密钥构造搜索结果，搜索“边界匹配”、页大小10，在第二页删除成员；总数11→10、回到第一页，筛选和页大小10保留。
- 第一次 API 空页回退截图仍有删除后详情错误 toast，已作为第四项缺陷修复。新标签中重放末页删除，通知区只有“已永久删除1个密钥”、该错误计数0、控制台无相关错误；04截图已替换为这次最终结果。随后打开剩余密钥的配置弹窗，名称和配置字段正常回读，证明按需加载没有破坏配置功能。
- 搜索防抖尚未完成时连续操作曾使验收菜单关闭；等待实际筛选状态稳定后重放成功，未将这一测试时序现象当作产品缺陷。

新增实际截图：[API 末页删除回退](../../output/implementation/axon-key-delete-20261103/04-api-last-page-recovery.png)、[分组搜索结果回退](../../output/implementation/axon-key-delete-20261103/05-group-search-page-recovery.png)。它们是实际页面截图，不是生成图。

### 保留的边界

- 备份的合并冲突沿用既有“凭据值相同才是同一项”契约。删除状态随备份中的历史身份保留；合并恢复不负责撤销目标库里另外存在的旧原文凭据，不按名称或源ID猜测撤销目标密钥。
- 删除入口目前按项目写权限显示，个人密钥仍由后端校验创建者；无权限目标会使整批失败。通用失败提示的细分属于低优先级体验问题，本轮不扩改全部动作。
- 真实 Redis 多实例、MySQL、PostgreSQL、TiDB 与移动设备矩阵仍未验证；不把已有静态与单机结果扩写成这些环境已通过。
- 本轮未运行 lint/build、全量类型检查或全库测试，未调用收费供应商，未提交或推送。

本轮浏览器标签与临时前后端服务均已关闭，18093/15173 端口释放；受管清理脚本返回 `cleaned=true`、`task_root_removed=true`，删除本轮8项临时产物，保留预先存在的 scratch 根目录、实际截图与本记录。

独立执行者最终复核了四处修复的源码、通过日志和替换后的04实际截图，确认通知区只有删除成功、未发现剩余阻断缺陷；没有重复运行测试或服务。主执行者据此与自身复现、修复及补验结果共同确认本阶段目标和限定验收完成。
