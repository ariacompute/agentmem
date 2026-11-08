package meta

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{
		Conn:                 db,
		PreferSimpleProtocol: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return &PostgresStore{db: gdb}, mock
}

func TestGormLogLevelBranches(t *testing.T) {
	cases := map[string]logger.LogLevel{
		"silent": logger.Silent,
		"error":  logger.Error,
		"info":   logger.Info,
		"warn":   logger.Warn,
		"":       logger.Warn,
		"other":  logger.Warn,
	}
	for in, want := range cases {
		if got := gormLogLevel(in); got != want {
			t.Fatalf("gormLogLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestOpenPostgresEmptyDSN(t *testing.T) {
	if _, err := OpenPostgres(context.Background(), Options{}); err == nil {
		t.Fatal("empty DSN must error")
	}
}

func TestPostgresUpsertGetListConcepts(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "concepts_meta"`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := s.UpsertConcept(ctx, ConceptMeta{ConceptID: "c1", ContentHash: "h", Visibility: "public", Owner: "alice", Status: "stable"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	now := time.Now()
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "content_hash", "visibility", "owner", "status", "created_at", "updated_at"}).
			AddRow("c1", "h", "public", "alice", "stable", now, now))
	got, err := s.GetConcept(ctx, "c1")
	if err != nil || got.ConceptID != "c1" {
		t.Fatalf("get = %+v (%v)", got, err)
	}

	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id"}).
			AddRow("c1").AddRow("c2"))
	if list, err := s.ListConcepts(ctx, 0); err != nil || len(list) != 2 {
		t.Fatalf("list(0) = %+v (%v)", list, err)
	}
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id"}).AddRow("c1"))
	if list, err := s.ListConcepts(ctx, 5); err != nil || len(list) != 1 {
		t.Fatalf("list(5) = %+v (%v)", list, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestPostgresGetConceptNotFound(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).WillReturnError(gorm.ErrRecordNotFound)
	if _, err := s.GetConcept(ctx, "missing"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestPostgresListProjectionsFilters(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)

	mock.ExpectQuery(`SELECT \* FROM "projections"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "adapter", "status"}).
			AddRow("c", "memory", "synced"))
	if out, err := s.ListProjections(ctx, ProjectionQuery{}); err != nil || len(out) != 1 {
		t.Fatalf("all = %+v (%v)", out, err)
	}
	mock.ExpectQuery(`SELECT \* FROM "projections"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "adapter", "status"}).AddRow("c", "memory", "synced"))
	if _, err := s.ListProjections(ctx, ProjectionQuery{ConceptID: "c", Adapter: "memory", Status: "synced", Limit: 10}); err != nil {
		t.Fatalf("filtered: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestPostgresSetProjectionState(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "projections"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectCommit()
	if err := s.SetProjectionState(ctx, Projection{ConceptID: "c", Adapter: "memory", ContentHash: "h", Status: "pending"}); err != nil {
		t.Fatalf("set: %v", err)
	}
}

func TestPostgresDecisions(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "decisions"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectCommit()
	if err := s.AppendDecision(ctx, DecisionRecord{ConceptID: "c", RuleID: "r1", Targets: StringList{"okf"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	mock.ExpectQuery(`SELECT \* FROM "decisions"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "concept_id", "rule_id", "targets"}).
			AddRow(1, "c", "r1", `["okf"]`))
	if out, err := s.ListDecisions(ctx, DecisionQuery{ConceptID: "c", Limit: 5}); err != nil || len(out) != 1 {
		t.Fatalf("list = %+v (%v)", out, err)
	}
}

