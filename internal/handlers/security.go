package handlers

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"agentmem/internal/auth"
	"agentmem/internal/mcp"
)

// apiPolicy is the policy for every JSON response: nothing may be loaded or
// fetched from an API answer. The console page needs a wider policy, which is
// why it is the single documented exception (see Console).
const apiPolicy = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'"

// Console is the optional web console compiled into the binary. The port lives
// here, where it is consumed, so internal/webui stays a leaf package with no
// agentmem imports.
type Console interface {
	http.Handler
	// Public reports whether a path may be read without a bearer token.
	Public(path string) bool
	// CSP is the page-level policy the console needs.
	CSP() string
}

// Options carries the security posture assembled by the CLI. A nil
// Authenticator fails closed: every protected route returns 401.
type Options struct {
	Auth           *auth.Authenticator
	LocalSubject   string
	AllowedOrigins []string
	RateRPS        int
	RateBurst      int
	// Console is nil when the console is not embedded, which keeps the
	// API-only posture reproducible in tests.
	Console Console
	// MCP configures the MCP server: the write switch, result clamps and the
	// session bounds. The zero value is the safe default (read-only tools).
	MCP mcp.Config
}

// routePermissions is the single, auditable permission matrix. It mirrors the
// 23 method+path combinations registered in NewHandler. Anything not listed
// here is handled by the router's own 405 logic, so an unsupported method on a
// known path never widens access. The console paths are not listed: they are
// public reads, resolved by Console.Public instead.
//
// /mcp is listed once per transport method with the read scope: every MCP
// request needs knowledge:read to reach the dispatcher, and each tool then
// checks its own scope (write tools additionally require AGENTMEM_MCP_WRITE).
func routePermissions(r *http.Request) (auth.Permission, bool) {
	switch r.Method + " " + r.URL.Path {
	case "POST /v1/knowledge":
		return auth.PermKnowledgeWrite, true
	case "GET /v1/knowledge", "POST /v1/query", "GET /v1/concepts":
		return auth.PermKnowledgeRead, true
	case "POST /v1/migrate":
		return auth.PermMigrateRun, true
	case "GET /v1/decisions", "GET /v1/computations":
		return auth.PermAuditRead, true
	case "POST /v1/computations":
		return auth.PermComputeRun, true
	case "GET /v1/graph", "GET /v1/graph/node", "GET /v1/graph/neighbors",
		"GET /v1/graph/path", "GET /v1/graph/search", "GET /v1/graph/schema",
		"POST /v1/graph/answerability", "POST /v1/graph/retrieve",
		"POST /mcp", "GET /mcp", "DELETE /mcp":
		return auth.PermKnowledgeRead, true
	case "PUT /v1/graph/node", "POST /v1/graph/edges":
		return auth.PermGraphWrite, true
	case "POST /v1/graph/index":
		// Reindexing rebuilds a projection, not knowledge: it needs the migrate
		// scope rather than the graph write scope.
		return auth.PermMigrateRun, true
	}
	return "", false
}

type security struct {
	opts    Options
	limiter *rateLimiter
	logs    logThrottle
}

func newSecurity(opts Options) *security {
	rps, burst := opts.RateRPS, opts.RateBurst
	if rps < 1 {
		rps = 20
	}
	if burst < 1 {
		burst = 40
	}
	return &security{opts: opts, limiter: newRateLimiter(rps, burst)}
}

