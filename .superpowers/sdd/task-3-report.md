# 任务 3 报告：真实联调脚本凭证和能力门禁

## 状态

DONE_WITH_CONCERNS：脚本实现完成；Linux Bash 语法检查和真实 NVIDIA 联调受当前 Windows 环境与用户要求“不真实调用 NVIDIA”限制，未执行。

## 改动

- `scripts/test/live-nvidia.sh`
  - 保留 `set -euo pipefail`、EXIT/INT/TERM trap，并集中清理管理员会话、临时 Access Key、脚本自有 NVIDIA Key 和秘密变量。
  - 登录成功后从进程环境读取 `NVIDIA_ROUTER_LIVE_KEY`，由 Python 构造 JSON 调用导入接口；严格接受 HTTP 201 与 `imported`/`duplicate`，只删除脚本本次导入且标记 owned 的 Key。
  - duplicate 通过 `masked` 从管理端 Key 列表解析唯一启用且 `auth_invalid=false` 的 Key。
  - Audio 模型按 `public_id` 唯一匹配并严格核对 `asr`/`tts` kind；按需调用 `/test`、启用模型、重新 GET 确认，并 export 服务端返回的 `capability_verified_at`。Audio 未配置时跳过能力验证，不生成时间戳。
  - Chat、Responses、Embedding 在创建临时 Access Key 前确认管理端模型唯一且已启用；随后才运行 live Go 测试。
  - Go 原始输出写入 `chmod 600` 的临时日志，仅提取 `case=<name> status=<PASS|FAIL|SKIP> duration=<duration>` 行；成功、失败和信号退出均删除日志。
  - 不打印响应正文、Cookie、NVIDIA Key、Access Key、SSE、音频或模型输出。
- `.superpowers/sdd/task-3-report.md`
  - 本报告。

## 测试命令和结果

- `go test ./...`：PASS。
- `git diff --check -- scripts/test/live-nvidia.sh`：PASS。
- PowerShell 静态断言：PASS，确认严格模式、三类 trap、导入/duplicate 分支、仅 `key_id` 的能力测试请求、启用 PATCH、0600 日志、过滤 case 输出和临时 NVIDIA Key 清理均存在。
- PowerShell 清理顺序断言：PASS，顺序为撤销临时 Access Key、删除自有 NVIDIA Key、注销管理员会话、unset 秘密变量。
- `bash -n scripts/test/live-nvidia.sh`：BLOCKED。当前 `C:\Windows\System32\bash.exe` 是 WSL 启动入口，未安装 Linux 发行版；没有可用的 Git Bash、Cygwin 或 shellcheck。
- 真实 `scripts/test/live-nvidia.sh`：未执行，避免真实调用 NVIDIA；因此没有真实导入、Audio 能力验证、Access Key 撤销和上游 live PASS 证据。

## TDD 说明

这是 Bash 联调脚本任务，仓库没有该脚本的可注入单元测试边界；用户又明确禁止修改 `tests/live`。因此没有新增单元测试，采用静态契约断言、语法检查和仓库 Go 回归测试。Linux Bash 与真实联调仍需在目标环境执行。

## 文件清单

- `scripts/test/live-nvidia.sh`
- `.superpowers/sdd/task-3-report.md`

## 自审和疑虑

- 已确认工作区中原有的 `internal/web/dist/index.html` 与计划文件未被修改，也未修改 `tests/live`。
- 清理失败会覆盖最终退出码为 1；duplicate Key 不会进入删除分支。
- 仍有两个环境验证缺口：当前机器不能执行 `bash -n`，且没有真实 NVIDIA 路由器/凭证可进行端到端联调。未将这两项写成 PASS。
- 当前工具未提供可调用的 subagent/luna 调度接口，本任务由当前代理完成，未伪造子代理结果。
