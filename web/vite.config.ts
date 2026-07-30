import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import UnoCSS from 'unocss/vite'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [vue(), UnoCSS()],
  build: {
    outDir: fileURLToPath(new URL('../internal/web/dist', import.meta.url)),
    emptyOutDir: true,
  },
})
