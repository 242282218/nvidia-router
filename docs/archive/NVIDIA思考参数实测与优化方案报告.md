# NVIDIA 思考参数实测与优化方案报告

- **日期**：2026-08-17
- **状态**：目标机实测完成，报告已按实测修订；本次未修改业务代码
- **目标发布**：`nvidia-router:deploy-20260817-redeploy-9b4a260`
- **内置代理池**：`star-proxy-pool:deploy-20260811`
- **涉及模型**：`z-ai/glm-5.2`、`deepseek-v4-flash`（映射到 `deepseek-ai/deepseek-v4-flash-0731`）、`minimaxai/minimax-m3`、`moonshotai/kimi-k2.6`
- **上游**：NVIDIA 托管 API（OpenAI 兼容接口）

> 本报告中的 reasoning 内容只在测试进程内统计长度和字段存在性，不保存、不回显原文。管理员密码、NVIDIA Key、Access Key、XApi 地址和代理凭据均未写入本文件。

## 结论先行

1. **GLM-5.2 并不是“完全不会思考”。** 在 `hangzhou2-2` 经路由器实测，默认请求的 reasoning 长度为 0；显式传 `reasoning_effort=high`、`thinking.type=enabled` 或流式 high 时均收到 reasoning。客户端没有传开关，或把 reasoning 当成最终文本读取，是“看不到思考”的首要解释。
2. **Responses 的 `output_text` 不等于完整输出。** 实测 `reasoning.effort=high` 且输出预算足够时，响应同时包含 `output` 类型 `reasoning` 和 `message`；reasoning 在前者的 `summary` 中，最终答案在后者及 `output_text` 中。只读取 `output_text` 会主动隐藏 reasoning。
3. **路由器当前代码没有证据表明会删除 Chat 的 `chat_template_kwargs`。** Chat 请求先只替换模型名，再把请求体交给 NVIDIA 客户端；Responses 会把 `reasoning.effort` 映射为 `reasoning_effort`，并保留 `thinking`。本地单测也验证了原生 thinking 字段透传。
4. **DeepSeek V4 Flash 在本次目标机窗口内不可用，但“加 kwargs 已修复”没有被目标机复现。** 代理模式下无 kwargs 90 秒无首字节；显式 kwargs 240 秒无首字节；临时关闭代理池直连后，显式 kwargs 仍 240 秒无首字节。当前证据支持“上游/账号/排队或模型服务状态异常”，不能把原因简化为路由器丢字段，也不能把旧的 206 秒成功样本当成稳定保证。
5. **当前不建议立即对所有模型自动注入 `chat_template_kwargs`。** 先保持透明透传，补充请求边界和 reasoning 可观测性；只有在 NVIDIA 服务恢复后用同一发布、同一账号、直连与代理双路径复现成功，才考虑仅对 DeepSeek 做模型级注入。

## 1. 问题范围与判定口径

用户反馈的“模型没有思考”可能对应四种不同问题：

| 现象 | 需要检查的层 | 不能直接推出的结论 |
|---|---|---|
| HTTP 200，但 reasoning 为空 | 请求是否显式开启、模型默认行为 | 不能直接说路由器吞了参数 |
| HTTP 200，reasoning 存在但界面看不到 | Chat/Responses 字段读取方式 | 不能用 `output_text` 代表完整 Responses 输出 |
| 流式只有最终文本 | SSE delta 字段、客户端事件过滤 | 不能只看最后一条 content delta |
| 长时间无首字节 | 上游排队、代理 CONNECT、模型超时、请求体兼容性 | 不能把 timeout 自动归因于 reasoning 参数 |

本报告将“已收到 reasoning”“已收到最终答案”“首字节”“请求成功”分别统计，不用一个“有/无思考”字段掩盖链路差异。

## 2. 实测环境与安全边界

### 2.1 目标机状态

本次真实联调严格在 `D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2` 对应的 `hangzhou2-2` 执行，使用该目录的 SSH 配置和主机别名。

测试前后均确认：

| 项目 | 结果 |
|---|---|
| 路由器 `/health/live` | HTTP 200 |
| 路由器 `/health/ready` | HTTP 200 |
| 代理池 `/healthz` | HTTP 200 |
| 路由器容器 | healthy，重启次数 0 |
| 代理池容器 | healthy，重启次数 0 |
| 测试前代理池 | `mode=built-in`，`enabled=true`，`healthy_size=32`，`total_size=32` |
| 测试后代理池 | 已恢复 `enabled=true`，健康检查通过 |

