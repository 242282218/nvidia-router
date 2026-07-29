# NVIDIA API 路由器第一轮实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法跟踪进度。任何需求冲突均以 `docs/NVIDIA API路由器第一轮需求文档.md` 为最高优先级，不得自行扩展范围。

**目标：** 构建一个面向个人使用、可管理多个合法 NVIDIA Build API Key 的 OpenAI-compatible 路由器，通过限流感知 Round Robin、单 Key 并发锁、FIFO 队列、分类冷却和首字节前故障转移，对外提供 Models、Chat Completions、Responses、Embeddings、Audio Transcriptions 和 Audio Speech 接口以及 Web 运维面板。

**架构：** 使用单实例 Go 应用承载数据面、控制面、SQLite、CLI 和嵌入式 Vue 管理端。数据面按“下游鉴权 → 请求校验 → 模型白名单 → FIFO/Key Lease → NVIDIA Adapter → JSON/SSE 转换 → 元数据统计”执行；协议转换器、调度器、数据库和 HTTP Handler 必须解耦。第一轮只实现 NVIDIA Build，不建立通用 Provider 抽象、用户平台、计费、多实例或分布式状态。

**技术栈：** Go 1.24+、`net/http`、`database/sql`、纯 Go/WASM SQLite 驱动 `github.com/ncruces/go-sqlite3/driver`、`log/slog`、`golang.org/x/crypto`；Vue 3、Composition API、TypeScript strict、Vite、UnoCSS、Vue Router、VueUse、pnpm；Go `testing`/`httptest`/race detector、Vitest、Vue Test Utils、Playwright；Docker Compose 单实例。

---

## 0. 执行规则

### 0.1 规范优先级

