package router

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/store"
)

// migrationMeta injects failures into the MetaStore port so the migration
// error paths stay reachable without PostgreSQL.
type migrationMeta struct {
	meta.MetaStore
	listErr   error
	cursorErr error
	saveErr   error
	recordErr error
}

func (m migrationMeta) ListProjections(ctx context.Context, q meta.ProjectionQuery) ([]meta.Projection, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.MetaStore.ListProjections(ctx, q)
}

func (m migrationMeta) MigrationCursor(ctx context.Context, job string) (meta.Cursor, error) {
	if m.cursorErr != nil {
		return meta.Cursor{}, m.cursorErr
	}
	return m.MetaStore.MigrationCursor(ctx, job)
}

func (m migrationMeta) SaveMigrationCursor(ctx context.Context, job string, c meta.Cursor) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	return m.MetaStore.SaveMigrationCursor(ctx, job, c)
}

func (m migrationMeta) SetProjectionState(ctx context.Context, p meta.Projection) error {
	if m.recordErr != nil {
		return m.recordErr
	}
	return m.MetaStore.SetProjectionState(ctx, p)
}

// migrationAdapter wraps a working adapter and injects Put/Delete failures.
type migrationAdapter struct {
	adapter.Adapter
	name      string
	putErr    error
	deleteErr error
}

func (a migrationAdapter) Name() string { return a.name }

func (a migrationAdapter) Put(ctx context.Context, id string, doc *okf.Document) error {
	if a.putErr != nil {
		return a.putErr
	}
	return a.Adapter.Put(ctx, id, doc)
}

func (a migrationAdapter) Delete(ctx context.Context, id string) error {
	if a.deleteErr != nil {
		return a.deleteErr
	}
	return a.Adapter.Delete(ctx, id)
}

// migrationRegistry registers a single failing target under name.
func migrationRegistry(t *testing.T, base adapter.Adapter, name string, putErr, deleteErr error) *adapter.Registry {
	t.Helper()
	r := adapter.NewRegistry()
	if err := r.RegisterAdapter(migrationAdapter{Adapter: base, name: name, putErr: putErr, deleteErr: deleteErr}); err != nil {
		t.Fatal(err)
	}
	return r
}

func memoryAdapterOf(t *testing.T, r *adapter.Registry) adapter.Adapter {
	t.Helper()
	a, ok := r.Get("memory")
	if !ok {
		t.Fatal("memory adapter missing")
	}
	return a
}

func TestMigrationCursorLoadFailure(t *testing.T) {
	m, s := seedMigrationFixture(t)
	m.Store.Meta = migrationMeta{MetaStore: s.Meta, cursorErr: errors.New("db down")}
	if _, err := m.Run(context.Background(), migrationOpts(DirectionToRAG)); err == nil {
		t.Fatal("cursor load failure must abort the job")
	}
}

func TestMigrationCursorSaveFailure(t *testing.T) {
	m, s := seedMigrationFixture(t)
	m.Store.Meta = migrationMeta{MetaStore: s.Meta, saveErr: errors.New("db down")}
	if _, err := m.Run(context.Background(), migrationOpts(DirectionToRAG)); err == nil {
		t.Fatal("cursor save failure must abort the job")
	}
}

func TestMigrationProjectionListFailureAborts(t *testing.T) {
	ctx := context.Background()
	m, s := seedMigrationFixture(t)
	m.Store.Meta = migrationMeta{MetaStore: s.Meta, listErr: errors.New("query failed")}
	if _, err := m.Run(ctx, migrationOpts(DirectionToRAG)); err == nil {
		t.Fatal("toRAG must surface projection query errors")
	}

	m2, s2 := seedMigrationFixture(t)
	_ = store.RecordProjection(ctx, s2.Meta, "metrics/a", "memory", "h", "f", store.ProjectionSynced)
	m2.Store.Meta = migrationMeta{MetaStore: s2.Meta, listErr: errors.New("query failed")}
	if _, err := m2.Run(ctx, migrationOpts(DirectionToCanonical)); err == nil {
		t.Fatal("toCanonical must surface projection query errors")
	}
}

