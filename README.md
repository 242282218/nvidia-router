# NVIDIA API 路由器

NVIDIA Build 专用的 OpenAI-compatible API 路由器，提供 NVIDIA API Key 加密存储、下游 Access Key、模型白名单、限流感知调度、管理页面和 SQLite 持久化。

本项目面向第一轮 MVP 和个人使用。第一轮支持单实例 Docker Compose 部署，也支持在受信反向代理后使用 external origin 与 Secure Cookie；不包含应用内 TLS、多实例、公开多租户或其他上游厂商。

## HTTP 与反向代理安全边界

> **重要：应用本身只提供 HTTP。直接将非回环监听暴露到不可信网络是不安全的；生产部署必须在受信反向代理后使用 HTTPS、`NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN`、`NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS` 和 `NVIDIA_ROUTER_ADMIN_SECURE_COOKIE=true`。**
>
> 普通 HTTP 会明文传输管理员密码、管理员 Cookie、下游 Access Key、提示词和响应内容。`X-Forwarded-Proto` 只有来自受信代理 CIDR 的请求才会被使用。

## 快速开始

默认 Compose 只绑定宿主机回环地址 `http://127.0.0.1:3756`。管理员用户名固定为 `admin`，首次密码由 `NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD` 注入，首次登录后必须立即修改；修改前，代理 API 和敏感管理操作不可用。

### 生成主密钥

主密钥必须在数据库和容器之外单独保管。使用以下命令生成 32 字节 Raw URL Base64 主密钥：

```bash
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

将结果作为 `NVIDIA_ROUTER_MASTER_KEY`。不要把真实主密钥提交到仓库、Shell history、Issue 或日志。

### 使用 Docker Compose

在仓库根目录创建 `.env`，设置主密钥和一次性初始管理员密码：

```dotenv
NVIDIA_ROUTER_MASTER_KEY=替换为上面命令生成的值
NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD=<生成至少12个字符的随机密码>
# 内置星空采集器：可由运行时 Secret 注入，也可在管理端加密保存
NVIDIA_ROUTER_XK_UPSTREAM_URL=由运行时 Secret Provider 注入
```

不要把真实密码写入仓库或命令历史；已有管理员记录后，初始密码不会覆盖现有密码，但 Compose 仍要求该变量存在以完成配置校验。

启动服务：

```bash
docker compose up -d --build
```

查看状态和日志：

```bash
docker compose ps
docker compose logs -f app
```

验证进程存活：

```bash
curl --fail http://SERVER_IP:3756/health/live
```

`docker-compose.yml` 使用 named volume `nvidia-router-data` 持久化 `/data`，数据库不会因容器重建而自动删除。重启或升级时仍使用同一个卷：

```bash
docker compose restart app
docker compose up -d --build
```

停止服务（保留 named volume）：

```bash
docker compose stop app
```

不要使用 `docker compose down -v`，除非明确要删除数据库和全部持久化数据。

更完整的 Linux 单机步骤见 [docs/Linux单机部署说明.md](docs/Linux单机部署说明.md)，备份和恢复见 [docs/备份与恢复说明.md](docs/备份与恢复说明.md)。

## 配置

| 变量 | 默认值或要求 | 说明 |
| --- | --- | --- |
| `NVIDIA_ROUTER_MASTER_KEY` | 必填 | 32 字节 Raw URL Base64 主密钥；错误或缺失会阻止服务正常启动 |
| `NVIDIA_ROUTER_MASTER_KEY_VERSION` | `1` | 当前主密钥版本；轮转完成后切换到新版本 |
| `NVIDIA_ROUTER_LEGACY_MASTER_KEY` | 空 | 轮转兼容窗口内的旧主密钥；只从运行时环境注入，不写入数据库或日志 |
| `NVIDIA_ROUTER_LEGACY_MASTER_KEY_VERSION` | `1` | 旧主密钥版本 |
| `NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD` | 必填 | 首次初始化密码，至少 12 个字符；已有管理员时不会覆盖密码 |
| `NVIDIA_ROUTER_ADMIN_SECURE_COOKIE` | `false` | HTTPS 反向代理部署设为 `true` |
| `NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN` | 空 | 反向代理公开的完整 `http(s)` origin，不含路径或 query |
| `NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS` | 空 | 仅信任这些来源 IP 的 forwarded proto |
| `NVIDIA_ROUTER_LISTEN_ADDR` | `0.0.0.0:3756` | 应用监听地址；Compose 默认只将宿主端口绑定到 `127.0.0.1` |
| `NVIDIA_ROUTER_DATA_DIR` | `/data` | SQLite 数据目录 |
| `NVIDIA_ROUTER_TEMP_DIR` | `/tmp` | 请求临时资源目录 |
| `NVIDIA_ROUTER_NVIDIA_BASE_URL` | `https://integrate.api.nvidia.com` | NVIDIA 上游 HTTPS 地址 |
| `NVIDIA_ROUTER_XK_UPSTREAM_URL` | 空 | 内置采集器的 XApi 地址；必须带 provider query 凭据，可作为运行时回退，也可从管理端加密保存；Web 只返回脱敏 endpoint |
| `NVIDIA_ROUTER_XK_VALIDATION_URL` | NVIDIA 基础地址 | 代理验证地址，不含 query 或凭据 |
| `NVIDIA_ROUTER_XK_VALIDATION_STATUS` | `404` | 代理验证期望的 HTTP 状态码 |
| `NVIDIA_ROUTER_XK_COLLECT_INTERVAL` | `5s` | 采集周期 |
| `NVIDIA_ROUTER_XK_PROXY_TTL` | `120s` | 代理出口 TTL |
| `NVIDIA_ROUTER_XK_EXPECTED_QTY` | `2` | 每次租约期望出口数量 |
| `NVIDIA_ROUTER_XK_CONCURRENCY` | `2` | 代理验证并发数 |