测试期间临时关闭代理池只用于一次隔离 A/B，结束后恢复；没有重启服务、改模型目录、改上游 Key 或改数据库结构。

### 2.2 测试方法

- Chat：非流式和流式均统计 HTTP 状态、总耗时、首数据时间、`reasoning_content` 长度、最终 content 长度、字段名、SSE `[DONE]`。
- Responses：本轮目标机重点统计非流式 `output` item 类型、reasoning summary 长度、message 内容长度和 `output_text` 长度；流式转换行为由本地 mock/单测覆盖，不把未完成的目标机流式请求计为 PASS。
- DeepSeek：使用公开别名 `deepseek-v4-flash`，分别测试无 `chat_template_kwargs`、显式 kwargs，以及关闭代理池后的显式 kwargs。
- 只记录元数据，不写原始响应。每轮使用临时 Access Key，测试结束撤销；同名临时记录在收尾时确认全部为已撤销状态。
- 测试请求通过当前生产发布的真实路由器执行，不把本地 mock 测试冒充线上结果。

## 3. 路由器代码审查

### 3.1 Chat 请求体

`internal/httpapi/v1/chat.go:61-83` 的处理顺序是解析请求、按模型 ID 解析 Chat 模型、调用 `MarshalFor`，然后把结果交给上游客户端。当前解析模型时只传 `Kind: chat`，没有按模型能力拦截 reasoning。

`internal/protocol/chat/request.go:73-83` 的 `MarshalFor` 只克隆原始 JSON 字段并替换 `model`：

```go
fields := cloneFields(r.fields)
fields["model"] = mappedModel
return marshalFields(fields)
```

因此 `reasoning_effort`、`thinking`、`chat_template_kwargs` 以及其他未被禁止的字段不会在这里被删除或重写。

### 3.2 NVIDIA 上游客户端

`internal/upstream/nvidia/chat.go:26-45` 直接把已经生成的 JSON body 放入 HTTP 请求；该层没有 reasoning 参数归一化。流式响应随后由 `internal/sse/proxy.go` 代理。

这说明：如果请求已经进入 Chat handler，当前代码路径没有一个已确认的“丢弃 `chat_template_kwargs`”节点。DeepSeek 本次两条路径都超时，应继续检查上游状态和代理错误，而不是先改 body 作为既定事实。

### 3.3 Responses 映射和输出

`internal/protocol/responses/request.go:613-635` 当前行为：

- `reasoning_effort` 原样映射到 Chat 的 `reasoning_effort`；
- `reasoning.effort` 映射到 Chat 的 `reasoning_effort`；
- `thinking` 原样映射到 Chat 的 `thinking`；
- `max_output_tokens` 映射到 Chat 的 `max_tokens`。

Responses 非流式转换会识别 Chat `message.reasoning_content`（`internal/protocol/responses/nonstream.go:78` 附近），流式转换会识别 `delta.reasoning_content`（`internal/protocol/responses/delta.go:29` 附近）。已有审查记录指出，`delta.reasoning` 这种另一种字段形状尚未兼容，属于待补的兼容性风险，不是本次 GLM 实测失败的证据。

## 4. `hangzhou2-2` 真实结果

### 4.1 GLM-5.2 Chat 非流式

请求使用同一短题，`max_tokens=64`。字符数是统计值，不代表 reasoning 质量，也不能跨不同题目直接比较。

| Case | HTTP | 总耗时 | reasoning 字符 | content 字符 | 关键结果 |
|---|---:|---:|---:|---:|---|
| 默认，无 reasoning 参数 | 200 | 27.252 s | 0 | 1 | `message` 含 `reasoning_content` 字段，但值为空；正常结束 |
| `reasoning_effort=high` | 200 | 29.610 s | 64 | 1 | 收到 reasoning，正常结束 |
| `thinking={type:enabled,budget_tokens:512}` | 200 | 35.593 s | 105 | 0 | `finish_reason=length`，预算被 reasoning 消耗 |
| `thinking={type:disabled}` | 200 | 33.974 s | 0 | 1 | 关闭思考，正常结束 |

这组数据直接说明：当前路由器能把思考请求送到 GLM；默认不思考和显式思考是两个不同请求语义。`thinking.enabled` 的 content 为 0 不是“响应丢失”，而是 64 个输出 token 先耗在 reasoning 上，最终以 `length` 结束。

