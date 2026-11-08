package netsafe

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateEndpointCoverageBranches(t *testing.T) {
	invalid := []struct {
		raw   string
		hosts []string
	}{
		{"", nil},
		{"http://", nil},
		{"ftp://x.example.com", []string{"x.example.com"}},
		{"http://user:pass@x.example.com", []string{"x.example.com"}},
		{"http://x.example.com#frag", []string{"x.example.com"}},
		{"http://evil.example.com", []string{"good.example.com"}},
		{"http://10.0.0.1", []string{"*.0.0.1"}},
	}
	for _, c := range invalid {
		if err := ValidateEndpoint(c.raw, c.hosts); err == nil {
			t.Fatalf("expected %q to be rejected", c.raw)
		}
	}
	// An exact private IP literal is allowed for a local sidecar.
	if err := ValidateEndpoint("http://127.0.0.1", []string{"127.0.0.1"}); err != nil {
		t.Fatalf("exact private ip: %v", err)
	}
	// A wildcard subdomain entry matches.
	if err := ValidateEndpoint("https://api.example.com", []string{"*.example.com"}); err != nil {
		t.Fatalf("wildcard: %v", err)
	}
	// Empty allowlist entries are skipped.
	if err := ValidateEndpoint("https://a.b", []string{"", "a.b"}); err != nil {
		t.Fatalf("empty entry: %v", err)
	}
}

func TestRedirectPolicyCoverageBranches(t *testing.T) {
	p := RedirectPolicy([]string{"good.example.com"}, 0)
	req := httptest.NewRequest("GET", "http://good.example.com/x", nil)
	// Too many hops.
	via := make([]*http.Request, 3)
	if err := p(req, via); err == nil {
		t.Fatal("too many hops must fail")
	}
	// TLS downgrade.
	https := httptest.NewRequest("GET", "https://good.example.com/x", nil)
	if err := p(httptest.NewRequest("GET", "http://good.example.com/x", nil), []*http.Request{https}); err == nil {
		t.Fatal("TLS downgrade must fail")
	}
	// Disallowed host.
	if err := p(httptest.NewRequest("GET", "http://evil.example.com/x", nil), nil); err == nil {
		t.Fatal("disallowed host must fail")
	}
	// Allowed hop with no prior.
	if err := p(req, nil); err != nil {
		t.Fatalf("allowed hop: %v", err)
	}
}
