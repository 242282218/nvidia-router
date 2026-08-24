// 前端视觉 QA 专用环境：隔离数据库 + 播种数据 + 独立 Vite（HMR）。
//
//   node scripts/test/web_qa_env.mjs [--port 5175] [--no-seed]
//
// 与本地统一启动器（scripts/start-local.ps1，见 memory.md §11）互不干扰：
// 本脚本自带 e2e harness（临时目录 SQLite + mock NVIDIA 上游），不读项目
// .env，不接触 data/，不占用 3756/5173。就绪后把连接信息写入
// tmp/web-qa-env.json 供 web_visual_qa.mjs 读取，Ctrl+C 结束并回收子进程。
//
// 之所以不直接对着本地实例做视觉 QA：管理员口令存在库里且可能已改，
// 而 QA 需要的是可复现的数据集——隔离库既能稳定播种，又不会写坏本地数据。
import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = dirname(dirname(dirname(fileURLToPath(import.meta.url))))
const tmpDir = join(repoRoot, 'tmp')
const statePath = join(tmpDir, 'web-qa-env.json')

const argv = process.argv.slice(2)
const flag = (name) => {
  const i = argv.indexOf(`--${name}`)
  return i >= 0 ? argv[i + 1] : undefined
}
const vitePort = Number(flag('port') ?? 5175)
const seedEnabled = !argv.includes('--no-seed')

// harness 的初始口令由 tests/e2e/harness/main.go 硬编码，仅隔离库有效。
const INITIAL_PASSWORD = 'e2e-initial-admin-password'
const PASSWORD = 'e2e-admin-password-2026'

const children = []

function run(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: repoRoot,
    windowsHide: true,
    ...options,
  })
  children.push(child)
  return child
}

function killTree(child) {
  if (!child.pid || child.exitCode !== null) return
  if (process.platform === 'win32') {
    // Windows 上 SIGTERM 不会传给子进程树；用 taskkill 精确按 PID 收编。
    spawn('taskkill', ['/T', '/F', '/PID', String(child.pid)], { windowsHide: true })
  } else {
    child.kill('SIGTERM')
  }
}

let shuttingDown = false
function shutdown(code = 0) {
  if (shuttingDown) return
  shuttingDown = true
  for (const child of children) killTree(child)
  try {
    rmSync(statePath, { force: true })
  } catch { /* best effort */ }
  setTimeout(() => process.exit(code), 400)
}
process.on('SIGINT', () => shutdown(0))
process.on('SIGTERM', () => shutdown(0))

