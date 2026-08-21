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
- 候选发现排序约定（`internal/modelcatalog/candidate_sort.go` 的 `sortCandidates`）：OpenCodeFree `-free` 模型最前（按 ID 字母序）→ NVIDIA 按参数量（`(\d+)b` 正则，如 550b/120b/31b）从大到小，识别不出大小的按厂商前缀（`/` 前的 vendor）分组排序 → 其余非 free 的 OpenCodeFree 模型最后。排序在后端 `DiscoverCandidates` 完成，前端按返回顺序直出。

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

## 2026-08-20 全量审查 + 部署 20260820-full-review（= main bf409ce）

- **在机联调法（最省事、无外网明文）**：Windows 用 paramiko + `hangzhou2-2/ssh_host_key`（无需密码）连 `114.55.25.190`，把探针 py 上传到远端 `/tmp` 用 `python3` 跑，命中 `127.0.0.1:3756`。管理 API 的**变更请求（POST/PATCH/DELETE）必须带 `Origin: http://127.0.0.1:3756`**（同源 CSRF 守卫），否则 403 `invalid_origin`。JSON 结构：`/admin/api/proxy-pool` 与 `/admin/api/models` 都在 `data` 键下；monitoring `range` **仅 24h/7d/30d**（`1h/6h` 是 model-health 的，打到 monitoring 会 400）。AccessKey：`POST /admin/api/access-keys {name}` → 201，明文在 `key` 字段（一次性）；测试后 `DELETE` + `logout`。
- **代码健康结论**：`go vet`/`go build`/`go test ./...`/web lint+typecheck+test+build 全绿（修掉 1 个陈旧单测后）。全链路验证 OK：minimax-m3 经 下游→路由器→内置代理池→NVIDIA **797ms** 正确返回，流式 `[DONE]` 到达，思考强度 low/high 区分有效（deepseek reasoning_len 0→186）。
- **55% 成功率是外部驱动，非路由器 bug**（测试方案要求分层归因）：① `deepseek-ai/deepseek-v4-flash-0731` NVIDIA 侧病态慢，reasoning_effort=none + max_tokens=24 仍需 **88–150s**（首字节远超客户端 60s → 记 499/失败），是该模型上游延迟不是路由器；② OpenCodeFree 网关阶段性整体宕机，所有模型返回非标准 `HTTP 638`，路由器正确映射 502 `upstream_error`（`serveOpenCodeFree` 未知≥300→502）；③ `z-ai/glm-5.2` 偶发 `upstream_proxy_unavailable` 502（97ms 快速失败），根因是 XApi 出口薄（qty=2 套餐上限）导致瞬时无健康出口，属 fail-safe 正确行为。
- **`XK_EXPECTED_QTY` = XApi 请求的 `qty` 参数**（`upstream.go:119 query.Set("qty")`），当前套餐 `qty>2 → 506`，所以生产必须保持 `2`；memory 早前"推荐 4"与 506 现实冲突，**不要贸然调 4**（会致 fetch 全失败、池枯竭）。`XK_MAX_LATENCY` 未配=0（慢速门槛禁用）。
- **日志级别坑**：`slog.Default()` 默认 INFO，`pool_updated`/`proxy_validation_failed` 是 **DEBUG（不可见）**，只有 `validation_all_failed`（整批失败）是 WARN。**不能因看不到 `pool_updated` 就断定"0 次成功验证"**；应以 `/metrics` 的 `nvidia_router_proxy_pool_healthy` 和实际请求成功率为准。诊断验证失败用远端直连复现：读容器 env 的 XApi/验证 URL（不回显），`curl --proxy http://ip:port --http2 <validation_url>` 期望 404——实测多数代理 0.6–2s 返回 404/h2 成功，偶发 CONNECT 失败（curl_exit=56）。
- **改 admin 密码**：DB 已存在 admin 时 `INITIAL_ADMIN_PASSWORD` 无效，必须 CLI `admin reset-password`（app 必须先停、持进程锁、密码走 stdin）；`ResetPassword` 置 `must_change_password=0`，改完可直接登录且 `/health/ready` 保持就绪。部署顺序：停 app → **旧镜像** `db backup`（迁移前快照，`database.Open` 会跑迁移）→ **新镜像** `admin reset-password`（顺带应用迁移 037）→ `compose up` → 健康校验。孤儿容器 `nvidia-router-proxy-pool-1`（旧 release 20260811）与当前单体无关，属预期 warning。

