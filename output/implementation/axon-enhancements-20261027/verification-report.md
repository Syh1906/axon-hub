# 供应商适配增量实现与验证

> 最新状态：`v1.0.0-beta10-fork.7.syh.3` 已于北京时间 2026-10-28 00:02:31 正式发布并设为最新版本；完整工作流成功。下文“未提交／未发布”为对应准备阶段的历史记录，最终结果见文末。

日期：2026-10-27。状态：本地实现与相关验收完成，未提交、未推送、未发布。

本轮承接「规划 axon-hub 供应商适配」最后确认的标签默认优先级、列表快捷修改及排序、目录价格导入、姓选填、渠道模型多选；保留接手时已有的模型弹窗与自动补全改动。

## 用户调试后的补充调整（2026-10-27）

以下更新取代下方首轮记录中的「须选择标签」「仅导入价格」部分：

- 标签列默认显示。未选择顶部标签时，按每个渠道自身标签中的最小已配置优先级展示和排序；没有配置则显示继承。优先级表头只保留标题，不再重复标签名。快捷编辑内显示正在修改的标签。请求实际标签继承范围不变。
- 右侧入口移到模型卡片标题旁，导入全部卡片能力、推理、模态、价格、上下文／输出限制及日期，不修改左侧配置；目录缺失项恢复为空／关闭，无价格模型也可导入。
- 新增纯函数 `channel-priority.ts`、`catalog-card.ts` 及行为用例。前端 109 项测试通过；后端无标签筛选排序测试通过；完整类型检查仍为 172 条既有诊断，相关新增／修改文件无新增诊断。
- 浏览器实测无顶部标签筛选时显示 14／14／20；模型卡片导入后工具调用、视觉、温度、模态同步更新，上下文 1,047,576、输出 32,768，左侧 `demo-catalog-price` 不变，并已保存并重新打开回读。正则草稿 `^demo-.*$` 导入后保持匹配方式、表达式及规则优先级不变，随后取消草稿；无标签筛选降序得到 20／14／14。
- 核心实现：[渠道显示优先级（new）](../../../frontend/src/features/channels/utils/channel-priority.ts)、[卡片转换（new）](../../../frontend/src/features/models/data/catalog-card.ts)、[卡片导入（new）](../../../frontend/src/features/models/components/models-catalog-import.tsx)。
- 新截图：[渠道自身标签优先级](06-channel-own-tags.png)、[完整模型卡片导入](07-model-card-import.png)。

## 首轮已实现行为（历史验收记录）

| 场景 | 实际行为 |
| --- | --- |
| 标签默认优先级 | 密钥单独覆盖优先，其次请求所用标签默认值，最后模型关联；排除标签不继承，多标签取最小配置值；不扩大原候选权限 |
| 同级渠道选择 | 先保留故障退避及不可用限制，再按权重降序；同权重继续使用负载均衡评分；会话复用保持 |
| 渠道列表 | 优先级、权重、倍率默认并列，点击数值编辑，无铅笔；批量弹窗可同时修改优先级和权重；清优先级不改权重 |
| 页面排序 | 优先级、倍率对全部筛选结果排序后分页；当前浏览器保存默认字段／方向；列标题临时排序不改默认设置 |
| 价格导入 | 右侧独立选择目录并整套替换价格，不改模型标识或能力；左侧空价自动填充；已有 0 元价、切换开发者均保留已填价格 |
| 用户姓 | 新建／编辑允许留空；编辑清空提交空字符串，并能重新读取为空 |
| 模型多选 | 从管理模型展开具体 ID；规则只提供公开 ID；筛选全选／取消只影响当前可选项；去重并作为手动模型保存，不自动关联 |

标签设置复用现有 JSON，无 schema/data migration。倍率专用更新复用现有事务与价格版本，避免快捷修改误清已有成本项。渠道表单按渲染、状态 hook、纯函数拆分：2051／1742／178 行，未新增超 2200 行手写文件。

## 自动验证

| 检查 | 命令／方式 | 结果 |
| --- | --- | --- |
| GraphQL 生成 | 在 `internal/server/gql` 执行项目声明的 `go generate` | 通过；清除了生成过程带来的纯换行噪音 |
| 后端相关完整测试 | `go test ./internal/objects ./internal/server/biz ./internal/server/orchestrator ./internal/server/gql` | 四个包通过 |
| 故障退避与恢复 | `go test ./internal/server/orchestrator -run 'TestChannelPriorityWeightTiersRespectFailureRecovery' -count=1` | 生产与调试路径均通过：高权重近期失败／半开熔断退后，恢复后重新优先 |
| 前端单元测试 | 在 `frontend` 执行 `npx --yes pnpm@10 test:unit` | 104 通过，0 失败 |
| 应用类型检查 | 在 `frontend` 执行 `node node_modules/typescript/bin/tsc --noEmit -p tsconfig.app.json` | 未全绿；基线 173 条，当前 172 条，见下方说明 |
| 格式与差异 | 本轮前端文件执行现有 Prettier，Go 手写文件执行 `gofmt`；`git diff --check` | 通过 |

