# NVIDIA 真实联调说明

本文说明本仓库真实 NVIDIA 联调的前置条件、环境变量、测试范围和清理规则。真实联调会访问正在运行的路由器，并通过路由器访问 NVIDIA；它不是 Mock 测试，也不代表本次执行一定成功。

## 1. 先读懂 PASS、FAIL 和 SKIP

`tests/live` 中的测试文件使用 Go build tag `live`。不带 `-tags=live` 时，该目录中的真实联调测试不会参与构建。

```go
//go:build live
```

直接运行 `go test -tags=live ./tests/live -v` 时，`loadConfig` 缺少路由器地址、下游 Access Key 或 Chat 模型会调用 `t.SkipNow()`。Embedding、ASR 和 TTS 也分别会在对应环境变量为空时跳过。`t.Skip` 只表示没有执行该 case，绝不能当作 PASS；退出码为 0 也不能证明真实联调通过。验收时必须逐项查看 `case=<名称> status=<PASS|SKIP|FAIL>`，配置能力不得是 SKIP。

脚本对运行服务、管理员密码、原始 NVIDIA Key 和 Chat 模型做前置检查，缺少时将 `Configuration` 报为 FAIL，而不是让测试空成功。脚本创建的临时 Access Key 只是测试凭证，不能替代真实 case 的 PASS。

## 2. 前置条件

1. 已安装并可执行 `git`、`curl`、`python3` 和 Go。`scripts/test/live-nvidia.sh` 会逐项检查这 4 个命令。
2. 路由器已启动，并能从执行测试的机器访问。例如 Compose 默认将容器端口映射到 `3756`。
3. `/health/live` 返回 HTTP `200`。脚本会先检查：

   ```text
   GET ${NVIDIA_ROUTER_LIVE_BASE_URL}/health/live
   ```

4. 管理员账号已可登录。新部署通常使用 `admin`，但首次登录必须按项目要求修改默认密码；脚本的管理员用户名默认值也是 `admin`。
5. 有合法、可用且允许本次验证的 NVIDIA API Key。Key 只通过环境变量传给脚本，不能写入仓库、源码、测试夹具、日志或镜像。
6. 管理端已有需要测试的模型白名单。模型列表由管理员维护，`/v1/models` 只返回已启用的全局白名单模型。
7. 必测模型对应的账户权限、配额、endpoint 和区域限制已确认。不要根据静态模型目录或模型名称推断账户一定可用。
8. 若执行 ASR，还要准备本地非空音频文件；若执行 TTS，还要确认账户中的 TTS 模型和 voice 可用。音频文件只用于请求生命周期，不能提交到仓库。

服务本身的核心配置通常来自 `.env.example` 和 Compose：

| 变量 | 用途 |
| --- | --- |
| `NVIDIA_ROUTER_LISTEN_ADDR` | 路由器监听地址，例如 `0.0.0.0:3756` |
| `NVIDIA_ROUTER_DATA_DIR` | SQLite 等持久化数据目录，例如 `/data` |
| `NVIDIA_ROUTER_TEMP_DIR` | 请求临时资源目录，例如 `/tmp` |
| `NVIDIA_ROUTER_MASTER_KEY` | 32 字节 Raw URL Base64 主密钥；缺失或错误会阻止服务正常启动 |
| `NVIDIA_ROUTER_NVIDIA_BASE_URL` | NVIDIA 上游地址；示例值为 `https://integrate.api.nvidia.com` |

## 3. 必测主路径

正式联调至少必须对同一实际可用配置完成以下 PASS。请求正文和响应正文只用于测试断言，不得打印或保存。

| case | 路由 | 当前测试的实际校验 |
| --- | --- | --- |
| `Models` | `GET /v1/models` | HTTP `200`、`object=list`、`data` 非空，且每个模型 ID 非空 |
| `ChatNonstream` | `POST /v1/chat/completions` | 使用 `NVIDIA_ROUTER_LIVE_CHAT_MODEL`，发送 `stream=false` 和 `Reply with exactly: ok`，HTTP `200` 且 `choices` 非空 |
| `ChatStream` | `POST /v1/chat/completions` | 使用同一 Chat 模型发送 `stream=true`，响应为 `text/event-stream`，并出现 `data: [DONE]` |
| `ResponsesNonstream` | `POST /v1/responses` | 使用 `NVIDIA_ROUTER_LIVE_RESPONSES_MODEL`（为空时回退 Chat 模型），发送文本 `input`，HTTP `200`、`object=response`、`status=completed` 且 `output` 非空 |
| `ResponsesStream` | `POST /v1/responses` | 同一 Responses 模型发送 `stream=true`，响应为 SSE，并出现 `event: response.completed` |
| `Embedding` | `POST /v1/embeddings` | 使用实际 Key 可用的 `NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL`，HTTP `200`、`object=list`、`data` 非空且首项 embedding 非空 |

