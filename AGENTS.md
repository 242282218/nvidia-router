# 项目约定

## 强制部署环境

本项目所有部署、真实联调、重启、回滚和线上状态检查，必须在以下国内环境执行：

`D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2`

执行前必须依次读取并遵守：

1. `D:\PROJECT_ZZZZZZZZZ\服务器管理\AGENTS.md`
2. `D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\AGENTS.md`
3. `D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\memory.md`
4. 本项目部署脚本、Compose 文件和部署说明

远程连接必须使用目标目录提供的配置和主机别名：

```powershell
Set-Location 'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2'
ssh -F .\ssh_config_local hangzhou2-2
```

不得使用其他服务器目录或国外环境执行本项目部署和星空代理真实联调。部署前先检查远端服务、端口和数据库状态；每一步改动后必须验证健康检查、关键端口和业务接口。XApi 完整地址、provider 凭据、SSH 私钥及其他密钥只允许通过运行时 Secret 注入，不得写入 Git、规则文件、脚本、`memory.md`、日志或命令输出。

## 镜像与发布版本管理（强制）

- 每次构建和部署必须使用唯一、不可漂移的版本号，禁止生产使用 `latest`、`local`、`dev` 或其他无版本标签。
- 版本号格式统一为 `YYYYMMDD-变更主题-git短SHA`；同日同主题重复发布时追加序号或时间后缀。例如：`20260823-redeploy-cfcaecf`。
- 同一版本号必须同时用于：
  - Git 源码对应的 `HEAD` 短 SHA；
  - Release 目录 `/opt/nvidia-router-releases/<版本号>`；
  - 镜像标签 `nvidia-router:deploy-<版本号>`；
  - 部署记录中的回滚点和验证结果。
- 标准发布必须调用 `scripts/deploy/deploy_remote.py <版本号>`，通过 `git archive HEAD` 打包；构建前核对 `HEAD` 与版本号中的短 SHA 一致。源码、数据库状态或配置发生变化时不得复用旧版本号。
- Compose 生产覆盖文件必须通过 `NVIDIA_ROUTER_IMAGE` 注入版本化镜像；不得回退到 `nvidia-router:local`，不得用未版本化镜像启动或回滚。
- 发布后必须检查容器实际镜像标签、Release 工作目录、Git SHA、数据库备份路径和健康验证结果；任务完成后将版本映射、备份位置、回滚版本和未完成验证项写入 `memory.md`。同一版本的 Release 已存在备份时不得覆盖，必须生成新版本号。

## 测试机信息

### 国内测试机（星空代理联调用，必选）

星空代理池的上游 XApi 仅支持国内出口，代理真实联调必须使用本机；当前 `nvida反代` 单体内置 XApi 采集、验证、池管理和 CONNECT，不连接独立代理池服务。

| 项 | 值 |
|---|---|
| IP | `114.55.25.190` |
| 用户 | `root` |
| 密码 | `REDACTED_CREDENTIAL` |
| 端口 | 22 |
| 用途 | 星空代理池真实联调、NVIDIA 路由器容器部署、真实联调 |
| 环境 | Ubuntu 24.04 (kernel 6.8), Docker 29.1.5, Python 3.12, 无 Go（用 Docker 构建） |

完整 XApi 地址和 provider 凭据只注入 `nvida反代` 运行时环境，不写入 Git、数据库、日志或文档。

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

- 在国内测试机运行单体 `nvida反代`，通过运行时 Secret 注入 `NVIDIA_ROUTER_XK_UPSTREAM_URL`。
- 真实联调必须确认 XApi 采集、代理验证、池内轮换和 CONNECT 链路成功；池未就绪时 `nvida反代` 不会静默直连。

## 记忆沉淀

- 任务完成后将可复用的测试方法、运行命令、前置条件、已知限制和排障结论沉淀到项目根 `memory.md`，后续同类任务先读 `memory.md` 再执行。
- 不记录密钥、URL 凭据、日志原文、临时数据或普通通过结果；无值得沉淀内容时不创建空文件。

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
