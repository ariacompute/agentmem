package adapter

import (
	"context"
	"errors"
	"fmt"
)

// Lifecycle is an optional port implemented by adapters that own resources
// (sidecar connections, subprocesses, caches). The registry detects it by
// type assertion; adapters that do not need it stay unchanged.
//
// Start must verify the channel is usable and fail closed on error. Close must
// be idempotent: a second call returns nil without touching the backend.
type Lifecycle interface {
	Start(ctx context.Context) error
	Close(ctx context.Context) error
}

// StartAll starts lifecycle-aware adapters in name order. The first failure
// aborts assembly: a configured but unreachable sidecar must never degrade
// into a silent in-process fallback.
func (r *Registry) StartAll(ctx context.Context) error {
	for _, a := range r.All() {
		l, ok := a.(Lifecycle)
		if !ok {
			continue
		}
		if err := l.Start(ctx); err != nil {
			return fmt.Errorf("adapter: start %s: %w", a.Name(), err)
		}
	}
	return nil
}

// Close releases lifecycle-aware adapters in reverse name order. All adapters
// are attempted even if one fails; errors are aggregated, never swallowed.
func (r *Registry) Close(ctx context.Context) error {
	all := r.All()
	var errs []error
	for i := len(all) - 1; i >= 0; i-- {
		l, ok := all[i].(Lifecycle)
		if !ok {
			continue
		}
		if err := l.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("adapter: close %s: %w", all[i].Name(), err))
		}
	}
	return errors.Join(errs...)
}
