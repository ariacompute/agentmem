package webui

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func newConsole(t *testing.T) *Handler {
	t.Helper()
	h, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return h
}

func assetNames(t *testing.T) []string {
	t.Helper()
	files, err := FS()
	if err != nil {
		t.Fatalf("FS(): %v", err)
	}
	entries, err := fs.ReadDir(files, "assets")
	if err != nil {
		t.Fatalf("assets/ is missing from the embedded console: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestIndexAnswersWithTheConsolePolicy(t *testing.T) {
	h := newConsole(t)
	rec := serve(h, http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("GET / content type = %q", contentType)
	}
	if !strings.Contains(rec.Body.String(), `id="app"`) {
		t.Fatal("GET / did not return the console page")
	}
	policy := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'none'",
		"script-src 'self' 'sha256-",
		"style-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("CSP %q lacks %q", policy, want)
		}
	}
	if policy != h.CSP() {
		t.Fatal("the served policy and CSP() drifted")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index cache control = %q", got)
	}
	for _, header := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rec.Header().Get(header) == "" {
			t.Fatalf("%s is missing", header)
		}
	}
}

// The digest is computed from the embedded page, so the Go header can never
// drift from the inline script the browser receives.
func TestPolicyDigestMatchesTheEmbeddedInlineScript(t *testing.T) {
	h := newConsole(t)
	files, err := FS()
	if err != nil {
		t.Fatalf("FS(): %v", err)
	}
	index, err := fs.ReadFile(files, indexName)
	if err != nil {
		t.Fatalf("read %s: %v", indexName, err)
	}
	digest, err := inlineDigest(index)
	if err != nil {
		t.Fatalf("inlineDigest: %v", err)
	}
	if !strings.Contains(h.CSP(), digest) {
		t.Fatalf("CSP %q does not admit %s", h.CSP(), digest)
	}
}

// A half-built dist would serve a page pointing at files that do not exist.
func TestIndexReferencesOnlyEmbeddedAssets(t *testing.T) {
	files, err := FS()
	if err != nil {
		t.Fatalf("FS(): %v", err)
	}
	index, err := fs.ReadFile(files, indexName)
	if err != nil {
		t.Fatalf("read %s: %v", indexName, err)
	}
	references := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(string(index), -1)
	if len(references) == 0 {
		t.Fatal("index.html references no bundled asset")
	}
	for _, match := range references {
		name := strings.TrimPrefix(match[1], "/")
		if _, err := fs.ReadFile(files, name); err != nil {
			t.Fatalf("index.html references a missing asset %q: %v", name, err)
		}
	}
}

func TestHashedAssetsAreCacheableForever(t *testing.T) {
	h := newConsole(t)
	names := assetNames(t)
	for _, name := range names {
		rec := serve(h, http.MethodGet, "/assets/"+name)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /assets/%s = %d", name, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("%s cache control = %q", name, got)
		}
		if rec.Header().Get("Content-Type") == "" {
			t.Fatalf("%s has no content type", name)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}

func TestHeadCarriesHeadersWithoutABody(t *testing.T) {
	h := newConsole(t)
	rec := serve(h, http.MethodHead, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD / = %d", rec.Code)
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD / must advertise a content length")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD / returned %d body bytes", rec.Body.Len())
	}
}

func TestUnknownPathsAnswerJSONNotFound(t *testing.T) {
	h := newConsole(t)
	for _, target := range []string{
		"/nope",
		"/assets/",
		"/assets/../index.html",
		"/assets/missing.js",
		"/v1/knowledge",
	} {
		rec := serve(h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", target, rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "not found" {
			t.Fatalf("GET %s body = %q", target, rec.Body.String())
		}
	}
}

func TestWriteMethodsAreRejectedInJSON(t *testing.T) {
	h := newConsole(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := serve(h, method, "/")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s / = %d", method, rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
			t.Fatalf("%s / body = %q", method, rec.Body.String())
		}
	}
}

func TestPublicAllowlistIsClosed(t *testing.T) {
	h := newConsole(t)
	name := assetNames(t)[0]
	for _, target := range []string{"/", "/index.html", "/assets/" + name} {
		if !h.Public(target) {
			t.Fatalf("%s must be public", target)
		}
	}
	for _, target := range []string{
		"", "/v1", "/v1/knowledge", "/healthz", "/index.htm",
		"/assets/", "/assets/../index.html", "/assetsx/a.js",
	} {
		if h.Public(target) {
			t.Fatalf("%q must not be public", target)
		}
	}
}
