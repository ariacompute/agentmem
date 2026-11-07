package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"agentmem/internal/config"
	"agentmem/internal/service"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Git.Root = filepath.Join(t.TempDir(), "bundle")
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
