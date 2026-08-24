// 视觉 QA 数据集：只在 web_qa_env.mjs 的隔离临时库里运行。
//
// 播种目标不是"业务正确"，而是"布局压力"：长模型名、超长 Base URL、
// 满编容量的列表、混合状态徽章、以及真实请求日志。缺陷（文本裁切、
// 容器撑破、徽章换行）只在数据够长够多时才会显形，空库页面永远是干净的。
//
// 契约要点（会咬人的几处）：
//   - 所有写请求必须带与 baseURL 完全一致的 Origin，否则 403。
//   - change-password 只收 current_password/new_password，且换发新 cookie。
//   - /admin/api/nvidia-keys/batch 的 keys 是换行分隔的**字符串**，不是数组。
//   - selectionDTO 不含 context_length / stream_*，那些只能走 PATCH。
//   - kind=asr/tts 直接 enabled:true 会被能力校验拒绝，只能先停用入库。
//   - request_logs 只有请求热路径一个写入口，且 BufferRecorder 默认 30s 才 flush。

const VALID_KEYS = [
  'nvapi-fixture-not-a-real-key-123456789',
  'fixture-second-valid-key-123456789',
]

// 故意混入格式合法但上游不认的 key：让列表同时出现有效/失效状态徽章。
const INVALID_KEYS = [
  'nvapi-seed-unauthorized-key-0001-aaaaaaaaaaaaaaaa',
  'nvapi-seed-unauthorized-key-0002-bbbbbbbbbbbbbbbb',
  'nvapi-seed-unauthorized-key-0003-cccccccccccccccc',
]

// 上游 id 刻意包含超长命名空间：这是表格列宽与 badge 换行的真实压力源。
const MODELS = [
  {
    upstream_id: 'meta/llama-3.1-8b-instruct',
    display_name: 'Llama 3.1 8B Instruct',
    public_id: 'llama-3.1-8b',
    kind: 'chat',
    supports_tools: true,
    context_length: 131072,
  },
  {
    upstream_id: 'nvidia/embedding-qa-4',
    display_name: 'NVIDIA Embedding QA 4',
    public_id: 'embedding-qa-4',
    kind: 'embedding',
  },
  {
    upstream_id: 'deepseek-ai/deepseek-r1-distill-qwen-32b-instruct-preview',
    display_name: 'DeepSeek R1 Distill Qwen 32B Instruct Preview 超长显示名用于压测列宽与换行',
    public_id: 'deepseek-r1-distill-qwen-32b-instruct-preview-long-identifier',
    kind: 'chat',
    supports_tools: true,
    supports_reasoning: true,
    context_length: 163840,
    stream_first_token_timeout_ms: 180_000,
    stream_idle_timeout_ms: 240_000,
  },
  {
    upstream_id: 'qwen/qwen2.5-vl-72b-instruct',
    display_name: 'Qwen2.5 VL 72B Instruct',
    public_id: 'qwen2.5-vl-72b',
    kind: 'chat',
    supports_vision: true,
    supports_tools: true,
    supports_reasoning: true,
    context_length: 32768,
  },
  {
    upstream_id: 'nvidia/llama-3.3-nemotron-super-49b-v1.5',
    display_name: 'Llama 3.3 Nemotron Super 49B v1.5',
    public_id: 'nemotron-super-49b',
    kind: 'chat',
    supports_tools: true,
    supports_reasoning: true,
    context_length: 131072,
  },
  {
    upstream_id: 'openai/gpt-oss-120b',
    display_name: 'GPT OSS 120B',
    public_id: 'gpt-oss-120b',
    kind: 'chat',
    supports_tools: true,
    supports_reasoning: true,
    context_length: 131072,
  },
  {
    upstream_id: 'nvidia/parakeet-ctc-0.6b-asr',
    display_name: 'Parakeet CTC 0.6B ASR',
    public_id: 'parakeet-ctc-asr',
    kind: 'asr',
  },
  {
    upstream_id: 'nvidia/magpie-tts-multilingual',
    display_name: 'Magpie TTS Multilingual',
    public_id: 'magpie-tts',
    kind: 'tts',
  },
]

