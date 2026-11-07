package router

import (
	"context"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/store"
)

func seedMigrationFixture(t *testing.T) (*Migrator, *store.OKFStore) {
	t.Helper()
	w, s, r := newWriteFixture(t)
	for _, id := range []string{"metrics/a", "metrics/b"} {
		if _, err := w.Put(context.Background(), "alice", id, simpleDoc("Metric", "definition of "+id)); err != nil {
			t.Fatal(err)
		}
	}
	return NewMigrator(s, r), s
}

func migrationOpts(direction string) MigrationOptions {
	return MigrationOptions{Job: "test", Direction: direction, Target: "memory", Limit: 10}
}

func TestMigrationDryRunDoesNotWrite(t *testing.T) {
	m, s := seedMigrationFixture(t)
	ctx := context.Background()
	opt := migrationOpts(DirectionToRAG)
	opt.DryRun = true
	rep, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Scanned != 2 || rep.Planned != 2 || rep.Synced != 0 {
		t.Fatalf("dry-run report = %+v", rep)
	}
	projections, _ := s.Meta.ListProjections(ctx, meta.ProjectionQuery{Adapter: "memory"})
	if len(projections) != 0 {
		t.Fatal("dry-run wrote projections")
	}
	c, _ := s.Meta.MigrationCursor(ctx, "test")
	if c.LastConceptID != "" {
		t.Fatal("dry-run advanced the cursor")
	}
}

func TestMigrationReconcilesAndIsIdempotent(t *testing.T) {
	m, s := seedMigrationFixture(t)
	ctx := context.Background()
	first, err := m.Run(ctx, migrationOpts(DirectionToRAG))
	if err != nil {
		t.Fatal(err)
	}
	if first.Scanned != 2 || first.Synced != 2 || !first.Done {
		t.Fatalf("first run = %+v", first)
	}
	projections, _ := s.Meta.ListProjections(ctx, meta.ProjectionQuery{Adapter: "memory"})
	if len(projections) != 2 {
		t.Fatalf("projections = %+v", projections)
	}
	// A second job instance with a fresh job name must detect everything is synced.
	opt := migrationOpts(DirectionToRAG)
	opt.Job = "recheck"
	second, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if second.Synced != 0 || second.Skipped != 2 {
		t.Fatalf("idempotency broken: %+v", second)
	}
}

func TestMigrationResumesFromCursor(t *testing.T) {
	m, s := seedMigrationFixture(t)
	ctx := context.Background()
	opt := migrationOpts(DirectionToRAG)
	opt.Limit = 1
	first, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if first.Scanned != 1 || first.Done || first.LastConceptID == "" {
		t.Fatalf("first batch = %+v", first)
	}
	c, _ := s.Meta.MigrationCursor(ctx, "test")
	if c.LastConceptID != first.LastConceptID {
		t.Fatalf("cursor not saved: %+v", c)
	}
	second, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned != 1 || !second.Done || second.Synced != 1 {
		t.Fatalf("second batch = %+v", second)
	}
}

func TestFingerprintDriftTriggersRebuild(t *testing.T) {
	m, s := seedMigrationFixture(t)
	ctx := context.Background()
	if _, err := m.Run(ctx, migrationOpts(DirectionToRAG)); err != nil {
		t.Fatal(err)
	}
	// Simulate an embedding/config change recorded on a projection.
	_ = store.RecordProjection(ctx, s.Meta, "metrics/a", "memory", "old-hash", "old-fingerprint", store.ProjectionSynced)
	opt := migrationOpts(DirectionToRAG)
	opt.Job = "drift"
	rep, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Planned == 0 || rep.Synced == 0 {
		t.Fatalf("drift not rebuilt: %+v", rep)
	}
}

func TestRagToCanonicalInvalidatesOrphans(t *testing.T) {
	m, s := seedMigrationFixture(t)
	ctx := context.Background()
	// A projection with no canonical body is an orphan.
	_ = store.RecordProjection(ctx, s.Meta, "ghost/removed", "memory", "hash", "fp", store.ProjectionSynced)
	rep, err := m.Run(ctx, migrationOpts(DirectionToCanonical))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Invalidated == 0 {
		t.Fatalf("orphan not invalidated: %+v", rep)
	}
	projections, _ := s.Meta.ListProjections(ctx, meta.ProjectionQuery{ConceptID: "ghost/removed"})
	if len(projections) != 1 || projections[0].Status != store.ProjectionStale {
		t.Fatalf("orphan not marked stale: %+v", projections)
	}
}

func TestMigrationValidation(t *testing.T) {
	m, _ := seedMigrationFixture(t)
	if _, err := m.Run(context.Background(), MigrationOptions{Direction: "unknown", Target: "memory"}); err == nil {
		t.Fatal("unknown direction must fail")
	}
	if _, err := m.Run(context.Background(), MigrationOptions{Direction: DirectionToRAG, Target: "okf"}); err == nil {
		t.Fatal("canonical cannot be a migration target")
	}
	if _, err := m.Run(context.Background(), MigrationOptions{Direction: DirectionToRAG, Target: "missing"}); err == nil {
		t.Fatal("unregistered target must fail")
	}
}