类型基线使用同一已安装 TypeScript、同一 `frontend` cwd 和 tsconfig，在内存编译器读取路径上替换为 HEAD 源码；排除本轮新增源码作为基线入口。按文件与诊断码统计，拆出的 hook 归回原渠道表单比较：无新增诊断。没有修改基线代码或为检查建立额外 checkout。完整类型检查仍被仓库已有错误阻塞，不能视为全量类型检查通过。

未运行 lint、build，遵守项目约束。没有调用真实供应商，也没有进行生产、远程系统或数据库迁移操作。

## 浏览器验证

使用 Codex 内置浏览器，前端 `http://127.0.0.1:5173`、演示后端 `http://127.0.0.1:8090`。实际页面截图为 1689×1122，CSS 视口相同，浏览器 DPR 1.5；工具截图已按 CSS 尺寸输出。

- 批量保存 A/B/C 的优先级 14/14/20 和权重 10/20/100；重新打开回读一致。
- 直接点击 A 优先级，清空后保存，列表显示「继承」且排至配置数值之后；再恢复 14。
- 「重置所有优先级」只清空优先级输入，权重不变；该验证草稿取消，没有覆盖演示配置。
- 将 C 的成本倍率设为 0.15，保存回显；已有价格保留由后端事务测试覆盖。
- 默认排序保存优先级升序；临时切为降序；刷新后恢复默认升序。
- 右侧选 OpenAI / GPT-4.1，导入输入 2、输出 8、输入缓存 0.5；左侧自定义 ID 不变。演示模型 `demo-catalog-price` 已创建，列表回读输入／输出金额一致。
- 手动录入 0 元计费项，切换左侧开发者和模型 ID 后仍为 0；新建空价选择 GPT-4.1 mini 自动填入 0.4／1.6／0.1。
- 用户姓清空保存，再打开确认空字符串；验证后恢复原演示用户姓名。新建界面也展示「姓（选填）」。
- 模型面板中已添加 ID 禁用；搜索 `qa-dialog` 全选 4 项，缩小搜索并取消其中 1 项后剩 3 项；添加保存后显示 8 个手动模型，刷新列表仍保留新增项。
- 新建渠道面板可直接选择规则展开的 gpt-6-luna／gpt-6.1-luna；已输入 demo-chat 标记已添加。仅截图后取消，未创建渠道。
- 页面间导航、对话框关闭后再次打开正常。最终检查自 13:10 UTC 以后无浏览器应用错误；更早的开发期 HMR context 错误已通过完整刷新消失。

浏览器发现并修复：目录选项 label 回调误清空选中 ID，导致导入按钮不可用；切换开发者清空右侧价格；新增下拉框未占满行宽。后端完整测试还发现权重覆盖故障退避，已保留失败状态并增加生产／调试双路径回归。

## 视觉证据与本地入口

- [交互汇报（new）](report.html)，运行入口：`http://127.0.0.1:5174/enhancements.html`，使用原有静态服务，未新启服务。
- [汇报图（new）](implementation-overview.png)。
- [渠道实际页面（new）](00-channel-list.png)、[点击数值（new）](01-channel-quick-edit.png)。
- [优先级与权重（new）](02-priority-weight-dialog.png)、[默认排序（new）](02-default-sort.png)。
- [目录价格（new）](03-model-price-import.png)、[姓选填（new）](04-user-optional-lastname.png)、[模型多选（new）](05-channel-model-picker.png)。
- [视觉对照记录（new）](../../../design-qa.md)。

演示前端 5173 保持原进程，8090 后端经明确授权重启加载最终代码，`output/local-preview/axon-routing/runtime/preview.db` 保留。三个渠道均禁用、地址为本机占位地址，供界面调试；新模型没有自动创建关联。

## 主要代码与说明

- [标签写入（new）](../../../internal/server/biz/channel_routing.go)、[排序分页（new）](../../../internal/server/biz/channel_routing_query.go)、[相关测试（new）](../../../internal/server/biz/channel_routing_test.go)。
- [请求优先级继承](../../../internal/server/orchestrator/candidates_priority.go)、[同级权重和故障退避](../../../internal/server/orchestrator/load_balancer.go)。
- [数字快捷编辑（new）](../../../frontend/src/features/channels/components/channel-routing-value-cell.tsx)、[批量优先级（new）](../../../frontend/src/features/channels/components/channels-tag-priorities-dialog.tsx)、[默认排序（new）](../../../frontend/src/features/channels/components/channels-default-sort.tsx)。
- [目录导入（new）](../../../frontend/src/features/models/components/models-catalog-import.tsx)、[模型选择面板（new）](../../../frontend/src/features/channels/components/channels-model-picker.tsx)。
- [Fork 维护台账](../../../docs/fork-maintenance.md)与中英文渠道、模型、密钥、负载均衡指南已同步。

