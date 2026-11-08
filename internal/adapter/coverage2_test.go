package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentmem/internal/config"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

type errEmbedder struct{}

func (errEmbedder) Embed(context.Context, string) (meta.Vector, error) {
	return nil, errors.New("embed boom")
}
func (errEmbedder) Dimension() int   { return 0 }
func (errEmbedder) Identity() string { return "err" }

type lifeAdapter struct {
	name     string
	startErr error
	closeErr error
}

func (f *lifeAdapter) Name() string                                     { return f.name }
func (f *lifeAdapter) Capabilities() Capabilities                       { return Capabilities{} }
func (f *lifeAdapter) Put(context.Context, string, *okf.Document) error { return nil }
func (f *lifeAdapter) Query(context.Context, Query) ([]Hit, error)      { return nil, nil }
func (f *lifeAdapter) Delete(context.Context, string) error             { return nil }
func (f *lifeAdapter) Fingerprint() string                              { return f.name }
func (f *lifeAdapter) Health(context.Context) error                     { return nil }
func (f *lifeAdapter) Start(context.Context) error                      { return f.startErr }
func (f *lifeAdapter) Close(context.Context) error                      { return f.closeErr }

func TestMemoryAdapterBranches(t *testing.T) {
	ctx := context.Background()
	a := NewMemoryAdapter(nil)

	cancel, done := context.WithCancel(context.Background())
	done()
	if err := a.Put(cancel, "x", &okf.Document{Title: "t"}); err == nil {
		t.Fatal("cancelled put must error")
	}
	if _, err := a.Query(cancel, Query{Text: "t"}); err == nil {
		t.Fatal("cancelled query must error")
	}
	if err := a.Health(cancel); err == nil {
		t.Fatal("cancelled health must error")
	}

	// Embed error path (both Put and Query).
	ea := NewMemoryAdapter(errEmbedder{})
	if err := ea.Put(ctx, "x", &okf.Document{Title: "t"}); err == nil {
		t.Fatal("embed error on put")
	}
	if _, err := ea.Query(ctx, Query{Text: "t"}); err == nil {
		t.Fatal("embed error on query")
	}

	// Successful put + query using the supplied vector skips embedding.
	if err := a.Put(ctx, "c", &okf.Document{Title: "orders", Description: "table", Body: "row"}); err != nil {
		t.Fatal(err)
	}
	v, _ := a.embedder.Embed(ctx, "orders table row")
	hits, err := a.Query(ctx, Query{Text: "orders", Vector: v, TopK: 5})
	if err != nil || len(hits) == 0 {
		t.Fatalf("vector query = %+v (%v)", hits, err)
	}
	// A zero vector with an unrelated text yields no matches (score <= 0).
	if hits, _ := a.Query(ctx, Query{Text: "zzz", Vector: meta.Vector{0, 0}}); len(hits) != 0 {
		t.Fatalf("unrelated query = %+v", hits)
	}
	if err := a.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}

	if docText(nil) != "" {
		t.Fatal("nil doc text must be empty")
	}
	if got := shortSnippet(strings.Repeat("word ", 60)); !strings.HasSuffix(got, "...") {
		t.Fatalf("long snippet = %q", got)
	}
	if cosineVectors(meta.Vector{1}, meta.Vector{1, 2}) != 0 {
		t.Fatal("length mismatch must be 0")
	}
	if cosineVectors(meta.Vector{0}, meta.Vector{1}) != 0 {
		t.Fatal("zero norm must be 0")
	}
}

func TestSortHitsTieBreaks(t *testing.T) {
	hits := []Hit{{ConceptID: "b", Score: 1}, {ConceptID: "a", Score: 1}, {ConceptID: "c", Score: 2}}
	sortHits(hits)
	if hits[0].ConceptID != "c" || hits[1].ConceptID != "a" || hits[2].ConceptID != "b" {
		t.Fatalf("order = %+v", hits)
	}
}

func TestTruncateHitsBranches(t *testing.T) {
	in := []Hit{{ConceptID: "a"}, {ConceptID: "b"}}
	if got := truncateHits(in, 1); len(got) != 1 {
		t.Fatalf("truncate = %+v", got)
	}
	if got := truncateHits(in, 0); len(got) != 2 {
		t.Fatalf("no truncate = %+v", got)
	}
}

func TestOKFAdapterBranches(t *testing.T) {
	ctx := context.Background()
	if err := NewOKFAdapter(nil).Health(ctx); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("nil bundle health = %v", err)
	}

	dir := t.TempDir()
	b, err := okf.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("concepts/orders", &okf.Document{Type: "Concept", Title: "Orders", Description: "order table"}); err != nil {
		t.Fatal(err)
	}
	a := NewOKFAdapter(b)

	cancel, done := context.WithCancel(context.Background())
	done()
	if _, err := a.Query(cancel, Query{Text: "orders"}); err == nil {
		t.Fatal("cancelled query must error")
	}

	// Exact id match scores 1.0.
	if hits, _ := a.Query(ctx, Query{Text: "concepts/orders"}); len(hits) == 0 || hits[0].Score != 1.0 {
		t.Fatalf("exact = %+v", hits)
	}
	// Empty query scores a flat 0.5.
	if hits, _ := a.Query(ctx, Query{Text: ""}); len(hits) == 0 || hits[0].Score != 0.5 {
		t.Fatalf("empty = %+v", hits)
	}
	// Token match where the id contains the needle scores 0.95.
	if hits, _ := a.Query(ctx, Query{Text: "orders"}); len(hits) == 0 || hits[0].Score != 0.95 {
		t.Fatalf("contains = %+v", hits)
	}
	// No token overlap is skipped.
	if hits, _ := a.Query(ctx, Query{Text: "zzz"}); len(hits) != 0 {
		t.Fatalf("no match = %+v", hits)
	}
}