### 4.2 GLM-5.2 Chat 流式

| Case | HTTP | 总耗时 | 首数据 | SSE 数据事件 | reasoning delta | content delta | `[DONE]` |
|---|---:|---:|---:|---:|---:|---:|---|
| `reasoning_effort=high` | 200 | 35.860 s | 34.890 s | 27 | 95 字符 | 0 字符 | 是 |

流式 delta 的字段集合包含 `reasoning_content`。本样本同样使用较小输出预算，未产生 content delta；不能据此判断流式 content 被代理层吞掉。

### 4.3 GLM-5.2 Responses

| Case | HTTP | 总耗时 | output 类型 | reasoning | message | `output_text` |
|---|---:|---:|---|---:|---:|---:|
| `reasoning.effort=high`，`max_output_tokens=64` | 200 | 44.663 s | `reasoning` | 91 | 0 | 0 |
| `reasoning.effort=high`，`max_output_tokens=256` | 200 | 38.870 s | `reasoning`、`message` | 340 | 81 | 81 |

第二行是完整成功样本：reasoning 和最终答案同时存在。第一行只有 reasoning 是输出预算过小造成的合理结果，说明客户端不能把 `output_text=""` 单独解释成“模型没有思考”或“转换层丢数据”。

### 4.4 DeepSeek V4 Flash Chat 流式 A/B

所有请求都使用 `max_tokens=128`，统计到超时前没有任何 SSE 数据。

| 路径 | 请求参数 | 客户端窗口 | HTTP/客户端结果 | 首数据 | reasoning/content |
|---|---|---:|---|---|---|
| 内置代理池 | 无 `chat_template_kwargs`，顶层 `reasoning_effort=high` | 90 s | HTTP 0，curl 28 | 无 | 0 / 0 |
| 内置代理池 | `chat_template_kwargs={thinking:true,reasoning_effort:high}` | 240 s | HTTP 0，curl 28 | 无 | 0 / 0 |
| 直连（临时 `enabled=false`） | 同上显式 kwargs | 240 s | HTTP 0，curl 28 | 无 | 0 / 0 |

本次结果不能证明 kwargs 无效，也不能证明 kwargs 有效；它证明的是**当前目标机测试窗口内，上游没有在 240 秒内交付首字节**。直连也失败后，单纯把问题归因于内置代理池不成立。旧报告中“加 kwargs 后 206 秒完成”的样本应保留为历史样本，不能作为当前稳定性承诺。

### 4.5 目标机日志辅助证据

DeepSeek 测试窗口附近的应用日志元数据统计到：`validation_all_failed=31`、`proxy_transport_retired=1`、`transport_error=1`。这些主要是代理池验证/传输健康信号，不能把 31 次验证失败直接等同于 31 次模型请求失败；它们只能说明测试环境存在出口波动，足以解释 502/延迟抖动风险，但不能单独解释 GLM reasoning 字段是否存在。

## 5. 根因判断

### 5.1 “模型没有思考”的根因分层

1. **默认行为**：GLM-5.2 当前路由器请求不带 reasoning 参数时，目标机实测 reasoning 为 0。客户端必须显式选择思考。
2. **请求字段形状**：`thinking` 的强度字符串形式不是可靠协议。已有 NVIDIA 直连样本中 `thinking:"low"` 返回 400；对象形式 `{type:enabled/disabled}` 才是可用开关形状。
3. **显示层误读**：Responses 的 reasoning 是独立 `output` item；只读 `output_text` 会隐藏 reasoning。Chat 客户端也必须读取 `message.reasoning_content` 或流式 `delta.reasoning_content`。
4. **强度语义受 NVIDIA 托管层限制**：既有上游样本中 `low/high/max` 的 reasoning 长度接近，不能把客户端的七档强度原样承诺为 NVIDIA 托管 API 的七档计算深度。档位差异还会受题目、输出预算、排队和随机性影响。
5. **DeepSeek 超时**：本次目标机代理和直连都没有首字节，最可靠的当前表述是上游/账号权限/服务排队或模型服务状态异常；路由器丢 kwargs 尚未被证实。

### 5.2 已排除和未排除

