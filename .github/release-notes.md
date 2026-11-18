# v1.0.0-rc1-fork.10

基于 looplj/axonhub 的 v1.0.0-rc1，比较基线为本 fork 的 rc1-fork.9。提供 Linux amd64 和 arm64 镜像，固定摘要见 release.json。

- 原生 Anthropic `POST /v1/messages/count_tokens` 返回所选供应商的输入 Token 计数。沿用模型、渠道权限和并发控制；不发起生成、不产生生成消费。需要支持计数的 Anthropic 端点；不支持 Bedrock 原生计数。
- 旧版 OpenAI Completions 的文本或 Token 批次按原形交给供应商。流式的多条选择分别聚合；缺失用量保持未知。Jina 重排只返回总 Token 时，按输入用量记录，显式零与未报告仍有区分。
- 原生流式音频保留供应商的结束或中断信号。已开始传送的二进制在上游中断时会报告读取错误；未完整分隔或类型冲突的音频事件不会被补成成功。

从 rc1-fork.9 升级不增加数据库迁移或必填配置。回退到该版本的固定镜像会恢复原有协议行为。本次发布不自动更新运行中的实例；真实供应商能力仍需按渠道验证。