## 2026-08-20 模型审计与预提交超时

- NVIDIA 真实矩阵确认 `nvidia/nemotron-3-ultra-550b-a55b` 与 `stepfun-ai/step-3.7-flash` 会输出 `reasoning_content`；若白名单仍标记 `supports_reasoning=false`，小 `max_tokens` 会被思考消耗并出现空内容/`length`。迁移 038 将两者回填为 `thinking` 原生线格式，descriptor 也必须同步能力提示。
- 代理池请求在写出请求后等不到响应头属于目标/模型首字节超时，不应被默认 504 failover matcher 再次扩散到所有 Key；用 typed fault 覆盖 matcher，连接在写出前失败仍保留重试。
- NVIDIA pooled Acquire 的短暂空池应复用 `AcquireWithWait`，但等待 context 必须绑定本次 `FirstByteDeadline`，不能让流式请求的补池等待越过 retry budget。
- OpenCodeFree 网关曾返回非标准 436；非流式首次遇到 436/标准瞬时 5xx 可重试一次，最终必须写出 502 `upstream_unavailable`，不能留下空 200。

## 2026-08-20 模型白名单多维审计与推理预算对账

- 审计方法：`scripts/test/remote_exec.py` + `model_whitelist_audit_remote.py`（在机探针，密码走 stdin）→ `orchestrate_audit.py` 分阶段 → `aggregate_audit.py`（逐用例）/ `summarize_audit.py`（总分、失败分布、延迟分位）；渠道归因用 `channel_baseline_remote.py`。报告见 `docs/plans/2026-08-20-模型白名单多维审计报告.md`。
- **`orchestrate_audit.py` 用 `capture_output=True`，阶段中途被杀会丢该阶段全部数据**（本次 stability 即如此）。需要中途可观察时改流式落盘。
- **能力模型无运行时闭环**：`supports_reasoning` 只来自 `descriptor.go` 硬编码列表、SQL 迁移、管理端 PATCH；`reasoning_content` 只在 `internal/observability/reasoning.go` 记指标，从不回写 catalog；`modelcatalog/service.go:344` 的能力探测只测可达性。声明与上游实际行为漂移会直接变成用户可见故障。
- **P0/P1 同源**：声明为 false 但实际会思考 → `chat.go:454-461` 返回 501（过度拒绝）；声明支持但级别无强制力 → 思考吃光预算返回空内容。两者都是"路由器推导的数与上游行为无因果连接"。**只修 P0 会把症状平移成 P1**，必须成对修。
- **`openai` 线格式丢弃数值预算**（`compat/reasoning.go` 的 openai 分支只发裸 `reasoning_effort` 字符串），所以 NVIDIA openai 线格式模型的 low/medium/high **没有任何节流作用**；实测 m3、glm-5.2 各档 reasoning 长度非单调，只有 `none` / `thinking:disabled` 的关断是确定性的。据此新增 `ReasoningProfile.AdvisoryLevels`（`Provider==nvidia && wire==openai` 时为真），把所有启用档归一为标准值 `high`，只保留开/关语义。**不能归一到 `auto`——那不是 OpenAI 标准取值，上游可能 422。**
- **`thinking` 线格式才有真实杠杆**：`ApplyReasoning` 现将 `budget_tokens` 压到 `max_tokens`（含 `max_completion_tokens`/`max_output_tokens` 拼写）的 3/4，为答案保留 1/4；无 `max_tokens` 时不压（无从对账），`auto`(-1) 与 `disabled` 不受影响。回归测试 `internal/compat/reasoning_budget_test.go`。
- 测试 profile 坑：`ZeroAllowed=false` 时 `availableLevels` 会丢掉 `none`，`thinking:{"type":"disabled"}` 会被 `nearestLevel` 拉回 enabled。构造推理 profile 的单测必须显式设 `ZeroAllowed: true`。
- 模型实测结论（NVIDIA 渠道，2026-08-20）：nemotron-3-ultra-550b 最均衡（94%，长文 14274 内容 / 236 思考）；glm-5.2 工具调用 6/6 全绿但流式最大空档 4.3s，客户端 idle 超时不应低于 10s；minimax-m3 长上下文预填充最快（8K 仅 3.9s）但长文生成会把预算全烧在思考上；step-3.7-flash 长任务能力弱（8K 预填充 191s，TTFT 可达 173s）。`deepseek-ai/deepseek-v4-flash-0731` 120s 无首字节，建议停用。

