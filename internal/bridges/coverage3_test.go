package bridges

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

func TestExportedCommandHelpers(t *testing.T) {
	if err := ValidateCommand("engine-cli"); err != nil {
		t.Fatalf("bare name rejected: %v", err)
	}
	if err := ValidateCommand(""); err != nil {
		t.Fatalf("an empty command selects the offline mock: %v", err)
	}
	if SafeArgument("héllo") != true {
		t.Fatal("UTF-8 text must be a safe argument")
	}
	if SafeArgument("a;b") {
		t.Fatal("shell metacharacters must be rejected")
	}
	if NewRunner(time.Second) == nil {
		t.Fatal("NewRunner must return a runner")
	}
}

func TestCLIBridgeUsesSpecCommandAndOfflineDelete(t *testing.T) {
	b, err := NewCLIBridge(
		config.AdapterConfig{Name: "engine"},
		EngineSpec{Name: "engine", Kind: "cli", Command: "engine-cli"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if b.command != "engine-cli" {
		t.Fatalf("command = %q", b.command)
	}

	offline, err := NewCLIBridge(config.AdapterConfig{Name: "engine"}, EngineSpec{Name: "engine", Kind: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if err := offline.Put(context.Background(), "team/a", &okf.Document{Type: "Concept", Title: "t"}); err != nil {
		t.Fatalf("offline put: %v", err)
	}
	if err := offline.Delete(context.Background(), "team/a"); err != nil {
		t.Fatalf("offline delete: %v", err)
	}
}

func TestHTTPBridgeOfflineDelete(t *testing.T) {
	b, err := NewHTTPBridge(config.AdapterConfig{Name: "engine"}, EngineSpec{Name: "engine"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(context.Background(), "team/a", &okf.Document{Type: "Concept", Title: "t"}); err != nil {
		t.Fatalf("offline put: %v", err)
	}
	if err := b.Delete(context.Background(), "team/a"); err != nil {
		t.Fatalf("offline delete: %v", err)
	}
}

func newServer(t *testing.T, h http.HandlerFunc) *HTTPBridge {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	b, err := NewHTTPBridge(
		config.AdapterConfig{Name: "engine", Endpoint: srv.URL, EgressHosts: []string{"127.0.0.1"}},
		EngineSpec{Name: "engine"},
	)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHTTPBridgeQueryPropagatesTransportError(t *testing.T) {
	b := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	if _, err := b.Query(context.Background(), adapter.Query{Text: "q"}); err == nil {
		t.Fatal("a 400 response must fail the query")
	}
}

func TestHTTPBridgeQueryRejectsUnencodableVector(t *testing.T) {
	b := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hits":[]}`))
	})
	if _, err := b.Query(context.Background(), adapter.Query{Text: "q", Vector: []float32{float32(math.NaN())}}); err == nil {
		t.Fatal("a non-finite vector cannot be serialised and must fail")
	}
}

func TestHTTPBridgeReportsTruncatedResponseBody(t *testing.T) {
	b := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write([]byte("short"))
	})
	if b.Health(context.Background()) == nil {
		t.Fatal("a truncated response body must fail the request")
	}
}

func TestPostJSONEdgeCases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	hosts := []string{"127.0.0.1"}
	ctx := context.Background()

	// A zero timeout falls back to the default instead of failing.
	if _, err := PostJSON(ctx, srv.URL, "/run", hosts, 0, map[string]string{"a": "b"}); err != nil {
		t.Fatalf("zero timeout: %v", err)
	}
	// A channel cannot be serialised.
	if _, err := PostJSON(ctx, srv.URL, "/run", hosts, time.Second, make(chan int)); err == nil {
		t.Fatal("an unserialisable payload must fail")
	}
	// Oversized payload.
	big := map[string]string{"a": strings.Repeat("x", MaxOutboundRequestBytes+10)}
	if _, err := PostJSON(ctx, srv.URL, "/run", hosts, time.Second, big); err == nil {
		t.Fatal("an oversized payload must fail")
	}
	// A control character in the path makes request construction fail.
	if _, err := PostJSON(ctx, srv.URL, "/run\x7f", hosts, time.Second, map[string]string{"a": "b"}); err == nil {
		t.Fatal("an invalid path must fail request construction")
	}
}

func TestPostJSONReportsTruncatedResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	if _, err := PostJSON(context.Background(), srv.URL, "/run", []string{"127.0.0.1"}, time.Second, map[string]string{"a": "b"}); err == nil {
		t.Fatal("a truncated response body must fail")
	}
}

// stubFS is an fs.FS that can fail on purpose.
type stubFS struct {
	entries []fs.DirEntry
	dirErr  error
	files   map[string][]byte
	readErr error
}

func (s stubFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

func (s stubFS) ReadDir(string) ([]fs.DirEntry, error) {
	if s.dirErr != nil {
		return nil, s.dirErr
	}
	return s.entries, nil
}

func (s stubFS) ReadFile(name string) ([]byte, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	if data, ok := s.files[name]; ok {
		return data, nil
	}
	return nil, errors.New("unreadable")
}

// yamlDirEntry is a minimal fs.DirEntry for the stub filesystem.
type yamlDirEntry struct{ name string }

func (e yamlDirEntry) Name() string               { return e.name }
func (e yamlDirEntry) IsDir() bool                { return false }
func (e yamlDirEntry) Type() fs.FileMode          { return 0 }
func (e yamlDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("no info") }

func withEngineFS(t *testing.T, replacement fs.FS) {
	t.Helper()
	previous := engineFS
	engineFS = replacement
	t.Cleanup(func() { engineFS = previous })
}

func TestEngineSpecsReadDirError(t *testing.T) {
	withEngineFS(t, stubFS{dirErr: errors.New("no directory")})
	if _, err := EngineSpecs(); err == nil {
		t.Fatal("a failing directory read must be reported")
	}
}

func TestEngineSpecsReadFileError(t *testing.T) {
	withEngineFS(t, stubFS{
		entries: []fs.DirEntry{yamlDirEntry{name: "engine.yaml"}},
		readErr: errors.New("unreadable"),
	})
	if _, err := EngineSpecs(); err == nil {
		t.Fatal("a failing declaration read must be reported")
	}
}

func TestEngineSpecsRejectsMalformedDeclarations(t *testing.T) {
	cases := map[string]string{
		"invalid yaml":     "name: [\n",
		"multiple docs":    "name: a\nkind: a\n---\nname: b\nkind: b\n",
		"missing name":     "kind: engine\n",
		"missing kind":     "name: engine\n",
		"unknown yaml key": "name: a\nkind: a\nbogus: b\n",
	}
	for name, data := range cases {
		withEngineFS(t, stubFS{
			entries: []fs.DirEntry{yamlDirEntry{name: "engine.yaml"}},
			files:   map[string][]byte{"engines/engine.yaml": []byte(data)},
		})
		if _, err := EngineSpecs(); err == nil {
			t.Fatalf("%s must fail closed", name)
		}
	}
}

func TestSpecForPropagatesSpecsError(t *testing.T) {
	withEngineFS(t, stubFS{dirErr: errors.New("no directory")})
	if _, err := SpecFor("engine"); err == nil {
		t.Fatal("SpecFor must surface an engine declaration failure")
	}
}

func TestRegisterKindsRejectsNilRegistryAndBadFactory(t *testing.T) {
	if err := RegisterKinds(nil); err == nil {
		t.Fatal("a nil registry must be rejected")
	}
	previous := kindFactories
	kindFactories = []struct {
		kind    string
		factory adapter.Factory
	}{{kind: "", factory: nil}}
	t.Cleanup(func() { kindFactories = previous })
	if err := RegisterKinds(adapter.NewRegistry()); err == nil {
		t.Fatal("an invalid factory must be reported")
	}
}

func TestRegisterKindsFactoriesFailWithoutSpec(t *testing.T) {
	r := adapter.NewRegistry()
	if err := RegisterKinds(r); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Adapters = []config.AdapterConfig{
		{Name: "no-such-engine", Kind: "http", Enabled: true},
		{Name: "no-such-cli-engine", Kind: "cli", Enabled: true},
	}
	if err := r.Build(cfg, adapter.Deps{}); err == nil {
		t.Fatal("a kind without an engine declaration must fail to build")
	}
}
