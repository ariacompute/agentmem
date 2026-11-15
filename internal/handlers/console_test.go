package handlers

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmem/internal/webui"
)

// withConsole attaches the real embedded console to a posture. The console is
// part of the binary, so the test must not fake it: a fake would not catch a
// missing dist or a policy that cannot render the page.
func withConsole(t *testing.T, opts Options) Options {
	t.Helper()
	console, err := webui.New()
	if err != nil {
		t.Fatalf("webui.New: %v", err)
	}
	opts.Console = console
	return opts
}

func consoleAsset(t *testing.T) string {
	t.Helper()
	files, err := webui.FS()
	if err != nil {
		t.Fatalf("webui.FS: %v", err)
	}
	entries, err := fs.ReadDir(files, "assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded assets: %v", err)
	}
	return "/assets/" + entries[0].Name()
}

func TestConsoleIsPublicWhileTheAPIKeepsItsPolicy(t *testing.T) {
	opts, tokens := securedOptions(t)
	srv := httptest.NewServer(NewHandler(newTestService(t), withConsole(t, opts)))
	defer srv.Close()

	// The page is readable without credentials, like GET /healthz.
	resp, body := doJSON(t, http.MethodGet, srv.URL+"/", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("GET / content type = %q", contentType)
	}
	if !strings.Contains(body, `id="app"`) {
		t.Fatal("GET / did not serve the console page")
	}
	policy := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'self'") || !strings.Contains(policy, "'sha256-") {
		t.Fatalf("console CSP = %q", policy)
	}
	if strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("the console page must be revalidatable, not no-store")
	}

	asset := consoleAsset(t)
	assetResp, _ := doJSON(t, http.MethodGet, srv.URL+asset, nil, "")
	if assetResp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", asset, assetResp.StatusCode)
	}
	if cache := assetResp.Header.Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("asset cache control = %q", cache)
	}

	// JSON answers keep the deny-all policy and stay uncached.
	apiResp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts",
		map[string]string{"Authorization": tokens["admin"]}, "")
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/concepts = %d", apiResp.StatusCode)
	}
	apiPolicy := apiResp.Header.Get("Content-Security-Policy")
	if strings.Contains(apiPolicy, "script-src") {
		t.Fatalf("API answers must not admit scripts: %q", apiPolicy)
	}
	if apiResp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("API cache control = %q", apiResp.Header.Get("Cache-Control"))
	}
}

func TestConsoleDoesNotSwallowAPIRoutes(t *testing.T) {
	srv := newTestServerWithOptions(t, withConsole(t, openOptions()))
	defer srv.Close()

	for _, target := range []string{"/v1/unknown", "/v1/knowledge/extra", "/nope"} {
		resp, body := doJSON(t, http.MethodGet, srv.URL+target, nil, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d", target, resp.StatusCode)
		}
		var payload map[string]string
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("GET %s answered non-JSON %q", target, body)
		}
		if payload["error"] == "" {
			t.Fatalf("GET %s body = %q", target, body)
		}
		if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
			t.Fatalf("GET %s content type = %q", target, contentType)
		}
	}

	// The health check and the nine API routes are untouched.
	health, _ := doJSON(t, http.MethodGet, srv.URL+"/healthz", nil, "")
	if health.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz = %d", health.StatusCode)
	}
}

func TestConsoleRejectsNonReadMethods(t *testing.T) {
	srv := newTestServerWithOptions(t, withConsole(t, openOptions()))
	defer srv.Close()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		resp, _ := doJSON(t, method, srv.URL+"/", nil, "")
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s / = %d", method, resp.StatusCode)
		}
	}
}
