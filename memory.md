# 记忆 — 可复用经验、约束与验证方法

> 约束：本文件不记录密钥、URL 凭据、日志原文、临时数据或普通测试通过结果；只沉淀可复用的方法、命令、限制与排障结论。密钥与完整 XApi 地址仅通过运行时 Secret 注入。

## 1. 项目约束（必读）

- 强制部署环境：国内 `114.55.25.190`（`hangzhou2-2`），国外 `149.71.241.250` 不用于星空代理联调。部署前依次读取 `D:\PROJECT_ZZZZZZZZZ\服务器管理\AGENTS.md`、`hangzhou2-2/AGENTS.md`、`hangzhou2-2/memory.md` 及本项目 `docker-compose*.yml`。
- 连接：`ssh -F .\ssh_config_local hangzhou2-2`（宿主机）或 `paramiko` 4.x 直连 `114.55.25.190:22`；Windows 无 `sshpass/plink`。
- 架构：单体 `nvida反代` 内置 XApi 采集→验证→池管理→CONNECT，不依赖独立代理池服务；空池/未就绪时不静默直连，返回临时不可用（`proxy pool disabled/no healthy proxy`）。
- 安全：`NVIDIA_ROUTER_XK_UPSTREAM_URL` 完整地址仅运行时注入，Web/日志/API 只返回脱敏 endpoint；不在 Git/文档/脚本/日志中写入真实凭据。
- XApi 特性：国内出口才可用，`qty=2` 为当前套餐上限（`qty=3` 返回 506），`host:port` TXT 解析，验证期望 `404`（`NVIDIA_ROUTER_XK_VALIDATION_STATUS`）。

## 2. 核心配置与默认值

| 变量 | 默认 | 说明 |
|---|---|---|
| `NVIDIA_ROUTER_MASTER_KEY` | 必填 32B RawURLBase64 | `openssl rand -base64 32 | tr '+/' '-_' | tr -d '='`，不落盘 |
| `NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD` | 必填 ≥12 | 首次改密前代理与敏感管理面不可用，`/health/ready` 非就绪 |
| `NVIDIA_ROUTER_XK_COLLECT_INTERVAL` | `5s` | 采集周期 |
| `NVIDIA_ROUTER_XK_PROXY_TTL` | `120s` | TTL；Grace 续期 `TTL/2=60s`，上限 `TTL*2=240s` 锚定 `ValidatedAt` |
| `NVIDIA_ROUTER_XK_EXPECTED_QTY` | `4`（2026-08-19 由 2→4） | 2–10，`4` 为生产推荐缓冲；需与上游 `qty` 套餐上限一致 |
| `NVIDIA_ROUTER_XK_CONCURRENCY` | `3`（由 2→3） | 1–10，验证并发 |
| `stream_first_token_timeout_ms` | `60000` | 推理模型建议 120s 客户端同步 |
| `stream_idle_timeout_ms` | `180000` | DeepSeek 长思考需大窗口 |
| 监听 | `0.0.0.0:3756`，Compose 绑定 `127.0.0.1:3756:3756` | 公网需受信反向代理 + `TRUSTED_PROXY_CIDRS` + `ADMIN_EXTERNAL_ORIGIN` + `ADMIN_SECURE_COOKIE=true` |

## 3. 已知限制与坑位