Embedding 是必测能力。当前 Go 测试在 `NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL` 为空时会 SKIP；这种结果不能作为验收通过，必须先找到实际可用的 Embedding 模型并设置变量后重新运行。

测试使用的固定请求文本是探针文本，不要据此推断业务质量。测试只检查协议和必要字段，不对生成内容做通用质量评分。

## 4. ASR/TTS 能力验证门禁

ASR 和 TTS 不是仅凭 `/v1/models` 列表即可启用的能力。只有对账户中真实存在的模型和真实 endpoint 成功验证，才能认为能力已验证。

### 4.1 何时执行

- ASR：同时设置 `NVIDIA_ROUTER_LIVE_ASR_MODEL` 和 `NVIDIA_ROUTER_LIVE_ASR_FILE`。测试调用 `POST /v1/audio/transcriptions`，上传 `file` 和 `model`，要求 HTTP `200`，且 JSON 中 `text` 或 `transcript` 至少一个非空。
- TTS：设置 `NVIDIA_ROUTER_LIVE_TTS_MODEL`；`NVIDIA_ROUTER_LIVE_TTS_VOICE` 为空时测试代码使用 `alloy`。测试调用 `POST /v1/audio/speech`，要求 HTTP `200`、Content-Type 为 `audio/*` 或 `application/octet-stream`，且响应音频非空。
- 账户没有对应真实模型、endpoint、权限或测试素材时，不要填一个猜测值强行运行。让该 Audio case 明确 SKIP，并记录“未启用模型/未完成真实能力验证”；SKIP 不是 PASS，也不能据此设置验证时间。

### 4.2 管理 API 验证与启用顺序

成功条件必须同时包括：实际账户、实际模型、实际 endpoint 请求成功，并且满足上面的响应断言。管理端提供以下受审计入口：

- `POST /admin/api/models/<id>/test`；兼容别名为 `POST /admin/api/models/<id>/verify`。
- 请求体严格为 `{"key_id": <positive integer>}`。未知字段（包括调用者提交 `verified_at` 或 `capability_verified_at`）返回 `400 invalid_request`。
- 服务端读取对应的加密 NVIDIA Key，真实调用该模型 endpoint；成功后由服务端生成 UTC `capability_verified_at`，并在同一事务中清除该 Key 与模型的 block。
- 验证失败不写入时间，也不清除 block。调用者不能指定验证时间。
- ASR/TTS 在 `capability_verified_at` 为空时不能启用。验证成功后仍必须显式调用 `PATCH /admin/api/models/<id>`，提交 `{"enabled":true}`；验证接口不会隐式启用模型。

当前仓库的管理模型 API 可用于发现和维护模型：

- `GET /admin/api/models/candidates`：从首个启用 NVIDIA Key 发现候选模型；不会自动写入白名单。
- `POST /admin/api/models`：保存管理员选择的白名单模型。
- `GET /admin/api/models`：查看模型的 `kind`、`enabled` 和 `capability_verified_at`。
- `PATCH /admin/api/models/<id>`：修改允许的模型字段和 `enabled` 状态。ASR/TTS 在验证时间为空时不能启用。

不要直接修改 SQLite，也不要在文档或联调记录中伪造时间戳。验证接口只负责真实探测、写入服务端时间和清 block；Audio 的启用仍由单独的 `PATCH` 明确完成。

## 5. 联调环境变量

### 5.1 直接运行 Go live test

`tests/live/client_test.go` 实际读取以下变量：

