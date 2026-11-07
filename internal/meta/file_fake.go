package meta

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// NewFileFakeStore creates the offline MetaStore with a JSON snapshot.
//
// CLI commands are separate processes, so a purely in-memory fake would lose
// ACL metadata, projections and the decision audit between invocations. The
// snapshot is only used when no PostgreSQL DSN is configured; the production
// path remains entirely in PostgreSQL.
func NewFileFakeStore(path string) (*FakeStore, error) {
	f := NewFakeStore()
	if path == "" {
		return f, nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var s Snapshot
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("meta: decode fake snapshot %s: %w", path, err)
		}
		f.Import(s)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("meta: read fake snapshot %s: %w", path, err)
	}
	f.onClose = func() error { return f.SaveTo(path) }
	return f, nil
}

// SaveTo atomically persists the current snapshot.
func (f *FakeStore) SaveTo(path string) error {
	if path == "" {
		return nil
	}
	data, err := json.Marshal(f.Export())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
