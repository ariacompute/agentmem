package compute

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"agentmem/internal/meta"
)

// Runtime executes one bound computation and returns verifiable evidence.
type Runtime interface {
	Name() string
	Execute(ctx context.Context, b BoundComputation) (Receipt, error)
}

// RuntimeNames lists every runtime with a formal contract. Only "postgres"
// has an executor; the others are explicit Unsupported skeletons.
var RuntimeNames = []string{"bigquery", "dbt", "looker", "postgres", "python"}

// RuntimeRegistry is the explicit, immutable-at-runtime set of runtimes.
type RuntimeRegistry struct {
	mu       sync.RWMutex
	runtimes map[string]Runtime
}

// NewRuntimeRegistry registers all five runtime contracts. exec may be an
// offline fake; it is injected by the service, never constructed here.
func NewRuntimeRegistry(exec meta.SQLExecutor, maxRows int) *RuntimeRegistry {
	r := &RuntimeRegistry{runtimes: make(map[string]Runtime, len(RuntimeNames))}
	_ = r.Register(&postgresRuntime{exec: exec, maxRows: maxRows})
	for _, name := range RuntimeNames {
		if name == "postgres" {
			continue
		}
		_ = r.Register(&unsupportedRuntime{name: name})
	}
	return r
}

func (r *RuntimeRegistry) Register(rt Runtime) error {
	if rt == nil || rt.Name() == "" {
		return fmt.Errorf("compute: invalid runtime")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.runtimes[rt.Name()]; exists {
		return fmt.Errorf("compute: runtime %q already registered", rt.Name())
	}
	r.runtimes[rt.Name()] = rt
	return nil
}

func (r *RuntimeRegistry) Get(name string) (Runtime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.runtimes[name]
	return rt, ok
}

// Names returns the registered runtime names in stable order.
func (r *RuntimeRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.runtimes))
	for n := range r.runtimes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
