package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Git.Root = filepath.Join(t.TempDir(), "bundle")
	writeHandlerBundle(t, cfg.Git.Root)
	svc, err := service.Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return httptest.NewServer(NewHandler(svc))
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
		"subject":    "alice",
		"document": map[string]any{
			"type": "Metric", "title": "Churn", "description": "monthly churn definition",
			"body": "# Definition\n", "visibility": "public", "owner": "alice",
		},
	})
	if r.StatusCode != http.StatusOK || out["content_hash"] == "" {
		t.Fatalf("put: %d %+v", r.StatusCode, out)
	}

	get, err := http.Get(srv.URL + "/v1/knowledge?id=metrics/churn&subject=alice")
	if err != nil || get.StatusCode != http.StatusOK {
		t.Fatalf("get: %v %v", get, err)
	}
	_ = get.Body.Close()

	r, out = postJSON(t, srv.URL+"/v1/query", map[string]any{
		"query": "define churn", "subject": "alice", "top_k": 5,
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
		"concept_id": "computations/revenue", "subject": "analyst:1",
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
