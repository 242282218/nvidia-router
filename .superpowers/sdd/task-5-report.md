# 任务 5 报告：Compose acceptance 和 CI

## 状态

实现完成；本机 Compose acceptance **BLOCKED**，没有伪造 PASS。

## 实现

- 创建 `scripts/test/compose-acceptance.sh`，使用 `set -Eeuo pipefail`。
- 项目名固定为 `nvr-acceptance-$$`。
- 通过 Compose 自身执行 `config`、`build` 和 `up -d --wait`，使用 Compose 文件中的同一镜像 `nvidia-router:local`。
- 按要求生成并导出随机 `NVIDIA_ROUTER_MASTER_KEY`，不打印主密钥。
- 检查 live 成功，并要求首次改密前 ready 的 HTTP 状态不是 200。
- EXIT trap 在失败时先输出 `docker compose ps` 和最近 100 行服务日志，再执行 `docker compose -p "$project" down -v --remove-orphans`；清理失败不会覆盖原始退出码。
- `.github/workflows/ci.yml` 保留前端 lint/typecheck/test/build、Go test/race/vet、golangci-lint、secret scan、docker build，以及 Chromium 安装、E2E 执行和 artifact 上传；verify job 新增 Compose acceptance 步骤。

## 验证

- 脚本静态检查：必需命令、镜像一致性、项目名、主密钥导出、失败诊断和清理命令均已检查通过。
- `git diff --check`：通过，无空白错误。
- `bash -n scripts/test/compose-acceptance.sh`：BLOCKED。当前 `bash` 是 Windows 的 WSL 启动入口，不能在本机提供可用的 Linux Bash 验证环境。
- Docker Compose acceptance：BLOCKED。当前环境没有 `docker` 或 `docker-compose` 命令，因此未执行 `config/build/up/curl`，也未宣称 acceptance PASS。

## 范围

本任务只修改了 `scripts/test/compose-acceptance.sh`、`.github/workflows/ci.yml` 和本报告；未修改 `docker-compose.yml`。
