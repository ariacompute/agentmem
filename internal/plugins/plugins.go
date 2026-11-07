// Package plugins is the explicit registry of third-party engine plugins.
// Go .so plugins are deliberately not used: every engine is compiled into the
// single binary and registered by name.
package plugins

import (
	"fmt"
	"sort"

	"agentmem/internal/adapter"
	"agentmem/internal/plugins/graphrag"
	"agentmem/internal/plugins/lightrag"
	"agentmem/internal/plugins/llamaindex"
	"agentmem/internal/plugins/manifest"
	"agentmem/internal/plugins/pageindex"
)

// Entry is one compiled-in plugin.
type Entry struct {
	Name       string
	Register   func(*adapter.Registry) error
	Descriptor func() (manifest.Descriptor, error)
}

// All returns the plugin table in stable name order.
func All() []Entry {
	return []Entry{
		{Name: "graphrag", Register: graphrag.Register, Descriptor: graphrag.Descriptor},
		{Name: "lightrag", Register: lightrag.Register, Descriptor: lightrag.Descriptor},
		{Name: "llamaindex", Register: llamaindex.Register, Descriptor: llamaindex.Descriptor},
		{Name: "pageindex", Register: pageindex.Register, Descriptor: pageindex.Descriptor},
	}
}

// Kinds lists the configuration kinds owned by compiled-in plugins.
func Kinds() []string {
	entries := All()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

// RegisterAll registers every plugin kind. It is idempotent so the CLI and
// server entry points can both call it.
func RegisterAll(r *adapter.Registry) error {
	if r == nil {
		return fmt.Errorf("plugins: nil registry")
	}
	for _, e := range All() {
		if r.HasKind(e.Name) {
			continue
		}
		if err := e.Register(r); err != nil {
			return fmt.Errorf("plugins: register %s: %w", e.Name, err)
		}
	}
	return nil
}

// Descriptors returns all validated manifests in stable name order.
func Descriptors() ([]manifest.Descriptor, error) {
	entries := All()
	out := make([]manifest.Descriptor, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		d, err := e.Descriptor()
		if err != nil {
			return nil, fmt.Errorf("plugins: %s: %w", e.Name, err)
		}
		if d.Name != e.Name || d.Kind != e.Name || seen[d.Name] {
			return nil, fmt.Errorf("plugins: inconsistent or duplicate descriptor %q", d.Name)
		}
		seen[d.Name] = true
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
