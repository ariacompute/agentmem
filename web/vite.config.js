import { defineConfig } from 'vite'

// The status page is a dependency-free ES module. Vite is only used to bundle
// static assets; the same page can be served directly from web/ without Node.
//
// The proxy keeps /healthz and /v1 on the page origin, which is why the CSP in
// index.html can restrict connect-src to 'self' and never needs the backend
// origin. Adding a proxy prefix here therefore asks for a matching CSP review.
//
// The build output goes straight into the Go package that embeds it
// (internal/webui/dist, `//go:embed all:dist`). One output directory means one
// copy: there is no separate artifact to keep in sync. The directory is
// committed so `go build` and `go test ./...` work on a machine without bun.
export default defineConfig({
  server: {
    proxy: {
      '/healthz': 'http://127.0.0.1:8080',
      '/v1': 'http://127.0.0.1:8080',
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    // outDir sits outside the Vite root on purpose, so the guard has to be
    // lifted explicitly; an empty run must still replace stale hashed assets.
    emptyOutDir: true,
    rollupOptions: { input: 'index.html' },
  },
})
