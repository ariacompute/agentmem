package meta

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migrationFS is the SQL migration source. Production reads the embedded
// files; tests replace it to exercise the fail-closed read paths.
var migrationFS fs.FS = embeddedMigrations

func migrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, "migrations/"+e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
