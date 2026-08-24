import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { cwd } from 'node:process'

import { describe, expect, it } from 'vitest'

import unoConfig from '../../uno.config'

// uno.config.ts shortcuts 采用对象数组形式，且 variant 可组合基础类
//（如 btn-primary 引用 btn-base）；断言前先归一化并展开组合引用。
const rawShortcuts = unoConfig.shortcuts as Record<string, string>[] | Record<string, string>
const flat: Record<string, string> = Array.isArray(rawShortcuts)
  ? Object.assign({}, ...rawShortcuts)
  : rawShortcuts

function expand(name: string, seen: Set<string> = new Set()): string {
  if (seen.has(name)) return ''
  seen.add(name)
  const value = flat[name] ?? ''
  return value
    .split(/\s+/)
    .map((token) => (token in flat ? expand(token, seen) : token))
    .join(' ')
}

const shortcuts: Record<string, string> = Object.fromEntries(
  Object.keys(flat).map((name) => [name, expand(name)]),
)

function collectSourceFiles(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const path = join(directory, name)
    return statSync(path).isDirectory()
      ? collectSourceFiles(path)
      : /\.(ts|vue|css)$/.test(name)
        ? [path]
        : []
  })
}

describe('semantic control colors', () => {
  it('gives each filled status badge an explicit readable foreground', () => {
    expect(shortcuts['badge-success']).toContain('text-[var(--color-success-foreground)]')
    expect(shortcuts['badge-warning']).toContain('text-[var(--color-warning-foreground)]')
    expect(shortcuts['badge-danger']).toContain('text-[var(--color-danger-foreground)]')
    expect(shortcuts['badge-info']).toContain('text-[var(--color-info-foreground)]')
  })

  it('keeps filled primary controls on their dedicated foreground token', () => {
    expect(shortcuts['btn-primary']).toContain('text-[var(--color-accent-foreground)]')
  })

  it('does not make disabled controls readable only through opacity', () => {
    for (const name of ['btn-primary', 'btn-secondary', 'btn-danger', 'btn-ghost']) {
      expect(shortcuts[name]).toContain('disabled:text-[var(--color-disabled-foreground)]')
      expect(shortcuts[name]).not.toContain('disabled:opacity-40')
    }
  })

  it('does not use UnoCSS alpha modifiers on arbitrary color variables', () => {
    const stylesDirectory = join(cwd(), 'src', 'styles')
    const sourceFiles = [
      ...collectSourceFiles(join(stylesDirectory, '..')),
      join(stylesDirectory, '..', '..', 'uno.config.ts'),
    ]
    const unsupportedPattern = /(?:bg|border|text|ring|outline|decoration|fill|stroke)-\[var\(--color-[^)]+\)\]\/(?:\d+(?:\.\d+)?|\[[^\]]+\])/g
    const offenders = sourceFiles.flatMap((file) => {
      const source = readFileSync(file, 'utf8')
      return [...source.matchAll(unsupportedPattern)].map((match) => `${file}: ${match[0]}`)
    })

    expect(offenders).toEqual([])
  })

  it('keeps shared shortcuts on canonical radius tokens', () => {
    const offenders = Object.entries(shortcuts)
      .filter(([, value]) => /rounded-\[\d+px\]/.test(value))
      .map(([name, value]) => `${name}: ${value}`)

    expect(offenders).toEqual([])
  })
})