## 2026-08-20 OpenCodeFree 502 根因与代理边界

- **内网网关不能走出口代理**：网关是 Compose 服务别名（单标签主机名，端口 6020），只有本机网络可达；`opencodefree/client.go` 的 `do()` 原先在代理已配置时把每次调用都送去外部 XApi 出口，出口无路由到私有地址，返回**非标准状态 638**，被映射成 502 `upstream_error`，看起来像整渠道宕机。属 `c0cafc7`「代理池接入」的回归。修复：`NewClient` 用 `isLocalHost` 判定回环/私有网段/链路本地/未指定地址/不含点的单标签名 → 直连。**判断代理是否该介入的准则：代理只为对公网端点隐藏来源地址；目标只有本机可达时，代理既不可行也无意义。**
- **638 会污染代理池**：`attemptThroughProxy` 对 `status>=500` 调 `ReportHTTPFailure`，于是这个配置层错误把健康出口按 HTTP 失败隔离，持续劣化质量排序。排查代理池异常时，先确认是否有非标准状态在被当作真实上游失败上报。
- **诊断方法（网关容器无 curl/wget/python3，但有 node）**：`docker exec -i <gateway> node -e <script>`，Key 从 app 容器 `printenv` 读取后经 **stdin** 注入 node（不进 argv、不进宿主机进程表），并在输出前 `replace(key,'[redacted]')`。脚本 `scripts/test/opencodefree_{diagnose,authed_probe,model_probe}_remote.py` 可复用：分别覆盖路由器侧、网关侧直连、单模型多形态。
- **区分"渠道故障"与"链路故障"的通用手法**：同一时刻做两侧对照——经路由器探测 vs 从上游容器内用同一把 Key 直连。两侧结论不一致即说明故障在中间链路，不要凭路由器侧的 5xx 就判定上游宕机。
- **`muse-spark-1.2-contributor-free` 不可用**：网关返回 403 `RegionError`「This model is not available in your country.」，非流式/流式/长推理三形态稳定复现。出站由网关自身发起、不经路由器出口池，且 XApi 出口本身即国内，换出口无效。**已决定不支持**。网关另有 `mimo-v2.5-free`、`nemotron-3.5-lightning-free`、`laguna-s-2.1-free` 及一批 claude/gemini 模型未纳入白名单，纳入前必须逐个实测而非只看 `/v1/models` 列表。
- **代理错误 reason 此前完全不可观测**：`xkproxy` 四种 `ErrorReason` 中 `TransportFailed` 与 `ProxyRejected` 共用 502 与完全相同的公开文案，`Error()` 返回常量字符串不含 reason，全链路无一处记录。已在 `attempt.go` 补 `slog.Warn("proxy_error", reason, cause_type, key_id)`——只记 cause 的**类型名**，不记文本（可能内嵌出口地址）。
- **反查代理失败类型的特征表**（不依赖日志时可用）：`ReasonNoHealthyProxy` → **503** +「upstream proxy **pool** is temporarily unavailable」；`ReasonTransportFailed` → **502** +「upstream proxy is temporarily unavailable」；`ReasonProxyRejected` → 经 `writeChatError` → **502** + 同一文案。另：NVIDIA 与 OCF 路径都接了 `AcquireWithWait`，空池会轮询等待，**首个 tick 250ms**——失败快于 250ms 即可排除"池空"。

## 2026-08-21 全量审查、发布 main 与部署 20260821-full-audit