async function waitFor(label, probe, { timeoutMs = 60_000, intervalMs = 250 } = {}) {
  const deadline = Date.now() + timeoutMs
  let lastError
  while (Date.now() < deadline) {
    try {
      if (await probe()) return
    } catch (error) {
      lastError = error
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
  throw new Error(`timed out waiting for ${label}${lastError ? `: ${lastError.message}` : ''}`)
}

function exec(command, args) {
  return new Promise((resolve, reject) => {
    const child = run(command, args, { stdio: ['ignore', 'pipe', 'pipe'] })
    let stderr = ''
    child.stderr.on('data', (chunk) => { stderr += chunk })
    child.on('error', reject)
    child.on('exit', (code) => {
      if (code === 0) resolve()
      else reject(new Error(`${command} ${args.join(' ')} exited ${code}: ${stderr.slice(-800)}`))
    })
  })
}

// ── 1. 编译 harness ──────────────────────────────────────────────
// 直接跑二进制而不是 `go run`：`go run` 会另起一个孙进程，退出时难以精确回收。
mkdirSync(tmpDir, { recursive: true })
const harnessBin = join(tmpDir, process.platform === 'win32' ? 'web-qa-harness.exe' : 'web-qa-harness')
process.stdout.write('building e2e harness… ')
await exec('go', ['build', '-o', harnessBin, './tests/e2e/harness'])
console.log('ok')

// ── 2. 启动 harness，第一行 stdout 就是它的 baseURL ───────────────
const harness = run(harnessBin, [], { stdio: ['ignore', 'pipe', 'pipe'] })
let harnessOut = ''
const apiOrigin = await new Promise((resolve, reject) => {
  harness.stdout.on('data', (chunk) => {
    harnessOut += chunk
    const match = /^(http:\/\/\S+)/m.exec(harnessOut)
    if (match) resolve(match[1])
  })
  harness.stderr.on('data', (chunk) => { harnessOut += chunk })
  harness.on('exit', (code) => reject(new Error(`harness exited ${code}: ${harnessOut.slice(-800)}`)))
  setTimeout(() => reject(new Error(`harness produced no URL: ${harnessOut.slice(-800)}`)), 30_000)
})
await waitFor('harness health', async () => (await fetch(`${apiOrigin}/health/live`)).ok)
console.log(`harness  ${apiOrigin}`)

// ── 3. 播种：登录（含强制改密）后写入有真实体量的数据 ─────────────
const cookieJar = new Map()

function cookieHeader() {
  return [...cookieJar.entries()].map(([name, value]) => `${name}=${value}`).join('; ')
}

async function api(method, path, body) {
  const response = await fetch(`${apiOrigin}${path}`, {
    method,
    headers: {
      'content-type': 'application/json',
      // 变更请求要求 Origin 与 Host 一致（见 internal/httpapi/admin 的同源校验）。
      origin: apiOrigin,
      ...(cookieJar.size ? { cookie: cookieHeader() } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    redirect: 'manual',
  })
  for (const raw of response.headers.getSetCookie?.() ?? []) {
    const [pair] = raw.split(';')
    const index = pair.indexOf('=')
    if (index > 0) cookieJar.set(pair.slice(0, index).trim(), pair.slice(index + 1).trim())
  }
  const text = await response.text()
  let parsed
  try { parsed = text ? JSON.parse(text) : undefined } catch { parsed = text }
  return { status: response.status, ok: response.ok, body: parsed }
}

async function seed() {
  const { seedAdminData } = await import('./web_qa_seed.mjs')
  await seedAdminData({ api, initialPassword: INITIAL_PASSWORD, password: PASSWORD, apiOrigin })
}

if (seedEnabled) {
  process.stdout.write('seeding… ')
  await seed()
  console.log('ok')
}

// ── 4. 启动独立 Vite，/admin/api 代理指向 harness ─────────────────
// 走 Vite 的 JS 入口而不是 .bin/vite.cmd：.cmd 需要 shell，而 shell 拼接参数
// 既有转义风险，也让退出时的进程树回收变得不确定。
const viteEntry = join(repoRoot, 'web', 'node_modules', 'vite', 'bin', 'vite.js')
const vite = run(process.execPath, [viteEntry, '--host', '127.0.0.1', '--port', String(vitePort), '--strictPort'], {
  cwd: join(repoRoot, 'web'),
  env: { ...process.env, VITE_PROXY_ORIGIN: apiOrigin },
  stdio: ['ignore', 'pipe', 'pipe'],
})
let viteOut = ''
vite.stdout.on('data', (chunk) => { viteOut += chunk })
vite.stderr.on('data', (chunk) => { viteOut += chunk })

const webOrigin = `http://127.0.0.1:${vitePort}`
try {
  await waitFor('vite', async () => (await fetch(`${webOrigin}/admin/login`)).ok, { timeoutMs: 90_000 })
} catch (error) {
  console.error(viteOut.slice(-1_500))
  shutdown(1)
  throw error
}

writeFileSync(statePath, `${JSON.stringify({
  webOrigin,
  apiOrigin,
  username: 'admin',
  password: PASSWORD,
  seeded: seedEnabled,
}, null, 2)}\n`)

console.log(`web      ${webOrigin}/admin/`)
console.log(`state    ${statePath}`)
console.log('ready — Ctrl+C to stop')

harness.on('exit', (code) => {
  if (!shuttingDown) {
    console.error(`harness exited unexpectedly (${code})`)
    shutdown(1)
  }
})
vite.on('exit', (code) => {
  if (!shuttingDown) {
    console.error(`vite exited unexpectedly (${code}):\n${viteOut.slice(-1_500)}`)
    shutdown(1)
  }
})
