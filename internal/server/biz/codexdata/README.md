# 官方 Codex 发布快照

这里保存 OpenAI 开源 Codex 的完整 `models.json`，供没有完整上游目录时使用。
文件按 `rust-v<客户端版本>` 发布 tag 获取，保留原始字节；本目录 `.gitattributes` 禁止 JSON 换行转换，避免 Windows 检出改变摘要；来源与 SHA-256 见 `manifest.json`，许可证见 `LICENSE`。

内置版本：0.166.0、0.167.0。当前两份官方文件内容相同，均为 12 个型号。
这属于发布快照，不代表官方认证接口的实时目录，也不授予模型调用权限。

运行时按请求的客户端版本获取对应公开 tag，验证后按版本归档；不会抓取 `main`，不会携带渠道凭据。
归档不存在或获取失败时，只使用不高于客户端版本的已验证快照。固定版本不自动漂移。
新增内置版本时同时更新 manifest，并运行 `TestCodexSnapshotsBundledAndArchive`。
