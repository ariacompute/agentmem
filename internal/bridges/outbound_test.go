package bridges

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostJSONRequiresAllowlistedHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	if _, err := PostJSON(context.Background(), srv.URL, "/run", nil, time.Second, map[string]string{"a": "b"}); err == nil {
		t.Fatal("an unlisted host must be refused")
	}
	if _, err := PostJSON(context.Background(), srv.URL, "/run", []string{"127.0.0.1"}, time.Second, map[string]string{"a": "b"}); err != nil {
		t.Fatalf("allowlisted loopback must pass: %v", err)
	}
}

func TestPostJSONRetries5xxAndNot4xx(t *testing.T) {
	var five, four int32
	fiveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&five, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer fiveSrv.Close()
	if _, err := PostJSON(context.Background(), fiveSrv.URL, "/run", []string{"127.0.0.1"}, time.Second, nil); err == nil {
		t.Fatal("5xx must fail")
	}
	if got := atomic.LoadInt32(&five); got != 2 {
		t.Fatalf("5xx attempts = %d, want 2", got)
	}

	fourSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&four, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer fourSrv.Close()
	if _, err := PostJSON(context.Background(), fourSrv.URL, "/run", []string{"127.0.0.1"}, time.Second, nil); err == nil {
		t.Fatal("4xx must fail")
	}
	if got := atomic.LoadInt32(&four); got != 1 {
		t.Fatalf("4xx attempts = %d, want 1", got)
	}
}

func TestPostJSONRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", MaxOutboundResponseBytes+16)))
	}))
	defer srv.Close()
	_, err := PostJSON(context.Background(), srv.URL, "/run", []string{"127.0.0.1"}, time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized response must fail closed, got %v", err)
	}
}

func TestPostJSONRejectsCrossHostRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.invalid/run", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := PostJSON(context.Background(), srv.URL, "/run", []string{"127.0.0.1"}, time.Second, nil)
	if err == nil {
		t.Fatal("a redirect outside the allowlist must be refused")
	}
	if strings.Contains(err.Error(), "example.invalid") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaks the endpoint: %v", err)
	}
}