`nvida反代` 在单体进程内完成 XApi 采集、TXT 解析、代理验证、TTL 管理、质量评分、轮换和 NVIDIA CONNECT。代理配置启用后不会静默回退直连；上游未就绪、代理池为空或 CONNECT 失败会返回临时不可用。Web 可以热更新采集参数，也可以通过管理员页面提交新的 XApi URL；服务端只保存其加密密文，页面和 API 只返回脱敏 endpoint。运行时 Secret 仍可作为首次启动或兼容回退配置。

流式请求的运行时设置将首 token 等待与已提交响应的空闲窗口分开：`stream_first_token_timeout_ms` 默认 60000，`stream_idle_timeout_ms` 默认 180000。DeepSeek v4-flash 这类长思考模型建议保留较大的 idle 窗口；窗口越大，单个 Key 的流式槽位被占用时间越长。429 会尊重上游 `Retry-After` 后再切换 Key，NVIDIA 529 临时过载也会进入有界重试和冷却，不会通过代理失败静默回退直连。

## CLI

镜像入口为 `/usr/local/bin/nvidia-router`，默认容器命令为 `serve`。当前 CLI 入口和参数如下：

```text
nvidia-router --help
nvidia-router serve
nvidia-router admin reset-password  # read the new password from stdin
nvidia-router admin rotate-master-key --new-version <n> --backup <path>
nvidia-router db backup --output <path>
```

当前 CLI 只实现顶层 `--help`，没有 `db help` 或 `serve --help` 子命令；执行 `nvidia-router db help` 会返回无效命令并退出。数据库帮助信息以顶层 `--help` 和本节的实际 `db backup` 用法为准。

容器内执行管理命令时，数据目录为 `/data`。例如先停服，再在容器内生成数据库备份：

```bash
docker compose stop app
docker compose run --rm --no-deps -v "$(pwd)/backups:/data-backups" app db backup --output /data-backups/router.db
```

恢复前请阅读 [docs/备份与恢复说明.md](docs/备份与恢复说明.md)。每日自动备份与保留策略见 [docs/自动备份方案.md](docs/自动备份方案.md)。密码重置不会重新加密或删除 NVIDIA Key，但重置后会撤销全部管理员会话：

```bash
read -r -s new_password
printf '%s\n' "$new_password" | docker compose run --rm --no-deps app admin reset-password
unset new_password
```

## API 范围

已实现的 OpenAI-compatible 路径、部分支持和明确拒绝项见 [docs/API兼容范围.md](docs/API兼容范围.md)。健康检查包括：

- `GET /health/live`：进程存活检查。
- `GET /health/ready`：数据库、迁移、主密钥 sentinel、首次改密和关闭状态检查；首次改密完成前不会返回就绪。

所有未知的 `/v1/*` 路径返回结构化 HTTP `501`，不会转发到 NVIDIA。

