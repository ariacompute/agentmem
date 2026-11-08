// Package netsafe is the shared outbound-network boundary for the HTTP bridge
// and the System One client. It is a leaf package with no internal deps, so
// both callers enforce exactly the same allowlist and redirect rules.
package netsafe

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// DefaultMaxHops bounds redirect chains. The standard library follows ten by
// default, which is more than this service ever needs.
const DefaultMaxHops = 3

// ValidateEndpoint accepts only http/https URLs whose host is explicitly
// allowlisted. Credentials embedded in the URL, unusual ports on non-http
// schemes and non-global IP literals are rejected. Hostname resolution is
// intentionally not performed: that keeps the check deterministic and offline,
// while RedirectPolicy re-validates every hop at request time.
func ValidateEndpoint(raw string, allowHosts []string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("netsafe: empty endpoint")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("netsafe: invalid endpoint: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("netsafe: endpoint scheme %q is not allowed", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("netsafe: endpoint has no host")
	}
	if u.User != nil {
		return errors.New("netsafe: endpoint must not embed credentials")
	}
	if u.Fragment != "" {
		return errors.New("netsafe: endpoint must not contain a fragment")
	}
	host := u.Hostname()
	if !hostAllowed(host, allowHosts) {
		return fmt.Errorf("netsafe: endpoint host %q is not allowlisted", host)
	}
	if ip := net.ParseIP(host); ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate()) {
		// Loopback, link-local, multicast and RFC1918 literals are only usable
		// when the operator named that exact address (a local sidecar), never
		// through a wildcard entry.
		if !exactHost(host, allowHosts) {
			return fmt.Errorf("netsafe: endpoint address %q is not a public unicast address", host)
		}
	}
	return nil
}

func exactHost(host string, allowHosts []string) bool {
	for _, h := range allowHosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

func hostAllowed(host string, allowHosts []string) bool {
	for _, h := range allowHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		lower := strings.ToLower(host)
		if h == lower {
			return true
		}
		if strings.HasPrefix(h, "*.") && strings.HasSuffix(lower, h[1:]) {
			return true
		}
	}
	return false
}

// RedirectPolicy returns a CheckRedirect callback that re-validates each hop,
// limits the chain length and forbids TLS downgrade.
func RedirectPolicy(allowHosts []string, maxHops int) func(*http.Request, []*http.Request) error {
	if maxHops < 1 {
		maxHops = DefaultMaxHops
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHops {
			return fmt.Errorf("netsafe: redirect chain exceeds %d hops", maxHops)
		}
		if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("netsafe: redirect would downgrade TLS")
		}
		return ValidateEndpoint(req.URL.String(), allowHosts)
	}
}