- **部署脚本**：`scripts/deploy/deploy_remote.py <tag>`，一条命令完成打包→上传→继承 `.env`/`docker-compose.deploy.yml`→构建→停 app→旧镜像备份→新镜像启动→健康校验，失败即停且打印回滚坐标。线上结构固定为：release 目录 `/opt/nvidia-router-releases/<tag>`、镜像 `nvidia-router:deploy-<tag>`、compose 用 `docker-compose.yml + docker-compose.deploy.yml`、数据卷 `nvr-data`（external）。
- **国内构建必须传 GOPROXY**：目标机访问不了 `proxy.golang.org`，`docker build` 必须带 `--build-arg GOPROXY=https://goproxy.cn,direct`（Dockerfile 第 27-28 行已注明）。漏了会在 `go mod download` 卡 90s 后超时失败。
- **审查方法**：按包分域并行下发子代理（router/pool、xkproxy/upstream、httpapi/安全、database、protocol/sse/app），要求每条结论给 file:line + 具体失败场景 + 标注未验证项。34.5k 行代码一轮产出 24 项发现，其中约 1/3 是真缺陷。
- **甄别纪律（本轮两次主动回退）**：子代理报的"缺陷"若与**带理据注释的既有测试**冲突，先判断是不是有意设计，不要单方面推翻。`xkproxy` 系统性故障期计数饱和（`http_failure_test.go` 断言 audit H8 有意为之）与 `Retry-After` 归零冷却（`TestClassifierPreservesPastRetryAfterAsZeroCooldown`，HTTP-date 传输途中过期时立即重试是对的）两项都已回退，留作待决。
- **回归测试必须能失败**：每个修复都用「临时注掉修复行 → 测试必须失败 → 恢复 → 必须通过」验证过。曾写出一个空转测试（断言守卫响应头，但该头由包装器设置、与内层 handler 无关），必须额外断言内层 handler 的实际产物。
- 本轮修复的真缺陷：`MarshalFor` 快路径丢弃消息归一化（P0，legacy 工具历史请求被上游 422）；`/metrics` 无鉴权（P0）；模型测试探针未 `MarkComplete` 导致健康出口被记失败、颠倒整池质量排序（P1）；前端契约要求 `success+failure==request_count` 但 canceled 单列，一个 499 即让统计页全空（P1）；`adminaudit.Recorder` 从不设 `CreatedAt` 导致审计时间戳恒为零值（P1）；`model_health_probes` 只写不删而摘要最宽只读 7 天（P1）；`/admin/api/stats/cost` 未注册导致成本面板 404、审计把硬删除记成 revoke（P2）。
- **上线后验证的最小集**：`/metrics` 匿名必须 401、带会话 200；能力位是否随迁移落库（`GET /admin/api/models` 查 `supports_reasoning`/`reasoning_wire_format`）；此前 501 的模型改为 200；小 `max_tokens` + high 档下 `content_chars > 0`。脚本 `scripts/test/post_deploy_verify_remote.py`。
- **注意**：`AdvisoryLevels`（openai 线格式档位归一）当前对线上四个 chat 模型**空转**，因为它们都已是 `thinking` 线格式。真正生效的是预算对账。

## 2026-08-21 二轮全量审查、发布 main 与部署 20260821-review-2

