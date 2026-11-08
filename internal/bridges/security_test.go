package bridges

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
)

func TestHTTPBridgeRejectsNonAllowlistedEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"http://169.254.169.254/latest/meta-data",
		"file:///etc/passwd",
		"https://evil.example.net/put",
		"https://user:pass@api.example.com/put",
	} {
		_, err := NewHTTPBridge(config.AdapterConfig{
			Name: "engine", Endpoint: endpoint,
			EgressHosts: []string{"api.example.com"},
		}, testSpec())
		if err == nil {
			t.Fatalf("endpoint %q must fail closed", endpoint)
		}
	}
}

func TestHTTPBridgeRejectsCrossHostRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer srv.Close()
	b, err := NewHTTPBridge(config.AdapterConfig{
		Name: "engine", Endpoint: srv.URL, TimeoutMS: 500,
	}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Health(context.Background()); err == nil {
		t.Fatal("a redirect to an unallowlisted host must be rejected")
	}
}

func TestHTTPBridgeAllowsSameHostRedirect(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Redirect(w, r, srv.URL+"/final", http.StatusFound)
	}))
	defer srv.Close()
	b, err := NewHTTPBridge(config.AdapterConfig{
		Name: "engine", Endpoint: srv.URL, TimeoutMS: 500,
	}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Health(context.Background()); err != nil {
		t.Fatalf("same-host redirect must be allowed: %v", err)
	}
}

func TestHTTPBridgeErrorDoesNotLeakURL(t *testing.T) {
	b, err := NewHTTPBridge(config.AdapterConfig{
		Name: "engine", Endpoint: "http://127.0.0.1:1", TimeoutMS: 100,
		EgressHosts: []string{"127.0.0.1"},
	}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	err = b.Health(context.Background())
	if err == nil {
		t.Fatal("unreachable endpoint must fail")
	}
	if strings.Contains(err.Error(), "127.0.0.1") ||
		strings.Contains(err.Error(), "http://") {
		t.Fatalf("transport error leaked the endpoint: %s", err)
	}
}

func TestCLIBridgeRejectsPathAndShellCommands(t *testing.T) {
	for _, command := range []string{
		"/bin/sh", "../evil", "sh -c", "cmd.exe;rm", "C:\\Windows\\cmd", "$(id)",
	} {
		_, err := NewCLIBridge(config.AdapterConfig{
			Name: "cli", Endpoint: command,
		}, testSpec())
		if err == nil {
			t.Fatalf("CLI command %q must fail closed", command)
		}
	}
}

func TestCLIBridgeAcceptsUTF8ButRejectsMetacharacters(t *testing.T) {
	b, err := NewCLIBridge(config.AdapterConfig{
		Name: "cli", Endpoint: "fake-engine",
	}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	b.runner = func(context.Context, string, []string, []byte) ([]byte, error) {
		return []byte(`{"hits":[]}`), nil
	}
	ctx := context.Background()
	if _, err := b.Query(ctx, adapter.Query{Text: "订单 2026 年度收入"}); err != nil {
		t.Fatalf("UTF-8 query text must be usable: %v", err)
	}
	for _, text := range []string{"$(id)", "`id`", "a;b", "a|b", "a\nb", "a'b"} {
		if _, err := b.Query(ctx, adapter.Query{Text: text}); err == nil {
			t.Fatalf("unsafe CLI argument %q must be rejected", text)
		}
	}
}

func TestDefaultRunnerNeverEchoesStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh unavailable: %v", err)
	}
	_, err := defaultRunner(2*time.Second)(context.Background(), "sh",
		[]string{"-c", "printf 'secret-stderr-marker' >&2; exit 1"}, nil)
	if err == nil {
		t.Fatal("a failing command must return an error")
	}
	if strings.Contains(err.Error(), "secret-stderr-marker") {
		t.Fatalf("external stderr leaked into the error: %s", err)
	}
}

func TestEngineSpecsSurviveStrictParsing(t *testing.T) {
	specs, err := EngineSpecs()
	if err != nil {
		t.Fatalf("embedded engine declarations must parse with KnownFields(true): %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("no engine specs loaded")
	}
	for _, s := range specs {
		if s.Name == "" || s.Kind == "" {
			t.Fatalf("incomplete engine spec: %+v", s)
		}
	}
}
