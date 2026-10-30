# 上游钱包补齐：实现与验收记录

2026-10-30，独立工作树 `codex/upstream-wallets`，基线 `be9b673f`。下文按阶段保留实现与复审记录；最终状态已按用户授权合入 `release/v1.0.x`，未发布／部署，合并验收见文末。其他任务的未提交资料及原服务未改动。

## 实现结果

- 新增 DeepSeek、Moonshot／Kimi 开放平台与兼容 Billing 余额查询，复用 URL、Key、代理、超时和刷新机制。
- 官方钱包按域名识别，原生请求不再试探网关接口；网关保留挂载路径，按响应识别 Billing。
- 无账号、无旧认证的 Key 认证来源首次查询成功后创建本地默认账号，只关联本次成功的 Key。New API 继续使用原授权流程。已有归属保持，重复刷新不重复建号。
- 钱包全部币种保留；同账户去重，待关联 Key 的结果可见但不相加。兼容 Billing 未声明账户归属时标为上游额度，单 Key 列表可查看，明细逐 Key 展示。
- Billing 直接余额优先；只有限额与用量周期明确一致时推算剩余。未提供限额周期时展示年度查询区间、限额与已用量，不虚构剩余；不限额不是无限钱包资金。
- 三种新增来源不提供倍率跟随，本地成本使用手动倍率。旧 UNKNOWN 不支持／认证失败缓存按查询版本重检，原关闭同步保持关闭。
- 沿用 JSON 存储，无新表或 data migration；GraphQL 保留旧单余额字段并追加币种列表、自动账号标记和用量时间区间。

## 检查结果与命令

- `go test ./internal/server/biz/upstream -count=1`：通过。新增用例覆盖两种官方钱包、域名与路径、币种、零余额、无效响应、Billing 直接金额、同周期计算、未知周期不计算、无限额标记。
- `go test ./internal/server/biz/upstream ./internal/server/biz ./internal/server/gql -run 'Test(OfficialWallet|Wallet|Billing|NewAPI|Sub2API|ChannelAccounts|ChannelAccount|Upstream|AppliedUpstream|RequestCostSnapshot)' -count=1`：三个包的定向用例通过；覆盖旧来源、账号认证隔离、默认账号、多 Key 去重、多币种、旧快照、手动倍率及正常 GraphQL 刷新事务边界。
- `node --test frontend/src/features/channels/data/channel-upstream-refresh.test.mjs`：5 项通过，包含余额支持而倍率不支持时仍刷新。
- `node node_modules/typescript/bin/tsc --noEmit -p tsconfig.app.json`（frontend 目录）：未通过，172 项诊断。余额组件未出现诊断；相关 schema 的 5 项诊断位于未修改的旧校验代码。未报告全站类型检查通过。
- `git diff --check`：通过；中英新增文案键一致，手写新增／增长文件均低于 2200 行。未运行 lint/build。
- 前端使用 CI 同版本系列 `npx --yes pnpm@10 install --frozen-lockfile`，锁文件无修改。默认 pnpm 12 不读取旧 overrides，故首次冻结安装失败；改为项目已采用的 pnpm 10 后成功。
- 本机没有 make；执行 generate 目标中的 `go generate`，Ent 生成两次被 Windows 映射文件占用中断。恢复本轮无关 Ent 生成内容和换行后，单独执行同流程的 `go run ../gqlgen/generate.go` 完成 GraphQL 生成；最终 Ent 无差异。

## API 与浏览器联调

本轮独立后端 `127.0.0.1:18090`、前端 `127.0.0.1:15173`、本地模拟上游 `127.0.0.1:18091`。使用新建隔离 SQLite、虚构 Key 和测试账号，不使用真实厂商凭据。测试走真实管理 API、GraphQL、服务端查询与数据库保存，未通过浏览器替换响应冒充联调。

五组 API 断言通过：DeepSeek 双 Key／双币种、Moonshot 国际钱包、Billing 直接余额、Billing 同周期额度、Billing 未知周期。原生官方域名请求经显式本地代理返回夹具。首次夹具误用大写代理类型，按现有前端小写 `url` 契约修正后重验通过。

内置浏览器验证：

