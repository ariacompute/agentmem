// Package config loads and strictly validates agentmem runtime configuration.
//
// The single configuration source is a .env file (see .env.example) plus
// process environment overrides. YAML is deliberately not a config format:
// compose consumes the same .env through env_file and ${...} interpolation.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// EnvPrefix is the prefix of every agentmem configuration variable.
const EnvPrefix = "AGENTMEM_"

// ErrUnsupported reports a configured capability that has no implementation.
var ErrUnsupported = errors.New("config: unsupported capability")

// Config is the root runtime configuration.
type Config struct {
	Server    ServerConfig    `json:"server" yaml:"server"`
	Git       GitConfig       `json:"git" yaml:"git"`
	Postgres  PostgresConfig  `json:"postgres" yaml:"postgres"`
	Router    RouterConfig    `json:"router" yaml:"router"`
	Compute   ComputeConfig   `json:"compute" yaml:"compute"`
	Adapters  []AdapterConfig `json:"adapters" yaml:"adapters"`
	ACL       ACLConfig       `json:"acl" yaml:"acl"`
	Auth      AuthConfig      `json:"auth" yaml:"auth"`
	Egress    EgressConfig    `json:"egress" yaml:"egress"`
	RateLimit RateLimitConfig `json:"rate_limit" yaml:"rate_limit"`
}

// ComputeConfig bounds the Attested Computation executor. Zero values are
// normalised to DefaultCompute() by the service layer.
type ComputeConfig struct {
	TimeoutMS int `json:"timeout_ms" yaml:"timeout_ms"`
	MaxRows   int `json:"max_rows" yaml:"max_rows"`
}

func DefaultCompute() ComputeConfig { return ComputeConfig{TimeoutMS: 5000, MaxRows: 100} }

type ServerConfig struct {
	Addr string `json:"addr" yaml:"addr"`
	Mode string `json:"mode" yaml:"mode"`
}

type GitConfig struct {
	Root        string `json:"root" yaml:"root"`
	AuthorName  string `json:"author_name" yaml:"author_name"`
	AuthorEmail string `json:"author_email" yaml:"author_email"`
}

type PostgresConfig struct {
	DSN             string `json:"dsn" yaml:"dsn"`
	MaxOpenConns    int    `json:"max_open_conns" yaml:"max_open_conns"`
	MaxIdleConns    int    `json:"max_idle_conns" yaml:"max_idle_conns"`
	ConnMaxLifetime string `json:"conn_max_lifetime" yaml:"conn_max_lifetime"`
	LogLevel        string `json:"log_level" yaml:"log_level"`
	VectorDSN       string `json:"vector_dsn" yaml:"vector_dsn"`
}

type RouterConfig struct {
	SystemOne      SystemOneConfig `json:"systemone" yaml:"systemone"`
	ConflictPolicy string          `json:"conflict_policy" yaml:"conflict_policy"`
}

type SystemOneConfig struct {
	Endpoint      string  `json:"endpoint" yaml:"endpoint"`
	TimeoutMS     int     `json:"timeout_ms" yaml:"timeout_ms"`
	CacheCapacity int     `json:"cache_capacity" yaml:"cache_capacity"`
	MaxChoices    int     `json:"max_choices" yaml:"max_choices"`
	MinConfidence float64 `json:"min_confidence" yaml:"min_confidence"`
	Fallback      string  `json:"fallback" yaml:"fallback"`
}

type AdapterConfig struct {
	Name      string `json:"name" yaml:"name"`
	Kind      string `json:"kind" yaml:"kind"`
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	Endpoint  string `json:"endpoint,omitempty" yaml:"endpoint"`
	TimeoutMS int    `json:"timeout_ms,omitempty" yaml:"timeout_ms"`
	// EgressHosts is injected at assembly time from the global allowlist. It
	// is deliberately not part of the .env JSON surface.
	EgressHosts []string `json:"-" yaml:"-"`
}

type ACLConfig struct {
	DefaultVisibility string `json:"default_visibility" yaml:"default_visibility"`
	DefaultOwner      string `json:"default_owner" yaml:"default_owner"`
}

