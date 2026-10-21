import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// In development, run the Go server in --dev mode on 127.0.0.1:8443 and open
// http://localhost:5173. Vite forwards /api (including WebSockets) to it.
// changeOrigin stays false so the Host header matches the Origin header,
// which the server's origin check requires.
const backend = process.env.TILDE_BACKEND ?? 'http://127.0.0.1:8443'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: backend, ws: true, changeOrigin: false },
    },
  },
  test: {
    // Unit tests for stores and helpers; they need window and localStorage.
    // The Playwright UI tests in e2e/ are not Vitest's.
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
  },
})
