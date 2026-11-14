# HTTP 上传超时后的连接池恢复

- 日期：2026-11-14。
- 状态：本地实现、独立 `gpt-6.1-sol / max` 审查、自检及受影响定向验收完成；已提交 `641410e9` 并完成临时快照双机部署，未新增正式版本。生产基础验收与短时观察见[快照部署记录](snapshot-connection-20261114.md)，实际故障恢复效果仍未验证。
- 范围：在同一服务器、同一目标上游内恢复原生 HTTP 连接；不调整供应商、渠道优先级、账号、请求正文或生产超时配置。

## 行为与原因

现场已有约 2.76 MB 请求在取得连接后上传超过 120 秒的证据，但没有证明某条复用连接就是根因。此次增加有限的连接恢复能力，避免把全部上传异常解释为线路问题，也避免不断重连。

AxonHub 自建 HTTP 客户端以实际目标的 scheme/host 为范围创建标准库连接池；不同客户端保留各自的代理与 TLS 策略。明确的上传超时发生在取得连接之后、上传完成或响应头到达之前时，仅为后续请求切换新池。连接建立超时、用户取消、响应等待超时和禁用复用不触发更换。

旧池不再分配新请求，已有响应按正常生命周期继续。引用计数覆盖响应正文读到 EOF 或 Close，而不是只覆盖 RoundTrip 返回；取消与失败也释放引用。每个目标最多一个当前池和一个旧池，并发失败合并恢复。新池需要完成一次不小于失败正文的上传并取得响应头才解除恢复抑制；小请求成功不构成大请求恢复证据。空闲池按既有 IdleConnTimeout 回收，渠道清理会让仍有响应的池在完成后回收。

恢复层不重发请求，不增加原有重试预算。原有执行结果未知禁重放规则和阶段超时保持。外部通过 NewHttpClientWithClient 注入的 HTTP 客户端仍归调用方管理，原生 WebSocket 与 SDK 自管连接不接管。

分段日志新增 pool_generation、connection_id、connection_reused、protocol、connection_attempts、pool_recovery。pool_replaced 表示未来请求切换新池；upload_verified 表示同等或更大上传完成且取得响应头，不表示模型执行已成功。两种状态及恢复受抑制的失败可在 info 日志定位，不记录正文或凭据。

WebSocket 初始化改用 TLSConfig 获取配置副本，避免引入连接池封装后底层 Transport 类型判断失效。TLS 策略副本独立于标准库延迟初始化，WebSocket 调整 ALPN 不会改动共享 HTTP 策略。

## 文件

- new：`llm/httpclient/transport_pool.go`、`llm/httpclient/transport_pool_test.go`、`llm/httpclient/transport_pool_retry_test.go`。
- `llm/httpclient/client.go`、`client_test.go`、`transport_progress.go`、`transport_progress_test.go`：连接池接入、诊断及配置继承。
- `llm/pipeline/response_phase_timeout.go`、`response_phase_timeout_test.go`：明确上传超时触发与真实 HTTP 回归。
- `llm/transformer/openai/responses/websocket_executor.go`、`websocket_executor_test.go`：TLS 策略复制及隔离验证。
- `internal/server/orchestrator/request_transport.go`：恢复与同等上传验证的 info 日志。
- `docs/zh/getting-started/request-processing.md`、`docs/en/getting-started/request-processing.md`：同步中英文行为说明。
- `docs/fork-maintenance.md`：新增未发布维护 topic。

没有依赖、持久缓存结构、数据库迁移或前端变更。

## 验证

测试通过本地 httptest HTTP/1.1 和 HTTP/2 服务执行，不调用收费模型、不读取生产请求正文。临时目录使用 verification-cleanup 管理，GOTMPDIR、TMP、TEMP 仅在测试进程中指向本任务目录。

在 `F:/GitProject/axon-hub/llm` 执行：

```powershell
go test -vet=off ./httpclient ./pipeline ./transformer/openai/responses -count=1
```

三个包均通过。覆盖以下行为：

- 同一 HTTP/2 连接上的正常响应在其他上传恢复时完整结束，HTTP/1.1 同样覆盖。
- 并发上传失败只更换一次；新请求的池版本和连接标识发生变化。
- 其他目标上游的连接继续复用。
- 新池仍失败和只有小请求成功时，不持续更换；同等上传成功后重新允许恢复。
- 旧池仍在交付响应时，不创建第三个池。
- 真实阶段计时器触发恢复，原 POST 的服务器接收次数仍为一次。
- 父请求取消、上传完成后的响应超时不更换池；已有 524 分类保持。
- 响应正文结束、主动清理与空闲期限能回收池；TLS 配置复制和 WebSocket HTTP/1.1 协商保持。

在 `F:/GitProject/axon-hub` 执行：

```powershell
go test -vet=off ./internal/server/orchestrator -run 'Test(TransportFailureIsLoggedAfterHTTP200|UploadTimeoutRemainsGatewayFailure|RetryTransport)' -count=1
```

通过，覆盖原重试分类、重试流程、上传失败分类及日志去重。后续 TLS 策略副本和测试补充未改变这些行为，沿用结果。