| 变量 | 必需性 | 说明 |
| --- | --- | --- |
| `NVIDIA_ROUTER_LIVE_BASE_URL` | 必需 | 被测路由器地址，必须是无用户名和密码的 `http` 或 `https` URL；末尾 `/` 会被去掉 |
| `NVIDIA_ROUTER_LIVE_ACCESS_KEY` | 必需 | 被测路由器的下游 Access Key；不是管理员密码，也不是 NVIDIA 上游 Key |
| `NVIDIA_ROUTER_LIVE_CHAT_MODEL` | 必需 | Chat 模型；缺少时整个 `TestLiveNVIDIA` 跳过 |
| `NVIDIA_ROUTER_LIVE_RESPONSES_MODEL` | 否 | Responses 模型；为空时回退到 Chat 模型 |
| `NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL` | 验收必需 | Embedding 模型；为空时该 case SKIP，不能算通过 |
| `NVIDIA_ROUTER_LIVE_ASR_MODEL` | 可选 | 设置后才尝试 ASR；必须与真实账户模型一致 |
| `NVIDIA_ROUTER_LIVE_ASR_FILE` | 可选 | ASR 音频文件路径；与 ASR 模型同时设置才执行 |
| `NVIDIA_ROUTER_LIVE_TTS_MODEL` | 可选 | 设置后才尝试 TTS；必须与真实账户模型一致 |
| `NVIDIA_ROUTER_LIVE_TTS_VOICE` | 可选 | TTS voice；为空时使用 `alloy` |

直接运行时，`NVIDIA_ROUTER_LIVE_ACCESS_KEY` 的值会通过 `Authorization: Bearer ...` 发送到路由器。不要把它写入命令历史、输出或文件。

### 5.2 可重复脚本

`scripts/test/live-nvidia.sh` 实际要求：

| 变量 | 必需性 | 用途 |
| --- | --- | --- |
| `NVIDIA_ROUTER_LIVE_BASE_URL` | 必需 | 路由器地址，并用于健康检查和管理 API |
| `NVIDIA_ROUTER_ADMIN_PASSWORD` | 必需 | 登录管理 API；不要使用占位值 |
| `NVIDIA_ROUTER_LIVE_KEY` | 必需 | 原始 NVIDIA API Key；只能来自运行环境，成功导入或识别后应立即取消该变量 |
| `NVIDIA_ROUTER_LIVE_CHAT_MODEL` | 必需 | 传给 live test 的 Chat 模型 |
| `NVIDIA_ROUTER_ADMIN_USERNAME` | 可选 | 管理员用户名；未设置时脚本使用 `admin` |
| `NVIDIA_ROUTER_LIVE_RESPONSES_MODEL` | 可选 | 由 live test 使用；为空时回退 Chat 模型 |
| `NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL` | 验收必需 | 由 live test 使用；为空会 SKIP Embedding |
| `NVIDIA_ROUTER_LIVE_ASR_MODEL` | 可选 | 由 live test 使用 |
| `NVIDIA_ROUTER_LIVE_ASR_FILE` | 可选 | 由 live test 使用 |
| `NVIDIA_ROUTER_LIVE_TTS_MODEL` | 可选 | 由 live test 使用 |
| `NVIDIA_ROUTER_LIVE_TTS_VOICE` | 可选 | 由 live test 使用，空值默认为 `alloy` |

`NVIDIA_ROUTER_LIVE_KEY` 只能来自运行环境，不能写入仓库。完整脚本生命周期应为：导入或识别临时 NVIDIA Key，按需调用上述验证接口并显式启用 Audio，创建临时 Access Key，运行 live case，撤销 Access Key，删除脚本自己新导入的 NVIDIA Key，注销管理员会话。正常输出只允许 `case`、`status` 和 `duration`，不得输出秘密、请求/响应正文或测试原始日志。

当前工作区的 `scripts/test/live-nvidia.sh` 尚未实现这套完整生命周期：它只检查已有可用 NVIDIA Key，未导入 `NVIDIA_ROUTER_LIVE_KEY`、未调用模型验证/启用接口，并直接输出 `go test -v` 原始日志。文档不能把这些未实现行为记为联调 PASS；完成脚本收尾后必须重新核对本节。

## 6. 临时凭证、失败清理和输出安全

脚本在启动时注册 `EXIT` trap。无论 Go 测试失败、前置步骤失败，还是收到中断信号，清理逻辑都会尽力执行：

1. 若临时 Access Key 已创建，调用 `DELETE /admin/api/access-keys/<id>` 撤销它；撤销失败会使脚本最终失败。
2. 删除脚本本次新导入的 NVIDIA Key；已有 Key 不得删除。
3. 若管理员会话已建立，调用 `POST /admin/api/auth/logout` 注销会话；注销失败会使原本成功的脚本最终失败。
4. `unset` 所有临时秘密、Cookie 和环境变量。

