## [Unreleased]

### Added

- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.

## [v1.0.0-beta10-fork.7.syh.12] - 2026-11-02

### 修复

- Codex 目录默认根据模型配置和 Key 权限自动生成，优先复用相同实际型号资料，缺资料的别名按模型页关联继承，保留已知能力限制并随客户端版本刷新；手动 JSON 改为可选覆盖。
- Responses 自定义工具请求排除不兼容协议渠道，工具执行结果保留 `name`，修复仅有工具名称、没有 `call_id` 时转发失败。

- Codex 目录导入按每个公开型号保存独立指令和客户端工具设置，避免同一正则规则下新旧型号及不同来源声明的差异阻止预览。
- 来源差异列出待管理员核对的字段；上下文等能力仍按 Key 可见路线合并，Key 别名使用映射后的目标型号选择定义。
- 客户端工具输出截断策略不再与渠道上下文限制混合；旧单模型定义和既有路线快照继续可读，无 SQL 迁移。
- 明确 Codex 完整目录必须使用 /codex/models 的 models 结构；普通 /v1/models 的 data 名单不可替代。目录确认不能转换不兼容的推理工具协议。

## [v1.0.0-beta10-fork.7.syh.11] - 2026-11-02

### 修复

- 多渠道 Codex 模型目录支持保存管理员确认的完整定义及渠道绑定，按当前项目和 Key 可见路线合并能力，不再要求每个渠道都提供目录；隔离坏记录并校验已知字段及嵌套消息。
- 修复统计页缓存重入空白、列表回顶闪色和请求行悬浮反馈，统一表格交互；采用 moego 优化局部图片编解码和请求字段处理。

### 新增

- 模型编辑提供按客户端版本导入、确认、保存回读及清除目录定义；取消旧请求，避免关闭重开后状态被覆盖。
- 成本与额度展示小数位数可配置；只改变显示，不改变结算金额。
- 按需快照构建及精确手动退役，支持已验收正式版本替代旧快照，保留共享镜像保护及回收收据。

### 发布与升级

- 双架构 GHCR 镜像及 release.json 精简发布，不生成独立二进制包；直接上游与原始上游基线不变。
- 相对 .syh.10 无 schema/data migration 变化；目录定义写入既有 settings JSON。定义按精确 clientVersion 确认，客户端升级后需重新导入，缺少目录的渠道由管理员核实能力。
- 生产部署与快照退役分别验收，不将云端启动检查当作真实供应商推理或计费验证。

## [v1.0.0-beta10-fork.7.syh.9] - 2026-11-01

### Added

- Storage retention controls, resumable cleanup with preserved usage summaries, capacity reporting, and controlled database transfer/export tools.
- Read-only historical fixed/image cost previews using frozen prices and retained evidence; missing usage remains unknown.
- Valid text-speed sample counts and consistent row, menu and candidate-list hover feedback.

### Fixed

- Image pricing can use explicit outgoing request dimensions when output dimensions are unavailable, with unconfirmed evidence shown separately.
- Flat fees work without token usage and preserve cache-duration-specific pricing; historical previews retain five-minute and one-hour cache quantities.
- Request logs, consumption rankings, model analytics and performance queries share the CCH per-request speed average, including measured failed/canceled streams and deterministic final-attempt selection.
- Incremental request/channel loading, refresh state, detailed daily channel usage and matched-model price inheritance.

### Release

- Cloud-only amd64/arm64 GHCR images and release.json; no cross-platform binary packages or image archives uploaded from local machines.
- Ent adds usage archives, pricing completeness/speed-version fields and a request-execution cleanup index. Existing saved retention policies remain unchanged; back up before upgrading from .syh.8.
- Historical production bills are not rewritten. Cleanup, database transfer and production deployment remain separately controlled operations.

## [v1.0.0-beta10-fork.7.syh.8] - 2026-10-31

### Added

- Standard/Fast/Ultrafast pricing with official references, per-item multiplier or unit-price overrides, long-context rules, frozen evidence and consistent budget settlement.
- Full Codex model catalogs, client-aware HTTP/WebSocket transport, and complete multi-turn Responses history recovery.
- Inline group/key editing, dashboard period costs, channel daily usage, project read visibility, incremental request/channel lists and refresh controls.
- New-input token details, mode/context cost badges, whole-row expansion, grouped leaderboard defaults and channel cache-ranking costs.

### Fixed

- Regex and prefix model-price matching, matched output limits for budget admission, zero-price preservation and stale price-editor responses.
- Truncated Responses history, unsafe replay after uncertain sends, terminal service-tier parsing and local budget-error attribution.
- Duplicate usage counts, cache-hit ratios, SQLite day boundaries, group request-log GraphQL evidence, date editing and interrupted operation recovery.

### Release

- GitHub Actions publishes amd64/arm64 GHCR images plus release.json by default; cross-platform binaries are optional via publishBinaries and omitted for this version.
- No new database migration; back up before upgrading from .syh.7. Older binaries do not understand the new pricing rules. See the release notes for verification limits.

## [v1.0.0-beta10-fork.7.syh.7] - 2026-10-30

### Added

- Request log calendar and second-precision filters, combined channel/tag/key/group filtering, and persisted URL selections.
- Per-attempt routing, credential hints, sanitized failure evidence, and actual retry/stop decisions; optional channel website links.
- Project API key groups with bulk operations, resumable receipts, direct group usage, and shared key/group monetary budgets.
- Per-image size-based pricing, official DeepSeek/Moonshot wallet queries, and saved default provider filtering.

### Fixed

- Reporting timezone access for project request readers, stale key hints across credential types, and missing stop reasons after streaming output.
- Account balance refresh coupling with key assignment lookups, multi-currency aggregation, and current-page refresh scope.

### Upgrade

- Ent manages new group, operation receipt and budget tables plus nullable request diagnostics and website fields; no new data migration version.
- Backup format 1.6 preserves budgets and group state; older versions reject the new format. Back up the database and configuration before upgrading.
- See [.syh.7 release notes](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-beta10-fork.7.syh.7) for migration, rollback and verification limits.


v0.4.0

- Introduced thread-aware tracing with zero-SDK integration and configurable trace headers
- Added trace visualization interface for following end-to-end conversations
- Added configurable data storage policies to keep or trim trace payloads based on compliance needs

v0.3.0

- Launched project workspace management with per-project API keys and dashboards
- Improved project-scoped permissions and usage insights

v0.2.0

- Added multimodal image generation via chat completions and image_generation tools
- Documented provider support and sample usage for image workflows

v0.1.1

- Add OpenRouter outbound transformer
- Support Google Gemini OpenAI compatible API
