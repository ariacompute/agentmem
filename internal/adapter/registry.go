package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"agentmem/internal/config"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// Deps are the injected lower-layer ports. No concrete backend (gorm, HTTP,
// file system beyond the bundle) is constructed inside the registry itself.
type Deps struct {
	Bundle   *okf.Bundle
	Vector   meta.VectorStore
	Embedder Embedder
	Meta     meta.MetaStore
	// AllowedHosts is the closed outbound allowlist shared by HTTP bridges.
	AllowedHosts []string
}

// Factory builds one adapter from configuration and injected dependencies.
type Factory func(cfg config.AdapterConfig, deps Deps) (Adapter, error)

// Registry is an explicit, configuration-driven plugin registry. Go .so
// plugins are deliberately avoided: they are fragile across platforms.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	adapters  map[string]Adapter
}

func NewRegistry() *Registry {
	r := &Registry{factories: map[string]Factory{}, adapters: map[string]Adapter{}}
	_ = r.RegisterKind("okf", func(_ config.AdapterConfig, d Deps) (Adapter, error) {
		if d.Bundle == nil {
			return nil, fmt.Errorf("adapter: okf requires a bundle")
		}
		return NewOKFAdapter(d.Bundle), nil
	})
	_ = r.RegisterKind("memory", func(_ config.AdapterConfig, d Deps) (Adapter, error) {
		return NewMemoryAdapter(d.Embedder), nil
	})
	_ = r.RegisterKind("pgvector", func(_ config.AdapterConfig, d Deps) (Adapter, error) {
		v := d.Vector
		if v == nil {
			// Offline fallback, explicitly logged so it is never a silent no-op.
			slog.Warn("adapter: pgvector DSN absent, using in-memory fake vector store")
			v = meta.NewFakeStore()
		}
		return NewPGVectorAdapter(v, d.Embedder)
	})
	return r
}

// RegisterKind adds a factory for a configuration kind. Duplicate kinds fail.
func (r *Registry) RegisterKind(kind string, f Factory) error {
	if kind == "" || f == nil {
		return fmt.Errorf("adapter: invalid factory for kind %q", kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[kind]; exists {
		return fmt.Errorf("adapter: kind %q already registered", kind)
	}
	r.factories[kind] = f
	return nil
}

// HasKind reports whether a factory for the given kind is registered.
func (r *Registry) HasKind(kind string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[kind]
	return ok
}

// RegisterAdapter injects an already-built adapter (used by tests and bridges).
func (r *Registry) RegisterAdapter(a Adapter) error {
	if a == nil {
		return fmt.Errorf("adapter: nil adapter")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[a.Name()]; exists {
		return fmt.Errorf("adapter: name %q already registered", a.Name())
	}
	r.adapters[a.Name()] = a
	return nil
}

// Build assembles all enabled adapters from configuration.
func (r *Registry) Build(cfg *config.Config, deps Deps) error {
	if cfg == nil {
		return fmt.Errorf("adapter: nil config")
	}
	if deps.Embedder == nil {
		deps.Embedder = NewHashingEmbedder(64)
	}
	for _, a := range cfg.Adapters {
		if !a.Enabled {
			continue
		}
		r.mu.RLock()
		_, exists := r.adapters[a.Name]
		f, ok := r.factories[a.Kind]
		r.mu.RUnlock()
		if exists {
			return fmt.Errorf("adapter: name %q already built", a.Name)
		}
		if !ok {
			return fmt.Errorf("%w: adapter %q kind %q has no factory", ErrUnsupported, a.Name, a.Kind)
		}
		// Inject the global egress allowlist into each adapter without widening
		// the .env surface (EgressHosts is a runtime-only field).
		a.EgressHosts = deps.AllowedHosts
		built, err := f(a, deps)
		if err != nil {
			return fmt.Errorf("adapter: build %q: %w", a.Name, err)
		}
		if built == nil {
			return fmt.Errorf("adapter: factory for %q returned nil", a.Name)
		}
		if err := r.RegisterAdapter(built); err != nil {
			return err
		}
	}
	return nil
}

// Get looks up a built adapter by name.
func (r *Registry) Get(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// Names returns sorted registered adapter names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for n := range r.adapters {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// All returns adapters in name order for deterministic routing.
func (r *Registry) All() []Adapter {
	out := make([]Adapter, 0, len(r.Names()))
	for _, n := range r.Names() {
		if a, ok := r.Get(n); ok {
			out = append(out, a)
		}
	}
	return out
}

// HealthAll checks every adapter. Errors are aggregated, not swallowed.
func (r *Registry) HealthAll(ctx context.Context) map[string]error {
	out := map[string]error{}
	for _, a := range r.All() {
		out[a.Name()] = a.Health(ctx)
	}
	return out
}
