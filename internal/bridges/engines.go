package bridges

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"gopkg.in/yaml.v3"
)

//go:embed engines/*.yaml
var embeddedEngines embed.FS

// engineFS is the declaration source. Production reads the embedded YAML;
// tests replace it to exercise the fail-closed parse and validation paths.
var engineFS fs.FS = embeddedEngines

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
	entries, err := fs.ReadDir(engineFS, "engines")
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
		data, err := fs.ReadFile(engineFS, "engines/"+name)
		if err != nil {
			return nil, err
		}
		var raw engineSpecYAML
		// Embedded declarations are code, not user data: unknown keys and
		// multiple documents must fail closed rather than be silently ignored.
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("bridges: parse %s: %w", name, err)
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("bridges: %s must contain exactly one YAML document", name)
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

// kindFactories is the bridge kind table in registration order.
var kindFactories = []struct {
	kind    string
	factory adapter.Factory
}{
	{"http", func(cfg config.AdapterConfig, _ adapter.Deps) (adapter.Adapter, error) {
		spec, err := SpecFor(cfg.Name)
		if err != nil {
			return nil, err
		}
		return NewHTTPBridge(cfg, spec)
	}},
	{"cli", func(cfg config.AdapterConfig, _ adapter.Deps) (adapter.Adapter, error) {
		spec, err := SpecFor(cfg.Name)
		if err != nil {
			return nil, err
		}
		return NewCLIBridge(cfg, spec)
	}},
}

// RegisterKinds adds HTTP/CLI factories to the plugin registry. It is
// idempotent so the CLI and server entry points can both call it.
func RegisterKinds(r *adapter.Registry) error {
	if r == nil {
		return errors.New("bridges: nil registry")
	}
	for _, k := range kindFactories {
		if r.HasKind(k.kind) {
			continue
		}
		if err := r.RegisterKind(k.kind, k.factory); err != nil {
			return err
		}
	}
	return nil
}
