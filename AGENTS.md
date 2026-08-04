# 项目约定

## 测试机信息

### 国内测试机（星空代理联调用，必选）

星空代理池的上游 XApi 仅支持国内出口，代理池真实联调必须使用本机；`nvida反代` 只连接代理池的标准 HTTP 正向代理端口。

| 项 | 值 |
|---|---|
| IP | `114.55.25.190` |
| 用户 | `root` |
| 密码 | `REDACTED_CREDENTIAL` |
| 端口 | 22 |
| 用途 | 星空代理池真实联调、NVIDIA 路由器容器部署、真实联调 |
| 环境 | Ubuntu 24.04 (kernel 6.8), Docker 29.1.5, Python 3.12, 无 Go（用 Docker 构建） |

代理池项目默认监听 `:8080`，代理认证使用用户名 `proxy` 和代理池 `PROXY_AUTH_KEY`。两个项目独立部署时，通过 Docker 网络或可达宿主机地址连接。

### 国外测试机（仅通用部署，不用于星空代理）

| 项 | 值 |
|---|---|
| IP | `149.71.241.250` |
| 用户 | `root` |
| 密码 | `REDACTED_CREDENTIAL` |
| 端口 | 22 |
| 用途 | 通用服务部署（已有 grok-clearance、blog-navigation 等容器） |
| 限制 | 星空代理不支持国外 IP 调用（返回 `not within the scope of service`），不得用于星空代理联调 |

## 星空代理池联调要点

- 先在 `D:\PROJECT_ZZZZZZZZZ\星空代理池` 启动代理池，并确认 `http://127.0.0.1:8080/healthz` 正常。
- `nvida反代` 使用 `NVIDIA_ROUTER_XK_PROXY_URL` 连接代理池，使用 `NVIDIA_ROUTER_XK_PROXY_AUTH_KEY` 注入代理认证 Key；不再配置或调用 XApi 提取 URL。
- 代理池的 XApi 订单凭据只注入代理池运行时环境；代理认证 Key 和 NVIDIA Key 只注入各自运行时环境，不写入 Git、日志或文档。
- 真实联调必须确认代理池已健康、认证成功、CONNECT 链路经过代理池，且代理池未就绪时 `nvida反代` 不会静默直连。

## 连接方式

Windows 本机无 sshpass/plink，用 Python paramiko 4.x（已安装）执行远程操作：

```python
import paramiko
client = paramiko.SSHClient()
client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
client.connect("114.55.25.190", username="root", password="REDACTED_CREDENTIAL", timeout=20)
stdin, stdout, stderr = client.exec_command(command, timeout=60)
print(stdout.read().decode())
```