- **推理"关断"被误判为能力需求（本轮最大缺陷，链路已逐行确认）**：`parseReasoningEffort`/`parseThinking`/`parseReasoningFields` 对 `none`/`off`/`disabled`/`thinking:false` 一律置 `Requested: true`（调用方确实提了这个参数），而 `chat/request.go:97` 与 `responses/request.go:133` 直接把 `Requested` 当作 `Requirements.Reasoning` → `modelcatalog/capabilities.go` 判 `!model.SupportsReasoning` → `ErrCapabilityUnsupported` → `httpapi/v1/chat.go:477` **501 not_implemented**。后果：任何把 `reasoning_effort` 当全局默认发送的客户端，会丢掉全部非推理模型。修法是新增 `ReasoningSpec.RequiresReasoning() = Requested && Level != none`。
- **必须成对修，否则症状平移**：只放宽能力判定后，`MarshalFor` 的快路径会把 `reasoning_effort` 原样转发给 NIM，而 NIM 对 schema 外字段答 **422**（同理由见 `chat/request.go:186` 删 `max_completion_tokens`）。所以同时加 `compat.StripReasoning`（一次清掉 `reasoning_effort`/`reasoning`/`thinking` 三个互冗余别名）并把快路径条件收紧到 `!r.reasoning.Requested`。**收窄范围很关键**：strip 只在"显式关断 + 模型不支持推理"时触发，不能扩到 `Requested` 全集——`responses/request_test.go:236` 有带理据的既有测试，故意把推理请求转发给本地标记为非推理的模型交由上游裁决。
- **安全审计误报会撞上防泄漏测试**：修好 `checkProductionSecurity` 的监听地址判定后，`tests/mocknvidia` 的 `TestSecretsAndBodiesDoNotLeakIntoResponsesLogsOrSQLite` 稳定失败——告警文案里含 `Cookie` 字样，而泄漏扫描是 `bytes.Contains(artifact, []byte("Cookie"))` 裸子串匹配。**根因不在测试**：测试 harness 手搭 `config.Config`，`ListenAddress` 为空；而 `config.Load` 永远会补默认值（`config.go:137` + `valueOrDefault`），所以空地址只可能来自"自带 listener 的进程内 harness"，此时无 socket 可审计，直接 early return。**不要为了让测试过而削弱泄漏断言**。定位手法：在 HEAD 建 `git worktree` 跑同一测试，确认是本轮引入。
- **gofmt 此前无 CI 门禁**：golangci-lint 不带配置文件运行时，v2 默认**不启用** gofmt linter，于是 26 个文件已漂移，其中 3 个是真畸形（函数签名与首语句挤在一行）：`crypto/rotation.go`、`httpapi/v1/chat.go`、`observability/stats.go`。已在 `ci.yml` 加 `gofmt -l .` 门禁。
- **本机无法跑 `-race`**：`go test -race` 需要 cgo，本机 `CGO_ENABLED=0` 且 PATH 无 gcc。竞态只能靠 CI（`go test -race ./...` + `./tests/mocknvidia -count=10`），交付时要明说这条缺口。
- **改 admin 密码已脚本化**：`scripts/deploy/reset_admin_password_remote.py`，自身从 stdin 读密码 → 停 app（释放进程锁）→ `docker run --rm -i` 把密码只写进容器 stdin → 起 app → 登录 200 + 匿名 `/metrics` 401 双验证。脚本内无任何凭据，密码不进 argv/文件/日志/远端进程表。`admin reset-password` 只读**一行** stdin、无二次确认，且 `openExistingRouterDatabase` 仅 `database.Open`，**不需要主密钥/env-file**（与 `db backup` 同）。
- **在机验证推理矩阵**：`scripts/test/reasoning_off_probe_remote.py`（6 形态 × 2 线格式）、`scripts/test/reasoning_profile_dump_remote.py`（导出启用模型的完整 reasoning profile）。线上 12 个用例全 200。
- **一次被数据否掉的假设，记下来避免重犯**：观察到 `stepfun-ai/step-3.7-flash` 即使收到 `thinking:{"type":"disabled"}` 仍返回 `reasoning_content`，先怀疑是 `availableLevels`（`compat/reasoning.go:429` 在 `ZeroAllowed=false` 时丢掉 `none`，被 `nearestLevel` 拉回 enabled）。**实测证伪**：该模型 `zero_allowed=true` 且 levels 含 `none`，路由器确实发了 disabled，是 NVIDIA 上游不理会 → 外部归因。对照组同时证明路由器侧正确：`openai` 线格式的 `opencode-free/nemotron-3-ultra-free` 在 `none` 下 `completion_tokens` 39→2、`reasoning_chars=0`。**判定"关断是否生效"必须跨线格式做对照，不能只看单模型。**
- **501 缺陷在当前生产不可达**：线上 11 个模型里三个非推理模型（`meta/llama-3.2-90b-vision-instruct`、`openai/gpt-oss-120b`、`opencodefree/x-preview-f-free`）**全部处于停用**，而停用模型在能力门禁之前就被拒。所以该修复的线上证据只能是"单测 + 部署产物含修复"，不能靠线上复现；用 `grep` 校验 release 目录源码（`/opt/nvidia-router-releases/<tag>/`）来确认镜像确实由含修复的源码构建。
- **子代理汇报的踩坑**：第一轮 7 个审查子代理里 6 个用 SendMessage 催报全部无响应。**根因是把"汇报"设计成了旁路信道**；改为让每个子代理的**最终返回文本就是报告全文**（Agent 工具的返回值），并在 prompt 里固定 `## 结论/覆盖范围/发现/未覆盖` 段式、限定最多 5 条、要求 file:line + 具体失败场景，才稳定拿到结果。

## 2026-08-21 前端前沿化改造发布与部署（efa5d93）

