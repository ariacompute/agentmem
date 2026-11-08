package router

import (
	"context"
	"testing"
)

func TestMigratorToRAGAndCanonical(t *testing.T) {
	_, s, r := newWriteFixture(t)
	ctx := context.Background()
	for _, id := range []string{"concepts/a", "concepts/b"} {
		if _, err := s.Put(ctx, id, simpleDoc("Note", id)); err != nil {
			t.Fatal(err)
		}
	}
	m := NewMigrator(s, r)

	// First sync projects both concepts into the memory adapter.
	rep, err := m.Run(ctx, MigrationOptions{Target: "memory", Direction: DirectionToRAG, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Synced != 2 {
		t.Fatalf("synced = %+v", rep)
	}

	// A fresh job sees up-to-date projections and skips them.
	rep, err = m.Run(ctx, MigrationOptions{Job: "fresh", Target: "memory", Direction: DirectionToRAG, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped != 2 {
		t.Fatalf("skipped = %+v", rep)
	}

	// rag_to_canonical with matching hashes skips everything.
	if _, err := m.Run(ctx, MigrationOptions{Job: "c1", Target: "memory", Direction: DirectionToCanonical, Limit: 10}); err != nil {
		t.Fatal(err)
	}

	// Drift one body and remove another to drive invalidation and orphan cleanup.
	if _, err := s.Put(ctx, "concepts/a", simpleDoc("Note", "changed")); err != nil {
		t.Fatal(err)
	}
	if err := s.Bundle.Delete("concepts/b"); err != nil {
		t.Fatal(err)
	}
	rep, err = m.Run(ctx, MigrationOptions{Job: "c2", Target: "memory", Direction: DirectionToCanonical, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Invalidated != 2 {
		t.Fatalf("invalidated = %+v", rep)
	}
}
