# 测试脚本说明

## 可复用脚本（tracked，提交到 Git）

| 脚本 | 用途 | 运行时机 |
|---|---|---|
| `live-nvidia.sh` | NVIDIA 真实端点全量联调（chat/responses/embed/audio/speech + 代理链路），需运行时 Secret | 国内测试机 `hangzhou2-2`，每次发布后 |
| `live-xk-proxy.sh` | 星空代理池内置模式静态检查（`bash -n` + 自测） | 本地/CI，无需 Secret |
| `compose-acceptance.sh` | Compose 验收（docker compose config + 构建 + 健康检查） | CI / 本地预检 |
| `proxy-pool-integration-test.sh` | 代理池集成验证（采集/轮换/隔离轻量探针） | 本地 |
| `run-deepseek-stability.sh` | DeepSeek 稳定性压测（串行/并发，支持 `ROUTER_BASE/ACCESS_KEY`） | 手动稳定性验证 |
| `verify_remote.sh` + `verify_remote_run.py` | 远端冒烟（login → runtime summary → proxy status → access key → chat） | 部署后冒烟 |
| `ssh_wait_healthy.py` | 等待容器 healthy（轮询 `docker inspect`） | 部署辅助 |

> 所有真实联调脚本只通过运行时 Secret 注入 `NVIDIA_ROUTER_XK_UPSTREAM_URL` / `NVIDIA_ROUTER_LIVE_KEY` / `NVIDIA_ROUTER_ADMIN_PASSWORD`，不落盘、不回显。

## 诊断归档（ignored，`_archive/`）

`_archive/` 下为一次性诊断/复现脚本（`remote_*.py` / `round5_*.py` / `glm52_*.py` / `model_matrix.py` / `redeploy.py` 等），含运行时探针与硬编码 release 路径，仅供追溯，不进入 Git（见 `.gitignore` 的 `scripts/test/_archive/` 条目）。需要时可拷贝到 `D:\tmp\temp\` 执行，用后删除。

## 已清理

根目录临时产物（`*.log` / `*.exe` / `.tmp-*` / `tmp/serve*.log` / `tmp/run.bat`）已按 `.gitignore` 清理；`data/` 与 `key/` 保持 ignored，不提交。

## 新增脚本约定

- 可复用脚本放入 `scripts/test/` 并在 `docs/项目测试方案.md` 的执行框架中登记。
- 一次性诊断脚本直接在 `D:\tmp\temp\` 编写，或放入 `_archive/`，不在主目录堆积。
- 含 Secret 的脚本必须 `umask 077` + `mktemp` + `chmod 600`，且通过 `python -c` 解析响应时不得打印 Key/URL。
