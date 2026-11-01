# 第五版发布结果

2026-10-29 03:34:53（北京时间）已正式发布 `v1.0.0-beta10-fork.7.syh.5`，GitHub 标记为最新正式版，非草稿、非预发布。

- 发布页面：https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-beta10-fork.7.syh.5
- 源码提交：`9d4487538976d4014915a6a0b52dcf546f0c16a7`，已推送至 `release/v1.0.x`。
- 版本文件：`internal/build/VERSION`；完整发布说明：`.github/release-notes.md`。
- 内容包括此前未发布的多账号／多地址、角色与用户授权修复，以及本阶段默认标签、Key 用量入口、批量关联、匹配类型和悬浮稳定性改动。
- 正式发布工作流全部成功：https://github.com/Syh1906/axon-hub/actions/runs/40750365442
- 维护分支 Test、Lint、Build 均成功，运行号分别为 `40749673964`、`40749674080`、`40749673969`。

## 产物核对

12 个附件已上传：9 个跨平台 ZIP、`checksums.txt`、`config.schema.json`、`release.json`。9 个 ZIP 在校验和文件中的哈希全部与 GitHub 附件摘要一致；下载的清单、校验和文件及配置 schema 的实际字节 SHA-256 与 GitHub 摘要一致。未重新下载全部 ZIP 或在本机逐个运行。

发布清单版本、源码提交、两层上游基线、构建地址及 `linux/amd64`、`linux/arm64` 均与本次发布一致。固定镜像：

```text
ghcr.io/syh1906/axon-hub@sha256:23e041a3abc9fe9ad3ffa3d5f69c4284c35dfb2300aa7ed1e5a9a644ead52caa
```

工作流已实际拉取固定摘要镜像，连接临时 PostgreSQL 17 验证健康及自报版本。没有升级生产机器；生产仍应先备份，再通过私有部署仓库首台验收。

## 执行与检查

- 本地前端 `node --test "src/**/*.test.mjs"`：129/129。
- 本地 `node --test scripts/release/release.test.mjs`：3/3；夹具写入本次受管临时目录。
- 本地 `./scripts/release/version.test.ps1`：6/6。
- `git -c core.safecrlf=false diff --check` 及暂存差异检查通过。
- `git commit -m 'feat: 完善渠道模型交互并准备第五版发布'`。
- `git push origin HEAD:release/v1.0.x`。
- `git tag -a v1.0.0-beta10-fork.7.syh.5 -m 'release: v1.0.0-beta10-fork.7.syh.5'`。
- 调用现有 `prepareRelease` 校验 tag、分支包含关系、版本顺序和来源后，执行 `git push origin v1.0.0-beta10-fork.7.syh.5`。
- 用 `gh run view`、`gh release view` 和 `gh release list` 核对工作流、附件和最新正式版状态。
- 未运行本地 lint/build；远端现有流程执行并通过。此前 172 项既有 TypeScript 诊断仍是已知限制。

本地 `design-qa.md`、`output/` 中的预览、演示数据及运行资料未提交，现有本地开发服务未重启。真实上游账号、跨方言存量升级、生产恢复以及本阶段尚未覆盖的边界详见发布说明和同目录 `review.md`。