1. `docs/NVIDIA API路由器第一轮需求文档.md`：规范性需求，优先级最高。
2. 本计划：实施细化和已确认的歧义关闭结果。
3. `docs/9Router与NVIDIA反代调研报告.md`：调研背景，不得覆盖需求。
4. 9Router 固定提交 [`79918c7`](https://github.com/decolua/9router/tree/79918c7830695bbca4a45c9fea4a42c3e9fd73d1)：仅参考 NVIDIA wire format、能力表、SSE/Responses 行为。
5. New API 固定提交 [`c27d1ef`](https://github.com/QuantumNous/new-api/tree/c27d1ef651c608dd8b9e60848a7e0f13a8619d9b)：仅参考控制面分层、手动测试链路、流状态机、前端组织和单镜像构建。

发现本计划与需求文档冲突时，Codex 必须停止对应任务，记录冲突位置，不得静默选择实现。

### 0.2 许可证边界

- 采用 clean-room 实现，不复制 New API 或 RelayKit 的 AGPLv3 源码。
- 不把 New API、RelayKit 作为 Go 依赖。
- 如逐字复用 9Router MIT 代码，必须记录来源文件、固定提交和版权，并在分发物中保留 MIT 声明；默认优先按公开行为重新实现。
- 不通过代理绕过 NVIDIA 认证、区域、模型许可或配额。

### 0.3 禁止扩展

Codex 不得在第一轮加入：HTTPS、证书、反向代理、多管理员、团队成员、角色、权限、配额、计费、支付、其他 AI Provider、Redis、PostgreSQL、权重、优先级、随机路由、多实例、Kubernetes、Prometheus、后台巡检、Responses 状态存储、图片生成、Files、Batch、Assistants、内置工具或 MCP。

### 0.4 TDD 与提交规则

每项任务严格执行：

1. 写失败测试。
2. 运行指定测试，确认因缺少目标行为失败，而不是语法或环境错误。
3. 写最少实现。
4. 运行局部测试、race test、lint。
5. 只提交该任务相关文件。
6. 每次 commit 前运行 `git diff --check`；执行任务给定的 `git add` 后运行 `git status --short`，确认没有该任务产生但未暂存的文件。若有遗漏，先修正文档化的文件清单或提交命令，不得把脏修改留给后续任务。

若工作区尚未初始化 Git，仅任务 1 可执行 `git init`；不得自动创建远程仓库或 push。

---

## 1. 已锁定的实施决策

| 编号 | 决策 |
|---|---|
| D-001 | 默认监听 `0.0.0.0:3756`，Compose 映射 `3756:3756`。 |
| D-002 | Chat 支持 HTTPS `image_url` 和 Base64 Data URL；路由器不下载远程图片。 |
| D-003 | JSON 请求最大 32 MiB；单张 Base64 图片解码后最大 20 MiB；Audio 请求最大 25 MiB。 |
| D-004 | 连接、首字节和非流式总超时默认 10 秒、60 秒、5 分钟，通过管理页面持久化配置。 |
| D-005 | 首字节预算从每个上游 attempt 开始，覆盖连接、响应头和首个可输出事件；所有 attempt 同时受整个请求总预算。 |
| D-006 | 429 无 `Retry-After` 时从 5 秒指数退避到最大 5 分钟，并加入 ±20% jitter。 |
| D-007 | 网络错误和可恢复 500/502/503/504 冷却 15 秒；任意成功请求清零连续失败和退避级别。 |
| D-008 | 全忙进入 FIFO；全冷却返回 429；全停用/鉴权失效返回 503；全部对模型不可用返回 404。 |
| D-009 | 主密钥变量为 `NVIDIA_ROUTER_MASTER_KEY`，Raw URL Base64 解码后必须恰好 32 字节。 |
| D-010 | Cookie 为 HttpOnly、SameSite=Strict；写操作校验同源 Origin；登录每 IP 5 次/分钟并加入失败指数延迟。 |
| D-011 | 优雅关闭宽限期默认 60 秒。 |
| D-012 | Audio Adapter 完成后仍需真实 NVIDIA 联调门禁；未验证模型不可启用。 |
| D-013 | NVIDIA Key 明文只允许在导入验证或构造上游请求期间短暂存在于内存，不得长期缓存、记录或持久化。 |
| D-014 | Round Robin 在成功发放 Lease 时推进到下一个索引；failover 不重复尝试同一个 Key。 |
| D-015 | 上游 401 视为整 Key 无效；`/models` 的 403 视为整 Key 无效；模型请求的 403 默认记录 Key-模型不可用，除非上游明确返回账号/凭据失效码。 |
| D-016 | 可恢复 HTTP 5xx 仅为 500、502、503、504；其他 4xx/5xx 默认不换 Key。 |
| D-017 | Chat 图片允许 `image/png`、`image/jpeg`、`image/webp`、`image/gif` Data URL。 |
| D-018 | Audio 通过可重放请求体支持首字节前换 Key；临时文件权限为 `0600`，请求结束必删。 |

---

## 2. 架构与依赖方向

### 2.1 数据面

```text
HTTP /v1
  -> AccessKeyAuth
  -> BodyLimit
  -> Protocol Validator / Transformer
  -> ModelCatalog
  -> AttemptOrchestrator
       -> Pool.Acquire(model, attempted)
       -> NVIDIA Client
       -> FaultClassifier
       -> KeyStateWriter.MarkSuccess/MarkFailure
       -> Pool.Release
  -> JSON response 或 SSE/Binary writer
  -> RequestRecorder（只记录允许的元数据）
```

### 2.2 控制面

```text
HTTP /admin/api
  -> AdminSessionAuth
  -> OriginGuard（写操作）
  -> Admin Service
       -> NVIDIA Key Repository / Validator
       -> Access Key Repository
       -> Model Catalog
       -> Runtime Settings
       -> Statistics
```

### 2.3 依赖约束

- `internal/protocol/*` 不得 import 数据库、HTTP Handler、Admin 或 Pool。
- `internal/upstream/nvidia` 不得 import Web、Admin 或 SQLite Repository。
- `internal/pool` 只持有可调度元数据和 Lease，不持有长期 NVIDIA Key 明文。
- `internal/httpapi/*` 只做 HTTP 边界、DTO 和调用编排，不写 SQL、不实现协议转换。
- `internal/observability` 不接受请求/响应正文类型，只接受白名单元数据结构。
- 所有跨包错误使用 `%w` 携带上下文；公共错误由 `internal/apierror` 统一映射。

### 2.4 正式目录

```text
.
├── cmd/nvidia-router/main.go
├── internal/
│   ├── app/{app.go,server.go,shutdown.go}
│   ├── config/config.go
│   ├── clock/clock.go
│   ├── apierror/{error.go,writer.go}
│   ├── keystate/state.go
│   ├── crypto/{master.go,aead.go,digest.go,sentinel.go}
│   ├── database/{db.go,migrate.go,backup.go}
│   ├── database/migrations/{001_initial.sql,002_indexes.sql}
│   ├── adminauth/{password.go,session.go,ratelimit.go,origin.go}
│   ├── accesskey/{model.go,repository.go,service.go,middleware.go}
│   ├── nvidiakey/{model.go,repository.go,service.go,validator.go}
│   ├── modelcatalog/{model.go,repository.go,service.go,capabilities.go}
│   ├── runtimeconfig/{snapshot.go,store.go,repository.go}
│   ├── pool/{pool.go,lease.go,queue.go,state.go}
│   ├── fault/{fault.go,classifier.go,retry_after.go,cooldown.go}
│   ├── router/{attempt.go,commit.go,budget.go,replay.go}
│   ├── upstream/nvidia/{descriptor.go,client.go,models.go,chat.go,embeddings.go,audio.go}
│   ├── protocol/chat/{request.go,validate.go,rules.go,image.go}
│   ├── protocol/responses/{types.go,validate.go,request.go,nonstream.go,stream.go,state.go}
│   ├── protocol/embeddings/validate.go
│   ├── protocol/audio/{transcriptions.go,speech.go}
│   ├── sse/{event.go,decoder.go,encoder.go,proxy.go}
│   ├── observability/{request.go,repository.go,stats.go,cleanup.go}
│   ├── httpapi/{router.go,middleware.go}
│   ├── httpapi/v1/{models.go,chat.go,responses.go,embeddings.go,audio.go,unsupported.go}
│   ├── httpapi/admin/{auth.go,nvidia_keys.go,access_keys.go,models.go,runtime.go,stats.go,settings.go}
│   ├── httpapi/health/health.go
│   └── web/{embed.go,handler.go}
├── web/
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── uno.config.ts
│   └── src/
│       ├── main.ts
│       ├── router/index.ts
│       ├── shared/{api,components,types}/
│       └── features/{auth,nvidia-keys,access-keys,models,runtime,statistics}/
├── tests/{mocknvidia,live,e2e}/
├── scripts/test/live-nvidia.sh
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── .github/workflows/ci.yml
├── go.mod
├── go.sum
├── pnpm-workspace.yaml
└── README.md
```

---

## 3. 数据模型

### 3.1 `001_initial.sql`

`migrate.go` 在读取迁移账本前先执行以下固定 bootstrap DDL；它不属于版本迁移文件，且使用 `IF NOT EXISTS` 保证幂等：

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL
);
```

`001_initial.sql` 从以下内容开始：

```sql
CREATE TABLE crypto_sentinel (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  version INTEGER NOT NULL,
  nonce BLOB NOT NULL,
  ciphertext BLOB NOT NULL
);

CREATE TABLE admins (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  must_change_password INTEGER NOT NULL DEFAULT 1 CHECK (must_change_password IN (0, 1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE admin_sessions (
  id TEXT PRIMARY KEY,
  token_digest BLOB NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  revoked_at TEXT
);

CREATE TABLE nvidia_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,
  fingerprint BLOB NOT NULL UNIQUE,
  display_prefix TEXT NOT NULL,
  display_suffix TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  auth_invalid INTEGER NOT NULL DEFAULT 0 CHECK (auth_invalid IN (0, 1)),
  cooldown_until TEXT,
  cooldown_reason TEXT,
  cooldown_level INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_success_at TEXT,
  last_error_at TEXT,
  last_error_code TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE models (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  public_id TEXT NOT NULL UNIQUE,
  upstream_id TEXT NOT NULL,
  display_name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('chat', 'embedding', 'asr', 'tts')),
  enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  supports_vision INTEGER NOT NULL DEFAULT 0 CHECK (supports_vision IN (0, 1)),
  supports_tools INTEGER NOT NULL DEFAULT 0 CHECK (supports_tools IN (0, 1)),
  supports_reasoning INTEGER NOT NULL DEFAULT 0 CHECK (supports_reasoning IN (0, 1)),
  reasoning_wire_format TEXT NOT NULL DEFAULT 'none' CHECK (reasoning_wire_format IN ('none', 'openai')),
  capability_verified_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE nvidia_key_model_blocks (
  nvidia_key_id INTEGER NOT NULL REFERENCES nvidia_keys(id) ON DELETE CASCADE,
  model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  reason_code TEXT NOT NULL,
  upstream_status INTEGER,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  PRIMARY KEY (nvidia_key_id, model_id)
);

CREATE TABLE access_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  key_digest BLOB NOT NULL UNIQUE,
  key_prefix TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at TEXT
);

CREATE TABLE runtime_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  queue_capacity INTEGER NOT NULL DEFAULT 100 CHECK (queue_capacity BETWEEN 1 AND 10000),
  queue_wait_timeout_ms INTEGER NOT NULL DEFAULT 60000 CHECK (queue_wait_timeout_ms BETWEEN 1000 AND 600000),
  connect_timeout_ms INTEGER NOT NULL DEFAULT 10000 CHECK (connect_timeout_ms BETWEEN 1000 AND 120000),
  first_byte_timeout_ms INTEGER NOT NULL DEFAULT 60000 CHECK (first_byte_timeout_ms BETWEEN 1000 AND 600000),
  nonstream_total_timeout_ms INTEGER NOT NULL DEFAULT 300000 CHECK (nonstream_total_timeout_ms BETWEEN 1000 AND 1800000),
  shutdown_grace_ms INTEGER NOT NULL DEFAULT 60000 CHECK (shutdown_grace_ms BETWEEN 1000 AND 600000),
  updated_at TEXT NOT NULL
);

INSERT INTO runtime_settings (
  id, queue_capacity, queue_wait_timeout_ms, connect_timeout_ms,
  first_byte_timeout_ms, nonstream_total_timeout_ms, shutdown_grace_ms, updated_at
) VALUES (1, 100, 60000, 10000, 60000, 300000, 60000, '1970-01-01T00:00:00Z');

CREATE TABLE request_logs (
  request_id TEXT PRIMARY KEY,
  endpoint TEXT NOT NULL,
  model_id TEXT,
  access_key_id INTEGER REFERENCES access_keys(id) ON DELETE SET NULL,
  nvidia_key_id INTEGER REFERENCES nvidia_keys(id) ON DELETE SET NULL,
  http_status INTEGER NOT NULL,
  outcome TEXT NOT NULL,
  error_code TEXT,
  is_stream INTEGER NOT NULL CHECK (is_stream IN (0, 1)),
  queue_ms INTEGER NOT NULL DEFAULT 0,
  first_byte_ms INTEGER,
  duration_ms INTEGER NOT NULL,
  attempt_count INTEGER NOT NULL,
  prompt_tokens INTEGER,
  completion_tokens INTEGER,
  upstream_request_id TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE daily_stats (
  day TEXT NOT NULL,
  dimension_type TEXT NOT NULL CHECK (dimension_type IN ('global', 'model', 'nvidia_key', 'access_key')),
  dimension_id TEXT NOT NULL,
  request_count INTEGER NOT NULL DEFAULT 0,
  success_count INTEGER NOT NULL DEFAULT 0,
  failure_count INTEGER NOT NULL DEFAULT 0,
  total_duration_ms INTEGER NOT NULL DEFAULT 0,
  total_queue_ms INTEGER NOT NULL DEFAULT 0,
  total_attempts INTEGER NOT NULL DEFAULT 0,
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, dimension_type, dimension_id)
);
```

### 3.2 `002_indexes.sql`

```sql
CREATE INDEX idx_admin_sessions_expires ON admin_sessions(expires_at) WHERE revoked_at IS NULL;
CREATE INDEX idx_nvidia_keys_schedulable ON nvidia_keys(enabled, auth_invalid, cooldown_until);
CREATE INDEX idx_models_enabled_kind ON models(enabled, kind);
CREATE INDEX idx_key_model_blocks_model ON nvidia_key_model_blocks(model_id, nvidia_key_id);
CREATE INDEX idx_access_keys_active ON access_keys(key_digest) WHERE revoked_at IS NULL;
CREATE INDEX idx_request_logs_created ON request_logs(created_at);
CREATE INDEX idx_request_logs_model_created ON request_logs(model_id, created_at);
CREATE INDEX idx_request_logs_nvidia_key_created ON request_logs(nvidia_key_id, created_at);
CREATE INDEX idx_request_logs_access_key_created ON request_logs(access_key_id, created_at);
```

### 3.3 SQLite 启动约束

每次打开数据库必须执行并验证：

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;
```

单实例将 `db.SetMaxOpenConns(1)`，避免 SQLite 写竞争。迁移失败、checksum 改变或 PRAGMA 未生效必须拒绝启动。

---

## 4. 核心类型与稳定接口

后续任务不得随意更名。

```go
// internal/config/config.go
type Config struct {
    ListenAddress string
    DataDir       string
    TempDir       string
    MasterKey     [32]byte
    NVIDIABaseURL *url.URL
}
type LoadOptions struct { AllowInsecureTestUpstream bool }
func LoadFromEnv(opts LoadOptions) (Config, error)

// internal/modelcatalog/model.go
type Kind string
const (
    KindChat Kind = "chat"
    KindEmbedding Kind = "embedding"
    KindASR Kind = "asr"
    KindTTS Kind = "tts"
)

type Model struct {
    ID int64
    PublicID string
    UpstreamID string
    DisplayName string
    Kind Kind
    Enabled bool
    SupportsVision bool
    SupportsTools bool
    SupportsReasoning bool
    ReasoningWireFormat string
    CapabilityVerifiedAt *time.Time
}

// internal/fault/fault.go
type Scope uint8
const (
    ScopeRequest Scope = iota
    ScopeCredential
    ScopeModelCredential
    ScopeTransientCredential
    ScopeUpstreamGlobal
)

type Fault struct {
    HTTPStatus int
    Scope Scope
    Retryable bool
    RetryAfter time.Duration
    DisableKey bool
    BlockModel bool
    PublicType, PublicCode, PublicMessage string
    Cause error
}

// internal/pool/lease.go
type Lease interface {
    KeyID() int64
    Release()
}

// internal/nvidiakey/service.go
type KeyStateWriter interface {
    MarkSuccess(ctx context.Context, keyID int64) (keystate.KeySnapshot, error)
    MarkFailure(ctx context.Context, keyID, modelID int64, f fault.Fault) (keystate.KeySnapshot, error)
}

// internal/keystate/state.go
type KeySnapshot struct {
    ID int64
    Enabled bool
    AuthInvalid bool
    CooldownUntil *time.Time
    CooldownLevel int
    ConsecutiveFailures int
}
type ModelBlock struct { KeyID, ModelID int64 }

// internal/pool/pool.go
type StateSync interface {
    LoadSnapshot(keys []keystate.KeySnapshot, blocks []keystate.ModelBlock)
    UpsertKey(key keystate.KeySnapshot)
    RemoveKey(keyID int64)
    ApplySuccess(keyID int64)
    ApplyFailure(keyID, modelID int64, f fault.Fault, persisted keystate.KeySnapshot)
    SetModelBlock(keyID, modelID int64, blocked bool)
}

// internal/runtimeconfig/snapshot.go
type Snapshot struct {
    QueueCapacity int
    QueueWaitTimeout time.Duration
    ConnectTimeout time.Duration
    FirstByteTimeout time.Duration
    NonstreamTotalTimeout time.Duration
    ShutdownGrace time.Duration
}
type Provider interface { Snapshot() Snapshot }

// internal/nvidiakey/service.go
type SecretProvider interface {
    WithSecret(ctx context.Context, keyID int64, fn func([]byte) error) error
}

// internal/router/attempt.go
type ExecuteFunc func(ctx context.Context, keyID int64, secret []byte, commit *CommitState) (*http.Response, error)
type AttemptResult struct {
    Response *http.Response
    Lease pool.Lease
    Attempts int
}

// internal/sse/event.go
type Event struct {
    Event string
    ID string
    Retry string
    Data []string
    Comments []string
}
```

---

## 5. 路由和错误契约

### 5.1 对外路由

| Method | Path | 鉴权 |
|---|---|---|
| GET | `/health/live` | 无 |
| GET | `/health/ready` | 无 |
| GET | `/v1/models` | 下游 Bearer Key |
| POST | `/v1/chat/completions` | 下游 Bearer Key |
| POST | `/v1/responses` | 下游 Bearer Key |
| POST | `/v1/embeddings` | 下游 Bearer Key |
| POST | `/v1/audio/transcriptions` | 下游 Bearer Key |
| POST | `/v1/audio/speech` | 下游 Bearer Key |

其他 `/v1/*` 返回 HTTP 501，不访问 NVIDIA。

### 5.2 管理路由

| Method | Path | 作用 |
|---|---|---|
| POST | `/admin/api/auth/login` | 登录；未改密时仍只创建受限会话 |
| GET | `/admin/api/auth/session` | 当前会话和 `must_change_password` |
| POST | `/admin/api/auth/change-password` | 强制/主动改密并撤销其他会话 |
| POST | `/admin/api/auth/logout` | 当前会话失效 |
| POST | `/admin/api/auth/revoke-all` | 全部会话失效 |
| GET/POST | `/admin/api/nvidia-keys` | 列表/单个导入 |
| POST | `/admin/api/nvidia-keys/batch` | 批量导入，逐行结果 |
| PATCH/DELETE | `/admin/api/nvidia-keys/{id}` | 启停/删除 |
| POST | `/admin/api/nvidia-keys/{id}/test` | 单 Key 手动测试 |
| POST | `/admin/api/nvidia-keys/test-all` | 顺序测试全部 Key |
| GET/POST | `/admin/api/access-keys` | 列表/创建 |
| DELETE | `/admin/api/access-keys/{id}` | 撤销 |
| GET | `/admin/api/models/candidates` | 首 Key 发现候选 |
| GET/POST | `/admin/api/models` | 白名单列表/保存选择 |
| PATCH | `/admin/api/models/{id}` | 启停和能力字段 |
| DELETE | `/admin/api/key-model-blocks/{keyID}/{modelID}` | 管理员清除 block 后手测 |
| GET | `/admin/api/runtime/summary` | 池、队列和状态摘要 |
| GET | `/admin/api/errors` | 最近安全错误元数据 |
| GET | `/admin/api/stats` | 基础统计 |
| GET/PATCH | `/admin/api/settings` | 队列、超时和关闭宽限设置 |

### 5.3 统一错误

```json
{
  "error": {
    "message": "安全的公开信息",
    "type": "invalid_request_error",
    "param": null,
    "code": "stable_machine_code"
  }
}
```

| 场景 | HTTP | code |
|---|---:|---|
| JSON/字段错误 | 400 | `invalid_request` |
| Base64/图片格式错误 | 400 | `invalid_image` |
| Body 超限 | 413 | `request_too_large` |
| 下游 Key 缺失/无效 | 401 | `invalid_api_key` |
| 模型不在白名单 | 404 | `model_not_found` |
| 所有 Key 对模型不可用 | 404 | `model_not_available` |
| 全部 Key 冷却 | 429 | `all_keys_cooling_down` |
| 队列满 | 429 | `queue_full` |
| 队列超时 | 429 | `queue_timeout` |
| 不支持能力或路径 | 501 | `not_implemented` |
| NVIDIA 协议异常 | 502 | `upstream_protocol_error` |
| 无启用/有效 Key | 503 | `no_available_keys` |
| 关闭中 | 503 | `server_shutting_down` |
| 上游连接/首字节超时耗尽 | 504 | `upstream_timeout` |

公共错误不得包含原始 NVIDIA body、URL、堆栈、Authorization、Cookie 或完整 Key。

---

# 阶段 0：工程、契约和构建骨架

## 任务 1：初始化仓库与 Go/Vue 工程

**文件：**
- 创建：`.gitignore`
- 创建：`go.mod`
- 创建：`cmd/nvidia-router/main.go`
- 创建：`internal/app/app.go`
- 创建：`internal/app/cli.go`
- 创建：`internal/app/app_test.go`
- 创建：`scripts/check-dev-env.sh`
- 创建：`pnpm-workspace.yaml`
- 创建：`web/package.json`
- 创建：`web/index.html`
- 创建：`web/tsconfig.json`
- 创建：`web/vite.config.ts`
- 创建：`web/uno.config.ts`
- 创建：`web/eslint.config.js`
- 创建：`web/src/main.ts`
- 创建：`web/src/App.vue`

- [ ] **步骤 1：若无 `.git`，初始化本地 Git 并创建开发分支**

```bash
git init
git switch -c feat/nvidia-router-mvp
```

若已有 Git，只创建分支，不执行第二次 `git init`。

- [ ] **步骤 2：执行开发环境预检**

`scripts/check-dev-env.sh` 检查 Git、Go 1.24+、pnpm、Node.js 20+；检查 `golangci-lint` 是否可用，缺失时给出固定版本安装命令但不静默安装。SQLite 选用 cgo-free 的 `github.com/ncruces/go-sqlite3/driver`，因此 Windows 本地无需 GCC；race test 在 Windows 和 Linux 均必须可运行。

运行：

```bash
bash scripts/check-dev-env.sh
```

预期：所有必需工具 PASS；缺失工具时脚本退出非 0 并给出准确安装步骤。

- [ ] **步骤 3：创建最小 Go 模块和失败测试**

`go.mod` 的 module 固定为 `nvidia-router`，Go 版本写 `1.24`。测试要求 `app.New` 存在且返回非空实例：

```go
func TestNew(t *testing.T) {
    app, err := New(context.Background(), Dependencies{})
    if err != nil { t.Fatalf("New: %v", err) }
    if app == nil { t.Fatal("expected app") }
}
```

运行：

```bash
go test ./internal/app
```

预期：FAIL，`undefined: New`。

- [ ] **步骤 4：创建最小 `App` 和命令入口**

`internal/app/app.go` 定义当前阶段不依赖未来包的最小 `Dependencies{}` 和 `New`；`internal/app/cli.go` 只支持 `--help`，未来任务逐步扩展。`main.go` 只调用 `app.RunCLI(os.Args[1:])`；禁止在 `main` 中创建 DB、路由或全局变量。完成后执行 `go test ./...` 和 `go build ./cmd/...`，必须通过。

- [ ] **步骤 5：创建 Vue strict 工程**

`package.json` 至少包含：`vue`、`vue-router`、`@vueuse/core`、`vite`、`typescript`、`unocss`、`vitest`、`@vue/test-utils`、`eslint`。脚本固定为：`dev`、`build`、`typecheck`、`test`、`lint`。

- [ ] **步骤 6：安装并锁定依赖**

```bash
go mod tidy
pnpm install
pnpm --dir web run typecheck
pnpm --dir web run build
go test ./...
go build ./cmd/...
```

预期：所有命令成功并生成 `pnpm-lock.yaml`。当前 Go 代码尚无外部依赖时允许没有 `go.sum`；后续首次加入外部依赖的任务必须提交生成的 `go.mod/go.sum`。

- [ ] **步骤 7：提交**

```bash
git add -A
git commit -m "chore: 初始化 NVIDIA 路由器工程"
```

## 任务 2：固定配置、请求限制和环境变量解析

**文件：**
- 创建：`internal/config/config.go`
- 创建：`internal/config/config_test.go`
- 创建：`internal/clock/clock.go`
- 创建：`internal/clock/clock_test.go`
- 创建：`.env.example`

- [ ] **步骤 1：编写表驱动失败测试**

覆盖：默认地址 `0.0.0.0:3756`、默认 `/data` 和 `/tmp`、默认 NVIDIA Base URL、受控测试 HTTP Base URL、生产非 HTTPS URL 拒绝、缺失主密钥、非法 Base64URL、解码非 32 字节、合法 32 字节。`internal/clock` 同时定义 `Clock{Now, NewTimer, AfterFunc}` 及生产实现，供登录限速、Pool、冷却、清理和测试统一注入，避免后续包前向依赖。

```go
func TestLoadFromEnvRejectsMissingMasterKey(t *testing.T)
func TestLoadFromEnvUsesPort3756AndNVIDIADefault(t *testing.T)
func TestLoadFromEnvRejectsInvalidRawURLBase64(t *testing.T)
func TestLoadFromEnvRejectsWrongMasterKeyLength(t *testing.T)
func TestLoadFromEnvAllowsHTTPOnlyWithTestOption(t *testing.T)
```

每个测试使用 `t.Setenv` 隔离变量；默认值测试断言 `ListenAddress == "0.0.0.0:3756"` 和 Base URL 精确等于 `https://integrate.api.nvidia.com`。

运行：`go test ./internal/config -run TestLoad -v`。
预期：FAIL，目标函数不存在。

- [ ] **步骤 2：实现稳定配置契约**

环境变量：

```text
NVIDIA_ROUTER_LISTEN_ADDR=0.0.0.0:3756
NVIDIA_ROUTER_DATA_DIR=/data
NVIDIA_ROUTER_TEMP_DIR=/tmp
NVIDIA_ROUTER_MASTER_KEY=<Raw URL Base64 32 bytes>
NVIDIA_ROUTER_NVIDIA_BASE_URL=https://integrate.api.nvidia.com
```

统一只使用 `NVIDIA_ROUTER_NVIDIA_BASE_URL`；它仅用于 Mock/测试和受控部署覆盖，不在管理页面提供任意上游配置。默认及生产值必须为 HTTPS；仅测试二进制通过显式 `AllowInsecureTestUpstream` 选项允许 `httptest.Server` 的 HTTP URL。

- [ ] **步骤 3：定义常量**

```go
const (
    JSONBodyLimit = 32 << 20
    ImageDecodedLimit = 20 << 20
    AudioBodyLimit = 25 << 20
)
```

- [ ] **步骤 4：验证**

```bash
go test ./internal/config ./internal/clock -v
golangci-lint run --fix ./...
```

- [ ] **步骤 5：提交**

```bash
git add internal/config internal/clock .env.example
git commit -m "feat: 定义运行配置和请求限制"
```

## 任务 3：建立统一 API 错误与 Fault 基础契约

**文件：**
- 创建：`internal/apierror/error.go`
- 创建：`internal/apierror/writer.go`
- 创建：`internal/apierror/writer_test.go`
- 创建：`internal/fault/fault.go`
- 创建：`internal/fault/fault_test.go`

- [ ] **步骤 1：编写错误 JSON 和泄漏失败测试**

断言字段固定为 `message/type/param/code`，并构造包含 `Authorization: Bearer nvapi-secret`、内部 URL 和堆栈的 cause，确认响应中均不存在。

- [ ] **步骤 2：实现 `apierror.Error`**

```go
type Error struct {
    Status int
    Type string
    Code string
    Message string
    Param *string
    RetryAfter time.Duration
    Cause error
}
```

`Write` 只序列化公开字段；内部 cause 只能交给结构化日志记录错误类型，不记录 `Cause.Error()` 原文。

- [ ] **步骤 3：实现 Retry-After**

`RetryAfter > 0` 时向上取整秒并设置 Header；禁止为非重试错误添加该 Header。

- [ ] **步骤 4：定义最低层 `fault.Fault` 契约**

按第 4 章稳定接口创建 `Scope` 和 `Fault`，仅定义类型、构造和 `Unwrap()`，不在此任务实现 NVIDIA 状态分类、Retry-After 或冷却算法。这样 Pool、NVIDIA Key 状态写入和 Attempt 包可按顺序编译。

- [ ] **步骤 5：验证并提交**

```bash
go test ./internal/apierror ./internal/fault -v
git add internal/apierror internal/fault
git commit -m "feat: 统一 OpenAI 风格错误响应"
```

---

# 阶段 1：SQLite、安全存储、管理员与 CLI

## 任务 4：SQLite、WAL 与版本化迁移

**文件：**
- 创建：`internal/database/db.go`
- 创建：`internal/database/migrate.go`
- 创建：`internal/database/db_test.go`
- 创建：`internal/database/migrate_test.go`
- 创建：`internal/database/migrations/001_initial.sql`
- 创建：`internal/database/migrations/002_indexes.sql`

- [ ] **步骤 1：写 WAL、外键和迁移失败测试**

使用 `t.TempDir()`；断言 `journal_mode=wal`、`foreign_keys=1`、默认 settings 行存在，且迁移只创建 schema，不依赖管理员密码服务。管理员默认行由任务 6 在应用启动事务中初始化。

- [ ] **步骤 2：写迁移 checksum 修改测试**

迁移首次成功后模拟相同版本不同 checksum，预期 `Migrate` 返回带上下文错误且不继续启动。

- [ ] **步骤 3：实现 DB 打开**

使用 `github.com/ncruces/go-sqlite3/driver` 的 `driver.Open` 打开 `file:<path>?_timefmt=rfc3339&_txlock=immediate`，并 `SetMaxOpenConns(1)`；该驱动 cgo-free，支持 Windows/Linux 和 Online Backup。逐项执行 PRAGMA 并读取结果验证。错误格式示例：`fmt.Errorf("enable SQLite WAL: %w", err)`。首次引入依赖后执行 `go mod tidy`，将 `go.mod/go.sum` 一并提交。

- [ ] **步骤 4：实现嵌入式迁移**

使用 `//go:embed migrations/*.sql`；按文件编号排序；每个迁移在事务中执行；记录 SHA-256 checksum。

- [ ] **步骤 5：实现初始设置数据**

迁移插入 `runtime_settings(id=1)` 默认行。迁移不得创建默认管理员，因为 Argon2id 服务在任务 6 才实现；任务 6 的启动初始化事务负责检测并创建 `admin/admin`。

- [ ] **步骤 6：运行测试**

```bash
go mod tidy
go test ./internal/database -v
go test -race ./internal/database
```

- [ ] **步骤 7：提交**

```bash
git add internal/database go.mod go.sum
git commit -m "feat: 添加 SQLite WAL 和版本化迁移"
```

## 任务 5：主密钥派生、AEAD、指纹与 sentinel

**文件：**
- 创建：`internal/crypto/master.go`
- 创建：`internal/crypto/aead.go`
- 创建：`internal/crypto/digest.go`
- 创建：`internal/crypto/sentinel.go`
- 创建：`internal/crypto/crypto_test.go`

- [ ] **步骤 1：写失败测试**

覆盖：AES-256-GCM round trip、随机 nonce 导致同明文密文不同、错误主密钥解密失败、AAD 不同失败、同 Key HMAC 指纹稳定、不同用途派生密钥不同、sentinel 首次创建和错误主密钥拒绝。

- [ ] **步骤 2：实现 HKDF 子密钥**

从 32 字节主密钥分别派生：

```text
nvidia-router/aead/v1
nvidia-router/fingerprint/v1
nvidia-router/access-key/v1
nvidia-router/session/v1
```

禁止同一子密钥跨用途。

- [ ] **步骤 3：实现 AES-256-GCM**

AAD 固定含记录类型和版本，例如 `nvidia-key:v1`；nonce 使用 `crypto/rand`；解密后返回新 `[]byte`。调用方在使用后执行 `crypto.Zero(secret)`，但文档明确 Go GC 无法保证物理内存擦除。

- [ ] **步骤 4：实现 sentinel**

首次数据库为空时加密固定随机 sentinel 并插入；已有 sentinel 时启动必须成功解密并比较常量时间；失败则拒绝启动，不覆盖记录。

- [ ] **步骤 5：验证**

```bash
go mod tidy
go test ./internal/crypto -v
go test -race ./internal/crypto
```

- [ ] **步骤 6：提交**

```bash
git add internal/crypto go.mod go.sum
git commit -m "feat: 加密 NVIDIA Key 并验证主密钥"
```

## 任务 6：管理员密码和初始化

**文件：**
- 创建：`internal/adminauth/password.go`
- 创建：`internal/adminauth/password_test.go`
- 创建：`internal/adminauth/repository.go`
- 创建：`internal/adminauth/repository_test.go`

- [ ] **步骤 1：写 Argon2id 测试**

参数固定：64 MiB memory、3 iterations、parallelism 2、salt 16 bytes、key 32 bytes；验证正确密码、错误密码、非法编码和常量时间比较。

- [ ] **步骤 2：实现 PHC 编码**

格式：`$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>`。禁止记录密码和 hash。

- [ ] **步骤 3：实现首次 admin 初始化**

数据库无 admin 时插入 `admin/admin` 的 Argon2id hash 和 `must_change_password=1`；已有 admin 不重置。

- [ ] **步骤 4：实现改密事务**

校验当前密码；生成新 hash；`must_change_password=0`；撤销除当前新会话外所有会话。新密码最少 12 字符，禁止等于 `admin`；该安全补充写入 UI 提示。

- [ ] **步骤 5：验证与提交**

```bash
go mod tidy
go test ./internal/adminauth -run 'Password|Admin' -v
git add internal/adminauth go.mod go.sum
git commit -m "feat: 添加管理员密码和强制改密"
```

## 任务 7：管理员会话、CSRF 与登录限速

**文件：**
- 创建：`internal/adminauth/session.go`
- 创建：`internal/adminauth/session_test.go`
- 创建：`internal/adminauth/ratelimit.go`
- 创建：`internal/adminauth/ratelimit_test.go`
- 创建：`internal/adminauth/origin.go`
- 创建：`internal/adminauth/origin_test.go`

- [ ] **步骤 1：写 opaque session 测试**

会话 token 使用 32 随机字节 Base64URL；数据库只存 HMAC digest；有效期固定 24 小时；登出、改密和 revoke-all 后不可用；过期不可用。

- [ ] **步骤 2：写 Cookie 测试**

名称固定 `nvr_admin_session`，`HttpOnly=true`、`SameSite=Strict`、`Path=/`、`MaxAge=86400`。第一轮 HTTP 下 `Secure=false`，测试和部署文档必须显式说明。

- [ ] **步骤 3：写登录限速测试**

使用注入式 Clock：每 IP 60 秒滑动窗口最多 5 次；连续失败延迟 1、2、4、8 秒，最大 8 秒；成功登录清零；内存记录 24 小时未使用后清理。

- [ ] **步骤 4：写 Origin Guard 测试**

所有 POST/PATCH/PUT/DELETE 管理请求必须有 `Origin` 且 scheme/host 与请求 Host 相同；登录同样校验；GET/HEAD 不要求 Origin。由于公网 HTTP，允许 `http://IP:3756`，拒绝跨源。

- [ ] **步骤 5：实现并验证**

```bash
go test ./internal/adminauth -run 'Session|Rate|Origin' -v
go test -race ./internal/adminauth
```

- [ ] **步骤 6：提交**

```bash
git add internal/adminauth
git commit -m "feat: 添加管理员会话和 Web 安全边界"
```

## 任务 8：下游 Access Key

**文件：**
- 创建：`internal/accesskey/model.go`
- 创建：`internal/accesskey/repository.go`
- 创建：`internal/accesskey/service.go`
- 创建：`internal/accesskey/middleware.go`
- 创建：`internal/accesskey/service_test.go`
- 创建：`internal/accesskey/middleware_test.go`

- [ ] **步骤 1：写创建和一次展示测试**

明文格式固定 `nvr_` + 43 字符 Raw URL Base64（32 随机字节）。创建响应包含一次明文；重新查询只返回 `key_prefix`，不得返回 digest 或明文。

- [ ] **步骤 2：写鉴权与撤销测试**

只接受 `Authorization: Bearer <key>`；digest 使用派生 HMAC；撤销立即失效；成功请求异步合并更新 `last_used_at`，最多每分钟写一次。

- [ ] **步骤 3：实现服务和 Middleware**

Context 只写入 `AccessKeyIdentity{ID, Prefix}`，不写明文。

- [ ] **步骤 4：数据库和日志泄漏扫描测试**

创建 Key 后读取 SQLite 文件字节和捕获日志，断言都不包含明文。

- [ ] **步骤 5：验证并提交**

```bash
go test ./internal/accesskey -v
go test -race ./internal/accesskey
git add internal/accesskey
git commit -m "feat: 添加不可恢复的下游访问 Key"
```

## 任务 9：CLI 密码重置和一致性备份

**文件：**
- 创建：`internal/database/backup.go`
- 创建：`internal/database/backup_test.go`
- 修改：`internal/app/cli.go`
- 创建：`internal/app/cli_test.go`

- [ ] **步骤 1：写 SQLite Backup API 测试**

在 WAL 在线写入后执行 backup；打开备份并校验 schema、admin 和 Key 记录完整；禁止通过 `os.ReadFile`/`os.WriteFile` 复制在线 DB 实现。

- [ ] **步骤 2：实现备份**

通过 `db.Conn(ctx).Raw` 将底层连接断言为别名导入的 `sqlite3driver.Conn`（`github.com/ncruces/go-sqlite3/driver`），调用 `conn.Raw().Backup("main", destinationURI)`；目标先写同目录临时文件，成功后原子 rename，权限 `0600`。不得换回依赖 CGO 的驱动。

- [ ] **步骤 3：写 CLI 测试**

命令固定：

```bash
nvidia-router admin reset-password --password '<new>'
nvidia-router db backup --output /backup/router.db
```

本任务只实现不需要 HTTP Server 的恢复命令；`serve` 子命令在任务 10 的 Server 完成后实现。重置密码撤销所有 session，不修改 NVIDIA Key ciphertext/nonce/fingerprint。

- [ ] **步骤 4：实现并验证**

```bash
go test ./internal/database ./internal/app -run 'Backup|CLI' -v
```

- [ ] **步骤 5：提交**

```bash
git add internal/database/backup* internal/app/cli*
git commit -m "feat: 添加管理员恢复和 SQLite 一致性备份"
```

## 任务 10：App 依赖容器、健康检查和启动门禁

**文件：**
- 修改：`internal/app/app.go`
- 修改：`internal/app/cli.go`
- 修改：`internal/app/cli_test.go`
- 创建：`internal/app/server.go`
- 创建：`internal/httpapi/router.go`
- 创建：`internal/httpapi/router_test.go`
- 创建：`internal/runtimeconfig/snapshot.go`
- 创建：`internal/runtimeconfig/store.go`
- 创建：`internal/runtimeconfig/repository.go`
- 创建：`internal/runtimeconfig/store_test.go`
- 创建：`internal/httpapi/health/health.go`
- 创建：`internal/httpapi/health/health_test.go`

- [ ] **步骤 1：定义显式 `Dependencies`**

```go
type Dependencies struct {
    Config config.Config
    DB *sql.DB
    Logger *slog.Logger
    Clock clock.Clock
    NVIDIAHTTPClient *http.Client
}
```

生产构造器补默认依赖；测试可全部注入。禁止包级 `DB`、Scheduler 和 Settings。

- [ ] **步骤 2：写 health 失败测试**

`/health/live` 只返回 `{"status":"ok"}`。`/health/ready` 在 DB 失败、迁移失败、sentinel 失败、`must_change_password=1`、关闭中时返回 503；不得返回 Key 数、模型或统计。

- [ ] **步骤 3：创建可增量注册的顶层 Router**

`httpapi.NewRouter` 当前只注册 health 和管理前端占位路由；后续任务每次修改该文件注册已存在的 `/v1` 与 `/admin/api` Handler。测试要求 API 路径永不落入 SPA。禁止等到任务 31 才首次创建顶层 Router。

- [ ] **步骤 4：实现共享 Runtime Settings Store**

从 `runtime_settings` 加载 `runtimeconfig.Snapshot` 到 `atomic.Value`；`Snapshot()` 返回值拷贝，保证每个请求开始时只读一次。`Store(ctx, next)` 在数据库事务成功后原子替换内存值；事务失败不得更新。后续 Pool 在 Acquire 开始读取 queue 设置、Attempt 读取 first-byte/total、NVIDIA Client 每次建立 Transport/Dial Context 读取 connect timeout、Shutdown 读取 grace。测试验证失败事务不更新、并发 Snapshot 无 race。

- [ ] **步骤 5：实现当前阶段可用的启动顺序**

顺序固定：配置 → DB/PRAGMA → migrations → sentinel → admin 初始化 →当前已存在的 repositories → runtime settings Store → handlers。Pool 等尚未实现的依赖不得在本任务引用；后续任务 15、18、20、33、39 注入同一个 `runtimeconfig.Provider`，禁止各包缓存独立配置。任一步失败都关闭已打开资源并返回带上下文错误。

- [ ] **步骤 6：实现生产 `serve` 子命令**

`RunCLI(["serve"])` 必须调用配置加载、DB/PRAGMA、迁移、sentinel、admin 初始化和 `Server.ListenAndServe`；支持 Context/SIGTERM；启动任一步失败时按逆序关闭资源。CLI 测试使用随机端口和可取消 Context，等待 `/health/live` 后取消，断言进程入口正常退出。

- [ ] **步骤 7：验证并提交**

```bash
go test ./internal/app ./internal/httpapi/... ./internal/runtimeconfig -v
go test -race ./internal/runtimeconfig -count=20
git add internal/app internal/httpapi internal/runtimeconfig
git commit -m "feat: 组装应用并添加启动就绪门禁"
```

---

# 阶段 2：NVIDIA Client、模型、Key 池、Chat 与 SSE

## 任务 11：唯一 NVIDIA Descriptor 和语义不变量

**文件：**
- 创建：`internal/upstream/nvidia/descriptor.go`
- 创建：`internal/upstream/nvidia/descriptor_test.go`

- [ ] **步骤 1：写 descriptor golden 测试**

固定默认端点：

```text
GET  https://integrate.api.nvidia.com/v1/models
POST https://integrate.api.nvidia.com/v1/chat/completions
POST https://integrate.api.nvidia.com/v1/embeddings
POST https://integrate.api.nvidia.com/v1/audio/transcriptions
POST https://integrate.api.nvidia.com/v1/audio/speech
```

Chat stream 额外 `Accept: text/event-stream`；所有请求使用 Bearer 和适当 Content-Type。

- [ ] **步骤 2：定义 capability hint**

内置 hint 仅作为管理端候选默认值，不直接启用模型。为 9Router 固定提交中确认的 MiniMax/GLM/DeepSeek NVIDIA 模型设置 `reasoning_wire_format=openai`；未知模型默认 `kind=chat`、所有能力 false。

- [ ] **步骤 3：实现 `Descriptor.Validate()`**

检查 URL scheme、每种公开 kind 的 endpoint、认证 scheme、capability wire format。测试必须能捕获“有 ASR 模型却没有 ASR endpoint/adapter”的语义断链。

- [ ] **步骤 4：提交**

```bash
go test ./internal/upstream/nvidia -run Descriptor -v
git add internal/upstream/nvidia/descriptor*
git commit -m "feat: 定义 NVIDIA 上游描述和能力不变量"
```

## 任务 12：NVIDIA `/models` Client 和验证三态

**文件：**
- 创建：`internal/upstream/nvidia/client.go`
- 创建：`internal/upstream/nvidia/models.go`
- 创建：`internal/upstream/nvidia/models_test.go`
- 创建：`tests/mocknvidia/server.go`

- [ ] **步骤 1：写模型响应形状测试**

支持：直接数组、`data`、`models`、`results`；模型 ID 去重并排序；空/非法 JSON 返回协议错误。

- [ ] **步骤 2：写验证状态测试**

```go
type ValidationState uint8
const (
    ValidationValid ValidationState = iota
    ValidationInvalidCredential
    ValidationTemporarilyUnavailable
    ValidationIndeterminate
)
```

200 为 Valid；401/403 为 InvalidCredential；429、500/502/503/504、网络和超时为 TemporarilyUnavailable；200 malformed 为 Indeterminate。只有 Valid 可入库。

- [ ] **步骤 3：实现安全错误读取**

最多读取 8 KiB 上游错误 body，只用于分类，不持久化原文；提取 `x-request-id` 等允许列表 Header；响应和日志不包含 body。

- [ ] **步骤 4：实现 Mock NVIDIA Server**

支持脚本化队列响应、请求计数、Header 捕获、SSE 延迟和取消检测，供后续所有测试复用。

- [ ] **步骤 5：验证并提交**

```bash
go test ./internal/upstream/nvidia ./tests/mocknvidia -v
git add internal/upstream/nvidia tests/mocknvidia
git commit -m "feat: 添加 NVIDIA 模型发现和 Key 验证"
```

## 任务 13：NVIDIA Key Repository、导入和短暂解密

**文件：**
- 创建：`internal/keystate/state.go`
- 创建：`internal/keystate/state_test.go`
- 创建：`internal/nvidiakey/model.go`
- 创建：`internal/nvidiakey/repository.go`
- 创建：`internal/nvidiakey/service.go`
- 创建：`internal/nvidiakey/validator.go`
- 创建：`internal/nvidiakey/service_test.go`

- [ ] **步骤 1：写格式和去重测试**

格式校验只允许可打印、无空白、长度 20～512 的 Bearer token；不硬编码 `nvapi-` 前缀。相同 Key 因 HMAC fingerprint 唯一索引只保存一次。

- [ ] **步骤 2：写批量部分成功测试**

输入逐行 trim、忽略空行但保留原始行号；返回 `line/status/reason/masked`；Valid 入库，Invalid/Temporary/Indeterminate 均不入库；结果和日志不含原 Key。

- [ ] **步骤 3：实现 `WithSecret`**

```go
func (s *Service) WithSecret(ctx context.Context, id int64, fn func([]byte) error) error
```

按需解密，`defer crypto.Zero(secret)`；禁止提供 `GetPlaintext` 或返回长期 secret 的 Repository 方法。

- [ ] **步骤 4：写 DB 文件泄漏测试**

添加真实格式假 Key 后读取 DB、WAL 和 captured logs，均不得包含明文。

- [ ] **步骤 5：验证并提交**

```bash
go test ./internal/nvidiakey -v
go test -race ./internal/nvidiakey
git add internal/keystate internal/nvidiakey
git commit -m "feat: 安全导入和管理 NVIDIA Key"
```

## 任务 14：模型候选、白名单与能力门禁

**文件：**
- 创建：`internal/modelcatalog/model.go`
- 创建：`internal/modelcatalog/repository.go`
- 创建：`internal/modelcatalog/capabilities.go`
- 创建：`internal/modelcatalog/service.go`
- 创建：`internal/modelcatalog/service_test.go`

- [ ] **步骤 1：写首 Key 候选导入测试**

候选读取 `/models` 但不自动入白名单；管理员提交选择后创建 `models` 行；新增 Key 不改变已有行。

- [ ] **步骤 2：写白名单测试**

只返回 `enabled=1`；public ID 可与 upstream ID 不同；请求 public ID 映射到 upstream ID；禁用后立即不可调用。

- [ ] **步骤 3：写 capability 门禁测试**

Chat 图片要求 `supports_vision=1`；tools 要求 `supports_tools=1`；reasoning 要求 `supports_reasoning=1`。Embedding/ASR/TTS kind 必须与路由一致。ASR/TTS 只有 `capability_verified_at` 非空才允许 `enabled=1`。

- [ ] **步骤 4：实现 block 关系**

`BlockKeyModel` upsert；`UnblockKeyModel` 只在管理员手动测试成功后执行；删除模型或 Key 级联清理。

- [ ] **步骤 5：验证并提交**

```bash
go test ./internal/modelcatalog -v
git add internal/modelcatalog
git commit -m "feat: 添加稳定模型白名单和能力门禁"
```

## 任务 15：Key Pool、Lease 和 Round Robin

**文件：**
- 创建：`internal/pool/state.go`
- 创建：`internal/pool/lease.go`
- 创建：`internal/pool/pool.go`
- 创建：`internal/pool/pool_test.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写确定性 Round Robin 测试**

使用 fake clock 和固定 Key ID `[1,2,3]`；本任务先实现不排队的内部 `tryAcquire(modelID, attempted)`，每次成功分配立刻推进游标；Release 后序列保持 1→2→3→1；重启新 Pool 从第一个可用 Key 开始。唯一公开的阻塞式 `Acquire(ctx, modelID, attempted)` 在任务 16 结合 FIFO 实现。

- [ ] **步骤 2：写持久状态加载与过滤测试**

Pool 构造器接收同一个 `runtimeconfig.Provider`。实现稳定接口 `LoadSnapshot/UpsertKey/RemoveKey/ApplySuccess/ApplyFailure/SetModelBlock`。App 启动从 Repository 一次读取所有 Key 和 block 后调用 `LoadSnapshot`；Pool 跳过 disabled、auth_invalid、cooldown、busy、attempted 和 key-model block。每 Key 同时最多一个 Lease。测试断言 DB snapshot 加载后选择结果正确，管理操作同步后下一次 Acquire 立即变化。

- [ ] **步骤 3：实现 Lease 生命周期**

Lease `Release` 幂等；成功、失败、panic 和 context cancel 都必须释放。Lease 只管理内存 busy 状态；成功/失败持久化由 Attempt 调用 `KeyStateWriter`，避免 Pool 依赖数据库或 Fault 分类实现。使用 mutex 管理 busy，不把 secret 存在 state。

- [ ] **步骤 4：运行 race test**

启动 100 goroutine 对 10 Key Acquire/Release，断言每 Key 最大活跃数始终为 1。

```bash
go test -race ./internal/pool -run 'RoundRobin|Concurrency' -count=20
```

- [ ] **步骤 5：提交**

```bash
git add internal/pool internal/app/app.go
git commit -m "feat: 实现单并发限流感知 Key Pool"
```

## 任务 16：全局 FIFO 队列与无候选原因

**文件：**
- 创建：`internal/pool/queue.go`
- 创建：`internal/pool/queue_test.go`
- 修改：`internal/pool/pool.go`
- 修改：`internal/pool/pool_test.go`

- [ ] **步骤 1：写 FIFO、取消和容量测试**

为 Pool 实现唯一公开入口 `Acquire(ctx, modelID, attempted)`：先从共享 `runtimeconfig.Provider.Snapshot()` 读取 queue capacity/wait，再调用内部 `tryAcquire`；全部候选仅因 busy 不可用时排队；释放 Lease 唤醒队首；被取消 waiter 立即移除且不阻塞下一项；默认容量 100；等待 60 秒；队列满和超时返回不同 code。测试必须从公开 `Acquire` 进入，而不是直接测试 Queue。

- [ ] **步骤 2：写模型感知 head-of-line 测试**

严格全局 FIFO：后入请求不得绕过仍可能获得 Key 的队首。若队首已变为全模型 block/全停用，立即以分类错误完成，然后调度下一项。

- [ ] **步骤 3：实现 `UnavailableReason`**

```go
type UnavailableReason uint8
const (
    UnavailableBusy UnavailableReason = iota
    UnavailableCooling
    UnavailableDisabled
    UnavailableModelBlocked
)
```

只对 Busy 入队；Cooling 429；Disabled 503；ModelBlocked 404。

- [ ] **步骤 4：实现并测试 `Pool.Shutdown()`**

在 Pool 同一 mutex 下设置 closed、拒绝新 Acquire、移出所有 waiter，并以 `server_shutting_down` 完成等待；幂等且不可重新打开。任务 39 只调用此稳定原语。测试创建已占用唯一 Key 和一个排队 Acquire，调用 Shutdown 后 waiter 立即返回关闭错误，后续 Acquire 同样立即失败。

- [ ] **步骤 5：验证和提交**

```bash
go test -race ./internal/pool -run 'Queue|Acquire|Shutdown' -count=20
git add internal/pool/queue* internal/pool/pool*
git commit -m "feat: 添加可取消的全局 FIFO 队列"
```

## 任务 17：Fault 分类、Retry-After 与冷却状态

**文件：**
- 修改：`internal/fault/fault.go`
- 创建：`internal/fault/classifier.go`
- 创建：`internal/fault/retry_after.go`
- 创建：`internal/fault/cooldown.go`
- 创建：`internal/fault/classifier_test.go`
- 创建：`internal/fault/cooldown_test.go`
- 修改：`internal/nvidiakey/repository.go`
- 修改：`internal/nvidiakey/service.go`
- 修改：`internal/nvidiakey/service_test.go`

- [ ] **步骤 1：写分类表测试**

覆盖：请求 400/404/409/422 不重试；401 disable Key；`/models` 403 disable Key；模型请求 403 默认 block model；明确 `invalid_api_key/authentication_error/account_deactivated` disable Key；429 retry；500/502/503/504 retry；其他 5xx 不 retry；连接/reset/timeout retry。

- [ ] **步骤 2：写 Retry-After 测试**

支持整数秒和 HTTP-date；过去时间视为 0；异常值回退指数退避。

- [ ] **步骤 3：写冷却测试**

429 等级 0→5 秒、1→10 秒、2→20 秒，最大 5 分钟，jitter 使用可注入随机源；网络/5xx 15 秒；成功后 level 和 consecutive failures 清零。

- [ ] **步骤 4：实现安全上游错误摘要**

只解析 JSON 中允许的 `error.code`/`type`，不持久化 `message` 原文；未知错误采取保守规则，不根据任意字符串猜测整 Key 失效。

- [ ] **步骤 5：实现 Key 状态事务写入**

在 `nvidiakey.Service` 实现稳定 `KeyStateWriter`：401 原子设置 `auth_invalid`；429/网络/可恢复 5xx 原子更新 cooldown、level、consecutive failures；模型 403 原子 upsert block；成功原子清零 cooldown reason/until、level、consecutive failures 并更新时间。每个方法提交事务后返回最新 `keystate.KeySnapshot`。事务失败必须返回错误且不得伪造 snapshot。测试关闭并重新打开 DB 后状态仍存在。

- [ ] **步骤 6：验证并提交**

```bash
go test ./internal/fault ./internal/nvidiakey -run 'Classifier|Cooldown|MarkSuccess|MarkFailure' -v
git add internal/fault internal/nvidiakey/repository.go internal/nvidiakey/service.go internal/nvidiakey/service_test.go
git commit -m "feat: 分类上游故障并管理 Key 冷却"
```

## 任务 18：统一 Attempt Orchestrator 和预算

**文件：**
- 创建：`internal/router/budget.go`
- 创建：`internal/router/commit.go`
- 创建：`internal/router/attempt.go`
- 创建：`internal/router/attempt_test.go`

- [ ] **步骤 1：写每 Key 一次和统一预算测试**

三个 Key 前两个失败、第三个成功；每个只请求一次；总 deadline 不因换 Key 延长；Transport 禁止自动重试。

- [ ] **步骤 2：写 committed 测试**

首次 `WriteHeader`、`Write` 或 `Flush` 后 `CommitState=true`；此后任何错误均不调用第二 Key。

- [ ] **步骤 3：实现分阶段预算**

Attempt 构造器接收同一个 `runtimeconfig.Provider`。每次客户端请求开始调用一次 `Snapshot()` 并将其写入 request-scoped Budget：连接由该 snapshot 的 Dial timeout 控制；每 attempt 使用 snapshot 的 first-byte timeout；非流式使用 snapshot 的 total deadline；流式在首字节后只受客户端 context 和关闭 context。一个请求中设置变化不得改变已捕获 snapshot。

- [ ] **步骤 4：实现 retry loop**

维护 `map[int64]struct{}` attempted；Acquire → `SecretProvider.WithSecret` 内 execute → classify → `KeyStateWriter.MarkSuccess/MarkFailure` 事务返回最新持久化 `keystate.KeySnapshot` → `StateSync.ApplySuccess/ApplyFailure` 更新 Pool → Release。数据库事务必须先成功，随后持有 Pool mutex 执行无失败分支的纯内存更新；进程若在两步之间崩溃，重启时以数据库 snapshot 恢复，不得先更新内存再提交数据库。若 fault 不可 retry、committed 或预算耗尽则返回；所有候选耗尽时选择最后最有意义的公共错误。本任务使用 fake ExecuteFunc 连续执行两次 Attempt，断言第一次触发 401、429、模型 403 后，第二次立即跳过对应 Key；真实 App HTTP 连续请求测试在任务 20 接入 Chat Handler 后补充。

- [ ] **步骤 5：验证并提交**

```bash
go test -race ./internal/router -run Attempt -count=20
git add internal/router/budget.go internal/router/commit.go internal/router/attempt*
git commit -m "feat: 实现首字节前多 Key 故障转移"
```

## 任务 19：Chat 请求校验、未知字段透传和图片

**文件：**
- 创建：`internal/protocol/chat/request.go`
- 创建：`internal/protocol/chat/validate.go`
- 创建：`internal/protocol/chat/rules.go`
- 创建：`internal/protocol/chat/image.go`
- 创建：`internal/protocol/chat/request_test.go`
- 创建：`internal/protocol/chat/image_test.go`

- [ ] **步骤 1：写 RawMessage 保留测试**

解码到 `map[string]json.RawMessage`；修正规则执行后未知合法字段字节语义保留；明确拒绝项优先于透传。

- [ ] **步骤 2：写字段校验测试**

要求 model、messages；校验 role、tools、tool_choice、stream 类型；白名单外/能力不支持在接触 NVIDIA 前失败。

- [ ] **步骤 3：写 reasoning 修正测试**

`reasoning_wire_format=openai` 时移除 native `thinking` 并写 `reasoning_effort`；不支持 reasoning 的模型收到 reasoning 字段则返回 400，不静默忽略。

- [ ] **步骤 4：写图片测试**

允许 HTTPS URL；拒绝 HTTP/file/ftp。允许 `data:image/png|jpeg|webp|gif;base64,`；严格 Base64 解码；单图解码最大 20 MiB；总 JSON 32 MiB；非 vision 模型拒绝；路由器不得请求远程 URL。

- [ ] **步骤 5：实现规则链**

```text
RejectUnsupported → ValidateRequired → ValidateModelCapabilities → NormalizeReasoning → PreserveUnknown
```

- [ ] **步骤 6：验证并提交**

```bash
go test ./internal/protocol/chat -v
git add internal/protocol/chat
git commit -m "feat: 校验并修正 Chat 请求"
```

## 任务 20：NVIDIA Chat Client 和非流式接口

**文件：**
- 创建：`internal/upstream/nvidia/chat.go`
- 创建：`internal/upstream/nvidia/chat_test.go`
- 创建：`internal/httpapi/v1/chat.go`
- 创建：`internal/httpapi/v1/chat_test.go`
- 创建：`internal/httpapi/middleware.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`
- 修改：`internal/app/server.go`

- [ ] **步骤 1：写 URL/Header/Body golden 测试**

断言 Bearer、JSON Content-Type、model 映射、stream 保留、reasoning 修正、tools 和未知字段。NVIDIA Client 接收 request-scoped Runtime Snapshot，并为每个 attempt 创建带该 ConnectTimeout 的 `net.Dialer`/Transport；禁止共享会随设置变化而突变的连接超时全局变量。错误输出不含上游 URL 或 Key。

- [ ] **步骤 2：写 Handler 主路径测试**

下游 Access Key 鉴权、32 MiB limit、模型白名单、Attempt 调度、非流式 2xx body 结构化透传。上游非 JSON 2xx 返回 502 协议错误并可在未 committed 时换 Key。

- [ ] **步骤 3：实现非流式响应校验**

最多 32 MiB 读取上游 JSON；验证对象且包含 `choices`；保留未知字段；提取 usage 和上游 request ID 到元数据，不记录正文。

- [ ] **步骤 4：接入实际 Server 并验证**

修改顶层 Router 注册 `/v1/chat/completions`，修改 App/Server 注入 AccessKey、ModelCatalog、Attempt 和 NVIDIA Client。集成测试必须从 `httptest.NewServer(app.Handler())` 发起真实 HTTP 请求，不能只直接调用 Handler；连续请求分别让 Mock NVIDIA 返回 401、429、模型 403，断言下一请求立即跳过刚失效、冷却或 block 的 Key。

```bash
go test ./internal/upstream/nvidia ./internal/httpapi/v1 ./internal/app -run Chat -v
```

- [ ] **步骤 5：提交**

```bash
git add internal/upstream/nvidia/chat* internal/httpapi/v1/chat* internal/httpapi/middleware.go internal/httpapi/router.go internal/app/app.go internal/app/server.go
git commit -m "feat: 添加非流式 Chat Completions 代理"
```

## 任务 21：SSE Event Parser 和 Chat 流代理

**文件：**
- 创建：`internal/sse/event.go`
- 创建：`internal/sse/decoder.go`
- 创建：`internal/sse/encoder.go`
- 创建：`internal/sse/proxy.go`
- 创建：`internal/sse/decoder_test.go`
- 创建：`internal/sse/proxy_test.go`
- 创建：`internal/sse/fuzz_test.go`
- 修改：`internal/upstream/nvidia/chat.go`
- 修改：`internal/upstream/nvidia/chat_test.go`
- 修改：`internal/httpapi/v1/chat.go`
- 修改：`internal/httpapi/v1/chat_test.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写 event parser 测试**

覆盖 `\n`/`\r\n`、comment、event/id/retry、多 data 行、UTF-8 跨 chunk、大于 64 KiB tool arguments、空行分隔、EOF 未完成行。禁止使用默认 64 KiB `bufio.Scanner`。

- [ ] **步骤 2：写 Chat passthrough 测试**

保留 comment 和非 JSON `data:`；重复 `[DONE]` 只输出一次；收到 `[DONE]` 后忽略其他终止；EOF 前未收到 `[DONE]` 视为中途断流。

- [ ] **步骤 3：写 first-byte failover 测试**

上游 200 但 60 秒内无首个完整事件：未提交下游，可换 Key。首事件写出后上游断流：不能换 Key，只结束连接并记录 `upstream_stream_interrupted`。

- [ ] **步骤 4：实现 cancel 传播**

读取和写入均 select 客户端 context；断开后立刻 `response.Body.Close()`，释放 Lease；不使用 9Router 的 500ms 延迟。

- [ ] **步骤 5：Fuzz**

Fuzz 任意分块，要求不 panic、不无限增长；单 Event 上限 4 MiB，超过后返回协议错误。

- [ ] **步骤 6：把 SSE 接入 Chat HTTP 路径**

`chat.go` 根据 `stream:true` 调用 SSE Proxy，并通过 App 级集成测试验证真实 `/v1/chat/completions` 的事件、Flush、取消、Lease 释放和 committed 后不重试。禁止只完成 `internal/sse` 包而不修改 Handler。

- [ ] **步骤 7：验证并提交**

```bash
go test ./internal/sse ./internal/upstream/nvidia ./internal/httpapi/v1 ./internal/app -run 'SSE|Stream|Chat' -v
go test -race ./internal/sse ./internal/httpapi/v1 ./internal/app -count=10
go test ./internal/sse -fuzz=FuzzDecoder -fuzztime=10s
git add internal/sse internal/upstream/nvidia/chat* internal/httpapi/v1/chat* internal/app/app.go
git commit -m "feat: 添加健壮 SSE 解析和 Chat 流代理"
```

## 任务 22：`/v1/models` 和不支持路由

**文件：**
- 创建：`internal/httpapi/v1/models.go`
- 创建：`internal/httpapi/v1/models_test.go`
- 创建：`internal/httpapi/v1/unsupported.go`
- 创建：`internal/httpapi/v1/unsupported_test.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写模型列表测试**

仅 enabled 白名单；OpenAI 形状 `object=list`、每项 `object=model`；新增 Key 不改变结果；禁用立即消失。

- [ ] **步骤 2：写未知 `/v1/*` 测试**

返回 501 `not_implemented`；Mock NVIDIA 请求计数保持 0；错误无内部信息。

- [ ] **步骤 3：注册路由并验证**

修改顶层 Router 注册 `/v1/models` 和最终 `/v1/*` 兜底；兜底顺序必须在所有已知路由之后。App 集成测试从真实 Server 调用并断言未知路径没有命中 Mock NVIDIA。

```bash
go test ./internal/httpapi/v1 ./internal/app -run 'Models|Unsupported' -v
```

- [ ] **步骤 4：提交**

```bash
git add internal/httpapi/v1/models* internal/httpapi/v1/unsupported* internal/httpapi/router.go internal/app/app.go
git commit -m "feat: 发布稳定模型白名单并拒绝未知接口"
```

---

# 阶段 3：Responses、Embeddings、Audio 和完整协议矩阵

## 任务 23：Responses 请求校验和 Responses→Chat

**文件：**
- 创建：`internal/protocol/responses/types.go`
- 创建：`internal/protocol/responses/validate.go`
- 创建：`internal/protocol/responses/request.go`
- 创建：`internal/protocol/responses/request_test.go`

- [ ] **步骤 1：写明确拒绝测试**

`store:true`、`previous_response_id`、background、文件、图片、OpenAI hosted tools、远程 MCP、状态恢复字段均返回 400 `unsupported_responses_feature`，不得透传。

- [ ] **步骤 2：写文本和多轮转换 golden**

`instructions` 置于 system；字符串 input 转 user；多轮 input 保序；function_call 转 assistant tool_calls；function_call_output 转 tool message；tools/tool_choice/reasoning/max_output_tokens 正确映射。

- [ ] **步骤 3：实现纯函数**

```go
func ToChat(body []byte, model modelcatalog.Model) ([]byte, error)
```

不得 import DB、Pool、HTTP Handler。未知 Responses 字段仅在不改变状态语义时透传到内部中间结构，不能直接透传给 Chat。

- [ ] **步骤 4：验证并提交**

```bash
go test ./internal/protocol/responses -run Request -v
git add internal/protocol/responses
git commit -m "feat: 转换 Responses 请求为 Chat"
```

## 任务 24：非流式 Chat→Responses

**文件：**
- 创建：`internal/protocol/responses/nonstream.go`
- 创建：`internal/protocol/responses/nonstream_test.go`
- 创建：`internal/httpapi/v1/responses.go`
- 创建：`internal/httpapi/v1/responses_test.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写文本、reasoning、工具和 usage golden**

输出含 `id/object/status/model/output/usage`；文本为 message output item；function tool 为 function_call item；多个 tool calls 保序；usage 映射 input/output/total tokens。

- [ ] **步骤 2：写 Handler 调度测试**

Responses 复用同一 ModelCatalog、Pool、Attempt 和 NVIDIA Chat Client；不得建立独立 Key 调度路径。

- [ ] **步骤 3：实现稳定 ID**

使用 crypto-random request-scoped ID，格式 `resp_...`；非流式转换不保存 response。

- [ ] **步骤 4：验证并提交**

```bash
go test ./internal/protocol/responses ./internal/httpapi/v1 ./internal/app -run Responses -v
git add internal/protocol/responses/nonstream* internal/httpapi/v1/responses* internal/httpapi/router.go internal/app/app.go
git commit -m "feat: 添加非流式 Responses 转换"
```

## 任务 25：Responses SSE 状态机

**文件：**
- 创建：`internal/protocol/responses/state.go`
- 创建：`internal/protocol/responses/stream.go`
- 创建：`internal/protocol/responses/stream_test.go`
- 修改：`internal/httpapi/v1/responses.go`
- 修改：`internal/httpapi/v1/responses_test.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写精确文本事件序列**

```text
response.created
response.in_progress
response.output_item.added
response.content_part.added
response.output_text.delta
response.output_text.done
response.content_part.done
response.output_item.done
response.completed
[DONE]
```

每个事件有单调递增 sequence number；terminal 只出现一次。

- [ ] **步骤 2：写工具事件序列**

覆盖两个并行 tool calls、arguments 多 chunk、空 arguments、ID 保留、arguments done、item done；不得要求每个 delta 都是完整 JSON。

- [ ] **步骤 3：写 reasoning 和断流测试**

reasoning delta 映射到 summary；上游在 terminal 前 EOF 时，如果已 committed，生成 `response.failed` 后单一 `[DONE]`；如果未 committed，交给 Attempt 换 Key。

- [ ] **步骤 4：实现每请求状态**

State 只存在请求生命周期，不持久化；不得记录 text、reasoning 或 tool arguments。

- [ ] **步骤 5：接入真实 Responses HTTP 路径**

修改 `responses.go`，在 `stream:true` 时通过本状态机消费 NVIDIA Chat SSE；App 集成测试从 `/v1/responses` 断言完整事件、Flush、取消、失败 terminal 和 Lease 释放。禁止只验证纯转换包。

- [ ] **步骤 6：验证并提交**

```bash
go test ./internal/protocol/responses ./internal/httpapi/v1 ./internal/app -run 'Responses|Stream' -v
go test -race ./internal/protocol/responses ./internal/httpapi/v1 ./internal/app -run Stream -count=10
git add internal/protocol/responses/state.go internal/protocol/responses/stream* internal/httpapi/v1/responses* internal/app/app.go
git commit -m "feat: 添加 Responses SSE 转换状态机"
```

## 任务 26：Embeddings

**文件：**
- 创建：`internal/protocol/embeddings/validate.go`
- 创建：`internal/protocol/embeddings/validate_test.go`
- 创建：`internal/upstream/nvidia/embeddings.go`
- 创建：`internal/upstream/nvidia/embeddings_test.go`
- 创建：`internal/httpapi/v1/embeddings.go`
- 创建：`internal/httpapi/v1/embeddings_test.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写输入和模型 kind 测试**

支持 string 和 string array input；拒绝空 input；只允许 kind=embedding 且 enabled；未知字段透传；输入正文不得进入日志。

- [ ] **步骤 2：写 NVIDIA wire golden**

Bearer、`/v1/embeddings`、model 映射、`encoding_format`/`dimensions` 合法透传；响应验证 `data` 数组和 usage。

- [ ] **步骤 3：复用 Attempt**

429/5xx/连接错误按统一规则换 Key；每 Key 一次；非流式 5 分钟总预算。

- [ ] **步骤 4：验证并提交**

```bash
go test ./internal/protocol/embeddings ./internal/upstream/nvidia ./internal/httpapi/v1 ./internal/app -run Embedding -v
git add internal/protocol/embeddings internal/upstream/nvidia/embeddings* internal/httpapi/v1/embeddings* internal/httpapi/router.go internal/app/app.go
git commit -m "feat: 添加 NVIDIA Embeddings 代理"
```

## 任务 27：可重放请求体和安全临时文件

**文件：**
- 创建：`internal/router/replay.go`
- 创建：`internal/router/replay_test.go`

- [ ] **步骤 1：写内存/文件阈值测试**

小于 1 MiB 保存在内存；1～25 MiB 写到 `Config.TempDir` 临时文件；权限 `0600`；每次 `Open()` 从头读；超过 25 MiB 返回 413。

- [ ] **步骤 2：写清理测试**

成功、上游失败、客户端取消、context timeout 和 panic recovery 后都删除文件；测试用 `t.TempDir()` 断言目录为空。

- [ ] **步骤 3：实现生命周期**

```go
type ReplayableBody interface {
    Open() (io.ReadCloser, error)
    Size() int64
    Close() error
}
```

`Close` 幂等。禁止把内容写入日志或数据库。

- [ ] **步骤 4：验证并提交**

```bash
go test -race ./internal/router -run Replay -count=10
git add internal/router/replay*
git commit -m "feat: 添加可重放且自动清理的请求体"
```

## 任务 28：Audio Transcriptions 能力门禁

**文件：**
- 创建：`internal/protocol/audio/transcriptions.go`
- 创建：`internal/protocol/audio/transcriptions_test.go`
- 创建：`internal/upstream/nvidia/audio.go`
- 创建：`internal/upstream/nvidia/audio_test.go`
- 创建：`internal/httpapi/v1/audio.go`
- 创建：`internal/httpapi/v1/audio_test.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写 multipart 验证测试**

要求 file/model；只允许 enabled、kind=asr、`capability_verified_at` 非空；Audio 总体最大 25 MiB；保留 NVIDIA 可映射字段；不保存音频/转录正文。

- [ ] **步骤 2：写可重放和 failover 测试**

第一个 Key 在首字节前 503，第二个成功；每次 multipart 内容一致；临时文件最终删除。上游已返回首字节后断流不换 Key。

- [ ] **步骤 3：实现 adapter**

默认 `POST /v1/audio/transcriptions`，Bearer，multipart。响应 JSON 至少验证 `text` 或 `transcript`，再归一化 OpenAI shape。未经真实联调验证的模型无法启用，因此 endpoint 假设不会被伪装成已验证支持。

- [ ] **步骤 4：验证并提交**

```bash
go test ./internal/protocol/audio ./internal/upstream/nvidia ./internal/httpapi/v1 ./internal/app -run Transcription -v
git add internal/protocol/audio/transcriptions* internal/upstream/nvidia/audio* internal/httpapi/v1/audio* internal/httpapi/router.go internal/app/app.go
git commit -m "feat: 添加受能力门禁的 NVIDIA ASR 代理"
```

## 任务 29：Audio Speech 流式二进制代理

**文件：**
- 创建：`internal/protocol/audio/speech.go`
- 创建：`internal/protocol/audio/speech_test.go`
- 修改：`internal/upstream/nvidia/audio.go`
- 修改：`internal/upstream/nvidia/audio_test.go`
- 修改：`internal/httpapi/v1/audio.go`
- 修改：`internal/httpapi/v1/audio_test.go`

- [ ] **步骤 1：写输入测试**

要求 input/model；支持 voice 和 response_format；只允许 enabled、kind=tts、已验证模型；JSON 总体 32 MiB；不记录 input。

- [ ] **步骤 2：写首块提交测试**

上游 200 后先读取首块二进制，成功后才提交下游 Header；首块前错误可换 Key；首块写出后中断不可换 Key。

- [ ] **步骤 3：实现安全 Content-Type**

只透传允许的 `audio/wav`、`audio/mpeg`、`audio/ogg`、`audio/flac`、`application/octet-stream`；未知类型改为 `application/octet-stream`。直接 `io.CopyBuffer`，不做 9Router 式 base64 全量中转。

- [ ] **步骤 4：验证并提交**

```bash
go test ./internal/protocol/audio ./internal/upstream/nvidia ./internal/httpapi/v1 -run Speech -v
git add internal/protocol/audio internal/upstream/nvidia/audio* internal/httpapi/v1/audio*
git commit -m "feat: 添加受能力门禁的 NVIDIA TTS 代理"
```

## 任务 30：Mock NVIDIA 完整错误和流式矩阵

**文件：**
- 创建：`tests/mocknvidia/integration_test.go`
- 创建：`tests/mocknvidia/leak_test.go`

- [ ] **步骤 1：实现表驱动矩阵**

必须覆盖需求中的 401、403、429/Retry-After、500/502/503/504、连接失败、响应头超时、首字节超时、非流式总超时、取消、冷却恢复、遍历全部 Key、单并发、Key-模型 block、全部忙、队列满/超时、SSE 非 JSON、重复 `[DONE]`、中途断流、committed 后不重试、Responses 事件、tool arguments、`store:true`、未知接口。

- [ ] **步骤 2：添加新增边界**

全冷却 429、全停用 503、全模型 block 404、未知字段透传、图片 URL/Base64 和 Audio 临时文件清理。关闭中 503 由任务 39 在关闭功能实现后覆盖。

- [ ] **步骤 3：泄漏扫描**

捕获 HTTP response、slog、SQLite/WAL 和备份，搜索完整 NVIDIA Key、Access Key、Authorization、Cookie 和 fixture 正文；任何匹配失败。Docker build context 和镜像泄漏扫描在任务 40 创建容器文件后执行。

- [ ] **步骤 4：验证并提交**

```bash
go test -race ./tests/mocknvidia -count=5
git add tests/mocknvidia
git commit -m "test: 覆盖 NVIDIA 路由错误和泄漏矩阵"
```

---

# 阶段 4：管理 API、Vue 运维面板、统计和交付

## 任务 31：管理认证 API 和强制改密门禁

**文件：**
- 创建：`internal/httpapi/admin/auth.go`
- 创建：`internal/httpapi/admin/auth_test.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`
- 修改：`internal/app/server.go`

- [ ] **步骤 1：写登录流程 E2E 测试**

`admin/admin` 登录获得受限 Session；`/v1/*` 和 Key 管理返回 `password_change_required`；改成 12+ 字符非 admin 密码后旧会话撤销、新会话可用；logout/revoke-all 生效。

- [ ] **步骤 2：写限速和 Origin 测试**

第 6 次/分钟返回 429；跨源写请求 403；同源成功；Cookie 属性符合任务 7。

- [ ] **步骤 3：实现 Router 组装**

管理 GET 需 session；写操作需 session + Origin；改密前只允许 session、change-password、logout。

- [ ] **步骤 4：验证并提交**

```bash
go test ./internal/httpapi/admin ./internal/app -run Auth -v
git add internal/httpapi/admin/auth* internal/httpapi/router.go internal/app/app.go internal/app/server.go
git commit -m "feat: 添加管理登录和强制改密 API"
```

## 任务 32：NVIDIA Key、Access Key 和模型管理 API

**文件：**
- 创建：`internal/httpapi/admin/nvidia_keys.go`
- 创建：`internal/httpapi/admin/nvidia_keys_test.go`
- 创建：`internal/httpapi/admin/access_keys.go`
- 创建：`internal/httpapi/admin/access_keys_test.go`
- 创建：`internal/httpapi/admin/models.go`
- 创建：`internal/httpapi/admin/models_test.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写秘密字段负向测试**

任何列表、详情、错误和测试结果均不得包含 ciphertext、nonce、fingerprint、digest 或明文。Access Key 明文只在 POST 201 返回一次。

- [ ] **步骤 2：写 Key CRUD/批量 API 测试**

逐行部分成功；启停；删除；单测；test-all 顺序执行且同一时刻只测一个 Key；测试复用真实 NVIDIA Client/Fault 分类，不建立第二套逻辑。

- [ ] **步骤 3：写模型 API 测试**

首 Key 候选；保存白名单；编辑 kind/能力；ASR/TTS 未验证不能启用；手动模型测试成功后设置 verified_at 并清除 block。

- [ ] **步骤 4：实现 DTO allowlist 和 Pool 同步**

显式构造 response DTO，禁止直接 JSON 编码数据库 entity。NVIDIA Key 新增/启停/删除和 key-model block 变更必须先提交数据库事务，再调用无失败分支的 `StateSync.UpsertKey/RemoveKey/SetModelBlock` 完成内存同步；App 重启时始终从数据库全量加载 snapshot。增加 App HTTP 测试，管理员停用、删除或解除 block 后下一次调度立即反映变化。

- [ ] **步骤 5：验证并提交**

```bash
go test ./internal/httpapi/admin ./internal/app -run 'NVIDIAKey|AccessKey|Model' -v
git add internal/httpapi/admin/nvidia_keys* internal/httpapi/admin/access_keys* internal/httpapi/admin/models* internal/httpapi/router.go internal/app/app.go
git commit -m "feat: 添加 Key 和模型管理 API"
```

## 任务 33：运行设置、摘要和即时应用

**文件：**
- 创建：`internal/httpapi/admin/settings.go`
- 创建：`internal/httpapi/admin/settings_test.go`
- 创建：`internal/httpapi/admin/runtime.go`
- 创建：`internal/httpapi/admin/runtime_test.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`
- 修改：`internal/app/server.go`

- [ ] **步骤 1：写设置边界测试**

使用 DB CHECK 同样的范围；无效配置 400；合法配置事务保存并原子更新内存 snapshot；进行中的请求继续使用创建时 snapshot，新请求使用新值。

- [ ] **步骤 2：写 runtime summary 测试**

只返回 Key 状态计数、active、queue length/capacity、最早 cooldown、shutting_down；不返回任何 secret 或请求正文。

- [ ] **步骤 3：注册并接入应用**

顶层 Router 注册 `GET/PATCH /admin/api/settings` 和 `GET /admin/api/runtime/summary`；App/Server 注入任务 10 创建的同一个 `runtimeconfig.Store`。PATCH 只调用 `Store(ctx,next)`，不得维护 Admin 专用副本。使用 `httptest.NewServer(app.Handler())` 验证 PATCH 写入 SQLite 后：新 Acquire 使用新 queue 配置，新 Attempt 使用新 first-byte/total，新 NVIDIA Dial 使用新 connect timeout，进行中的请求仍使用创建时 snapshot。

- [ ] **步骤 4：实现并验证**

```bash
go test ./internal/httpapi/admin ./internal/app -run 'Settings|Runtime' -v
git add internal/httpapi/admin/settings* internal/httpapi/admin/runtime* internal/httpapi/router.go internal/app/app.go internal/app/server.go
git commit -m "feat: 添加运行设置和池状态摘要"
```

## 任务 34：请求元数据、日聚合和 30 天清理

**文件：**
- 创建：`internal/observability/request.go`
- 创建：`internal/observability/repository.go`
- 创建：`internal/observability/stats.go`
- 创建：`internal/observability/cleanup.go`
- 创建：`internal/observability/observability_test.go`
- 创建：`internal/httpapi/admin/stats.go`
- 创建：`internal/httpapi/admin/stats_test.go`
- 修改：`internal/httpapi/middleware.go`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`
- 修改：`internal/app/server.go`

- [ ] **步骤 1：定义允许列表结构**

`RequestRecord` 不得包含 `Body/Prompt/Response/Content/Headers/Cookie/Secret` 字段。记录总请求一次，attempt 只用计数和最终使用 Key，不为每个 attempt 创建正文日志。

- [ ] **步骤 2：固定统计口径**

一次客户端请求最终 2xx 为 success；queue_ms 包含等待；duration 从接收请求到完成；first_byte_ms 仅流式/二进制；attempt_count 含首尝试；SSE duration 为完整连接时长；无 usage 则 token 为 NULL，不估算。

- [ ] **步骤 3：写四维 UPSERT 测试**

同一请求更新 global、model、nvidia_key、access_key 四个 daily_stats；删除 Key 后历史以 ID 字符串保留。

- [ ] **步骤 4：写清理测试**

UTC 每日 03:00 执行；删除严格早于 30 天的 request_logs；不删 daily_stats、Key、models、sessions 或 settings；启动后若错过时间，在 1 分钟内补执行一次。

- [ ] **步骤 5：把 Recorder 和 Cleanup Worker 接入运行时**

修改数据面 Middleware，在每个 `/v1` 请求完成或流关闭时调用 Recorder；修改 App 启动 Cleanup Worker，并保存 cancel/wait handle；Server 关闭时停止并等待 Worker。增加真实 HTTP 集成测试：请求 Mock Chat 后 `request_logs` 和四维聚合均更新；使用 fake clock 启动应用后验证错过清理时间会在 1 分钟内补跑。

- [ ] **步骤 6：实现最近错误和统计 API**

错误页只显示固定 code、status、ID、模型、时间、request ID；不显示原始上游 message。修改顶层 Router 注册统计路由。

- [ ] **步骤 7：验证并提交**

```bash
go test ./internal/observability ./internal/httpapi/admin ./internal/app -run 'Stats|Cleanup|Error|Record' -v
go test -race ./internal/observability ./internal/app -count=10
git add internal/observability internal/httpapi/admin/stats* internal/httpapi/middleware.go internal/httpapi/router.go internal/app
git commit -m "feat: 记录安全请求元数据和基础统计"
```

## 任务 35：Vue 工程、API Client、Router 和登录页

**文件：**
- 创建：`web/src/router/index.ts`
- 创建：`web/src/shared/api/client.ts`
- 创建：`web/src/shared/api/types.ts`
- 创建：`web/src/shared/components/AppShell.vue`
- 创建：`web/src/features/auth/api.ts`
- 创建：`web/src/features/auth/useSession.ts`
- 创建：`web/src/features/auth/LoginView.vue`
- 创建：`web/src/features/auth/ChangePasswordView.vue`
- 创建：`web/src/features/auth/useSession.spec.ts`
- 创建：`web/src/features/auth/LoginView.spec.ts`
- 创建：`web/src/features/auth/ChangePasswordView.spec.ts`
- 创建：`web/src/router/index.spec.ts`
- 创建：`web/src/shared/api/client.spec.ts`
- 修改：`web/src/main.ts`
- 修改：`web/src/App.vue`

- [ ] **步骤 1：写 strict 类型和 API 错误测试**

Client 使用 `credentials:'same-origin'`；写操作自动携带浏览器 Origin；统一解析 OpenAI error shape；禁止把 NVIDIA Key 保存到 localStorage/sessionStorage。

- [ ] **步骤 2：写 Router Guard 测试**

无会话到登录页；`must_change_password=true` 只能到改密页；正常会话进入 AppShell。

- [ ] **步骤 3：实现登录和改密页面并接入入口**

展示 HTTP 明文风险横幅；密码最少 12 字符且不能为 admin；错误不回显提交密码。修改 `main.ts` 安装 Router，修改 `App.vue` 渲染 `RouterView`。增加从应用入口导航到登录/改密页面的 Router 集成测试。

- [ ] **步骤 4：验证并提交**

```bash
pnpm --dir web run test -- auth
pnpm --dir web run typecheck
pnpm --dir web run lint --fix
git add web/src/router web/src/shared web/src/features/auth web/src/main.ts web/src/App.vue
git commit -m "feat: 添加管理端登录和强制改密页面"
```

## 任务 36：NVIDIA Key 和模型页面

**文件：**
- 创建：`web/src/features/nvidia-keys/api.ts`
- 创建：`web/src/features/nvidia-keys/types.ts`
- 创建：`web/src/features/nvidia-keys/NvidiaKeysView.vue`
- 创建：`web/src/features/nvidia-keys/KeyTable.vue`
- 创建：`web/src/features/nvidia-keys/KeyCards.vue`
- 创建：`web/src/features/nvidia-keys/BatchImportDialog.vue`
- 创建：`web/src/features/nvidia-keys/KeyTestDialog.vue`
- 创建：`web/src/features/nvidia-keys/NvidiaKeysView.spec.ts`
- 创建：`web/src/features/nvidia-keys/BatchImportDialog.spec.ts`
- 创建：`web/src/features/models/api.ts`
- 创建：`web/src/features/models/types.ts`
- 创建：`web/src/features/models/ModelsView.vue`
- 创建：`web/src/features/models/ModelTable.vue`
- 创建：`web/src/features/models/ModelCards.vue`
- 创建：`web/src/features/models/ModelsView.spec.ts`
- 修改：`web/src/router/index.ts`

- [ ] **步骤 1：写单个/批量导入 UI 测试**

逐行结果显示 line/masked/status/reason；提交后立即清空 textarea；DOM 中不得继续保留完整 Key；不提供复制/查看/导出 NVIDIA Key 按钮。

- [ ] **步骤 2：写桌面/手机组件测试**

桌面显示 table；手机显示 cards；手机可启停、单测、删除；高级批量操作可提示使用桌面。

- [ ] **步骤 3：写模型能力门禁 UI 测试**

kind、vision/tools/reasoning；ASR/TTS 未验证时启用按钮禁用并显示原因；block 关系可查看和手测恢复。

- [ ] **步骤 4：注册页面路由并验证**

只在本任务把 NVIDIA Key 和 Models 页面加入已存在 Router；增加从 AppShell 导航到两个页面的集成测试，不提前引用任务 37 尚未创建的组件。

```bash
pnpm --dir web run test -- nvidia-keys models
pnpm --dir web run typecheck
pnpm --dir web run lint --fix
```

- [ ] **步骤 5：提交**

```bash
git add web/src/features/nvidia-keys web/src/features/models web/src/router/index.ts
git commit -m "feat: 添加 NVIDIA Key 和模型运维页面"
```

## 任务 37：Access Key、运行状态、设置和统计页面

**文件：**
- 创建：`web/src/features/access-keys/api.ts`
- 创建：`web/src/features/access-keys/types.ts`
- 创建：`web/src/features/access-keys/AccessKeysView.vue`
- 创建：`web/src/features/access-keys/CreateAccessKeyDialog.vue`
- 创建：`web/src/features/access-keys/AccessKeysView.spec.ts`
- 创建：`web/src/features/runtime/api.ts`
- 创建：`web/src/features/runtime/types.ts`
- 创建：`web/src/features/runtime/RuntimeView.vue`
- 创建：`web/src/features/runtime/SettingsForm.vue`
- 创建：`web/src/features/runtime/RuntimeView.spec.ts`
- 创建：`web/src/features/statistics/api.ts`
- 创建：`web/src/features/statistics/types.ts`
- 创建：`web/src/features/statistics/StatisticsView.vue`
- 创建：`web/src/features/statistics/StatisticsView.spec.ts`
- 修改：`web/src/router/index.ts`
- 修改：`web/src/shared/components/AppShell.vue`

- [ ] **步骤 1：写 Access Key 一次展示测试**

创建后 modal 一次显示和复制；关闭后再次打开列表不能恢复明文；撤销需确认；显示 name/prefix/created/last_used。

- [ ] **步骤 2：写 runtime/settings 测试**

显示池状态、active、queue；编辑 queue 100/60 秒、connect 10 秒、first-byte 60 秒、nonstream 5 分钟、shutdown 60 秒；后端校验错误显示在字段旁。

- [ ] **步骤 3：写 stats 测试**

总体、模型、NVIDIA Key、Access Key 四维；只显示计数、成功率、平均 duration/queue、attempt 和 token；无 token 显示 `—`。

- [ ] **步骤 4：注册页面路由并验证**

将三个页面加入 Router 和 AppShell 导航，增加从应用入口访问页面的集成测试。

```bash
pnpm --dir web run test -- access-keys runtime statistics
pnpm --dir web run typecheck
pnpm --dir web run lint --fix
git add web/src/features/access-keys web/src/features/runtime web/src/features/statistics web/src/router/index.ts web/src/shared/components/AppShell.vue
git commit -m "feat: 完成运维摘要和访问 Key 管理页面"
```

## 任务 38：嵌入前端、SPA 路由和静态安全头

**文件：**
- 创建：`internal/web/embed.go`
- 创建：`internal/web/handler.go`
- 创建：`internal/web/handler_test.go`
- 创建：`internal/web/dist/index.html`
- 修改：`web/vite.config.ts`
- 修改：`.gitignore`
- 修改：`internal/httpapi/router.go`
- 修改：`internal/app/app.go`

- [ ] **步骤 1：写 SPA 和 API 隔离测试**

管理前端未知路径回退 `index.html`；`/v1/*`、`/admin/api/*`、`/health/*` 永不回退 HTML；静态文件正确 Content-Type。

- [ ] **步骤 2：实现安全头**

即使 HTTP 也设置：`X-Content-Type-Options:nosniff`、`Referrer-Policy:no-referrer`、`Cache-Control:no-store`（登录/API）、CSP 只允许 self 和 data 图片；不得设置虚假的 HSTS。

- [ ] **步骤 3：构建和嵌入**

将 Vite `build.outDir` 固定为绝对解析到仓库的 `internal/web/dist`，构建时 `emptyOutDir=true`。使用 `//go:embed all:dist`，因为 embed pattern 只能读取 `internal/web` 子目录。仓库提交一个不含脚本的最小 `internal/web/dist/index.html` 测试占位文件以保证干净 checkout 能编译；生产 Docker 和发布验证必须先运行前端 build 覆盖它。`.gitignore` 忽略 dist 中除该 index 占位以外的构建产物。

- [ ] **步骤 4：挂载到真实应用并验证**

App 将 `internal/web.Handler` 作为显式依赖传给顶层 Router，替换任务 10 的占位页面；路由优先级固定为 health → `/v1` → `/admin/api` → 静态文件/SPA fallback。App 级 HTTP 测试验证 `/` 和前端深链返回 index，三个 API 前缀永不返回 HTML。

- [ ] **步骤 5：验证和提交**

```bash
pnpm --dir web run build
go test ./internal/web ./internal/app ./internal/httpapi/... -v
go test ./...
git add internal/web web/vite.config.ts .gitignore internal/httpapi/router.go internal/app/app.go
git commit -m "feat: 将 Vue 管理端嵌入 Go 应用"
```

## 任务 39：优雅关闭和服务生命周期

**文件：**
- 创建：`internal/app/shutdown.go`
- 创建：`internal/app/shutdown_test.go`
- 修改：`internal/app/server.go`
- 修改：`internal/httpapi/health/health.go`
- 修改：`internal/pool/pool.go`
- 修改：`internal/pool/pool_test.go`
- 修改：`internal/pool/queue.go`
- 修改：`internal/pool/queue_test.go`

- [ ] **步骤 1：写关闭顺序测试**

SIGTERM 后 ready 立即 503；新 API 请求和入队返回 `server_shutting_down`；已有请求最多等待 settings 中 60 秒；超时后取消上游；最后关闭 DB。

- [ ] **步骤 2：写 SSE 和队列关闭测试**

已有 SSE 在 60 秒内正常完成则成功；队列 waiter 立即获得 503，不继续等待；超期 SSE context 被取消并释放 Lease。

- [ ] **步骤 3：实现两阶段关闭**

关闭开始时从共享 `runtimeconfig.Provider` 读取一次 ShutdownGrace snapshot。顺序固定为：原子设置 `shuttingDown` → 调用任务 16 的 `Pool.Shutdown()`，在同一锁内拒绝新 Acquire 并 drain/cancel 所有排队 waiter（返回 503）→ 启动 `http.Server.Shutdown` 等待正在执行的非排队请求 → 同时等待数据面与 Cleanup Worker wait group → 宽限期到达后取消 root context → 等待资源释放 → DB Close。不得先阻塞在 `Server.Shutdown` 再清队列。

- [ ] **步骤 4：验证并提交**

```bash
go test -race ./internal/app ./internal/pool -run Shutdown -count=10
git add internal/app internal/httpapi/health/health.go internal/pool/pool* internal/pool/queue*
git commit -m "feat: 添加 60 秒优雅关闭流程"
```

## 任务 40：Docker、Compose 和 CI

**文件：**
- 创建：`Dockerfile`
- 创建：`docker-compose.yml`
- 创建：`.dockerignore`
- 创建：`.github/workflows/ci.yml`
- 创建：`scripts/check-secrets.sh`

- [ ] **步骤 1：实现多阶段构建**

阶段：pnpm frontend（输出 `internal/web/dist`）→ `CGO_ENABLED=0` Go build → Debian slim runtime。最终使用固定非 root UID/GID；镜像内预创建并授权 `/data`；`/tmp` 为临时目录；镜像不包含 `.env`、DB、Key 或 live test secrets。

- [ ] **步骤 2：实现单服务 Compose**

只包含 app；映射 `3756:3756`；使用 Docker named volume `nvidia-router-data:/data`，避免首次 bind mount 的 root 所有权导致非 root 进程无法写入；主密钥从环境注入；healthcheck 调用 `/health/live`，部署就绪检查另用 `/health/ready`。不加入 Redis/PostgreSQL。若用户改用宿主机 bind mount，部署文档必须给出预创建目录和匹配 UID/GID 的权限命令。

- [ ] **步骤 3：实现 CI**

```text
Web: pnpm install --frozen-lockfile, lint, typecheck, test, build（先生成 internal/web/dist）
Go: go test ./..., go test -race ./..., golangci-lint
Docker: docker build
```

CI 固定 `golangci-lint` 版本，与 `scripts/check-dev-env.sh` 一致；不能使用浮动 latest。前端 build 必须先于会编译 `internal/web` 的 Go 命令，避免干净 checkout 只使用占位 index。

真实 NVIDIA 测试不在普通 CI 自动运行。

- [ ] **步骤 4：验证镜像**

```bash
docker build -t nvidia-router:test .
docker compose config
```

扫描：`docker history` 和导出文件中不含测试 Key。`scripts/check-secrets.sh` 使用 `git ls-files -z` 扫描所有 tracked 文件（包括 docs 和 tests），匹配真实 NVIDIA/Access Key 形状时返回非 0；只按精确固定值 allowlist 明显假 Key，禁止整体排除目录。脚本使用 `if rg ...; then exit 1; fi` 正确处理“无匹配时 rg 返回 1”的语义，并纳入 CI。

- [ ] **步骤 5：提交**

```bash
git add Dockerfile docker-compose.yml .dockerignore .github scripts/check-secrets.sh
git commit -m "build: 添加单实例容器和持续集成"
```

## 任务 41：真实 NVIDIA 联调、能力验证和可重复脚本

**文件：**
- 创建：`tests/live/live_test.go`
- 创建：`scripts/test/live-nvidia.sh`
- 创建：`docs/NVIDIA真实联调说明.md`

- [ ] **步骤 1：实现显式 live test 门禁**

使用 build tag `live`；从 `NVIDIA_ROUTER_LIVE_KEY` 或已经运行的管理页面测试链获取 Key；缺失时 `t.Skip`，不得空成功；输出只包含 case 名、status 和耗时，不输出 body/Key。

- [ ] **步骤 2：覆盖真实主路径**

Models、非流式 Chat、SSE Chat、非流式 Responses、流式 Responses、实际可用 Embedding。ASR/TTS 只有在账户模型和 endpoint 真正验证后执行，并将对应模型的 `capability_verified_at` 设置流程写入文档。

- [ ] **步骤 3：实现幂等脚本**

脚本检测服务、创建临时 Access Key、运行测试、最后撤销临时 Key；Key 通过环境传递；失败也执行清理；不写测试数据到仓库。

- [ ] **步骤 4：运行**

```bash
go test -tags=live ./tests/live -v
bash scripts/test/live-nvidia.sh
```

预期：配置能力均 PASS；账户不支持的 Audio case 明确 SKIP 并说明未启用模型，不能伪造 PASS。

- [ ] **步骤 5：提交**

```bash
git add tests/live scripts/test docs/NVIDIA真实联调说明.md
git commit -m "test: 添加真实 NVIDIA 主路径联调"
```

## 任务 42：部署、恢复和 HTTP 风险文档

**文件：**
- 创建：`README.md`
- 创建：`docs/Linux单机部署说明.md`
- 创建：`docs/备份与恢复说明.md`
- 创建：`docs/API兼容范围.md`

- [ ] **步骤 1：写部署说明**

给出主密钥生成命令：

```bash
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

说明默认 `http://SERVER_IP:3756`、首次 `admin/admin`、强制改密、数据卷和重启命令。

- [ ] **步骤 2：醒目标注 HTTP 风险**

必须明确：管理员密码、Cookie、Access Key、提示词和响应均明文传输；第一轮不是安全生产部署；禁止写“安全公网部署”；HTTPS 属于后续迭代。

- [ ] **步骤 3：写备份恢复**

停服恢复 DB；必须配套原主密钥；使用 `db backup`；禁止在线复制 WAL 作为正式备份；恢复后先验证 ready 和 sentinel。

- [ ] **步骤 4：写 API 兼容表**

列出支持/部分支持/明确拒绝；Chat 图片 URL/Base64；Responses 不支持项；Audio 真实验证门禁；所有未知 `/v1/*` 501。

- [ ] **步骤 5：文档命令验真并提交**

逐条执行 README 中的构建、测试、Compose config 和 CLI help 命令。

```bash
git add README.md docs/Linux单机部署说明.md docs/备份与恢复说明.md docs/API兼容范围.md
git commit -m "docs: 完成部署恢复和兼容范围说明"
```

## 任务 43：Playwright 管理流程 E2E

**文件：**
- 创建：`web/playwright.config.ts`
- 创建：`tests/e2e/harness/main.go`
- 创建：`tests/e2e/run.sh`
- 创建：`tests/e2e/admin.spec.ts`
- 创建：`tests/e2e/keys.spec.ts`
- 创建：`tests/e2e/runtime.spec.ts`
- 修改：`web/package.json`
- 修改：`pnpm-lock.yaml`
- 修改：`.github/workflows/ci.yml`

- [ ] **步骤 1：安装固定 Playwright 并创建可启动 Harness**

将固定版本 `@playwright/test` 加入 devDependencies，更新 lockfile；CI 执行 `pnpm --dir web exec playwright install --with-deps chromium`。`tests/e2e/harness/main.go` 在随机本地端口启动可编程 Mock NVIDIA 和完整 App，使用 `t.TempDir` 等价的进程临时目录、随机主密钥和 `AllowInsecureTestUpstream=true`；将 App URL 写到 stdout。Harness 捕获 SIGTERM 并删除 DB、WAL、临时文件。`tests/e2e/run.sh` 负责构建前端、启动 Harness、等待 `/health/live`、设置 Playwright baseURL、运行 Chromium，并用 trap 清理。

- [ ] **步骤 2：写首次登录和改密 E2E**

验证 admin/admin → 强制改密 → 正常面板 → logout → 新密码登录；改密前代理调用被拒绝。

- [ ] **步骤 3：写 Key 管理 E2E**

使用 Mock NVIDIA；单个/批量部分成功；DOM 无完整 Key 残留；Access Key 一次展示；模型勾选；启停和 test-all。

- [ ] **步骤 4：写移动端 E2E**

手机 viewport 下卡片布局；可查看、启停、单测和撤销；高级操作提示使用桌面。

- [ ] **步骤 5：运行并提交**

```bash
pnpm --dir web exec playwright install chromium
bash tests/e2e/run.sh

git add web/playwright.config.ts web/package.json pnpm-lock.yaml tests/e2e .github/workflows/ci.yml
git commit -m "test: 覆盖管理端关键用户流程"
```

## 任务 44：最终发布门禁和需求追踪核验

**文件：**
- 创建：`docs/第一轮验收报告.md`

- [ ] **步骤 1：运行完整前端验证**

```bash
pnpm --dir web run lint --fix
pnpm --dir web run typecheck
pnpm --dir web run test
pnpm --dir web run build
bash tests/e2e/run.sh
```

预期：全部 PASS；该顺序先生成 `internal/web/dist`，再编译 Go embed 包。

- [ ] **步骤 2：运行完整后端验证**

```bash
go test ./...
go test -race ./...
golangci-lint run --fix ./...
```

预期：全部 PASS，无 race。

- [ ] **步骤 3：运行协议和泄漏门禁**

```bash
go test -race ./tests/mocknvidia -count=10
bash scripts/check-secrets.sh
```

预期：Mock 全 PASS；脚本扫描全部 Git tracked 文件且没有秘密匹配。测试假 Key 必须使用脚本中精确列出的固定 allowlist 值，不能排除整个 `docs` 或 `tests` 目录。

- [ ] **步骤 4：运行容器验证**

```bash
export NVIDIA_ROUTER_MASTER_KEY="$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"
trap 'docker compose -p nvr-acceptance down -v' EXIT
docker build -t nvidia-router:acceptance .
docker compose -p nvr-acceptance up -d --wait
curl -fsS http://127.0.0.1:3756/health/live
if curl -fsS http://127.0.0.1:3756/health/ready; then
  echo 'expected initial readiness to fail before password change' >&2
  exit 1
fi
```

预期：live 为 200；全新 volume 中首次未改密时 ready 为 503。改密后的 ready=200 已由任务 31/43 的应用 E2E 验证；trap 无论成功失败都删除验收容器和 volume。

- [ ] **步骤 5：逐条填写验收报告**

报告必须映射需求文档第 1～26 章、`FR/NFR/SEC/OPS/TEST/OUT` 编号、实施 commit、测试命令和结果。任何未通过项标记 FAIL，不得使用“基本通过”。

- [ ] **步骤 6：确认排除项没有被实现**

检查依赖和路由中不存在 Redis、PostgreSQL、Provider registry、团队用户、计费、权重、多实例、Prometheus、HTTPS 或反向代理配置。

- [ ] **步骤 7：提交最终验收报告**

```bash
git add docs/第一轮验收报告.md
git commit -m "docs: 记录 NVIDIA 路由器第一轮验收结果"
```

---

## 6. 需求追踪矩阵

| 需求章节 | 追踪 ID | 实施任务 | 验证任务 |
|---|---|---|---|
| 产品目标/范围 | FR-SCOPE-001～006 | 1～3、40～42 | 44 |
| 通用 API | FR-API-001 | 3、20、31 | 30、44 |
| Models | FR-API-002 | 14、22 | 22、30、41 |
| Chat | FR-API-003 | 18～21 | 20、21、30、41 |
| Responses | FR-API-004 | 23～25 | 23～25、30、41 |
| Embeddings | FR-API-005 | 26 | 26、30、41 |
| Audio | FR-API-006～007 | 27～29 | 28～30、41 |
| 未支持接口 | FR-API-008 | 22 | 22、30 |
| 请求转换 | FR-CONV-001～003 | 19、23 | 19、23、30 |
| NVIDIA Key | FR-KEY-001～007 | 5、12、13、32 | 13、30、32 |
| 模型白名单 | FR-MODEL-001～004 | 14、32 | 14、22、32 |
| 调度 | FR-SCHED-001～006 | 15～18 | 15～18、30 |
| 故障/冷却 | FR-FAIL-001～006 | 17、18 | 17、18、30 |
| 队列/超时 | FR-QUEUE-001～006、NFR-TIMEOUT | 16、18、33 | 16、18、30 |
| 管理员/会话 | SEC-AUTH-001～005 | 6、7、31 | 7、31、43 |
| Access Key | SEC-DKEY-001～004 | 8、32 | 8、32、43 |
| Web 面板 | FR-UI-001～007 | 35～38 | 35～38、43 |
| SQLite/迁移 | NFR-DATA-001～005 | 4 | 4、44 |
| 加密 | SEC-CRYPT-001～006 | 5、13 | 5、13、30 |
| 日志/隐私 | SEC-PRIV-001～005 | 3、34 | 30、34、44 |
| 数据保留 | NFR-RET-001～003 | 34 | 34 |
| 备份/恢复 | OPS-BACKUP-001～005 | 9、42 | 9、42 |
| 健康检查 | OPS-HEALTH-001～003 | 10、39 | 10、39、44 |
| 部署 | OPS-DEPLOY-001～006 | 40、42 | 40、44 |
| 优雅关闭 | OPS-SHUTDOWN-001～005 | 39 | 39、44 |
| 真实联调 | TEST-REAL-001～008 | 41 | 41、44 |
| Mock 边界 | TEST-MOCK-001～035 | 30 | 30、44 |
| 排除项 | OUT-001～028 | 全任务约束 | 44 |

---

## 7. Codex 每阶段停止条件

### 阶段 0 完成条件

- Go/Vue 工程可构建。
- 配置和统一错误测试通过。
- 尚未接触真实 NVIDIA Key。

### 阶段 1 完成条件

- SQLite WAL/迁移、sentinel、管理员、会话、Access Key、CLI 和 health 全部通过 race test。
- 数据库、WAL、日志不含任何测试 secret 明文。
- 错误主密钥启动失败。

### 阶段 2 完成条件

- `/v1/models`、普通/流式 Chat 可使用 Mock NVIDIA。
- Round Robin、单并发、FIFO、冷却、failover、取消和 committed 行为可重复通过 race test。
- SSE 大事件、重复 `[DONE]` 和断流测试通过。

### 阶段 3 完成条件

- Responses 普通/流式、Embeddings、ASR/TTS Adapter 和完整 Mock 矩阵通过。
- Audio 未真实验证模型仍不能启用。
- 协议模块无数据库/调度依赖。

### 阶段 4 完成条件

- 管理端流程、统计清理、容器、备份、关闭和真实主路径通过。
- 所有需求有明确 PASS/FAIL 证据。
- 不存在未声明的功能、秘密或正文持久化。

---

## 8. 实施时必须警惕的错误

1. **不要复制 9Router 的宽泛 fallback。** 400/422 等请求错误不能遍历 Key。
2. **不要让 `http.Transport` 自动做语义重试。** 同一 Key 对同一请求只能一次。
3. **不要把 SSE passthrough 写成默认 Scanner。** 必须支持多行 data、大事件、CRLF 和 UTF-8 分块。
4. **不要在上游 Header 到达时就视为 committed。** 流式必须等首个完整可输出事件，TTS 必须等首块音频。
5. **不要静默删除 Responses 不支持字段。** `store:true` 等必须明确拒绝。
6. **不要把多个 NVIDIA Key 存在一个字段。** 一 Key 一行，一状态一行。
7. **不要存原始上游错误正文。** 只保存分类码、status 和允许的 request ID。
8. **不要在 debug 模式记录请求体、响应体或 SSE chunk。** 隐私规则不因日志级别变化。
9. **不要把 Audio 临时文件理解为正文持久化。** 它只能存在请求生命周期，权限 0600，最终必删。
10. **不要声明 ASR/TTS 已支持，除非真实联调通过并设置 capability_verified_at。**
11. **不要给管理页面添加 NVIDIA Key 明文查看或导出。**
12. **不要把第一轮 HTTP 部署写成安全生产方案。**
13. **不要为未来团队化设计 Repository 接口或数据库抽象。** 仅在 Access Key 表保留未来 owner 扩展的迁移空间，不提前加成员业务。
14. **不要把 New API 的 AGPL RelayKit 作为依赖。**
15. **不要在没有用户要求时 push、创建远程仓库或部署到公网。**
