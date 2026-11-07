# 独立维护、同步上游与发版

当前正式版为 [`v1.0.0-beta12-fork.1`](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-beta12-fork.1)（2026-11-07），发布源码 `aed80ce3`；双架构镜像及清单已核验，双机已完成部署验收。缓存完整证据链及真实业务未验边界继续以台账为准。

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
当前原始基线是 looplj 的 v1.0.0-beta12，对应 2b1a0f93；
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
目标部署验收的 `replacement_run`。正式版须为成功的 release 工作流及已公开的非预发布版本，
并校验 `release.json` 的版本、提交、双架构和构建地址。默认只列出精确计划；确认授权和运行／回退引用后，
以 `execute=true` 执行。此入口不连接服务器，服务器核对和本地镜像删除仍由维护流程完成。
它沿用发布时的仓库 `GITHUB_TOKEN` 管理本仓库关联的 GHCR 包，不要求扩大个人令牌范围。
只删除旧清单的三个精确镜像标签对应的包版本，以及旧任务的清单和 `.dockerbuild`
附件；共享其他标签或与替代镜像同摘要时拒绝删除。保留原工作流历史和退役收据，
部分失败时记录已完成项并停止，不自动重试或扩大删除范围。
