package bridges

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

func bridgeDoc() *okf.Document {
	return &okf.Document{Type: "Concept", Title: "Orders", Description: "orders table", Body: "# Body"}
}

func testSpec() EngineSpec {
	return EngineSpec{Name: "stub", Kind: "http", Capabilities: adapter.Capabilities{Vector: true}}
}

func TestEngineSpecsCoverFiveEngines(t *testing.T) {
	specs, err := EngineSpecs()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"llamaindex": false, "pageindex": false, "graphrag": false,
		"lightrag": false, "pgvector": false,
	}
	for _, s := range specs {
		if _, ok := want[s.Name]; !ok {
			t.Fatalf("unexpected engine %q", s.Name)
		}
		want[s.Name] = true
		if !s.OfflineMock {
			t.Fatalf("%s must be offline-mockable", s.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing engine spec %q", name)
		}
	}
	page, err := SpecFor("pageindex")
	if err != nil || !page.Capabilities.PageLevel {
		t.Fatalf("pageindex capabilities: %+v (%v)", page, err)
	}
	if _, err := SpecFor("missing"); err == nil {
		t.Fatal("unknown engine must fail")
	}
}

func TestHTTPBridgeWireProtocol(t *testing.T) {
	var putSeen, deleteSeen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/put":
			var req putRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConceptID != "tables/orders" {
				t.Errorf("bad put payload: %+v (%v)", req, err)
			}
			putSeen.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/query":
			var req queryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Text != "orders" || req.TopK != 3 {
				t.Errorf("bad query payload: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(queryResponse{Hits: []adapter.Hit{{ConceptID: "tables/orders", Score: 0.9}}})
		case "/delete":
			deleteSeen.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	b, err := NewHTTPBridge(config.AdapterConfig{Name: "llamaindex", Endpoint: srv.URL, TimeoutMS: 1000}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b.Health(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, "tables/orders", bridgeDoc()); err != nil {
		t.Fatal(err)
	}
	hits, err := b.Query(ctx, adapter.Query{Text: "orders", TopK: 3})
	if err != nil || len(hits) != 1 {
		t.Fatalf("query = %+v (%v)", hits, err)
	}
	if hits[0].Source != "llamaindex" || hits[0].Citation != "llamaindex:/tables/orders" {
		t.Fatalf("provenance not filled: %+v", hits[0])
	}
	if err := b.Delete(ctx, "tables/orders"); err != nil {
		t.Fatal(err)
	}
	if putSeen.Load() != 1 || deleteSeen.Load() != 1 {
		t.Fatalf("put=%d delete=%d", putSeen.Load(), deleteSeen.Load())
	}
}

func TestHTTPBridgeOfflineMock(t *testing.T) {
	b, err := NewHTTPBridge(config.AdapterConfig{Name: "graphrag"}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b.Health(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, "c", bridgeDoc()); err != nil {
		t.Fatal(err)
	}
	hits, err := b.Query(ctx, adapter.Query{Text: "orders", TopK: 5})
	if err != nil || len(hits) == 0 {
		t.Fatalf("offline mock query = %+v (%v)", hits, err)
	}
}

func TestHTTPBridgeRetryPolicy(t *testing.T) {
	var calls4xx, calls5xx atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/4") {
			calls4xx.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls5xx.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b, _ := NewHTTPBridge(config.AdapterConfig{Name: "s", Endpoint: srv.URL, TimeoutMS: 1000}, testSpec())
	if _, err := b.do(context.Background(), http.MethodGet, "/4xx", nil); err == nil {
		t.Fatal("4xx must fail")
	}
	if calls4xx.Load() != 1 {
		t.Fatalf("4xx should not be retried, got %d calls", calls4xx.Load())
	}
	if _, err := b.do(context.Background(), http.MethodGet, "/5xx", nil); err == nil {
		t.Fatal("5xx must fail after bounded retries")
	}
	if calls5xx.Load() != 2 {
		t.Fatalf("expected 2 attempts (maxRetries=1), got %d", calls5xx.Load())
	}
}

func TestCLIBridgeRunnerAndWhitelist(t *testing.T) {
	b, err := NewCLIBridge(config.AdapterConfig{Name: "clirag", Endpoint: "fake-engine", TimeoutMS: 1000}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	var seen [][]string
	b.runner = func(ctx context.Context, command string, args []string, stdin []byte) ([]byte, error) {
		if command != "fake-engine" {
			t.Fatalf("command = %q", command)
		}
		seen = append(seen, append([]string(nil), args...))
		switch args[0] {
		case "query":
			if len(stdin) != 0 {
				t.Fatal("query should not send stdin")
			}
			return []byte(`{"hits":[{"concept_id":"c","score":0.7}]}`), nil
		case "put":
			var req putRequest
			if err := json.Unmarshal(stdin, &req); err != nil || req.ConceptID != "c" {
				t.Fatalf("put stdin = %s (%v)", stdin, err)
			}
			return []byte(`{}`), nil
		default:
			return []byte(`{}`), nil
		}
	}
	ctx := context.Background()
	if err := b.Health(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, "c", bridgeDoc()); err != nil {
		t.Fatal(err)
	}
	hits, err := b.Query(ctx, adapter.Query{Text: "alpha", TopK: 2})
	if err != nil || len(hits) != 1 || hits[0].Source != "clirag" {
		t.Fatalf("cli query = %+v (%v)", hits, err)
	}
	if err := b.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 || seen[1][0] != "put" || seen[2][0] != "query" {
		t.Fatalf("call sequence = %+v", seen)
	}
	if _, err := b.Query(ctx, adapter.Query{Text: "bad;rm -rf"}); err == nil {
		t.Fatal("unsafe CLI argument must be rejected")
	}
}

func TestCLIBridgeOfflineMock(t *testing.T) {
	b, err := NewCLIBridge(config.AdapterConfig{Name: "clirag"}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b.Put(ctx, "c", bridgeDoc()); err != nil {
		t.Fatal(err)
	}
	if hits, err := b.Query(ctx, adapter.Query{Text: "orders"}); err != nil || len(hits) == 0 {
		t.Fatalf("offline cli mock = %+v (%v)", hits, err)
	}
}

func TestRegisterKindsBuildsAllEnginesOffline(t *testing.T) {
	r := adapter.NewRegistry()
	if err := RegisterKinds(r); err != nil {
		t.Fatal(err)
	}
	if err := RegisterKinds(r); err != nil {
		t.Fatalf("RegisterKinds must be idempotent: %v", err)
	}
	cfg := config.Default()
	cfg.Adapters = []config.AdapterConfig{
		{Name: "llamaindex", Kind: "http", Enabled: true},
		{Name: "pageindex", Kind: "http", Enabled: true},
		{Name: "graphrag", Kind: "cli", Enabled: true},
		{Name: "lightrag", Kind: "http", Enabled: true},
		{Name: "pgvector", Kind: "pgvector", Enabled: true},
	}
	if err := r.Build(cfg, adapter.Deps{}); err != nil {
		t.Fatalf("all five engines must build offline: %v", err)
	}
	if len(r.Names()) != 5 {
		t.Fatalf("built = %v", r.Names())
	}
	for name, err := range r.HealthAll(context.Background()) {
		if err != nil {
			t.Fatalf("health %s: %v", name, err)
		}
	}
	for _, name := range r.Names() {
		a, _ := r.Get(name)
		if err := a.Put(context.Background(), "c", bridgeDoc()); err != nil {
			t.Fatalf("%s put: %v", name, err)
		}
		if hits, err := a.Query(context.Background(), adapter.Query{Text: "orders", TopK: 3}); err != nil || len(hits) == 0 {
			t.Fatalf("%s query = %+v (%v)", name, hits, err)
		}
	}
}
