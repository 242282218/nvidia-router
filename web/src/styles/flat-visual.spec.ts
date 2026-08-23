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
