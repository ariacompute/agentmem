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
)

// Options carries the security posture assembled by the CLI. A nil
// Authenticator fails closed: every protected route returns 401.
type Options struct {
	Auth           *auth.Authenticator
	LocalSubject   string
	AllowedOrigins []string
	RateRPS        int
	RateBurst      int
}

// routePermissions is the single, auditable permission matrix. It mirrors the
// 9 method+path combinations registered in NewHandler. Anything not listed
// here is handled by the router's own 405 logic.
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
		setSecurityHeaders(w.Header())
		if !s.limiter.Allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		if !s.originAllowed(r) {
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
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

func setSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
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
