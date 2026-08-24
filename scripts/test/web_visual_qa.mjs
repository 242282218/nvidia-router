// 全站视觉 QA 探针：逐页 × 逐视口 × 双主题 × 浮层态截图 + 几何缺陷检测。
//
// 用法（推荐先起隔离 QA 环境，见 memory.md §14）：
//   node scripts/test/web_qa_env.mjs            # 另一个终端，常驻
//   node scripts/test/web_visual_qa.mjs [baseURL] [--out <dir>] [--only <substr>]
//
// baseURL 优先级：命令行 > PLAYWRIGHT_BASE_URL > tmp/web-qa-env.json 的
// webOrigin > http://127.0.0.1:5173。口令同样优先取隔离环境（仅临时库有效），
// 缺失时退回本地 .env 的初始口令；两者都不回显、不落盘。
//
// 检测项（纯几何/计算样式，不依赖像素对比；假阳性规则见 memory.md §14）：
//   - 文档级横向溢出       scrollWidth - clientWidth > 0
//   - 元素越界             右/左边缘超出视口，且无滚动祖先兜住
//   - 容器裁掉内容         裁切容器内容溢出，且不是 ellipsis 单行截断
//   - 文本裁切             叶子节点 scroll 尺寸超出 client 且未做省略处理
//   - 可交互元素重叠       命中测试：中心点被同层元素接管
//   - 触控目标过小         命中区（含 label）< 24px（WCAG 2.2 AA 下限）
//   - 浮层越界             dialog/menu/tooltip/listbox 面板超出视口四边
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'

const require = createRequire(fileURLToPath(new URL('../../web/package.json', import.meta.url)))
const { chromium } = require('@playwright/test')

const repoRoot = dirname(dirname(dirname(fileURLToPath(import.meta.url))))

const argv = process.argv.slice(2)
function flag(name) {
  const i = argv.indexOf(`--${name}`)
  return i >= 0 ? argv[i + 1] : undefined
}
const envState = readEnvState()
const positional = argv.filter((a, i) => !a.startsWith('--') && !(i > 0 && argv[i - 1].startsWith('--')))
const baseURL = positional[0] ?? process.env.PLAYWRIGHT_BASE_URL ?? envState?.webOrigin ?? 'http://127.0.0.1:5173'
const outDir = join(repoRoot, flag('out') ?? 'tmp/visual-qa')
const only = flag('only')

// 优先读 web_qa_env.mjs 写下的隔离环境（可复现数据集，口令只在临时库有效）；
// 没有它时退回本地 .env 的初始口令——仅当本地库仍是首次引导状态才有效。
function readEnvState() {
  try {
    return JSON.parse(readFileSync(join(repoRoot, 'tmp/web-qa-env.json'), 'utf8'))
  } catch {
    return undefined
  }
}

