# 渠道全局与标签优先级实现记录

日期：2026-10-29。状态：实现与本地验收完成，已提交、推送并随正式版 `v1.0.0-beta10-fork.7.syh.6` 发布；未部署生产机器。

发布源码提交 `92b62dee691fc1f864bbe9f26935d37b437d655c`，功能提交 `b78f041fb93e3665658fee91c5e215e9443c3a18`。[Release](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-beta10-fork.7.syh.6) 于北京时间 2026-10-29 17:22:33 公开为正式 latest，[发布工作流](https://github.com/Syh1906/axon-hub/actions/runs/40845466280) 全部成功。完整版本归属、12 个附件的校验结果与生产部署边界见 `docs/fork-maintenance.md` 的独立发布条目。发布后只回写文档记录，不移动已发布 tag。

## 行为与根因

原快捷编辑使用列表筛选标签来禁用标签选择器，导致筛选一个标签后无法编辑同渠道的其他标签。现改为独立的小浮窗：全局优先级在上，渠道全部标签逐行列出，不受列表筛选限制。布局沿用已确认预览与项目现有组件。

全局值和标签值一次保存；标签留空使用全局值，全局留空沿用模型关联。保留原有带符号的 32 位整数范围，0 与负数均有效，数值越小越先。取消、Escape 和点击外部丢弃未保存草稿；校验失败禁止保存，接口失败保留草稿。长内容滚动，标题与底部按钮固定可见。

实际请求顺序为密钥单独设置 → 命中标签最小配置值 → 渠道全局值 → 模型关联。多标签中有显式设置时先采用标签设置；全局值仅在没有命中标签设置时使用。没有指定请求标签时也使用全局值。候选渠道的权限范围、模型匹配及原有权重／健康排序不变。

未指定单个列表标签时展示全局值；旧渠道尚未设置全局值时保留原有标签最小值展示。服务器按同一展示规则排序后分页。普通渠道设置编辑保留全局值及标签值。新增值存储于原有 settings JSON，无数据库迁移；旧 JSON 缺省保持原调度行为。

## 主要文件

- new：`frontend/src/features/channels/components/channel-priority-cell.tsx`，独立优先级浮窗。
- `frontend/src/features/channels/data/channel-routing.ts`、`channel-query-fields.ts`、`channels.ts`、`schema.ts`：保存、类型及全部相关查询回读。
- `frontend/src/features/channels/utils/channel-priority.ts`：展示与输入校验；原通用数值浮窗只负责权重和成本倍率。
- `internal/objects/channel{,_routing}.go`：全局字段与继承。
- `internal/server/biz/channel_routing{,_query}.go`、`channel.go`：原子保存、排序和普通编辑保留。
- `internal/server/orchestrator/select_candidates.go`：无标签请求也应用全局值。
- `internal/server/gql/`：新增保存接口、输出字段和生成代码。
- 中英文渠道指南、文案与 `docs/fork-maintenance.md` 已同步。

## 已执行检查

- `go generate`（在 `internal/server/gql`）：执行 Makefile 的 `generate` 同一入口；本机没有 make。首次遇 Windows 文件占用，第二次成功，无遗留无关生成差异。
- `go test ./internal/server/biz ./internal/server/orchestrator -run 'TestChannel(TagPriorities|Priorities|RoutingOrder|Priority)' -count=1`：两个包通过。覆盖整体保存、普通编辑保留、标签清空、全局清空、零值、无标签、非法标签整体回滚、筛选／未筛选排序、调度继承及密钥覆盖。
- `node --test src/features/channels/channel-priority.test.mjs src/features/channels/default-tags.test.mjs`（frontend）：9 项通过。
- 使用本地 Prettier 格式化本次相关文件，`gofmt` 格式化相关 Go 文件；`git diff --check` 通过。未运行 lint 或 build。
- `node node_modules/typescript/bin/tsc --noEmit --pretty false -p tsconfig.app.json`：未通过，报 172 条诊断。另以同一 TypeScript CompilerHost 在内存中对照 HEAD 和当前文件：两者均为 170 条，逐文件／错误代码计数无差异；没有修改源码以绕过检查。完整项目类型检查仍不是通过状态。

## 真实接口与浏览器验收

环境：隔离 SQLite 数据库，后端 `127.0.0.1:8090`，前端 `127.0.0.1:5173/channels`，Codex 内置浏览器。测试渠道仅指向本机占位地址，不连接真实供应商。

- 只筛选“本地演示”，仍可编辑“备用”；同时保存全局 10、本地演示 14、备用 0，重新打开准确回读。
- 修改备用为 99 后取消，重开仍为 0；清空备用保存后回读为空，提示“使用全局（10）”。
- 全局输入小数时显示错误且保存不可用；Escape 和外部关闭后未保存值不落库。
- 清除列表筛选后显示全局 10；无标签渠道保存全局 0，刷新后仍为 0。
- 19 个标签全部可编辑，末项保存 -5 后刷新仍存在；内容区滚动时标题和操作按钮可见。
- 全局与标签全部清空后重开均为空，恢复继承。
- 独立 GraphQL 查询验证实际持久化；提交不存在的标签时报错，全局值没有部分写入。
- 视口检查：1280×720、与参考一致的 1689×1122，以及窄屏 390×844。从窄屏横向表格的优先级入口打开后，浮窗位于屏幕内且输入、取消、保存可操作。
- 页面身份正确、非空、没有框架错误覆盖层；页面控制台未发现 error/warn。

实际截图：

- [筛选单标签时的完整浮窗](priority-popover.png)
- [长标签列表末项](priority-long-tags.png)
- [窄屏浮窗](priority-mobile.png)

## 限制与清理

未部署到线上；未调用真实供应商。真实 MySQL／PostgreSQL 实例没有联调，本次不涉及 schema migration。项目完整 TypeScript 检查仍有上述已有诊断。

本轮创建的服务、浏览器标签与临时测试目录在交付前关闭并清理；不修改其他会话服务和已有演示库。实际截图和本记录作为交付保留。

## 独立审查与自检（2026-10-29）

按用户要求，使用独立 subagent `gpt-6.1-sol`、推理强度 `max` 审查当前差异、目标覆盖、项目规范及直接影响；主执行者同步自检并负责真实页面／接口复验。审查范围仅为本阶段优先级功能，没有扩展全库治理。

审查发现并关闭 3 项 P2 问题：

| 问题与触发条件 | 局部修正 | 验证结果 |
| --- | --- | --- |
| 已有值时只输入 `-` 或 `1e`，浏览器字段值变为空字符串；表单禁用原生校验，错误保存为继承 | 新浮窗移除 `noValidate`，恢复浏览器对未完成数字的校验 | 真实组件修复前可复现清空；修复后提示“请输入一个数字”，不提交。独立接口确认原全局 10／标签 14 保持；合法 -3、真正清空均可正常保存 |
| GraphQL 变量可绕过界面范围限制，写入 2147483648 或 -2147483649 | 对全局、标签及原批量标签保存入口统一校验 32 位有符号整数范围 | 新增 `TestChannelPriorityIntegerBounds` 覆盖合法两端值和越界不写入。真实 GraphQL 三个入口均拒绝越界，全局 30／标签 14／权重 0 保持 |
| 保存成功后列表回读尚未完成，立即重开产生旧草稿，连续保存另一标签会覆盖前一次值 | 新保存 hook 等待列表及详情缓存刷新完成，再解除保存状态 | 真实页面施加 1800ms 网络延迟，修复前 10 改 20 后重开仍为 10；修复后重开准确为 30，连续保存备用标签 21 后全局仍为 30 |

相关文件：`channel-priority-cell.tsx`、`data/channel-routing.ts`、`internal/server/biz/channel_routing.go` 及原有业务测试文件。没有改变已确认视觉方案、继承顺序、负数规则或数据库结构。

主执行者追加执行：

```text
go test ./internal/server/biz -run 'TestChannel(TagPriorities|Priorities|PriorityIntegerBounds)' -count=1
git diff --check
```

均通过。独立 subagent 另行复跑定向 Go 测试、前端 9 项测试、新增整数边界测试，均通过；它独立查看了三张实际截图并确认布局符合已确认约束。真实页面复验由主执行者执行，独立审查者根据补丁与复验结果闭环，没有把这些操作声称为其亲自执行。

独立最终结论：本阶段无剩余阻塞项，也没有其他有证据的非阻塞问题；目标覆盖及项目迭代规范符合要求。完整 TypeScript 检查与线上／其他数据库的限制仍按上文保留；独立审查未重跑 TypeScript 基线对照。未执行 lint/build、提交、推送或部署。

[未完成数字被阻止保存的复验截图](review-invalid-number.png)。测试网络延迟已恢复，复核临时服务与隔离库在交付前清理。
