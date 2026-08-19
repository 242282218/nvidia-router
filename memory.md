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
- 测试脚本：可复用 `scripts/test/{live-nvidia,compose-acceptance,proxy-pool-integration-test,run-deepseek-stability,verify_remote}.sh`，诊断归档 `scripts/test/_archive/`（ignored）。
- 新增脚本：含 Secret 的必须 `umask 077` + `mktemp 600`，不打印 Key/URL；一次性诊断优先写 `D:\tmp\temp\`，用后删除。
- 日志/产物：根 `*.log`/`*.exe`/`.tmp-*`/`tmp/` 按 `.gitignore` 清理；`data/` 与 `key/` 不提交。
