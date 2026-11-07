package compute

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// Request is a computation invocation. Parameters may only contain declared
// names with typed values; computation text is never accepted.
type Request struct {
	ConceptID  string
	Subject    string
	Parameters map[string]any
}

// Envelope is the only response shape. A rejected computation carries no
// DisplayValue and DisplayAllowed=false, so a caller cannot accidentally show
// an unverified number.
type Envelope struct {
	ConceptID      string  `json:"concept_id"`
	Runtime        string  `json:"runtime"`
	ContractHash   string  `json:"contract_hash"`
	ParamsDigest   string  `json:"params_digest"`
	JobID          string  `json:"job_id"`
	DisplayAllowed bool    `json:"display_allowed"`
	DisplayValue   string  `json:"display_value,omitempty"`
	Verified       bool    `json:"verified"`
	Stale          bool    `json:"stale"`
	Verdict        Verdict `json:"verdict"`
	ErrorClass     string  `json:"error_class,omitempty"`
}

// Service orchestrates load -> bind -> execute -> attest -> gate -> audit.
type Service struct {
	Bundle   *okf.Bundle
	Runtimes *RuntimeRegistry
	Audit    meta.MetaStore
	Timeout  time.Duration
	MaxRows  int
	Now      func() time.Time
}

// NewService builds a computation service with defaults for zero values.
func NewService(b *okf.Bundle, r *RuntimeRegistry, audit meta.MetaStore, timeout time.Duration, maxRows int) *Service {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if maxRows <= 0 {
		maxRows = 100
	}
	return &Service{Bundle: b, Runtimes: r, Audit: audit, Timeout: timeout, MaxRows: maxRows}
}

// Run executes one Attested Computation. Every outcome, including failures,
// is written to the audit store. Attestation failure is a valid run outcome
// (nil error) whose envelope explicitly refuses to display a value.
func (s *Service) Run(ctx context.Context, req Request) (Envelope, error) {
	env := Envelope{ConceptID: req.ConceptID, Verdict: Verdict{Reasons: []string{}}}
	if req.ConceptID == "" {
		err := fail(ErrContract, "concept_id is required")
		return s.finish(ctx, req, env, "error_"+Class(err), err)
	}
	c, err := LoadContract(s.Bundle, req.ConceptID)
	if err != nil {
		return s.finish(ctx, req, env, "error_"+Class(err), err)
	}
	env.Runtime, env.ContractHash, env.Verified = c.Runtime, c.ContractHash, c.Verified

	bound, err := Bind(c, req.Parameters)
	if err != nil {
		return s.finish(ctx, req, env, "error_"+Class(err), err)
	}
	env.ParamsDigest = bound.ParamsDigest

	rt, ok := s.Runtimes.Get(c.Runtime)
	if !ok {
		err := fail(ErrUnsupported, "runtime %q has no registered contract", c.Runtime)
		return s.finish(ctx, req, env, "error_"+Class(err), err)
	}
	runCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	receipt, err := rt.Execute(runCtx, bound)
	if err != nil {
		return s.finish(ctx, req, env, "error_"+Class(err), err)
	}
	env.JobID = receipt.JobID
	verdict, attestErr := (Attester{Now: s.Now}).Attest(bound, receipt)
	env.Verdict, env.Stale = verdict, verdict.Stale
	if attestErr != nil {
		env.DisplayAllowed = false
		env.ErrorClass = Class(attestErr)
		return s.finish(ctx, req, env, "rejected", nil)
	}
	env.DisplayAllowed = true
	env.DisplayValue = receipt.DisplayValue
	return s.finish(ctx, req, env, "passed", nil)
}

// List returns the computation audit trail, newest first.
func (s *Service) List(ctx context.Context, conceptID string, limit int) ([]meta.ComputationRecord, error) {
	if s.Audit == nil {
		return []meta.ComputationRecord{}, nil
	}
	out, err := s.Audit.ListComputations(ctx, meta.ComputationQuery{ConceptID: conceptID, Limit: limit})
	if out == nil {
		out = []meta.ComputationRecord{}
	}
	return out, err
}

func (s *Service) finish(ctx context.Context, req Request, env Envelope, verdict string, runErr error) (Envelope, error) {
	if env.ErrorClass == "" && runErr != nil {
		env.ErrorClass = Class(runErr)
	}
	jobID := env.JobID
	if jobID == "" {
		jobID = "pending-" + meta.DigestBytes(env.ConceptID, env.Runtime, verdict)[:24]
	}
	rec := meta.ComputationRecord{
		ConceptID: env.ConceptID, Subject: req.Subject, Runtime: env.Runtime,
		ContractHash: env.ContractHash, ParamsDigest: env.ParamsDigest,
		JobID: jobID, Verdict: verdict, DisplayAllowed: env.DisplayAllowed,
		Provenance: env.Verdict.Provenance, Fidelity: env.Verdict.Fidelity,
		Stale: env.Stale, Reasons: meta.StringList(env.Verdict.Reasons),
		ErrorClass: env.ErrorClass,
	}
	if s.Audit != nil {
		if err := s.Audit.AppendComputation(ctx, rec); err != nil {
			// Never log bodies, SQL, parameters, results or credentials.
			slog.Warn("compute: audit write failed", "class", env.ErrorClass)
			if runErr != nil {
				runErr = errors.Join(runErr, err)
			} else {
				runErr = err
			}
		}
	}
	return env, runErr
}
