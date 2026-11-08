// Package auth establishes a trusted principal at the HTTP boundary.
//
// It is a leaf package with no internal dependencies. Tokens are configured as
// SHA-256 digests only: the raw bearer value never appears in configuration,
// logs or errors. Callers downstream keep receiving a plain subject string;
// the Handler layer is the single place where that string is established.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Permission is a capability granted to a principal.
type Permission string

const (
	PermHealthRead     Permission = "health:read"
	PermKnowledgeRead  Permission = "knowledge:read"
	PermKnowledgeWrite Permission = "knowledge:write"
	PermMigrateRun     Permission = "migrate:run"
	PermAuditRead      Permission = "audit:read"
	PermComputeRun     Permission = "compute:run"
)

var allPermissions = map[Permission]struct{}{
	PermHealthRead:     {},
	PermKnowledgeRead:  {},
	PermKnowledgeWrite: {},
	PermMigrateRun:     {},
	PermAuditRead:      {},
	PermComputeRun:     {},
}

// ParsePermission rejects unknown capabilities instead of silently granting
// nothing: a typo in .env must fail closed at startup.
func ParsePermission(s string) (Permission, error) {
	p := Permission(s)
	if _, ok := allPermissions[p]; !ok {
		return "", fmt.Errorf("auth: unknown permission %q", s)
	}
	return p, nil
}

// Principal is immutable and can only be constructed by this package.
type Principal struct {
	subject string
	scopes  map[Permission]struct{}
}

func (p Principal) Subject() string { return p.subject }

func (p Principal) Allows(perm Permission) bool {
	_, ok := p.scopes[perm]
	return ok
}

// TokenSpec is pure configuration data. TokenSHA256 is the lower-case hex
// SHA-256 digest of the bearer token, never the token itself.
type TokenSpec struct {
	TokenSHA256 string
	Subject     string
	Scopes      []Permission
}

// Digest returns the hex SHA-256 digest of a raw bearer token. It exists so
// operators and tests can produce .env values without duplicating the scheme.
func Digest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type Authenticator struct {
	enabled  bool
	byDigest map[string]Principal
}

// New validates every token spec up front. An enabled authenticator without
// at least one token is rejected so a misconfiguration cannot open the API.
func New(specs []TokenSpec, enabled bool) (*Authenticator, error) {
	a := &Authenticator{enabled: enabled, byDigest: make(map[string]Principal, len(specs))}
	for i, s := range specs {
		digest, err := normaliseDigest(s.TokenSHA256)
		if err != nil {
			return nil, fmt.Errorf("auth: token %d: %w", i, err)
		}
		subject := strings.TrimSpace(s.Subject)
		if subject == "" {
			return nil, fmt.Errorf("auth: token %d: empty subject", i)
		}
		if len(s.Scopes) == 0 {
			return nil, fmt.Errorf("auth: token %d: no scopes", i)
		}
		if _, dup := a.byDigest[digest]; dup {
			return nil, fmt.Errorf("auth: token %d: duplicate digest", i)
		}
		scopes := make(map[Permission]struct{}, len(s.Scopes))
		for _, p := range s.Scopes {
			if _, ok := allPermissions[p]; !ok {
				return nil, fmt.Errorf("auth: token %d: unknown permission %q", i, p)
			}
			scopes[p] = struct{}{}
		}
		a.byDigest[digest] = Principal{subject: subject, scopes: scopes}
	}
	if enabled && len(a.byDigest) == 0 {
		return nil, errors.New("auth: enabled authenticator requires at least one token")
	}
	return a, nil
}

func normaliseDigest(s string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(s))
	if len(d) != sha256.Size*2 {
		return "", fmt.Errorf("digest must be %d hex characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(d); err != nil {
		return "", fmt.Errorf("digest must be hexadecimal: %w", err)
	}
	return d, nil
}

func (a *Authenticator) Enabled() bool { return a != nil && a.enabled }

// Authenticate compares the digest of the raw token against every configured
// digest in constant time. Timing therefore does not reveal which digest
// matched. The raw token is neither stored nor returned.
func (a *Authenticator) Authenticate(raw string) (Principal, bool) {
	if a == nil || !a.enabled || raw == "" {
		return Principal{}, false
	}
	provided := Digest(raw)
	var matched Principal
	found := 0
	for digest, p := range a.byDigest {
		if subtle.ConstantTimeCompare([]byte(digest), []byte(provided)) == 1 {
			matched, found = p, 1
		}
	}
	if found == 0 {
		return Principal{}, false
	}
	return matched, true
}

// LocalPrincipal is used only when authentication is disabled (dev/offline).
// It carries every permission but is clearly marked as a local subject, so
// ACLs still have a concrete owner to compare against.
func LocalPrincipal(subject string) Principal {
	if strings.TrimSpace(subject) == "" {
		subject = "local"
	}
	scopes := make(map[Permission]struct{}, len(allPermissions))
	for p := range allPermissions {
		scopes[p] = struct{}{}
	}
	return Principal{subject: subject, scopes: scopes}
}

type contextKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(contextKey{}).(Principal)
	return p, ok
}