- **Validator HTTP/2**：验证与请求两条 transport 都必须 `ForceAttemptHTTP2: true`（`internal/xkproxy/validator.go:transportFor`、`manager.go:newTransport`、`upstream/nvidia/chat.go`），否则 CONNECT 到 CDN 后 `malformed HTTP response` 导致全量验证失败，池靠 Grace 无限续命 stale 出口。回归测试 `TestValidatorTransportEnablesHTTP2`。
- **Grace 续命**：上限必须锚定 `ValidatedAt` 而非 `now`，且跳过 `ValidatedAt=0` 的从未验证出口，否则每 5s 周期都会把 `ExpiresAt` 推到 `now+240s` 导致永不过期。测试 `TestPoolGraceCapAnchoredToValidatedAt`。
- **Collector 重试**：`validation_all_failed` 应在 `fetch` 内有界重试 3 次（利用 TXT 随机性抓活 IP），最终失败返回 `false` 触发退避（`nextInterval(true)` 重置会空耗配额）。
- **质量选路**：`orderByQuality` 的相对慢速阈值 `fastest*3` 仅比较 `RequestLatencyEWMA`，不混入 `probe` 延迟；冷启动期（无 req 样本）用 `probe` 兜底（>3s -15 / >2s -8 / >1s -3），有样本后切回 request。长尾从 20s→11s→3.4s。
- **Transport 错误映射**：`ReasonTransportFailed` 必须分类为 `Retryable` 的 `ScopeUpstreamGlobal` fault（`upstream_proxy_unavailable`）让 `attempt.Run` 换 Key 重试；否则直接 `return err` 短路，换 Key 循环不生效（并发 502 根因）。
- **OpenCodeFree 错误透传**：`serveOpenCodeFree` 对 `status>=300` 必须先映射（404→`upstream_model_not_found`/429→`upstream_unavailable`/其余→`upstream_error`），`ValidateNonstreamChat` 的 `ErrProtocol/ErrEmptyResponse` 需映射为 `fault.Protocol/EmptyResponse`，否则 `writeChatError` fallback 500 掩盖模型下线。
- **499 单列**：推理模型首字节 60–90s 易被客户端 60s 超时中断记 `499`，已单列为 `outcome=canceled`（`daily_stats.canceled_count` + `idx_request_logs_canceled_created`），`SuccessRate` 在分母中扣除 `canceled`，`/metrics` 与 `monitoring` 单独暴露，避免 30% 的 499 拖成 70% 失败率。迁移 `033_canceled_outcome.sql` 含 legacy fallback。
- **410 Gone**：裸 `deepseek-v4-flash` 已 410，迁移 015 映射到 `deepseek-ai/deepseek-v4-flash-0731`；白名单发现与 gateway 同步缺口会导致已下线 free 模型仍 enabled（需 `opencodefree.Client.Models` 定时禁用）。
- **Race**：无 CGO 时 `go test -race` 不可用，依赖锁结构与并发测试覆盖；`go vet` 必过。
- **多实例**：限流与池状态为内存态，不跨实例共享，需 Redis 才可多实例（当前不做）。
- **部署镜像行已参数化**（`docker-compose.deploy.yml` 的 image 行 = `${NVIDIA_ROUTER_IMAGE:-nvidia-router:local}`）：构建/启动直接注入 `NVIDIA_ROUTER_IMAGE=nvidia-router:deploy-<tag>` 即可，不要再用 sed 改镜像行；`git archive HEAD` 打包后 `cp` 旧 release 的 `.env`（chmod 600）+ `docker-compose.deploy.yml`，无新增迁移时 DB 直接兼容。
- **性能基线（2026-08-20 两轮优化后）**：详见 `docs/plans/2026-08-20-性能优化调研与实施.md` 与调研底稿 `docs/代码全量调研与优化建议.md`（含逐条状态）。要点：不需要换语言（Go 最优，Rust 仅窄模块 15% 收益）；热点对照表（crypto GCM 实例缓存、eventhub O(1) 环形、SSE AfterFunc 去 goroutine、MarshalFor raw 快路径、collector worker 池、validator keep-alive、manager RWMutex+Clone 外移、SQLite 035/036 索引）可直接复用；前端改动必须重建并提交 `internal/web/dist`（go:embed）。
- **SQLite 迁移命名坑**：`CREATE INDEX IF NOT EXISTS` 同名已存在时是 no-op，升级索引必须换新名字（035 的 model/access 部分索引因与 002 同名失效，036 用 `_v2` 后缀重建，`docs/代码全量调研与优化建议.md` #6）。新增迁移前先核对 `002_indexes.sql` 已有索引名。
- **opencodefree 请求体零拷贝**：`client.go:79` 已由 `strings.NewReader(string(body))` 改 `bytes.NewReader(body)`，25MiB 上限场景省全量拷贝。
- **契约红 = 前后端类型漂移**：后端 migration/结构体加字段后，`web/src/features/statistics/types.ts` 与 `contract.spec.ts` 必须同步（`canceled_count` 教训），跑 `pnpm --dir web run test` 验证。

## 4. 验证方法（最小充分）

