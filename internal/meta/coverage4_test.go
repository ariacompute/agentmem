package meta

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// TestPostgresMigrateExecutesAll drives the full migration loop with every
// statement reported as not-yet-applied, exercising the read-file/exec/insert
// path end to end without a database.
func TestPostgresMigrateExecutesAll(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	files, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations embedded")
	}
	for _, name := range files {
		raw, err := migrationFS.ReadFile(name)
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
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func TestOpenPostgresParseError(t *testing.T) {
	// A malformed port fails synchronously during DSN parsing; no connection
	// is attempted, so this stays offline.
	if _, err := OpenPostgres(context.Background(), Options{DSN: "host=x port=notanumber"}); err == nil {
		t.Fatal("invalid port must error without connecting")
	}
}
