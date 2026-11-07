package pluginbase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
	"agentmem/internal/plugins/manifest"
)

const testManifest = `
name: demo
kind: demo
engine: demo
version: "1.0.0"
bridge: http
protocol_version: agentmem-bridge/v1
timeout_ms: 1000
offline_mock: true
citation_scheme: "demo:/"
capabilities:
  vector: true
  semantic: false
  graph: false
  page_level: false
sdk_boundary:
  sidecar: "Demo sidecar"
  wire_protocol: agentmem-bridge/v1
  endpoints: ["GET /healthz", "POST /query"]
  capability_map:
    vector: "Demo vector index"
  credentials_ref: "AGENTMEM_DEMO_TOKEN"
`

func descriptor(t *testing.T) manifest.Descriptor {
	t.Helper()
	d, err := manifest.Parse([]byte(testManifest))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestOfflineLifecycleAndCitation(t *testing.T) {
	ctx := context.Background()
	p, err := New(descriptor(t), config.AdapterConfig{Name: "demo", Kind: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if p.State() != StateNew || p.Status()["mode"] != "offline-mock" {
		t.Fatalf("initial status = %+v", p.Status())
	}
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start must be idempotent: %v", err)
	}
	if err := p.Put(ctx, "c1", &okf.Document{Type: "Concept", Body: "alpha"}); err != nil {
		t.Fatal(err)
	}
	hits, err := p.Query(ctx, adapter.Query{Text: "alpha", TopK: 3})
	if err != nil || len(hits) == 0 {
		t.Fatalf("query: %v / %+v", err, hits)
	}
	if hits[0].Source != "demo" || !strings.HasPrefix(hits[0].Citation, "demo:/") {
		t.Fatalf("provenance = %+v", hits[0])
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("close must be idempotent: %v", err)
	}
	if p.State() != StateClosed {
		t.Fatalf("state = %q", p.State())
	}
	if err := p.Health(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("health after close = %v", err)
	}
	if err := p.Put(ctx, "c2", &okf.Document{Type: "Concept"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("put after close = %v", err)
	}
	if _, err := p.Query(ctx, adapter.Query{Text: "x"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("query after close = %v", err)
	}
	if err := p.Start(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("start after close = %v", err)
	}
}

func TestSidecarWireProtocolAndProvenance(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"hits": []map[string]any{{
					"concept_id": "p17", "score": 0.9, "snippet": "page 17",
					"source": "demo", "citation": "demo://page/17",
				}},
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := New(descriptor(t), config.AdapterConfig{Name: "demo", Kind: "demo", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status()["mode"] != "sidecar" {
		t.Fatalf("mode = %s", p.Status()["mode"])
	}
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := p.Put(ctx, "c1", &okf.Document{Type: "Concept", Body: "alpha"}); err != nil {
		t.Fatal(err)
	}
	hits, err := p.Query(ctx, adapter.Query{Text: "alpha", TopK: 2})
	if err != nil || len(hits) != 1 {
		t.Fatalf("query: %v / %+v", err, hits)
	}
	if hits[0].Citation != "demo://page/17" {
		t.Fatalf("sidecar citation overwritten: %q", hits[0].Citation)
	}
	if err := p.Delete(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /healthz", "POST /put", "POST /query", "POST /delete"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("wire calls = %v, want %v", seen, want)
	}
}

func TestStartFailsClosedOnUnhealthySidecar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx := context.Background()
	p, err := New(descriptor(t), config.AdapterConfig{Name: "demo", Kind: "demo", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(ctx); err == nil {
		t.Fatal("unhealthy sidecar must fail closed")
	}
	if p.State() != StateNew {
		t.Fatalf("failed start must not mark running, got %q", p.State())
	}
}