- 页面身份为“余额隔离验收”，无框架报错，控制台 error/warn 为空。
- 自动账号无需账号 ID／额外令牌；备注保存后关闭重开回读一致。
- DeepSeek 显示 CNY 128.60、USD 8.00；第二把 Key 待关联，不累计为两倍。
- 手动倍率保存为 1.25，随后刷新余额仍使用 1.25；跟随上游选项不可用。
- Billing 未知周期显示限额 100、已用 25 和查询区间，不显示虚构的 75；已知周期用例返回 75。
- 窄屏 390×844 下表格可横向滚动，关闭与同步设置按钮可见；验收后恢复浏览器尺寸。

实际截图：[账号与认证](accounts.png)、[多 Key 与多币种](multikey.png)、[Billing 周期说明](billing-period.png)、[窄屏](mobile.png)。预览图不作为实际验收证据。

## 边界与收尾

尚未使用真实 DeepSeek、Moonshot 或第三方 Billing 网关账号联调。不同网关的计费周期字段和额度含义可能不同，不能以本地夹具通过声称覆盖全部厂商变体。没有新增数据库结构，未启动额外 MySQL／PostgreSQL 实例。

本轮三个联调进程已按 PID、启动时间和程序路径核对后关闭；端口 18090／15173／18091 已释放。临时数据库、模拟服务、检查脚本和日志按 verification-cleanup 清理；源码、预览、实际截图、说明文档及工作树保留。

## 两项复审问题修复与限定复验（2026-10-30）

首次独立复审确认两个 P2，因此当时未将本阶段判为全部验收通过。用户授权后，仅修复这两处及直接关联的中英文说明，未重新审查已确认正常的官方接口、默认账号、认证、倍率、GraphQL 或数据库范围。

- Billing：原实现接受含时分秒和时区的限额周期，但用量请求只发送日期，仍可能相减。现在将实际查询边界统一为 UTC 日期，并仅在两端与原限额周期完全一致时计算剩余；其余保留限额、用量，返回的用量区间与请求一致。相关中英文提示改为“无法确认限额与用量周期一致”，避免把精度不匹配误称为上游未提供周期。
- 多 Key 悬浮预览：原先只读取 `KEY`，遗漏 `ACCOUNT` 和 `QUOTA`。现在显示已有结果的各币种与类型；Billing 无可推算金额时分别显示限额、已用量。保留账号／待关联、最多三把 Key、完整明细入口和原汇总规则。

在现有 `TestBillingDetectionAndPeriod` 中补充午夜 Unix 时间、非午夜 RFC3339、时区偏移、同日短周期，验证实际请求参数、返回区间、是否允许相减及限额／用量保留。修前用例真实失败，修后执行 `go test ./internal/server/biz/upstream -run TestBillingDetectionAndPeriod -count=1 -v`，8 个子例全部通过；同组原有直接余额、整日周期、未知周期、不限额用例保持通过。

前端按 `axon-frontend-iteration` 的直接缺陷修复分支执行，沿用既有布局，无新设计审批。内置浏览器在 `http://127.0.0.1:15173/wallet-preview` 加载真实 `ChannelUpstreamBalanceCell` 组件与固定数据，验证双币种、Moonshot 单币种、长备注、待关联、Billing 直接额度及限额／用量、旧 Key 额度、订阅、空结果、三条预览上限、明细入口设置正确渠道、关闭后重复进入，以及 390×844 窄屏。此为限定组件展示验收，不替代此前管理 API 联调，也不声称进行了真实厂商联调。

夹具初次直接引用预打包模块导致导出错误，改为正常依赖解析后成功；编辑期间夹具热更新触发重复挂载提示。最终代码稳定后，新建干净浏览器会话验证 Billing 预览，控制台 error/warn 为空，无框架错误覆盖层。上述夹具问题未修改产品代码。

实际组件截图（不是设计图）：

- [双币种与待关联](wallet-fix-multicurrency.png)
- [Billing 额度与用量](wallet-fix-billing.png)
- [窄屏](wallet-fix-mobile.png)

独立 `gpt-6.1-sol / max` subagent 对上述修复与直接影响复审，复跑同组 8 个 Billing 用例，并检查代码及实际组件截图，结论为“两个 P2 均可关闭，限定范围内未发现新增或残留问题”。主执行者完成浏览器交互自检。未重复全站类型检查或无关测试，未运行 lint/build；前述真实厂商联调及全站类型检查限制仍保留。