```bash
# 本地（需 Go/Node/pnpm）
go vet ./...
go test ./...                          # 全量；tests/mocknvidia 含代理集成
pnpm --dir web run lint
pnpm --dir web run typecheck
pnpm --dir web run test                # 172+ 用例
pnpm --dir web run build
docker compose config                  # 需注入 NVIDIA_ROUTER_MASTER_KEY
bash -n scripts/test/live-xk-proxy.sh
NVIDIA_ROUTER_XK_PROXY_LIVE_SELF_TEST=1 bash scripts/test/live-nvidia.sh

# 单测聚焦
go test ./internal/xkproxy -run TestValidatorTransport
go test ./internal/xkproxy -run TestPoolGrace
go test ./internal/httpapi/v1 -run TestChat
go test ./internal/observability -run TestMetrics

# 真实联调（国内 hangzhou2-2，运行时注入）
# 需：NVIDIA_ROUTER_XK_UPSTREAM_URL / NVIDIA_ROUTER_LIVE_KEY / ADMIN_PASSWORD / BASE_URL / 模型名
bash scripts/test/live-nvidia.sh              # 全端点含代理链路
bash scripts/test/live-xk-proxy.sh            # 内置池静态检查
curl --fail http://127.0.0.1:3756/health/live
curl --fail http://127.0.0.1:3756/health/ready   # 首次改密前非就绪为预期
curl -H "Authorization: Bearer <ak>" http://127.0.0.1:3756/v1/models
# 监控：GET /admin/api/monitoring/summary?range=24h 观察 success/canceled/failure 分桶
# 指标：GET /metrics | grep nvidia_router_proxy_pool
```

- **判定**：`SKIP` 非 `PASS`；无逐 case `status=PASS` 不得宣称 live/E2E 通过；`live-nvidia.sh` 的租约/热连接指标 `BLOCKED` 时不算加速通过。
- **14 维测试**：见 `docs/项目测试方案.md`（D1 长任务/D2 思考/D3 输出/D4 工具/D5 协议/D6 链路/D7 性能/D8 容错 + M1 池/M2 Key/M3 认证/M4 目录/M5 可观测/M6 适配），并行编排将 70min 串行压缩至 30min（A 组 6 代理并行 + Soak 后台，B/C 独占串行）。

## 5. 排障速查

| 现象 | 定位 | 处置 |
|---|---|---|
| 池 `healthy 300+ latency_samples 0 remaining 240s` | Grace 续命 + validator h2 缺失 | 检查 `ForceAttemptHTTP2`，确认 `ValidatedAt` 锚定，清理 stale 后观察 `latency_samples=1` |
| 流式 240s 超时但非流式 200 | 池全死 IP + reasoning 长首字节 | 修 validator；客户端超时提至 120s，走 `opencode-free` 快路径验证 |
| 并发 502 `upstream_proxy_unavailable` 串行 0 失败 | 廉价共享租约并发上限，非换 Key 代码 | 降低并发或提高 `expected_qty/concurrency`，记录为物理边界 |
| `6 模型 500 internal_error` | `serveOpenCodeFree` 未映射非 2xx | 检查 `chat.go:198` 的 status 分支与 `ErrProtocol` 映射；禁用已下线模型 |
| 成功率 60% 含大量 499 | 客户端 60s 超时 vs NVIDIA 90s TTFT | 客户端/文档对齐 120s，监控用 `canceled_count` 单列 |

## 6. 文档与脚本约定

