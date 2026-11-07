package compute

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentmem/internal/meta"
)

type tamperRuntime struct{ mutate func(*Receipt) }

func (t *tamperRuntime) Name() string { return "postgres" }

func (t *tamperRuntime) Execute(ctx context.Context, b BoundComputation) (Receipt, error) {
	r, err := (&postgresRuntime{exec: FakeExecutor{}, maxRows: 10}).Execute(ctx, b)
	if err != nil {
		return r, err
	}
	t.mutate(&r)
	return r, nil
}

func tamperedRegistry(mutate func(*Receipt)) *RuntimeRegistry {
	r := &RuntimeRegistry{runtimes: map[string]Runtime{}}
	_ = r.Register(&tamperRuntime{mutate: mutate})
	return r
}

func TestServiceFullChainAndAudit(t *testing.T) {
	b := testBundle(t)
	store := meta.NewFakeStore()
	svc := NewService(b, NewRuntimeRegistry(FakeExecutor{}, 10), store, time.Second, 10)
	env, err := svc.Run(context.Background(), Request{
		ConceptID: "computations/revenue", Subject: "analyst:1",
		Parameters: map[string]any{"year": 2026},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !env.DisplayAllowed || env.DisplayValue == "" || !env.Verdict.Passed || !env.Verified {
		t.Fatalf("envelope = %+v", env)
	}
	if env.JobID == "" || env.ContractHash == "" || env.ParamsDigest == "" {
		t.Fatalf("missing provenance in envelope: %+v", env)
	}
	records, err := svc.List(context.Background(), "computations/revenue", 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("audit: %v / %+v", err, records)
	}
	rec := records[0]
	if rec.Verdict != "passed" || !rec.DisplayAllowed || !rec.Provenance || !rec.Fidelity ||
		rec.Subject != "analyst:1" || rec.ContractHash != env.ContractHash {
		t.Fatalf("audit record = %+v", rec)
	}
	if strings.Contains(rec.ErrorClass, "sql") || rec.JobID == "" {
		t.Fatalf("audit record leaked class or lost job id: %+v", rec)
	}
	if _, err := svc.Run(context.Background(), Request{
		ConceptID: "computations/revenue", Parameters: map[string]any{"year": 2026},
	}); err != nil {
		t.Fatal(err)
	}
	records, _ = svc.List(context.Background(), "", 10)
	if len(records) != 1 {
		t.Fatalf("identical runs must be idempotent in the audit, got %d", len(records))
	}
}

func TestServiceUnsupportedAndInvalidAreAudited(t *testing.T) {
	b := testBundle(t)
	writeResource(t, b, "computations/bigquery.md", strings.Replace(postgresDoc,
		"runtime: postgres", "runtime: bigquery", 1))
	store := meta.NewFakeStore()
	svc := NewService(b, NewRuntimeRegistry(FakeExecutor{}, 10), store, time.Second, 10)
	cases := []struct {
		name  string
		req   Request
		want  error
		class string
	}{
		{"unsupported runtime", Request{ConceptID: "computations/bigquery", Parameters: map[string]any{"year": 2026}}, ErrUnsupported, "unsupported"},
		{"bad params", Request{ConceptID: "computations/revenue", Parameters: map[string]any{"year": "oops"}}, ErrInvalidParams, "invalid_params"},
		{"rewrite attempt", Request{ConceptID: "computations/revenue", Parameters: map[string]any{"year": 2026, "sql": "SELECT 1"}}, ErrComputationRewrite, "computation_rewrite"},
		{"missing contract", Request{ConceptID: "computations/absent"}, ErrContract, "contract"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := svc.Run(context.Background(), tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if env.DisplayAllowed || env.DisplayValue != "" || env.ErrorClass != tc.class {
				t.Fatalf("failed run must not display a value: %+v", env)
			}
		})
	}
	records, err := svc.List(context.Background(), "", 50)
	if err != nil || len(records) != len(cases) {
		t.Fatalf("every failure must leave an audit record: %v / %d", err, len(records))
	}
}

func TestServiceRejectsDisplayOnAttestationFailure(t *testing.T) {
	b := testBundle(t)
	store := meta.NewFakeStore()
	svc := NewService(b, tamperedRegistry(func(r *Receipt) {
		r.DisplayValue = "999999999"
	}), store, time.Second, 10)
	env, err := svc.Run(context.Background(), Request{
		ConceptID: "computations/revenue", Parameters: map[string]any{"year": 2026},
	})
	if err != nil {
		t.Fatalf("attestation rejection is a run outcome, not a transport error: %v", err)
	}
	if env.DisplayAllowed || env.DisplayValue != "" || env.ErrorClass != "attestation_failed" || env.Verdict.Passed {
		t.Fatalf("unverified value was exposed: %+v", env)
	}
	if len(env.Verdict.Reasons) == 0 {
		t.Fatal("rejection must explain itself")
	}
	records, _ := svc.List(context.Background(), "", 10)
	if len(records) != 1 || records[0].Verdict != "rejected" || records[0].DisplayAllowed {
		t.Fatalf("audit = %+v", records)
	}
}

func TestServiceRejectsForgedSQL(t *testing.T) {
	b := testBundle(t)
	svc := NewService(b, tamperedRegistry(func(r *Receipt) {
		r.ExecutedSQL = "SELECT 1 FROM orders"
	}), meta.NewFakeStore(), time.Second, 10)
	env, err := svc.Run(context.Background(), Request{
		ConceptID: "computations/revenue", Parameters: map[string]any{"year": 2026},
	})
	if err != nil || env.DisplayAllowed || env.Verdict.Provenance {
		t.Fatalf("forged SQL accepted: %+v / %v", env, err)
	}
}
