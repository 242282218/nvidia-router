# NVIDIA API 路由器

NVIDIA Build 专用的 OpenAI-compatible API 路由器，提供 NVIDIA API Key 加密存储、下游 Access Key、模型白名单、限流感知调度、管理页面和 SQLite 持久化。

本项目面向第一轮 MVP 和个人使用。第一轮只支持单实例 Docker Compose 部署，不包含 HTTPS、反向代理、多实例、公开多租户或其他上游厂商。

## 第一轮 HTTP 风险

> **重要：第一轮默认通过普通 HTTP 提供服务，不是安全生产部署。禁止将本项目当前的 HTTP 方式描述为安全公网部署，也不要把它用于不可信网络。**
>
> 普通 HTTP 会明文传输管理员密码、管理员 Cookie、下游 Access Key、提示词和响应内容。HTTPS、证书和反向代理属于后续迭代，不在当前第一轮范围内。

## 快速开始

默认访问地址为 `http://SERVER_IP:3756`。首次管理员账号和密码均为 `admin`，即 `admin/admin`。首次登录后必须立即修改密码；修改前，代理 API 和敏感管理操作不可用。

### 生成主密钥

主密钥必须在数据库和容器之外单独保管。使用以下命令生成 32 字节 Raw URL Base64 主密钥：

```bash
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

将结果作为 `NVIDIA_ROUTER_MASTER_KEY`。不要把真实主密钥提交到仓库、Shell history、Issue 或日志。

### 使用 Docker Compose

在仓库根目录创建 `.env`，至少设置主密钥：

```dotenv
NVIDIA_ROUTER_MASTER_KEY=替换为上面命令生成的值
```

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
| `NVIDIA_ROUTER_LISTEN_ADDR` | `0.0.0.0:3756` | 监听地址 |
| `NVIDIA_ROUTER_DATA_DIR` | `/data` | SQLite 数据目录 |
| `NVIDIA_ROUTER_TEMP_DIR` | `/tmp` | 请求临时资源目录 |
| `NVIDIA_ROUTER_NVIDIA_BASE_URL` | `https://integrate.api.nvidia.com` | NVIDIA 上游 HTTPS 地址 |

## CLI

镜像入口为 `/usr/local/bin/nvidia-router`，默认容器命令为 `serve`。当前 CLI 入口和参数如下：

```text
nvidia-router --help
nvidia-router serve
nvidia-router admin reset-password --password <new>
nvidia-router db backup --output <path>
```

当前 CLI 只实现顶层 `--help`，没有 `db help` 或 `serve --help` 子命令；执行 `nvidia-router db help` 会返回无效命令并退出。数据库帮助信息以顶层 `--help` 和本节的实际 `db backup` 用法为准。

容器内执行管理命令时，数据目录为 `/data`。例如先停服，再在容器内生成数据库备份：

```bash
docker compose stop app
docker compose run --rm --no-deps -v "$(pwd)/backups:/data-backups" app db backup --output /data-backups/router.db
```

恢复前请阅读 [docs/备份与恢复说明.md](docs/备份与恢复说明.md)。密码重置不会重新加密或删除 NVIDIA Key，但重置后会撤销全部管理员会话：

```bash
docker compose run --rm --no-deps app admin reset-password --password '<new-password>'
```

## API 范围

已实现的 OpenAI-compatible 路径、部分支持和明确拒绝项见 [docs/API兼容范围.md](docs/API兼容范围.md)。健康检查包括：

- `GET /health/live`：进程存活检查。
- `GET /health/ready`：数据库、迁移、主密钥 sentinel、首次改密和关闭状态检查；首次改密完成前不会返回就绪。

所有未知的 `/v1/*` 路径返回结构化 HTTP `501`，不会转发到 NVIDIA。

Audio 模型的真实能力验证使用 `POST /admin/api/models/<id>/test`，兼容别名为 `/admin/api/models/<id>/verify`。请求体只允许 `{"key_id": <positive integer>}`；未知字段返回 `400 invalid_request`。服务端使用对应加密 NVIDIA Key 真实调用模型 endpoint，成功后生成 UTC `capability_verified_at` 并事务清除 block；失败不写时间、不清 block，调用者不能提交 `verified_at`。ASR/TTS 验证前不能启用，验证后仍需显式 PATCH `{"enabled":true}`。

真实联调见 [docs/NVIDIA真实联调说明.md](docs/NVIDIA真实联调说明.md)。`NVIDIA_ROUTER_LIVE_KEY` 只能从运行环境注入。`SKIP` 不是 PASS，命令成功退出也不能替代逐 case `status=PASS`；CI 负责 race、lint、secret scan、Compose 和 E2E，真实 NVIDIA 仍需显式注入运行时凭证。真实联调会产生 NVIDIA 费用并处理敏感数据，第一轮普通 HTTP 明文风险仍然存在。

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

本次文档任务中的验证结果为：`go run ./cmd/nvidia-router --help`、`go test ./...`、`pnpm --dir web run typecheck`、`pnpm --dir web run build` 和 `git diff --check` 成功；根目录 `pnpm build` 因没有 `package.json` 按预期失败；`go run ./cmd/nvidia-router serve` 因未提供主密钥按预期失败；`db help` 和 `admin --help` 因当前 CLI 未实现子命令帮助按预期失败；`docker compose config` 未执行，因为当前环境没有 `docker` 可执行文件。以上命令只验证帮助、编译、单元测试、前端类型检查、前端构建和文档差异，不能代表真实 NVIDIA 联调或 E2E 通过。真实联调需要运行中的路由器、完成首次改密、有效 NVIDIA Key、模型白名单和逐项 `status=PASS` 证据；详见 [docs/NVIDIA真实联调说明.md](docs/NVIDIA真实联调说明.md)。没有这些证据时，不得宣称 live 或 E2E 已通过。

## 相关文档

- [Linux 单机部署说明](docs/Linux单机部署说明.md)
- [备份与恢复说明](docs/备份与恢复说明.md)
- [API 兼容范围](docs/API兼容范围.md)
- [NVIDIA 真实联调说明](docs/NVIDIA真实联调说明.md)
- [第一轮需求文档](docs/NVIDIA%20API路由器第一轮需求文档.md)
