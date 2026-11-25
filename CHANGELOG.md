## [v1.0.0-rc1-fork.28] - 2026-11-25

- 后台请求列表与详情默认展示请求当时的最终计费成本和倍率。具备系统渠道读取权限的管理员可在费用悬浮详情对照渠道成本及倍率。手动补算预览采用同一计费口径，并保留原账单。
- 渠道消耗榜按计费成本排名。项目用量统计支持在密钥分组、模型、密钥、用户及展开的模型明细中同时查看计费成本和渠道成本。「显示渠道成本」首次默认关闭，排行榜和用量统计在当前浏览器分别记住上次选择。
- 同时有零价与缺价分项时保留明确的零。旧快照展示有记录的金额和倍率，缺少分项依据的基础合计显示为「—」。
- 从 fork.27 升级无需数据库迁移或历史账单回填。成本统计首次查询时会按新版口径重新生成所需的汇总缓存。

## [v1.0.0-rc1-fork.27] - 2026-11-25

- API 密钥和密钥分组可设置全局计费倍率。按密钥单渠道、密钥全局、分组单渠道、分组全局、渠道倍率的顺序取第一个已设置的值。倍率弹窗自动列出匹配的启用和停用渠道，分组汇总启用成员的全部已加载配置。
- 渠道消耗榜按请求最终计费金额排名，默认隐藏渠道成本列，需要对账时可手动显示。
- 自助用量概览和分组费用对照默认显示今天，本月趋势固定从报表时区的月初展示到今天。费用对照复用同一批账单汇总分组、密钥和渠道，减少重复查询。
- 从 fork.26 升级时，密钥表和分组表各新增一个可空的 `global_rate` 字段，原有记录继续继承倍率，无需回填。回退前须核对是否已使用全局倍率，并关闭旧版自动迁移以保留新增字段。fork.26 无法按新设的全局倍率计费。

## [v1.0.0-rc1-fork.26] - 2026-11-24

- HTTP 客户端经网关转发到 Responses WebSocket 时，已声明支持独立回合的上游可以在原连接上接收改写后的完整历史。只追加新输入时，网关按 HTTP 入口的历史格式匹配上一轮，匹配成功只发送新增内容。分段日志可查看连接复用、等待时间和实际写入的消息大小。
- 配置密钥渠道倍率时，列表按该密钥可用的渠道范围展示，并区分同名渠道。
- 修复旧版 Codex 客户端读取模型目录时缺少模型的问题，更正 Sol 的输入能力为文本和图像。
- 从 fork.25 升级无需数据库迁移。两台 AxonHub 串联时，双方升级后即可在原有 WS 握手中协商独立回合能力。

## [v1.0.0-rc1-fork.25] - 2026-11-24

- 用量统计与消耗排行榜随列表返回模型明细，展开、收起无需再次请求。长范围和大榜单按完整排名分批显示；后台依据数据库与入账压力调节日期汇总并发。
- 支持为分组或单把 API 密钥指定渠道计费倍率。管理员可以对照渠道成本与计费用量；自助用量和预算使用请求执行时确定的计费金额。
- 启用 Responses WebSocket 端点的渠道可选择自动、优先 WS 或仅 WS。已有渠道默认使用自动模式，后台任务和压缩接口继续使用 HTTP。
- 网关和 LLM 模块更新 WebSocket 依赖，修复 Go 漏洞公告 `GO-2026-6278`。
- 从 fork.24 升级时，Ent 自动创建倍率规则表，增加计费与恢复标识字段及索引。原有账目保留，不需要历史数据回填。新增规则生效后，旧版无法执行这些规则，回退前须核对计费设置与在途请求，并禁用旧版自动迁移，以保留新增字段。

## [v1.0.0-rc1-fork.24] - 2026-11-24

- API 密钥自助用量和管理员分组用量的请求日志默认滚动加载，仍可切换页码。读取下一批时按创建时间和请求编号续读，浏览历史时保持列宽和阅读位置。
- 刷新按钮同步更新日志、汇总、每日趋势和按模型统计。自动刷新默认关闭，可选择 3、5、10、30 或 60 秒。平稳更新原位显示状态和用量，逐条插入模式按顺序显示少量新请求。浏览历史或切到后台时暂停定时更新。
- 从 fork.23 升级无需数据库迁移、数据回填或新增配置。

