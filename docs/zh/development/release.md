# 独立维护、同步上游与发版

本仓库为 Syh1906/axon-hub。直接上游为 ChouUn/axon-hub，原始上游为
looplj/axonhub。应用在 GitHub Actions 构建，镜像保存在 GitHub GHCR，二进制包和
部署清单保存在 GitHub Releases；不需要 Docker Hub 账号。

多台生产机各自使用 PostgreSQL、配置和数据卷，通过私有部署仓库
[axon-hub-ops](https://github.com/Syh1906/axon-hub-ops) 分批升级。
该仓库只有获得授权的账号可访问。

## 分支与来源

| 对象 | 职责 |
| --- | --- |
| origin | Syh1906/axon-hub，自己的代码和发布 |
| upstream | ChouUn/axon-hub，直接跟踪的维护版本 |
| original-upstream | looplj/axonhub，核对原始发布、迁移和来源 |
| release/v1.0.x | 自己的长期维护分支 |
| feature/*、fix/* | 二开功能与修复 |
| sync/chouun-* | 合入某个 ChouUn 发布的临时分支 |

从自己的维护分支创建同步分支，再 merge 直接上游的发布 tag。
已发布分支保持连续历史，不 rebase、强推或用上游分支覆盖自己的功能。

首次配置远程时先检查 git remote -v；以下添加命令只在相应名称不存在时执行：

    git remote add upstream https://github.com/ChouUn/axon-hub.git
    git remote add original-upstream https://github.com/looplj/axonhub.git

GitHub CLI 在 fork 中可能默认选择父仓库。对自己的 PR/Release 操作显式传
`--repo Syh1906/axon-hub`；本机也可执行 `gh repo set-default Syh1906/axon-hub`
固定当前 checkout 的默认目标。发布工作流显式使用当前仓库，不向父仓库写入。

## 三层版本与发布清单

release.config.json 保存仓库、发布分支、直接上游 tag/提交、原始上游 tag/提交，以及独立的 prerelease 布尔值。
当前直接基线是 ChouUn 的 v1.0.0-beta10-fork.7，对应 b205ae8e；
原始基线是 looplj 的 v1.0.0-beta10，对应 939b2bc0。

自己的版本采用 <直接上游 tag>.syh.N：

| 场景 | 版本示例 |
| --- | --- |
| 当前代码首次独立发布 | v1.0.0-beta10-fork.7.syh.1 |
| 同一直接基线继续二开 | v1.0.0-beta10-fork.7.syh.2 |
| 合入 ChouUn 的 fork.8 后首次发布 | v1.0.0-beta10-fork.8.syh.1 |
| 直接上游以后基于 beta11 发布 fork.1 | v1.0.0-beta11-fork.1.syh.1 |

后两行是规则示例，不表示这些上游版本已经存在。不自行递增 ChouUn 的 fork.N，
也不把自己的版本命名成他的版本。换直接基线后从 syh.1 开始；同一基线内严格递增，
不得覆盖或移动已发布 tag。发布工作流只接收带 .syh.N 的 tag。

xversion.Parse 供升级检查和数据迁移共同使用，保证下列顺序：

    beta10-fork.7 < beta10-fork.7.syh.9 < beta10-fork.7.syh.10
    < beta10-fork.8 < beta10-fork.8.syh.1 < beta11-fork.1.syh.1

以上省略共同的 v1.0.0- 前缀。不要改成会越过未来上游迁移的独立大版本。
新增自己的数据迁移使用其首次发布的完整 syh 版本；保持所有已发布迁移不可变，
同时核对直接上游和原始上游新增迁移。程序当前用整体系统版本判断数据迁移执行，
因此新迁移必须晚于已部署版本，不能在旧版本号下补迁移。

Release 附件 release.json 包含 schemaVersion、version、repository、commit、
upstream、originalUpstream、image、platforms 和 runURL。image 固定多架构摘要，
例如 ghcr.io/syh1906/axon-hub@sha256:…；tag 只用于标记版本，部署以摘要为准。

## 合入直接上游

以目标 v1.0.0-beta10-fork.8 为示例，实际执行前先确认它已发布：

    git switch release/v1.0.x
    git pull --ff-only origin release/v1.0.x
    git fetch upstream
    git fetch original-upstream
    git switch -c sync/chouun-v1.0.0-beta10-fork.8
    git merge --no-ff v1.0.0-beta10-fork.8

1. 逐项核对 [Fork 维护台账](../../fork-maintenance.md)，处理冲突并保留仍有效的自有功能。
2. 检查原始上游及 ChouUn 的表结构迁移、数据迁移、配置和缓存变化；
   新上游迁移不能因为自己的版本编号被跳过。
3. 更新 release.config.json 的直接基线。原始基线仅在确实升级时更新。
   用 git rev-parse '<tag>^{commit}' 取得提交号，不根据版本名猜测。
4. 更新 .github/release-notes.md，列出新增功能、数据库/配置变化、支持的升级路径、
   真实验收结果及回退条件；同步维护台账。
5. 运行相关检查。PR 的现有 CI 执行检查，正式发布还会在同一 tag 上重新运行测试。
6. 推送同步分支并创建 PR，审阅后合回自己的维护分支，再独立打 syh tag。

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

先更新本次发布说明并确认维护分支检查通过；以下是首次发布示例，不是自动执行指令：

    git tag -a v1.0.0-beta10-fork.7.syh.1 -m "release: v1.0.0-beta10-fork.7.syh.1"
    git push origin v1.0.0-beta10-fork.7.syh.1

工作流顺序：

1. 检查 tag 编号、仓库、维护分支包含关系及直接上游基线。
2. 调用现有测试工作流，包括两个 Go 模块、前端单元测试和发布脚本测试。
3. 在 amd64、arm64 原生执行器构建同一源码并写入版本和提交号；
   推送各架构镜像，合成 ghcr.io/syh1906/axon-hub 的版本镜像。
4. 拉取固定摘要的镜像，连接临时 PostgreSQL 17，做启动和自报版本检查。此检查不代表生产数据升级或业务验收。
5. GoReleaser 构建下载包、校验和及配置 schema，上传草稿；没有反向提交版本文件的步骤。
6. 生成并上传 release.json，写入本次发布说明，以完整版本号为标题，按 prerelease 配置正式／预发布。

正式发布默认使用 prerelease: false，并设为 GitHub 最新版本；测试发布明确设为 true，且不成为最新正式版。
上游 tag 中的 beta/rc 只表示来源，不决定自己的发布级别。更新检查按 GitHub 的 prerelease 字段筛选；
“包含预发布版本”开启时才纳入测试发布。旧版程序的识别修正随下一次程序发版生效。
发布级别调整不替换已有镜像或 tag；生产仍固定镜像摘要。

中途失败不会把不完整版本公开为正式 Release。检查工作流记录后，可重试失败任务：
重跑二进制发布前先核对现有草稿资产；已经公开的版本禁止重跑覆盖，使用新 syh 编号。
镜像构建中途可能已有某架构 tag，发布成功与否以完整 Release 清单为准。

## 多台生产机部署

在私有 ops 仓库配置真实主机清单、专用 SSH 密钥和已核对的主机指纹。
不在应用仓库提交主机凭据、数据库密码或 API Key。

选择发布版本，先运行 canary；首台验收通过后，用该成功任务编号运行 production。
生产阶段校验首台使用的版本、摘要、ops 代码与主机清单完全一致，再逐台执行。
同一台没有版本变化时只验证，不重复进入维护和备份。

每台仅提前拉取应用镜像、保留现有配置、停应用写入、备份 PostgreSQL、
修改 AXONHUB_IMAGE 并更新应用服务；不更新数据库镜像，不重建数据卷。
备份或验证失败时停止后续机器，保留现场；不自动恢复数据库。
业务验收、恢复演练和实际网络可达性必须在接入真实机器后验证。

具体 secrets、目录、备份状态及回退步骤以私有 ops 仓库 README.md 为准。
