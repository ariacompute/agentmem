package meta

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestFakeListDecisionsFilterAndLimit(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	for _, id := range []string{"a", "a", "b"} {
		if err := f.AppendDecision(ctx, DecisionRecord{ConceptID: id, RuleID: "r"}); err != nil {
			t.Fatal(err)
		}
	}
	if out, _ := f.ListDecisions(ctx, DecisionQuery{ConceptID: "a"}); len(out) != 2 {
		t.Fatalf("filter = %+v", out)
	}
	if out, _ := f.ListDecisions(ctx, DecisionQuery{Limit: 1}); len(out) != 1 {
		t.Fatalf("limit = %+v", out)
	}
}

func TestFakeZeroValueAppendComputation(t *testing.T) {
	// A zero-value FakeStore has nil runKeys; AppendComputation must lazily
	// initialise it rather than panic.
	f := &FakeStore{}
	if err := f.AppendComputation(context.Background(), ComputationRecord{JobID: "j", Verdict: "v"}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestFakeCheckACLFallback(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	_ = f.UpsertConcept(ctx, ConceptMeta{ConceptID: "c", Visibility: "public"})
	if ok, err := f.CheckACL(ctx, "bob", "c"); err != nil || !ok {
		t.Fatalf("public fallback = %v (%v)", ok, err)
	}
	if ok, _ := f.CheckACL(ctx, "bob", "missing"); ok {
		t.Fatal("unknown concept must deny")
	}
}

func TestFakeExportCoversAllSections(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	_ = f.SetProjectionState(ctx, Projection{ConceptID: "c", Adapter: "memory", Status: "done"})
	_ = f.SaveMigrationCursor(ctx, "job", Cursor{LastConceptID: "c", Done: true})
	_ = f.SetEngineFingerprint(ctx, "memory", "v1")
	_ = f.PutACL(ctx, ACLEntry{Subject: "bob", ConceptID: "c", Allow: true})
	_ = f.PutVector(ctx, "c", Vector{1, 2})
	snap := f.Export()
	if len(snap.Projections) != 1 || len(snap.Cursors) != 1 || len(snap.Fingerprints) != 1 ||
		len(snap.ACL) != 1 || len(snap.Vectors) != 1 {
		t.Fatalf("export sections: %+v", snap)
	}
}

func TestNewFileFakeStoreInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileFakeStore(p); err == nil {
		t.Fatal("invalid snapshot must error")
	}
}

func TestSaveToMkdirError(t *testing.T) {
	f := NewFakeStore()
	// Make the parent a regular file so MkdirAll fails with ENOTDIR.
	base := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(base, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveTo(filepath.Join(base, "child", "snap.json")); err == nil {
		t.Fatal("mkdir failure must error")
	}
}

func TestPostgresMigrateAlreadyApplied(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	files, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for range files {
		mock.ExpectBegin()
		mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(`SELECT count\(\*\) FROM "schema_migrations"`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectCommit()
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}