## 已知限制

- 未验证真实供应商请求、跨机器 live 更新和 MySQL／PostgreSQL 实例运行；当前后端测试使用项目测试环境，排序比较在 Go 内统一执行。
- 优先级／倍率排序会读取全部筛选渠道后排序，避免只排当前页；超大渠道规模需另行评估数据库排序，未做性能基准。
- 本轮验收以桌面实际界面为准，未完成手机端和暗色主题的全流程验证。
- 仓库已有类型错误未扩大范围修复；没有提交或推送。


## 开发者重复分组缺陷修复（2026-10-27）

**结果：通过。** 本地模型列表由两个同名 OpenAI 分组恢复为一个分组，共 9 个模型。与是否关联渠道无关。

- 根因：自动补全允许手输自定义值；手输 `OpenAI` 失焦时原样写入，后端创建／批量创建／编辑也未统一内置开发者 ID。列表按原始字符串分组，但两种值都显示为 OpenAI；开发者规则原先也按原始字符串匹配。
- 修复：前端输入识别内置 ID 和显示名称，后端写入统一内置 ID；分组、规则编辑、继承和决策计数统一比较。历史大小写规则条目全部参与继承，不丢弃其中规则；自定义开发者名称仍保持原样。没有新增数据库迁移，没有修改共享模型 ID 自动补全的大小写语义。
- 主要代码：[开发者规范化（new）](../../../internal/server/biz/model_developer.go)、[前端输入识别（new）](../../../frontend/src/features/models/data/developer.ts)、[规则继承](../../../internal/server/biz/model_settings_inheritance.go)、[模型列表](../../../frontend/src/features/models/components/models-table.tsx)、[规则编辑](../../../frontend/src/features/models/components/models-association-dialog.tsx)。
- 回归用例：[后端（new）](../../../internal/server/biz/model_developer_test.go)、[前端（new）](../../../frontend/src/features/models/developer.test.mjs)；扩展既有 `candidates_model_rules_test.go` 验证旧大小写模型的真实候选选择与来源计数。
- 后端命令：仓库根执行 `go test ./internal/server/biz ./internal/server/orchestrator -count=1`，两包通过；随后补充用例后执行 `go test ./internal/server/orchestrator -run TestDefaultSelectorRuleRequestsKeepTheirModelIDs -count=1`，通过。
- 前端命令：`frontend` 下执行 `node --test "src/**/*.test.mjs"`，111 项通过、0 失败。`node node_modules/typescript/bin/tsc -p tsconfig.app.json --noEmit` 仍有 172 项既有诊断，本次修改的开发者辅助函数及三个模型组件无诊断；未运行 lint 或 build。
- 格式／差异：相关 Go 文件 gofmt；新增前端文件和改动组件按既有格式整理；`git diff --check` 通过。移除了无关格式差异。手写文件均未超过 2200 行。
- 浏览器：编辑原异常模型 `qa-dialog-menu-fixed-20261027`，手输 ` OPENAI ` 后失焦恢复显示 OpenAI，保存成功；只读数据库复核其开发者为 `openai`，全部有效模型仅剩 `openai: 9`。刷新后显示 1 个开发者、9 个模型。创建草稿验证 `OpenAI` 可识别、`MyProvider` 保留原样，随后取消草稿。
- 环境：只重启已授权的本地演示后端 8090；前端 5173 仍为原进程，数据库保留。未调用真实供应商，未做生产／远程写入，未提交或推送。未新增 MySQL／PostgreSQL 实库验证；本次没有数据库结构或方言查询变更。
- 截图：[修复后的开发者分组（new）](10-model-developer-group-fixed.png)。本地模型页保留供继续调试。


## `.syh.3` 发版准备（2026-10-27）

