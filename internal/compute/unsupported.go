package compute

import "context"

// unsupportedRuntime is a formal contract skeleton. It never succeeds and
// never silently degrades to another runtime: callers can detect it with
// errors.Is(err, compute.ErrUnsupported).
type unsupportedRuntime struct{ name string }

func (u *unsupportedRuntime) Name() string { return u.name }

func (u *unsupportedRuntime) Execute(_ context.Context, _ BoundComputation) (Receipt, error) {
	return Receipt{}, fail(ErrUnsupported,
		"runtime %q has a contract but no executor; only postgres executes", u.name)
}
