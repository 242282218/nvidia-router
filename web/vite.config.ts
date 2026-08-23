import { readdirSync, rmdirSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import UnoCSS from 'unocss/vite'
import { defineConfig, loadEnv, type Plugin } from 'vite'

const outDir = fileURLToPath(new URL('../internal/web/dist', import.meta.url))

function removeTree(dir: string): void {
  let entries
  try {
    entries = readdirSync(dir, { withFileTypes: true })
  } catch {
    return
  }
  for (const entry of entries) {
    const target = join(dir, entry.name)
    if (entry.isDirectory()) {
      removeTree(target)
    } else {
      unlinkSync(target)
    }
  }
  rmdirSync(dir)
}

// Vite silently skips emptyOutDir when outDir resolves outside its project
// root, so stale fingerprinted assets accumulated here and every one of them
// was embedded into the binary by //go:embed all:dist.
function cleanEmbeddedDist(): Plugin {
  return {
    name: 'nvidia-router-clean-embedded-dist',
    apply: 'build',
    enforce: 'pre',
    buildStart() {
      removeTree(outDir)
    },
  }
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const proxyOrigin = env.VITE_PROXY_ORIGIN || 'http://127.0.0.1:3756'

  return {
    plugins: [cleanEmbeddedDist(), vue(), UnoCSS()],
    server: {
      proxy: {
        '/admin/api': {
          target: proxyOrigin,
          changeOrigin: true,
          headers: {
            Origin: proxyOrigin,
          },
        },
      },
    },
    build: {
      outDir,
      emptyOutDir: true,
      rollupOptions: {
        output: {
          // Asset URLs must change when embedded CSS or JavaScript changes;
          // the Go handler marks /assets/* immutable for one year.
          entryFileNames: 'assets/[name]-[hash].js',
          chunkFileNames: 'assets/[name]-[hash].js',
          assetFileNames: 'assets/[name]-[hash][extname]',
        },
      },
    },
  }
})
