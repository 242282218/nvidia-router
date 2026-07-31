# API 兼容范围

本文记录当前第一轮实现的 OpenAI-compatible API 边界。兼容性表示路由器当前代码能够接受、校验、转换并转发的范围，不代表 NVIDIA 账户对每个模型、endpoint 或参数都一定有权限。所有请求仍受管理员维护的全局模型白名单和 NVIDIA 上游能力限制。

## 状态定义

| 状态 | 含义 |
| --- | --- |
| 支持 | 当前代码有明确路由和转换，满足该项协议主路径；仍需实际模型和上游账户可用 |
| 部分支持 | 只覆盖表中列出的子集，其他字段或能力会明确拒绝，不能按完整 OpenAI API 理解 |
| 明确拒绝 | 当前版本有意不实现，返回结构化错误，不会静默忽略或转发 |

## 路由总表

| 接口 | 状态 | 当前范围 |
| --- | --- | --- |
| `GET /v1/models` | 支持 | 返回管理员启用的全局模型白名单，不是 NVIDIA 原始模型目录的无条件透传 |
| `POST /v1/chat/completions` | 支持 | 普通和 SSE 流式 Chat；模型、消息、工具和能力按白名单校验 |
| `POST /v1/responses` | 部分支持 | 文本、函数工具、普通响应和流式事件转换到 Chat；不支持项见下表 |
| `POST /v1/embeddings` | 支持 | 转发合法 Embedding 请求；必须有已启用的 `embedding` 模型 |
| `POST /v1/audio/transcriptions` | 部分支持 | multipart 文件上传和可映射字段；必须通过 ASR 真实能力验证门禁 |
| `POST /v1/audio/speech` | 部分支持 | 文本、模型、voice、输出格式等可映射字段；必须通过 TTS 真实能力验证门禁 |
| 其他 `/v1/*` | 明确拒绝 | 返回 HTTP `501`、`not_implemented`；不会原样转发到 NVIDIA |

## Chat Completions

### 支持

- `messages` 中的 `system`、`user`、`assistant`、`tool` 角色。
- `stream: false` 普通响应和 `stream: true` SSE 响应。
- 文本消息。
- 函数工具（`tools[].type=function`）及 `tool_choice` 的 `none`、`auto`、`required` 和函数选择。
- 已在模型目录标记支持的视觉输入。
- 已标记为 OpenAI wire format 的 reasoning 参数；`reasoning_effort`、兼容的 `reasoning`/`thinking` 会归一化。
- 未知但合法的 JSON 字段按代码的透传策略保留；与 NVIDIA 明确不兼容的字段可能被修正、拒绝或删除。

### 图片输入

Chat 图片只支持消息 `content` 数组中的 `image_url` 对象，且当前允许的格式为：

- `https://` 图片 URL；代码拒绝 `http://`、带凭证或没有主机的远程 URL，且该 URL 必须由 NVIDIA endpoint 实际支持；
- `data:image/png;base64,...`；
- `data:image/jpeg;base64,...`；
- `data:image/webp;base64,...`；
- `data:image/gif;base64,...`。

Base64 必须是合法的 `data:` 图片 URL，解码后不超过 20 MiB。其他 MIME 类型、损坏的 Base64、非图片 data URL、超限内容或不支持该能力的模型会被拒绝。图片能力要求所选 Chat 模型在模型目录中标记 `supports_vision`。

## Responses

Responses 通过受控转换映射到 Chat，而不是把完整 Responses 请求原样透传。当前支持：

- 字符串 `input`；
- 文本消息数组及 `system`、`user`、`assistant` 角色；
- `instructions` 转换为前置 system 消息；
- 文本 content parts；
- 函数工具、函数调用和函数调用输出；
- `stream: false` 普通响应转换；
- `stream: true` SSE 事件转换；
- 在模型标记支持时使用 reasoning 和 `max_output_tokens` 的必要映射。

以下 Responses 能力明确不支持，会返回结构化 `400`，不会静默忽略：

