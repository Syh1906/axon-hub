# 多渠道 Codex 模型目录修复与阶段验收

日期：2026-11-02。状态：本阶段本地实现、自检、独立复核和联调验收通过；本地提交；未推送、未发版、未部署。生产及真实供应商仍未验收。

## 目标与结果

原实现要求同一公开模型的每个候选渠道都有完整目录；任一缺失便省略模型，全部省略返回 503。现在模型 `settings.codexCatalog` 保存管理员确认的完整定义、精确客户端版本及渠道／实际模型绑定；已确认模型不再依赖上游逐家提供目录。未配置定义的模型保留已发布的渠道目录读取方式。

每个完整来源的能力分别保存在 `routes.capabilities`；请求时只合并当前项目、Key 可见且已确认的候选路线。`model` 保存固定指令及没有目录来源路线的继承基线，`routes.limits` 只能额外缩减能力。管理员必须核实没有目录的渠道；系统不凭名称推断能力。新关联不能自动继承确认。

当前每个模型定义只对应一个精确 `clientVersion`。客户端升级后需要重新导入确认；不宣称未来客户端自动兼容。普通 Responses、权限、WS 传输和升级发布流程不在此次修改范围。

## 主要文件

- `internal/objects/model.go`、`internal/server/biz/model.go`：既有 settings JSON 保存及校验，无 SQL schema/data migration。
- `internal/server/biz/codex_definition.go`（new）：定义校验、导入、显式路线绑定、按可见范围合并能力。
- `internal/server/biz/codex_catalog.go`、`codex_catalog_models.go`：已保存定义优先；远程记录隔离，档位按 ID 合并，固定指令并保留未知字段。来源并发 4、总预算 10 秒。
- `internal/server/biz/codex_catalog_validation.go`（new）：已知字段类型、整数范围、枚举及限制后的最终结果校验，阻止坏模型拖垮客户端整个目录。
- `internal/server/biz/codex_catalog_messages.go`（new）：官方 0.166.0 的 23 个嵌套消息类型只读投影；校验后仍输出原始数据，未知字段保留。
- `internal/server/api/codex_catalog.go`：失败响应也给出省略模型数量。
- `internal/server/gql/model.graphql`、`model.resolvers.go`、`generated.go`：受写权限控制的只读导入预览及模型定义保存回读。
- `frontend/src/features/models/components/model-codex-definition-editor.tsx`（new）：版本、导入、JSON 核对、确认应用、清除、取消异步请求。
- 同目录 `models-action-dialog.tsx`、`models-association-dialog.tsx`：原右侧滚动栏内编辑、固定标题与保存区、重开重置状态、关联保存保留定义。
- 模型 GraphQL 查询、Zod、中英文文案及 Codex 指南同步。
- `internal/server/biz/codex_definition_test.go`、`internal/server/gql/codex_definition_test.go`（new）：业务及真实 GraphQL HTTP 回归。
- `internal/server/biz/testdata/codex-0.166.0.json`（new）：协议代表样例，原模型指令已全部替换为虚构短文本，不作为生产目录。
- [真实页面验收截图](../../output/implementation/axon-catalog-definition-20261102/editor-integrated-review.png)（new）。

## 独立审查与修正

用户指定独立代理 `gpt-6.1-sol / max`，任务 `/root/catalog_independent_review`；只读审查当前修复及直接影响，不审计无关模块。最初不能将阶段直接认定为完整验收，审查及自检发现并修复：

1. 浅层校验允许坏枚举、类型、小数、空数组元素及深层消息字段进入响应。独立代理用真实 CLI 0.166.0 证实：好模型与一条坏模型同数组时，整个 `model/list` 可能为空。现对已知字段及完整消息结构校验，远程只隔离坏记录，保存定义和最终限制结果均拒绝非法结构。
2. 官方缺省为 false 的能力不能因字段缺失而宣称支持；限制含义相反的 `node_repl_disabled`、`node_repl_auto_review_required` 按更严格结果合并。合法 shell 别名统一为 `unified_exec`。
3. 导入时提前合并所有来源会让 Key 看不到的较小来源继续限制返回模型。现保留逐路线能力，到请求时按实际可见候选合并；大小来源两种顺序均覆盖。
4. 编辑器位置曾形成第三栏，且缺完整页面联调。现放入原右侧滚动区，真实页面验证两栏、固定标题底部及无横向溢出。
5. 关闭导入中的弹窗再打开时，旧异步请求可能回写状态。现卸载取消请求并忽略取消后的结果；真实延迟 4 秒联调确认旧请求结束后新弹窗仍正常可保存。

独立代理最终结论：本阶段验收通过，已确认问题全部闭环，未发现剩余阻塞项。其最终结论时 CLI 直连仍待主执行补验；下节已单列补验通过结果。官方协议核对限定于上述已证实路径，没有扩展为未来 schema 框架。

已按 `axon-frontend-iteration` 的缺陷修复流程执行真实页面和后端联调，保留现有两栏、固定标题及底部操作区；未另行扩展视觉设计。其他技能只补充诊断、验证及临时资源约束，没有重启多套工作流。

## 验证与证据范围

按项目规则未运行 lint/build。只补受本轮修改影响的检查；已有有效结果复用，不因进入收尾全量重跑。