func TestPostgresComputations(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "computation_runs"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectCommit()
	r := ComputationRecord{ConceptID: "c", JobID: "j", Verdict: "passed", DisplayAllowed: true}
	if err := s.AppendComputation(ctx, r); err != nil {
		t.Fatalf("append: %v", err)
	}
	mock.ExpectQuery(`SELECT \* FROM "computation_runs"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "concept_id"}).AddRow(1, "c"))
	if out, err := s.ListComputations(ctx, ComputationQuery{ConceptID: "c", Limit: 5}); err != nil || len(out) != 1 {
		t.Fatalf("list = %+v (%v)", out, err)
	}
}

func TestPostgresMigrationCursor(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "migration_cursors"`).WillReturnError(gorm.ErrRecordNotFound)
	c, err := s.MigrationCursor(ctx, "job")
	if err != nil || c.Job != "job" || c.Done {
		t.Fatalf("fresh cursor = %+v (%v)", c, err)
	}
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "migration_cursors"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := s.SaveMigrationCursor(ctx, "job", Cursor{LastConceptID: "c", Done: true}); err != nil {
		t.Fatalf("save: %v", err)
	}
	mock.ExpectQuery(`SELECT \* FROM "migration_cursors"`).
		WillReturnRows(sqlmock.NewRows([]string{"job", "last_concept_id", "done"}).AddRow("job", "c", true))
	c, err = s.MigrationCursor(ctx, "job")
	if err != nil || !c.Done || c.LastConceptID != "c" {
		t.Fatalf("saved cursor = %+v (%v)", c, err)
	}
}

