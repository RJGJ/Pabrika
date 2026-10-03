import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// Dev notes:
// - Open the app as exactly http://localhost:5173 (not 127.0.0.1) and run the Go server with
//   BASE_URL=http://localhost:5173 COOKIE_SECURE=false so the Origin check on mutations passes.
//   The proxy does not rewrite Origin.
// - Only /api is proxied (it also covers the SSE stream). /mcp is not used by the UI.
// - emptyOutDir is false so web/dist/.gitkeep survives; the prebuild script cleans dist instead.
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  base: '/',
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: false,
    sourcemap: false,
  },
  test: {
    environment: 'jsdom',
    include: ['tests/**/*.{test,spec}.ts'],
    globals: false,
    // First test in a file may import the whole app; jsdom startup on Windows exceeds the 5 s default.
    testTimeout: 30000,
    hookTimeout: 30000,
  },
})
