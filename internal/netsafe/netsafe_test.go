package netsafe

import (
	"net/http"
	"net/url"
	"testing"
)

var publicHosts = []string{"api.example.com", "*.trusted.example"}

func TestValidateEndpointAcceptsAllowlistedHosts(t *testing.T) {
	for _, raw := range []string{
		"https://api.example.com/v1/systemone",
		"http://api.example.com:8010/healthz",
		"https://engine.trusted.example/put",
	} {
		if err := ValidateEndpoint(raw, publicHosts); err != nil {
			t.Fatalf("ValidateEndpoint(%q) = %v", raw, err)
		}
	}
}

func TestValidateEndpointRejectsUnsafeTargets(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"file scheme":   "file:///etc/passwd",
		"gopher scheme": "gopher://api.example.com/_",
		"no host":       "https:///v1",
		"unknown host":  "https://evil.example.net/v1",
		"metadata ip":   "http://169.254.169.254/latest/meta-data",
		"private ip":    "http://10.0.0.5:8010/healthz",
		"loopback":      "http://127.0.0.1:8010/healthz",
		"userinfo":      "https://user:pass@api.example.com/v1",
		"fragment":      "https://api.example.com/v1#x",
		"malformed":     "https://%zz",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateEndpoint(raw, publicHosts); err == nil {
				t.Fatalf("ValidateEndpoint(%q) must fail closed", raw)
			}
		})
	}
	if err := ValidateEndpoint("https://api.example.com/v1", nil); err == nil {
		t.Fatal("an empty allowlist must not permit any outbound endpoint")
	}
}

func TestValidateEndpointAllowsExplicitLocalSidecar(t *testing.T) {
	if err := ValidateEndpoint("http://127.0.0.1:8010/healthz", []string{"127.0.0.1"}); err != nil {
		t.Fatalf("an explicitly allowlisted loopback sidecar must work: %v", err)
	}
	if err := ValidateEndpoint("http://127.0.0.1:8010/healthz", []string{"*.local"}); err == nil {
		t.Fatal("a wildcard must not implicitly allow a loopback address")
	}
}

func TestRedirectPolicyRevalidatesEachHop(t *testing.T) {
	policy := RedirectPolicy(publicHosts, DefaultMaxHops)
	start := mustRequest(t, "https://api.example.com/v1")
	if err := policy(mustRequest(t, "https://engine.trusted.example/put"), []*http.Request{start}); err != nil {
		t.Fatalf("allowlisted redirect rejected: %v", err)
	}
	if err := policy(mustRequest(t, "https://evil.example.net/put"), []*http.Request{start}); err == nil {
		t.Fatal("cross-host redirect to a non-allowlisted host must be rejected")
	}
	if err := policy(mustRequest(t, "http://169.254.169.254/latest"), []*http.Request{start}); err == nil {
		t.Fatal("redirect to a metadata address must be rejected")
	}
	if err := policy(mustRequest(t, "http://api.example.com/put"), []*http.Request{start}); err == nil {
		t.Fatal("TLS downgrade on redirect must be rejected")
	}
}

func TestRedirectPolicyBoundsHops(t *testing.T) {
	policy := RedirectPolicy(publicHosts, 2)
	via := []*http.Request{
		mustRequest(t, "https://api.example.com/a"),
		mustRequest(t, "https://api.example.com/b"),
	}
	if err := policy(mustRequest(t, "https://api.example.com/c"), via); err == nil {
		t.Fatal("redirect chain over the hop limit must be rejected")
	}
}

func mustRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Request{URL: u, Method: http.MethodGet, Header: http.Header{}}
}
