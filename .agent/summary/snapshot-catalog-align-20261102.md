# 目录与对齐修复快照部署

2026-11-02。Git 推送、快照构建、云悠和泉哥逐台更新、临时配置及被替换镜像清理均已完成。

## 源码与构建

- 修复提交 `7f592ffb2ee03e59f39ba98f8cd65a5ec47277eb` 已推送 `release/v1.0.x`，包含目录获取预算、来源排序、普通名单缓存和渠道用量列对齐。前阶段本地验收与独立复审见[修复记录](catalog-deadline-align-20261102.md)。
- 提交采用 `[skip ci]`，由手动快照工作流集中执行一套云端测试。调用 `gh workflow run snapshot.yml --repo Syh1906/axon-hub --ref release/v1.0.x -f commit=7f592ffb2ee03e59f39ba98f8cd65a5ec47277eb -f base_version=v1.0.0-beta10-fork.7.syh.12`。
- [任务 41422746005](https://github.com/Syh1906/axon-hub/actions/runs/41422746005) 全部成功，包含来源及迁移范围、后端测试、前端单元、发布脚本、amd64/arm64 构建、多架构合成及 PostgreSQL 17 镜像启动／版本／提交检查。
- 基线为已公开正式版 `.syh.12`。源码没有新增 schema/data migration。快照沿用现有工作流，正式版本编号与发布记录继续由正式发版流程维护。
- 程序版本 `v1.0.0-beta10-fork.7.syh.12+snapshot.7f592ffb2ee0.41422746005.1`。
- 快照 ID `snapshot-7f592ffb2ee0-41422746005-1`。
- 固定镜像 `ghcr.io/syh1906/axon-hub@sha256:9691bd45d09c174c10034aff3261fe30aa5c3e18fb75a68becf5b48de0d2fc07`。
- new [snapshot.json](../../output/implementation/snapshot-catalog-align-20261102/snapshot.json)，通过 `gh run download` 保存。文件 SHA-256 为 `a7fdd4cb8575394ba7598b4f06449bebb55396b8117a501189a5c768389d5c6e`。版本、完整提交、基线、两层上游、平台和运行地址均已核对。

## 逐台部署

旧版在线时执行 `docker pull --quiet <固定摘要>`，核对 OCI 版本与提交，保存本轮受限配置副本，更新 `AXONHUB_IMAGE` 并执行以下命令。

```text
docker compose config --quiet
docker compose up -d --no-deps --pull never --wait --wait-timeout 70 axonhub
```

| 目标 | 部署记录完成时间（新加坡） | 重建及健康等待 | 配置副本清理 |
| --- | --- | --- | --- |
| 云悠 | 17:56:02 | 6.460 秒 | 15412 字节 |
| 泉哥 | 17:58:11 | 36.909 秒 | 16218 字节 |

上述耗时统计重建命令及健康等待。实际逐请求中断时长未单独测量。

- 两台应用 healthy、重启数 0，本机和公网健康接口返回完整快照版本与源码提交。首页及两个入口 JS/CSS 资源为 200，初始化 true，匿名 `/v1/models` 和 `/codex/models` 为预期 401，公网 `/metrics` 为 404。
- 两台启动后日志中检查到的 ERROR 均对应本轮两个匿名接口的 401。
- 数据库与其他容器的身份、启动时间、镜像、运行状态和资源配置逐项核对一致。应用内存／交换空间额度、tmpfs、Compose、应用配置、其他环境变量和 Nginx 摘要一致。
- 临时配置副本按固定目录、归属标记、文件类型及摘要核对后逐项清理。本轮按现行规则没有数据库备份，历史数据库及配置备份保持其原有生命周期。
- 云悠额外认证目录复核在读取本机当前选中 provider 的配置时发现缺少 `model_catalog_url`，请求未发出。该项记录为未验证，部署检查按健康、版本、资源和鉴权边界的实际结果记录。真实供应商推理、计费与登录后全量交互未实测。

## 替换镜像清理与收据

沿用用户“不暂留旧镜像”的要求，在两台升级完成后核对全部容器引用及共享标签，执行 `docker image rm ghcr.io/syh1906/axon-hub@sha256:e521f04d75441fb5da3b94cab9a4249f483bf7ef7d98274660fd34d1aa52404c`，删除本次替换的 `.syh.12` 本地镜像。

两台均确认目标镜像已不存在，当前快照容器保持同一身份且健康。可用空间前后各增加 194740224 字节，约 185.72 MiB，在线业务可能影响该差值。旧版本和摘要保留在记录中，可按需重新拉取。

new 两台完整收据保存在私有维护项目 `.local/servers/<服务器>/evidence/snapshot-41422746005-20261102.json`。服务资料、总览、服务索引及远端 `snapshot.json`、`deployment.json`、`upgrade-snapshot-7f592ffb2ee0-41422746005-1.json` 已同步，`release.json` 保存正式基线。
