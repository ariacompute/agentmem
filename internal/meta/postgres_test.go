package meta

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedactConnectionError(t *testing.T) {
	dsn := "postgres://agentmem:s3cr3t@db:5432/agentmem?sslmode=require"
	err := fmt.Errorf("driver: cannot connect using %s (password=s3cr3t)", dsn)
	got := redactConnectionError(err, dsn).Error()
	for _, secret := range []string{dsn, "s3cr3t"} {
		if strings.Contains(got, secret) {
			t.Fatalf("connection error leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected a redaction marker: %s", got)
	}
	if redactConnectionError(nil, dsn) != nil {
		t.Fatal("nil error must stay nil")
	}
	bare := redactConnectionError(errors.New("postgres://u:p@host/db password=p"), "").Error()
	if strings.Contains(bare, ":p@") || strings.Contains(bare, "password=p") {
		t.Fatalf("credentials in a bare error were not redacted: %s", bare)
	}
}

func TestACLFromVisibilityRejectsAnonymousPrivate(t *testing.T) {
	m := ConceptMeta{ConceptID: "private/c", Visibility: "private", Owner: "alice"}
	for _, subject := range []string{"", "bob"} {
		if aclFromVisibility(m, subject) {
			t.Fatalf("private document readable by %q", subject)
		}
	}
	if !aclFromVisibility(m, "alice") {
		t.Fatal("owner must read a private document")
	}
	if aclFromVisibility(ConceptMeta{Visibility: "team"}, "") {
		t.Fatal("anonymous subject must not read team documents")
	}
}

func TestFakeMetaStoreContract(t *testing.T) {
	ctx := context.Background()
	s := NewFakeStore()
	defer s.Close()

	m := ConceptMeta{ConceptID: "tables/orders", ContentHash: "abc", Visibility: "private", Owner: "alice", Status: "stable"}
	if err := s.UpsertConcept(ctx, m); err != nil {
		t.Fatal(err)
	}
	// Upsert is idempotent and keeps the original CreatedAt.
	got, err := s.GetConcept(ctx, "tables/orders")
	if err != nil || got.ContentHash != "abc" {
		t.Fatalf("get = %+v (%v)", got, err)
	}
	created := got.CreatedAt
	if err := s.UpsertConcept(ctx, ConceptMeta{ConceptID: "tables/orders", ContentHash: "def", Visibility: "public", Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConcept(ctx, "tables/orders")
	if got.ContentHash != "def" || !got.CreatedAt.Equal(created) {
		t.Fatalf("upsert lost idempotency: %+v", got)
	}
	all, _ := s.ListConcepts(ctx, 10)
	if len(all) != 1 {
		t.Fatalf("list = %d", len(all))
	}
}

func TestProjectionUpsertIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := NewFakeStore()
	p := Projection{ConceptID: "c", Adapter: "memory", ContentHash: "h1", Status: "pending"}
	if err := s.SetProjectionState(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.ContentHash, p.Status = "h2", "synced"
	if err := s.SetProjectionState(ctx, p); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListProjections(ctx, ProjectionQuery{ConceptID: "c"})
	if len(list) != 1 || list[0].ContentHash != "h2" || list[0].Status != "synced" {
		t.Fatalf("projections = %+v", list)
	}
	list, _ = s.ListProjections(ctx, ProjectionQuery{Status: "pending"})
	if len(list) != 0 {
		t.Fatalf("status filter wrong: %+v", list)
	}
}

func TestDecisionsCursorFingerprint(t *testing.T) {
	ctx := context.Background()
	s := NewFakeStore()
	if err := s.AppendDecision(ctx, DecisionRecord{ConceptID: "c", RuleID: "r1", Targets: StringList{"okf"}}); err != nil {
		t.Fatal(err)
	}
	_ = s.AppendDecision(ctx, DecisionRecord{ConceptID: "other", RuleID: "r2"})
	ds, _ := s.ListDecisions(ctx, DecisionQuery{ConceptID: "c", Limit: 10})
	if len(ds) != 1 || ds[0].RuleID != "r1" || len(ds[0].Targets) != 1 {
		t.Fatalf("decisions = %+v", ds)
	}
	if c, _ := s.MigrationCursor(ctx, "job1"); c.Job != "job1" || c.Done {
		t.Fatalf("fresh cursor = %+v", c)
	}
	_ = s.SaveMigrationCursor(ctx, "job1", Cursor{LastConceptID: "c", Done: true})
	if c, _ := s.MigrationCursor(ctx, "job1"); !c.Done || c.LastConceptID != "c" {
		t.Fatalf("saved cursor = %+v", c)
	}
	if fp, _ := s.EngineFingerprint(ctx, "memory"); fp != "" {
		t.Fatalf("fresh fingerprint = %q", fp)
	}
	_ = s.SetEngineFingerprint(ctx, "memory", "embed-v1")
	if fp, _ := s.EngineFingerprint(ctx, "memory"); fp != "embed-v1" {
		t.Fatalf("fingerprint = %q", fp)
	}
}

func TestACLPolicy(t *testing.T) {
	ctx := context.Background()
	s := NewFakeStore()
	_ = s.UpsertConcept(ctx, ConceptMeta{ConceptID: "private/c", Visibility: "private", Owner: "alice"})
	_ = s.UpsertConcept(ctx, ConceptMeta{ConceptID: "public/c", Visibility: "public", Owner: "alice"})
	if ok, _ := s.CheckACL(ctx, "bob", "private/c"); ok {
		t.Fatal("private concept leaked to non-owner")
	}
	if ok, _ := s.CheckACL(ctx, "alice", "private/c"); !ok {
		t.Fatal("owner denied")
	}
	if ok, _ := s.CheckACL(ctx, "", "public/c"); !ok {
		t.Fatal("public concept denied")
	}
	_ = s.PutACL(ctx, ACLEntry{Subject: "bob", ConceptID: "private/c", Allow: true})
	if ok, _ := s.CheckACL(ctx, "bob", "private/c"); !ok {
		t.Fatal("explicit ACL grant ignored")
	}
}

func TestFakeVectorStore(t *testing.T) {
	ctx := context.Background()
	s := NewFakeStore()
	_ = s.PutVector(ctx, "a", Vector{1, 0})
	_ = s.PutVector(ctx, "b", Vector{0, 1})
	hits, err := s.QueryVector(ctx, Vector{1, 0}, 5)
	if err != nil || len(hits) != 2 || hits[0].ConceptID != "a" {
		t.Fatalf("hits = %+v (%v)", hits, err)
	}
	if hits[0].Score < 0.99 || hits[1].Score > 0.01 {
		t.Fatalf("cosine scores wrong: %+v", hits)
	}
}

func TestVectorValuerScanner(t *testing.T) {
	v := Vector{1.5, -2, 0}
	val, err := v.Value()
	if err != nil {
		t.Fatal(err)
	}
	var got Vector
	if err := got.Scan(val); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 1.5 || got[1] != -2 {
		t.Fatalf("round-trip = %+v", got)
	}
	if err := got.Scan("[]"); err != nil || len(got) != 0 {
		t.Fatalf("empty vector = %+v (%v)", got, err)
	}
	if _, err := (Vector{}).Value(); err == nil {
		t.Fatal("empty vector must be rejected")
	}
}

func TestStringListValuerScanner(t *testing.T) {
	l := StringList{"okf", "memory"}
	val, err := l.Value()
	if err != nil {
		t.Fatal(err)
	}
	var got StringList
	if err := got.Scan(val); err != nil || len(got) != 2 || got[1] != "memory" {
		t.Fatalf("round-trip = %+v (%v)", got, err)
	}
}

func TestSplitStatements(t *testing.T) {
	stmts := splitStatements("-- comment\nCREATE TABLE a (x INT);\n\nCREATE INDEX i ON a (x);\n")
	if len(stmts) != 2 || !strings.HasPrefix(stmts[0], "CREATE TABLE") {
		t.Fatalf("statements = %q", stmts)
	}
}

func TestMigrationFilesSorted(t *testing.T) {
	files, err := migrationFiles()
	if err != nil || len(files) != 3 || files[0] != "migrations/001_init.sql" ||
		files[2] != "migrations/003_computations.sql" {
		t.Fatalf("files = %v (%v)", files, err)
	}
}

// Optional integration test: only runs when a real DSN is provided.
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("AGENTMEM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set AGENTMEM_TEST_PG_DSN to run the PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := OpenPostgres(ctx, Options{DSN: dsn, MaxOpenConns: 5, MaxIdleConns: 2, ConnMaxLifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpsertConcept(ctx, ConceptMeta{ConceptID: "integration/c", ContentHash: "h", Visibility: "public", Owner: "system", Status: "stable"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConcept(ctx, "integration/c")
	if err != nil || got.ContentHash != "h" {
		t.Fatalf("postgres get = %+v (%v)", got, err)
	}
}
