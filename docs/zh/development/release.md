# 独立维护、同步上游与发版

2026-11-24，云悠、泉哥从 fork.24 更新至 [fork.25 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.25)。两站已核对新增的渠道倍率表、计费字段、索引与各一笔旧用量。应用重建至健康分别用时 6.855 秒和 42.433 秒。切换期间的请求结果及回退条件见[维护台账](../../fork-maintenance.md)。

2026-11-24，云悠、泉哥已部署 [fork.24 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.24)。本次对齐个人和分组用量页的请求日志刷新，无数据库迁移。固定镜像摘要、双站恢复健康时间和业务请求采样见[维护台账](../../fork-maintenance.md)。

2026-11-23，云悠、泉哥依次更新至 [fork.23 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.23)，两站均已验证归档请求索引生效。重建耗时和切换期间的请求结果见[维护台账](../../fork-maintenance.md)。

2026-11-23，泉哥、云悠依次更新到 [fork.22 精简正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.22)。云悠向泉哥的直连 HTTP 渠道已启用跨请求字典压缩。固定摘要、切换窗口和生产观测见[维护台账](../../fork-maintenance.md)。

2026-11-22，云悠和泉哥已部署 [fork.21 精简正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.21)。后台支持上游协议自动择优和请求压缩默认设置，原有固定端点继续优先。双架构镜像摘要、健康检查及切换窗口的请求状态见[维护台账](../../fork-maintenance.md)。

2026-11-21，云悠和泉哥均已更新到 [fork.20 精简正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.20)。请求日志和 API 密钥用量明细分别展示发送档位与计费档位。本版无需数据库迁移，两站的健康检查、在途请求和上线后验证见[维护台账](../../fork-maintenance.md)。

2026-11-21，云悠、泉哥均已更新到 [fork.19 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.19)。本版为慢速大请求自动比较连接协议。两站的镜像、健康检查和切换期间请求状态见[维护台账](../../fork-maintenance.md)。

2026-11-21，云悠、泉哥已部署 [fork.18 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.18)。两站完成会话与思考预算迁移，圆圆端点启用了 HTTP/1.1 长连接及请求压缩。切换时间、在途请求和业务验证见[维护台账](../../fork-maintenance.md)。回切 fork.17 前须按[更新日志](../../../CHANGELOG.md)保护已回填数据。

2026-11-21，云悠和泉哥已更新至 [fork.17 精简正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.17)。两站的版本、镜像摘要、数据库结构和模型卡验收见[部署台账](../../fork-maintenance.md)。

2026-11-20，[rc1-fork.16 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.16) 已通过精简工作流发布并部署至云悠和泉哥。两台本机及公网验收通过。切换时段的请求状态见[部署记录](../../fork-maintenance.md)。

所有发版以本仓库维护分支及该分支最新已发布版本为基线，按当前基线递增 fork 序号。普通发版只核对本仓库分支、标签、发布清单和既有上游来源记录。只有明确要求合并或同步上游时，才单独执行下文的上游合并流程。

2026-11-20，[rc1-fork.15 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.15) 已从本仓库 fork.14 发布并部署至云悠、泉哥。两台均保持官方 Codex 源码为活动目录，版本、健康和资源检查通过。[部署记录](../../fork-maintenance.md)

2026-11-19，[rc1-fork.14 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.14) 已通过精简工作流发布，云悠和泉哥均已更新并通过内外验收。镜像摘要、切换时间及验证范围见 [Fork 维护台账](../../fork-maintenance.md)。

2026-11-19，[rc1-fork.13 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.13) 通过精简工作流发布。云悠和泉哥均已更新至同一固定摘要并通过内外验收。切换时刻、在途请求及首字收益的验证范围见 [Fork 维护台账](../../fork-maintenance.md)。

2026-11-19 发布的 [rc1-fork.12 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.12) 已部署至云悠和泉哥。两机均核对了公网版本、源码提交和镜像摘要。实际切换时间和验收记录见 [Fork 维护台账](../../fork-maintenance.md)。

