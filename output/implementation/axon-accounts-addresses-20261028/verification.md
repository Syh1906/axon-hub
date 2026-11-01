# 渠道多账号与多地址交付记录

2026-10-28。实现及本地验收完成，未提交、推送或部署。渠道按同一上游、分组／套餐和倍率组织；多个 Key 可属于不同账号，不新增渠道内部低倍率优先调度。系统／项目权限整体拆分仍为暂缓范围。

## 已实现

- 每账号独立认证、验证连接和关联 Key；New API 无调用记录的新 Key 可直接识别。Sub2API 采用手动账号归属。
- 账户余额按归属去重，不混入 Key 额度；不同币种分开，失败、过期或待关联明确标记。停用 Key 保留已知账户余额。
- Key 备注、独立启停、批量导入、凭据显示／隐藏／复制、渠道内会话 Key 复用开关。读取完整凭据检查渠道管理权限。
- 创建／编辑时配置多个地址、优先级、启停、测速；统一保存和取消。模型请求切换地址保留模型与 Key，沿用原重试次数和总截止时间，不重放已输出的流式内容。
- 接口与路径合并默认、自定义配置，展示最终请求 URL；表格有独立高度限制，保留高级模型协议设置。
- 新增 `.syh.5` 数据迁移和备份恢复处理，保留旧认证，只关联已有证据能确认归属的 Key。

## 浏览器证据

使用内置浏览器，`http://127.0.0.1:5173/channels`，1699×1132；独立 SQLite 与本地模拟上游，仅使用虚构账号和 Key。页面身份正确、内容正常，无框架错误覆盖层；最后检查控制台 error/warn 为空。

| 检查 | 结果 |
| --- | --- |
| 新建三个 Key | 三把 Key 分别得到额度与查询状态，不依赖调用记录 |
| 多账号与余额 | A 有 10 把 Key、余额 $60，B 有 1 把 Key、余额 $40；总计 $100、11 个 Key |
| Key 管理 | 备注保存回读；单独停用后显示 10 个启用；复用会话开关关闭后回读仍关闭 |
| 长列表 | 弹窗外层滚动为 0，列表在 610px 内容区内滚动，标题与关闭按钮保留 |
| 凭据 | 已保存示例令牌显示、隐藏、复制内容核对及不重填验证连接均通过；复制测试后清空本次剪贴板内容 |
| 悬浮明细 | 11 把 Key 仅预览前三条，可展开全部；主表格高度不随 Key 数增长 |
| 多地址 | 两地址、优先级 10／20 保存回读；不可达显示网络错误，可达显示 HTTP 200；取消修改不写入 |
| 接口路径 | `/chat/custom` 保存后重新打开仍一致，两个最终 URL 同屏可见 |
| 重复打开 | 创建、编辑、取消后继续进入其他弹窗可操作 |

实际截图（均为 new，示例数据）：

- [Key 管理](01-key-management.jpg)
- [余额与悬浮明细](02-channel-balance.jpg)
- [账号认证](03-account-authentication.jpg)
- [多地址编辑](04-edit-addresses.jpg)
- [接口与路径](05-interface-paths.jpg)

按确认预览 07／08／09／04／05 的信息结构实现，保留现有主题、表格和弹窗。实际列表使用 11 把 Key 验证滚动，未复制生成图的示例文字或强制匹配其像素尺寸。

## 检查与修正

相关 Go 包最终结果均通过：`internal/server/biz`、`biz/upstream`、`orchestrator`、`gql`、`backup`、`internal/ent/migrate/datamigrate`，以及独立 `llm` 模块的 `pipeline`。部分合并执行首次失败后按具体失败修正并复验对应包。补充覆盖账号隔离、错误归属、去重、失败保留但标记部分汇总、权限、迁移幂等和版本顺序、地址切换固定 Key、超时预算与流式输出边界。

仓库根目录执行的相关命令包括：

```text
go test ./internal/server/biz ./internal/server/biz/upstream ./internal/server/orchestrator ./internal/server/gql ./internal/server/backup ./internal/ent/migrate/datamigrate -count=1
go test -json ./internal/server/biz ./internal/server/orchestrator -count=1
go test ./internal/server/biz -count=1
```

首条合并命令不是一次通过；`orchestrator` 最终在第二条命令中通过，`biz` 修正测试夹具后在第三条命令中通过。在独立 `llm/` 目录执行 `go test ./pipeline -count=1` 通过。

前端执行下列命令，35 项通过：

```text
node --test src/features/channels/data/channel-addresses.test.mjs src/features/channels/data/channel-upstream-refresh.test.mjs src/features/channels/data/channel-config.test.mjs src/features/channels/data/channel-query-error.test.mjs src/features/channels/data/protocol-options.test.mjs src/features/channels/channel-priority.test.mjs
```

TypeScript 使用项目编译器 API、`noEmit` 和 HEAD 文件覆盖比较：HEAD 与当前均为 170 项诊断，文件／错误码维度无新增。表单 `modelProtocols.enabled` 输入输出类型问题在 HEAD 已存在，根因文本一致；不把全项目类型检查报告为通过。中英文新增字面量文案键完整，相关文件已格式化。`git -c core.safecrlf=false diff --check` 通过；未运行 lint/build。

验收发现并修正：地址查询片段漏字段导致编辑丢失显示、自定义端点未进入编辑默认值、隐藏控件定位使弹窗外层滚动，以及超时原流程跳过备用地址。完整业务测试还暴露此前用户权限修复的认证测试夹具缺少权限检查依赖和操作者，已补齐测试设置，保留真实缓存失效路径。

## 边界

真实 New API／Sub2API 账号、真实上游故障切换、MySQL／PostgreSQL 实例升级、生产备份恢复、移动端与暗色主题尚未验收。地址切换用于模型请求；账户同步仍使用渠道 Base URL。版本迁移未在生产执行。

临时前后端及模拟上游已关闭，5173／8090／19876 端口已释放。verification-cleanup 返回 `cleaned=true`、`task_root_removed=true`，本次独立演示数据库和测试临时文件已清理，预先存在的 scratch 根目录保留。交付仅保留上述截图、说明和代码。

主要代码：新建 `internal/server/biz/channel_accounts.go`、`channel_addresses.go`、`internal/server/orchestrator/channel_addresses.go`、`internal/objects/channel_accounts.go`、`internal/server/gql/channel_accounts.graphql`、`.syh.5` 迁移、前端账号／地址／接口／凭据组件及相应数据模块。使用说明见 `docs/zh/guides/channel-management.md` 与英文对应文件，维护归属见 `docs/fork-maintenance.md`。
