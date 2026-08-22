# 文档索引

## 快速入口

- 部署：[`Linux单机部署说明.md`](Linux单机部署说明.md) · [`公网双服务部署说明.md`](公网双服务部署说明.md) · [`备份与恢复说明.md`](备份与恢复说明.md) · [`自动备份方案.md`](自动备份方案.md)
- 安全：[`安全加固检查清单.md`](安全加固检查清单.md) · [`多实例部署注意事项.md`](多实例部署注意事项.md)
- API：[`API兼容范围.md`](API兼容范围.md) · [`NVIDIA API路由器第一轮需求文档.md`](NVIDIA%20API路由器第一轮需求文档.md)
- 代理池：[`星空代理池融合说明.md`](星空代理池融合说明.md) · [`星空代理实现验收记录.md`](星空代理实现验收记录.md) · [`星空代理真实联调说明.md`](星空代理真实联调说明.md)
- 测试：[`项目测试方案.md`](项目测试方案.md) · [`NVIDIA真实联调说明.md`](NVIDIA真实联调说明.md) · [`测试机信息.md`](测试机信息.md)
- 前端：[`前端对比度配对表.md`](前端对比度配对表.md)（Light/Dark 双主题 WCAG 配对登记，`scripts/calc_contrast.py` 实算）
- 代码调研：[`代码全量调研与优化建议.md`](代码全量调研与优化建议.md)（全量分析 + 对标优秀项目 + 语言重写评估 + 分批次优化清单）
- 顶层：[`../README.md`](../README.md)（快速开始与配置表） · [`../AGENTS.md`](../AGENTS.md)（项目约定） · [`../memory.md`](../memory.md)（可复用记忆）

## 有效性说明

`docs/*.md` 为当前有效文档；`docs/archive/*.md` 为历史归档，仅追溯；`docs/plans/` 为阶段性 plan/分析报告（含最新的 `2026-08-22-vibe场景模型评测报告.md` 与 `2026-08-20-性能优化调研与实施.md`）。

## 测试脚本索引

- 可复用（tracked）：`scripts/test/live-nvidia.sh` · `scripts/test/live-xk-proxy.sh` · `scripts/test/compose-acceptance.sh` · `scripts/test/proxy-pool-integration-test.sh` · `scripts/test/run-deepseek-stability.sh` · `scripts/test/verify_remote.sh`
- 部署辅助（tracked）：`scripts/deploy/` · `scripts/check-*.sh` · `scripts/e2e/`
- 诊断归档（ignored）：`scripts/test/_archive/`（一次性 `remote_*.py`/`round5_*.py`/`glm52_*.py` 等，含运行时探针，不进入 Git）