- GitHub `main` 提交 `efa5d93`（feat: 前端前沿化改造——暗色模式、命令面板、图表升级与登录页重设计），84 文件 +1925/-237，已推送 origin/main。工作区里上一轮未提交的前端修复（KeepAlive 轮询挂起、UnoCSS 扫描安全 variant 映射、pointer-coarse 触控目标）一并入库；提交前确认这些改动与本轮同主题且合并状态全量验证通过。
- 部署：`python scripts/deploy/deploy_remote.py 20260821-web-ui-efa5d93` 一条命令完成（git archive HEAD → 继承 `20260122-fix-cde` 的 `.env`/deploy override → GOPROXY=goproxy.cn 构建 → 停 app → 旧镜像备份 → 切换 → live/ready 校验）。本次无数据库迁移，DB 直接兼容。
- 备份：`/opt/nvidia-router-releases/20260821-web-ui-efa5d93/backups/predeploy-20260821-web-ui-efa5d93/router.db`（600，5.86MB）。
- 上线后验证（全部实测）：容器 healthy、restarts=0、OOM=false；近 3 分钟日志 panic/fatal 计数 0；嵌入 HTML 引用新资源 hash `index-CzSL9L4X.css`/`index-DRm58z-c.js` 且均 HTTP 200；匿名 `/v1/models`、`/metrics`、`/admin/api/models` 均 401，根路径 200；公网 `114.55.25.190:3756` health/live 与登录页均 200。
- 回滚链：20260821-web-ui-efa5d93 → 20260122-fix-cde。
- 未做（缺运行时凭据）：管理员会话级验证、真实模型请求与代理池预热后渠道判定。按既有教训，重启后 NVIDIA 渠道有预热期假 502，判定渠道故障前先读 `/metrics` 的 `proxy_pool_healthy`。

## 2026-08-21 前端「前沿高级」改造（Dark/命令面板/图表/登录页）

- **双主题落地方式**：`theme.css` 用 `:root`（Light）+ `[data-theme='dark']` 属性选择器两套 token；`shared/useTheme.ts` 模块级单例管理偏好（light/dark/system，localStorage `nvr-theme`），`initTheme()` 必须在 `main.ts` 首帧前调用防 FOUC；watch 用 `{ flush: 'sync' }` 让 DOM 属性立即落地。View Transitions 圆形扩散切换：`document.startViewTransition` + WAAPI 驱动 `::view-transition-new(root)` 的 clip-path，需在 CSS 里关掉默认交叉淡化；不支持/reduced-motion 直接瞬时切换。
- **对比度红线**：任何新文本/背景配对先跑 `python scripts/calc_contrast.py`（已含 DARK 段与 `_tint_on_surface()` 复现 color-mix），登记进 `docs/前端对比度配对表.md` 后才能进代码；当前 74/74。暗色状态色用提亮变体（success #4ac269 / warning #d9a53f / danger #f47067 / info #85b6ff），tint 底混 surface 不混白。
- **shortcuts.spec 硬约束**：UnoCSS 任意值里禁止 `bg-[var(--x)]/50` 这类 alpha 修饰符，半透明一律写 `color-mix(in srgb, ...)`。
- **ESLint no-undef 坑**：web 的 eslint 对 `.vue` 不注入 DOM 全局，类型位置必须写 `globalThis.HTMLElement` / `globalThis.KeyboardEvent` 等（AppShell 既有约定）。
- **vitest+happy-dom 环境怪癖**：命令面板带输入查询时，从事件派发上下文发起含懒加载组件的 `router.push` 会永远 pending（守卫跑完、afterEach 不触发）；同状态从测试主体直接 push 一切正常。单测断言面板契约用 `vi.spyOn(router, 'push')`，完整导航集成交给 router/AppShell spec。真实浏览器无此问题。
- **模块级单例测试法**：useTheme/useCommandPalette 是模块级 ref 单例，跨用例污染状态；用 `vi.resetModules()` + 动态 `await import()` 在 beforeEach 里拿干净实例。
- **图表升级要点**：折线图改 Catmull-Rom→Bézier 平滑曲线 + 渐变面积（SVG defs 渐变 id 必须实例唯一，用 Math.random）；hover 十字线 tooltip 用 HTML 覆盖层按百分比定位；失败趋势保留虚线作色盲第二编码。UiStat 支持 `sparkline`（归一化 viewBox+non-scaling-stroke）与数值 count-up（`useCountUp`，reduced-motion 直跳）。
- **验证命令不变**：lint/typecheck/test/build 全绿后 `go build ./...` 确认 embed；前端改动产物已重建进 `internal/web/dist`。

