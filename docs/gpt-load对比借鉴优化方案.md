# gpt-load 对比借鉴优化方案

**编制日期：** 2026-08-02<br>
**关联文档：** [代码审查优化清单.md](代码审查优化清单.md)、[9Router与NVIDIA反代调研报告.md](9Router与NVIDIA反代调研报告.md)
**对比对象：** [tbphp/gpt-load](https://github.com/tbphp/gpt-load)（main 分支，Go 1.25，多渠道 AI 透明代理）

## 一、背景与目的

本方案不是迁移到 gpt-load，也不是把它当作"标杆"全面对齐——两个项目是不同物种：

- **gpt-load** 是"多渠道密钥路由器"：上游格式原样透传，只换端点与 key，做密钥池轮换、负载均衡、故障转移。它面向多渠道、多账户、横向扩展，运维侧可配项多，集群能力成熟。
- **本项目** 是"NVIDIA 专用协议网关"：把 OpenAI 兼容请求映射成 NVIDIA 调用，做 responses→chat 协议转换、按 model 粒度屏蔽、busy+队列精确控速、Argon2id 管理认证、AES-GCM 密钥加密。它在协议正确性、单 key 限流精度、安全工程上更扎实。

本方案的目的是**从 gpt-load 已验证的设计里挑出对本项目确有收益的点**，给出可落地的改造步骤、触及文件、验证方法、风险取舍，避免"为对齐而抄"。每条都标注了它命中我们已有问题（多数已在[代码审查优化清单](代码审查优化清单.md)里追踪），而不是空泛建议。

## 二、总结表

| 编号 | 主题 | 优先级 | 命中问题 | 改动范围 | 估时 | 执行状态 |
|---|---|---|---|---|---|---|
| B1 | 请求日志批量入库 | P0 | 审查 #25 SQLite 热路径瓶颈 | observability 新增 buffer + flusher | 1 天 | ✅ 已完成 |
| B2 | 上游 key 脱敏（RedactSecret） | P1 | 错误体可能含上游 URL/key，未审计安全点 | observability + fault 出口脱敏 | 0.5 天 | ✅ 已完成 |
| B3 | Key 主动巡检恢复（CronChecker 思路） | P1 | 永久吊销 key 空转"恢复→失败→冷却" | nvidiakey 新增 healthchecker | 1 天 | ✅ 已完成 |
| B4 | 可配置故障转移状态码匹配 | P1 | fault.Classify 状态码判定硬编码 | fault + runtimeconfig | 1 天 | ✅ 已完成 |
| B5 | 日志保留天数可配 | P2 | observability 硬编码 30 天 | runtimeconfig + cleanup | 0.5 天 | ✅ 已完成 |
| B6 | 流式响应 `X-Accel-Buffering: no` | P2 | 反代层可能缓冲吃掉应用 flush | v1 各流式 handler | 0.5 天 | ✅ 已完成 |
| B7 | 超时配置按端点/模型组覆盖 | P2 | 全局统一超时对 audio/speech 慢端点不精细 | runtimeconfig + budget | 2 天 | ⏸ MVP 阶段不做（详见 B7 §299 "建议先不做完整端点覆盖"，违反 KISS，差异大维度单独需要再做） |
| B8 | 平滑动加权轮询（WWR）选多上游 | P3 | 未来扩展多 region/备上游端点时 | upstream/nvidia 新增 upstreams | 暂不做 | ⏸ 留作扩展点 |
| B9 | 全局 Access Key 概念 | P3 | 多组场景逐组建 key 不友好 | accesskey + 数据库迁移 | 2 天 | ⏸ 留作扩展点 |

**总体路线：** B1 先做（直接消除 SQLite 瓶颈，收益最高、改动可控）；B2+B3+B4 构成 P1 组（覆盖安全/正确性/运维灵活性的核心缺口）；B5+B6+B7 是 P2 体验型；B8/B9 留作未来扩展点，MVP 阶段不投入。

**执行跟踪（2026-08-02 落地）：** P0 + P1 + 体验型 P2（B1-B6）已完成并通过单测；B7/B8/B9 按"四·总体路线"和 B7 的"建议先不做"判断留作未来扩展点。改动摘要：

- **B1** `internal/observability/buffer.go` 新增 `BufferRecorder`（`batchSize` + `flushDelay` + `force chan chan error`）批量单事务入库；`internal/app/app.go` 组装 flusher goroutine；`internal/app/observability_test.go` 验证前调 `FlushObservability`；`shutdown.go` 缓冲 flusher/DB 关闭次序。
- **B2** `internal/observability/redact.go` 新增 `RedactBearerToken`/`RedactAuthorizationHeader`/`RedactURLQueryToken`；`http.go` parseUsage 入参过 redact。
- **B3** `internal/nvidiakey/healthchecker.go` 新增 `HealthChecker` + bounded worker pool + `probeStateWriter` WireProbe/WireWriter，只恢复不恶化；`repository.go` 新增 `ListKeysForHealthCheck`；`service.go` 新增 `ProbeHealth`。
- **B4** `internal/fault/matcher.go` 新增 `FailoverMatcher`（O(log n) 二分合并区间 + 校验拒 [200,399]）；`runtimeconfig/snapshot.go` 加 `FailoverStatusCodes` 字段 + Validate 接入 `NewFailoverMatcher`；`router/attempt.go` `shouldRetry` union 语义（`Retryable` OR `matcher.Match`），`buildFailoverMatcher` 空 spec 回退默认；`admin/settings.go` 暴露 PATCH/GET；`database/migrations/005_runtime_failover_and_retention.sql` 增列 + migration_005_test.go。**取舍（已确认）**：union 语义使运维只能用 spec **扩大**故障转移集（如 403/opt-in），无法用 spec 缩小关闭既有 5xx 重试，与 §202 "429 only → 502 不重试"端到端无法并存——详见 §B4 风险/取舍段。
- **B5** `runtimeconfig/snapshot.go` 加 `RequestLogRetentionDays` 字段 + 1..365 校验；`observability/cleanup.go` 持 `cleanupSettingsProvider`，每周期 `retentionDays()` 读 snapshot 回退 `DefaultRequestLogRetentionDays`，`app.go` 注入 settings；admin settings 暴露 PATCH。
- **B6** 三处流式头加 `X-Accel-Buffering: no`：`internal/sse/proxy.go`（chat SSE）、`internal/httpapi/v1/responses.go` `writeSSEHeaders`、`audio.go` speech 写响应头前；非流式不加（已在 chat_test 验证未设置）。

---

## B1. 请求日志批量入库（P0）

**命中：** 审查 #25（每请求一次同步 DB 事务，SQLite `SetMaxOpenConns(1)` 串行化是吞吐瓶颈）<br>
**借鉴对象：** gpt-load 的 `request_log_write_interval_minutes`（默认 1 分钟批量入库）

### 现状

`internal/observability/http.go:76` 在每请求结束时**同步**调用 `recorder.Record`；`repository.go:19-38` 的 `Record` 是 `BeginTx → insert request_logs + upsert daily_stats（4 维）→ Commit`。SQLite 单连接下，这条事务占据全部请求路径的串行点，高并发时所有请求排队等 DB 写。

gpt-load 把"每请求落库"改成"每请求入内存缓冲 → 后台 goroutine 周期性批量 flush"，把日志从热路径挪到旁路。这是收益最高、改动最可控的一项。

### 设计要点

1. **新增 buffer 队列**：`observability.BufferRecorder` 包装现有 `Repository`，提供 `Record(record)` 不再直接落库，而是塞进有界 channel（容量如 4096）后立即返回。
2. **新增 flusher worker**：后台 goroutine 持有 root context，每 `flushInterval`（默认 30s；可暴露 runtimeconfig）或缓冲满 N 条（如 256）触发一次批量落库。
3. **批量落库实现**：单事务内循环 `insertRequestRecord` + 累加 `daily_stats`；用 `INSERT ... ON CONFLICT` 累加，与现有一致。一次事务写满批，减少 SQLite 锁占用的请求次数。
4. **优雅关闭**：见 `cleanup.go` 的 `Run(ctx)` 模式——root context cancel 时 flusher 做最后一次 drain（带 10s 超时），避免日志丢失。复用 `App.shutdownGrace` 思路。
5. **背压**：channel 满时（4096 上限）退化为丢弃 + 计数告警（log `buffer overflow, dropped N`），**不让日志路径反压请求路径**。这是日志旁路化的核心原则。
6. **.middle 兼容**：保留 `Record` 接口，让 `HTTPMiddleware` 调用方式不变，仅注入的是 `BufferRecorder` 而非直接 `Repository`。

### 触及文件

- 新增 `internal/observability/buffer.go`：`BufferRecorder` + flusher。
- 改 `internal/app/app.go`：组装时用 `BufferRecorder` 包装 `Repository`，并在清理 worker旁启动 flusher。
- 改 `internal/observability/cleanup.go`：保留（与 flusher 互不冲突）。

### 验证

- 单测：批量上限触发 flush、超时触发 flush、channel 满丢弃+计数、ctx cancel 时最后 drain。
- 集成场景：在测试机打并发请求（如 50 RPS 持续 1min），对比改造前后 SQLite 写入次数、p99 响应延迟。预期 p99 下降、写事务次数从 N 次/秒降到 ≤2 次/分钟。
- 数据完整性：flush 后 `request_logs` 行数 == 处理请求数（含丢弃时记录 dropped 计数）。

### 风险/取舍

- **日志延迟可见性**：批量入库后，管理后台"最近 100 条错误"延迟最多 30s+批量剩余时间。可接受——日志是事后审计而非实时监控。
- **崩溃丢日志**：进程崩在 flush 前，最多丢一个批（≥256 条或 30s 内的量）。同等级风险下，原方案是"每请求都等 DB"，崩溃也只丢该请求；改造后丢的更多但概率低，且这是日志而非业务数据。可接受。
- **不引入 Redis/外部依赖**：gpt-load 集群靠 Redis 协调多实例共享缓冲。我们单实例不需要，保持纯 Go 内存 + SQLite 即可。

---

## B2. 上游 key 脱敏（RedactSecret）（P1）

**命中：** 错误体可能含上游 URL/Authorization 串，未审计的安全点<br>
**借鉴对象：** gpt-load 的 `RedactSecret` 在写日志/返回客户端前剔除上游 key（关键是因为 Gemini key 走 URL query 传输错误会带完整 URL）

### 现状

我们的 NVIDIA key 走 `Authorization: Bearer`，正常不会进错误 URL，但有两个未审计点：

1. `internal/observability/http.go` 的 `trackingWriter` 捕获响应体（上限 2MiB）用于解析 usage——如果上游返回错误且我们透传了上游错误体，其中**可能含上游 echo 回显的 Authorization 头**（NVIDIA 错误页少见但非零可能）。
2. `internal/fault/classifier.go` 解析上游错误体定 408 的 `ErrorCode` 和 cooling 决定——错误原文字可能最终流入 `request_logs.error_code` 或日志。

虽然实际泄漏面比 Gemini 小，但安全工程原则是：**出口处一律过一道脱敏，而不是假设上游不会泄漏**。

### 设计要点

1. **集中脱敏函数**：新增 `internal/observability/redact.go`（或 `internal/utils/redact.go`），提供：
   - `RedactBearerToken(s string) string`：正则替换 `(?i)bearer\s+[A-Za-z0-9._-]{20,}` 为 `bearer <redacted>`。
   - `RedactAuthorizationHeader(h http.Header)`：删 `Authorization` 字段值并写 `<redacted>`。
   - `RedactURLQueryToken(rawURL string) string`：保留 `?key=`、`?apikey=` 之外的 query（未来扩展 Gemini 时已有方案）。
2. **接入点（最少改动覆盖最多风险）**：
   - `observability/http.go` 的 `parseUsage` 前对 `tracked.body.Bytes()` 脱敏——这是会进入响应体缓存的唯一入口。
   - `request_logs` 不存完整响应体，但 `ErrorCode` 字段如果原本是上游原文应过脱敏（多半已是结构化 code 如 `upstream_error`，仍是兜底）。
   - 应用日志（`slog`）凡是 `Error(... error ...)` 输出上游 err 的，过 `RedactBearerToken`。
3. **不删上游 key 实际传输**：脱敏只影响"被记录/返回"的内容，不影响实际发往上游的请求头（那是功能本身）。

### 触及文件

- 新增 `internal/observability/redact.go` + 单测。
- 改 `internal/observability/http.go`：`parseUsage` 入参过脱敏。
- 改 `internal/observability/repository.go`：`RequestRecord.ErrorCode` 落库前兜底脱敏（如果是上游原文）。
- 排查 `internal/fault/*.go` 与各 v1 handler 的 `logger.Error(...)` 调用点，统一过脱敏。

### 验证

- 单测：含 `Authorization: Bearer nvapi_xxx...` 的字符串经脱敏后只保留 `<redacted>`；正常对话内容不含该字段时原样返回。
- 构造一个 mock 上游返回带 Authorization 回显的错误体，断言 observability 日志中不含明文 token。

### 风险/取舍

- 兼容性低风险，脱敏是单向过滤。
- 注意不要误伤合法包含 `Bearer` 字样的对话内容——脱敏规则要求**前缀 + 长 token 形状**双重匹配，避免误删普通文本里出现 `bearer` 一词。

---

## B3. Key 主动巡检恢复（P1）

**命中：** 永久吊销的 NVIDIA key 会在"恢复→失败→冷却"空转，反复消耗请求<br>
**借鉴对象：** gpt-load 的 `CronChecker` 每 5 分钟调用 `Validator.ValidateSingleKey` 对 invalid key 探活恢复

### 现状

我们的 `nvidiakey` 已有 `validator.go` 的 `Test` 函数（调 `/v1/models` 探活），但**只在管理后台手动触发**。冷却到期会被 pool 自动恢复，但若 key 已被 NVIDIA 永久吊销，它每次恢复后被第一个请求命中→失败→重新进冷却→到期再恢复，循环不止，每次还浪费一个用户请求做"探针"。`auth_invalid` 标志也存在（401/DisableKey 时置位），但**没有任何机制让 auth_invalid 的 key 自动重新校验**——它就永远趴在那里直到运维手动 Test。

gpt-load 的做法：定时主动遍历 invalid key 调上游验证接口，成功则恢复。这把"是不是可用"的判定从"被动等用户请求打爆"前移到"主动探针"。

### 设计要点

1. **新增 `internal/nvidiakey/healthchecker.go`**：`HealthChecker` 持 root context 周期性运行（默认 10 分钟一次，可关 runtimeconfig）。
2. **探活范围**：周期内遍历 `nvidia_keys` 中所有 `auth_invalid=1` 或 `cooldown_until < now` 但 `enabled=1` 的 key（即运维没禁用、但状态不健康的 key）。
3. **并发限速**：用 worker pool（默认 4 个并发），避免一次巡检打爆上游或本地 SQLite 单连接。gpt-load 用 `KeyValidationConcurrency=10`，我们的 SQLite 单连接偏向更低并发。
4. **探活结果处理**：复用 `Validator.Test`，得到 `valid`/`invalid`/`temporarily_unavailable`/`indeterminate`：
   - `valid` → `MarkSuccess`，恢复为 active（写入 lifecycle 不需用户请求触发）
   - `invalid`（401）→ 维持 `auth_invalid`，记录上次检查时间（避免下次立刻重试）；连续 N 次 invalid 可考虑自动 `enabled=0`（运维侧后续可调）
   - `temporarily_unavailable`/`indeterminate` → 不动状态，等下个周期（不主动改 cooldown，避免与运行时请求路径竞争）
5. **与运行时路径解耦**：探活走独立的 `nvidia.Client` 调用，不进 pool 的 lease 流程，不影响请求调度；MarkSuccess 的快照合并复用既有事务路径（已有 `repository.MarkSuccess`），不新增并发路径。
6. **节流**：每 key 维护 `last_checked_at`，周期内只探一次（避免 bot 反复打同一永久吊销 key）；连续 invalid 可拉长下次探活间隔（指数退避 10min→1h→6h）。

### 触及文件

- 新增 `internal/nvidiakey/healthchecker.go` + 测试。
- 改 `internal/nvidiakey/repository.go`：可能需 `ListKeysForHealthCheck(onlyInvalid bool) ([]KeySnapshot, error)` 与 `UpdateLastCheckedAt(keyID, time)`。
- 改 `internal/nvidiakey/repository.go` 的 schema：考虑加 `last_health_check_at` 字段（迁移 005）；不强制，可先复用 `cooldown_until` 推断。
- 改 `internal/app/app.go`：启动 HealthChecker goroutine，root context cancel 时退出。
- runtimeconfig 新增 `health_check_interval_ms`、`health_check_enabled`、`health_check_concurrency`。

### 验证

- 单测：mock validator，valid/invalid/unavailable 三种结果下的状态变化、连续 invalid 的退避。
- 集成场景：在测试机导入一个真实被吊销的 NVIDIA key，观察它进入 auth_invalid 后是否被巡检识别，是否停止空转消耗请求。
- 不破坏既有请求路径：HealthChecker 运行期间并发请求，pool acquire 不受影响（共用同一 Repository 但走独立事务）。

### 风险/取舍

- **探活也消耗 NVIDIA 配额**：调用 `/v1/models` 一般免费但要算调用次数。10 分钟一次、4 并发，单 key 一天 144 次探活——可控。
- **auth_invalid 自动清零**：当前 auth_invalid 是"运维干预点"（错误认证的 key 不应该自动恢复，避免僵尸 key 反复尝试）。所以**默认只对"非 auth_invalid 的不健康 key"做巡检**，auth_invalid 的 key 仍靠运维手动 Test 恢复。这是与 gpt-load 的关键差异——我们对永久吊销更保守。

---

## B4. 可配置故障转移状态码匹配（P1）

**命中：** `fault.Classify` 状态码判定逻辑硬编码（401/403/429/5xx 在代码里各分支），加一种新错误模式要改代码<br>
**借鉴对象：** gpt-load 的 `FailoverStatusCodeMatcher` —— `"429,500-599"` 字符串 spec，单码/范围/逗号分隔，O(log n) 二分匹配

### 现状

`internal/fault/classifier.go` 的 `Classify` 把每个状态码的归因写死：401 → credentialFault，429 → rateLimit（带 Retry-After），5xx → upstreamFault。这是合理的，因为这些分类语义稳定。但**"哪些状态码触发换 key 重试"这件事本身应该是运维侧可调**——

例如运维想知道"403 时也换 key 重试，不直接给客户端 503"，或"503 不要换 key 直接告知客户端上游挂了"，目前都要改代码重新发版。gpt-load 把这条做成组级可配 spec 串，运维在后台改一个字符串即可。

### 设计要点

1. **保持 `Classify` 的"误判码"逻辑**：401 永远是认证错误、429 永远读 Retry-After、400/422 永远是请求错误——这些是协议事实，不应可配。
2. **可配的是"是否换 key 重试"**：新增 `runtimeconfig` 字段 `failover_status_codes`（字符串，默认 `"429,500,502,503,504"`），由独立的 `failover.Matcher` 解析与匹配。
3. **降级兜底**：当 `failover_status_codes` 为空或解析失败时回退到硬编码默认集（保持向后兼容、避免配置错让所有请求不重试）。
4. **接入点**：`Attempt.Run` 在每次 fault 拿到后，调 `matcher.Match(fault.HTTPStatus)` 决定是否进入下一轮重试，而非硬编码 `currentFault.Retryable`。`fault.Classify` 仍生成原有的 `Retryable` 字段用作**默认值**（即未配置时行为不变），Matcher 仅做**覆盖决策**。
5. **不引入"组级"配置层级**（gpt-load 是组级）：MVP 阶段全局一个配置即可，避免引入 B7 的多级配置体系造成的复杂度。后续如果需要按模型组差异化，再扩展。

### 触及文件

- 新增 `internal/fault/matcher.go`：解析 spec 字符串 + `Match(int) bool`，区间排序合并 + 二分（抄 gpt-load 的 `status_code_matcher.go` 思路）。
- 改 `internal/fault/classifier.go`：`Fault` 增加暴露 `HTTPStatus` 字段（如未暴露）给 Matcher 用。
- 改 `internal/router/attempt.go`：拿 fault 后先 `matcher.Match` 决定 retryable。
- 改 `internal/runtimeconfig/`：新增 `FailoverStatusCodes` 字段 + 默认值 + 校验。
- 管理后台 settings 页面：暴露文本框写 spec，预校验合法格式。

### 验证

- 单测：spec 解析正确（单码、范围、逗号、换行分隔、无效输入降级）、Match 在区间内/外正确。
- 端到端：见下方"风险/取舍"——此条已与实际落地语义偏移，详见下一节。

### 风险/取舍

- **运维误配风险**：把 200 设进 failover 集会导致成功响应被反复重试，浪费 key 配额。校验时拒绝 `[200, 399]` 区间内码进入。
- **Classify 与 Matcher 双层**：略增复杂度，但分层清晰——"为什么错"（Classify）和"要不要换 key"（Matcher）是两个正交决策，分两层反而更可读。
- **本方案 §4 接入点与 §202 端到端的内在矛盾**（落地取舍已确认）：第 4 点要求"未配置时行为不变"（含 401/403 凭据类故障仍按 `Classify.Retryable=true` 自动换 key），而 §202 端到端要求"429 only 缩小后 502 不再换 key"。两者不可同时成立——`Retryable` 与 `matcher` 同或关系下，缩小 spec 无法关闭既有 5xx 重试；同与关系下，默认 spec 不含 401/403，凭据类故障不再换 key（与"行为不变"相违）。**落地选择为 union 语义**：`shouldRetry = currentFault.Retryable OR matcher.Match(HTTPStatus)`——保留默认场景下凭据/限流/5xx 的所有既有重试行为，运维只能用 spec **扩大**（如把 403 加入集让模型 disabled key 也换 key 重试），无法用 spec **关闭**既有故障转移。这一取舍的优点是零行为变更，缺点是 §202 的"缩减以禁用"端到端做不出来；如未来确实需要"运维可关闭"的能力，再升级为"二选一"语义并配套迁移默认集。已加 `TestAttemptFailoverSpecWidensRetryAcrossNonRetryableFault` 锁定 union 的扩展能力、并加 `runtimeconfig.Validate` 拒 [100,399] 码作为前置护栏。

---

## B5. 日志保留天数可配（P2）

**命中：** `observability/cleanup.go:11` 硬编码 `requestLogRetentionDays = 30`<br>
**借鉴对象：** gpt-load 的 `request_log_retention_days=7`（可配）

### 现状

清理 worker 写死 30 天。低频小流量场景 30 天可能偏多（占 SQLite 空间），高流量场景可能想留更长审计。这是低改动高便利的项。

### 设计要点

1. `runtimeconfig` 新增 `request_log_retention_days`（int，范围 1-365，默认 30）。
2. `CleanupWorker` 从快照读最新值，每周期开始时取一次（不必每请求读，cleanup 是低频）。
3. 沿用 `cleanup.go` 的 root context 模式，无需新结构。

### 触及文件

- 改 `internal/observability/cleanup.go`：`CleanupWorker` 持 `runtimeconfig.Snapshot` 取值。
- 改 `internal/runtimeconfig/`：新增字段 + 校验。
- 改 `internal/app/app.go`：组装时注入 snapshot 引用。

### 验证

- 单测：retention=1 时 cutoff 边界正确；动态修改 retention 后下一个周期生效。

### 风险

- 极小。唯一关注点是 runtimeconfig 修改后立即生效，避免运维变 7 后下一个周期仍按 30 跑——只要每周期开始时重读快照即可。

---

## B6. 流式响应 `X-Accel-Buffering: no`（P2）

**借鉴对象：** gpt-load 的每个流式响应头加 `X-Accel-Buffering: no` 防 Nginx 缓冲

### 现状

我们的应用层 `Flush()` 调用做对了，但默认部署链路里如果用户前置 Nginx 缓冲（`proxy_buffering on` 是 Nginx 默认！），SSE 客户端体验会变差——客户端看不到流式效果，等 Nginx buffer 满了一波吐出。`Linux单机部署说明.md` 提到生产在反代后部署但未强调 SSE 缓冲配置。

应用主动加这个头是**自描述**做法：哪怕用户忘了配 Nginx，应用告诉反代"我这个响应不要缓冲"。

### 设计要点

每个流式 handler 在写响应头时加 `X-Accel-Buffering: no`：
- `/v1/chat/completions` 流式（sse.Proxy 透传路径）
- `/v1/responses` 流式
- `/v1/audio/speech` 二进制流（speech 不严格是 SSE，但流式同样不该缓冲）

非流式不加（让 Nginx 正常缓冲压缩优化）。

### 触及文件

- 改 `internal/httpapi/v1/chat.go`、`responses.go`、`audio.go`（speech handler）：流式分支设头。
- 可考虑统一抽到 `sse.Proxy` 或 commit 之前的位置，避免散落。

### 验证

- 单测：流式响应头包含指定字段；非流式不含。
- 真实链路：用 Nginx 默认 `proxy_buffering on` 反代后打一个流式 chat，观察客户端是否实时收到 chunk（之前应该是批量到）。

### 风险

- 无。该头只被 Nginx 系反代识别，其他反代忽略，无副作用。

---

## B7. 超时配置按端点/模型组覆盖（P2）

**命中：** 全局统一 connect/firstByte/total 超时对 audio/speech 等慢端点不够精细<br>
**借鉴对象：** gpt-load 的三级配置优先级（组 > 系统 > 环境变量），含 `request_timeout`、`connect_timeout` 等均可组级覆盖

### 现状

`runtimeconfig` 的超时全局一份。audio/speech 比 chat 慢，全局超时要按最慢端点设，结果 chat 请求空等更久才被超时杀掉。这是"覆盖空隙"问题。

### 设计要点

1. 引入"端点级"（不是"组级"——本项目无组概念）的**覆盖层**：默认值来自 runtimeconfig，端点级配置存在更高优先级的设置里。
2. MVP 简化版：**仅支持几个维度可被端点覆盖**——`connect_timeout_ms`、`first_byte_timeout_ms`、`nonstream_total_timeout_ms`。流式总超时永远不设（跟随客户端）。
3. 存储方式：仍用单一 `runtime_settings` 表加新列 `endpoint`（NULL 表示全局默认），或新增 `endpoint_settings` 小表。MVP 偏后者，迁移更小。
4. 不引入 gpt-load 的完整三级链——本项目无环境组分层，二级（全局 + 端点）足够。

### 触及文件

- 改 `internal/runtimeconfig/`：新增 `EndpointOverrides` map。
- 改 `internal/router/budget.go`：`newBudget` 时按端点名查覆盖值。
- 改 `internal/app/app.go` 或 handler：传 endpoint 名给 budget 构造。
- 管理后台 settings：表单支持"为 /v1/audio/speech 单独设"。

### 验证

- 单测：audio/speech 拿到覆盖的更长超时值；chat 拿到全局默认。
- runtimeconfig 改 endpoint 覆盖后立即生效（atomic snapshot 替换）。

### 风险/取舍

- **复杂度爬升**：引入覆盖层会让"为什么这个请求超时是 60s"难以一眼判断——必须查全局+端点两处。需在 runtime admin API 返回**最终生效值**（merged），不只是原始配置。
- 建议先不做完整端点覆盖，只在确实差异大的关键维度（如 audio 总超时）开覆盖，避免过度配置化（违反 KISS）。

---

## B8. 平滑动加权轮询（WWR）选多上游（P3，暂不做）

**借鉴对象：** gpt-load 的 `channel/base_channel.go` Nginx 平滑 WWR 算法选 upstream

### 当前不做的理由

本项目目前只有单一 NVIDIA 端点（`Descriptor.WithBaseURL` 支持重写但实务单点），WWR 无用武之地。**只有未来出现以下场景之一才考虑引入**：
- NVIDIA 分多 region 端点，需要按权重分散流量
- 出现备用端点，主端点失败时切到备用

届时抄 gpt-load 的算法实现即可（`currentWeight += weight; best.CurrentWeight -= totalWeight`），单文件可落地，约 0.5 天。**MVP 阶段不投入**，作为已记录的扩展点。

---

## B9. 全局 Access Key 概念（P3）

**借鉴对象：** gpt-load 的全局代理 key（`proxy_keys`）+ 组级代理 key 双层，全局可在所有组用

### 现状

我们的 Access Key 每个独立记录绑定模型权限，没有"一个 key 访问所有模型组"概念。运维想给一个客户端全权限，现要为每个 model 单独建 key（每个 key 一个 `nvapi_` 字符串，客户端要管理多个）。模型数量增加时这件事会越来越烦。

### 设计要点

1. Access Key 表加 `scope` 字段：`per_model`（默认，向后兼容）/ `global`（全模型可用）。
2. `accesskey.Middleware` 验证时：`scope=global` 跳过模型权限 check；`scope=per_model` 沿用现逻辑。
3. 创建 key 时管理后台可选 scope。
4. 不引入"组级"（本项目无组），仅二档。

### 触及文件

- 数据库迁移 005：`access_keys.scope` 列默认 `'per_model'`。
- 改 `internal/accesskey/service.go`：Create 时记录 scope；Middleware 校验时分支。
- 管理后台：创建表单加 scope 选项。

### 验证

- 单测：global key 访问未在白名单模型时通过；per_model key 同场景被拒。
- 数据迁移测试：现有所有 key 默认 per_model，行为不变。

### 风险/取舍

- **向后兼容低风险**：默认 per_model 等价现有行为。
- **不做更复杂的 RBAC**：MVP 不必引入"哪些 model 集合可访问"，global + per_model 二档简单可用足够。避免一开始就做完整权限矩阵。

---

## 三、不应借鉴/应保留的本项目优势

公平列出 gpt-load 比较弱、本项目应**保留不动**的设计，避免误判：

| 设计 | 本项目做法 | gpt-load 做法 | 结论 |
|---|---|---|---|
| 按 model 粒度 key 屏蔽 | `nvidia_key_model_blocks` 表，单 key 单 model 屏蔽 | 整 key 黑名单 | 保留本项目——NVIDIA 单 key 多模型配额差异明显，按 model 屏蔽更贴合 |
| busy + 等待队列 | lease 期间 busy=true，FIFO 队列防单 key 打出 429 | 无 busy，靠"打爆再换" | 保留本项目——单 key 配额珍贵场景必须精确控速 |
| CommitState 显式流式 commit | `CommitState.committed atomic.Bool`，首字节后不换 key | 隐式保证（流式成功不重试） | 保留本项目——显式可审计，避免隐式假设 |
| 协议转换层 | responses→chat 完整状态机、采样参数白名单、reasoning 能力门禁 | 无（透明透传） | 保留本项目——这是核心价值 |
| 管理认证 | Argon2id + 首次强制改密 + 登录限流双键 + Origin 校验 + Secure Cookie | 单 AUTH_KEY 常数时间比对 | 保留本项目——暴露公网时合规性更高 |
| 密钥静态加密 | AES-256-GCM + HKDF 派生子密钥 + sentinel 校验主密钥 | 可选 ENCRYPTION_KEY，CLI migrate | 保留本项目——工程化更完整 |
| 主动 token 用量采集 | 从 SSE 末事件解析 usage 写入 request_logs | 未见主动解析 | 保留本项目——下游计费/分析依赖 |
| 集群能力 | 不支持（单实例 MVP） | Leader-Follower + Redis Pub/Sub | **暂不借鉴**，未来扩多实例时再考虑抄其架构 |

---

## 四、验证策略总体原则

1. **单元测试优先**：每项至少覆盖正常路径 + 关键边界（背压、降级、ctx cancel、配置缺失回退默认）。
2. **集成验证在测试机**（114.55.25.190 国内机或 149.71.241.250 国外机）：B1 在并发场景验证吞吐改善，B3 用真实被吊销 key 验证空转消除，B6 在 Nginx 反代后验证流式体验。
3. **不破坏既有审查清单已修项**：B6 流式头加在 `sse.Proxy` 同路径时要确认不影响 #6 的 idle timeout 修复、CommitState 的 commit 时机。
4. **每项独立可发版**：B1-B9 之间无强依赖（B5 与 B1 都改 observability 但互不冲突），可独立 PR、独立回滚。
5. **不增加 CGO 依赖**：B1 的批量落库仍走 `database/sql` + ncruces 纯 Go SQLite，不引外部依赖保持单二进制部署。

## 五、执行建议与排序

- **第一波（1.5 天）**：B1 完成。直接消除审查 #25 痛点，收益最高。
- **第二波（2.5 天）**：B2 + B3 + B4 并行进行（不同模块）。完成 P1 安全/正确性/运维灵活性核心缺口。
- **第三波（3 天）**：B5 + B6 + B7。体验型，可按需取舍 B7。
- **保留扩展点**：B8、B9 视未来需求触发再做，不在 MVP 内推进。

---

## 附：与代码审查优化清单的关联

本方案中各项不会替代审查清单的既有项，而是互补关系：

| 本方案编号 | 与审查清单的关联 |
|---|---|
| B1 | 直接修 #25（每请求 DB 事务是 SQLite 瓶颈） |
| B2 | 新增项（审查清单未单列，但属安全工程一致补强） |
| B3 | 与 #29（MarkSuccess 失败吞成功）相关——巡检路径同样走 MarkSuccess，注意 #29 修复后才能安全引入 |
| B4 | 与 fault.Classify 的硬编码改造相关，但不动 Classify 内已修的 #10/#11 |
| B5 | 与 #31（session 清理与日志清理撞同一连接）协同——批量入库后日志写入次数大减，#31 的连接争用缓解 |
| B6 | 与 #6（流式 idle 超时）路径协同，应同模块同时改 |
| B7 | 新增维度，审查清单未涉及 |

执行各编号时建议回头核对相关已修/未修审查项，避免重复劳动或与新引入逻辑冲突。
