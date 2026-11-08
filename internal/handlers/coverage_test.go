package handlers

import (
	"net/http"
	"testing"
)

func TestNewSecurityDefaultRateLimit(t *testing.T) {
	// Exercises the rps<1 / burst<1 default branches.
	s := newSecurity(Options{})
	if s.limiter.rps != 20 || s.limiter.burst != 40 {
		t.Fatalf("rate defaults wrong: %v/%v", s.limiter.rps, s.limiter.burst)
	}
}

func TestAllRoutesRejectUnsupportedMethods(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/healthz"},
		{http.MethodDelete, "/v1/knowledge"},
		{http.MethodGet, "/v1/query"},
		{http.MethodGet, "/v1/migrate"},
		{http.MethodPost, "/v1/decisions"},
		{http.MethodPost, "/v1/concepts"},
		{http.MethodDelete, "/v1/computations"},
	}
	for _, tc := range cases {
		resp, err := http.DefaultClient.Do(mustReq(t, tc.method, srv.URL+tc.path, nil, ""))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s got %d, want 405", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestOriginMatchingHostIsAllowed(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	// httptest sets r.Host to host:port; an Origin with the same host (and a
	// scheme) must be treated as same-origin and allowed.
	host := srv.Listener.Addr().String()
	resp, err := http.DefaultClient.Do(mustReq(t, http.MethodGet, srv.URL+"/v1/concepts",
		map[string]string{"Origin": "http://" + host}, ""))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("same-origin request got %d", resp.StatusCode)
	}
}

func mustReq(t *testing.T, method, url string, headers map[string]string, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}
