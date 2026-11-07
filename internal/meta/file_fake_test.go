package meta

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFileFakeStoreSharesStateAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.fake.json")
	ctx := context.Background()

	first, err := NewFileFakeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.UpsertConcept(ctx, ConceptMeta{
		ConceptID: "metrics/churn", ContentHash: "h1",
		Visibility: "public", Owner: "alice", Status: "stable",
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.AppendDecision(ctx, DecisionRecord{ConceptID: "metrics/churn", RuleID: "R1", Targets: StringList{"okf"}}); err != nil {
		t.Fatal(err)
	}
	if err := first.SetProjectionState(ctx, Projection{ConceptID: "metrics/churn", Adapter: "memory", ContentHash: "h1", Status: "synced"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := NewFileFakeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	m, err := second.GetConcept(ctx, "metrics/churn")
	if err != nil || m.ContentHash != "h1" {
		t.Fatalf("concept not persisted: %+v (%v)", m, err)
	}
	decisions, _ := second.ListDecisions(ctx, DecisionQuery{Limit: 10})
	if len(decisions) != 1 || decisions[0].RuleID != "R1" || len(decisions[0].Targets) != 1 {
		t.Fatalf("decisions not persisted: %+v", decisions)
	}
	projections, _ := second.ListProjections(ctx, ProjectionQuery{Adapter: "memory"})
	if len(projections) != 1 || projections[0].Status != "synced" {
		t.Fatalf("projections not persisted: %+v", projections)
	}
	if ok, _ := second.CheckACL(ctx, "bob", "metrics/churn"); !ok {
		t.Fatal("public ACL not persisted")
	}
}

func TestFileFakeStoreMissingFileStartsEmpty(t *testing.T) {
	s, err := NewFileFakeStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if list, _ := s.ListConcepts(context.Background(), 10); len(list) != 0 {
		t.Fatalf("expected empty store, got %+v", list)
	}
}

func TestFileFakeStoreEmptyPathStaysInMemory(t *testing.T) {
	s, err := NewFileFakeStore("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
