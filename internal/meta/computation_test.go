package meta

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFakeComputationAudit(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	r := ComputationRecord{
		ConceptID: "computations/revenue", Runtime: "postgres",
		ContractHash: "abc", ParamsDigest: "def", JobID: "job-1", Verdict: "passed",
		DisplayAllowed: true, Provenance: true, Fidelity: true,
	}
	if err := f.AppendComputation(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := f.AppendComputation(ctx, r); err != nil {
		t.Fatal(err)
	}
	all, err := f.ListComputations(ctx, ComputationQuery{})
	if err != nil || len(all) != 1 {
		t.Fatalf("idempotency: %v / %+v", err, all)
	}
	if all[0].ID == 0 || all[0].CreatedAt.IsZero() || all[0].Reasons == nil {
		t.Fatalf("record not normalised: %+v", all[0])
	}
	r2 := r
	r2.JobID, r2.Verdict, r2.DisplayAllowed = "job-2", "rejected", false
	if err := f.AppendComputation(ctx, r2); err != nil {
		t.Fatal(err)
	}
	all, _ = f.ListComputations(ctx, ComputationQuery{})
	if len(all) != 2 || all[0].Verdict != "rejected" {
		t.Fatalf("latest-first ordering wrong: %+v", all)
	}
	filtered, _ := f.ListComputations(ctx, ComputationQuery{ConceptID: "other"})
	if len(filtered) != 0 {
		t.Fatalf("concept filter ignored: %+v", filtered)
	}
	limited, _ := f.ListComputations(ctx, ComputationQuery{Limit: 1})
	if len(limited) != 1 {
		t.Fatalf("limit ignored: %+v", limited)
	}
}

func TestFakeSQLExecutorIsDeterministic(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	a, err := f.Query(ctx, "SELECT 1 WHERE y = $1", []any{int64(2026)}, 10)
	if err != nil || len(a.Values) != 1 {
		t.Fatalf("query: %v / %+v", err, a)
	}
	b, _ := f.Query(ctx, "SELECT 1 WHERE y = $1", []any{int64(2026)}, 10)
	if ScalarString(a.Values[0][0]) != ScalarString(b.Values[0][0]) {
		t.Fatal("offline executor is not deterministic")
	}
	c, _ := f.Query(ctx, "SELECT 1 WHERE y = $1", []any{int64(2025)}, 10)
	if ScalarString(a.Values[0][0]) == ScalarString(c.Values[0][0]) {
		t.Fatal("different bound values must produce different results")
	}
	if len(a.Values) > 10 || len(a.Columns) != 1 {
		t.Fatalf("row cap or columns wrong: %+v", a)
	}
}

func TestSnapshotCarriesComputationAudit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "meta.fake.json")
	f, err := NewFileFakeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.AppendComputation(ctx, ComputationRecord{
		ConceptID: "c1", JobID: "job-1", Verdict: "passed", DisplayAllowed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	g, err := NewFileFakeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	all, err := g.ListComputations(ctx, ComputationQuery{})
	if err != nil || len(all) != 1 || all[0].JobID != "job-1" {
		t.Fatalf("snapshot lost audit: %v / %+v", err, all)
	}
	if err := g.AppendComputation(ctx, ComputationRecord{
		ConceptID: "c1", JobID: "job-1", Verdict: "passed",
	}); err != nil {
		t.Fatal(err)
	}
	all, _ = g.ListComputations(ctx, ComputationQuery{})
	if len(all) != 1 {
		t.Fatal("dedupe keys were not rebuilt from the snapshot")
	}
}