本轮临时 Vite 服务及浏览器标签页已关闭，15173 端口释放，viewport 已恢复；临时夹具按 verification-cleanup 清理成功。Vite 自动生成的路由文件经逐字比较确认内容未变后恢复原换行，未留下无关改动。工作树继续保留，未提交、合并或发布。

## New API 账号关联与当前页刷新修复（2026-10-30）

用户随后要求处理线上旧版 Packyapi 的余额阻塞，并明确要求手动账号关联生效、刷新限定当前筛选后的可见分页。本轮只调整这两条链路及直接调用方，不重审已经关闭的三种钱包与 Billing 问题。

### 根因与改动

1. 本地 CCH 的 `provider-balance/probe.ts` 直接用账号认证查询 `/api/user/self`；Axon 却仅在 Key 分组查询取得匹配账号 ID 后保存该账号余额，且用倍率状态判定账号余额完整性。现在将账号查询时间与错误独立记录在现有 JSON 快照中，已验证账号余额即使在 Key 搜索失败时也能保存、显示和按账号去重。手动 `accountRef` 不被查询结果重写。真正的账号认证失败保留旧金额、标明不完整及错误；Key 额度正常时仍可独立刷新。
2. Axon 搜索前删除 `sk-`，并发送 `page_size=2`。本轮读取 [Packyapi 公开前端资源](https://www.packyapi.com/assets/index-tBbBNWO3.js)，其搜索框要求完整密钥或后四位，发送原值 `token` 及 `p/size`；[New API 上游源码](https://github.com/QuantumNous/new-api/blob/main/model/token.go) 本身会处理前缀。现改为完整 Key、`p=1/size=2`，并根据条数与 `total` 拒绝歧义结果；不按尾号匹配，不引入日志或其他端点兜底。此为已确认的请求契约差异，不能仅凭公开源码断言它是线上那次失败的唯一原因。新版尚未使用真实 Packyapi 凭据联调。
3. “刷新全部”原先调用全局目标查询，绕过当前列表。现在按钮改为“刷新当前页”，从表格实际行取 ID，刷新 helper 再限定请求 ID 集合。名称输入防抖、分页／筛选占位数据不进入目标；空页禁用。翻页只检查该页到期结果，继续复用未到期快照。已发布的后端目标查询 API 保留兼容，页面不再调用它。
4. 原界面把分组核验失败也写成“未关联”，容易误认为保存的账号关系失效。现分别显示“已按你的选择关联账号”和“分组未取得／分组未确认”。这些文案配合真实查询与保存逻辑修复，不以改提示替代修复。分组未确认时不伪造倍率，继续用手动值。

GraphQL 追加账号 `balanceErrorCode`，已同步 Go 对象、schema、生成代码、公共查询字段、前端类型与展示；执行 `go run ../gqlgen/generate.go` 成功。新增查询状态字段沿用 JSON，不新增数据库表或迁移；旧快照缺少账号查询时间时会进行首次独立查询。手动关联后的账号认证仍按对应账号配置隔离。

### 验证与复审

- `go test ./internal/server/biz ./internal/server/biz/upstream -run 'Test(NewAPI|ChannelAccounts)' -count=1`：通过。新增完整 Key 搜索、空／多结果、不同账号、详情不一致五组场景；账号余额在 Key 额度失败及分组失败时仍能保存回读，账号关联保持，未确认倍率不能冒认成功。
- `go test ./internal/server/gql -run TestUpstreamRefreshGraphQLCommitsEachKeyOutsideRequestTransaction -count=1`：通过。
- `node --test frontend/src/features/channels/data/channel-upstream-refresh.test.mjs`：7/7 通过，新增当前页／筛选 ID 限定、空目标和 Key 接口不支持但账号可查询的场景。
- `node node_modules/typescript/bin/tsc --noEmit -p tsconfig.app.json`：仍有 172 项诊断，未通过。本轮修改的组件与刷新逻辑无诊断；schema 中的五项诊断位于未修改的旧校验段，未声称全站检查通过。
- 独立 `gpt-6.1-sol / max` 复审通过后，追加复核发现账号 401 后仍可能由正常 Key 额度刷新顺带重试认证。已在调用层按实际到期分支决定是否加载账号，补充“Key 余额成功、账号 401、超过间隔后仅刷新 Key”的用例，并验证共享 session 不阻塞后续需要账号查询的 Key。独立复跑 `TestNewAPISelectedAccountBalanceDoesNotDependOnKeyGroup` 与 `TestChannelAccountsIndependentAuthAndBalance` 通过，该 P2 已关闭；未发现本轮限定范围内其他问题。
- `git diff --check` 通过；未运行 lint/build，未扩大为全库审查。

真实联调使用本轮新建的隔离 SQLite、后端 18090、Vite 15173 和模拟 New API 服务 18091，建立 12 个虚构渠道。没有浏览器替换 API 响应：初始化、账号保存、关联修改、余额刷新和回读均经过真实管理 API／GraphQL。初始化夹具首次将 GraphQL Decimal 数字误断言为字符串，修正测试断言后回读通过，产品代码未因此修改。

内置浏览器与模拟上游请求计数共同确认：第一页手动刷新只访问该页 10 个渠道；第二页只访问其 2 个渠道；筛选后只刷新匹配的 1 个渠道；空结果按钮禁用。切页时新鲜快照不请求上游；将隔离库中第二页一个快照设为到期后，第一页不访问它，切到第二页才查询。账号搜索未命中仍显示 $10 账号余额，Key 保留手动关联；成功匹配完整 Key 的场景取得倍率。账号备注保存后重开回读正确，取消关联再重新选择也立即保存。桌面与 390×844 窄屏均检查，控制台 error/warn 为空。

实际页面证据：[账号余额](account-balance-independent.png)、[账号关联与分组分别展示](account-key-group-independent.png)、[手动关联](manual-account-association.png)、[筛选后当前页](refresh-filtered-page.png)、[窄屏](refresh-filtered-mobile.png)。

三个本轮服务及浏览器页已关闭、端口释放、视口恢复，临时库与夹具按 verification-cleanup 清理；生成器产生的纯换行差异在内容一致检查后恢复。主工作树及原服务未改动。本工作树保留，尚未提交、合并、发布或更新线上旧版。

## 合入维护分支验收（2026-10-30）

用户明确批准提交、合入主分支并在确认后清理临时工作树。本仓库的长期维护分支为 `release/v1.0.x`。合并前主线为 `a69dc0e0`（新增密钥分组管理），主工作树无未提交代码，另有 183 个其他任务的未跟踪资料。

实现及设计／验收资料提交为 `7ddfdbe3`。先在功能工作树整合 `a69dc0e0`，共享的维护台账与 GraphQL 生成文件自动合并，无冲突；再次执行 `go run ../gqlgen/generate.go`，生成结果与自动合并内容一致。整合提交为 `9dc1c358`，随后主工作树已成功快进到该提交。

合并结果检查：

- `go test ./internal/server/biz/upstream ./internal/server/gql -count=1`：通过。
- `go test ./internal/server/biz -run 'Test(NewAPI|ChannelAccounts|ChannelAccount|Wallet|Sub2APIDefaultAccount|Upstream|AppliedUpstream|RequestCostSnapshot|KeyManagementAtomicRowsAndReplay|APIKeyGroupAdministration)' -count=1`：通过。
- `node --test frontend/src/features/channels/data/channel-upstream-refresh.test.mjs frontend/src/features/key-groups/drafts.test.mjs`：12/12 通过。
- `git diff --check`、暂存差异检查、无未解决冲突检查通过。主工作树与已验证功能分支代码树完全一致，原主线提交仍包含在历史中；183 个原未跟踪文件列表保持一致，没有暂存或覆盖这些文件。

此前外置的 8 张修复截图已复制到本目录，链接改为相对路径，避免清理工作树后丢失验收材料。未运行 lint/build，未重复无改动范围的浏览器验收；真实厂商账号未联调与全站 TypeScript 未通过的限制仍保持。此次仅合并本地源码，不推送远程、不打发布 tag、不部署线上。临时工作树随后通过 Codex 的受管归档流程回收，并保留可恢复记录。
