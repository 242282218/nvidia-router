# Linux 单机部署说明

本文说明第一轮 MVP 在单台 Linux 服务器上的 Docker Compose 部署方式。部署目标是单实例和个人使用。

## 重要安全边界

> **醒目警告：应用本身只提供 HTTP。直接将非回环监听暴露到不可信网络是不安全的；安全部署必须在受信反向代理后使用 HTTPS、Secure Cookie、external origin 和 trusted proxy CIDR。**
>
> 普通 HTTP 会明文传输管理员密码、管理员会话 Cookie、下游 Access Key、API 请求内容和响应内容。`X-Forwarded-Proto` 只有来自 `NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS` 的来源才会被使用。

## 环境要求

- Linux 主机，已安装 Docker Engine 和 Docker Compose 插件。
- 能使用 `docker compose` 命令。
- 主机上准备独立保存的主密钥和 NVIDIA Build API Key。
- 防火墙只开放实际需要的端口。由于当前服务是普通 HTTP，不能用开放端口替代 HTTPS 的安全性。

## 准备项目和主密钥

在目标主机获取仓库并进入仓库根目录，然后生成一次主密钥：

```bash
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

该命令输出 32 字节 Raw URL Base64 主密钥。把它保存到受控的密码管理或密钥管理位置，并确保恢复时仍能取得同一个值。主密钥不在数据库中，丢失或更换都会使已加密的 NVIDIA Key 无法恢复。

创建 `.env`：

```dotenv
NVIDIA_ROUTER_MASTER_KEY=替换为实际主密钥
NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD=替换为至少 12 个字符的随机密码
# 内置采集器：可通过运行时 Secret Provider 注入，也可在管理端加密保存
# NVIDIA_ROUTER_XK_UPSTREAM_URL=可选的运行时回退配置，不写入仓库
# HTTPS 反向代理部署时启用以下配置
# NVIDIA_ROUTER_ADMIN_SECURE_COOKIE=true
# NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN=https://admin.example.com
# NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS=127.0.0.1/32
```

`.env` 只应由受信用户读取；不要将包含真实主密钥或密码的文件提交到 Git。Compose 还会使用以下固定值：容器监听 `0.0.0.0:3756`，数据目录 `/data`，临时目录 `/tmp`，宿主端口默认只绑定 `127.0.0.1:3756`。

内置代理池在 `nvida反代` 单体进程内完成 XApi 采集、代理验证、TTL 管理、轮换和 CONNECT。XApi 完整地址可由运行时 Secret 注入，或由管理员页面提交后使用主密钥加密保存；日志和 API 响应只保留脱敏 endpoint。代理池未就绪时不会回退直连。

## 启动和首次登录

先检查 Compose 配置能展开：

```bash
docker compose config
```

构建并启动单个应用实例：

```bash
docker compose up -d --build
```

查看容器状态和日志：

```bash
docker compose ps
docker compose logs -f app
```

默认 Compose 访问地址为：

```text
http://127.0.0.1:3756
```

管理员用户名固定为 `admin`，首次密码来自 `NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD`。首次登录后必须立即修改密码。服务在首次改密前会拒绝代理 API 和敏感管理操作；`/health/ready` 也不会报告就绪。若要通过 HTTPS 反向代理访问，公开 origin 必须与 `NVIDIA_ROUTER_ADMIN_EXTERNAL_ORIGIN` 精确匹配，并将反向代理来源加入 `NVIDIA_ROUTER_TRUSTED_PROXY_CIDRS`。

改密完成后验证健康状态：

```bash
curl --fail http://SERVER_IP:3756/health/live
curl --fail http://SERVER_IP:3756/health/ready
```

`/health/live` 只表示进程存活；`/health/ready` 还会检查数据库、迁移、主密钥 sentinel、管理员是否完成首次改密以及服务是否正在关闭。

## 数据卷和重启

`docker-compose.yml` 声明 named volume `nvidia-router-data`，挂载到容器 `/data`。SQLite 数据、迁移结果和加密 sentinel 会留在该卷中：

```bash
docker volume ls
docker compose config --volumes
```

重启应用或重建镜像时保持同一个 named volume：

```bash
docker compose restart app
docker compose up -d --build
```

普通停止会保留数据卷：

```bash
docker compose stop app
```

不要在维护或升级时使用 `docker compose down -v`，因为 `-v` 会删除 Compose 管理的 named volume，从而删除数据库。只有在明确销毁整套实例和数据时才允许删除该卷。

应用配置的关闭宽限期为 10 分钟。收到停止信号后，应用停止接收新请求，在宽限期内等待已有请求，随后取消剩余上游请求并关闭数据库。维护窗口仍应预留足够时间，并在停服后再执行正式备份或恢复。

## 常用 CLI

容器镜像的真实入口是 `/usr/local/bin/nvidia-router`，默认命令为 `serve`。查看帮助：

```bash
docker compose run --rm --no-deps app --help
```

重置管理员密码前停止正在运行的应用，再使用同一个 named volume 执行：

```bash
docker compose stop app
docker compose run --rm --no-deps app admin reset-password --password '<new-password>'
docker compose up -d app
```

密码重置会撤销全部管理员会话，不会删除或重新加密 NVIDIA Key。命令行中的密码可能进入 Shell history；生产操作应使用受控的秘密注入方式，并在完成后清理历史记录。

主密钥轮转必须停服执行。旧密钥由 `NVIDIA_ROUTER_MASTER_KEY` 提供，新密钥只由 `NVIDIA_ROUTER_NEW_MASTER_KEY` 提供，二者都不能写入命令参数、日志或仓库：

```bash
docker compose stop app
NVIDIA_ROUTER_MASTER_KEY="$OLD_KEY" \
NVIDIA_ROUTER_NEW_MASTER_KEY="$NEW_KEY" \
docker compose run --rm --no-deps app \
  admin rotate-master-key --new-version 2 --backup /data-backups/router-before-rotation.db
```

成功后把新密钥设为 `NVIDIA_ROUTER_MASTER_KEY`，并在兼容窗口保留旧密钥为 `NVIDIA_ROUTER_LEGACY_MASTER_KEY`。Access Key 和管理员 session 摘要会在认证成功时懒迁移；确认旧摘要清零并完成备份恢复演练后再删除旧密钥。

数据库备份命令及恢复顺序见 [备份与恢复说明](备份与恢复说明.md)。

## 升级检查

升级前先停服并完成数据库备份，再更新镜像：

```bash
docker compose stop app
# 按备份与恢复说明执行 db backup
docker compose up -d --build
curl --fail http://SERVER_IP:3756/health/live
curl --fail http://SERVER_IP:3756/health/ready
```

`ready` 失败时不要继续导入 NVIDIA Key 或执行代理请求。先检查使用的 named volume、原主密钥、迁移错误和 sentinel 错误。

## 真实联调边界

本说明不代表 NVIDIA 真实联调或 E2E 已通过。真实联调必须使用合法且实际可用的 NVIDIA Key、已启用的模型白名单和安全的凭证注入方式，并逐项确认测试输出中的 `status=PASS`。任何必测项为 `SKIP` 都只能记为未完成，不能记为通过。Audio 还需要真实模型和 endpoint 请求成功，并受 `capability_verified_at` 门禁约束。详见 [NVIDIA真实联调说明](NVIDIA真实联调说明.md)。