## 2026-08-21 第二轮修复（发布 20260821-review-3）与未决清单

- **`%w` 包 nil 仍是非 nil error**：`fmt.Errorf("...: %w", f())` 中 `f()` 返回 nil 时，得到的是文本为 `%!w(<nil>)` 的**非 nil** error。`modelcatalog/saveSelection` 因此让已存储的 opencodefree 模型永远无法再启用，且 `SaveSelectionsResult` 是单事务，同批次无关模型一并回滚。线上 Vue 页面 `ModelsView.vue` 的 `saveCandidates` 会先过滤掉已配置模型，所以 UI 走不到——**缺陷藏在"前端恰好绕过"的路径里，直接调 API 的脚本会立刻命中**。
- **修这条时踩的坑（重要方法论）**：最初按"该守卫是死代码"整段删除，结果既有测试 `TestSaveSelectionRejectsEnablingExistingNonNVIDIAProvider` 失败——它 seed 的是 `provider='openai_compatible'`，**不属于两种受支持 provider**，`validateEnabledProvider` 对它确实返回错误。所以守卫真正的作用是拦住"存储 provider 不受支持的历史行被 upsert 静默改写成 nvidia 并启用"。正确修法是只把 `%w` 包装收进 `if err != nil`。**教训：判断一段校验是不是死代码，必须把它的所有输入取值域走一遍，而不是只看当前业务里常见的那两个值；既有测试是取值域的现成证据。**
- **`EarliestCooldownExpiry` 缺 `> now` 过滤 → 健康检查空转烧配额**：`cooldown_until` 只有 `markSuccess` 会清除，探测持续失败的 Key 永久保留过去时间戳；`nextDelay` 对 `remaining<=0` 返回 0，于是扫描循环以"探测耗时"为唯一节流持续跑，每轮都发真实 NVIDIA `/v1/models`。**不要改 `nextDelay` 的 return 0**（`healthchecker_test.go:307` 有理据断言，关闭 half-open 间隙是有意的），错在 SQL。钩子签名改为接收 checker 自身 `clock.Now()`，让"是否仍在冷却"与"还要等多久"用同一时间源。
- **`nvidiakey.formatTimestamp` 是 `Truncate(time.Second) + RFC3339`**（定长），所以 `cooldown_until` 的字符串比较与时间序一致，SQL 里直接 `> ?` 是安全的。注意 `modelhealth` 用的是 `RFC3339Nano`（**变长**，去尾随零），字典序在小数位是前缀关系时会反转——两处不要混用同一套比较假设。
- **写"只删单条缓存"的回归测试要避开 TTL 混淆**：accesskey 认证缓存 TTL 30s。若用"推进 1 分钟让 Key 过期"来构造，survivor 的缓存条目也被 TTL 清掉，测试即使在有 bug 时也会失败/在修好后也不通过。正确构造是**让 Key 的过期时间短于缓存 TTL**（如 10s 过期 + 推进 15s），这样缓存条目仍在、走的才是"命中缓存但身份已过期"那个分支。
- **子代理配额耗尽的应对**：本轮 16 个子代理因 API 配额（`403 pre-consume quota failed`）集体失败。**失败前先 `git status` 确认它们没留下半成品编辑**——本次工作树是干净的，可直接自己接手。让子代理"最终返回文本即报告全文"这个改法是有效的（rev-catalog、rev-protocol 都交回了完整可用的报告）。
- **本轮已修**：见上一条 commit。**未修、已确认值得做的**（下一批）：
  1. P1 `responses/nonstream.go:177-203` `extractText` 只接受字符串/`[{type,text}]` 形态的 `reasoning_content`，对象形态使成功的 200 变成 502 并触发换 Key 重试；而 `upstream/nvidia/chat.go:287-317` 的 `hasTextValue` 和流式 `delta.go:107-138` 都容忍对象形态 → **同模型同形态，流式 200 / 非流式 502**。同函数 `:118` 的"reasoning aliases disagree"同样把 200 变 502。
  2. P2 `responses/stream.go:185-204` 流式工具调用的 `id` 只对 `name` 做迟到补正，`id` 迟到时 `call_id` 全程为空，客户端无法回提交工具结果；非流式 `nonstream.go:141-143` 有 `call_%d` 兜底可对齐。
  3. P2 `modelhealth` 在"所有 NVIDIA Key 都在冷却"时把模型报成 `no_credential`/未配置，与"真的没配 Key"混为一谈，运维处置动作完全不同。
  4. P2 `chat/request.go:144-155` raw 快路径把重复顶层键原样转发上游（慢路径 `marshalFields` 从 map 重建天然去重），"校验看到的字节"与"转发的字节"不是同一份。
  5. P3 拼错的 `reasoning_effort`（如 `"hgih"`）在 `!DynamicAllowed` 时被最近邻拉到 `none`，用户以为开了最高强度、实际关闭思考且无任何告警。