收尾复核补充了重定向到新目标时清除上一目标恢复标记的修正，并在 llm 模块执行 `go test -vet=off ./httpclient -run 'TestTransportTrace' -count=1`，通过。其余无重定向的连接生命周期、TLS 和重试行为未改变，沿用上述结果。

Go 修改使用 gofmt；最终差异使用 `git diff --check` 检查。测试临时目录已由受管脚本清理，未删除原有 scratch 根目录。未运行 lint、独立 build 或全库测试。环境 CGO_ENABLED=0，且 gcc/clang 均不可用，未运行 `-race`，未安装编译器。

## 尚未验证

这次验证证明受控故障下连接恢复、隔离和不额外重发的行为，不证明泉哥到圆圆的实际线路已经改善。发布后应在原服务器、原上游，按请求大小、并发与新旧池版本比较自然流量；若新池持续同样缓慢，应继续定位网络或上游接收问题，不能将更换池当作带宽提升。

## 独立审查与自检闭环

用户要求按 `gpt-6.1-sol / max` 独立子代理审查。子代理 `/root/connection_review` 使用独立上下文，只读核对既定方案、当前差异、标准库调用链及验证证据；主执行者负责自检、复现、局部修复和补验。最终子代理结论为“未发现仍阻塞本阶段验收的问题，可以通过本地实现验收”；没有把主执行者测试描述为子代理独立重跑。

| 发现 | 修正及证据 |
|------|-----------|
| P1：持诊断锁进入标准库清理，与 HTTP/2 持池锁调用 GetConn 存在反向锁序 | 恢复在锁内只修改状态，实际连接关闭与标准库清理移到全部共享锁外；阻塞 Close 期间 GetConn 和池状态可访问，并验证旧连接关闭前不允许第三代。 |
| P2：响应 EOF 早于 HTTP/2 内部清理，池记录已删除但真实连接遗留 | 用延迟 request.Body.Close 固定窗口，原代码观察服务端 ConnState 关闭失败；改为自有拨号连接按代际登记，最后响应结束后回收，原失败用例及迟到拨号、双响应保留用例通过。 |
| P2：先返回响应头、后上传完成时恢复抑制没有解除 | 真实 HTTP/2 先 Flush 头再读大正文，原代码未产生 upload_verified；改为两项证据任意顺序共同确认后通过。 |
| P2：标准库透明重试或 307 重定向沿用旧阶段证据 | HTTP/1.1 复用连接零字节写失败后阻塞第二次 Dial，原代码残留 Connected；GetConn 清当前尝试证据，后续真实 307 上传停顿正确分类为上传超时并只恢复目标池。 |
| P2：HTTPS 代理双层 TLS 找不到自有连接身份 | 两层 tls.Client 包装本地连接，原代码返回 0；逐层查找身份且保留最外层协议判断后通过。 |

关闭动作由代际自己的 DialContext 归属证明，不直接关闭 httptrace 借出的连接。退役时先标记拒绝迟到连接，旧代占位直到实际关闭结束。自定义 DefaultTransport TLS 拨号器保留原实现与 ALPN 所有权，不接管；项目当前没有这一自定义入口，增加定向契约检查即可，不扩展为第三方配置审计。

本轮没有新增恢复策略、供应商调用或生产操作。只修复上述目标内的并发、回收和诊断偏差。项目唯一内部迭代 skill 为 `axon-frontend-iteration`，明确“不用于无视觉变化的后端修改”，因此本轮不执行图像、前端和浏览器流程。遵守 AGENTS.md、Go 与缓存规则；中英文行为说明同步，无缓存序列化或数据库变更。

补验统一在 `F:/GitProject/axon-hub/llm`、`GOMAXPROCS=2` 下串行执行，临时产物仅写入受管 `axon-connection-review-20261114` 任务目录。最终锁序与生命周期修正后执行：

```powershell
go test -vet=off -p 1 ./httpclient -run '^Test(UploadRecovery|TransportPool|RetiredPool|RecoveryCloses|ConnectionIdentity|TransportTrace|HTTPTransport)' -count=1 -timeout=30s
go test -vet=off -p 1 ./pipeline -run '^Test(UploadTimeoutRecovers|HTTPPoolRecovery|RedirectUpload|ResponsePhase|HTTPUpload|HTTP524)' -count=1 -timeout=30s
```

均通过。另在自定义 TLS 拨号器契约补齐后执行过含 `TestNewHttpClient` 的定向用例并通过。本轮先复现失败再修复的证据仅用于上述具体缺口；未因切换执行者重跑原三包。原 Responses WebSocket TLS 与 orchestrator 重试/日志相关结果仍覆盖未改变的行为，继续复用。最后只移除没有生产调用方的 `generation.close` 透传助手，测试直接执行其原有 prepareClose 返回动作；不改变连接回收行为，不为该等价清理重复运行测试。

最终 `git diff --check` 及修改 Go 文件的 gofmt 检查通过。最大修改文件仍为 WebSocket 测试的 1694 行，小于 2200 行。缺少 C 编译器，未运行 `-race`；独立审查不替代 race、生产自然流量或长期负载验证。
