package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/auth"
	"agentmem/internal/config"
	"agentmem/internal/service"
)

const handlerSQL = `SELECT SUM(amount) AS revenue_ytd
FROM orders
WHERE EXTRACT(YEAR FROM placed_at) = :year
  AND status = 'booked'
`

const handlerPostgresDoc = `---
type: Attested Computation
title: Revenue YTD
runtime: postgres
verified:
  - by: human:jsmith@acme
parameters:
  - name: year
    type: integer
    required: true
computation: references/revenue.sql
executor:
  resource: runtime/postgres.md
  receipt: [job_id, executed_sql, result]
attester:
  resource: attesters/sql_equality.go
---
# Computation
`

func writeHandlerBundle(t *testing.T, root string) {
	t.Helper()
	for rel, data := range map[string]string{
		"references/revenue.sql":   handlerSQL,
		"computations/revenue.md":  handlerPostgresDoc,
		"computations/bigquery.md": strings.Replace(handlerPostgresDoc, "runtime: postgres", "runtime: bigquery", 1),
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestService(t *testing.T) *service.KnowledgeService {
	t.Helper()
	cfg := config.Default()
	cfg.Git.Root = filepath.Join(t.TempDir(), "bundle")
	writeHandlerBundle(t, cfg.Git.Root)
	svc, err := service.Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func mustAuth(t *testing.T, specs []auth.TokenSpec, enabled bool) *auth.Authenticator {
	t.Helper()
	a, err := auth.New(specs, enabled)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// openOptions is the offline/dev posture: authentication disabled, local
// principal with every scope, and a rate limit high enough not to interfere
// with functional assertions.
func openOptions() Options {
	return Options{
		Auth:         mustAuthUnchecked(nil, false),
		LocalSubject: "system",
		RateRPS:      100000,
		RateBurst:    100000,
	}
}

func mustAuthUnchecked(specs []auth.TokenSpec, enabled bool) *auth.Authenticator {
	a, err := auth.New(specs, enabled)
	if err != nil {
		panic(err)
	}
	return a
}

func newTestServer(t *testing.T) *httptest.Server {
	return newTestServerWithOptions(t, openOptions())
}

func newTestServerWithOptions(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewHandler(newTestService(t), opts))
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, url string, payload any) (*http.Response, map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func doJSON(t *testing.T, method, url string, headers map[string]string, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(data)
}

func TestHTTPEndToEndWriteReadQueryMigrateAudit(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	_ = resp.Body.Close()

	r, out := postJSON(t, srv.URL+"/v1/knowledge", map[string]any{
		"concept_id": "metrics/churn",
		"document": map[string]any{
			"type": "Metric", "title": "Churn", "description": "monthly churn definition",
			"body": "# Definition\n", "visibility": "public", "owner": "system",
		},
	})
	if r.StatusCode != http.StatusOK || out["content_hash"] == "" {
		t.Fatalf("put: %d %+v", r.StatusCode, out)
	}

	get, err := http.Get(srv.URL + "/v1/knowledge?id=metrics/churn")
	if err != nil || get.StatusCode != http.StatusOK {
		t.Fatalf("get: %v %v", get, err)
	}
	_ = get.Body.Close()

	r, out = postJSON(t, srv.URL+"/v1/query", map[string]any{
		"query": "define churn", "top_k": 5,
	})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("query: %d %+v", r.StatusCode, out)
	}

	r, out = postJSON(t, srv.URL+"/v1/migrate", map[string]any{
		"direction": "canonical_to_rag", "target": "memory", "job": "e2e",
	})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("migrate: %d %+v", r.StatusCode, out)
	}

	decisions, err := http.Get(srv.URL + "/v1/decisions?limit=10")
	if err != nil || decisions.StatusCode != http.StatusOK {
		t.Fatalf("decisions: %v %v", decisions, err)
	}
	_ = decisions.Body.Close()
}

func TestHTTPValidationAndMethods(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	r, _ := postJSON(t, srv.URL+"/v1/knowledge", map[string]any{"concept_id": "x"})
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing type should be 400, got %d", r.StatusCode)
	}
	resp, err := http.Post(srv.URL+"/healthz", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("method: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	r, _ = postJSON(t, srv.URL+"/v1/migrate", map[string]any{"direction": "nope", "target": "memory"})
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid migration should be 400, got %d", r.StatusCode)
	}
}

func TestHTTPComputationsEndToEnd(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	r, out := postJSON(t, srv.URL+"/v1/computations", map[string]any{
		"concept_id": "computations/revenue",
		"parameters": map[string]any{"year": 2026},
	})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("compute: %d %+v", r.StatusCode, out)
	}
	if out["display_allowed"] != true || out["display_value"] == nil || out["display_value"] == "" {
		t.Fatalf("verified value missing: %+v", out)
	}
	v, _ := out["verdict"].(map[string]any)
	if v == nil || v["passed"] != true {
		t.Fatalf("verdict = %+v", v)
	}

	audit, err := http.Get(srv.URL + "/v1/computations?concept_id=computations/revenue&limit=10")
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	_ = json.NewDecoder(audit.Body).Decode(&records)
	_ = audit.Body.Close()
	if audit.StatusCode != http.StatusOK || len(records) != 1 || records[0]["verdict"] != "passed" {
		t.Fatalf("audit: %d %+v", audit.StatusCode, records)
	}
	if _, leaked := records[0]["display_value"]; leaked {
		t.Fatal("audit record must not store result values")
	}

	r, out = postJSON(t, srv.URL+"/v1/computations", map[string]any{
		"concept_id": "computations/bigquery", "parameters": map[string]any{"year": 2026},
	})
	if r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported runtime: %d %+v", r.StatusCode, out)
	}
	env, _ := out["envelope"].(map[string]any)
	if env == nil || env["display_allowed"] != false || env["display_value"] != nil {
		t.Fatalf("failed run exposed a value: %+v", out)
	}

	for name, params := range map[string]map[string]any{
		"bad type": {"year": "oops"},
		"rewrite":  {"year": 2026, "sql": "SELECT 1"},
	} {
		r, out = postJSON(t, srv.URL+"/v1/computations", map[string]any{
			"concept_id": "computations/revenue", "parameters": params,
		})
		if r.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: %d %+v", name, r.StatusCode, out)
		}
		if env, _ := out["envelope"].(map[string]any); env != nil && env["display_value"] != nil {
			t.Fatalf("%s exposed a value: %+v", name, out)
		}
	}

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/computations", bytes.NewReader([]byte(`{}`)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("method: %d", resp.StatusCode)
	}
}

