# OpenCodeFree 与 Codex 能力闭环设计

日期：2026-08-22

## 目标

为 NVIDIA 与 OpenCodeFree 两个渠道建立可验证的模型能力闭环，并让 Codex CLI 通过 `/v1/responses` 使用 OpenCodeFree 模型。重点覆盖 reasoning、工具调用、长上下文、流式与稳定性，同时保持未知模型的安全默认行为。

## 设计决策

### 1. 能力来源与状态

- NVIDIA 和 OpenCodeFree 均使用代码内静态 capability registry 作为候选发现时的初始推断。
- OpenCodeFree `/models` 只提供模型基础元数据，不依赖其返回不存在的能力字段。
- 未命中 registry 的模型允许保存和启用，但默认不声明 reasoning/tools 能力，管理台显示“待验证”，在探测完成前不自动注入 reasoning。
- 模型测试任务执行基础可用性、reasoning wire format、工具调用探针，并在明确得到结论时回填模型能力。
- 4xx 且语义明确表示能力或参数不支持时才写入“不支持”；429、5xx、网络错误或超时只报告为待定，不覆盖已有能力。
- reasoning 结果区分可见思考、隐藏思考、待验证和不支持；不保存思维正文。

### 2. 默认 reasoning

- 新增 runtime 开关，默认开启，可由管理台关闭。
- 当请求没有 `reasoning_effort`、`reasoning`、`thinking` 三个顶层别名时，对支持 reasoning 的模型自动选择其 profile 最高可用档。
- 显式 reasoning 参数（包括 `none`、`thinking:false`、disabled）始终优先，不被自动默认覆盖。
- `openai` wire format 使用该上游可接受的最高启用档；NVIDIA advisory profile 保持现有“只区分开/关”的诚实语义；`thinking` wire format 使用最高预算并遵守既有输出预算保护。
- 监控记录 reasoning 来源，区分客户端显式请求与 `auto-inject`。

### 3. Responses API

- `/v1/responses` 继续复用现有 Responses↔Chat 协议转换层。
- 为 Responses handler 增加 OpenCodeFree provider 分支，复用现有网关错误映射、瞬时错误重试和流式生命周期约束。
- 支持非流式、流式、函数工具、reasoning、`store:false` 等 Codex 核心路径。
- Codex 烟测实际暴露的必要参数按最小范围补齐；需要服务端状态、后台任务或 hosted tools 的能力仍返回稳定的 `unsupported_responses_feature`，不静默丢弃。

### 4. 测试与结果

- 单元测试覆盖 reasoning 默认注入、显式关闭优先级、能力探测分类、模型回填和 Responses provider 路由。
- 测试任务返回每个模型的紧凑探测摘要，包括基础、reasoning 状态/wire、tools、耗时和错误分类；不保存请求/响应正文。
- 部署后对当前已启用模型执行完整矩阵：reasoning 线格式与自动注入、工具、长上下文、输出预算、重复稳定性和 TTFT/P95。
- 运行一次使用临时凭据的真实 `codex-cli 0.148.0` 安全烟测，覆盖 Responses 流式与函数工具往返；工具操作限定为只读、无网络、无文件修改。

## 数据流

```text
候选发现
  -> 静态 registry 给出初值
  -> 未知模型标记待验证
  -> 管理员手动加入模型测试任务
  -> 基础/reasoning/tools 探针
  -> 明确结论回填模型能力与 reasoning 状态
  -> 请求解析
  -> runtime 开关 + 无显式 reasoning 别名
  -> 自动选择最高 profile 档
  -> Chat 或 Responses provider 路由
  -> 观测显式/自动 reasoning 来源
```

## 部署边界

- 只修改本任务涉及的源文件、迁移、前端状态展示和测试脚本；保留工作树中已有的无关删除。
- 生产部署使用隔离 release 目录和新镜像 tag；复用现有 `.env` 与外部 `nvr-data` 卷，不覆盖密钥。
- 部署前做数据库/数据卷备份；部署后先验证 proxy pool 预热、live/ready，再验证业务流量。
- 任何失败保留旧 release 和回滚坐标，不执行强制 Git 操作或数据删除。
