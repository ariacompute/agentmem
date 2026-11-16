package meta

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// brokenMigrationFS fails every directory read.
type brokenMigrationFS struct{ err error }

func (b brokenMigrationFS) Open(string) (fs.File, error) { return nil, b.err }
func (b brokenMigrationFS) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, b.err
}

func withMigrationFS(t *testing.T, replacement fs.FS) {
	t.Helper()
	previous := migrationFS
	migrationFS = replacement
	t.Cleanup(func() { migrationFS = previous })
}

func TestMigrationFilesReadError(t *testing.T) {
	withMigrationFS(t, brokenMigrationFS{err: errors.New("no directory")})
	if _, err := migrationFiles(); err == nil {
		t.Fatal("a failing migration directory must be reported")
	}
	s, _ := newMockStore(t)
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("Migrate must surface a failing migration listing")
	}
}

// stubPool is a ConnPool that is not a *sql.DB, which makes gorm.DB.DB fail.
type stubPool struct{}

func (stubPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("unsupported")
}
func (stubPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("unsupported")
}
func (stubPool) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unsupported")
}
func (stubPool) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func withOpenGorm(t *testing.T, open func(string, logger.Interface) (*gorm.DB, error)) {
	t.Helper()
	previous := openGorm
	openGorm = open
	t.Cleanup(func() { openGorm = previous })
}

func TestOpenPostgresConfiguresPoolAndMigrates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	gdb, err := gormOpenMock(db)
	if err != nil {
		t.Fatal(err)
	}
	withOpenGorm(t, func(string, logger.Interface) (*gorm.DB, error) { return gdb, nil })

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	expectMigrationsAfterBootstrap(t, mock)
	store, err := OpenPostgres(context.Background(), Options{
		DSN: "postgres://user:pass@example/db", MaxOpenConns: 4, MaxIdleConns: 2,
		ConnMaxLifetime: time.Minute, LogLevel: "silent",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if store == nil {
		t.Fatal("store must be returned")
	}
	mock.ExpectClose()
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPostgresReportsPingFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	gdb, err := gormOpenMock(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectClose()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	withOpenGorm(t, func(string, logger.Interface) (*gorm.DB, error) { return gdb, nil })
	if _, err := OpenPostgres(context.Background(), Options{DSN: "postgres://x/y", MaxIdleConns: 1}); err == nil {
		t.Fatal("a failing ping must abort the open")
	}
}

func TestOpenPostgresReportsMissingSQLDB(t *testing.T) {
	withOpenGorm(t, func(string, logger.Interface) (*gorm.DB, error) {
		gdb := &gorm.DB{Config: &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}}
		gdb.ConnPool = stubPool{}
		return gdb, nil
	})
	if _, err := OpenPostgres(context.Background(), Options{DSN: "postgres://x/y"}); err == nil {
		t.Fatal("a pool that is not a *sql.DB must abort the open")
	}
}

func TestOpenPostgresClosesPoolOnMigrationFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	gdb, err := gormOpenMock(db)
	if err != nil {
		t.Fatal(err)
	}
	withOpenGorm(t, func(string, logger.Interface) (*gorm.DB, error) { return gdb, nil })
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnError(errors.New("boom"))
	mock.ExpectClose()
	if _, err := OpenPostgres(context.Background(), Options{DSN: "postgres://x/y", MaxIdleConns: 1}); err == nil {
		t.Fatal("a failing bootstrap must abort the open")
	}
}

func TestCloseReportsMissingSQLDB(t *testing.T) {
	gdb := &gorm.DB{Config: &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}}
	gdb.ConnPool = stubPool{}
	s := &PostgresStore{db: gdb}
	if err := s.Close(); err == nil {
		t.Fatal("a pool that is not a *sql.DB must fail Close")
	}
}

func TestMigrateSkipsOptionalPgvectorFailure(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	expectMigrationsAfterBootstrap(t, mock)

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigratePropagatesRequiredFailure(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnError(errors.New("lock refused"))
	mock.ExpectRollback()
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("a required migration failure must abort startup")
	}
}