2026-11-18：[rc1-fork.11 正式版](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.11) 已通过精简工作流发布，云悠和泉哥均从 fork.9 更新至该版，内外健康及版本核对通过。源码为 `0c87d028cceaa9d88fdf5e1a25dceb898544b9e2`，仅提供双架构镜像和 `release.json`。固定摘要、切换时间及验收边界见 [Fork 维护台账](../../fork-maintenance.md)。以下为历史状态。

2026-11-17：当前正式版及云悠／泉哥运行版本为 [v1.0.0-rc1-fork.9](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.9)，源码 `e5d3f1e84e70761a12bd4515d274fae3e345e8ce`。[精简发布 44063632868](https://github.com/Syh1906/axon-hub/actions/runs/44063632868)及同提交 Test／Build／Lint／Govulncheck 全部通过，正式/latest，仅双架构镜像与 `release.json`。翻译刷新修复和浏览器语言默认已上线；两机共 5 个老账号迁移为 `auto`，权限和更新时间保持，后续手选继续保留。

两机内外健康、版本／提交、页面资源、语言迁移和配置保持检查通过，重建及健康等待分别 6.818／7.172 秒。泉哥切换窗口内，云悠 1 条转发流中断失败并出现重试，之后同渠道已有 16 次成功执行，不是零中断部署。生产使用者原始报错现场及全部翻译插件尚未复验。未创建数据库备份，本轮远端临时文件已清理，旧 fork.8 镜像保留；仅换回旧镜像不能恢复迁移前语言偏好。固定摘要与验收边界见 [Fork 维护台账](../../fork-maintenance.md)和[本轮记录（本地）](../../../.agent/summary/release-rc1-fork9-20261117.md)。以下保留历史状态，不代表当前运行版本。

2026-11-17：当前正式版及云悠／泉哥运行版本为 [v1.0.0-rc1-fork.8](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.8)，源码 `100e0c3b75bfabeef92793eea1507bccf233c6a9`。[精简发布 44018089295](https://github.com/Syh1906/axon-hub/actions/runs/44018089295)及同提交 Test／Build／Lint／Govulncheck 全部通过，仅双架构镜像与 release.json。性能优化无新增结构或迁移；两机健康、版本、页面资源、配置及结构保持验证通过，重建及健康等待分别 6.577／7.345 秒。

泉哥切换窗口出现 3 条跨机转发请求的重试告警，随后同渠道恢复成功；另有上游 `invalid_encrypted_content` 失败尚未定因。浏览器无登录态，生产统计页面延迟未验，不把基本健康视为性能已验收。未创建数据库备份，两机本轮配置副本及临时进程已清理，旧 fork.7 镜像暂留。详见 [Fork 维护台账](../../fork-maintenance.md)和[本轮记录（本地）](../../../.agent/summary/release-rc1-fork8-20261117.md)。以下保留历史状态。


2026-11-17：当前正式版及云悠／泉哥运行版本为 [v1.0.0-rc1-fork.7](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.7)，源码 `6a2788f5ca84c8c9b15035dd169221808e71190f`。[精简发布 43927183251](https://github.com/Syh1906/axon-hub/actions/runs/43927183251)及同提交 Test／Build／Lint／Govulncheck 全部通过，仅双架构镜像与 release.json。两机完成统计新表／字段升级，内外健康、页面资源、配置保持检查通过；重建加健康等待分别 6.690／10.264 秒。泉哥切换窗口影响云悠的 3 条转发请求，窗口后同渠道恢复成功，不宣称零中断。

本轮临时配置副本及部署进程已清理，未创建数据库备份；旧正式 fork.6 镜像暂留。固定摘要、CI、迁移与自然流量证据见 [Fork 维护台账](../../fork-maintenance.md)和[本轮记录（本地）](../../../.agent/summary/release-rc1-fork7-20261117.md)。以下段落保留历史状态，不代表当前版本。

2026-11-15：当前正式版及云悠／泉哥运行版本均为 [v1.0.0-rc1-fork.6](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.6)，源码 `269412f52dd18ac31ef113f9018752c251316329`。[精简发布 43639889595](https://github.com/Syh1906/axon-hub/actions/runs/43639889595)与同提交 Test／Build／Lint／Govulncheck 全部通过；`prerelease=false`、`publishBinaries=false`，仅双架构镜像及 `release.json`，无新增迁移或必填配置。两台依次更新、基础验收及短时自然流量观察通过，应用重启数 0，数据库和既有配置保持。

云悠／泉哥重建与健康等待分别 6.797／7.511 秒；观察约 477／264 秒时分别 37／52 条自然请求完成，没有失败请求，另有取消和处理中请求。实际观察到自动试扩容及无收益回收，尚不能证明长期性能改善或原慢请求问题全部消除。两机旧临时快照镜像及云端三个镜像包版本／三个附件已[精确退役](https://github.com/Syh1906/axon-hub/actions/runs/43641274709)，保留 fork.5 正式回退镜像。临时配置副本和部署工具已清理。源码、摘要与详细边界见 [Fork 维护台账](../../fork-maintenance.md)及[本轮部署记录（本地）](../../../.agent/summary/release-rc1-fork6-20261115.md)。以下保留历史记录，不代表当前运行版本。

历史临时部署（2026-11-14，已于 fork.6 退役）：连接池恢复源码 `641410e9` 已通过[快照工作流 43605316825](https://github.com/Syh1906/axon-hub/actions/runs/43605316825)，云悠完成约 100 秒观察后，泉哥使用同一固定摘要更新；两台均通过基础验收。当时运行版本为 `v1.0.0-rc1-fork.5+snapshot.641410e9916d.43605316825.1`，没有创建新 tag／Release，当时正式版本仍为下述 fork.5。见[快照部署记录](../../../.agent/summary/snapshot-connection-20261114.md)。

历史正式版 `v1.0.0-rc1-fork.5`（2026-11-14），大请求分段超时与图片保真优化已完成精简正式发布及云悠／泉哥更新。仅双架构镜像与部署清单，无新增数据库迁移或必填配置；同提交云端检查、镜像启动及双机基础验收通过。泉哥切换窗口曾使一条云悠转发请求收到 502，后续同一路径恢复成功；不宣称零中断。来源、摘要及结果以 [Fork 维护台账](../../fork-maintenance.md) 为准。

历史 `v1.0.0-rc1-fork.4` 的统计加载优化新增可重建统计汇总表，首次历史回填仍有等待。当时泉哥切换命令中断后已恢复，容器运行空档约 6 分 2 秒；这不是本次 fork.5 切换结果。下述 fork.2 段落同样保留其当时验收记录，不能当作当前运行版本。

历史正式版 [v1.0.0-rc1-fork.2](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.2)（2026-11-11），源码 `1c0a65a093f9c356f0005e8b59e13f2c16ffd0ff`。同提交 Test／Build／Lint／Govulncheck 通过，[精简发版 42989955513](https://github.com/Syh1906/axon-hub/actions/runs/42989955513) 成功，正式/latest，仅 amd64／arm64 镜像及 `release.json`。[更新说明](../../../.github/release-notes.md)覆盖后台读取优化、失败定位与快照退役校验修复；相对 fork.1 无后端运行代码、schema/data migration 或依赖变更。

固定镜像摘要为 `sha256:201ff56eb572b667d786825a3f240f05b2ccd730c4b1dc95e002c16e58cf217f`，清单 SHA-256 为 `d1fb3fa182e875827747adeb0367b13716a5c2c9dc44846c64e8ad6fd8cf6f36`。云悠／泉哥分别于新加坡时间 16:58:14／16:59:51 完成基础部署验收；重建及健康等待 6.393／6.960 秒，不等于实测逐请求中断。双机内外健康、版本/提交、初始化、34 项首页资源及匿名鉴权通过；数据库结构摘要、渠道/模型/项目/Key编号集合保持，系统版本更新为 fork.2。

仅更新应用，其他容器、环境、资源、挂载、代理和配置摘要保持，泉哥当天调整的应用 8 GiB／数据库 2 GiB 额度保留。未创建数据库备份，临时配置副本及部署工具已清理，旧正式镜像保留。短时自然流量：云悠 6 完成、1 处理中；泉哥 4 完成、1 取消、6 处理中，采样未见失败或正文超时日志，不构成长时间无故障证明。未复测登录后全部页面、收费供应商、WS端到端或长期负载；历史空正文传输及供应商兼容问题仍未定因／未解决。

本仓库为 Syh1906/axon-hub。beta12 起直接同步原始上游 looplj/axonhub；
ChouUn/axon-hub 是保留历史与功能的继承 fork。应用在 GitHub Actions 构建，镜像保存在 GitHub GHCR，二进制包和
部署清单保存在 GitHub Releases；不需要 Docker Hub 账号。自 `.syh.8` 起默认仅发布双架构镜像与部署清单，跨平台下载包由配置显式开启。

多台生产机各自使用 PostgreSQL、配置和数据卷，按维护记录分批升级。
当前生产更新沿用已有运维项目；私有部署仓库
[axon-hub-ops](https://github.com/Syh1906/axon-hub-ops) 是独立的部署实现，
不是正式发布或快照部署的必需前提。具体主机信息只保存在私有运维资料中。

最新正式版本、源码提交、发布工作流与产物核验结果统一记录在
[Fork 维护台账的发布条目](../../fork-maintenance.md#github-独立发布与两层上游追溯)。
该记录表示应用已发布；生产机是否完成升级以部署仓库的记录为准。

## 分支与来源

| 对象 | 职责 |
| --- | --- |
| origin | Syh1906/axon-hub，自己的代码和发布 |
| upstream | ChouUn/axon-hub，继承 fork 历史与可选补丁来源 |
| original-upstream | looplj/axonhub，直接同步的 release 基线 |
| release/v1.0.x | 自己的长期维护分支 |
| feature/*、fix/* | 二开功能与修复 |
| codex/sync-original-* | 合入原始上游 release 的临时分支 |

从自己的维护分支创建同步分支，再 merge 原始上游的发布 tag。
已发布分支保持连续历史，不 rebase、强推或用上游分支覆盖自己的功能。

首次配置远程时先检查 git remote -v；以下添加命令只在相应名称不存在时执行：

    git remote add upstream https://github.com/ChouUn/axon-hub.git
    git remote add original-upstream https://github.com/looplj/axonhub.git

GitHub CLI 在 fork 中可能默认选择父仓库。对自己的 PR/Release 操作显式传
`--repo Syh1906/axon-hub`；本机也可执行 `gh repo set-default Syh1906/axon-hub`
固定当前 checkout 的默认目标。发布工作流显式使用当前仓库，不向父仓库写入。

## 三层版本与发布清单

release.config.json 保存仓库、发布分支、直接上游 tag/提交、原始上游 tag/提交，以及独立的 prerelease 和 publishBinaries 布尔值。
publishBinaries 为 false 时跳过 GoReleaser 及其前端依赖安装、各平台下载包、校验和与配置 schema 附件，仅保留镜像和 release.json；true 时仍走原完整下载包流程。选择在打新 tag 前写入配置，不能通过重跑修改已公开的版本。
镜像发布仍执行测试、双架构构建与临时 PostgreSQL 启动检查。构建和推送都在 GitHub 云端进行，不在本地打包，也不导出镜像 tar；Docker 构建上下文排除设计图、验收目录和本地演示数据。
仅镜像版本不支持原生二进制下载升级，安装脚本不会获得对应包；此类用户应保留原版或等待明确启用下载包的新版本。GitHub 自动生成源码归档不属于重复构建产物。
当前工作分支的原始基线是 looplj 的 v1.0.0-rc1，对应 d2935453（已发布 rc1-fork.1）；
历史继承 fork 基线是 ChouUn 的 v1.0.0-beta10-fork.7，对应 b205ae8e；beta10 原始基线为 939b2bc0。

beta12 之后自己的版本采用 <原始上游 tag>-fork.N：

| 场景 | 版本示例 |
| --- | --- |
| beta12 基线首次发布 | v1.0.0-beta12-fork.1 |
| 同一原始基线继续二开 | v1.0.0-beta12-fork.2 |
| 后续合入其他 fork 的选定补丁 | v1.0.0-beta12-fork.3 |
| 原始上游将来发布 beta13 并完成合并（示例） | v1.0.0-beta13-fork.1 |

历史 `.syh.N` 仍可读取和追溯，但不再新增。原始上游变更后从 fork.1 开始递增，
同一原始基线内严格递增，不得覆盖或移动已发布 tag。发布工作流只接收带 `-fork.N` 的 tag。

xversion.Parse 供升级检查和数据迁移共同使用，保证下列顺序：

    beta12 < beta12-fork.1 < beta12-fork.2 < beta13 < beta13-fork.1

以上省略共同的 v1.0.0- 前缀。不要改成会越过未来上游迁移的独立大版本。
新增自己的数据迁移使用其首次发布的完整 `-fork.N` 版本；保持所有已发布迁移不可变，
同时核对直接上游和原始上游新增迁移。程序当前用整体系统版本判断数据迁移执行，
因此新迁移必须晚于已部署版本，不能在旧版本号下补迁移。

Release 附件 release.json 包含 schemaVersion、version、repository、commit、
upstream、originalUpstream、inheritedFork、image、platforms 和 runURL。image 固定多架构摘要，
例如 ghcr.io/syh1906/axon-hub@sha256:…；tag 只用于标记版本，部署以摘要为准。

## 合入原始上游

先核对目标 release tag 的真实性，并对照 [beta12 合并台账](../../fork-maintenance.md#v100-beta12原始上游合并目标-beta12-fork1)。以下用尚未确认发布的 beta13 作为操作示例：

    git switch release/v1.0.x
    git fetch original-upstream tag v1.0.0-beta13
    git switch -c codex/sync-original-beta13
    git merge --no-ff --no-commit v1.0.0-beta13

合并保留两条父历史。beta12 的合并提交为 `50bc66fa`，后续通过原始上游 tag 比较新增范围，不重新重放 beta10 的全部 fork 改动。

1. 逐项核对 [Fork 维护台账](../../fork-maintenance.md)，处理冲突并保留仍有效的自有功能。
2. 检查原始上游新增表结构迁移、数据迁移与已有 fork 迁移、配置和缓存变化；
   新上游迁移不能因为自己的版本编号被跳过。
3. 更新 release.config.json 的 upstream 和 originalUpstream，inheritedFork 保留继承来源。
   用 git rev-parse '<tag>^{commit}' 取得提交号，不根据版本名猜测。
4. 更新 .github/release-notes.md，列出新增功能、数据库/配置变化、支持的升级路径、
   真实验收结果及回退条件；同步维护台账。
5. 运行相关检查。PR 和分支 CI 执行检查；正式发布核对同一提交在维护分支上的成功 Test 记录，不重复运行测试。
6. 推送同步分支并创建 PR，审阅后合回自己的维护分支，再独立打 fork.N tag。

同步上游不会自动发版或部署。不要使用 git push --mirror 或把所有历史 tag 一次推到 origin。

## 首次启用 GitHub 发布

1. 在本仓库 Actions 中启用 fork 的工作流。
2. 确认仓库策略允许 GITHUB_TOKEN 写 contents 和 packages，能够创建 Release、
   推送本仓库镜像。不需要复制或提供个人明文令牌。
3. 首次镜像发布后，在 GitHub Packages 核对来源仓库与可见性。
   公开镜像无需生产机登录；私有镜像需在各生产机预先配置只读拉取权限。
4. 原始上游的 Helm 工作流仍只对原作者仓库运行；本 fork 停用旧的每日 unstable 镜像，
   模型目录定时同步也只在原始上游运行。需要引入目录变化时按正常同步和发版流程处理。
5. 二进制继续沿用上游 GoReleaser 构建；移除向上游 Homebrew 仓库写入的步骤。
   安装、升级脚本及程序内更新检查指向 Syh1906/axon-hub。

检查项的分支触发仍覆盖 release/**。项目禁止未经明确请求在本地额外运行 lint/build，
该约束不因文档示例或配置修改而消失。

## 发布一个版本

先更新本次发布说明并推送维护分支，等待该提交的 Test 成功，再创建 tag。发布流程按完整提交号、维护分支和 push 事件读取最新测试记录；缺失、未完成或失败时停止发布，不自动重跑。以下是首次发布示例，不是自动执行指令：

    git tag -a v1.0.0-beta12-fork.1 -m "release: v1.0.0-beta12-fork.1"
    git push origin v1.0.0-beta12-fork.1

工作流顺序：

1. 检查 tag 编号、仓库、维护分支包含关系及直接上游基线。
2. 复用同一提交的维护分支 Test，覆盖两个 Go 模块、前端单元测试和发布脚本测试，发布摘要记录原测试地址。
3. 在 amd64、arm64 原生执行器构建同一源码并写入版本和提交号；
   推送各架构镜像，合成 ghcr.io/syh1906/axon-hub 的版本镜像。
4. 拉取固定摘要的镜像，连接临时 PostgreSQL 17，做启动和自报版本检查。此检查不代表生产数据升级或业务验收。
5. publishBinaries=true 时由 GoReleaser 构建下载包、校验和及配置 schema 并上传草稿；否则直接创建镜像发布草稿，省去重复构建。没有反向提交版本文件的步骤。
6. 生成并上传 release.json，写入本次发布说明，以完整版本号为标题，按 prerelease 配置正式／预发布。

正式发布默认使用 prerelease: false，并设为 GitHub 最新版本；测试发布明确设为 true，且不成为最新正式版。
上游 tag 中的 beta/rc 只表示来源，不决定自己的发布级别。更新检查按 GitHub 的 prerelease 字段筛选；
“包含预发布版本”开启时才纳入测试发布。旧版程序的识别修正随下一次程序发版生效。
发布级别调整不替换已有镜像或 tag；生产仍固定镜像摘要。

中途失败不会把不完整版本公开为正式 Release。检查工作流记录后，可重试失败任务：
重跑二进制发布前先核对现有草稿资产；已经公开的版本禁止重跑覆盖，使用新 fork.N 编号。
镜像构建中途可能已有某架构 tag，发布成功与否以完整 Release 清单为准。

## 多台生产机部署

在实际使用的维护项目中配置真实主机清单、专用 SSH 密钥和已核对的主机指纹。
不在应用仓库提交主机凭据、数据库密码或 API Key。

选择已核验的产物，先更新一台并验收，通过后用同一份清单及镜像摘要逐台更新。
使用私有 ops 自动流程时，canary 成功后才以同一成功任务编号运行 production，
并校验版本、摘要、ops 代码与主机清单一致。手动流程分别保存各台验收收据。
同一台没有版本变化时只验证，不重复进入维护和备份。

每台在旧版在线时拉取固定摘要镜像、核对迁移和回退兼容性，并临时保存配置；
只修改 AXONHUB_IMAGE，准备就绪后才短时重建应用。Axon 维护更新不创建数据库
备份，包括全库及部分表导出；不更新数据库镜像，不重建数据卷。部署验收通过后
删除本轮临时配置副本，只保留非敏感版本、摘要和验收收据。历史副本清理另行授权。

迁移与验证失败时停止后续机器，保留现场；不自动恢复数据库，也不把更换旧镜像
视为完整回退。业务请求、真实计费和网络可达性按实际验收范围记录；本流程未建立
数据库恢复点。具体目录、执行状态与回退限制以实际维护仓库及服务器资料为准。
私有 ops 自动流程如仍强制导出数据库，不用于本轮维护，也不能把本文当作其已修改的证据。

## 按需快照镜像

快照使用与正式发布相同的版本格式和来源约束：基线 tag 必须同时包含原始上游与继承 fork；源码历史已包含更晚的 fork.N 时，不能继续以旧编号生成快照。历史 ChouUn 配置的 .syh.N 快照校验仍保留。

发布编号下限仅约束所选基线；检查已合入的历史 tag 时允许更早编号，仍拒绝源码已包含更晚版本却选择旧基线。

少量改动需要提前部署时，在维护分支的 Actions 中手动运行“按需构建快照镜像”
（`.github/workflows/snapshot.yml`）。输入完整 40 位源码提交号 `commit` 和已公开的
正式基线 `base_version`，例如已发布的历史版本 `v1.0.0-beta10-fork.7.syh.19`（beta12 发布后再选对应正式版本）。
不创建 Git tag、Release 或新的发布编号，也不自动部署生产机。
手动入口要求工作流存在于 GitHub 默认分支；本仓库默认分支为 `release/v1.0.x`。

工作流核对来源仓库、维护分支、两层上游和基线祖先关系；源码已包含更晚编号时拒绝
沿用旧基线。普通快照拒绝基线以来 `internal/ent/schema/` 或
`internal/ent/migrate/` 的变化；这不代替对业务持久写入、配置及回退兼容性的人工审阅。
涉及迁移或不兼容数据变化时另定升级方案，不套用本入口。

测试和双架构构建均固定到输入提交；临时 PostgreSQL 启动、版本及提交标签检查通过后，
保存 `snapshot.json` 为 Actions 附件。程序版本为
`<正式基线>+snapshot.<12 位提交号>.<运行编号>.<重跑次数>`；镜像标签为
`snapshot-<12 位提交号>-<运行编号>-<重跑次数>`，重跑产生独立身份。
构建标识不改变正式版本排序，下一正式版本仍可正常识别。镜像标签和程序版本分别传入，
不覆盖正式版本镜像；部署始终固定摘要。构建中途失败可能留下镜像，但没有成功清单时
不得据此认定可部署。失败后选择重跑整个工作流，重新准备独立身份；
仅重跑失败任务会因沿用旧快照身份被拒绝，避免覆盖原产物。

`snapshot.json` 包含 `kind: snapshot`、`snapshotId`、正式基线及提交、源码提交、
程序版本、两层上游、固定镜像摘要、三个镜像标签、架构及运行地址。部署前核对构建成功
与清单一致，将清单保存至维护资料，不能只依赖可能到期的 Actions 附件。
各台分别记录实际镜像和验收结果；沿用上一节的在线准备、短时应用重建、不备份数据库
及失败停止推进的规则。健康检查不代表真实供应商请求或计费已验证。

### 手动退役与回收

快照是版本间临时产物，不作为长期归档。由用户需要时发起手动清理，
不添加定时检测、正式发版后的自动检测或删除钩子。
正式 Release 发布成功不等于所有服务器已切换：清理时核对实际运行与回退引用，
只退役已被正式版或新快照替代、各目标部署验收通过且无剩余引用的旧快照。

列出服务器本地镜像、GHCR 多架构镜像及 amd64/arm64 子镜像、Actions 附件的实际目标，
按现有删除授权回收。删除前检查所有容器引用和共享摘要，不按前缀或时间一刀切，
不删除关联的正式镜像。仍使用、需要回退或用途未明的快照保留并注明原因。
保留不含凭据的提交、摘要、构建地址、部署及回收收据；未清理项如实记录。

正式版或新快照替换旧快照时，可手动运行 `retire-snapshot.yml`，输入 `old_run` 和已经完成所有
目标部署验收的 `replacement_run`。正式版须为成功的 release 工作流及已公开的非预发布版本，接受当前 `-fork.N` 和历史 `.syh.N`，
并校验 `release.json` 的版本、提交、双架构和构建地址。默认只列出精确计划；确认授权和运行／回退引用后，
以 `execute=true` 执行。此入口不连接服务器，服务器核对和本地镜像删除仍由维护流程完成。
它沿用发布时的仓库 `GITHUB_TOKEN` 管理本仓库关联的 GHCR 包，不要求扩大个人令牌范围。
只删除旧清单的三个精确镜像标签对应的包版本，以及旧任务的清单和 `.dockerbuild`
附件；共享其他标签或与替代镜像同摘要时拒绝删除。保留原工作流历史和退役收据，
部分失败时记录已完成项并停止，不自动重试或扩大删除范围。