// AuthTokenSpec is pure configuration data. TokenSHA256 is the hex SHA-256
// digest of the bearer token; the raw value is never stored in configuration,
// logs or errors. Scopes are validated against knownPermissions below and
// converted to auth.Permission values by the assembly layer.
type AuthTokenSpec struct {
	TokenSHA256 string   `json:"token_sha256"`
	Subject     string   `json:"subject"`
	Scopes      []string `json:"scopes"`
}

type AuthConfig struct {
	Enabled        bool            `json:"enabled" yaml:"enabled"`
	Tokens         []AuthTokenSpec `json:"tokens" yaml:"tokens"`
	AllowedOrigins []string        `json:"allowed_origins" yaml:"allowed_origins"`
}

// EgressConfig is the closed allowlist for outbound sidecar/System One hosts.
type EgressConfig struct {
	AllowedHosts []string `json:"allowed_hosts" yaml:"allowed_hosts"`
}

type RateLimitConfig struct {
	RPS   int `json:"rps" yaml:"rps"`
	Burst int `json:"burst" yaml:"burst"`
}

// knownPermissions mirrors internal/auth's Permission constants. config is an
// L0 package and must not import auth, so the values are duplicated here and
// pinned by config tests and auth.New's own validation.
var knownPermissions = map[string]bool{
	"health:read": true, "knowledge:read": true, "knowledge:write": true,
	"migrate:run": true, "audit:read": true, "compute:run": true,
}

// Default returns an offline, fully testable configuration.
func Default() *Config {
	return &Config{
		Server: ServerConfig{Addr: "127.0.0.1:8080", Mode: "dev"},
		Git: GitConfig{
			Root: "./data/bundle", AuthorName: "agentmem", AuthorEmail: "agentmem@localhost",
		},
		Postgres: PostgresConfig{
			MaxOpenConns: 25, MaxIdleConns: 10, ConnMaxLifetime: "30m", LogLevel: "warn",
		},
		Router: RouterConfig{
			SystemOne: SystemOneConfig{
				TimeoutMS: 5000, CacheCapacity: 256, MaxChoices: 255,
				MinConfidence: 0.5, Fallback: "okf",
			},
			ConflictPolicy: "okf_wins",
		},
		Compute: DefaultCompute(),
		Adapters: []AdapterConfig{
			{Name: "okf", Kind: "okf", Enabled: true},
			{Name: "memory", Kind: "memory", Enabled: true},
		},
		ACL:       ACLConfig{DefaultVisibility: "private", DefaultOwner: "system"},
		Auth:      AuthConfig{Enabled: false},
		Egress:    EgressConfig{},
		RateLimit: RateLimitConfig{RPS: 20, Burst: 40},
	}
}

// Validate fails closed: unknown modes, duplicate adapters, missing production
// DSN and unimplemented capabilities are all hard errors.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Server.Addr) == "" {
		return errors.New("config: server.addr is empty")
	}
	switch c.Server.Mode {
	case "dev", "production":
	default:
		return fmt.Errorf("config: server.mode %q must be dev or production", c.Server.Mode)
	}
	if strings.TrimSpace(c.Git.Root) == "" {
		return errors.New("config: git.root is empty")
	}
	if c.Server.Mode == "production" && strings.TrimSpace(c.Postgres.DSN) == "" {
		return errors.New("config: postgres.dsn is required in production (AGENTMEM_PG_DSN)")
	}
	if c.Server.Mode == "production" {
		if !c.Auth.Enabled || len(c.Auth.Tokens) == 0 {
			return errors.New("config: production requires AGENTMEM_AUTH_ENABLED=true and at least one token")
		}
		if insecureDSN(c.Postgres.DSN) || insecureDSN(c.Postgres.VectorDSN) {
			return errors.New("config: production DSN must not disable TLS (sslmode=disable)")
		}
	}
	if c.Postgres.MaxOpenConns < 1 || c.Postgres.MaxIdleConns < 0 {
		return errors.New("config: invalid postgres pool sizes")
	}
	if c.Router.SystemOne.TimeoutMS < 1 || c.Router.SystemOne.CacheCapacity < 1 || c.Router.SystemOne.MaxChoices < 1 {
		return errors.New("config: invalid router.systemone limits")
	}
	if c.Router.ConflictPolicy != "okf_wins" {
		return fmt.Errorf("%w: conflict_policy %q", ErrUnsupported, c.Router.ConflictPolicy)
	}
	if c.Compute.TimeoutMS < 0 || c.Compute.TimeoutMS > 120000 ||
		c.Compute.MaxRows < 0 || c.Compute.MaxRows > 10000 {
		return errors.New("config: compute.timeout_ms must be 0..120000 and compute.max_rows 0..10000")
	}
	seen := map[string]bool{}
	for _, a := range c.Adapters {
		if strings.TrimSpace(a.Name) == "" || seen[a.Name] {
			return fmt.Errorf("config: duplicate or empty adapter name %q", a.Name)
		}
		seen[a.Name] = true
		switch a.Kind {
		case "okf", "memory", "pgvector", "http", "cli",
			"llamaindex", "pageindex", "graphrag", "lightrag":
		default:
			return fmt.Errorf("%w: adapter %q kind %q", ErrUnsupported, a.Name, a.Kind)
		}
	}
	switch c.ACL.DefaultVisibility {
	case "private", "team", "public":
	default:
		return fmt.Errorf("config: acl.default_visibility %q is invalid", c.ACL.DefaultVisibility)
	}
	if err := c.validateAuth(); err != nil {
		return err
	}
	if err := c.validateEgress(); err != nil {
		return err
	}
	if c.RateLimit.RPS < 1 || c.RateLimit.RPS > 100000 ||
		c.RateLimit.Burst < 1 || c.RateLimit.Burst > 1000000 {
		return errors.New("config: rate_limit.rps must be 1..100000 and burst 1..1000000")
	}
	return nil
}