- 有效文档：`docs/*.md`（`docs/README.md` 索引）；历史归档：`docs/archive/*.md`（`archive/README.md` 说明）；阶段报告：`docs/plans/YYYY-MM-DD-*.md`。
- 代码调研底稿：`docs/代码全量调研与优化建议.md`（P0-P3 问题清单 + 对标项目 + 语言重写评估；实施前先读）。
- 测试脚本：可复用 `scripts/test/{live-nvidia,compose-acceptance,proxy-pool-integration-test,run-deepseek-stability,verify_remote}.sh`，诊断归档 `scripts/test/_archive/`（ignored）。
- 新增脚本：含 Secret 的必须 `umask 077` + `mktemp 600`，不打印 Key/URL；一次性诊断优先写 `D:\tmp\temp\`，用后删除。
- 日志/产物：根 `*.log`/`*.exe`/`.tmp-*`/`tmp/` 按 `.gitignore` 清理；`data/` 与 `key/` 不提交。

## 7. 代码库要点（2026-08-20 全量调研沉淀）

- **语言结论**：Go 是最优解，不换语言；Python/Node/Rust 均不划算。
- **热点瓶颈**（均已验证，详见 docs/代码全量调研与优化建议.md）：
  - 读查询未走 reader 池：`nvidiakey/repository.go:200 LoadEncrypted` 与 `modelcatalog/repository.go` 全走单写连接（MaxOpenConns(1)），与写事务争抢 → 吞吐天花板；`busy_timeout` 触发主因。
  - 同一 body 重复全量 JSON 解析 2-3 次：`chat.go:82,94,96` + `responses.go:62,77,79` 的 `ReasoningLevelFromBody/ReasoningFieldsFromBody` 各做一次完整 unmarshal。
  - 流式每 token 全量 unmarshal（chat.go:515 reasoning 采样），应先字节级短路。
  - `opencodefree/client.go:79` 应改 `bytes.NewReader(body)` 零拷贝。
  - 后台 goroutine 无 panic recover（collector.go:116,382,405）；`manager.Close`/`settings.Update` 锁内长阻塞（wg.Wait 上限 ~27s）。
- **已知坑**：`035_perf_indexes.sql` 部分索引因同名 no-op 未生效（002 已有同名索引）；`pool.go StickyGet` 双重 Unlock panic（死代码，启用即崩）；`ShouldBackOff` 未接入采集退避。
- **对标结论**：架构已覆盖通用方案（transport 池化、换 Key 重试防重放、读写池分离、SSE 硬上限）；可借鉴：出口 backup 分级、冷却渐进恢复、按错误类型分冷却阈值、重试/冷却事件指标、保池策略（healthy<expected 提前采集）、限流标准响应头。

## 9. 2026-08-20 OpenCodeFree 协议重试与真实模型矩阵

- `nearestLevel` 的 tie-break 根因：请求了具体 reasoning level 时，若它与 `auto` 的预算距离相同，旧排序可能先选 `auto`，导致 `low` 被错误归一化。修复顺序为精确请求值优先，其次非 `auto` 值之间按预算距离和较小预算排序；回归测试覆盖 concrete `low` 不降级。
- OpenCodeFree 非流式请求遇到 HTTP 200 但空响应或 malformed JSON 时，仅在响应尚未交付给客户端前重试一次；429 和流式请求不重试，避免放大网关限流或重放不可重放的流。
- `scripts/test/live-model-matrix.py` 的 `strength`、`low_repeat`、`output`、`repeat` profile 可复用；运行时只从 `NVIDIA_ROUTER_ADMIN_PASSWORD` 读取密码，并通过 `hangzhou2-2` 的 SSH 配置执行，不能把密码、Key 或完整上游地址写入参数、输出或记忆。
- 本轮 OpenCodeFree 三个白名单模型已覆盖 reasoning low/none、low/medium/high、native thinking、流式、工具、长输入、输出预算和重复稳定性；`hy3` 在 reasoning 消耗输出预算时可能以 `finish_reason=length` 结束，属于预算现象，不应误判为协议失败。
- 外部限制：OpenCodeFree DeepSeek 可能进入约 30 秒的上游 `429` 限流窗口，等待后恢复；该现象应单列为上游限流，不归因于路由器重试逻辑。`thinking disabled` 的 reasoning 输出在不同上游模型间不一致，现有 preserve-native-thinking 契约暂不改动。

## 8. 2026-08-20 模型白名单 UI 修复

- 测试任务后端按单一 provider 校验模型 ID，因此前端测试选择也必须按当前渠道维护：批量“选中启用模型/全选”只作用于当前渠道，切换渠道清空旧选择，勾选其他渠道模型时自动切换渠道并保留该模型。
- OpenCodeFree 已由后端允许启用，模型表格和卡片不能再用 provider 条件禁用停用模型的启用按钮；音频模型的能力验证门禁仍保留。
- 页面级验证应等待 URL 离开 `/admin/login`，不能用宽泛的 `/admin/*` 正则（该正则会立即匹配登录页）；登录响应、会话、模型 API 和可见复选框需分别核对。
- 离线 CLI 操作受应用进程锁保护：运行 `db backup` 或 `admin reset-password` 前先停止 app；备份目录若由 root 创建，临时 Compose 容器使用应用 UID 10001 时需先调整目录属主，备份文件保持 0600。密码仅通过 stdin 注入。

## 2026-08-20 观测批量写入外键修复与部署

- 根因：请求观测异步缓冲中的 `RequestRecord` 可能在 AccessKey/NVIDIAKey 删除后才刷盘；SQLite 的 `ON DELETE SET NULL` 只处理已落库行，不能处理队列中的旧 ID，导致整批 `RecordBatch` 因外键约束回滚。
- 修复：`internal/observability/repository.go` 在插入批次的同一事务内核对两个外键表，将已删除引用归一化为 `NULL`；`internal/observability/buffer_test.go` 增加删除后批量写入回归测试。
- 本地验证：观测模块测试、`go vet ./...`、`go test ./...` 通过；远端 `live-nvidia.sh` 的 `bash -n` 与 parser self-test 通过。
- 发布：`/opt/nvidia-router-releases/20260820-observability-fk-fix`，镜像 `nvidia-router:deploy-20260820-observability-fk-fix`；切换前 `nvr-data` 备份保存在该 release 的 `backups/`，权限为 `600`。回滚需使用同一基础 Compose + deploy override，保留外部 `nvr-data` 与 `router-internal`。
- 真实回归：创建临时 AccessKey，调用 `/v1/models` 和 OpenCodeFree Chat，立即删除 Key，等待超过默认 `BufferRecorder` 的 30 秒 flush interval；请求日志继续落库，删除后的 `access_key_id` 为 `NULL`，无新的 FK/flush/panic/fatal 错误。临时 Key 已清理。
- 认证教训：登录探针必须从运行时 Secret 注入目标密码；本地环境变量与目标值不一致会产生误导性的 401。CLI 密码重置必须停 app、备份数据卷并通过 stdin 注入，密码不落盘、不写入日志或记忆。
- 限制：目标机没有 Go，`live-nvidia.sh` 完整 Go live suite 不能仅凭远端 parser self-test 宣称通过；完整模型矩阵仍需具备运行时模型/Key 的条件，不能把 `SKIP` 当作 `PASS`。

## 2026-08-20 渠道状态 / 模型健康度

- 管理页面入口位于“资源接入”分组、代理池之后，用户可见标题为“渠道状态”，路由为 `/admin/channel-status`；内部接口保持 `/admin/api/model-health/*`，统一经过管理会话和 Origin 校验。
- 模型健康检测默认关闭，频率默认 60 秒、允许 10～3600 秒，并发默认 2、允许 1～8；启用后立即触发首轮，后续按持久化频率调度。立即检测只入队，不阻塞管理请求。
- 扫描使用模型白名单的完整列表（包括停用模型）；NVIDIA 模型使用当前可用 Key，OpenCodeFree 不传 NVIDIA Key。探测复用只读 `modelcatalog.TestModel`，不修改白名单、Key 状态、封禁状态或请求监控统计。
- 探测记录独立存储在 `model_health_probes` / `model_health_latest`，用安全错误类别和成功/失败/超时/跳过/取消状态展示；页面固定 60 段时间格，必须同时提供文字状态/详情，不能只依赖颜色。
- 前端生产构建后必须运行 `scripts/check-web-dist.sh`；Vite 路由懒加载的 JS/CSS 资源由入口 JS 的依赖图间接引用，检查器需要递归追踪依赖，否则会误报 stale asset。Windows 可用 `D:\Program Files\Git\bin\bash.exe scripts/check-web-dist.sh`。
- 只读视觉检查可用 Playwright 注入模拟 `/admin/api/auth/session` 和 `/admin/api/model-health/summary` 响应，在 1440/768/375/320 宽度验证三列/单列卡片、移动抽屉、无横向溢出和频率控件；不要连接真实上游。
- 摘要接口的事件、latest 和设置必须通过 `modelhealth.Repository.SummarySnapshot` 的同一只读事务读取；设置 PATCH 必须在写事务内读取当前行并应用字段级 patch，避免两个管理页面互相覆盖。
- 频率控件使用可编辑数字输入而不是有限 preset，显示秒单位，前端与后端共同限制 10～3600；摘要聚合需单独返回 `stale_count`，不能把过期状态并入 `unchecked_count`。
