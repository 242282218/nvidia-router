# Task 4 文档同步报告

## 状态

BLOCKED：文档已同步，脚本生命周期仍未满足收尾计划，不能宣称第一轮文档与联调链路整体 PASS。

## 已完成

- `README.md`、`docs/NVIDIA真实联调说明.md`、`docs/API兼容范围.md` 已删除“没有公开验证入口/没有公开管理入口”等过时描述。
- 已明确 `POST /admin/api/models/<id>/test` 和兼容别名 `/verify`。
- 已明确请求体只能是 `{"key_id": <positive integer>}`，未知字段返回 `400 invalid_request`，调用者不能提交 `verified_at` 或 `capability_verified_at`。
- 已明确服务端使用对应加密 NVIDIA Key 真实调用模型 endpoint；成功生成 UTC `capability_verified_at` 并事务清 block，失败不写时间、不清 block。
- 已明确 ASR/TTS 验证前不可启用，验证后仍需显式 PATCH `{"enabled":true}`。
- 已同步 PASS/FAIL/BLOCKED/SKIP 规则、CI 的 race/lint/secret scan/Compose/E2E 职责、真实 NVIDIA 运行时凭证要求，以及第一轮 HTTP 明文、费用和敏感数据警告。

## 未完成与根因

当前 `scripts/test/live-nvidia.sh` 的真实实现仍然：

- 只检查已有可用 NVIDIA Key，不导入或识别 `NVIDIA_ROUTER_LIVE_KEY` 对应的临时 Key；
- 不调用 `/admin/api/models/<id>/test` 或 `/verify`，不按需 PATCH 启用 Audio；
- 清理只撤销临时 Access Key 和注销会话，不删除脚本自有的新 NVIDIA Key；
- 直接输出 `go test -tags=live ./tests/live -v` 原始日志，未做到只输出 `case/status/duration`。

本任务明确只允许修改三份文档和本报告，因此没有修改脚本。要解除 BLOCKED，必须在允许修改脚本的任务中补齐上述实现并重新运行 live 门禁。

## 验证

- `rg`：目标文档中不再存在“没有公开验证入口/没有公开管理入口”旧说法；已确认新 API、请求体、状态规则和凭证要求存在。
- `git diff --check`：通过。
- 本次未运行真实 NVIDIA 联调；当前环境也没有可用于该联调的运行时凭证和服务证据。
