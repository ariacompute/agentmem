import { defineConfig } from 'vite'

// The status page is a dependency-free ES module. Vite is only used to bundle
// static assets; the same page can be served directly from web/ without Node.
export default defineConfig({
  server: {
    proxy: {
      '/healthz': 'http://127.0.0.1:8080',
      '/v1': 'http://127.0.0.1:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: { input: 'index.html' },
  },
})