| 不支持项 | 说明 |
| --- | --- |
| `store: true` | 不保存响应；发送 `store: false` 以表达不要求持久化 |
| `previous_response_id` | 不支持有状态响应恢复 |
| `conversation` | 不支持服务端会话状态 |
| `metadata` | 不持久化响应元数据 |
| `prompt_cache_key` | 不支持提示词缓存键 |
| `background: true` | 不支持后台异步响应 |
| `include` | 不支持 OpenAI 托管工具或托管内容包含项 |
| 非 `function` 工具 | 只支持函数工具，不支持 OpenAI 内置工具、远程 MCP 等 |
| 图片输入 | 当前 Responses 到 Chat 转换不支持 image/file content |
| 文件输入 | 不支持 `file` input 或 file content |
| 其他未知 input/content 类型 | 明确返回 unsupported 错误 |

Responses 的有状态能力、`previous_response_id`、background 模式、OpenAI 内置工具和远程 MCP 也属于第一轮需求明确排除范围。

## Embedding

`POST /v1/embeddings` 支持当前 NVIDIA 可映射的请求和 OpenAI-compatible 响应形状。请求必须使用管理员启用且 `kind=embedding` 的模型。具体模型是否可用、是否有配额和权限，需要实际 NVIDIA 账户验证；只出现在模型列表中不能证明可用。

## Audio

### ASR：`/v1/audio/transcriptions`

部分支持 multipart 请求，要求非空 `file` 和 `model`，并受 25 MiB 请求限制。可映射字段包括 `language`、`prompt`、`response_format` 和 `timestamp_granularity`。上传文件只在请求生命周期内保存在内存/临时资源中，不写入数据库或日志。

### TTS：`/v1/audio/speech`

部分支持非空 `model`、`input`，以及可映射的 `voice` 和 `response_format` 字段。输入文本和生成音频不写入数据库或日志；已经向客户端输出音频字节后不能换 Key 重放。

### 真实验证门禁

Audio 不能仅凭 `/v1/models` 列表、模型名称或 Mock 测试启用。只有以下条件同时满足，才能认为能力已验证：

1. 使用真实 NVIDIA 账户、真实模型和真实 endpoint；
2. ASR 请求实际上传非空音频并返回 HTTP `200`，且 JSON 中 `text` 或 `transcript` 至少一个非空；
3. TTS 请求实际返回 HTTP `200`，且 Content-Type 为 `audio/*` 或 `application/octet-stream`，响应音频非空；
4. 成功请求完成后，使用真实成功时间设置对应模型的 `capability_verified_at`，随后才允许启用 ASR/TTS 模型。

Audio 验证由管理 API 受审计完成：`POST /admin/api/models/<id>/test`，兼容别名为 `/admin/api/models/<id>/verify`。请求体严格为 `{"key_id": <positive integer>}`；未知字段（包括 `verified_at` 和 `capability_verified_at`）返回 `400 invalid_request`。服务端使用对应加密 NVIDIA Key 真实调用模型 endpoint，成功后生成 UTC `capability_verified_at`，并在事务中清除该 Key 与模型的 block；失败不写入时间、不清 block，调用者不能提交验证时间。

ASR/TTS 在验证时间为空时不能启用。验证成功后仍必须显式 `PATCH /admin/api/models/<id>`，提交 `{"enabled":true}`；验证接口不会自动启用模型。没有真实模型、endpoint、权限或音频素材时，Audio case 应明确记为 `SKIP`，`SKIP` 不是 `PASS`。

## 认证和健康接口

- 管理 API 使用管理员会话 Cookie；首次 `admin/admin` 登录后必须改密。
- 下游 `/v1/*` 使用 Bearer Access Key。
- `/health/live` 是最小存活检查，不泄露 Key、模型或统计信息。
- `/health/ready` 检查数据库、迁移、主密钥 sentinel、首次改密和关闭状态。

普通 HTTP 部署会明文传输认证信息、请求和响应；第一轮不是安全生产部署，禁止描述为安全公网部署。HTTPS 属于后续迭代。

## 未知路径的行为

所有未注册的 `/v1/*` 路径，包括当前没有实现的图片、文件、Batch、Assistants 等接口，统一返回：

- HTTP 状态：`501 Not Implemented`；
- 错误码：`not_implemented`；
- 不联系 NVIDIA，不暴露内部地址、密钥或堆栈。

该行为不等于“兼容”：客户端必须只调用上表列出的明确路径和字段范围。
