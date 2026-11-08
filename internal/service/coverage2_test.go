package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/compute"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

type unhealthyAdapter struct{}

func (unhealthyAdapter) Name() string                                     { return "bad" }
func (unhealthyAdapter) Capabilities() adapter.Capabilities               { return adapter.Capabilities{} }
func (unhealthyAdapter) Put(context.Context, string, *okf.Document) error { return nil }
func (unhealthyAdapter) Query(context.Context, adapter.Query) ([]adapter.Hit, error) {
	return nil, nil
}
func (unhealthyAdapter) Delete(context.Context, string) error { return nil }
func (unhealthyAdapter) Fingerprint() string                  { return "bad-v1" }
func (unhealthyAdapter) Health(context.Context) error         { return errors.New("unhealthy") }

type closerAdapter struct{ closeErr error }

func (closerAdapter) Name() string                                     { return "closer" }
func (closerAdapter) Capabilities() adapter.Capabilities               { return adapter.Capabilities{} }
func (closerAdapter) Put(context.Context, string, *okf.Document) error { return nil }
func (closerAdapter) Query(context.Context, adapter.Query) ([]adapter.Hit, error) {
	return nil, nil
}
func (closerAdapter) Delete(context.Context, string) error { return nil }
func (closerAdapter) Fingerprint() string                  { return "closer-v1" }
func (closerAdapter) Health(context.Context) error         { return nil }
func (closerAdapter) Start(context.Context) error          { return nil }
func (c closerAdapter) Close(context.Context) error        { return c.closeErr }

func TestHealthDegradedAndConfigured(t *testing.T) {
	cfg := config.Default()
	r := adapter.NewRegistry()
	_ = r.RegisterAdapter(unhealthyAdapter{})
	svc := &KnowledgeService{Config: cfg, Registry: r}
	h := svc.Health(context.Background())
	if h["status"] != "degraded" || h["adapter:bad"] != "unhealthy" {
		t.Fatalf("health = %+v", h)
	}

	cfg2 := config.Default()
	cfg2.Postgres.DSN = "postgres://x"
	svc2 := &KnowledgeService{Config: cfg2, Registry: adapter.NewRegistry()}
	if got := svc2.Health(context.Background())["postgres"]; got != "configured" {
		t.Fatalf("postgres = %q", got)
	}
}

func TestCloseAggregatesErrors(t *testing.T) {
	r := adapter.NewRegistry()
	_ = r.RegisterAdapter(closerAdapter{closeErr: errors.New("registry close")})
	svc := &KnowledgeService{Registry: r, closer: func() error { return errors.New("closer") }}
	if err := svc.Close(); err == nil {
		t.Fatal("close must aggregate both errors")
	}
	// Nil registry and nil closer are safe.
	if err := (&KnowledgeService{}).Close(); err != nil {
		t.Fatalf("empty close = %v", err)
	}
}

func TestUnassembledCompute(t *testing.T) {
	svc := &KnowledgeService{}
	if _, err := svc.RunComputation(context.Background(), compute.Request{ConceptID: "x"}); err == nil {
		t.Fatal("unassembled compute must error")
	}
	recs, err := svc.ComputationAudit(context.Background(), "x", 5)
	if err != nil || len(recs) != 0 {
		t.Fatalf("unassembled audit = %+v (%v)", recs, err)
	}
}

func TestAssemblePostgresOpenError(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Root = filepath.Join(t.TempDir(), "bundle")
	// A malformed port makes meta.OpenPostgres fail synchronously, exercising
	// the Postgres branch of Assemble without a database.
	cfg.Postgres.DSN = "host=x port=notanumber"
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("postgres open error must propagate from Assemble")
	}
}