func TestMigrationBundleListFailure(t *testing.T) {
	m, s := seedMigrationFixture(t)
	if err := os.RemoveAll(s.Bundle.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(context.Background(), migrationOpts(DirectionToRAG)); err == nil {
		t.Fatal("unreadable bundle must abort the job")
	}
}

func TestMigrationUnreadableBodyIsCounted(t *testing.T) {
	m, s := seedMigrationFixture(t)
	link := filepath.Join(s.Bundle.Root, "dangling.md")
	if err := os.Symlink(filepath.Join(s.Bundle.Root, "missing-target"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rep, err := m.Run(context.Background(), migrationOpts(DirectionToRAG))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed == 0 || len(rep.Errors) == 0 {
		t.Fatalf("unreadable body not reported: %+v", rep)
	}
}

func TestMigrationMalformedBodyFailsGet(t *testing.T) {
	m, s := seedMigrationFixture(t)
	bad := filepath.Join(s.Bundle.Root, "broken.md")
	if err := os.WriteFile(bad, []byte("no frontmatter here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Run(context.Background(), migrationOpts(DirectionToRAG))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed == 0 {
		t.Fatalf("malformed body not reported: %+v", rep)
	}
	projections, _ := s.Meta.ListProjections(context.Background(), meta.ProjectionQuery{ConceptID: "broken"})
	if len(projections) != 0 {
		t.Fatalf("failed read must not record a projection: %+v", projections)
	}
}

func TestMigrationPutFailureRecordsFailedProjection(t *testing.T) {
	_, s, fixtureRegistry := newWriteFixture(t)
	w := NewWriteRouter(s, fixtureRegistry, NewComposite(NewRuleEngine(), NewStubDecisioner()))
	for _, id := range []string{"metrics/a", "metrics/b"} {
		if _, err := w.Put(context.Background(), "alice", id, simpleDoc("Metric", "definition of "+id)); err != nil {
			t.Fatal(err)
		}
	}
	reg := migrationRegistry(t, memoryAdapterOf(t, fixtureRegistry), "flaky", errors.New("index down"), nil)
	m := NewMigrator(s, reg)
	opt := migrationOpts(DirectionToRAG)
	opt.Target = "flaky"
	rep, err := m.Run(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 2 || rep.Synced != 0 || len(rep.Errors) != 2 {
		t.Fatalf("put failure not reported: %+v", rep)
	}
	projections, _ := s.Meta.ListProjections(context.Background(), meta.ProjectionQuery{ConceptID: "metrics/a", Adapter: "flaky"})
	if len(projections) != 1 || projections[0].Status != store.ProjectionFailed {
		t.Fatalf("failed projection not recorded: %+v", projections)
	}
}

func TestMigrationRecordFailureIsCounted(t *testing.T) {
	m, s := seedMigrationFixture(t)
	m.Store.Meta = migrationMeta{MetaStore: s.Meta, recordErr: errors.New("write failed")}
	rep, err := m.Run(context.Background(), migrationOpts(DirectionToRAG))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 2 || rep.Synced != 0 {
		t.Fatalf("record failure not counted: %+v", rep)
	}
}

func TestMigrationToCanonicalRespectsLimit(t *testing.T) {
	ctx := context.Background()
	m, s := seedMigrationFixture(t)
	for _, id := range []string{"metrics/a", "metrics/b", "z/last"} {
		_ = store.RecordProjection(ctx, s.Meta, id, "memory", "h", "f", store.ProjectionSynced)
	}
	opt := migrationOpts(DirectionToCanonical)
	opt.Limit = 2
	first, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if first.Scanned != 2 || first.Done || first.LastConceptID == "" {
		t.Fatalf("limit not honoured: %+v", first)
	}
	second, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned != 1 || !second.Done {
		t.Fatalf("cursor not resumed: %+v", second)
	}
}

func TestMigrationToCanonicalDeleteFailure(t *testing.T) {
	ctx := context.Background()
	_, s, fixtureRegistry := newWriteFixture(t)
	_ = store.RecordProjection(ctx, s.Meta, "ghost/gone", "flaky", "h", "f", store.ProjectionSynced)
	reg := migrationRegistry(t, memoryAdapterOf(t, fixtureRegistry), "flaky", nil, errors.New("delete rejected"))
	m := NewMigrator(s, reg)
	opt := migrationOpts(DirectionToCanonical)
	opt.Target = "flaky"
	rep, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 || rep.Invalidated != 0 || len(rep.Errors) != 1 {
		t.Fatalf("delete failure not reported: %+v", rep)
	}
	projections, _ := s.Meta.ListProjections(ctx, meta.ProjectionQuery{ConceptID: "ghost/gone", Adapter: "flaky"})
	if len(projections) != 1 || projections[0].Status != store.ProjectionStale {
		t.Fatalf("orphan not marked stale after failed delete: %+v", projections)
	}
}

func TestMigrationToCanonicalDeleteUnsupportedIsIgnored(t *testing.T) {
	ctx := context.Background()
	_, s, fixtureRegistry := newWriteFixture(t)
	_ = store.RecordProjection(ctx, s.Meta, "ghost/gone", "noDelete", "h", "f", store.ProjectionSynced)
	reg := migrationRegistry(t, memoryAdapterOf(t, fixtureRegistry), "noDelete", nil, adapter.ErrUnsupported)
	m := NewMigrator(s, reg)
	opt := migrationOpts(DirectionToCanonical)
	opt.Target = "noDelete"
	rep, err := m.Run(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Invalidated != 1 || rep.Failed != 0 {
		t.Fatalf("unsupported delete must not fail the job: %+v", rep)
	}
}

func TestMigrationToCanonicalRejectsEscapingProjection(t *testing.T) {
	ctx := context.Background()
	m, s := seedMigrationFixture(t)
	_ = store.RecordProjection(ctx, s.Meta, "../escape", "memory", "h", "f", store.ProjectionSynced)
	rep, err := m.Run(ctx, migrationOpts(DirectionToCanonical))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 || len(rep.Errors) != 1 {
		t.Fatalf("escaping concept id must be reported: %+v", rep)
	}
	if rep.Invalidated != 0 {
		t.Fatalf("path error is not an orphan: %+v", rep)
	}
}
