package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentmem/internal/config"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// TestUnsupportedErrorMatchesFreshInstance exercises unsupportedError.Is with a
// different pointer than the package-level sentinel.
func TestUnsupportedErrorMatchesFreshInstance(t *testing.T) {
	if !errors.Is(ErrUnsupported, &unsupportedError{}) {
		t.Fatal("a fresh unsupportedError must match the sentinel")
	}
	if errors.Is(ErrUnsupported, errors.New("other")) {
		t.Fatal("an unrelated error must not match")
	}
}

// plainAdapter deliberately does not implement Lifecycle.
type plainAdapter struct{ name string }

func (p *plainAdapter) Name() string                 { return p.name }
func (p *plainAdapter) Capabilities() Capabilities   { return Capabilities{Query: true} }
func (p *plainAdapter) Fingerprint() string          { return "plain/" + p.name }
func (p *plainAdapter) Health(context.Context) error { return nil }
func (p *plainAdapter) Put(context.Context, string, *okf.Document) error {
	return ErrUnsupported
}
func (p *plainAdapter) Query(context.Context, Query) ([]Hit, error) { return nil, nil }
func (p *plainAdapter) Delete(context.Context, string) error        { return ErrUnsupported }

func TestLifecycleSkipsAdaptersWithoutLifecycle(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterAdapter(&plainAdapter{name: "plain"}); err != nil {
		t.Fatal(err)
	}
	if err := r.StartAll(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func newTestBundle(t *testing.T) *okf.Bundle {
	t.Helper()
	b, err := okf.Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOKFAdapterQueryListError(t *testing.T) {
	b := newTestBundle(t)
	b.Root = filepath.Join(t.TempDir(), "missing", "root")
	a := NewOKFAdapter(b)
	if _, err := a.Query(context.Background(), Query{Text: "x"}); err == nil {
		t.Fatal("a missing bundle root must fail List and surface the error")
	}
}

// requireDACEnforcement skips a test that asserts on kernel permission checks.
// Root holds CAP_DAC_OVERRIDE, so a 0o000 file stays readable and the
// assertion cannot hold; a root CI container would otherwise fail on correct
// code.
func requireDACEnforcement(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses DAC checks (CAP_DAC_OVERRIDE)")
	}
}

func TestOKFAdapterToleratesUnreadableConcept(t *testing.T) {
	requireDACEnforcement(t)
	b := newTestBundle(t)
	if _, err := b.Put("team/readable", &okf.Document{Type: "Concept", Title: "Revenue", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("team/broken", &okf.Document{Type: "Concept", Title: "Revenue", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	p, err := b.Path("team/broken")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o000); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })
	hits, err := NewOKFAdapter(b).Query(context.Background(), Query{Text: "revenue"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, h := range hits {
		if h.ConceptID == "team/broken" {
			t.Fatal("an unreadable concept must be skipped")
		}
	}
}

func TestOKFAdapterSnippetFallsBackToTitleAndTiesBreakOnID(t *testing.T) {
	b := newTestBundle(t)
	// No description: the snippet must fall back to the title.
	for _, id := range []string{"team/b", "team/a"} {
		if _, err := b.Put(id, &okf.Document{Type: "Concept", Title: "Revenue"}); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := NewOKFAdapter(b).Query(context.Background(), Query{Text: "revenue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].ConceptID != "team/a" || hits[0].Snippet != "Revenue" {
		t.Fatalf("equal scores must sort by concept id: %+v", hits)
	}
}

// failingVectorStore fails every vector operation.
type failingVectorStore struct{ err error }

func (f *failingVectorStore) PutVector(context.Context, string, meta.Vector) error { return f.err }
func (f *failingVectorStore) QueryVector(context.Context, meta.Vector, int) ([]meta.VectorHit, error) {
	return nil, f.err
}
func (f *failingVectorStore) DeleteVector(context.Context, string) error { return f.err }

func TestPGVectorAdapterQueryVectorError(t *testing.T) {
	a, err := NewPGVectorAdapter(&failingVectorStore{err: errors.New("boom")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Query(context.Background(), Query{Text: "x"}); err == nil {
		t.Fatal("a failing vector store must surface the error")
	}
	v := meta.Vector{1, 0}
	if _, err := a.Query(context.Background(), Query{Vector: v}); err == nil {
		t.Fatal("a pre-computed vector must still surface store errors")
	}
}

func TestOKFAdapterQueryOrdersDistinctScores(t *testing.T) {
	b := newTestBundle(t)
	// An exact id match scores 1.0, a token overlap scores below it.
	if _, err := b.Put("metrics/revenue", &okf.Document{Type: "Concept", Title: "Revenue", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("other/note", &okf.Document{Type: "Concept", Title: "Revenue detail"}); err != nil {
		t.Fatal(err)
	}
	hits, err := NewOKFAdapter(b).Query(context.Background(), Query{Text: "metrics/revenue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ConceptID != "metrics/revenue" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestPGVectorAdapterQuerySortsHits(t *testing.T) {
	store := meta.NewFakeStore()
	ctx := context.Background()
	if err := store.PutVector(ctx, "b/near", meta.Vector{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutVector(ctx, "a/far", meta.Vector{1, 1}); err != nil {
		t.Fatal(err)
	}
	a, err := NewPGVectorAdapter(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := a.Query(ctx, Query{Vector: meta.Vector{1, 0}, TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ConceptID != "b/near" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestRegistryBuildRejectsDuplicateName(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterAdapter(&plainAdapter{name: "okf"}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Adapters = []config.AdapterConfig{{Name: "okf", Kind: "okf", Enabled: true}}
	if err := r.Build(cfg, Deps{}); err == nil {
		t.Fatal("building an already-registered name must fail")
	}
}
