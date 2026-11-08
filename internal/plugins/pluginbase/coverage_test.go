package pluginbase

import (
	"context"
	"errors"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
	"agentmem/internal/plugins/manifest"
)

func testDesc() manifest.Descriptor {
	return manifest.Descriptor{
		Name: "stub", Kind: "stub", Engine: "stub", Version: "1.0",
		Bridge: "http", ProtocolVersion: "1.0", OfflineMock: true,
		TimeoutMS: 3000, CitationScheme: "stub:/",
		Capabilities: adapter.Capabilities{Put: true, Query: true, Delete: true, Vector: true},
		Fingerprint:  "plugin/stub/v1",
	}
}

func TestPluginLifecycle(t *testing.T) {
	p, err := New(testDesc(), config.AdapterConfig{Name: "stub", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Capabilities()
	_ = p.Descriptor()
	_ = p.Status()
	_ = p.State()
	_ = p.Fingerprint()
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	doc := &okf.Document{Type: "Concept", Title: "T", Body: "b"}
	if err := p.Put(context.Background(), "x/y", doc); err != nil {
		t.Fatal(err)
	}
	hits, err := p.Query(context.Background(), adapter.Query{Text: "b", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if p.State() != StateRunning {
		t.Fatalf("state = %s", p.State())
	}
	if err := p.Delete(context.Background(), "x/y"); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Health(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("health after close = %v", err)
	}
	if _, err := p.Query(context.Background(), adapter.Query{Text: "b"}); !errors.Is(err, ErrClosed) {
		t.Fatal("query after close must fail")
	}
	if err := p.Delete(context.Background(), "x/y"); !errors.Is(err, ErrClosed) {
		t.Fatal("delete after close must fail")
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal("close must be idempotent")
	}
}

func TestPluginNewUnsupportedBridge(t *testing.T) {
	bad := testDesc()
	bad.Bridge = "weird"
	if _, err := New(bad, config.AdapterConfig{Name: "x"}); err == nil {
		t.Fatal("unsupported bridge must fail")
	}
}

func TestPluginCloseCancelledContext(t *testing.T) {
	p, _ := New(testDesc(), config.AdapterConfig{Name: "stub", Enabled: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close with cancelled ctx = %v", err)
	}
}
