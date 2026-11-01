# 分项计费与刷新交互验收

日期：2026-10-31。已按确认方案完成实现、定向回归及本地桌面浏览器联调；未提交、推送或发布。模式识别、Responses 恢复与正则修复的前阶段证据见 [前阶段记录](../../../.agent/summary/openai-service-tier-20261031.md)。本轮方案见 [分项方案](../../../docs/solutions/service-tier-item-pricing-proposal-20261031.md)。

## 实现结果

- Fast／Ultrafast 支持默认倍率与输入、输出、读缓存、写缓存变体覆盖。每项互斥选择倍率、指定单价、明确未配置；省略为继承，0 为免费。
- 优先级为当前价档分项、当前价档默认、模式分项、模式默认。时间段完全替代上下文档；指定单价不再乘默认模式倍率，渠道成本倍率最后只乘一次。固定／图片价格沿原行为。
- 官方参考提供比例与绝对单价两种应用，保存模型、版本、来源、日期、上下文范围及原始分项价格；用户预览后主动应用。未知范围与缓存时长不匹配明确未配置，不猜价格。
- 模型与渠道共用编辑器。渠道应用模型配置后独立，编辑分项不丢上下文范围，取消不保存；迟到预览不会覆盖已改基础价，空规则归一为继承。
- 预算预估、最终结算、恢复复用同一解析；逐项冻结实际规则来源、最终价、模式金额。管理端和自助 GraphQL 均返回完整证据；旧账单不根据现价重算。
- 自动刷新按钮常态显示当前间隔，悬浮／键盘聚焦显示刷新，点击只手动刷新；定时关闭仅在下拉。请求／线程／追踪复用该组件。

## 自动检查

| 检查 | 实际结果 |
| --- | --- |
| `go test ./internal/objects ./internal/server/biz ./internal/server/gql/... ./internal/server/orchestrator -run 'TestServiceTier\|Test.*Budget\|TestModelStandardPrices\|TestSelfUsage' -count=1` | 通过；无匹配测试的子包由 Go 明确报告 |
| `go test ./internal/server/gql/selfusage -run TestServiceTierItemsRecoveryAndSelfUsageGraphQL -count=1` | 最后补充账本／GraphQL 精确断言后通过 |
| `node --test src/features/models/pricing.test.mjs src/components/request-usage-cost-breakdown.test.mjs`（frontend） | 22/22 通过 |
| TypeScript 同环境 HEAD 对比 | 基线 170、当前 170，按文件／错误码／源码锚点比较新增 0；保留全部原始诊断见 [报告](typescript-review.json) |
| `git diff --check` | 通过，只有 Git LF/CRLF 提示 |
| 中英文 serviceTier 文案键 | 88 个，集合一致 |
| `gofmt` 与既有 Prettier 定向格式化 | 完成；编辑器 432 行，未增长超过项目限制 |

GraphQL 生成在受管源码副本中执行主 schema 与 selfusage 的 `go generate`，成功后同步生成文件并通过以上测试。直接 `pnpm exec prettier` 触发本机 pnpm 自动安装检查，因无 TTY 中止；未允许清空依赖、未改依赖配置，随后使用项目已安装的 `node node_modules/prettier/bin/prettier.cjs --write` 完成同一格式化工具的调用。未运行 lint/build。

真实 SQLite 恢复用例先 Reserve → MarkSent → RecordUsage 保存使用量与价格快照，然后将当前模型单价从 25 改成 999，创建新服务实例重读数据库并恢复两遍。断言冻结单价仍 25，模式小计 0.25，渠道 ×0.8 后为 0.2，只有一笔账本、唯一用量日志、预留归零；通过 API Key 上下文访问真实自助 GraphQL，费用 0.2、模式 fast、方式 unit_price、来源 custom 均正确。这里模拟服务重启，没有通过第二个操作系统进程执行该测试。

## 浏览器与本地接口

使用 Codex 内置浏览器访问 127.0.0.1:5173，实际后端 8090 与独立 SQLite；没有替换接口响应。

| 场景 | 观察结果 |
| --- | --- |
| 官方比例导入 | Sol Fast ×2、Ultrafast ×6，保存成功且重新打开完整回读 |
| 自定义分项保存 | Sol Fast 输出 ×2.5、缓存指定 0.15 保存回读成功，示例金额 0.65 |
| 取消与重开 | 将输出改 9 后取消，重开仍为 2.5、示例 0.65 |
| 官方绝对价／范围 | GPT-5.5 预览 0.65 → 2.00，应用后输入 12.5、输出 75、缓存 1.25；将输出改80后仍显示 ≤272000，最终取消此草稿 |
| 旧预览失效 | 官方预览打开后改基础价，旧应用入口消失，重新预览才应用 |
| 空规则 | 清空最后默认倍率、最后一条分项改继承均回到未配置，未残留空自定义规则 |
| 渠道复制独立 | “上游优先”渠道应用正则匹配 Sol 模型后将输出改 ×3，保存重开示例为 0.7；只读数据库核实模型仍 ×2.5，渠道 ×3 |
| 长内容 | 编辑内容独立滚动，标题与保存／取消始终留在弹窗内；连续保存、取消、重开可正常操作 |
| 新成本浮层 | #42 显示橙 Fast、紫1M；逐项输入 ×2、输出 ×2.5、缓存指定0.3；模式1.789008、渠道×0.8、合计1.431206 |
| 自动刷新 | 选5s后手动刷新，菜单仍选5s；下拉关闭成功；主按钮不再切换为关闭 |

截图均为真实页面：

- [模型分项编辑](model-item-rules.jpg)
- [渠道独立分项](channel-item-rules.jpg)
- [成本浮层](cost-itemized.jpg)
- [手动刷新后保留5s](refresh-interval.jpg)

按用户授权向独立演示库追加 #42 合成观测，使用现有 UsageLogService／BudgetService 计算结算，断言金额 1.4312064；保留原 41 条。追加命令使用受管 Go overlay 定向运行 `TestManualItemAppend`，通过。手工数据不属于临时验收目录，服务与库按要求继续保留，操作说明见 [本地手测](../../local-preview/axon-manual-20261031/README.md)。最新后端已重启加载最终 schema，health 返回200，进程身份清单已更新。

## 独立审查与限制

用户指定的独立 gpt-6.1-sol / max 审查确认当前范围无未解决 P0/P1/P2。审查提出的空规则、官方范围、旧预览、来源与统一摘要问题均已修正并复验；其建议的 GraphQL 费用及单账本断言也已补齐并通过。

全库 TypeScript 仍有170条存量诊断，因此不能称全库类型检查通过。未调用真实收费供应商，也未证明第三方中转实际获得加速；有效上游声明优先、缺失按客户端的行为已用本地证据验证。没有生产升级验收、远程写入或发布；本轮浏览器证据为桌面视口。官方参考不是 Codex credits 价表，30分钟／12小时写缓存不冒充项目5分钟／1小时。生成设计图只作已确认参考，以上真实截图才是实现验收证据。

自动化临时目录 `axon-item-pricing-20261031` 已按 verification-cleanup 固定脚本清理：`cleaned=true`、`task_root_removed=true`，删除2969个受管节点；预先存在的scratch根目录保留。手工服务／库和正式验收截图未清理。
