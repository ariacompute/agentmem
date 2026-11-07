package adapter

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agentmem/internal/config"
	"agentmem/internal/okf"
)

func testDeps(t *testing.T) Deps {
	t.Helper()
	b, err := okf.Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	return Deps{Bundle: b, Embedder: NewHashingEmbedder(32)}
}

func TestBuildDefaultAdapters(t *testing.T) {
	r := NewRegistry()
	cfg := config.Default()
	if err := r.Build(cfg, testDeps(t)); err != nil {
		t.Fatal(err)
	}
	names := r.Names()
	if len(names) != 2 || names[0] != "memory" || names[1] != "okf" {
		t.Fatalf("names = %v", names)
	}
	if _, ok := r.Get("okf"); !ok {
		t.Fatal("okf adapter missing")
	}
	health := r.HealthAll(context.Background())
	for name, err := range health {
		if err != nil {
			t.Fatalf("health %s: %v", name, err)
		}
	}
}

func TestDisabledAdaptersAreNotBuilt(t *testing.T) {
	r := NewRegistry()
	cfg := config.Default()
	cfg.Adapters = []config.AdapterConfig{
		{Name: "okf", Kind: "okf", Enabled: true},
		{Name: "memory", Kind: "memory", Enabled: false},
		{Name: "pgvector", Kind: "pgvector", Enabled: false},
	}
	if err := r.Build(cfg, testDeps(t)); err != nil {
		t.Fatal(err)
	}
	if len(r.Names()) != 1 || r.Names()[0] != "okf" {
		t.Fatalf("disabled adapter was built: %v", r.Names())
	}
}

func TestPGVectorFallsBackToFakeOffline(t *testing.T) {
	r := NewRegistry()
	cfg := config.Default()
	cfg.Adapters = []config.AdapterConfig{{Name: "pgvector", Kind: "pgvector", Enabled: true}}
	if err := r.Build(cfg, testDeps(t)); err != nil {
		t.Fatalf("pgvector must build offline: %v", err)
	}
	a, _ := r.Get("pgvector")
	if err := a.Put(context.Background(), "c", fixtureDoc("C", "body")); err != nil {
		t.Fatalf("fake pgvector put: %v", err)
	}
}

func TestUnknownKindIsUnsupported(t *testing.T) {
	r := NewRegistry()
	cfg := config.Default()
	cfg.Adapters = []config.AdapterConfig{{Name: "x", Kind: "mystery", Enabled: true}}
	if err := r.Build(cfg, testDeps(t)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestDuplicateRegistrationFails(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterKind("memory", func(config.AdapterConfig, Deps) (Adapter, error) {
		return NewMemoryAdapter(nil), nil
	}); err == nil {
		t.Fatal("duplicate kind must fail")
	}
	a := NewMemoryAdapter(nil)
	if err := r.RegisterAdapter(a); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAdapter(a); err == nil {
		t.Fatal("duplicate name must fail")
	}
}

func TestCustomFactoryExtendsRegistry(t *testing.T) {
	r := NewRegistry()
	called := false
	if err := r.RegisterKind("custom", func(cfg config.AdapterConfig, d Deps) (Adapter, error) {
		called = true
		if cfg.Name != "third-party" {
			t.Fatalf("name = %q", cfg.Name)
		}
		return &namedAdapter{name: "third-party"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Adapters = []config.AdapterConfig{{Name: "third-party", Kind: "custom", Enabled: true}}
	if err := r.Build(cfg, testDeps(t)); err != nil {
		t.Fatal(err)
	}
	if !called || r.Names()[0] != "third-party" {
		t.Fatalf("custom factory not used: %v", r.Names())
	}
}

func TestBuildRequiresBundleForOKF(t *testing.T) {
	r := NewRegistry()
	cfg := config.Default()
	cfg.Adapters = []config.AdapterConfig{{Name: "okf", Kind: "okf", Enabled: true}}
	if err := r.Build(cfg, Deps{}); err == nil {
		t.Fatal("okf adapter without bundle must fail")
	}
}

type namedAdapter struct{ name string }

func (n *namedAdapter) Name() string                 { return n.name }
func (n *namedAdapter) Capabilities() Capabilities   { return Capabilities{Query: true} }
func (n *namedAdapter) Fingerprint() string          { return "custom-v1" }
func (n *namedAdapter) Health(context.Context) error { return nil }
func (n *namedAdapter) Put(context.Context, string, *okf.Document) error {
	return ErrUnsupported
}
func (n *namedAdapter) Query(context.Context, Query) ([]Hit, error) { return nil, nil }
func (n *namedAdapter) Delete(context.Context, string) error        { return ErrUnsupported }
