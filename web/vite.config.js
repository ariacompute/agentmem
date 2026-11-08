import { defineConfig } from 'vite'

// The status page is a dependency-free ES module. Vite is only used to bundle
// static assets; the same page can be served directly from web/ without Node.
//
// The proxy keeps /healthz and /v1 on the page origin, which is why the CSP in
// index.html can restrict connect-src to 'self' and never needs the backend
// origin. Adding a proxy prefix here therefore asks for a matching CSP review.
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
