## [v1.0.0-beta12-fork.2] - 2026-11-08

### 修复

- 修复请求渠道过程、错误详情和会话补查因精简查询省略正文而触发的数据校验错误；保留正文按需加载及权限校验。
- 修复长会话追踪详情的递归校验栈溢出，逐节点校验完整树，不截断请求；同样覆盖线程内的追踪抽屉。
- 追踪解析识别正文未保存／过期等内部留存标记，避免误报供应商正文错误，并保留另一侧仍可读取的内容与用量信息。
- 无新增 schema/data migration，不改变模型转发、重试、计费和后台布局。完整验收与生产部署状态见 [Fork 维护台账](docs/fork-maintenance.md)。
- 已正式发布并依次更新双机，生产请求诊断、长追踪和迁移功能状态复验通过；正文短时缓存、响应正文关闭和磁盘清理策略保持。迁移专用目录按授权启用，未执行备份或导入。

## [v1.0.0-beta12-fork.1] - 2026-11-07

### 新增与调整

- 原始上游基线升级为 looplj/axonhub beta12；保留 ChouUn 继承历史，后续版本使用 beta12-fork.N，历史 syh 版本不改写。
- 接入 Decisions、TypeSafe、Cloudflare、Bailian Responses 及 MiniMax／ModelScope 图片供应商扩展，增加 Claude 自动模式审核、单渠道重试、多 Key 策略、自动禁用／恢复和配额路由配置。
- 吸收请求诊断、秒级分析范围、分页和聚合查询改进；保留 live＋archive 统计、最终执行／首字节耗时口径、项目＋分组 Key 名称约束和 0–10 模型优先级。
- 保留当前计价快照、预算预留／归属、分组预算、倍率、健康门控、多地址、请求留存及多来源 Codex 目录。

- ZenMux 原生视频是合并前已有能力，本次继续保留；六种可选多 Key 策略是相对合并前新增的控制入口，默认粘性行为沿用旧逻辑。

### 修复

- 将根模块、llm 与配置生成工具的 `github.com/klauspost/compress` 同步升级到 `v1.20.2`，修复 zstd 请求解压可触达的双块帧崩溃漏洞 `GO-2026-6865`。

- 统一供应商终态、客户端交付确认和实际用量；失败／取消不能被后续 `[DONE]` 或其他 choice 的正常结束覆盖，转换失败仍保留供应商模型证据。
- 修复 WS-only Codex 目录、认证、工具兼容性及刷新优先级遗漏；两个编辑入口保留目录资料和旧 HTTP 模型协议限制，取消后重开恢复设置。
- 修复整体请求截止时间与流式执行计时器之间的竞态，超时不再因取消回调抢先而误记为客户端取消；关机仍能及时中止执行。
- 修复渠道短表头被内部间距挤压的问题，仅渠道页使用紧凑表头；请求页样式与耗时排法保留。分组／密钥／用户用量增加每百万 Token 成本，复用现有统计及虚拟列表。
- 缓存系数排除无法唯一对应最终执行的多条用量，费用与 Token 总量不变；补充系数用途说明。输入用量缺失与明确零值的证据链、旧窗口处理仍是已确认待办，不能视为全部修复。
- 保留后台性能优化，修复普通 GraphQL 业务错误重复查询的回退；分析速度列沿用最终执行、首字节和文本 Token 口径，合并到已有维度统计，减少重复扫描。
- 修复渠道菜单打开的弹窗关闭后可能阻止整页点击的问题。
- 正式发布及快照支持新 fork.N 版本、两条来源祖先检查和过期基线拒绝。

### 升级与移除

- 包含自动表结构升级和旧配额路由转换；自动迁移时清理 Wafer 退役配额记录，保留既有迁移版本及行为。不能仅靠切换旧镜像完成数据回退。
- 已正式发布并完成云悠、泉哥双机部署验收。发布源码固定为 `aed80ce3`；验证结果、缓存待办、类型诊断及未覆盖的真实业务范围见 [Fork 维护台账](docs/fork-maintenance.md)，完整说明见 [.github/release-notes.md](.github/release-notes.md)。

## [v1.0.0-beta10-fork.7.syh.19] - 2026-11-07

### 新增与调整

- 增加独立官方 Codex 发布快照兜底、按客户端版本更新与归档、离线及固定版本回退；按对外模型 ID 精确匹配，缺少供应商目录也能取得完整 GPT 配置，不增加调用权限。
- 模型支持 mode: inherit 差异覆盖，未覆盖字段跟随资料更新；原完整定义与 DeepSeek 等第三方模型继续独立支持。
- 渠道缓存价格支持 5m／1h 双向转换，展示实际写入时长和计费依据，默认关闭；Key 请求成本精度与后台一致。
- 改善读取超时、取消、初始化失败恢复、模型列表可视区域渲染及排行榜查询；写操作不新增自动重试。

### 修复与升级

