// Package compute executes and attests OKF Attested Computations. The
// sanctioned body always comes from OKF+Git; an agent can only supply declared
// parameter values. Nothing here imports gorm: SQL execution is a meta port.
package compute

import "fmt"

// Error is a typed computation failure. errors.Is works across instances by
// Kind, so callers can distinguish rejections without parsing messages.
type Error struct {
	Kind    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "compute: " + e.Kind
	}
	return "compute: " + e.Kind + ": " + e.Message
}

func (e *Error) Is(target error) bool {
	o, ok := target.(*Error)
	return ok && o.Kind == e.Kind
}

var (
	// ErrUnsupported covers runtimes whose contract exists but no executor.
	ErrUnsupported = &Error{Kind: "unsupported"}
	// ErrInvalidParams covers missing, mistyped or undeclared parameter values.
	ErrInvalidParams = &Error{Kind: "invalid_params"}
	// ErrComputationRewrite covers an attempt to supply computation text.
	ErrComputationRewrite = &Error{Kind: "computation_rewrite"}
	// ErrAttestationFailed is returned when provenance or fidelity fails.
	ErrAttestationFailed = &Error{Kind: "attestation_failed"}
	// ErrContract covers an absent or unsafe sanctioned computation.
	ErrContract = &Error{Kind: "contract"}
	// ErrExecution covers an executor failure.
	ErrExecution = &Error{Kind: "execution"}
)

func fail(kind *Error, format string, args ...any) error {
	return &Error{Kind: kind.Kind, Message: fmt.Sprintf(format, args...)}
}

// Class returns a stable, log-safe class name for an error.
func Class(err error) string {
	if err == nil {
		return "passed"
	}
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return "internal"
}