## [v1.0.0-rc1-fork.23] - 2026-11-23

- 渠道页刷新今日用量时复用未变化的小时；新增请求、历史补价或执行状态变化只重算受影响的小时。列表与模型浮层继续使用同一份费用和性能数据。
- 用量统计和消耗排行榜先显示汇总，展开时再取所选行的模型指标。等待多日汇总期间只查询尚未完成的日期；快速收起或明细加载失败时保留已核对的结果。
- PostgreSQL 按请求编号读取在线和归档账单。跨渠道重试的费用计入实际收费渠道，同一请求在同一渠道产生的多笔模型费用只占一个渠道计价样本。
- 从 fork.22 升级时，Ent 会自动为归档表建立请求编号索引。建索引所需时间和磁盘空间取决于归档表大小，无需数据回填或新增配置。

## [v1.0.0-rc1-fork.22] - 2026-11-23

- 原生 Responses 转发会复用无需改写的正文。支持的对话协议在关闭原始分片存档和调试转储时，边接收输出边汇总正文与用量，降低长对话的内存占用。
- HTTP、WebSocket 和 Gemini 显式缓存写入共用实例级大正文额度，默认 256 MiB。WebSocket 排队只保留原帧和路由信息，合并预热时容量不足可重试。请求结束后，拨号追踪与落账计时器及时释放对正文的引用。
- 两台受控网关可按明确的 HTTPS 端点启用跨请求字典压缩，默认关闭。接收端确认保留字典后，后续请求可复用重复历史。字典丢失时，发送端只在接收端明确拒绝推理后补发一次。免鉴权流量继续使用普通传输。
- 从 fork.21 升级无需数据库迁移或新增必填配置。站间压缩的收发配置见[存储与传输指南](docs/zh/guides/storage-maintenance.md#大正文与站间传输)。

## [v1.0.0-rc1-fork.21] - 2026-11-22

- 后台「请求处理 → 转发行为 → 上游连接」提供 HTTP 协议和请求压缩的全局默认值，保存后对新请求生效。已经固定的端点继续使用原配置，两项后台设置支持系统备份与恢复。
- 使用共用 HTTP 客户端的渠道默认根据同类流式请求的首个模型或工具输出比较 HTTP/2 与 HTTP/1.1。明确的 HTTP/2 协议错误会让后续请求暂时回退；同一上游的自动试用和回退共享并发额度。
- Responses、Chat、Messages 和 Gemini 的 JSON 请求可以按端点声明及实测上传收益协商 zstd 压缩。各渠道的生产首字收益需结合上线后同类业务流量判断。
- 从 fork.20 升级无需数据库迁移或新增必填配置。

## [v1.0.0-rc1-fork.20] - 2026-11-21

- 请求日志和 API 密钥用量明细在模型名旁显示实际发给上游的 Fast 或 Ultrafast，即使没有费用明细也可查看。金额下方显示计费档位，紫色 1M 表示命中长上下文定价。
- 网关记录本次请求正文或 WebSocket 业务帧是否实际写出。本地模拟回包、写入结果不明、提前拒收正文和已放弃的重试不会误标发送档位。历史记录缺少可靠依据时留空。
- 从 fork.19 升级无需数据库迁移或新增配置。

## v1.0.0-rc1-fork.19

Responses 大请求可按正常流量自动优选 HTTP/1.1，无需逐个填写上游地址。网关测量写完请求正文的用时，在两次同档请求均有明显收益后优先使用。上传超时、效果变差或记录到期时重新使用默认协议协商。每个业务请求只发送一次用于比较，已有的上游重试规则继续生效。

## [v1.0.0-rc1-fork.18] - 2026-11-21

- Responses HTTP 支持为实测慢上传的完整 HTTPS 地址选用 HTTP/1.1 长连接，并可叠加已有的 zstd 请求压缩。默认继续自动协商协议。
- 项目请求和密钥自助用量支持按客户端会话精确检索，可查看实际发出的思考预算及上游最终回报。级联网关按配置采用上游最终结算档位。
- 渠道创建和编辑支持按模型探测协议、WebSocket 与工具能力，可逐项启停。探测失败保留既有选择。
- 从 fork.17 升级会新增请求及归档会话字段、项目会话索引和执行预算证据，并按现存记录回填。若需回切 fork.17，先临时设置 `AXONHUB_DB_DISABLE_AUTO_MIGRATION=true` 再启动旧版。否则旧版自动迁移可能删除新字段、索引及回填结果；库内版本记录不会随之回退，重新升级也不会自动重跑这次回填。恢复新版时恢复原迁移设置。

## [v1.0.0-rc1-fork.17] - 2026-11-21

- Codex 目录按字段采用模型卡与上下文设置、手写覆写、基础目录的优先顺序，覆盖官方源码、发布快照和自定义目录。URL 与上传 JSON 声明的能力，仅在当前密钥可用的所有兼容路线都明确限制时才收窄。修复部分渠道只上报文字导致客户端无法添加图片的问题。
- Gemini 专用密钥支持显式缓存，后续访问绑定创建时的供应商账号和地址，并区分续期结果未确认的情况。
- 为单批流式返回添加标记，流速统计排除这类样本。修正豆包视频任务终态和重复创建问题，保留创建超时、结果未确认时的原始错误。
- 从 fork.16 升级时，数据库新增 `gemini_caches` 表，以及 `request_executions` 的可空字段 `content_batches` 和 `content_batch_span_ms`。回切旧镜像会保留这些结构，旧版无法管理新版创建的显式缓存。

## [v1.0.0-rc1-fork.16] - 2026-11-20

- 项目服务账号支持只读查询请求和执行尝试。Gemini 原生接口支持读取可见模型详情。
- Codex 模型与速度档按可用路线筛选并保留目录覆写。单笔请求平均输出速度按 vLLM 公式计算。
- 本版无需数据库结构或数据迁移。[发布说明与镜像清单](https://github.com/Syh1906/axon-hub/releases/tag/v1.0.0-rc1-fork.16)

## [v1.0.0-rc1-fork.15] - 2026-11-20

从本仓库 rc1-fork.14 升级。本次精简正式发布提供 Linux amd64、arm64 镜像及部署清单 `release.json`。

- 在「集成与兼容 → Codex 兼容」导入本机快照或订阅公开 JSON 地址，预览通过后应用。目录 URL 预填[本仓库模型目录](https://raw.githubusercontent.com/Syh1906/axon-hub/refs/heads/release/v1.0.x/catalogs/codex/models.json)。官方源码、URL 订阅和本机快照互斥；固定模型定义、字段覆写和渠道限制继续生效。
- Codex 选择器保留模型展示名。兼容旧客户端的内置或已归档发布快照可离线使用；订阅检查能识别最低客户端版本变化，共享数据库的过期检查结果无法覆盖新来源。
- 完善请求列表和自助用量页的首字与平均输出速度统计，改进 Responses 断线续传、历史恢复与重复派发处理；结束帧缺少输出时，可用此前已确认完成的输出项恢复续传历史，并区分 Alpha Search 缺少搜索端点与模型不可用。
- 数据库为 requests 增加可空的 stream_timing 字段。旧记录继续可读，无新增必填配置。多副本部署应升级所有应用进程后再切换目录来源。

## [v1.0.0-rc1-fork.14] - 2026-11-19

本版从 rc1-fork.13 升级，基于 looplj/axonhub v1.0.0-rc1。精简正式发布提供 Linux amd64、arm64 镜像及部署清单 `release.json`。

- 在「渠道 → 请求设置」一次选择支持的加速档位、是否允许降档、是否强制发送指定档位，以及按哪档收费。「模型价格」继续维护标准、Fast 和 Ultrafast 的价格；旧版价格页保存也会保留渠道当前的计费策略。
- 新建渠道默认仅支持标准档、允许降档、优先按上游回报计费。Fast 请求没有可用的同档渠道时才降档，上游未确认档位时按实际发送档位结算。支持按客户端原始请求或固定档位计费。既有强制档位渠道保留原选路顺序。
- 改进流式首内容计时和新会话出口对照，默认只观察。Responses HTTP 可按上游明确宣告学习请求压缩能力，逐跳判断是否使用 zstd；同一请求的图片处理与档位读取减少重复扫描。实际首内容延迟和压缩收益取决于负载及端点。
- 本版无新增数据库迁移或必填配置。升级前的成本记录沿用原有结算依据；回退 fork.13 前，应先核对新版保存的固定计费档位，旧版无法执行该策略。

## [v1.0.0-rc1-fork.13] - 2026-11-19

基于 looplj/axonhub 的 v1.0.0-rc1，从 rc1-fork.12 升级。精简正式版提供 Linux amd64、arm64 镜像及 `release.json`。

- 已启用 HTTP 和 WebSocket 双协议的渠道，首个符合条件的 WS 回合可用实际业务请求验证模型的上游连接。手动探针兼容要求消息列表和 `store: false` 的端点，握手错误与发送后的中断可分别诊断。
- 可按实例指定仅复用已有图片压缩结果的渠道，以及接受 zstd 的完整 Responses HTTP 地址。两个列表默认均为空。本地双网关用例中，带图工具接续的中间一跳为 259 字节，最终 HTTP 正文压缩后少传 25.1%。这是单笔测试的传输量，公网首字收益需按站点验证。
- 请求日志区分上游首个事件、首个非空增量和网关成功写出该增量。采集本地数据库行号请使用 `axon_request_id`。新增只读 PostgreSQL 对照脚本，支持按可比工作量分析渠道首字，现有选路评分和超时沿用既有设置。
- 从 fork.12 升级无新增数据库迁移或必填配置。回退可切回原镜像及原配置。

## [v1.0.0-rc1-fork.12] - 2026-11-19

基于 looplj/axonhub 的 v1.0.0-rc1，从 rc1-fork.11 升级。

- 渠道管理支持单个及批量验证指定模型的原生 WebSocket。双协议渠道依据实际端点、凭据和模型选路。Codex 可通过 WS 接入一台或串联两台 AxonHub，最终上游可使用 HTTP。保留仅 WS 渠道的握手预算和加密历史的来源绑定。
- 网关减少大请求正文的重复处理、重复历史写入和无预留时的账务查询，增加请求交付分段计时。11.14 MiB 图片在本地回环对照中，20 笔有效首字 P50 从 621 毫秒降至 477 毫秒，总耗时 P50 从 760 毫秒降至 583 毫秒。
- 支持按渠道加速档位选路，修正覆写档位及模型兜底转发的计价。统一模型卡导入，完善 OpenAI、Anthropic 模型目录。请求列表分档展示耗时，新建 Responses 渠道默认允许 WS。
- 无新增数据库迁移或必填配置。新版本写入的扩展模型卡、路由配置及 WS 证据按新版语义使用，回退旧镜像前应核对这些配置。精简发布提供 Linux amd64、arm64 镜像及版本清单。

## [v1.0.0-rc1-fork.11] - 2026-11-18

基于 looplj/axonhub 的 v1.0.0-rc1，以下为相对 rc1-fork.10 的维护分支变更。精简正式发布提供 Linux amd64、arm64 镜像及 `release.json`。

- **密钥分组临时加额**：只增加选中周期的可用额度，常规上限不变。固定周期在原刷新时刻失效；滚动周期在发放时显示的下一笔消费释放点失效。无明确截止点时不能发放。已有单密钥和其他周期限制仍然生效。
- **余额显示与判断**：统一使用 UTC 查询账本、按预算时区显示，避免 SQLite 在非 UTC 时区提前释放额度；分组列表、详情和批量摘要按同一数据库快照计算余额，避免把刚结算的请求重复扣减。加额到期后页面自动回读，包括“额度耗尽”筛选。
- **Codex 模型目录与渠道匹配**：支持同步公开模型目录，保留上次成功结果；请求与模型目录按实际可见渠道选择匹配规则，尊重渠道上下文限制和工具协议。
- **原生协议修正**：完善 Anthropic 原生输入计数的渠道和计费边界、旧版文本补全的批量及流式聚合、二进制音频中断处理、Jina 重排的缺失或无效用量记录。

从 fork.10 升级无新增数据库结构、数据迁移或必填配置。临时加额保存在既有预算账号中；旧版不识别临时额度，回退镜像不能保证保留其可用效果。

## [v1.0.0-rc1-fork.9] - 2026-11-17

### 界面语言与翻译兼容

- 修复请求列表在网页翻译后更新首字耗时、总耗时和输出速度时可能崩溃的问题；通用错误页提供中英文提示和重新加载，不再统一标为 500。
- 默认按浏览器语言列表选择中英文，支持“跟随浏览器／中文／English”；手动选择保存到账号，未登录和自助用量页只保存当前浏览器偏好，页面语言声明同步。
- 修复缓存返回、登录续期和在途旧账号响应覆盖语言选择的边界，管理页面在账号语言就绪后再显示，自助页语言策略独立。

### 升级与验证

- 新账号默认语言改为 `auto`；一次性 `rc1-fork.9` 数据迁移将所有老账号转为“跟随浏览器”，包括旧手动选择。之后手选不会在重启时重置；账号权限不变。
- 调整现有语言列默认值，无新增表、列或索引；共享账号缓存键升级，避免读到迁移前偏好。旧版不理解 `auto`，保留旧镜像不代表可以完整回退或恢复旧语言。
- 独立 `gpt-6.1-sol / max` 复核、自检、27 项有效前端测试、三种数据库迁移和内置浏览器定向验收通过。精简正式发布仅含双架构镜像及 `release.json`；实际发布和部署结果见 Fork 维护台账。

## [v1.0.0-rc1-fork.8] - 2026-11-17

### 统计加载性能

- 消耗排行榜和项目用量按小时保存可复用汇总，仅重算来源变化的小时；完成进度独立提交，最后统一复核完整范围和归属。
- 去除展示汇总前逐条回写执行再读取的开销，PostgreSQL 从范围内请求定位执行及账目，剩余内部统计准备按日期和编号续读。
- 修复多个日期共用计算期限，以及超时原因误判、发布结果时租约未释放导致的失败和额外等待；预算耗尽可续算，真实错误继续上报。
- 保持原统计口径、权限、失败尝试费用、计价分母、归档及未知费用语义。

### 升级与验证

- 无新增 schema、索引、data migration 或配置项；复用既有汇总表，不作废未变化的日结果。
- 独立审查、自检、三种数据库定向验证及真实 GraphQL／调度器今日、本周、本月读取链通过。大数据合成样本验证减少首次与重复读取成本，不据此承诺生产延迟。
- 精简正式发布仅含双架构镜像和 release.json；发布、双机部署与实际验收见 [Fork 维护台账](docs/fork-maintenance.md)。

## [v1.0.0-rc1-fork.7] - 2026-11-17

### 渠道协议与请求控制

- 渠道可限制允许的客户端聊天协议并关闭跨协议转换，默认保留自动转换；Responses HTTP／WebSocket 允许同协议传输适配，选择、重试与最终发送遵守同一策略。
- 新增系统级“协议路由”页，分页查看公开聊天模型、实际型号和可选渠道，展示协议分支、条件、排除原因与历史成功请求；明细按需读取，不自动调用供应商。
- 补齐公开模型类型优先级、别名协议筛选后去重及不可变历史证据时间；不能保留 n、stop、logprobs 或 DeepSeek json_schema 要求的路径明确拒绝，端点探测不再把错误响应当作支持。
- 渠道请求设置支持不覆写／标准／Fast／Ultrafast，作用于实际出站 Responses HTTP／WebSocket；专用设置优先于通用正文覆盖，重试换渠道重新取值，保留原始档位和计费依据。

### 统计与预算修正

- 统计按明确刷新边界更新，渠道汇总与模型批量返回、悬停不请求；源变更与日期修订原子提交，减少重复历史签名扫描，并修复并发数据一致性。
- 图片预算不再把编码或 URL 长度当作普通文本重复计算；多种输入计价按最高候选费用预留一次，保留单一输入的阶梯规则和固定费用。

### 升级与验证

- 相对 fork.6，统计改动新增 statistics_revisions 表、request_executions 的 6 个 statistics 字段、usage_logs 的 3 个 statistics 字段及相关索引，由 Ent 自动升级；未新增 data migration。旧数据首次统计准备仍有成本。
- 精简正式发布只含 Linux amd64／arm64 镜像和 release.json。部署仅替换应用固定镜像，不备份数据库、不重建数据卷；应用重建及 schema 升级可能影响在途请求。
- 协议阶段独立 gpt-6.1-sol / max 复审、自检及定向回归通过；实际云端发布与双机更新结果以 [Fork 维护台账](docs/fork-maintenance.md) 为准。不据此宣称全部供应商、生产规模性能或回退演练已验证。

## [v1.0.0-rc1-fork.4] - 2026-11-13

### 性能与正确性

- 用量统计与消耗排行榜按系统自然日保存汇总，由后台受控回填；页面分批获取，浏览器复用已覆盖周期，减少本月／本周／昨天切换时的重统计。
- 省略未展示的性能指标，优化 PostgreSQL 执行与用量关联；未变化日期只核对相关字段、不完整聚合或重复下载。共享读取在全部等待者离开后取消。
- 补齐历史补价、移组／改名、跨日归档、静止活动日和并发生成的修正；跨页核对版本，不交付混合归属或不完整总额。缓存榜跨日样本及均值分母保持原口径。
- 浏览器日期缓存按用户／权限／项目隔离并有界回收；待处理或加载期间变化有明确提示，原完整 API 保留。

### 升级与验证

- 新增可重建的 `consumption_snapshots` 表及索引，Ent 自动升级；既有表、data migration、依赖和计费规则不变。部署仅更新应用，按既有规范不创建数据库备份。
- 精简正式发布仅含 amd64／arm64 镜像与 `release.json`。本地定向检查、真实合成库页面联调及独立 `gpt-6.1-sol / max` 复审通过；同提交云端测试补验 PostgreSQL 升级路径。
- 首次未覆盖历史仍需回填，五分钟核对仍有窄字段扫描成本；生产高并发、超大历史及 MySQL 升级不据合成结果宣称通过。实际发布与双机更新状态见 [Fork 维护台账](docs/fork-maintenance.md)。

## [v1.0.0-rc1-fork.3] - 2026-11-11

### 修复

- 修复 DeepSeek 原生 Responses 同协议重组丢失 `reasoning_text` 的问题，保留原生思考正文、摘要、分段和不透明内容，覆盖续聊及普通响应。
- Claude 保留结构化输出、思考绑定、容器、原始停止原因和停止文本；未映射原生工具及服务端历史不会被静默删除或误转成客户端函数。Claude Code 保留必需 beta 标头与原生工具选择名称。
- Gemini 保留多段思考、逐块签名、文件引用和文字／工具顺序，流式独立签名及结束分片不再丢失。
- 路由和最终出口拒绝无法等价表达的工具、历史与状态，保留普通函数和已有搜索映射；Responses 原生请求继续优先匹配 HTTP／WebSocket 传输方式。

### 升级与验证

- 发布检查发现 Go 标准库 `GO-2026-6859`（HTTP/1 `MaxBytesReader` 标头竞态崩溃），构建及安全检查升级到 Go 1.27.4，独立 llm 模块最低版本同步为同样已修复的 Go 1.26.11。
- 精简正式发布仅含 Linux amd64／arm64 镜像和 `release.json`。相对 rc1-fork.2 无 schema/data migration、第三方模块依赖或默认透传开关变更；生产仅更新应用固定镜像，不备份数据库、不重建数据卷。
- 本地定向回归、自检及独立 `gpt-6.1-sol / max` 复审完成，阶段内无剩余 P1/P2 阻塞项。真实供应商端到端和完整能力组合未据此宣称通过；发布与双机部署结果见 [Fork 维护台账](docs/fork-maintenance.md)。

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
