package bridges

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed engines/*.yaml
var engineFS embed.FS

type engineCaps struct {
	Vector    bool `yaml:"vector"`
	Semantic  bool `yaml:"semantic"`
	Graph     bool `yaml:"graph"`
	PageLevel bool `yaml:"page_level"`
}

type engineSpecYAML struct {
	Name        string     `yaml:"name"`
	Kind        string     `yaml:"kind"`
	Endpoint    string     `yaml:"endpoint"`
	Command     string     `yaml:"command"`
	TimeoutMS   int        `yaml:"timeout_ms"`
	OfflineMock bool       `yaml:"offline_mock"`
	Notes       string     `yaml:"notes"`
	RawCaps     engineCaps `yaml:"capabilities"`
}

// EngineSpec describes one third-party engine's bridge configuration.
type EngineSpec struct {
	Name         string
	Kind         string
	Endpoint     string
	Command      string
	TimeoutMS    int
	OfflineMock  bool
	Notes        string
	Capabilities adapter.Capabilities
}

// EngineSpecs loads and validates the embedded engine declarations.
func EngineSpecs() ([]EngineSpec, error) {
	entries, err := engineFS.ReadDir("engines")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]EngineSpec, 0, len(names))
	for _, name := range names {
		data, err := engineFS.ReadFile("engines/" + name)
		if err != nil {
			return nil, err
		}
		var raw engineSpecYAML
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("bridges: parse %s: %w", name, err)
		}
		if raw.Name == "" || raw.Kind == "" {
			return nil, fmt.Errorf("bridges: %s missing name/kind", name)
		}
		out = append(out, EngineSpec{
			Name: raw.Name, Kind: raw.Kind, Endpoint: raw.Endpoint,
			Command: raw.Command, TimeoutMS: raw.TimeoutMS, OfflineMock: raw.OfflineMock,
			Notes: raw.Notes,
			Capabilities: adapter.Capabilities{
				Vector: raw.RawCaps.Vector, Semantic: raw.RawCaps.Semantic,
				Graph: raw.RawCaps.Graph, PageLevel: raw.RawCaps.PageLevel,
			},
		})
	}
	return out, nil
}

// SpecFor finds an engine declaration by adapter name.
func SpecFor(name string) (EngineSpec, error) {
	specs, err := EngineSpecs()
	if err != nil {
		return EngineSpec{}, err
	}
	for _, s := range specs {
		if s.Name == name {
			return s, nil
		}
	}
	return EngineSpec{}, fmt.Errorf("bridges: no engine spec for %q", name)
}

// RegisterKinds adds HTTP/CLI factories to the plugin registry. It is
// idempotent so the CLI and server entry points can both call it.
func RegisterKinds(r *adapter.Registry) error {
	if !r.HasKind("http") {
		if err := r.RegisterKind("http", func(cfg config.AdapterConfig, _ adapter.Deps) (adapter.Adapter, error) {
			spec, err := SpecFor(cfg.Name)
			if err != nil {
				return nil, err
			}
			return NewHTTPBridge(cfg, spec)
		}); err != nil {
			return err
		}
	}
	if !r.HasKind("cli") {
		if err := r.RegisterKind("cli", func(cfg config.AdapterConfig, _ adapter.Deps) (adapter.Adapter, error) {
			spec, err := SpecFor(cfg.Name)
			if err != nil {
				return nil, err
			}
			return NewCLIBridge(cfg, spec)
		}); err != nil {
			return err
		}
	}
	return nil
}
