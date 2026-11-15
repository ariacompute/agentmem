// Package webui serves the agentmem console that is compiled into the binary.
//
// The console itself stays a dependency-free ES module page: `cd web && bun run
// build` bundles it straight into internal/webui/dist (see web/vite.config.js),
// and `//go:embed` turns that directory into the running binary. One output
// directory means one copy — there is no separate artifact to keep in sync, and
// the committed `dist/` keeps `go build` and `go test ./...` working on a
// machine without bun or a network.
//
// The package is a leaf: it imports nothing from agentmem, so handlers, cli and
// cmd can depend on it without inverting the layering.
package webui

import (
	"embed"
	"io/fs"
)

// dist is the Vite output directory. `all:` is required so files whose names
// start with "." or "_" survive, which keeps future asset naming free.
//
//go:embed all:dist
var dist embed.FS

// FS returns the console site root: index.html plus the hashed assets.
func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
