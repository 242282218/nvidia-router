# Linux 单机部署说明

本文说明第一轮 MVP 在单台 Linux 服务器上的 Docker Compose 部署方式。部署目标是单实例和个人使用。

## 重要安全边界

> **醒目警告：当前第一轮默认使用普通 HTTP，绝不是安全生产部署。禁止把本说明或当前部署方式写成“安全公网部署”，也禁止在不可信网络中直接暴露。**
>
> 普通 HTTP 会明文传输以下内容：
>
> - 管理员密码；
> - 管理员会话 Cookie；
> - 下游 Access Key；
> - API 请求中的提示词及其他请求内容；
> - API 响应内容。
>
> HTTPS、证书和 Caddy、Nginx 等反向代理属于后续迭代，当前第一轮没有提供这些配置。若要面向安全生产或不可信公网使用，当前版本不满足要求。

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
```

`.env` 只应由受信用户读取；不要将包含真实主密钥的文件提交到 Git。Compose 还会使用以下固定值：监听 `0.0.0.0:3756`，数据目录 `/data`，临时目录 `/tmp`，对外映射端口 `3756`。

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

默认访问地址为：

```text
http://SERVER_IP:3756
```

首次管理员账号和密码均为 `admin`，即 `admin/admin`。首次登录后必须立即修改密码。服务在首次改密前会拒绝代理 API 和敏感管理操作；`/health/ready` 也不会报告就绪。

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
