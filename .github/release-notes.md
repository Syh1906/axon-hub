# v1.0.0-rc1-fork.15

从本仓库 rc1-fork.14 升级。本次精简正式发布提供 Linux amd64、arm64 镜像及部署清单 `release.json`。

- 在「集成与兼容 → Codex 兼容」导入本机快照或订阅公开 JSON 地址，预览通过后应用。目录 URL 预填[本仓库模型目录](https://raw.githubusercontent.com/Syh1906/axon-hub/refs/heads/release/v1.0.x/catalogs/codex/models.json)。官方源码、URL 订阅和本机快照互斥；固定模型定义、字段覆写和渠道限制继续生效。
- Codex 选择器保留模型展示名。兼容旧客户端的内置或已归档发布快照可离线使用；订阅检查能识别最低客户端版本变化，共享数据库的过期检查结果无法覆盖新来源。
- 完善请求列表和自助用量页的首字与平均输出速度统计，改进 Responses 断线续传、历史恢复与重复派发处理；结束帧缺少输出时，可用此前已确认完成的输出项恢复续传历史，并区分 Alpha Search 缺少搜索端点与模型不可用。
- 数据库为 requests 增加可空的 stream_timing 字段。旧记录继续可读，无新增必填配置。多副本部署应升级所有应用进程后再切换目录来源。