func insecureDSN(dsn string) bool {
	return strings.Contains(strings.ToLower(dsn), "sslmode=disable")
}

func (c *Config) validateAuth() error {
	if c.Auth.Enabled && len(c.Auth.Tokens) == 0 {
		return errors.New("config: auth.enabled requires at least one token")
	}
	seen := make(map[string]bool, len(c.Auth.Tokens))
	for i, t := range c.Auth.Tokens {
		digest := strings.ToLower(strings.TrimSpace(t.TokenSHA256))
		if len(digest) != 64 {
			return fmt.Errorf("config: auth.tokens[%d].token_sha256 must be 64 hex characters", i)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return fmt.Errorf("config: auth.tokens[%d].token_sha256 must be hexadecimal", i)
		}
		if seen[digest] {
			return fmt.Errorf("config: auth.tokens[%d] duplicates a token digest", i)
		}
		seen[digest] = true
		if strings.TrimSpace(t.Subject) == "" {
			return fmt.Errorf("config: auth.tokens[%d].subject is empty", i)
		}
		if len(t.Scopes) == 0 {
			return fmt.Errorf("config: auth.tokens[%d].scopes is empty", i)
		}
		for _, s := range t.Scopes {
			if !knownPermissions[s] {
				return fmt.Errorf("config: auth.tokens[%d] has unknown scope %q", i, s)
			}
		}
	}
	for _, origin := range c.Auth.AllowedOrigins {
		u, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
			u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return fmt.Errorf("config: auth.allowed_origins entry %q must be an origin without a path", origin)
		}
	}
	return nil
}

func (c *Config) validateEgress() error {
	for _, h := range c.Egress.AllowedHosts {
		if strings.TrimSpace(h) == "" || strings.ContainsAny(h, "/: ") {
			return fmt.Errorf("config: egress.allowed_hosts entry %q must be a bare host", h)
		}
	}
	needsAllowlist := strings.TrimSpace(c.Router.SystemOne.Endpoint) != ""
	for _, a := range c.Adapters {
		// A CLI bridge endpoint is an executable name, not a URL.
		if a.Enabled && a.Kind != "cli" && strings.TrimSpace(a.Endpoint) != "" {
			needsAllowlist = true
		}
	}
	if needsAllowlist && len(c.Egress.AllowedHosts) == 0 {
		return errors.New("config: outbound endpoints require AGENTMEM_EGRESS_ALLOWED_HOSTS")
	}
	return nil
}

// VectorDSN returns the effective DSN for the pgvector adapter.
func (c *Config) VectorDSN() string {
	if c.Postgres.VectorDSN != "" {
		return c.Postgres.VectorDSN
	}
	return c.Postgres.DSN
}