// gormOpenMock wraps an existing *sql.DB in a silent GORM handle.
func gormOpenMock(db *sql.DB) (*gorm.DB, error) {
	return gorm.Open(postgres.New(postgres.Config{Conn: db, PreferSimpleProtocol: true}),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
}

// expectMigrationsAfterBootstrap scripts every migration file as not yet
// applied, assuming the schema_migrations bootstrap already ran.
func expectMigrationsAfterBootstrap(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	files, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.Contains(name, "pgvector") {
			// The optional vector migration needs the extension; skipping it
			// must leave the remaining migrations intact.
			mock.ExpectBegin()
			mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT count\(\*\) FROM "schema_migrations"`).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
			mock.ExpectExec(`.*`).WillReturnError(errors.New("extension unavailable"))
			mock.ExpectRollback()
			continue
		}
		expectApply(t, mock, name)
	}
}

// expectApply scripts one complete, successful migration transaction.
func expectApply(t *testing.T, mock sqlmock.Sqlmock, name string) {
	t.Helper()
	raw, err := fs.ReadFile(migrationFS, name)
	if err != nil {
		t.Fatal(err)
	}
	stmts := splitStatements(string(raw))
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM "schema_migrations"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	for range stmts {
		mock.ExpectExec(`.*`).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectExec(`INSERT INTO schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
}
func TestSaveToReportsFilesystemErrors(t *testing.T) {
	store := NewFakeStore()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTo(filepath.Join(blocker, "snapshot.json")); err == nil {
		t.Fatal("a parent that is a regular file must fail")
	}

	// Root holds CAP_DAC_OVERRIDE, so a 0o500 directory stays writable and this
	// assertion cannot hold; skip it rather than fail on correct code.
	if os.Geteuid() == 0 {
		t.Log("root bypasses DAC checks (CAP_DAC_OVERRIDE); skipping the read-only assertion")
		return
	}
	readOnly := filepath.Join(dir, "readonly")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	if err := store.SaveTo(filepath.Join(readOnly, "snapshot.json")); err == nil {
		t.Fatal("a read-only directory must fail the write")
	}
}

func TestScanRowsReportsColumnsError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT 1`).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
	rows, err := db.Query("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close rows: %v", err)
	}
	if _, err := scanRows(rows, 10); err == nil {
		t.Fatal("a closed result set must fail the column read")
	}
}

func TestScanRowsReportsScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT 1`).WillReturnRows(
		sqlmock.NewRows([]string{"n"}).AddRow(1).RowError(0, errors.New("scan refused")))
	rows, err := db.Query("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if _, err := scanRows(rows, 10); err == nil {
		t.Fatal("a scan failure must be reported")
	}
}

func TestQueryReportsScanFailure(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`SET TRANSACTION READ ONLY`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT`).WillReturnRows(
		sqlmock.NewRows([]string{"n"}).AddRow(1).RowError(0, errors.New("scan refused")))
	mock.ExpectRollback()
	if _, err := s.Query(context.Background(), "SELECT 1", nil, 10); err == nil {
		t.Fatal("a scan failure must surface through Query")
	}
}

func TestQueryVectorRejectsEmptyVector(t *testing.T) {
	s, _ := newMockStore(t)
	if _, err := s.QueryVector(context.Background(), Vector{}, 5); err == nil {
		t.Fatal("an empty vector must be rejected")
	}
}

// partialMigrationFS lists a migration that cannot be read.
type partialMigrationFS struct{ entries []fs.DirEntry }

func (p partialMigrationFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
func (p partialMigrationFS) ReadDir(string) ([]fs.DirEntry, error) {
	return p.entries, nil
}

// namedEntry is a minimal fs.DirEntry.
type namedEntry struct{ name string }

func (e namedEntry) Name() string               { return e.name }
func (e namedEntry) IsDir() bool                { return false }
func (e namedEntry) Type() fs.FileMode          { return 0 }
func (e namedEntry) Info() (fs.FileInfo, error) { return nil, errors.New("no info") }

func TestMigrateSurfacesListingFailure(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	withMigrationFS(t, brokenMigrationFS{err: errors.New("no directory")})
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("a failing migration listing must abort startup")
	}
}

func TestMigrateSurfacesUnreadableMigration(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	withMigrationFS(t, partialMigrationFS{entries: []fs.DirEntry{namedEntry{name: "001_broken.sql"}}})
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM "schema_migrations"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectRollback()
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("an unreadable migration file must abort startup")
	}
}

func TestMigrateSurfacesCountFailure(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM "schema_migrations"`).WillReturnError(errors.New("count refused"))
	mock.ExpectRollback()
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("a failing applied-count query must abort startup")
	}
}

func TestQueryVectorSurfacesStoreFailures(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT concept_id`).WillReturnError(errors.New("query refused"))
	if _, err := s.QueryVector(context.Background(), Vector{1}, 5); err == nil {
		t.Fatal("a failing vector query must be reported")
	}

	s, mock = newMockStore(t)
	mock.ExpectQuery(`SELECT concept_id`).WillReturnRows(
		sqlmock.NewRows([]string{"concept_id", "score"}).AddRow("c1", "not-a-number"))
	if _, err := s.QueryVector(context.Background(), Vector{1}, 5); err == nil {
		t.Fatal("an unreadable score must be reported")
	}
}

func TestSaveToRejectsUnserialisableVector(t *testing.T) {
	store := NewFakeStore()
	if err := store.PutVector(context.Background(), "c1", Vector{float32(math.NaN())}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTo(filepath.Join(t.TempDir(), "snapshot.json")); err == nil {
		t.Fatal("a non-finite vector cannot be serialised and must fail")
	}
}