func (s *security) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// Never echo panic values: they can contain paths, DSNs or bodies.
				slog.Error("handler panic", "type", fmt.Sprintf("%T", rec))
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		s.setSecurityHeaders(w.Header(), r)
		if !s.limiter.Allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		if !s.originAllowed(r) {
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		if s.publicPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		principal, ok := s.authenticate(r)
		if !ok {
			s.logs.Warn("authentication failed", r.Method, r.URL.Path)
			w.Header().Set("WWW-Authenticate", `Bearer realm="agentmem"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if perm, needed := routePermissions(r); needed && !principal.Allows(perm) {
			s.logs.Warn("permission denied", r.Method, r.URL.Path)
			writeError(w, http.StatusForbidden, "permission denied")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	})
}

func (s *security) authenticate(r *http.Request) (auth.Principal, bool) {
	a := s.opts.Auth
	if a == nil {
		return auth.Principal{}, false
	}
	if !a.Enabled() {
		return auth.LocalPrincipal(s.opts.LocalSubject), true
	}
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		return auth.Principal{}, false
	}
	return a.Authenticate(token)
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := parts[1]
	if token == "" || len(token) > 4096 {
		return "", false
	}
	return token, true
}

func (s *security) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return origin == ""
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true // same-origin request
	}
	for _, allowed := range s.opts.AllowedOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

// setSecurityHeaders applies the API posture by default and the console posture
// for the page and its assets. The console handler sets both headers again on
// its own responses (it must stay testable in isolation), and the two policies
// would otherwise intersect into a blank page.
func (s *security) setSecurityHeaders(h http.Header, r *http.Request) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	if s.consolePath(r) {
		h.Set("Content-Security-Policy", s.opts.Console.CSP())
		// Hashed assets are immutable and index.html revalidates; the static
		// handler owns the exact value.
		return
	}
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", apiPolicy)
}

// publicPath is the complete allowlist of anonymous reads: the health check and
// the embedded console page with its assets. Every other route authenticates
// first, so an unknown path still answers 401 before 404.
func (s *security) publicPath(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		return true
	}
	return s.consolePath(r)
}

// consolePath reports whether the request addresses the embedded console.
func (s *security) consolePath(r *http.Request) bool {
	return s.opts.Console != nil && s.opts.Console.Public(r.URL.Path)
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimiter is a bounded, in-memory token bucket per client. It is scoped to
// a single process; multi-instance deployments should front the service with
// a gateway (documented in docs/security.md).
type rateLimiter struct {
	mu          sync.Mutex
	rps         float64
	burst       float64
	buckets     map[string]*bucket
	lastCleanup time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rps, burst int) *rateLimiter {
	return &rateLimiter{
		rps: float64(rps), burst: float64(burst),
		buckets: make(map[string]*bucket), lastCleanup: time.Now(),
	}
}

func (l *rateLimiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastCleanup) > time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.last) > 5*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastCleanup = now
	}
	// Cap the map so a flood of spoofed source addresses cannot grow it
	// without bound; new keys are refused until old buckets expire.
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= 10000 {
			return false
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// logThrottle records at most one warning per second so a credential-stuffing
// flood cannot turn the logger into the denial of service.
type logThrottle struct {
	mu         sync.Mutex
	lastSecond int64
	suppressed int
}

func (l *logThrottle) Warn(msg, method, path string) {
	now := time.Now().Unix()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now != l.lastSecond {
		if l.suppressed > 0 {
			slog.Warn(msg, "suppressed", l.suppressed)
		}
		l.lastSecond, l.suppressed = now, 0
		slog.Warn(msg, "method", method, "path", path)
		return
	}
	l.suppressed++
}

// subjectFrom returns the trusted principal established by the middleware.
// Handlers must never read a subject from the request again.
func subjectFrom(r *http.Request) (string, bool) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.Subject() == "" {
		return "", false
	}
	return p.Subject(), true
}

// serverError keeps internal failures out of the response body while retaining
// a stable, searchable class and the Go error type for operators.
func serverError(w http.ResponseWriter, class string, err error) {
	slog.Warn("request failed", "class", class, "error_type", fmt.Sprintf("%T", err))
	writeError(w, http.StatusInternalServerError, "internal error")
}

// redactedBadRequest replaces the previous err.Error() echoes on client-error
// paths. Validation details that are safe to expose are passed explicitly.
func redactedBadRequest(w http.ResponseWriter, class string, err error, safeMsg string) {
	slog.Warn("request rejected", "class", class, "error_type", fmt.Sprintf("%T", err))
	writeError(w, http.StatusBadRequest, safeMsg)
}
