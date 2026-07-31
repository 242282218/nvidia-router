# 项目约定

## 测试机信息

### 国内测试机（星空代理联调用，必选）

星空代理（xkdaili）仅支持国内出口 IP 调用提取 API，联调时必须使用本机。

| 项 | 值 |
|---|---|
| IP | `114.55.25.190` |
| 用户 | `root` |
| 密码 | `REDACTED_CREDENTIAL` |
| 端口 | 22 |
| 用途 | 星空代理提取 API 调用、NVIDIA 路由器容器部署、真实联调 |
| 环境 | Ubuntu 24.04 (kernel 6.8), Docker 29.1.5, Python 3.12, 无 Go（用 Docker 构建） |

已确认：从本机调用 `api2.xkdaili.com/tools/XApi.ashx` 直接返回代理 IP（如 `123.182.209.193:40034`），无需白名单配置。

### 国外测试机（仅通用部署，不用于星空代理）

| 项 | 值 |
|---|---|
| IP | `149.71.241.250` |
| 用户 | `root` |
| 密码 | `REDACTED_CREDENTIAL` |
| 端口 | 22 |
| 用途 | 通用服务部署（已有 grok-clearance、blog-navigation 等容器） |
| 限制 | 星空代理不支持国外 IP 调用（返回 `not within the scope of service`），不得用于星空代理联调 |

## 星空代理联调要点

- 星空代理提取 API：`http://api2.xkdaili.com/tools/XApi.ashx?apikey=<订单号>&qty=1&format=txt&split=2&sign=<sign>`，成功返回单行 `IP:PORT`。
- 必须从国内机器（114.55.25.190）调用提取 API。
- 白名单管理接口存在但需要登录态（`/tools/submit_ajax.ashx?action=add_whiteip_api|delete_whiteip_api`），本订单当前无需白名单。
- 联调脚本：`scripts/test/live-xkproxy.sh`（需 `NVIDIA_ROUTER_XK_PROXY_LIVE_ENABLE=1` 及运行时凭据）。
- 凭据（apikey/sign/NVIDIA Key）仅运行时环境变量注入，不写入 Git、日志或文档。

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