- 前阶段命令 `GOMAXPROCS=2 go test -p 1 ./internal/server/biz ./internal/server/gql ./internal/server/api -run 'TestCodex(Catalog|Definition)' -count=1 -v`：9 项测试通过。API 包没有筛选命中的测试，不能单凭此命令宣称 API 端到端验收。
- 本轮 `GOMAXPROCS=2 go test -p 1 ./internal/server/biz ./internal/server/gql -run 'TestCodexDefinition|TestCodexCatalogValidationAndCapabilityIntersection' -count=1`：能力范围、缺省、坏字段、限制及 GraphQL 检查通过；后补正则／条件关联用例最初因测试夹具缺 BaseURL 失败，补齐静态来源夹具后仅重跑 `TestCodexDefinitionRegexAndConditionalBindings` 通过。没有将失败记为通过。
- 完整消息投影接入后，仅重跑受影响 `go test -p 1 ./internal/server/biz -run 'TestCodexDefinitionLimitsAndNestedKnownFields|TestCodexDefinitionCurrentClientSchema' -count=1`，通过。
- 回归覆盖多渠道离线后可读、项目与 Key 权限、别名、正则公开 ID／条件关联、未确认路线隔离、版本不匹配、逐来源能力顺序、非法已知字段和限制、消息深层类型、未知字段保留；GraphQL HTTP 覆盖导入、保存回读、清除及拒绝未授权预览。
- 按既有生成入口执行 `internal/server/gql` 下 `go generate`。无数据库迁移。
- 使用项目 TypeScript compiler 和 tsconfig 检查最初 5 个变动文件，诊断 0；异步请求修正后只补查受影响的 2 个 UI 文件，诊断 0。未宣称全库 TypeScript 通过。
- 本次相关文件 `git diff --check` 通过。并发货币任务曾产生其他文件差异，均保留、不纳入本次审查；全库差异检查不能代替本次定向结果。
- 自动格式化未执行成功：pnpm runner 无结果后停止，直接使用现有 Prettier 时缺少 `@trivago/prettier-plugin-sort-imports`。未安装依赖或改变格式配置；不宣称格式化命令通过。

### 真实管理页面联调

使用本轮启动的实际 Axon 后端、项目 Vite、隔离 SQLite 和仅监听 loopback 的目录来源；浏览器为已连接 Chrome。使用虚构测试账号与渠道，无生产凭据、真实供应商或收费推理。

1. 真实登录、进入模型编辑、导入 0.166.0。未确认不能保存；确认应用后保存成功。
2. 只读 SQLite 核实版本 0.166.0、2 条路线及 2 份来源能力快照。关闭重开回读成功。
3. 清除保存，再重开及读库均确认定义已移除；重新导入确认保存成功。
4. 来源延迟 4 秒，导入中取消并立即重开；新弹窗可保存，旧请求结束后仍可保存；后端记录 `context canceled`。
5. 正常右侧滚动时 dialog 自身 scrollTop=0，标题与底部固定；dialogWidth=scrollWidth=1151，无横向溢出。截图为真实完整业务页面，不再以早期模拟编辑器截图代替联调。

### 真实 Codex CLI

- 前阶段隔离 CODEX_HOME 及本地 HTTP 响应验证真实 0.159.2、0.166.0 可解析后端生成的目录 JSON；此结果只证明解析兼容，不证明两版本定义自动互通。
- 本轮新增 CLI 0.166.0 **直连当前真实 Axon `/codex/models`**：创建仅存在于隔离测试库的虚构 APIKey，明文只保留在验证进程内存。HTTP 200，models=1，slug=`gpt-6.1-sol`；app-server `model/list(refresh=true)` 返回 1 项，成功识别目标模型。CLI 进程已退出，未调用模型推理。
- 首次测试 Key 创建使用数字 projectID 被 GraphQL 正确拒绝；核实 GUID 契约后改用 `gid://axonhub/Project/1`，通过。该修正仅涉及验证脚本。

## 外部 CLI 与 App

此前按用户明确授权执行 `npm install --global @openai/codex@0.166.0 --prefix C:/Users/xiaowei/AppData/Roaming/npm --no-audit --no-fund`，成功，`codex --version` 为 `codex-cli 0.166.0`；当时 npm latest 为该正式版，未安装 alpha。

Codex App 继续使用内置 CLI；未改 App、启动方式、用户 Codex 配置或真实凭据。Axon 目录服务并不运行本机 CLI；没有机械替换 Go 请求中已有的默认客户端版本标识。

## 清理及尚未验收

本轮 Chrome 测试页全部关闭；仅结束核实归属的 Axon、目录来源、Vite 及 esbuild 子进程，保留其他服务。18090、18091、15173 均无监听；相关工具会话已退出。

在 `C:/Users/xiaowei/.codex` 执行固定命令 `python C:/Users/xiaowei/.codex/skills/verification-cleanup/scripts/cleanup_verification.py cleanup --task-id catalog-review-20261102`。结果 `cleaned=true`、`task_root_removed=true`、`removed_items=111`，预存 scratch 根保留。临时数据库、CLI 隔离目录、脚本、日志及 Go 临时输出已清理；截图和本文作为交付保留。

真实收费推理、生产服务器、真实渠道能力及 PostgreSQL/MySQL 实例未验收；没有 SQL 表结构变更。本阶段通过不等于云悠／泉哥服务器已经升级，仍需按既有独立发版部署流程发布、升级并在各环境导入确认模型定义。
