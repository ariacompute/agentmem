package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/compute"
	"agentmem/internal/config"
	"agentmem/internal/okf"
	"agentmem/internal/router"
)

func assembleOffline(t *testing.T) *KnowledgeService {
	t.Helper()
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	cfg.ACL.DefaultOwner = "tester"
	svc, err := Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return svc
}

func TestAssembleNilConfig(t *testing.T) {
	if _, err := Assemble(context.Background(), nil); err == nil {
		t.Fatal("nil config must error")
	}
}

func TestAssembleValidateError(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Mode = "bogus"
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("invalid mode must error")
	}
}

func TestAssembleSystemOneEgressError(t *testing.T) {
	cfg := config.Default()
	cfg.Router.SystemOne.Endpoint = "http://evil.example"
	cfg.Egress.AllowedHosts = []string{"good.example"}
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("disallowed system-one endpoint must error")
	}
}

func TestAssembleAdapterEgressError(t *testing.T) {
	cfg := config.Default()
	cfg.Adapters = append(cfg.Adapters, config.AdapterConfig{
		Name: "badhttp", Kind: "http", Enabled: true, Endpoint: "http://evil.example",
	})
	cfg.Egress.AllowedHosts = []string{"good.example"}
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("disallowed adapter endpoint must error")
	}
}

func TestServiceLifecycle(t *testing.T) {
	svc := assembleOffline(t)
	defer svc.Close()

	ctx := context.Background()
	if _, err := svc.Put(ctx, "alice", "metrics/x", &okf.Document{
		Type: "Metric", Title: "X", Body: "# x",
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := svc.Query(ctx, "alice", "x", 5); err != nil {
		t.Fatalf("query: %v", err)
	}
	if _, err := svc.RunMigration(ctx, router.MigrationOptions{
		Direction: router.DirectionToRAG, Target: "memory", Limit: 100,
	}); err != nil {
		t.Fatalf("migration: %v", err)
	}
	if _, err := svc.Decisions(ctx, "metrics/x", 10); err != nil {
		t.Fatalf("decisions: %v", err)
	}
	if _, err := svc.Concepts(ctx); err != nil {
		t.Fatalf("concepts: %v", err)
	}
	health := svc.Health(ctx)
	if health["status"] != "ok" && health["status"] != "degraded" {
		t.Fatalf("health = %+v", health)
	}
	if health["postgres"] != "fake (offline)" {
		t.Fatalf("postgres mode = %q", health["postgres"])
	}
}

func TestRunComputationMissingAndNilCompute(t *testing.T) {
	svc := assembleOffline(t)
	defer svc.Close()
	// Unknown concept id exercises the compute error path.
	if _, err := svc.RunComputation(context.Background(), compute.Request{
		ConceptID: "nope/missing", Subject: "tester", Parameters: map[string]any{},
	}); err == nil {
		t.Fatal("missing computation must error")
	}
	// Nil compute path.
	empty := &KnowledgeService{}
	if _, err := empty.RunComputation(context.Background(), compute.Request{
		ConceptID: "x", Subject: "tester", Parameters: map[string]any{},
	}); err == nil {
		t.Fatal("nil compute must error")
	}
	recs, err := empty.ComputationAudit(context.Background(), "x", 5)
	if err != nil || len(recs) != 0 {
		t.Fatalf("nil compute audit = %v (%v)", recs, err)
	}
}

// MigrationOptionsDefault and ComputeRequestFor mirror the production types so
// the test does not depend on unexported constructors.
func TestRunComputationOfflineSuccess(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "references/revenue.sql", "SELECT SUM(amount) FROM orders WHERE year = :year\n")
	writeFile(t, root, "computations/revenue.md", strings.ReplaceAll(`---
type: Attested Computation
title: Revenue
runtime: postgres
parameters:
  - name: year
    type: integer
    required: true
computation: references/revenue.sql
executor:
  resource: runtime/postgres.md
  receipt: [job_id, executed_sql, result]
---
# c
`, "runtime: postgres", "runtime: postgres"))
	cfg := config.Default()
	cfg.Git.Root = root
	svc, err := Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	defer svc.Close()
	env, err := svc.RunComputation(context.Background(), compute.Request{
		ConceptID: "computations/revenue", Subject: "tester", Parameters: map[string]any{"year": 2026},
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if !env.DisplayAllowed {
		t.Fatal("expected display allowed")
	}
	recs, err := svc.ComputationAudit(context.Background(), "computations/revenue", 5)
	if err != nil || len(recs) != 1 {
		t.Fatalf("audit = %v (%v)", recs, err)
	}
}

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
