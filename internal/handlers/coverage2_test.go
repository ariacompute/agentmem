package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmem/internal/compute"
)

func TestServerErrorDirect(t *testing.T) {
	w := httptest.NewRecorder()
	serverError(w, "test", errors.New("boom"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestComputeStatusMapping(t *testing.T) {
	if computeStatus(compute.ErrUnsupported) != http.StatusUnprocessableEntity {
		t.Fatal("unsupported")
	}
	if computeStatus(compute.ErrExecution) != http.StatusBadGateway {
		t.Fatal("execution")
	}
	if computeStatus(errors.New("other")) != http.StatusBadRequest {
		t.Fatal("default")
	}
}

func TestClientKeyBranches(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "1.2.3.4"
	if clientKey(r) != "1.2.3.4" {
		t.Fatalf("no port = %q", clientKey(r))
	}
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "1.2.3.4:5678"
	if clientKey(r2) != "1.2.3.4" {
		t.Fatalf("with port = %q", clientKey(r2))
	}
}

func TestRateLimiterCleanupAndCap(t *testing.T) {
	l := newRateLimiter(1, 1)
	l.lastCleanup = time.Now().Add(-2 * time.Minute)
	l.buckets["stale"] = &bucket{tokens: 0, last: time.Now().Add(-10 * time.Minute)}
	l.Allow("fresh")
	if _, ok := l.buckets["stale"]; ok {
		t.Fatal("stale bucket must be cleaned up")
	}

	capped := newRateLimiter(1, 1)
	for i := 0; i < 10000; i++ {
		capped.buckets[fmt.Sprintf("k%d", i)] = &bucket{tokens: 1, last: time.Now()}
	}
	if capped.Allow("new-key") {
		t.Fatal("a new key past the map cap must be refused")
	}
}

func TestHandlerValidationBranches(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// GET /v1/knowledge without id.
	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/knowledge", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing id = %d", resp.StatusCode)
	}
	// POST /v1/concepts is not allowed.
	resp, _ = doJSON(t, http.MethodPost, srv.URL+"/v1/concepts", nil, `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method = %d", resp.StatusCode)
	}
	// Put with an owner that is not the authenticated subject.
	resp, _ = doJSON(t, http.MethodPost, srv.URL+"/v1/knowledge", nil,
		`{"concept_id":"x","document":{"type":"Concept","owner":"someone-else"}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("owner mismatch = %d", resp.StatusCode)
	}
	// Computation: malformed JSON and an empty concept id both reject.
	resp, _ = doJSON(t, http.MethodPost, srv.URL+"/v1/computations", nil, `not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad compute json = %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, http.MethodPost, srv.URL+"/v1/computations", nil, `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty concept = %d", resp.StatusCode)
	}
	// Computation audit with an out-of-range limit.
	resp, _ = doJSON(t, http.MethodGet, srv.URL+"/v1/computations?limit=abc", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad audit limit = %d", resp.StatusCode)
	}
}

func TestOriginAllowedBranches(t *testing.T) {
	srv := newTestServerWithOptions(t, openOptions())
	defer srv.Close()
	// A "null" origin is treated as cross-origin and refused.
	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/v1/concepts", map[string]string{"Origin": "null"}, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("null origin = %d", resp.StatusCode)
	}
	// An origin without a host is refused.
	resp, _ = doJSON(t, http.MethodGet, srv.URL+"/v1/concepts", map[string]string{"Origin": "http://"}, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("hostless origin = %d", resp.StatusCode)
	}
	// No origin header at all is allowed.
	resp, _ = doJSON(t, http.MethodGet, srv.URL+"/v1/concepts", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no origin = %d", resp.StatusCode)
	}
}