Audio 模型的真实能力验证使用 `POST /admin/api/models/<id>/test`，兼容别名为 `/admin/api/models/<id>/verify`。请求体只允许 `{"key_id": <positive integer>}`；未知字段返回 `400 invalid_request`。服务端使用对应加密 NVIDIA Key 真实调用模型 endpoint，成功后生成 UTC `capability_verified_at` 并事务清除 block；失败不写时间、不清 block，调用者不能提交 `verified_at`。ASR/TTS 验证前不能启用，验证后仍需显式 PATCH `{"enabled":true}`。

真实联调见 [docs/NVIDIA真实联调说明.md](docs/NVIDIA真实联调说明.md)。`NVIDIA_ROUTER_LIVE_KEY` 只能从运行环境注入。`SKIP` 不是 PASS，命令成功退出也不能替代逐 case `status=PASS`；CI 负责 race、lint、secret scan、Compose 和 E2E，真实 NVIDIA 仍需显式注入运行时凭证。真实联调会产生 NVIDIA 费用并处理敏感数据，第一轮普通 HTTP 明文风险仍然存在。

星空代理的真实租约和热连接验证见 [docs/星空代理真实联调说明.md](docs/星空代理真实联调说明.md)。

## 开发和本地验证

需要 Go、Node.js、pnpm；Docker 验证还需要 Docker Engine 和 Compose 插件。下面列出任务要求的命令及其在当前仓库中的真实入口和结果：

```bash
# CLI 帮助：成功
go run ./cmd/nvidia-router --help

# Go 测试：成功
go test ./...

# 根目录命令：当前仓库没有 package.json，会失败
pnpm build

# 前端真实入口：成功
pnpm --dir web run typecheck
pnpm --dir web run build

# Compose 配置：需要 Docker 和主密钥
docker compose config

# CLI serve：需要 NVIDIA_ROUTER_MASTER_KEY 才能启动
go run ./cmd/nvidia-router serve

# 当前 CLI 没有 db help 子命令，会返回 invalid command
go run ./cmd/nvidia-router db help
```

仓库根目录没有 `package.json`，所以根目录命令 `pnpm build` 会返回 `ERR_PNPM_NO_IMPORTER_MANIFEST_FOUND`；不要把它当成可用构建入口。当前可用构建入口是 `pnpm --dir web run build`。

`docker compose config` 需要提供 `NVIDIA_ROUTER_MASTER_KEY`，例如通过环境变量注入，不要把真实密钥写入文件或命令历史：

```bash
NVIDIA_ROUTER_MASTER_KEY=REPLACE_WITH_A_32_BYTE_RAW_URL_BASE64_KEY docker compose config
```

`go run ./cmd/nvidia-router serve` 需要完整运行配置；没有主密钥时会以 `NVIDIA_ROUTER_MASTER_KEY is required` 失败。数据库命令的实际入口是 `db backup --output <path>`，不是 `db help`：

```bash
go run ./cmd/nvidia-router db backup --output <path>
```

该命令还需要已存在的 `/data/router.db` 和可写输出路径，正式备份请先停服并按 [docs/备份与恢复说明.md](docs/备份与恢复说明.md) 操作。

本轮验证结果为：`go test ./...`、`go vet ./...`、`pnpm --dir web run lint`、`pnpm --dir web run test`（103 个测试）、`pnpm --dir web run typecheck`、`pnpm --dir web run build` 和 `git diff --check` 成功；`go test -race ./...` 因当前环境缺少 CGO 编译器未执行成功；`docker compose config` 未执行，因为当前环境没有 `docker` 可执行文件。以上命令不能代表真实 NVIDIA 联调或 E2E 通过。真实联调需要运行中的路由器、完成首次改密、有效 NVIDIA Key、模型白名单和逐项 `status=PASS` 证据；详见 [docs/NVIDIA真实联调说明.md](docs/NVIDIA真实联调说明.md)。没有这些证据时，不得宣称 live 或 E2E 已通过。

## 相关文档

- [Linux 单机部署说明](docs/Linux单机部署说明.md)
- [备份与恢复说明](docs/备份与恢复说明.md)
- [自动备份方案](docs/自动备份方案.md)
- [多实例部署注意事项](docs/多实例部署注意事项.md)
- [安全加固检查清单](docs/安全加固检查清单.md)
- [API 兼容范围](docs/API兼容范围.md)
- [NVIDIA 真实联调说明](docs/NVIDIA真实联调说明.md)
- [第一轮需求文档](docs/NVIDIA%20API路由器第一轮需求文档.md)