func TestPostgresEngineFingerprint(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "engine_fingerprints"`).WillReturnError(gorm.ErrRecordNotFound)
	if fp, err := s.EngineFingerprint(ctx, "memory"); err != nil || fp != "" {
		t.Fatalf("missing fp = %q (%v)", fp, err)
	}
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "engine_fingerprints"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := s.SetEngineFingerprint(ctx, "memory", "v1"); err != nil {
		t.Fatalf("set fp: %v", err)
	}
	mock.ExpectQuery(`SELECT \* FROM "engine_fingerprints"`).
		WillReturnRows(sqlmock.NewRows([]string{"adapter", "fingerprint"}).AddRow("memory", "v1"))
	if fp, err := s.EngineFingerprint(ctx, "memory"); err != nil || fp != "v1" {
		t.Fatalf("fp = %q (%v)", fp, err)
	}
}

func TestPostgresACL(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "acl"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := s.PutACL(ctx, ACLEntry{Subject: "bob", ConceptID: "c", Allow: true}); err != nil {
		t.Fatalf("put acl: %v", err)
	}
	mock.ExpectQuery(`SELECT \* FROM "acl"`).
		WillReturnRows(sqlmock.NewRows([]string{"subject", "concept_id", "allow"}).AddRow("bob", "c", true))
	if ok, err := s.CheckACL(ctx, "bob", "c"); err != nil || !ok {
		t.Fatalf("explicit allow = %v (%v)", ok, err)
	}
}

func TestPostgresCheckACLFallbackVisibility(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	// No explicit ACL -> ErrRecordNotFound, then fallback reads the concept.
	mock.ExpectQuery(`SELECT \* FROM "acl"`).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "visibility", "owner"}).
			AddRow("pub", "public", "alice"))
	if ok, err := s.CheckACL(ctx, "", "pub"); err != nil || !ok {
		t.Fatalf("public fallback = %v (%v)", ok, err)
	}

	mock.ExpectQuery(`SELECT \* FROM "acl"`).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "visibility", "owner"}).
			AddRow("team", "team", "alice"))
	if ok, _ := s.CheckACL(ctx, "bob", "team"); !ok {
		t.Fatal("team with subject must allow")
	}
	mock.ExpectQuery(`SELECT \* FROM "acl"`).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "visibility", "owner"}).
			AddRow("team", "team", "alice"))
	if ok, _ := s.CheckACL(ctx, "", "team"); ok {
		t.Fatal("team without subject must deny")
	}
	mock.ExpectQuery(`SELECT \* FROM "acl"`).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "visibility", "owner"}).
			AddRow("priv", "private", "alice"))
	if ok, _ := s.CheckACL(ctx, "alice", "priv"); !ok {
		t.Fatal("private owner must allow")
	}
	mock.ExpectQuery(`SELECT \* FROM "acl"`).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "visibility", "owner"}).
			AddRow("priv", "private", "alice"))
	if ok, _ := s.CheckACL(ctx, "bob", "priv"); ok {
		t.Fatal("private non-owner must deny")
	}
	// Concept missing entirely -> fallback returns false.
	mock.ExpectQuery(`SELECT \* FROM "acl"`).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery(`SELECT \* FROM "concepts_meta"`).WillReturnError(gorm.ErrRecordNotFound)
	if ok, err := s.CheckACL(ctx, "bob", "ghost"); err != nil || ok {
		t.Fatalf("missing concept must deny: %v (%v)", ok, err)
	}
}

func TestPostgresClose(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectClose()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestPostgresQueryExec(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)

	// Empty statement returns an error before touching the DB.
	if _, err := s.Query(ctx, "   ", nil, 10); err == nil {
		t.Fatal("empty statement must error")
	}

	// Success with a []byte cell that must be stringified, plus a maxRows cap.
	mock.ExpectBegin()
	mock.ExpectExec(`SET TRANSACTION READ ONLY`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT amount`).
		WillReturnRows(sqlmock.NewRows([]string{"amount", "label"}).
			AddRow([]byte("42"), "x").AddRow([]byte("7"), "y"))
	mock.ExpectCommit()
	rows, err := s.Query(ctx, "SELECT amount, label FROM orders", nil, 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows.Values) != 1 {
		t.Fatalf("maxRows cap failed: %+v", rows.Values)
	}
	if rows.Columns[0] != "amount" || rows.Values[0][0] != "42" {
		t.Fatalf("row shape wrong: %+v", rows)
	}

	// SET TRANSACTION failure rolls back.
	mock.ExpectBegin()
	mock.ExpectExec(`SET TRANSACTION READ ONLY`).WillReturnError(errors.New("ro failed"))
	mock.ExpectRollback()
	if _, err := s.Query(ctx, "SELECT 1", nil, 10); err == nil {
		t.Fatal("read-only failure must error")
	}

	// Raw query failure rolls back.
	mock.ExpectBegin()
	mock.ExpectExec(`SET TRANSACTION READ ONLY`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT 1`).WillReturnError(errors.New("bad sql"))
	mock.ExpectRollback()
	if _, err := s.Query(ctx, "SELECT 1", nil, 10); err == nil {
		t.Fatal("raw failure must error")
	}

	// maxRows <= 0 defaults to 100; only one returned row is kept.
	mock.ExpectBegin()
	mock.ExpectExec(`SET TRANSACTION READ ONLY`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT v`).
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(int64(1)))
	mock.ExpectCommit()
	if rows, err := s.Query(ctx, "SELECT v", nil, 0); err != nil || len(rows.Values) != 1 {
		t.Fatalf("default maxRows: %+v (%v)", rows, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestPostgresVectorStore(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)

	// Empty vector is rejected before any SQL.
	if err := s.PutVector(ctx, "c", Vector{}); err == nil {
		t.Fatal("empty vector must error")
	}
	if _, err := s.QueryVector(ctx, Vector{}, 5); err == nil {
		t.Fatal("empty vector must error")
	}

	mock.ExpectExec(`INSERT INTO concept_vectors`).WillReturnResult(sqlmock.NewResult(1, 1))
	if err := s.PutVector(ctx, "c", Vector{1, 0}); err != nil {
		t.Fatalf("put vector: %v", err)
	}

	mock.ExpectQuery(`FROM concept_vectors`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "score"}).AddRow("c", 0.9))
	if hits, err := s.QueryVector(ctx, Vector{1, 0}, 0); err != nil || len(hits) != 1 || hits[0].ConceptID != "c" {
		t.Fatalf("query vector = %+v (%v)", hits, err)
	}

	mock.ExpectExec(`DELETE FROM concept_vectors`).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.DeleteVector(ctx, "c"); err != nil {
		t.Fatalf("delete vector: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}