清理结果会输出 `RevokeTemporaryAccessKey` 和 `AdminLogout` 的状态。脚本和 live test 的正常状态输出只应包含 case 名、`status` 和耗时；不得输出 NVIDIA Key、Access Key、管理员 Cookie、请求正文、响应正文、SSE 数据、音频内容或完整错误正文。发现终端、CI 日志或日志文件中有这些内容时，应立即停止传播并轮换相关凭证。

## 7. 运行命令

### 7.1 只做无秘密的编译检查

该命令不运行 live case，也不需要路由器或任何 Key；它只验证带 `live` build tag 的测试包可以编译：

```bash
go test -tags=live ./tests/live -run '^$'
```

### 7.2 直接执行真实测试

在安全的环境变量注入方式下设置变量，然后运行：

```bash
go test -tags=live ./tests/live -v
```

### 7.3 使用可重复脚本

脚本命令与任务 41 及仓库现有脚本一致：

```bash
bash scripts/test/live-nvidia.sh
```

脚本应在仓库根目录或能被 `git rev-parse --show-toplevel` 定位到仓库的目录执行。脚本会自行切换到仓库根目录。它不是 Windows `cmd.exe` 脚本，需要 Bash（例如 Linux、WSL 或 Git Bash）以及 `python3`。

## 8. 结果判定

- `Models`、`ChatNonstream`、`ChatStream`、`ResponsesNonstream`、`ResponsesStream` 和实际可用 `Embedding` 必须逐项看到 `status=PASS`。
- 任一必测 case 为 `SKIP`，结果只能记为“未完成真实联调”，不能记为 PASS。
- 任一必测 case 为 `FAIL`，结果为 FAIL；先检查服务、白名单、模型权限、上游配额和 endpoint，再重新运行。
- ASR/TTS 没有真实可用模型或 endpoint 时可以明确 `SKIP`，但必须说明未启用模型/未完成验证，且不能设置 `capability_verified_at`。
- ASR/TTS 只有在真实请求和响应断言成功后，才允许调用 `/test` 或 `/verify`；验证成功后仍需显式 PATCH `enabled=true`。
- 文档命令的成功只代表命令本身成功，不替代 live case 的 PASS 证据。
- CI 负责 race、lint、secret scan、Compose 和浏览器 E2E；真实 NVIDIA 联调不由普通 CI 自动提供凭证，必须显式注入运行时凭证。

## 9. 风险和注意事项

- 真实请求可能产生 NVIDIA 费用、消耗配额，并受限流、模型下线、账户权限和网络波动影响。使用专用测试 Key、最小权限账户和短生命周期临时 Access Key。
- `NVIDIA_ROUTER_LIVE_BASE_URL` 是被测路由器地址，不应误填 NVIDIA 上游地址；上游地址由服务配置中的 `NVIDIA_ROUTER_NVIDIA_BASE_URL` 控制。
- 第一轮默认部署可能使用普通 HTTP。HTTP 会明文传输管理员密码、Cookie、下游 Access Key、提示词和响应，不是安全的公网生产部署方案。不要在不可信网络上执行真实联调；HTTPS 属于后续部署工作。
- 真实提示词、模型输出、Embedding 输入、转录文本、上传音频和生成音频都属于敏感数据。测试使用的内容应为最小探针，且不得保存到仓库、数据库或日志。
- ASR 音频由测试进程从 `NVIDIA_ROUTER_LIVE_ASR_FILE` 读取；使用结束后应确认临时资源已清理，不要把个人或客户音频用于联调。
- 进程异常退出、机器断电或网络中断可能阻止清理请求。出现这种情况，必须使用管理员页面或 `DELETE /admin/api/access-keys/<id>` 按实际 ID 撤销残留临时 Access Key，并轮换可能暴露的凭证；不得凭猜测 ID 删除生产 Key。
- 不要把命令行中的秘密提交到 Shell history、CI 变量回显、截图、Issue、聊天记录或构建产物。执行前确认 CI 日志不会回显环境变量。
- 联调前确认服务数据库和模型白名单可恢复。不要为了让测试变绿而放宽模型门禁、伪造 `capability_verified_at`、把 Audio case 改成 PASS，或修改 live 测试和脚本的 SKIP 行为。
