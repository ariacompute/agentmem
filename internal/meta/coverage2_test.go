package meta

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestNotFoundErrorString(t *testing.T) {
	if errNotFound.Error() != "meta: not found" {
		t.Fatalf("message = %q", errNotFound.Error())
	}
}

func TestFakeGetConceptNotFound(t *testing.T) {
	f := NewFakeStore()
	if _, err := f.GetConcept(context.Background(), "missing"); !errors.Is(err, errNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestFakeListConceptsLimit(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	for _, id := range []string{"a", "b", "c"} {
		if err := f.UpsertConcept(ctx, ConceptMeta{ConceptID: id, Visibility: "public"}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := f.ListConcepts(ctx, 2)
	if err != nil || len(out) != 2 {
		t.Fatalf("limit = %+v (%v)", out, err)
	}
	all, err := f.ListConcepts(ctx, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("no limit = %+v (%v)", all, err)
	}
}

func TestFakeListProjectionsFiltersAndLimit(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	for _, p := range []Projection{
		{ConceptID: "a", Adapter: "memory", Status: "pending"},
		{ConceptID: "a", Adapter: "okf", Status: "done"},
		{ConceptID: "b", Adapter: "memory", Status: "done"},
	} {
		if err := f.SetProjectionState(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if out, _ := f.ListProjections(ctx, ProjectionQuery{ConceptID: "a"}); len(out) != 2 {
		t.Fatalf("concept filter = %+v", out)
	}
	if out, _ := f.ListProjections(ctx, ProjectionQuery{Adapter: "memory"}); len(out) != 2 {
		t.Fatalf("adapter filter = %+v", out)
	}
	if out, _ := f.ListProjections(ctx, ProjectionQuery{Status: "done"}); len(out) != 2 {
		t.Fatalf("status filter = %+v", out)
	}
	if out, _ := f.ListProjections(ctx, ProjectionQuery{Limit: 1}); len(out) != 1 {
		t.Fatalf("limit = %+v", out)
	}
}

func TestFakeQueryContextCancelled(t *testing.T) {
	f := NewFakeStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Query(ctx, "SELECT 1", nil, 1); err == nil {
		t.Fatal("cancelled context must error")
	}
}

func TestFakeAppendComputationDeduplicates(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	r := ComputationRecord{ConceptID: "c", JobID: "j", Verdict: "passed"}
	if err := f.AppendComputation(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := f.AppendComputation(ctx, r); err != nil {
		t.Fatal(err)
	}
	out, _ := f.ListComputations(ctx, ComputationQuery{})
	if len(out) != 1 {
		t.Fatalf("duplicate should be ignored, got %d", len(out))
	}
}

func TestFakeVectorTopKAndDelete(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	if err := f.PutVector(ctx, "a", Vector{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := f.PutVector(ctx, "b", Vector{0, 1}); err != nil {
		t.Fatal(err)
	}
	// topK <= 0 defaults to 5; both vectors are returned.
	if hits, err := f.QueryVector(ctx, Vector{1, 0}, 0); err != nil || len(hits) != 2 {
		t.Fatalf("default topK = %+v (%v)", hits, err)
	}
	// topK truncates.
	if hits, err := f.QueryVector(ctx, Vector{1, 0}, 1); err != nil || len(hits) != 1 {
		t.Fatalf("truncate = %+v (%v)", hits, err)
	}
	if err := f.DeleteVector(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if hits, _ := f.QueryVector(ctx, Vector{1, 0}, 5); len(hits) != 1 {
		t.Fatalf("after delete = %+v", hits)
	}
}

func TestFakeExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	if err := f.UpsertConcept(ctx, ConceptMeta{ConceptID: "a", Visibility: "public"}); err != nil {
		t.Fatal(err)
	}
	if err := f.PutVector(ctx, "a", Vector{1, 2}); err != nil {
		t.Fatal(err)
	}
	snap := f.Export()

	// Importing a snapshot with nil maps must normalise them, not panic.
	g := NewFakeStore()
	g.Import(Snapshot{})
	if g.Export().Concepts == nil {
		t.Fatal("import must normalise nil maps")
	}

	// Round-trip preserves state.
	g.Import(snap)
	if _, err := g.GetConcept(ctx, "a"); err != nil {
		t.Fatalf("concept lost on import: %v", err)
	}
	if hits, _ := g.QueryVector(ctx, Vector{1, 2}, 1); len(hits) != 1 {
		t.Fatalf("vector lost on import: %+v", hits)
	}
}

func TestCosineAndSqrtAndRankHits(t *testing.T) {
	if cosine(Vector{1}, Vector{1, 2}) != 0 {
		t.Fatal("length mismatch must be 0")
	}
	if cosine(Vector{}, Vector{}) != 0 {
		t.Fatal("empty must be 0")
	}
	if cosine(Vector{0}, Vector{1}) != 0 {
		t.Fatal("zero norm must be 0")
	}
	if cosine(Vector{1, 0}, Vector{1, 0}) != 1 {
		t.Fatal("identical vectors must be 1")
	}
	if sqrt(-1) != 0 || sqrt(0) != 0 {
		t.Fatal("non-positive sqrt must be 0")
	}
	if v := sqrt(4); v < 1.99 || v > 2.01 {
		t.Fatalf("sqrt(4) = %v", v)
	}

	hits := []VectorHit{{ConceptID: "b", Score: 0.5}, {ConceptID: "a", Score: 0.5}, {ConceptID: "c", Score: 0.9}}
	rankHits(hits)
	if hits[0].ConceptID != "c" || hits[1].ConceptID != "a" || hits[2].ConceptID != "b" {
		t.Fatalf("rank = %+v", hits)
	}
}

func TestScalarStringAllTypes(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"s", "s"},
		{[]byte("b"), "b"},
		{int64(5), "5"},
		{int(6), "6"},
		{float64(1.5), "1.5"},
		{float32(1.5), "1.5"},
		{true, "true"},
		{ts, ts.Format(time.RFC3339Nano)},
		{struct{ X int }{7}, "{7}"},
	}
	for _, c := range cases {
		if got := ScalarString(c.in); got != c.want {
			t.Fatalf("ScalarString(%T) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestDigestBytesStable(t *testing.T) {
	a := DigestBytes("a", "b")
	b := DigestBytes("a", "b")
	if a != b || len(a) != 64 {
		t.Fatalf("digest = %q", a)
	}
	if DigestBytes("a") == a {
		t.Fatal("different parts must differ")
	}
}

func TestDeterministicSQLRowsVariants(t *testing.T) {
	if r := DeterministicSQLRows("SELECT 1", []any{int(2)}, 5); len(r.Values) != 1 || len(r.Values[0]) != 1 {
		t.Fatalf("rows = %+v", r)
	}
	// maxRows > 1 is clamped to 1; both int and int64 arguments are accepted.
	a := DeterministicSQLRows("S", []any{int64(1)}, 1)
	b := DeterministicSQLRows("S", []any{int(1)}, 1)
	if len(a.Values) != 1 || len(b.Values) != 1 {
		t.Fatalf("rows = %+v / %+v", a, b)
	}
}

func TestStringListScanVariants(t *testing.T) {
	var l StringList
	if err := l.Scan(nil); err != nil || l != nil {
		t.Fatalf("nil scan = %v (%v)", l, err)
	}
	if err := l.Scan("[]"); err != nil || len(l) != 0 {
		t.Fatalf("empty scan = %v (%v)", l, err)
	}
	if err := l.Scan([]byte(`["a","b"]`)); err != nil || len(l) != 2 {
		t.Fatalf("bytes scan = %v (%v)", l, err)
	}
	if err := l.Scan(123); err == nil {
		t.Fatal("int scan must error")
	}
}

func TestVectorScanAndValueVariants(t *testing.T) {
	var v Vector
	if err := v.Scan(nil); err != nil || v != nil {
		t.Fatalf("nil = %v (%v)", v, err)
	}
	if err := v.Scan("[1,2]"); err != nil || len(v) != 2 {
		t.Fatalf("string = %v (%v)", v, err)
	}
	if err := v.Scan([]byte("[3] ")); err != nil || len(v) != 1 {
		t.Fatalf("bytes = %v (%v)", v, err)
	}
	if err := v.Scan("[]"); err != nil || len(v) != 0 {
		t.Fatalf("empty = %v (%v)", v, err)
	}
	if err := v.Scan("[bad]"); err == nil {
		t.Fatal("bad element must error")
	}
	if err := v.Scan(123); err == nil {
		t.Fatal("int must error")
	}
	if _, err := (Vector{}).Value(); err == nil {
		t.Fatal("empty vector Value must error")
	}
	if got, err := (Vector{1, 2}).Value(); err != nil || got != "[1,2]" {
		t.Fatalf("value = %v (%v)", got, err)
	}
}

func TestACLFromVisibilityDefault(t *testing.T) {
	if aclFromVisibility(ConceptMeta{Visibility: "mystery"}, "bob") {
		t.Fatal("unknown visibility must deny")
	}
	if !aclFromVisibility(ConceptMeta{Visibility: "public"}, "") {
		t.Fatal("public must allow")
	}
	if !aclFromVisibility(ConceptMeta{Visibility: "team"}, "bob") {
		t.Fatal("team must allow any subject")
	}
	if !aclFromVisibility(ConceptMeta{Visibility: "private", Owner: "bob"}, "bob") {
		t.Fatal("private owner must allow")
	}
	if aclFromVisibility(ConceptMeta{Visibility: "private", Owner: "bob"}, "ann") {
		t.Fatal("private non-owner must deny")
	}
}

func TestNewFileFakeStoreReadError(t *testing.T) {
	// A directory path makes os.ReadFile fail with something other than
	// ErrNotExist, exercising the error branch.
	if _, err := NewFileFakeStore(t.TempDir()); err == nil {
		t.Fatal("directory path must error")
	}
}

func TestSaveToEmptyAndSuccess(t *testing.T) {
	f := NewFakeStore()
	if err := f.SaveTo(""); err != nil {
		t.Fatalf("empty path = %v", err)
	}
	ctx := context.Background()
	_ = f.UpsertConcept(ctx, ConceptMeta{ConceptID: "a", Visibility: "public"})
	p := filepath.Join(t.TempDir(), "snap", "meta.json")
	if err := f.SaveTo(p); err != nil {
		t.Fatalf("save = %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("stdout file missing: %v", err)
	}
}

func TestPostgresAppendDecisionNilTargets(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "decisions"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectCommit()
	if err := s.AppendDecision(ctx, DecisionRecord{ConceptID: "c", RuleID: "r"}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestPostgresCheckACLNonNotFoundError(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "acl"`).WillReturnError(errors.New("boom"))
	if _, err := s.CheckACL(ctx, "s", "c"); err == nil {
		t.Fatal("generic error must propagate")
	}
}

func TestPostgresMigrateBootstrapError(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnError(errors.New("boom"))
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("bootstrap failure must error")
	}
}

func TestPostgresQueryEmptyStatement(t *testing.T) {
	ctx := context.Background()
	s, _ := newMockStore(t)
	if _, err := s.Query(ctx, "   ", nil, 0); err == nil {
		t.Fatal("empty statement must error")
	}
}