func TestHTTPHealthIncludesComputeRuntimes(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()
	if !strings.Contains(fmtSprint(out["compute_runtimes"]), "postgres") {
		t.Fatalf("health = %+v", out)
	}
}

func fmtSprint(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// --- security regression tests -------------------------------------------

func securedOptions(t *testing.T) (Options, map[string]string) {
	t.Helper()
	readerToken, adminToken := "reader-secret", "admin-secret"
	opts := Options{
		Auth: mustAuth(t, []auth.TokenSpec{
			{TokenSHA256: auth.Digest(readerToken), Subject: "alice", Scopes: []auth.Permission{
				auth.PermHealthRead, auth.PermKnowledgeRead,
			}},
			{TokenSHA256: auth.Digest(adminToken), Subject: "ops", Scopes: []auth.Permission{
				auth.PermHealthRead, auth.PermKnowledgeRead, auth.PermKnowledgeWrite,
				auth.PermMigrateRun, auth.PermAuditRead, auth.PermComputeRun,
			}},
		}, true),
		RateRPS:   100000,
		RateBurst: 100000,
	}
	return opts, map[string]string{
		"reader": "Bearer " + readerToken,
		"admin":  "Bearer " + adminToken,
	}
}

func TestHTTPRequiresAuthentication(t *testing.T) {
	opts, tokens := securedOptions(t)
	srv := newTestServerWithOptions(t, opts)
	defer srv.Close()

	for _, headers := range []map[string]string{
		{},
		{"Authorization": "Bearer wrong"},
		{"Authorization": "Basic abc"},
	} {
		resp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts", headers, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated request got %d", resp.StatusCode)
		}
		if resp.Header.Get("WWW-Authenticate") == "" {
			t.Fatal("401 must advertise the Bearer scheme")
		}
	}
	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts",
		map[string]string{"Authorization": tokens["admin"]}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid token got %d", resp.StatusCode)
	}
	// Health stays reachable without credentials for liveness probes.
	health, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("healthz must stay public, got %d", health.StatusCode)
	}
}