- 修复简略来源抹掉完整思考档位、工具覆盖未参与兼容判断、官方预览漏报来源差异的问题，保留人工限制和 Key 权限。
- 保留 encrypted_function_args 的缺失与空列表区别，按明确明文及接收者证据恢复误标智能体消息；compact／透传保持原始项，明确加密内容错误不在同渠道重复调用、不计为渠道健康故障。
- 相对 .syh.18 无新增 schema/data migration；新快照使用 systems 独立键，配置沿用 JSON 字段。旧版不支持继承定义，回退前需恢复兼容模型配置。
- 精简正式发布仅含双架构镜像和 release.json。本地定向验证及独立审查完成，生产验收另记；未来客户端、真实供应商推理／账单及全部页面性能未据此宣称通过。

## [v1.0.0-beta10-fork.7.syh.18] - 2026-11-05

### 新增与调整

- 支持文字输入、图片输入及对应缓存分项价格，覆盖渠道／模型标准价、阶梯、分时和加速计费；费用明细、预算预估及模型信息接口同步支持，缺失必要数量或价格时保持未定价。
- Key 和分组用量默认近 7 天，Token 使用 K/M/B 并保留精确提示，模型统计新增缓存命中率，成本精度遵循系统设置。
- 分组用量统一跳转独立 /self-usage 页面；请求日志复用后台模型、状态、缓存和耗时组件，显示路由、推理强度及调用方，保留权限边界。
- 渠道请求设置新增默认开启的内置生图工具权限；关闭的渠道不参与此类请求选路，全部候选关闭时返回明确错误，普通请求和独立图片接口不受影响。

### 修复与升级

- 修复管理端分组用量日志缺少图片规格和未定价字段导致的 GraphQL 422。
- usage_logs 新增四个可空分项用量字段，由 Ent 自动升级；旧记录不回填，无新增 data migration。分项价下模型信息旧单价摘要可能为 null，完整价格通过 pricing.items 提供。
- 精简正式发布仅含双架构 GHCR 镜像和 release.json；不备份数据库，生产只更新应用。旧版不识别新价格及权限，不视为可直接保持业务契约的回退版本。
- 本地定向测试、真实页面及独立复审完成；真实供应商账单、Image 2.6 调用与 MySQL 升级未验证。

## [v1.0.0-beta10-fork.7.syh.17] - 2026-11-04

### 修复与调整

- 修复多渠道 Codex 目录错误隐藏 Ultra：根据所选资料的实际推理档位继承选项，应用全部路线与人工限制后再次核对，保留未知映射和显式限制边界。
- 手动目录随当前模型关联生效，新路线继承已确认资料，保留权限和已声明的上下文限制。
- Responses 同协议转换保留跨聊天工具输出的原始类型、自身标识和命名空间，以及通过 previous_response_id 关联的自定义工具输出类型。
- 请求会话标记、详情和渠道筛选按实际派发渠道统一判断；思考档位标签采用适配明暗主题的分级配色。

### 发布与升级

- 双架构 GHCR 镜像及 release.json 精简正式发布；相对 .syh.16 无 schema/data migration，生产仅更新 Axon 应用。
- Ultra 定向回归与独立复审通过；真实供应商推理与完整多代理流程未验证。未纳入工作区未提交的分组用量 GraphQL 修改。

## [v1.0.0-beta10-fork.7.syh.16] - 2026-11-04

### 修复

- 完整 Responses 历史续聊优先原来源，原渠道停用、冷却、退出候选或原凭据／地址失效时允许使用有效备用配置；A→B 后混合历史优先 B，不因来源记录查询／保存失败阻断正常交付。仍依赖上游响应 ID、已经输出或执行结果不确定的请求保持原有保护。
- 修复进程重启后同步请求预留长期占用额度：从预留开始持续确认执行归属，在主库确认失效后回收占用并保留未知费用；停止失去归属后的发送和重试，保护活跃长请求及异步视频。备份恢复保留归属与释放记录，旧版无归属记录需根据执行中断证据定向处理。
- Codex 手动目录不再因客户端版本号与导入版本不同而省略模型；导入版本保留为来源记录，格式、权限和已确认路线校验继续生效。没有 Responses 目录或仅返回普通模型名单的渠道按缺资料处理，实际接口故障仍告警。
- 上游返回 504／524 后优先使用已允许的备用地址或渠道，不再在同一来源重复等待；仍依赖上游响应 ID 时保持来源约束。HTML 超时页转为明确的超时提示，保留原状态码和错误原因。
- 修复普通／流式错误收尾读取展示配置缺少内部权限的问题；已取消请求返回 499，避免误报为内部 500，真实上游错误与请求期限超时保持原分类。
- Responses 错误事件未提供状态码时，HTTP 错误出口返回 502，避免错误正文被发成 200；保留原始错误信息、上游状态及重试判断。
- 明确的 `upstream_stream_read_error` 在无 HTTP 错误状态且尚未输出、允许安全重放时进入既有重试流程，继续遵守预算及尚未展开的上游响应 ID 来源约束。
- 上游 429 提供 `Retry-After` 后，冷却期间不再向该渠道发送请求；保留允许的备用候选切换，否则返回 429 和剩余等待时间。本地拒绝不消耗 RPM、不延长冷却或触发凭据失败规则；保留客户端错误格式与隐藏／自定义文案策略。