func TestPGVectorBranches(t *testing.T) {
	ctx := context.Background()
	if _, err := NewPGVectorAdapter(nil, nil); err == nil {
		t.Fatal("nil store must error")
	}
	store := meta.NewFakeStore()
	a, err := NewPGVectorAdapter(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	ea, _ := NewPGVectorAdapter(store, errEmbedder{})
	if err := ea.Put(ctx, "x", &okf.Document{Title: "t"}); err == nil {
		t.Fatal("embed error on put")
	}
	if _, err := ea.Query(ctx, Query{Text: "t"}); err == nil {
		t.Fatal("embed error on query")
	}

	if err := a.Put(ctx, "c", &okf.Document{Title: "t", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	hits, err := a.Query(ctx, Query{Text: "t", TopK: 5})
	if err != nil || len(hits) == 0 {
		t.Fatalf("query = %+v (%v)", hits, err)
	}
	if err := a.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingVectorDBDefaults(t *testing.T) {
	ctx := context.Background()
	r := NewRecordingVectorDB(nil)
	if err := r.PutVector(ctx, "c", meta.Vector{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueryVector(ctx, meta.Vector{1, 2}, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteVector(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if calls := r.Calls(); len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestRegistryBranches(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()

	if err := r.RegisterKind("", nil); err == nil {
		t.Fatal("empty kind must error")
	}
	if err := r.RegisterKind("memory", nil); err == nil {
		t.Fatal("duplicate kind must error")
	}
	if err := r.RegisterAdapter(nil); err == nil {
		t.Fatal("nil adapter must error")
	}
	if err := r.RegisterAdapter(NewMemoryAdapter(nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAdapter(NewMemoryAdapter(nil)); err == nil {
		t.Fatal("duplicate adapter name must error")
	}

	cfg := &config.Config{Adapters: []config.AdapterConfig{{Name: "m", Kind: "memory", Enabled: true}}}
	rb := NewRegistry()
	if err := rb.Build(nil, Deps{}); err == nil {
		t.Fatal("nil config must error")
	}
	if err := rb.Build(cfg, Deps{}); err != nil {
		t.Fatalf("build memory: %v", err)
	}
	// Duplicate name already built.
	if err := rb.Build(cfg, Deps{}); err == nil {
		t.Fatal("duplicate build must error")
	}
	// Unknown kind.
	if err := NewRegistry().Build(&config.Config{Adapters: []config.AdapterConfig{{Name: "u", Kind: "nope", Enabled: true}}}, Deps{}); err == nil {
		t.Fatal("unknown kind must error")
	}
	// Disabled adapters are skipped.
	if err := NewRegistry().Build(&config.Config{Adapters: []config.AdapterConfig{{Name: "d", Kind: "nope", Enabled: false}}}, Deps{}); err != nil {
		t.Fatalf("disabled should skip: %v", err)
	}

	// Factory error and nil factory result.
	r2 := NewRegistry()
	_ = r2.RegisterKind("boom", func(config.AdapterConfig, Deps) (Adapter, error) { return nil, errors.New("x") })
	if err := r2.Build(&config.Config{Adapters: []config.AdapterConfig{{Name: "b", Kind: "boom", Enabled: true}}}, Deps{}); err == nil {
		t.Fatal("factory error must propagate")
	}
	r3 := NewRegistry()
	_ = r3.RegisterKind("nil", func(config.AdapterConfig, Deps) (Adapter, error) { return nil, nil })
	if err := r3.Build(&config.Config{Adapters: []config.AdapterConfig{{Name: "n", Kind: "nil", Enabled: true}}}, Deps{}); err == nil {
		t.Fatal("nil factory result must error")
	}

	// StartAll / Close surface lifecycle errors.
	r4 := NewRegistry()
	_ = r4.RegisterAdapter(&lifeAdapter{name: "a", startErr: errors.New("start"), closeErr: errors.New("close")})
	if err := r4.StartAll(ctx); err == nil {
		t.Fatal("start error must propagate")
	}
	if err := r4.Close(ctx); err == nil {
		t.Fatal("close error must propagate")
	}
}

func TestItoaAndSqrt32AndEmbedCancel(t *testing.T) {
	if itoa(0) != "0" || itoa(123) != "123" {
		t.Fatalf("itoa = %q / %q", itoa(0), itoa(123))
	}
	if sqrt32(0) != 0 || sqrt32(-1) != 0 {
		t.Fatal("non-positive sqrt32 must be 0")
	}
	if v := sqrt32(4); v < 1.99 || v > 2.01 {
		t.Fatalf("sqrt32(4) = %v", v)
	}
	cancel, done := context.WithCancel(context.Background())
	done()
	if _, err := NewHashingEmbedder(4).Embed(cancel, "x"); err == nil {
		t.Fatal("cancelled embed must error")
	}
}