const ACCESS_KEYS = [
  { name: '默认客户端', policy: { rpm_limit: 60, tpm_limit: 120_000 } },
  { name: 'Codex CLI 长名称用于压测卡片标题与操作列并存的边界条件', policy: { rpm_limit: 30, max_concurrent: 4 } },
  { name: 'Cline / Roo Code', policy: { tpm_limit: 240_000, token_budget: 50_000_000 } },
  { name: '内部压测', policy: { rpm_limit: 600, max_concurrent: 32 } },
  { name: '临时联调 — 待回收', policy: {} },
]

// selectionDTO 只接受这些字段；多传会被 decodeJSON 拒掉。
function toSelection(model) {
  return {
    public_id: model.public_id,
    upstream_id: model.upstream_id,
    display_name: model.display_name,
    kind: model.kind,
    provider: 'nvidia',
    // asr/tts 需要先验证能力才能启用，先停用入库即可占满列表。
    enabled: model.kind === 'chat' || model.kind === 'embedding',
    supports_vision: model.supports_vision ?? false,
    supports_tools: model.supports_tools ?? false,
    supports_reasoning: model.supports_reasoning ?? false,
  }
}

export async function seedAdminData({ api, initialPassword, password, apiOrigin }) {
  const notes = []
  const track = async (label, run) => {
    try {
      const result = await run()
      if (result && result.ok === false) notes.push(`${label} → HTTP ${result.status}`)
      return result
    } catch (error) {
      notes.push(`${label} → ${String(error).slice(0, 120)}`)
      return undefined
    }
  }

  // ── 登录（首次引导强制改密；改密会换发 cookie，由 api() 的 jar 接住） ──
  let login = await api('POST', '/admin/api/auth/login', { username: 'admin', password })
  if (!login.ok) {
    login = await api('POST', '/admin/api/auth/login', { username: 'admin', password: initialPassword })
    if (!login.ok) throw new Error(`seed login failed: HTTP ${login.status}`)
    const changed = await api('POST', '/admin/api/auth/change-password', {
      current_password: initialPassword,
      new_password: password,
    })
    if (!changed.ok) throw new Error(`seed change-password failed: HTTP ${changed.status}`)
  }

  // ── NVIDIA Keys ──
  for (const key of VALID_KEYS) {
    await track(`nvidia-key ${key.slice(0, 10)}…`, () => api('POST', '/admin/api/nvidia-keys', { key }))
  }
  await track('nvidia-keys batch', () => api('POST', '/admin/api/nvidia-keys/batch', { keys: INVALID_KEYS.join('\n') }))

  // ── Models：一次批量落库，再用 PATCH 补 selectionDTO 装不下的字段 ──
  await track('models save', () => api('POST', '/admin/api/models', { models: MODELS.map(toSelection) }))
  const listed = await api('GET', '/admin/api/models')
  const models = listed.body?.models ?? listed.body?.data ?? []
  for (const model of models) {
    const spec = MODELS.find((candidate) => candidate.upstream_id === model.upstream_id)
    if (!spec) continue
    const patch = {}
    if (spec.context_length) patch.context_length = spec.context_length
    if (spec.stream_first_token_timeout_ms) patch.stream_first_token_timeout_ms = spec.stream_first_token_timeout_ms
    if (spec.stream_idle_timeout_ms) patch.stream_idle_timeout_ms = spec.stream_idle_timeout_ms
    if (Object.keys(patch).length) await api('PATCH', `/admin/api/models/${model.id}`, patch)
  }

  // ── Access Keys：明文只在创建响应里出现一次 ──
  const tokens = []
  for (const entry of ACCESS_KEYS) {
    const created = await track(`access-key ${entry.name.slice(0, 6)}…`, () => api('POST', '/admin/api/access-keys', { name: entry.name }))
    const token = created?.body?.key ?? created?.body?.access_key
    if (typeof token === 'string') tokens.push(token)
    const id = created?.body?.id ?? created?.body?.access_key?.id
    if (id && Object.keys(entry.policy).length) {
      await api('PATCH', `/admin/api/access-keys/${id}`, entry.policy)
    }
  }

  // ── 真实流量：monitoring/statistics/首页 KPI 的唯一数据来源 ──
  // 尽早发出，好让 BufferRecorder 的 30s flush 与后续播种并行走完。
  const trafficStartedAt = Date.now()
  if (tokens.length) {
    for (let i = 0; i < 16; i += 1) {
      // 轮换 access key、公共 id 与故意写错的 id：成功与失败样本都要有。
      const token = tokens[i % tokens.length]
      const model = i % 5 === 4 ? 'seed-missing-model' : (i % 2 ? 'llama-3.1-8b' : 'meta/llama-3.1-8b-instruct')
      try {
        await fetch(`${apiOrigin}/v1/chat/completions`, {
          method: 'POST',
          headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
          body: JSON.stringify({ model, messages: [{ role: 'user', content: `seed traffic ${i}` }] }),
        })
      } catch { /* 失败同样是有效样本 */ }
    }
    try {
      await fetch(`${apiOrigin}/v1/models`, { headers: { authorization: 'Bearer nvr_seed-invalid-token' } })
    } catch { /* ignore */ }
  } else {
    notes.push('no access key plaintext returned; monitoring stays empty')
  }

  // ── 吊销一条：列表需要同时呈现有效与已吊销 ──
  const keyList = await api('GET', '/admin/api/access-keys')
  const revocable = (keyList.body?.access_keys ?? keyList.body?.keys ?? keyList.body?.data ?? []).at(-1)
  if (revocable?.id) {
    await track('access-key revoke', () => api('POST', `/admin/api/access-keys/${revocable.id}/revoke`))
  }

  // ── Provider：超长 base_url 是"容器撑破"的经典触发条件 ──
  await track('provider opencodefree', () => api('POST', '/admin/api/providers', {
    name: 'opencodefree',
    base_url: 'http://gateway-internal-opencodefree-very-long-host.example.com:18080/v1/openai/compatible',
    key: 'seed-provider-auth-key-not-a-real-credential',
  }))

  // ── 运行时 / 代理池 / 渠道探测：把只读面板也填上非零值 ──
  await track('settings patch', () => api('PATCH', '/admin/api/settings', {
    queue_capacity: 256,
    retry_budget_ms: 45_000,
    max_streaming_per_key: 8,
    stream_first_token_timeout_ms: 120_000,
    stream_idle_timeout_ms: 180_000,
    latency_routing_enabled: true,
  }))
  await track('proxy-pool patch', () => api('PATCH', '/admin/api/proxy-pool', {
    enabled: false,
    // upstream_url 必须带 provider 查询凭据，这里不伪造凭据串，留空即可；
    // 校验相关字段仍然写入，让代理池页面的表单有非默认值可看。
    validation_url: 'https://integrate.api.nvidia.com/v1/models',
    validation_status: 404,
    interval: '5s',
    proxy_ttl: '120s',
    max_latency: '2s',
  }))
  await track('model-health run', () => api('POST', '/admin/api/model-health/run'))

  // ── 等 flush：请求日志异步落库，不等就是空的监控页 ──
  const elapsed = Date.now() - trafficStartedAt
  const flushWaitMs = Math.max(0, 32_000 - elapsed)
  if (tokens.length && flushWaitMs > 0) {
    process.stdout.write(`waiting ${Math.round(flushWaitMs / 1000)}s for request-log flush… `)
    await new Promise((resolve) => setTimeout(resolve, flushWaitMs))
  }

  if (notes.length) console.log(`\n  seed notes: ${notes.join('; ')}`)
  return { notes }
}
