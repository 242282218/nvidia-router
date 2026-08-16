import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { cwd } from 'node:process'

import { describe, expect, it } from 'vitest'

const viteConfigSource = readFileSync(join(cwd(), 'vite.config.ts'), 'utf8')

describe('embedded asset cache compatibility', () => {
  it('fingerprints assets before the handler marks them immutable', () => {
    expect(viteConfigSource).toContain("entryFileNames: 'assets/[name]-[hash].js'")
    expect(viteConfigSource).toContain("chunkFileNames: 'assets/[name]-[hash].js'")
    expect(viteConfigSource).toContain("assetFileNames: 'assets/[name]-[hash][extname]'")
  })
})
