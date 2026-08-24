import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

function collectProductionSources(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const path = join(directory, name)
    if (statSync(path).isDirectory()) return collectProductionSources(path)
    if (name.endsWith('.spec.ts')) return []
    if (!name.endsWith('.css') && !name.endsWith('.ts') && !name.endsWith('.vue')) return []
    return [path]
  })
}

describe('Flat Outline visual baseline', () => {
  it('does not reintroduce material depth or decorative masks', () => {
    const sourceRoot = join(process.cwd(), 'src')
    const forbidden = [
      /box-shadow/i,
      /text-shadow/i,
      /drop-shadow/i,
      /backdrop-filter/i,
      /backdrop-blur/i,
      /mask-image/i,
      /animate-ping/i,
      /shadow-/i,
      // ring-* 工具类编译成 box-shadow，而且会和 theme.css 的
      // :focus-visible outline 叠成多层焦点指示。焦点统一用 outline 表达。
      /\bring-\d/,
    ]

    for (const path of collectProductionSources(sourceRoot)) {
      const source = readFileSync(path, 'utf8')
      for (const pattern of forbidden) {
        expect(source, path + ' contains ' + pattern).not.toMatch(pattern)
      }
    }
  })

  it('removes the authentication page decoration layer', () => {
    const source = readFileSync(join(process.cwd(), 'src/features/auth/AuthLayout.vue'), 'utf8')
    expect(source).not.toContain('auth-grid')
    expect(source).not.toContain('mask-image')
  })
})

describe('theme token discipline', () => {
  // presetUno 的 `dark:` 变体编译成 `.dark` 选择器，而本项目切换的是
  // [data-theme='dark']，两者永不相交——写下的 dark: 样式全是死代码，
  // 只会让组件在暗色主题下停留在亮色配色上。
  it('has no dark: variants, which never match this app theme switch', () => {
    const offenders = collectProductionSources(join(process.cwd(), 'src'))
      .filter((path) => /(?:^|[\s"'`:{[])dark:[a-z[]/.test(readFileSync(path, 'utf8')))
    expect(offenders).toEqual([])
  })

  // 调色板色阶不随主题切换，也不在 docs/前端对比度配对表.md 里登记；
  // 颜色只允许来自 theme.css 的语义 token。
  it('uses semantic tokens instead of raw palette colors', () => {
    const palette = /\b(?:text|bg|border|ring|from|to|via|accent|fill|stroke)-(?:slate|zinc|gray|neutral|stone|emerald|green|red|amber|yellow|blue|indigo|sky|violet|purple|fuchsia|pink|rose|orange|lime|teal|cyan)-\d{2,3}\b/
    const offenders = collectProductionSources(join(process.cwd(), 'src'))
      .filter((path) => palette.test(readFileSync(path, 'utf8')))
    expect(offenders).toEqual([])
  })
})
