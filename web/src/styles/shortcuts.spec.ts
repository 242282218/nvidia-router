import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { cwd } from 'node:process'

import { describe, expect, it } from 'vitest'

import unoConfig from '../../uno.config'

const shortcuts = unoConfig.shortcuts as Record<string, string>

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
})