| 假设 | 当前结论 | 证据 |
|---|---|---|
| Chat handler 删除 reasoning 参数 | 基本排除 | `chat.go:67-83`、`request.go:73-83`；GLM 目标机显式请求收到 reasoning |
| SSE 代理删除 `reasoning_content` | 基本排除 | GLM 流式收到 95 字符 reasoning delta 并有 `[DONE]` |
| Responses 一定丢 reasoning | 排除 | Responses `output` 收到 reasoning 340 字符 |
| 客户端只读 `output_text` 导致看不到 reasoning | 已证实为一种原因 | Responses reasoning 在独立 output item 中 |
| DeepSeek 一定只因缺 kwargs 挂起 | 未证实 | 目标机显式 kwargs 代理/直连都超时 |
| 代理池健康波动存在 | 已证实 | 32/32 初始健康但日志有 validation/transport 波动；不能代表每个模型请求失败 |
| `delta.reasoning` 形状兼容 | 未验证 | 当前转换代码主要识别 `reasoning_content` |

## 6. 优化方案

### 6.1 推荐路线：先保持透明透传，再补证据和观测

本次建议把“立即全局注入 kwargs”从当前推荐改成条件性后续方案：

| 方案 | 做法 | 优点 | 风险/限制 | 建议 |
|---|---|---|---|---|
| A：透明透传 + 客户端纠正 | 路由器不改写 reasoning；客户端按模型/API 正确传 `reasoning_effort`、`thinking`，Responses 读取 `output` | 零代码风险，已被 GLM 目标机验证 | DeepSeek 不能自动规避上游异常 | **当前采用** |
| B：DeepSeek 专属注入 | 仅对 `deepseek-ai/deepseek-v4-flash-0731` 合并 `chat_template_kwargs` | 如果上游恢复且确有兼容性 bug，可对旧客户端透明修复 | 本次目标机未验证成功；错误注入会掩盖上游状态，必须处理显式字段优先级 | **待复现后再做** |
| C：切换模型供应商 | GLM 直连原生供应商以获取更多强度档位 | 可获得供应商原生参数语义 | 失去 NVIDIA 聚合、Key 轮换和当前路由链路 | 需要业务决策 |

如果实施方案 B，必须满足以下约束：

1. 只匹配明确的 DeepSeek V4 Flash 上游模型 ID，不对所有 reasoning 模型全局注入。
2. 客户端显式提供 `chat_template_kwargs` 时不得覆盖；应做字段级合并并定义冲突优先级。
3. 同时覆盖 Chat 非流式、Chat 流式、Responses 映射；保留 `developer` 角色兼容性验证。
4. 先写失败单测，再在目标机做“直连成功 + 代理成功”的重复 A/B。一次成功不能作为稳定修复。

### 6.2 客户端参数建议

#### Chat

- 开思考：`reasoning_effort:"high"`，或供应商支持时使用 `thinking:{"type":"enabled"}`。
- 关思考：`thinking:{"type":"disabled"}`，不要把 `thinking` 写成字符串强度。
- 输出预算要给 reasoning 留空间；`max_tokens=64` 可能只得到 reasoning、没有最终 content。
- 流式客户端必须消费 `delta.reasoning_content`，不能只拼接 `delta.content`。

#### Responses

建议按以下顺序读取：

1. 遍历 `output`，处理 `type="reasoning"` 的 `summary`；
2. 处理 `type="message"` 的最终文本；
3. 把 `output_text` 当作“最终消息文本便利字段”，不是完整响应。

### 6.3 稳定性和可观测性

建议增加不含正文的观测字段或指标：

- `reasoning_requested`：是否请求思考；
- `reasoning_wire_fields`：请求中出现的字段名，不记录值；
- `reasoning_present`、`reasoning_chars`：响应是否有 reasoning 及长度；
- `first_byte_ms`、`first_token_ms`、`stream_done`；
- `route_mode`：direct / built-in proxy；
- `upstream_error_code`：429、503、529、timeout、proxy transport error 等分类。

当前 `request_logs` 只保存请求元数据、状态和 token/耗时，不保存请求体、响应体或 reasoning 字段存在性；因此线上“没有思考”的判断仍需要客户端日志或新增上述隐私安全指标。任何观测都不应保存 reasoning 原文、提示词、Key 或代理凭据。

对 DeepSeek 不建议无条件盲重试完整 reasoning 请求：一次请求可能已经在上游排队，重复请求会放大负载。应先区分首字节超时、429/503/529 和 CONNECT 失败，再决定有限重试、切换模型或返回结构化错误。

## 7. 验收标准

后续若实现方案 B，必须逐项取得明确结果：