## [v1.0.0-beta10-fork.7.syh.13] - 2026-11-03

### 新增与调整

- 渠道今日模型用量增加平均首字、平均输出和成功率，列表及浮层成本统一复用系统金额小数位设置。
- 模型目录可配置默认／最大上下文，渠道按具体型号逐字段覆盖；留空动态继承，自动目录与已有手动定义均适用。

### 修复与性能

- 支持 Codex 的 `response.interrupt`，保留中断前已完成的输出项，修复 WS 中断后转 HTTPS/SSE 续聊；维持 Key／项目隔离、正文存储开关和禁止不确定重放的边界。
- 渠道今日明细按稳定游标合并读取请求、最终执行和完整用量，复用历史价格并增加有界短时缓存；快速关闭重开不会把取消错误传给其他调用方。历史成本及有效样本含义不变。

- Codex 目录优先获取明确的完整来源，远程刷新预算调整为 3 秒，给客户端的 5 秒期限预留传输时间。普通 OpenAI 模型名单按上游缓存规则复用识别结果，减少重复探测。
- 渠道并发 / RPM 采用整表统一的数字区域宽度，当前值右对齐，上限保留独立位置，覆盖不同位数和未配置上限的混合列表。

### 发布与升级

- 继续采用双架构 GHCR 镜像加 release.json 的精简正式发布，复用同一提交的分支 Test；两层上游基线不变。
- 相对 .syh.12 无 schema/data migration。上下文设置写入现有 JSON，回退前应核对旧版本对新设置的解释。生产只更新 Axon 应用，不备份数据库；部署完成后按实际引用退役旧临时快照。

## [v1.0.0-beta10-fork.7.syh.12] - 2026-11-02

### 新增

- 渠道列表新增独立「并发 / RPM」列，展示当前实例用量与已配置上限；悬浮查看队列、TPM 和等待超时。
- 消耗排行榜和项目用量新增模型统计，按实际调用模型汇总成本、缓存命中率、平均首字耗时和平均输出速度；项目用量保留项目权限隔离。
- 请求列表的复用会话图标调整为紫色，便于识别会话复用。

### 修复

- Codex 目录默认根据模型配置和 Key 权限自动生成，优先复用相同实际型号资料，缺资料的别名按模型页关联继承，保留已知能力限制并随客户端版本刷新；手动 JSON 改为可选覆盖。
- Responses 自定义工具请求排除不兼容协议渠道，工具执行结果保留 `name`，修复仅有工具名称、没有 `call_id` 时转发失败。

- Codex 目录导入按每个公开型号保存独立指令和客户端工具设置，避免同一正则规则下新旧型号及不同来源声明的差异阻止预览。
- 来源差异列出待管理员核对的字段；上下文等能力仍按 Key 可见路线合并，Key 别名使用映射后的目标型号选择定义。
- 客户端工具输出截断策略不再与渠道上下文限制混合；旧单模型定义和既有路线快照继续可读，无 SQL 迁移。
- 明确 Codex 完整目录必须使用 /codex/models 的 models 结构；普通 /v1/models 的 data 名单不可替代。目录确认不能转换不兼容的推理工具协议。

### 发布与升级

- 发布复用同一提交在维护分支上的成功 Test，不再因打 tag 重跑整套测试；保留双架构镜像和临时 PostgreSQL 启动检查，仅交付镜像及 release.json。
- 相对 .syh.11 无 schema/data migration。新增统计返回字段不改变原维度契约；实时用量仅统计当前实例，服务重启后归零。

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
- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.
- NeuralWatt channels now show the elapsed monthly energy window marker on the kWh bar and an estimated period quota, matching the other windowed providers.

### Changed

- All dialog footers now share `DialogFooter`/`AlertDialogFooter` variants instead of per-dialog flex classes: `pinned` for sticky footers, `divided` for the top border, `gap` for spacing, `layout`/`align` for row/edge placement. Footers stack full width below `sm` (primary action on top) and sit right-aligned above it; the `content` layout keeps source order for footers that lead with explanatory text. This fixes the Model Association dialog's Cancel/Save buttons rendering with no gap.
- Footer buttons now meet a 44px touch target on phones and keep their normal control height from `sm` up.
- The `/analytics` time range is now precise to the second. `AnalyticsFilter.startTime`/`endTime` take RFC3339 timestamps instead of `YYYY-MM-DD` strings, `analyticsMetadata.earliestTime` replaces `earliestDate` with the earliest usage instant, every range is filtered as an inclusive absolute instant down to the millisecond, and the page uses the shared date-and-time range picker. The trend chart still aggregates per local calendar day.

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
