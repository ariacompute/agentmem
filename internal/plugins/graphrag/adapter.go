// Package graphrag is the agentmem plugin for the GraphRAG ecosystem.
package graphrag

import (
	_ "embed"
	"sync"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/plugins/manifest"
	"agentmem/internal/plugins/pluginbase"
)

//go:embed plugin.yaml
var manifestYAML []byte

var loadOnce struct {
	sync.Once
	desc manifest.Descriptor
	err  error
}

// Descriptor returns the validated plugin manifest.
func Descriptor() (manifest.Descriptor, error) {
	loadOnce.Do(func() { loadOnce.desc, loadOnce.err = manifest.Parse(manifestYAML) })
	return loadOnce.desc, loadOnce.err
}

// New is an adapter.Registry factory.
func New(cfg config.AdapterConfig, _ adapter.Deps) (adapter.Adapter, error) {
	d, err := Descriptor()
	if err != nil {
		return nil, err
	}
	return pluginbase.New(d, cfg)
}

// Register adds the engine-specific kind to the registry.
func Register(r *adapter.Registry) error { return r.RegisterKind("graphrag", New) }
