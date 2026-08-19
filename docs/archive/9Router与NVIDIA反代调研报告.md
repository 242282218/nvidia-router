# 9Router 与 NVIDIA NIM 反代调研报告

**调研日期：** 2026-07-29<br>
**核验版本：** `decolua/9router` `79918c7830695bbca4a45c9fea4a42c3e9fd73d1`（`master`）

## 一、结论摘要

9Router 是开源项目，不是只能通过远程服务使用的黑盒。官方仓库为 [decolua/9router](https://github.com/decolua/9router)，仓库公开、许可证为 MIT；官网为 [9router.com](https://9router.com/)。GitHub 元数据显示项目公开且当前仍有提交活动，但 issue 数量较多，生产使用前应固定版本并自行做回归测试。

9Router 的 NVIDIA NIM 逻辑可以合法地从公开源码提取和重写。核心不是复杂的专有协议，而是：

1. 将 NVIDIA 注册为 OpenAI-compatible provider。
2. 把请求转发到 `https://integrate.api.nvidia.com/v1/chat/completions`。
3. 使用 `Authorization: Bearer <NVIDIA_API_KEY>`。
4. 用 `/v1/models` 做密钥验证和模型发现。
5. 根据 NVIDIA 能力表处理模型名、上下文、视觉、reasoning 和流式请求。
6. 通过统一 SSE/响应转换层向本地客户端暴露 `/v1`。

如果目标是“尽快得到可用的 NVIDIA 反代”，优先评估 LiteLLM；如果目标是“多用户、多密钥、渠道管理和控制台”，评估 New API；如果目标是“云原生、高并发网关”，评估 Higress。9Router 源码最适合作为 NVIDIA provider 适配和客户端兼容细节的参考实现。

## 二、9Router 开源状态

| 项目 | 核验结果 |
|---|---|
| 官方仓库 | [github.com/decolua/9router](https://github.com/decolua/9router) |
| 官方网站 | [9router.com](https://9router.com/) |
| 可见性 | Public |
| 默认分支 | `master` |
| 许可证 | MIT，见 [LICENSE](https://github.com/decolua/9router/blob/master/LICENSE) |
| npm CLI | [npmjs.com/package/9router](https://www.npmjs.com/package/9router) |
| 容器 | [Docker Hub](https://hub.docker.com/r/decolua/9router)、GHCR |
| 核验提交 | `79918c7830695bbca4a45c9fea4a42c3e9fd73d1` |

仓库包含 Web 应用、CLI、provider registry、协议转换、认证、配额、回退、日志、统计、SSE、测试和 Docker 部署文件，属于完整网关源码，而不是只有示例配置。MIT 允许修改和再发布，但分发时必须保留版权和许可证声明；不能把 NVIDIA API key、用户数据或 9Router 的运行数据打包进派生项目。

## 三、NVIDIA 反代逻辑源码定位

### 3.1 Provider 注册表

源码：[open-sse/providers/registry/nvidia.js](https://github.com/decolua/9router/blob/master/open-sse/providers/registry/nvidia.js)

已核验的关键配置：

```text
provider id/alias: nvidia
display name: NVIDIA NIM
chat:   https://integrate.api.nvidia.com/v1/chat/completions
models: https://integrate.api.nvidia.com/v1/models
tts:    https://integrate.api.nvidia.com/v1/audio/speech
embed:  https://integrate.api.nvidia.com/v1/embeddings
auth:   Bearer API key
```

注册表同时包含模型静态目录和服务类型。当前版本中可见 NVIDIA、MiniMax、GLM、DeepSeek、Kimi 等模型条目，以及 `nvidia/nv-embedqa-e5-v5`、Parakeet ASR 和 FastPitch/Tacotron2 TTS 配置。静态目录不是 NVIDIA 账户实际可用模型的唯一来源，运行时仍应以 `/v1/models` 和上游返回结果为准。

### 3.2 能力与请求适配

源码：[open-sse/providers/capabilities.js](https://github.com/decolua/9router/blob/master/open-sse/providers/capabilities.js)

源码明确写出 NVIDIA NIM 为 OpenAI-compatible，并对部分 MiniMax/GLM 模型拒绝原生 `thinking` 字段，改用 OpenAI 风格的 reasoning 表达。这说明适配重点在统一 translator 之后的 provider-specific wire adaptation，而不是简单拼接 URL。

需要重点保留的能力维度：

- 模型 ID 与别名映射。
- 上下文窗口和最大输出限制。
- 视觉输入能力。
- tool calling、structured output 和 reasoning 参数兼容性。
- stream 与非 stream 使用同一个 chat completions URL。

### 3.3 模型发现和密钥验证

源码：[src/app/api/providers/[id]/models/route.js](https://github.com/decolua/9router/blob/master/src/app/api/providers/%5Bid%5D/models/route.js)、[testUtils.js](https://github.com/decolua/9router/blob/master/src/app/api/providers/%5Bid%5D/test/testUtils.js)

NVIDIA 模型发现通过 `GET https://integrate.api.nvidia.com/v1/models` 完成，使用 Bearer 认证；连接测试复用同一路径。无密钥返回 401，上游非 2xx 时保留 HTTP 状态并返回简化错误对象。解析器兼容 `data`、`models`、`results` 等常见列表形状。

### 3.4 流式与辅助模态

源码和快照：[golden-url-header.test.js.snap](https://github.com/decolua/9router/blob/master/tests/translator/__snapshots__/golden-url-header.test.js.snap)

快照证明 NVIDIA stream/non-stream 请求使用相同的 `/v1/chat/completions` URL。SSE 处理由共享转换层负责，不能仅复制 provider registry 就认为流式兼容已经完成。CHANGELOG 还记录了非 JSON SSE 行、重复 `[DONE]`、stream pipe/stall 等问题修复，说明这些边界需要纳入测试。

embedding 被归入 OpenAI-compatible provider；STT/TTS 则有 NVIDIA 专用 multipart/响应格式处理。若只实现聊天反代，可以先不实现辅助模态，但应在 API 路由层明确返回“不支持”，不要静默转发错误请求。

## 四、可提取的最小逻辑

建议将实现拆成四层：

```text
客户端 OpenAI SDK
        |
        v
本地鉴权、模型别名、限流、审计
        |
        v
统一请求模型 -> NVIDIA provider adapter
        |
        +--> GET  /v1/models
        +--> POST /v1/chat/completions
        +--> POST /v1/embeddings（可选）
        |
        v
SSE/JSON 响应归一化与错误映射
```

第一阶段只需实现 `/v1/models`、`/v1/chat/completions`、Bearer 上游认证、模型映射和 SSE。第二阶段再加入多 key 轮询、失败回退、配额、用量统计和管理面板。不要直接复制整个 9Router 应用，否则会把 Next.js、数据库、OAuth、MITM 和其他 provider 的耦合一起带入。

## 五、同类开源项目比较

| 项目 | 许可证 | NVIDIA 支持 | 统一 API/流式 | Key pool/路由 | 控制台 | 适合场景 |
|---|---|---|---|---|---|---|
| [LiteLLM](https://github.com/BerriAI/litellm) | 需以仓库当前 LICENSE 为准，企业能力有商业边界 | 原生 NVIDIA NIM provider | 完整 | virtual key、fallback、负载均衡 | 有 | 最快获得完整通用网关 |
| [New API](https://github.com/QuantumNous/new-api) | AGPLv3 | 需通过自定义渠道或 adapter 核验 | OpenAI/Claude/Gemini 等 | 渠道权重、重试、令牌管理 | 有 | API 中转站、多用户管理 |
| [One API](https://github.com/songquanpeng/one-api) | MIT | 未确认专用 NIM channel | OpenAI 兼容、stream | 多渠道、重试、负载均衡 | 有 | 轻量中转站，适合二次开发 |
| [Bifrost](https://github.com/maximhq/bifrost) | Apache-2.0 | 通用 provider 扩展，NIM 需核验 | OpenAI 兼容、stream | fallback、load balancing、virtual keys | 有 | Go 生态和可观测性 |
| [Higress](https://github.com/higress-group/higress) | Apache-2.0 | 通用 AI proxy/plugin，非 NIM 专用证据 | SSE、限流、缓存 | 需插件或外部控制面 | 有 | Kubernetes、高并发 |
| [Envoy AI Gateway](https://github.com/envoyproxy/ai-gateway) | Apache-2.0 | 当前无明确 NIM 专用 provider | 网关级流式 | 认证、路由、限流 | 无传统中转站面板 | 基础设施级网关 |
| [Jontte6/nim-to-openai-proxy](https://github.com/Jontte6/nim-to-openai-proxy) | MIT | 直接针对 NIM | OpenAI、stream | 简单 fallback | 无 | 学习最小转换逻辑；已停止维护 |
| [miztertea/nim-proxy](https://github.com/miztertea/nim-proxy) | MIT | 直接针对 NIM | OpenAI 兼容 | 主要是限流，完整 key pool 待核验 | 无 | 轻量 Rust 原型 |

“支持自定义 OpenAI endpoint”不等于“原生支持 NVIDIA NIM”。选型时必须单独验证模型字段、tool calling、reasoning、SSE、错误格式和 API key 轮换。

## 六、推荐方案

### 推荐一：直接使用 LiteLLM

适合同时代理 NVIDIA NIM、OpenAI、Anthropic 等上游，并需要 fallback、负载均衡、virtual keys、用量统计的情况。优点是 NVIDIA adapter 和通用网关能力成熟；代价是代码和配置体系较大，许可证及商业功能边界需要审查。

### 推荐二：以 New API/One API 为控制面，新增 NVIDIA channel

适合多用户 API 中转服务。复用渠道、令牌、权重、禁用、重试和面板，只新增 NVIDIA 的 URL、Bearer 认证、模型映射和 SSE 兼容测试。New API 的 AGPLv3 义务要在部署和分发前确认；若倾向 MIT，可优先评估 One API，但需接受其维护活跃度和 NIM 原生能力不确定性。

### 推荐三：独立实现最小 NVIDIA adapter

适合当前工作区要做专用 `nvida反代` 服务。建议使用 Go，保持服务边界简单：配置文件定义上游 key 和模型，HTTP client 负责请求转发，显式处理 SSE，配合 `httptest` 做协议回归。可参考 9Router 的 registry/capabilities 和 `nim-to-openai-proxy` 的最小路径，但不直接采用已停止维护项目的生产代码。

## 七、落地顺序和验收标准

1. 固定上游 base URL、API key 注入方式和允许模型列表。
2. 实现 `GET /v1/models`，验证 401、上游 401、超时和空列表。
3. 实现非流式 `POST /v1/chat/completions`，覆盖 system、tools、图片、reasoning 参数。
4. 实现流式 SSE，逐行转发合法事件，处理非 JSON 行、断连、重复 `[DONE]` 和客户端取消。
5. 增加多 key 轮询与仅对可重试错误的 fallback，避免对 4xx 无限重试。
6. 增加模型别名、请求日志脱敏、上游耗时、token 用量和健康状态。
7. 用 OpenAI SDK、curl 和至少一个真实下游客户端做集成验证。

最低验收标准：普通/流式聊天均能使用 OpenAI SDK；上游错误不会泄露 API key；客户端取消能释放上游连接；模型发现与实际权限一致；所有 key 不写入日志、仓库或镜像。

## 八、风险与边界

- 9Router 的 MIT 许可证允许复用代码，但必须保留许可证和版权声明，并审查其依赖许可证。
- NVIDIA API 的使用仍受 NVIDIA 服务条款、账户配额和模型许可约束；开源代理不等于获得模型或 API 的再分发权。
- 公开源码只能证明实现方式，不能证明某个 NVIDIA 账号具备所有模型权限，也不能保证当前模型目录永久稳定。
- 不应通过代理绕过上游认证、区域限制、配额或访问控制；密钥应由使用者合法提供并在服务端安全保存。
- 9Router 仓库 issue 较多。直接部署前要锁定 commit、审查安全公告、关闭不需要的管理入口并设置本地鉴权。

## 九、参考资料

- [9Router 官方仓库](https://github.com/decolua/9router)
- [9Router NVIDIA provider registry](https://github.com/decolua/9router/blob/master/open-sse/providers/registry/nvidia.js)
- [9Router provider capabilities](https://github.com/decolua/9router/blob/master/open-sse/providers/capabilities.js)
- [9Router models route](https://github.com/decolua/9router/blob/master/src/app/api/providers/%5Bid%5D/models/route.js)
- [NVIDIA NIM LLM APIs](https://docs.api.nvidia.com/nim/reference/llm-apis)
- [LiteLLM NVIDIA NIM provider](https://docs.litellm.ai/docs/providers/nvidia_nim)
- [New API](https://github.com/QuantumNous/new-api)
- [One API](https://github.com/songquanpeng/one-api)
- [Higress](https://github.com/higress-group/higress)
- [Envoy AI Gateway](https://github.com/envoyproxy/ai-gateway)