func TestHTTPPermissionMatrix(t *testing.T) {
	opts, tokens := securedOptions(t)
	srv := newTestServerWithOptions(t, opts)
	defer srv.Close()

	cases := []struct {
		name, method, path, body, token string
		want                            int
	}{
		{"read allowed", http.MethodGet, "/v1/concepts", "", "reader", http.StatusOK},
		{"write denied", http.MethodPost, "/v1/knowledge", `{"concept_id":"x","document":{"type":"Concept"}}`, "reader", http.StatusForbidden},
		{"migrate denied", http.MethodPost, "/v1/migrate", `{"direction":"canonical_to_rag","target":"memory"}`, "reader", http.StatusForbidden},
		{"audit denied", http.MethodGet, "/v1/decisions", "", "reader", http.StatusForbidden},
		{"compute denied", http.MethodPost, "/v1/computations", `{"concept_id":"computations/revenue"}`, "reader", http.StatusForbidden},
		{"write allowed", http.MethodPost, "/v1/knowledge", `{"concept_id":"x","document":{"type":"Concept"}}`, "admin", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := doJSON(t, tc.method, srv.URL+tc.path,
				map[string]string{"Authorization": tokens[tc.token]}, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("got %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestHTTPSubjectCannotBeSpoofed(t *testing.T) {
	opts, tokens := securedOptions(t)
	srv := newTestServerWithOptions(t, opts)
	defer srv.Close()

	// A self-asserted subject in the body is now an unknown JSON field.
	resp, body := doJSON(t, http.MethodPost, srv.URL+"/v1/knowledge",
		map[string]string{"Authorization": tokens["admin"]},
		`{"concept_id":"x","subject":"someone-else","document":{"type":"Concept"}}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "invalid JSON") {
		t.Fatalf("self-asserted subject must be rejected: %d %s", resp.StatusCode, body)
	}

	// A forged ?subject= on the query string must not widen ACL access.
	write, _ := doJSON(t, http.MethodPost, srv.URL+"/v1/knowledge",
		map[string]string{"Authorization": tokens["admin"]},
		`{"concept_id":"private/one","document":{"type":"Concept","visibility":"private","owner":"ops"}}`)
	if write.StatusCode != http.StatusOK {
		t.Fatalf("setup write: %d", write.StatusCode)
	}
	get, _ := doJSON(t, http.MethodGet,
		srv.URL+"/v1/knowledge?id=private/one&subject=ops",
		map[string]string{"Authorization": tokens["reader"]}, "")
	if get.StatusCode != http.StatusNotFound {
		t.Fatalf("reader with forged subject must not read a private document: %d", get.StatusCode)
	}
}

func TestHTTPErrorMessagesAreRedacted(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	resp, body := doJSON(t, http.MethodGet, srv.URL+"/v1/knowledge?id=does/not/exist", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing document: %d", resp.StatusCode)
	}
	for _, leak := range []string{"/tmp", "bundle", ".md", "open ", "no such file"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Fatalf("response leaked filesystem detail %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "document not found or access denied") {
		t.Fatalf("expected a generic error envelope, got %s", body)
	}
}

func TestHTTPStrictJSONAndContentType(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	cases := []struct {
		name, body, contentType string
	}{
		{"unknown field", `{"concept_id":"x","document":{"type":"Concept"},"extra":1}`, "application/json"},
		{"trailing data", `{"concept_id":"x"} {}`, "application/json"},
		{"malformed", `{"concept_id":`, "application/json"},
		{"wrong content type", `{"concept_id":"x"}`, "text/plain"},
		{"missing content type", `{"concept_id":"x"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/knowledge", strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestHTTPParameterBounds(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	for _, raw := range []string{"-1", "0", "1001", "abc", "999999999999999999999"} {
		resp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/decisions?limit="+raw, nil, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("limit=%s got %d, want 400", raw, resp.StatusCode)
		}
	}
	resp, _ := doJSON(t, http.MethodPost, srv.URL+"/v1/query", nil, `{"query":"x","top_k":101}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("top_k above the bound got %d", resp.StatusCode)
	}
}

func TestHTTPSecurityHeadersAndOrigin(t *testing.T) {
	opts, tokens := securedOptions(t)
	opts.AllowedOrigins = []string{"https://console.example.com"}
	srv := newTestServerWithOptions(t, opts)
	defer srv.Close()

	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts",
		map[string]string{"Authorization": tokens["reader"]}, "")
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing Content-Security-Policy")
	}

	cross, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts", map[string]string{
		"Authorization": tokens["reader"], "Origin": "https://evil.example.net",
	}, "")
	if cross.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin request got %d", cross.StatusCode)
	}
	allowed, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts", map[string]string{
		"Authorization": tokens["reader"], "Origin": "https://console.example.com",
	}, "")
	if allowed.StatusCode != http.StatusOK {
		t.Fatalf("allowlisted origin got %d", allowed.StatusCode)
	}
}

func TestHTTPRateLimit(t *testing.T) {
	opts := openOptions()
	opts.RateRPS, opts.RateBurst = 1, 1
	srv := newTestServerWithOptions(t, opts)
	defer srv.Close()
	sawLimited := false
	for i := 0; i < 5; i++ {
		resp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts", nil, "")
		if resp.StatusCode == http.StatusTooManyRequests {
			sawLimited = true
			break
		}
	}
	if !sawLimited {
		t.Fatal("rate limiter never rejected a request")
	}
}

func TestHTTPNilAuthenticatorFailsClosed(t *testing.T) {
	srv := newTestServerWithOptions(t, Options{RateRPS: 100000, RateBurst: 100000})
	defer srv.Close()
	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a missing authenticator must fail closed, got %d", resp.StatusCode)
	}
}

func TestHTTPPanicIsContained(t *testing.T) {
	// The recover middleware writes a generic error and keeps the server up.
	srv := httptest.NewServer(newSecurity(openOptions()).wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/concepts" {
			panic("dsn=postgres://secret")
		}
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/concepts")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError || strings.Contains(string(data), "secret") {
		t.Fatalf("panic leaked detail: %d %s", resp.StatusCode, data)
	}
	healthy, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = healthy.Body.Close()
	if healthy.StatusCode != http.StatusOK {
		t.Fatal("server did not survive a panic")
	}
}

func TestSecurityHelpers(t *testing.T) {
	for _, header := range []string{"Bearer", "Bearer a b", "Token abc"} {
		if _, ok := bearerToken(header); ok {
			t.Fatalf("malformed Bearer header %q must be rejected", header)
		}
	}
	if _, ok := bearerToken("bearer token"); !ok {
		t.Fatal("case-insensitive scheme must work")
	}
	if _, ok := bearerToken("Bearer " + strings.Repeat("x", 4097)); ok {
		t.Fatal("oversized token must be rejected")
	}
	if _, ok := subjectFrom(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("a request without a principal must not yield a subject")
	}
	perm, ok := routePermissions(httptest.NewRequest(http.MethodGet, "/v1/concepts", nil))
	if !ok || perm != auth.PermKnowledgeRead {
		t.Fatalf("route permission = %q, %v", perm, ok)
	}
	if _, ok := routePermissions(httptest.NewRequest(http.MethodPut, "/v1/concepts", nil)); ok {
		t.Fatal("unknown method must fall through to the router's 405 logic")
	}
}