- **重启后立刻探测 NVIDIA 渠道会得到假 502**：容器重启会把 XApi 出口池清空重建，采集+验证完成前 NVIDIA 渠道返回 502 `upstream_proxy_unavailable`（OpenCodeFree 渠道不经出口池，同一时刻全 200，正好是天然对照组）。**部署后验证必须先读 `/metrics` 的 `proxy_pool_healthy` 再判定渠道故障**，否则会把预热期误报成回归。脚本 `scripts/test/pool_warmup_check_remote.py`（读池 gauge + 间隔重试 5 次）。本轮实测：预热后 healthy=23，NVIDIA 连续 5/5 全 200。

## 2026-08-22 前端全量重构「暖纸工作室 Warm Studio」（6cb85c2..0cb8ba4，五阶段）

- **五阶段提交链**：①地基 token/图标/图表/表格 → ②壳层 → ③高频页 → ④资源页 → ⑤观测页。设计文档 `docs/plans/2026-08-21-前端全量重构-design.md`；对比度 74→88 配对全过（新增 canvas-deep/surface-raised 两层）。
- **依赖**：`motion-v@2.4.0` + `@lucide/vue@1.33.0`（lucide-vue-next 已 deprecated 换官方继任包）。图标走 `shared/ui/icons.ts` curated 映射 + `<UiIcon name>` 不变；UiIcon 用 style width/height 而非 size prop（lucide 的 size 类型是 number）。lucide 新命名：Filter→Funnel、CircleHelp→CircleQuestionMark、History→FileClock。
- **pnpm store 坑**：本机 node_modules 曾由 store v11 链接而 pnpm 10.28.2 只认 v10；`CI=true pnpm install --store-dir D:\tmp\pnpm-store` 重链接即可，esbuild build-script 忽略警告不影响 vite。
- **PowerShell 往返写坏 UTF-8**：`(Get-Content) -replace | Set-Content` 会按 GBK 读中文再写坏（spec 文件曾损坏靠 git checkout 恢复）。批量替换一律用 `python -c io.open(encoding='utf-8')` 或 Edit 工具。
- **既有测试是行为契约**：CreateAccessKeyDialog 的 legacy copy（textarea+execCommand）测试带理据——管理面板可能跑在 HTTP 明文下 navigator.clipboard 不可用。换 UiCopyField 时必须把降级逻辑收进组件而不是删测试；复制成功/失败要有可见文字反馈。
- **组件契约红线**：`[data-testid="key-table"]` 的响应式类（hidden md:block）也被断言；视图加 useRoute/useRouter 后 bare mount 的 spec 要补 memory-history router（ProxyPoolView 先例）；emit 多参数处理器参数顺序必须与 emits 声明一致。
- **动效策略落地**：关键弹层（命令面板）保留 Vue Transition + 过冲 bezier `cubic-bezier(0.34,1.4,0.64,1)`（避免双动画系统），Motion/motion-v 用于批量操作条、AuthLayout 入场；reduced-motion 全局 CSS 守卫 + `useReducedMotion()` 显式判空双保险。
- **明确放弃项（记录防重复劳动）**：表格虚拟滚动不做（全部表格已分页 ≤200 行/页，语义化 table 结构优先）；ModelsView(899 行) 拆 composables 未做（风险大于收益，本轮只做视觉统一），后续单独任务再做。
