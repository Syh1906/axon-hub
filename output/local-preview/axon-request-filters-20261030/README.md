# 请求筛选阶段：本地人工验收

2026-10-30，用户已完成人工验收，前后端已关闭，数据库保留。这里使用独立 SQLite 示例库，不影响原有环境，也不连接真实供应商。示例请求为构造的历史记录；真实重试与断流行为由自动化测试验证。

- 请求页：http://127.0.0.1:5173/project/requests
- 分组页：http://127.0.0.1:5173/project/key-groups
- 渠道页：http://127.0.0.1:5173/channels
- 后端：http://127.0.0.1:8090
- 公开测试账号：`my@example.com`，密码：`pwd123456`。仅供本机示例库使用。
- 报表时区：`Asia/Shanghai`。

## 示例内容

今天（数据生成日 2026-10-30）24 条、昨天 2 条、更早 1 条。包含成功、失败重试、缺失历史诊断、长标签、未分组、研发团队、停用 Key 和空分组。跨日后可选指定日期或全部查看。

可优先查看：今天/昨天切换、标签与分组组合筛选、失败记录中的逐次错误和调用过程、分组直接打开用量、空组及停用 Key 历史、渠道官网编辑后回读。占位渠道处于停用状态，不用于真实推理请求。

## 启停与数据

当前已停止，需要重新启动时，在两个 PowerShell 窗口分别执行：

```powershell
& F:/GitProject/axon-hub/output/local-preview/axon-request-filters-20261030/run-backend.ps1
& F:/GitProject/axon-hub/output/local-preview/axon-request-filters-20261030/run-frontend.ps1
```

停止本目录启动的前后端及子进程：

```powershell
& F:/GitProject/axon-hub/output/local-preview/axon-request-filters-20261030/stop.ps1
```

停止脚本核对 PID、启动时间和程序路径，避免终止被复用 PID 对应的其他进程。它不删除数据。`runtime/preview.db`、进程记录和配置保留且被 `.gitignore` 忽略；两服务仅监听 `127.0.0.1`。

首次空库可执行 `seed-demo.ps1`（需要 Python）创建上述示例；已完成的库直接保留，脚本不覆盖人工修改。

## 验证记录

[阶段实现与复审记录](../../implementation/axon-request-filters-20261030/verification.md)。本地页面可供人工确认，不表示真实供应商、生产环境或其他数据库方言均已验证。

当前浏览器环境存在强制 UTC 的注入脚本，可能让时间列与“报表时区”标签不一致。后端筛选按 Asia/Shanghai 执行；时间列需在未强制改写时区的浏览器环境中验收。已保留具体证据于上面的复审记录，没有更改浏览器设置。
