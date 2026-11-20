# v1.0.0-rc1-fork.16

从本仓库 fork.15 升级。正式发布提供 Linux amd64、arm64 镜像及固定镜像摘要清单 `release.json`。

- 项目服务账号可用 `read_requests` 查询本项目的请求及执行尝试。读取关联渠道名称还需 `read_channels`，调用模型仍需 `write_requests`。见 [#7](https://github.com/Syh1906/axon-hub/issues/7)。
- Gemini 原生路径和 `/v1beta` 支持按列表名称读取模型详情，遵循相同的模型可见范围，保留列表尾斜杠跳转。见 [#8](https://github.com/Syh1906/axon-hub/issues/8)。
- 单笔请求平均输出速度改用首个输出到结束的时长，按 vLLM 公式计算。Codex 模型与速度档按可用路线筛选，保留目录覆写。
- 本版无数据库结构或数据迁移。生产部署使用清单中的固定摘要，可切回 fork.15 镜像。