| 验收项 | PASS 条件 |
|---|---|
| GLM Chat 默认 | HTTP 200，reasoning 为空是预期，不报“丢字段” |
| GLM Chat high | HTTP 200，非流式 `reasoning_content` 非空 |
| GLM Chat 流式 | HTTP 200，`reasoning_content` delta 非空且 `[DONE]` 到达 |
| GLM Responses | `output` 同时有 reasoning/message，最终文本可读 |
| DeepSeek 直连 kwargs | 在约定时间内有首数据并完成，至少重复 2 次 |
| DeepSeek 代理 kwargs | 与直连分别验证，不能用直连 PASS 代替代理 PASS |
| DeepSeek 无 kwargs 对照 | 记录真实 HTTP/超时结果，不把 timeout 误报为参数被吞 |
| 服务恢复 | live/ready/代理池健康、容器重启次数不增加 |
| 安全收尾 | 临时 Access Key 撤销，Cookie/临时文件清理，凭据不落盘 |

## 8. 当前未验证项与限制

- 本次没有修改 `internal/upstream/nvidia`，因此没有对方案 B 做代码级修复验证。
- DeepSeek 本次代理和直连均在 240 秒内无首字节，无法从该窗口确认上游究竟是权限、排队、服务负载还是模板兼容性；需要 NVIDIA 服务恢复后重测，并记录 HTTP 429/503/529 等明确响应。
- `delta.reasoning` 与 `delta.reasoning_content` 的兼容差异尚未通过真实上游样本验证。
- GLM `low/medium/max` 的强度差异只来自既有上游样本，当前目标机本轮重点验证了默认/high/开关，不应据字符数推导稳定算力等级。
- 代理池日志中的验证失败与模型请求失败不是一一对应关系，不能用日志计数直接计算模型错误率。

## 9. 参考来源

以下是初稿调研保留的外部参考；实际结论以本报告的目标机实测和代码审查为准：

| 来源 | 用途 |
|---|---|
| [NVIDIA NIM DeepSeek V4 Flash API 参考](https://docs.api.nvidia.com/nim/reference/deepseek-ai-deepseek-v4-flash-infer) | `reasoning_effort` 与模板参数说明 |
| [NVIDIA NIM reasoning model 文档](https://docs.nvidia.com/nim/large-language-models/1.15.0/reasoning-model.html) | `enable_thinking` 与 reasoning 参数体系 |
| [opencode issue #24264](https://github.com/anomalyco/opencode/issues/24264) | DeepSeek NIM 缺少模板参数时挂起的历史案例 |
| [opencode PR #37833](https://github.com/anomalyco/opencode/pull/37833) | 相关修复及 `developer` 角色兼容性案例 |
| [NVIDIA DeepSeek 慢请求论坛讨论](https://forums.developer.nvidia.com/t/nvidia-nim-is-too-slow-via-api-for-deepseek-models/368959) | 免费/公共托管服务的排队和可用性风险 |
| [智谱 GLM 深度思考文档](https://docs.bigmodel.cn/cn/guide/capabilities/thinking) | 供应商原生 GLM 强度语义对比 |
| [DeepSeek 官方 Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode) | 供应商原生 thinking 参数对比 |

## 附录：代码位置

| 位置 | 说明 |
|---|---|
| `internal/httpapi/v1/chat.go:61-83` | Chat 解析、模型解析和请求体转发 |
| `internal/httpapi/v1/responses.go:55-107` | Responses 转 Chat 并调用同一 NVIDIA 客户端 |
| `internal/protocol/chat/request.go:73-83` | Chat 只替换模型名，保留原始字段 |
| `internal/protocol/responses/request.go:613-635` | Responses reasoning 参数映射 |
| `internal/upstream/nvidia/chat.go:26-45` | NVIDIA Chat body 原样送入 HTTP 请求 |
| `internal/protocol/responses/nonstream.go:78` 附近 | 非流式 reasoning 输出转换 |
| `internal/protocol/responses/delta.go:29` 附近 | 流式 reasoning delta 转换 |
| `tests/mocknvidia/integration_test.go:480-518` | DeepSeek reasoning 流式透传测试 |
| `tests/mocknvidia/proxy_integration_test.go:436-475` | 代理链路 reasoning 流式测试 |

## 附录：本次本地验证

- `go test ./...`：通过。
- `go vet ./...`：通过。
- 目标机 live/ready/代理池健康检查：均 HTTP 200。
- 目标机容器：均 healthy，重启次数 0。
- 本次仅修改本报告，未提交、未推送 Git。