function readAdminPassword() {
  if (process.env.NVIDIA_ROUTER_ADMIN_PASSWORD) return process.env.NVIDIA_ROUTER_ADMIN_PASSWORD
  const env = readFileSync(join(repoRoot, '.env'), 'utf8')
  for (const line of env.split(/\r?\n/)) {
    const m = /^NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD\s*=\s*(.*)$/.exec(line.trim())
    if (m) return m[1].replace(/^["']|["']$/g, '')
  }
  throw new Error('admin password not found in .env')
}

const PAGES = [
  { path: '/admin/', name: 'root' },
  { path: '/admin/nvidia-keys', name: 'nvidia-keys' },
  { path: '/admin/providers', name: 'providers' },
  { path: '/admin/models', name: 'models' },
  { path: '/admin/access-keys', name: 'access-keys' },
  { path: '/admin/proxy-pool', name: 'proxy-pool' },
  { path: '/admin/channel-status', name: 'channel-status' },
  { path: '/admin/runtime', name: 'runtime' },
  { path: '/admin/monitoring', name: 'monitoring' },
  { path: '/admin/system', name: 'system' },
  { path: '/admin/system?tab=live', name: 'system-live' },
  { path: '/admin/system?tab=audit', name: 'system-audit' },
  { path: '/admin/change-password', name: 'change-password' },
  { path: '/admin/no-such-page', name: 'not-found' },
]

const VIEWPORTS = [
  { name: '390', width: 390, height: 844 },
  { name: '768', width: 768, height: 1024 },
  // 1024 是 lg 断点的第一格：侧栏刚占掉 240px，正文反而比 768 更窄，
  // 是全站最容易溢出的宽度，必须单独测。
  { name: '1024', width: 1024, height: 800 },
  { name: '1440', width: 1440, height: 900 },
]

// 浮层打开态场景：静态截图查不到菜单/弹窗的裁切与遮挡，必须实际打开。
// 每个场景在导航后执行 open()，返回 false 表示该视口下入口不存在（跳过）。
const SCENES = [
  {
    name: 'command-palette',
    path: '/admin/models',
    async open(page, vp) {
      const trigger = vp.width >= 1024
        ? page.getByTestId('open-command-palette')
        : page.getByTestId('open-command-palette-mobile')
      if (!(await trigger.count())) return false
      await trigger.click()
      const input = page.getByLabel('搜索页面或操作')
      await input.waitFor({ state: 'visible', timeout: 4_000 })
      await input.fill('模')
      await page.waitForTimeout(200)
      return true
    },
  },
  {
    name: 'shortcut-help',
    path: '/admin/models',
    async open(page) {
      await page.keyboard.press('?')
      await page.getByRole('dialog', { name: '键盘快捷键' }).waitFor({ state: 'visible', timeout: 4_000 })
      await page.waitForTimeout(200)
      return true
    },
  },
  {
    name: 'mobile-drawer',
    path: '/admin/models',
    only: (vp) => vp.width < 1024,
    async open(page) {
      await page.getByRole('button', { name: '切换菜单' }).click()
      // 抽屉是 300ms transform 位移，几何检查必须等位移结束（memory.md §13）。
      await page.waitForTimeout(450)
      return true
    },
  },
  {
    name: 'account-menu',
    path: '/admin/models',
    only: (vp) => vp.width >= 1024,
    async open(page) {
      const trigger = page.getByRole('button', { name: '账户操作' })
      if (!(await trigger.count())) return false
      await trigger.click()
      await page.waitForTimeout(200)
      return true
    },
  },
  {
    name: 'create-access-key',
    path: '/admin/access-keys',
    async open(page) {
      const trigger = page.getByTestId('open-create-access-key')
      if (!(await trigger.count())) return false
      await trigger.click()
      await page.waitForTimeout(300)
      return true
    },
  },
  {
    name: 'batch-import-keys',
    path: '/admin/nvidia-keys',
    async open(page) {
      const trigger = page.getByTestId('open-batch-import')
      if (!(await trigger.count())) return false
      await trigger.click()
      await page.waitForTimeout(300)
      return true
    },
  },
]

// 页面内注入的几何审计器。返回结构化缺陷列表。
const AUDIT = () => {
  const issues = []
  const vw = window.innerWidth
  const vh = window.innerHeight
  const doc = document.documentElement

  const describe = (el) => {
    if (!el) return '<none>'
    const id = el.id ? `#${el.id}` : ''
    const testid = el.getAttribute?.('data-testid')
    const cls = (el.className && typeof el.className === 'string')
      ? `.${el.className.trim().split(/\s+/).slice(0, 3).join('.')}`
      : ''
    const txt = (el.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 40)
    return `${el.tagName?.toLowerCase() ?? '?'}${id}${testid ? `[${testid}]` : ''}${cls}${txt ? ` "${txt}"` : ''}`
  }

  const docOverflow = Math.max(
    doc.scrollWidth - doc.clientWidth,
    document.body.scrollWidth - document.body.clientWidth,
  )
  if (docOverflow > 0) issues.push({ kind: 'doc-overflow-x', px: docOverflow, el: 'document' })

  const all = [...document.querySelectorAll('body *')]
  // .sr-only 是刻意的 1x1 裁切（屏幕阅读器专用），position:absolute + clip
  // 本身就会触发溢出判定；连同其子树一起排除，否则真实缺陷会被噪声淹没。
  const screenReaderOnly = new Set()
  for (const node of document.querySelectorAll('.sr-only')) {
    screenReaderOnly.add(node)
    for (const child of node.querySelectorAll('*')) screenReaderOnly.add(child)
  }

  const clips = (cs) => ['auto', 'scroll', 'hidden', 'clip'].includes(cs.overflowX)
    || ['auto', 'scroll', 'hidden', 'clip'].includes(cs.overflowY)

  // 越界判定必须看祖先：滚动容器（数据表）里的宽内容是设计意图，
  // 关掉的抽屉整体位移到画布外，其子元素也不是缺陷。真正该报的是
  // 那个"顶到视口外"的容器自身，而不是它内部每一个后代。
  const offCanvas = new WeakMap()
  const isOffCanvas = (el) => {
    if (offCanvas.has(el)) return offCanvas.get(el)
    let node = el
    let verdict = false
    while (node && node !== document.body) {
      if (node.hasAttribute?.('inert')) { verdict = true; break }
      const r = node.getBoundingClientRect()
      // 完全位于画布左/右侧之外的固定层 = 收起的抽屉/离屏面板
      if (r.right <= 1 || r.left >= vw - 1) { verdict = true; break }
      node = node.parentElement
    }
    offCanvas.set(el, verdict)
    return verdict
  }
  const insideScroller = (el) => {
    let node = el.parentElement
    while (node && node !== document.body) {
      if (clips(getComputedStyle(node))) return true
      node = node.parentElement
    }
    return false
  }

  const visible = all.filter((el) => {
    if (screenReaderOnly.has(el)) return false
    const cs = getComputedStyle(el)
    if (cs.display === 'none' || cs.visibility === 'hidden' || cs.opacity === '0') return false
    const r = el.getBoundingClientRect()
    return r.width > 0 && r.height > 0
  })

  for (const el of visible) {
    const cs = getComputedStyle(el)
    const r = el.getBoundingClientRect()
    if (isOffCanvas(el)) continue

    // 1. 越界：元素越过视口边界，且没有滚动祖先为它兜住
    if (r.width < vw + 1 && !insideScroller(el)) {
      if (r.right > vw + 1) issues.push({ kind: 'out-of-viewport-right', px: Math.round(r.right - vw), el: describe(el) })
      if (r.left < -1 && cs.position !== 'fixed') issues.push({ kind: 'out-of-viewport-left', px: Math.round(-r.left), el: describe(el) })
    }

    // 2. 容器裁掉了内容。overflow:visible 时内容并未被裁掉，只是画到框外——
    // 那属于第 1 项（越界）的范畴；这里只报真正会丢内容的裁切容器。
    // 带 ellipsis 的单行截断是设计意图（.truncate），不算丢内容。
    const ellipsized = cs.textOverflow === 'ellipsis' && cs.whiteSpace === 'nowrap'
    if (clips(cs) && !ellipsized && cs.overflowX !== 'auto' && cs.overflowX !== 'scroll'
      && el.scrollWidth - el.clientWidth > 2 && el.clientWidth > 0) {
      issues.push({ kind: 'container-clip-x', px: el.scrollWidth - el.clientWidth, el: describe(el) })
    }

    // 3. 文本裁切：单行文本溢出但没有 ellipsis 处理
    const isLeaf = el.children.length === 0 && (el.textContent ?? '').trim().length > 0
    if (isLeaf) {
      const clippedX = el.scrollWidth - el.clientWidth > 1
      const clippedY = el.scrollHeight - el.clientHeight > 1
      const handled = cs.textOverflow === 'ellipsis' || cs.overflow === 'visible'
      if (clippedX && !handled) {
        issues.push({ kind: 'text-clip-x', px: el.scrollWidth - el.clientWidth, el: describe(el) })
      }
      if (clippedY && cs.overflowY === 'hidden' && !cs.webkitLineClamp) {
        issues.push({ kind: 'text-clip-y', px: el.scrollHeight - el.clientHeight, el: describe(el) })
      }
    }
  }

  // 4. 可交互元素：命中测试 + 触控尺寸
  // 模态/命令面板打开时，下方页面内容被 scrim 接管是设计意图；
  // 只报"同一层内互相遮挡"，即遮挡者不是覆盖全屏的高层浮层。
  const topLayer = [...document.querySelectorAll('*')].filter((el) => {
    const cs = getComputedStyle(el)
    if (cs.position !== 'fixed') return false
    if (Number(cs.zIndex) < 30) return false
    const r = el.getBoundingClientRect()
    return r.width >= vw - 2 && r.height >= vh - 2
  })
  const inTopLayer = (el) => topLayer.some((layer) => layer.contains(el))

  const interactive = [...document.querySelectorAll('button, a[href], input, select, textarea, [role="button"], [role="menuitem"], [role="tab"]')]
  for (const el of interactive) {
    const cs = getComputedStyle(el)
    if (cs.display === 'none' || cs.visibility === 'hidden') continue
    const r = el.getBoundingClientRect()
    if (r.width <= 0 || r.height <= 0) continue
    if (r.bottom < 0 || r.top > vh || r.right < 0 || r.left > vw) continue
    if (isOffCanvas(el)) continue
    // 浮层打开时，被 scrim 盖住的底层控件不算缺陷
    if (topLayer.length && !inTopLayer(el)) continue

    if (cs.position !== 'fixed' && el.type !== 'hidden' && el.tagName !== 'A') {
      // 复选框/单选框包在 <label> 里时，真实可点区域是 label 而不是 input 本体；
      // 按 input 的 16px 判定会把设计上合规的控件全报成过小。
      const host = el.closest('label') ?? el
      const hostRect = host.getBoundingClientRect()
      if (hostRect.width < 24 || hostRect.height < 24) {
        issues.push({ kind: 'target-too-small', px: `${Math.round(hostRect.width)}x${Math.round(hostRect.height)}`, el: describe(el) })
      }
    }

    const cx = Math.min(Math.max(r.left + r.width / 2, 1), vw - 1)
    const cy = Math.min(Math.max(r.top + r.height / 2, 1), vh - 1)
    // 铺满视口的 scrim 不是点目标：它的中心必然落在它上层的面板下面，
    // 对它做中心点命中测试只会得到假阳性。
    if (r.width >= vw - 2 && r.height >= vh - 2) continue
    const hit = document.elementFromPoint(cx, cy)
    if (hit && hit !== el && !el.contains(hit) && !hit.contains(el)) {
      const hitCs = getComputedStyle(hit)
      // 打开的菜单/弹窗/tooltip 浮在页面内容之上是设计意图，不是遮挡缺陷；
      // 只有"同一层内"互相压盖才需要报。
      const popover = hit.closest('[role="menu"], [role="dialog"], [role="tooltip"], [role="listbox"]')
      const fromPopover = popover !== null && !popover.contains(el)
      if (hitCs.pointerEvents !== 'none' && !fromPopover) {
        issues.push({ kind: 'interactive-occluded', px: '', el: describe(el), by: describe(hit) })
      }
    }
  }

  // 5. 浮层面板必须完整落在视口内，且过高时自身可滚动而不是溢出到视口外
  const panels = [...document.querySelectorAll('[role="dialog"], [role="menu"], [role="tooltip"], [role="listbox"], .tooltip-surface')]
  for (const el of panels) {
    const cs = getComputedStyle(el)
    if (cs.display === 'none' || cs.visibility === 'hidden' || cs.opacity === '0') continue
    const r = el.getBoundingClientRect()
    if (r.width <= 0 || r.height <= 0) continue
    if (r.right > vw + 1) issues.push({ kind: 'overlay-out-right', px: Math.round(r.right - vw), el: describe(el) })
    if (r.left < -1) issues.push({ kind: 'overlay-out-left', px: Math.round(-r.left), el: describe(el) })
    if (r.bottom > vh + 1) issues.push({ kind: 'overlay-out-bottom', px: Math.round(r.bottom - vh), el: describe(el) })
    if (r.top < -1) issues.push({ kind: 'overlay-out-top', px: Math.round(-r.top), el: describe(el) })
  }

  return issues
}

mkdirSync(outDir, { recursive: true })

const browser = await chromium.launch()
const password = envState?.password ?? readAdminPassword()
const report = []

async function login(page) {
  await page.goto(`${baseURL}/admin/login`, { waitUntil: 'domcontentloaded' })
  if (!page.url().includes('/admin/login')) return
  await page.locator('input[name="username"]').fill('admin')
  await page.locator('input[name="password"]').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
  // SPA 客户端跳转不触发 load 事件，waitForURL 的默认 waitUntil 会超时；
  // 直接轮询 page.url()。
  for (let i = 0; i < 60; i += 1) {
    if (!page.url().endsWith('/login')) return
    await page.waitForTimeout(250)
  }
  const shown = await page.locator('body').innerText()
  throw new Error(`login did not leave /admin/login; page says: ${shown.replace(/\s+/g, ' ').slice(0, 200)}`)
}

// 登录只做一次，之后每个视口/主题的 context 直接注入会话 Cookie：
// 管理端登录限流是每 IP+用户名 1 分钟 5 次，6 个 context 各自登录必然被拒。
async function captureSession() {
  const context = await browser.newContext()
  const page = await context.newPage()
  await login(page)
  const cookies = await context.cookies()
  await context.close()
  return cookies
}

const sessionCookies = await captureSession()

try {
  for (const theme of ['light', 'dark']) {
    for (const vp of VIEWPORTS) {
      const context = await browser.newContext({ viewport: { width: vp.width, height: vp.height } })
      await context.addCookies(sessionCookies)
      await context.addInitScript((t) => {
        try { window.localStorage.setItem('nvr-theme', t) } catch { /* ignore */ }
      }, theme)
      const page = await context.newPage()

      for (const target of PAGES) {
        if (only && !target.name.includes(only)) continue
        await page.goto(`${baseURL}${target.path}`, { waitUntil: 'domcontentloaded' })
        await page.waitForTimeout(1_600)
        const issues = await page.evaluate(AUDIT)
        const label = `${target.name}_${vp.name}_${theme}`
        await page.screenshot({ path: join(outDir, `${label}.png`), fullPage: true })
        report.push({ page: target.name, viewport: vp.name, theme, issues })
        const tag = issues.length ? `${issues.length} issue(s)` : 'clean'
        console.log(`${label.padEnd(40)} ${tag}`)
      }

      for (const scene of SCENES) {
        const name = `overlay-${scene.name}`
        if (only && !name.includes(only)) continue
        if (scene.only && !scene.only(vp)) continue
        await page.goto(`${baseURL}${scene.path}`, { waitUntil: 'domcontentloaded' })
        await page.waitForTimeout(1_200)
        let opened = false
        try {
          opened = await scene.open(page, vp)
        } catch (error) {
          report.push({ page: name, viewport: vp.name, theme, issues: [{ kind: 'scene-open-failed', px: '', el: String(error).slice(0, 160) }] })
          console.log(`${`${name}_${vp.name}_${theme}`.padEnd(40)} open failed`)
          continue
        }
        if (!opened) continue
        const issues = await page.evaluate(AUDIT)
        const label = `${name}_${vp.name}_${theme}`
        await page.screenshot({ path: join(outDir, `${label}.png`) })
        report.push({ page: name, viewport: vp.name, theme, issues })
        console.log(`${label.padEnd(40)} ${issues.length ? `${issues.length} issue(s)` : 'clean'}`)
      }
      await context.close()
    }
  }
} finally {
  await browser.close()
}

writeFileSync(join(outDir, 'report.json'), JSON.stringify(report, null, 2))

// 汇总：同一个缺陷会在 6 个视口×主题组合里各报一次，按「种类 + 元素签名」
// 折叠成一条，附上出现的页面与视口，才看得出哪些是真问题。
const groups = new Map()
for (const row of report) {
  for (const issue of row.issues) {
    const key = `${issue.kind} | ${issue.el} | ${issue.by ?? ''}`
    const entry = groups.get(key) ?? { kind: issue.kind, el: issue.el, by: issue.by, px: new Set(), pages: new Set(), viewports: new Set(), hits: 0 }
    entry.hits += 1
    entry.px.add(String(issue.px))
    entry.pages.add(row.page)
    entry.viewports.add(row.viewport)
    groups.set(key, entry)
  }
}
const byKind = new Map()
for (const entry of groups.values()) {
  const list = byKind.get(entry.kind) ?? []
  list.push(entry)
  byKind.set(entry.kind, list)
}
console.log('\n──── 汇总 ────')
if (byKind.size === 0) {
  console.log('无几何缺陷。')
} else {
  for (const [kind, list] of [...byKind.entries()].sort((a, b) => b[1].length - a[1].length)) {
    console.log(`\n${kind} — ${list.length} 处 / ${list.reduce((sum, e) => sum + e.hits, 0)} 次`)
    for (const entry of list.sort((a, b) => b.hits - a.hits).slice(0, 20)) {
      const pages = [...entry.pages]
      const where = pages.length > 4 ? `${pages.slice(0, 4).join(',')}+${pages.length - 4}` : pages.join(',')
      console.log(`  [${[...entry.px].join('/')}] ${entry.el}${entry.by ? ` << ${entry.by}` : ''}`)
      console.log(`      ${where} @ ${[...entry.viewports].join(',')}`)
    }
    if (list.length > 20) console.log(`  … 另有 ${list.length - 20} 处`)
  }
}
console.log(`\n截图与 report.json：${outDir}`)