- 目标：`v1.0.0-beta10-fork.7.syh.3`，正式版。已通过 GitHub CLI 与 `git ls-remote` 核实最新远端正式版为 `.syh.2`，尚无 `.syh.3` tag。
- 已更新 [程序版本](../../../internal/build/VERSION) 和 [发布说明](../../../.github/release-notes.md)。`release.config.json` 的两层上游基线、发布分支和 `prerelease: false` 保持不变；`minimumReleaseNumber` 是最低合法编号，不是当前版本号，因此保持 1。
- 复用 [.github/workflows/release.yml](../../../.github/workflows/release.yml)：tag 推送 → 来源校验 → 测试 → amd64/arm64 镜像 → 临时 PostgreSQL 启动检查 → 下载包草稿与 `release.json` → 正式发布。无需新增手动触发入口或修改发布权限。
- 验证：`node --test scripts/release/*.test.mjs` 3 项通过；`./scripts/release/version.test.ps1` 6 项通过；`go test ./internal/build ./internal/pkg/xversion -count=1` 成功（`internal/build` 无测试文件）。另已检查新版本号合法、发布说明标题一致、正式发布配置、两层上游祖先关系及 `git diff --check`。
- 当前工作分支：`fix/model-dialog-interactions`，已提交 HEAD 与远端维护分支均为 `5ba313e6`；此次实现和发布准备仍在工作区。尚未创建正式 tag，未执行当前源码的 tag 级 `prepareRelease`，未运行远端 Actions、镜像构建或生产部署。
- 后续发布动作：提交本次源代码／新增测试／文档及两个版本文件，合入并推送 `release/v1.0.x`，确认检查通过，再创建和推送 `v1.0.0-beta10-fork.7.syh.3` tag。不要把 `output/local-preview/` 的演示数据库、运行配置和日志作为发布源码提交；不要直接 `git add .` 纳入所有预览产物。
- 沿用此前不提交、不推送的要求，本轮仅完成本地准备。正式发布与部署分别授权；发布本身不会升级生产机器。


## `.syh.3` 正式发布完成（2026-10-28）

- 用户明确授权完成整个发版流程后，已提交 85 个源代码／测试／文档文件并推送功能分支。排除了本地演示数据库、配置、日志、截图和本报告；保留未跟踪的 `output/` 与 `design-qa.md`。
- 提交：`f3e6aa29` 功能与版本准备；`e9da8897` 修复 CI 报告的开发者测试用例格式（gofumpt 每项单独一行，无行为变化）。
- [PR #4](https://github.com/Syh1906/axon-hub/pull/4) 的构建、测试和 lint 全部通过后合入 `release/v1.0.x`；合并提交 `aff699d244687b1ed25dfba81e15142b383d9a75`。合并后的维护分支构建、测试、lint 也全部通过。
- 本地已切回维护分支并快进同步。创建带注释 tag `v1.0.0-beta10-fork.7.syh.3`，执行真实 `node scripts/release/release.mjs prepare` 通过后，推送该 tag；没有移动或覆盖旧 tag。
- [发布工作流 40531369946](https://github.com/Syh1906/axon-hub/actions/runs/40531369946) 最终状态 `completed / success`：来源校验、两个 Go 模块与前端／发布脚本测试、amd64／arm64 镜像、多架构合成、临时 PostgreSQL 17 启动及版本检查、下载包及清单上传均通过。
- [正式 Release](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-beta10-fork.7.syh.3)：`draft=false`、`prerelease=false`，GitHub `/releases/latest` 返回本版本。公开时间为 UTC 2026-10-27 16:02:31（北京时间 2026-10-28 00:02:31）。
- 12 个附件齐全且非空：Linux／Windows／macOS／FreeBSD 的 amd64、arm64 下载包，以及 Linux loong64，共 9 个 zip；另有 `checksums.txt`、`config.schema.json`、`release.json`。
- 完整性核对：下载并读取三个小型元数据附件，逐个计算 SHA-256 与 GitHub 附件摘要匹配；`checksums.txt` 的 9 条归档校验和全部与 GitHub 对应 zip 摘要相同。未在本机下载和运行这 9 个二进制包，不将元数据核对称为各平台运行测试。GoReleaser 的归档校验表不包括额外上传的 schema／release 清单，后两者通过 GitHub 摘要单独核验。
- [部署清单 release.json](https://github.com/Syh1906/axon-hub/releases/download/v1.0.0-beta10-fork.7.syh.3/release.json) 中版本、源码提交、两层上游来源、架构和工作流地址均核对一致。生产镜像固定为：

  `ghcr.io/syh1906/axon-hub@sha256:b7f1964fbe4f871a17cd7ed2b0592f70a592bafa29f99fa90d9dc7fb5aebdad0`

- 主要执行命令：`git commit`、`git push origin fix/model-dialog-interactions`、`gh pr create`、`gh pr checks`、`gh pr merge --merge --match-head-commit`、`git fetch origin`、`git switch release/v1.0.x`、`git merge --ff-only origin/release/v1.0.x`、`git tag -a`、`node scripts/release/release.mjs prepare`、`git push origin refs/tags/v1.0.0-beta10-fork.7.syh.3`、`gh run view`、`gh release view/download`。
- 本轮只发布应用，未执行生产机器升级、数据库写入或部署仓库工作流；本地演示服务保留。发布核验用临时文件按受管清理流程回收。
