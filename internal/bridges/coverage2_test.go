package bridges

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

func TestSafeArgument(t *testing.T) {
	if !safeArgument("plain-id_1") {
		t.Fatal("safe arg rejected")
	}
	if safeArgument(strings.Repeat("x", 4097)) {
		t.Fatal("oversized arg")
	}
	if safeArgument("a`b") {
		t.Fatal("backtick")
	}
	if safeArgument("a\x01b") {
		t.Fatal("control char")
	}
}

func TestValidateCommand(t *testing.T) {
	if err := validateCommand(""); err != nil {
		t.Fatalf("empty command (offline) must be allowed: %v", err)
	}
	if err := validateCommand("echo"); err != nil {
		t.Fatalf("valid command: %v", err)
	}
	if err := validateCommand("/bin/sh"); err == nil {
		t.Fatal("path command must be rejected")
	}
	if err := validateCommand(strings.Repeat("a", 300)); err == nil {
		t.Fatal("oversized command must be rejected")
	}
}

func TestDefaultRunner(t *testing.T) {
	ctx := context.Background()
	r := defaultRunner(time.Second)
	if _, err := r(ctx, "true", nil, nil); err != nil {
		t.Fatalf("true: %v", err)
	}
	if out, err := r(ctx, "cat", nil, []byte("payload")); err != nil || string(out) != "payload" {
		t.Fatalf("cat stdin = %q (%v)", out, err)
	}
	if _, err := r(ctx, "false", nil, nil); err == nil {
		t.Fatal("false must report an error")
	}
}

func TestNewCLIBridgeErrors(t *testing.T) {
	if _, err := NewCLIBridge(config.AdapterConfig{}, EngineSpec{}); err == nil {
		t.Fatal("missing name must error")
	}
	if _, err := NewCLIBridge(config.AdapterConfig{Name: "x", Endpoint: "/bin/sh"}, EngineSpec{}); err == nil {
		t.Fatal("path command must error")
	}
}

func TestCLIBridgeOnline(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "T", Title: "t"}

	b := &CLIBridge{name: "x", command: "echo", runner: func(context.Context, string, []string, []byte) ([]byte, error) {
		return []byte(`{"hits":[{"concept_id":"c"}]}`), nil
	}}
	if err := b.Put(ctx, "c", doc); err != nil {
		t.Fatal(err)
	}
	hits, err := b.Query(ctx, adapter.Query{Text: "t", TopK: 3})
	if err != nil || len(hits) != 1 || hits[0].Source != "x" {
		t.Fatalf("query = %+v (%v)", hits, err)
	}
	if err := b.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if err := b.Health(ctx); err != nil {
		t.Fatal(err)
	}

	bad := &CLIBridge{name: "x", command: "echo", runner: func(context.Context, string, []string, []byte) ([]byte, error) {
		return []byte("not json"), nil
	}}
	if _, err := bad.Query(ctx, adapter.Query{Text: "t"}); err == nil {
		t.Fatal("bad JSON must error")
	}

	if _, err := b.run(ctx, []string{"a`b"}, nil); err == nil {
		t.Fatal("unsafe arg must error")
	}
	pathCmd := &CLIBridge{name: "x", command: "/bin/sh", runner: func(context.Context, string, []string, []byte) ([]byte, error) {
		return nil, nil
	}}
	if _, err := pathCmd.run(ctx, []string{"health"}, nil); err == nil {
		t.Fatal("invalid command must error at run")
	}
}

func TestNewHTTPBridgeErrors(t *testing.T) {
	if _, err := NewHTTPBridge(config.AdapterConfig{}, testSpec()); err == nil {
		t.Fatal("missing name")
	}
	if _, err := NewHTTPBridge(config.AdapterConfig{Name: "h", Endpoint: "://bad"}, testSpec()); err == nil {
		t.Fatal("bad endpoint must fail netsafe")
	}
}

func TestHTTPBridgeOnline(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "T", Title: "t"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/query":
			_, _ = w.Write([]byte(`{"hits":[{"concept_id":"c"}]}`))
		case "/bad":
			w.WriteHeader(http.StatusBadRequest)
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	b := &HTTPBridge{name: "h", endpoint: srv.URL, client: srv.Client(), maxRetries: 0}
	if err := b.Health(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, "c", doc); err != nil {
		t.Fatal(err)
	}
	hits, err := b.Query(ctx, adapter.Query{Text: "t", TopK: 1, Vector: meta.Vector{1, 2}})
	if err != nil || len(hits) != 1 || hits[0].Source != "h" || hits[0].Citation != "h:/c" {
		t.Fatalf("query = %+v (%v)", hits, err)
	}
	if err := b.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.do(ctx, http.MethodGet, "/bad", nil); err == nil {
		t.Fatal("4xx must fail immediately")
	}
	if _, err := b.do(ctx, http.MethodGet, "/boom", nil); err == nil {
		t.Fatal("5xx must fail after retries")
	}
	if _, err := b.do(ctx, "BAD METHOD", "/x", nil); err == nil {
		t.Fatal("invalid method must fail to build")
	}

	// Decode failure from a well-formed 200 with non-JSON body.
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer badSrv.Close()
	badB := &HTTPBridge{name: "h", endpoint: badSrv.URL, client: badSrv.Client(), maxRetries: 0}
	if _, err := badB.Query(ctx, adapter.Query{Text: "t"}); err == nil {
		t.Fatal("decode failure must error")
	}
}

func TestRegisterKindsBranches(t *testing.T) {
	r := adapter.NewRegistry()
	if err := RegisterKinds(r); err != nil {
		t.Fatal(err)
	}
	// Idempotent: a second call skips already-registered kinds.
	if err := RegisterKinds(r); err != nil {
		t.Fatal(err)
	}
	// An unknown engine name fails inside the factory via SpecFor.
	cfg := &config.Config{Adapters: []config.AdapterConfig{{Name: "unknown-engine", Kind: "http", Enabled: true}}}
	if err := r.Build(cfg, adapter.Deps{}); err == nil {
		t.Fatal("http factory with unknown engine must fail")
	}
	r2 := adapter.NewRegistry()
	_ = RegisterKinds(r2)
	if err := r2.Build(&config.Config{Adapters: []config.AdapterConfig{{Name: "unknown-engine", Kind: "cli", Enabled: true}}}, adapter.Deps{}); err == nil {
		t.Fatal("cli factory with unknown engine must fail")
	}
}
