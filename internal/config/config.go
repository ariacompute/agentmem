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
	"os"
	"regexp"
	"strings"
)

// EnvPrefix is the prefix of every agentmem configuration variable.
const EnvPrefix = "AGENTMEM_"

// ErrUnsupported reports a configured capability that has no implementation.
var ErrUnsupported = errors.New("config: unsupported capability")

// Config is the root runtime configuration.
type Config struct {
	Server    ServerConfig    `json:"server" yaml:"server"`
	Console   ConsoleConfig   `json:"console" yaml:"console"`
	Git       GitConfig       `json:"git" yaml:"git"`
	Postgres  PostgresConfig  `json:"postgres" yaml:"postgres"`
	Router    RouterConfig    `json:"router" yaml:"router"`
	Compute   ComputeConfig   `json:"compute" yaml:"compute"`
	Graph     GraphConfig     `json:"graph" yaml:"graph"`
	Adapters  []AdapterConfig `json:"adapters" yaml:"adapters"`
	ACL       ACLConfig       `json:"acl" yaml:"acl"`
	Auth      AuthConfig      `json:"auth" yaml:"auth"`
	Egress    EgressConfig    `json:"egress" yaml:"egress"`
	RateLimit RateLimitConfig `json:"rate_limit" yaml:"rate_limit"`
}

// ConsoleConfig switches the embedded web console. Enabled defaults to true so
// an existing deployment keeps serving the page; a pure API deployment sets
// AGENTMEM_CONSOLE_ENABLED=false and gets JSON 404 for "/" and "/assets/*"
// without rebuilding the binary. This is a presentation switch only: the API
// routes, the permission matrix and the console's public allowlist are
// unchanged either way.
type ConsoleConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// ComputeSidecarConfig names one outbound executor for a non-postgres runtime.
// Bridge is "http" (Endpoint is a URL) or "cli" (Command is a bare executable
// name). The sidecar never receives a shell and never learns the contract hash
// as an authority: it only runs the sanctioned body it is handed.
type ComputeSidecarConfig struct {
	Runtime   string `json:"runtime" yaml:"runtime"`
	Bridge    string `json:"bridge" yaml:"bridge"`
	Endpoint  string `json:"endpoint,omitempty" yaml:"endpoint"`
	Command   string `json:"command,omitempty" yaml:"command"`
	TimeoutMS int    `json:"timeout_ms,omitempty" yaml:"timeout_ms"`
}

// ComputeConfig bounds the Attested Computation executor. Zero values are
// normalised to DefaultCompute() by the service layer.
type ComputeConfig struct {
	TimeoutMS int                    `json:"timeout_ms" yaml:"timeout_ms"`
	MaxRows   int                    `json:"max_rows" yaml:"max_rows"`
	Sidecars  []ComputeSidecarConfig `json:"sidecars" yaml:"sidecars"`
}

func DefaultCompute() ComputeConfig { return ComputeConfig{TimeoutMS: 5000, MaxRows: 100} }

// GraphConfig controls the knowledge graph. Enabled defaults to true so the
// projection follows every write; Backend selects the read implementation
// ("builtin" uses the PostgreSQL/offline index, "remote" queries an external
// graph engine). RemoteURL must appear in AGENTMEM_EGRESS_ALLOWED_HOSTS and
// RemoteTokenEnv names an environment variable, never the token itself.
type GraphConfig struct {
	Enabled         bool    `json:"enabled" yaml:"enabled"`
	Backend         string  `json:"backend" yaml:"backend"`
	RemoteURL       string  `json:"remote_url" yaml:"remote_url"`
	RemoteTokenEnv  string  `json:"remote_token_env" yaml:"remote_token_env"`
	RemoteTimeoutMS int     `json:"remote_timeout_ms" yaml:"remote_timeout_ms"`
	MaxDepth        int     `json:"max_depth" yaml:"max_depth"`
	MaxNodes        int     `json:"max_nodes" yaml:"max_nodes"`
	MaxEdges        int     `json:"max_edges" yaml:"max_edges"`
	CoverageMin     float64 `json:"coverage_min" yaml:"coverage_min"`
	RRFK            int     `json:"rrf_k" yaml:"rrf_k"`
}

// DefaultGraph is the offline-testable default: no external dependency.
func DefaultGraph() GraphConfig {
	return GraphConfig{
		Enabled: true, Backend: "builtin", RemoteTimeoutMS: 5000,
		MaxDepth: 3, MaxNodes: 100000, MaxEdges: 500000,
		CoverageMin: 0.6, RRFK: 60,
	}
}

// knownComputeRuntimes mirrors compute's non-postgres runtime names. config is
// an L0 package and must not import compute, so the values are duplicated here
// and pinned by config tests. postgres executes in-process and is rejected as a
// sidecar: it must never be proxied to an external host.
var knownComputeRuntimes = map[string]bool{
	"bigquery": true, "dbt": true, "looker": true, "python": true,
}

// sidecarCommandRe pins bridges' CLI command rule (a bare executable name)
// without importing bridges, which would invert the layering.
var sidecarCommandRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

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
	"graph:write": true,
}

// Default returns an offline, fully testable configuration.
func Default() *Config {
	return &Config{
		Server:  ServerConfig{Addr: "127.0.0.1:8080", Mode: "dev"},
		Console: ConsoleConfig{Enabled: true},
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
		Graph:   DefaultGraph(),
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
	if err := c.validateComputeSidecars(); err != nil {
		return err
	}
	if err := c.validateGraph(); err != nil {
		return err
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

func (c *Config) validateComputeSidecars() error {
	seen := make(map[string]bool, len(c.Compute.Sidecars))
	for i, s := range c.Compute.Sidecars {
		runtimeName := strings.ToLower(strings.TrimSpace(s.Runtime))
		if !knownComputeRuntimes[runtimeName] {
			return fmt.Errorf("%w: compute.sidecars[%d].runtime %q (postgres executes in-process; only bigquery, dbt, looker and python accept a sidecar)", ErrUnsupported, i, s.Runtime)
		}
		if seen[runtimeName] {
			return fmt.Errorf("config: compute.sidecars[%d] duplicates runtime %q", i, runtimeName)
		}
		seen[runtimeName] = true
		if s.TimeoutMS < 0 || s.TimeoutMS > 120000 {
			return fmt.Errorf("config: compute.sidecars[%d].timeout_ms must be 0..120000", i)
		}
		switch strings.ToLower(strings.TrimSpace(s.Bridge)) {
		case "http":
			if strings.TrimSpace(s.Endpoint) == "" {
				return fmt.Errorf("config: compute.sidecars[%d] bridge http requires an endpoint", i)
			}
			if strings.TrimSpace(s.Command) != "" {
				return fmt.Errorf("config: compute.sidecars[%d] bridge http must not set a command", i)
			}
		case "cli":
			command := strings.TrimSpace(s.Command)
			if command == "" {
				return fmt.Errorf("config: compute.sidecars[%d] bridge cli requires a bare command name", i)
			}
			if len(command) > 256 || !sidecarCommandRe.MatchString(command) {
				return fmt.Errorf("config: compute.sidecars[%d].command %q must be a bare executable name", i, s.Command)
			}
			if strings.TrimSpace(s.Endpoint) != "" {
				return fmt.Errorf("config: compute.sidecars[%d] bridge cli must not set an endpoint", i)
			}
		default:
			return fmt.Errorf("%w: compute.sidecars[%d].bridge %q (use http or cli)", ErrUnsupported, i, s.Bridge)
		}
	}
	return nil
}

func (c *Config) validateGraph() error {
	g := c.Graph
	if !g.Enabled {
		// A disabled feature must not force a remote endpoint or token, but the
		// bounds are still checked so a typo surfaces before it is switched on.
		return validateGraphBounds(g)
	}
	switch g.Backend {
	case "builtin":
	case "remote":
		if strings.TrimSpace(g.RemoteURL) == "" {
			return errors.New("config: graph.backend remote requires AGENTMEM_GRAPH_REMOTE_URL")
		}
		if !tokenEnvRe.MatchString(strings.TrimSpace(g.RemoteTokenEnv)) {
			return errors.New("config: graph.remote_token_env must name an environment variable")
		}
		if c.Server.Mode == "production" && strings.TrimSpace(os.Getenv(g.RemoteTokenEnv)) == "" {
			return fmt.Errorf("config: graph.remote_token_env %q is not set in the process environment", g.RemoteTokenEnv)
		}
	default:
		return fmt.Errorf("%w: graph.backend %q (use builtin or remote)", ErrUnsupported, g.Backend)
	}
	return validateGraphBounds(g)
}

func validateGraphBounds(g GraphConfig) error {
	if g.MaxDepth < 1 || g.MaxDepth > 8 {
		return errors.New("config: graph.max_depth must be 1..8")
	}
	if g.MaxNodes < 1 || g.MaxNodes > 1000000 {
		return errors.New("config: graph.max_nodes must be 1..1000000")
	}
	if g.MaxEdges < 1 || g.MaxEdges > 5000000 {
		return errors.New("config: graph.max_edges must be 1..5000000")
	}
	if g.CoverageMin < 0 || g.CoverageMin > 1 {
		return errors.New("config: graph.coverage_min must be 0..1")
	}
	if g.RRFK < 1 || g.RRFK > 1000 {
		return errors.New("config: graph.rrf_k must be 1..1000")
	}
	if g.RemoteTimeoutMS < 0 || g.RemoteTimeoutMS > 120000 {
		return errors.New("config: graph.remote_timeout_ms must be 0..120000")
	}
	return nil
}

// tokenEnvRe pins the shape of an environment variable name.
var tokenEnvRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	for _, s := range c.Compute.Sidecars {
		// A CLI sidecar command is an executable name, not a URL.
		if strings.EqualFold(strings.TrimSpace(s.Bridge), "http") && strings.TrimSpace(s.Endpoint) != "" {
			needsAllowlist = true
		}
	}
	if c.Graph.Enabled && strings.EqualFold(strings.TrimSpace(c.Graph.Backend), "remote") &&
		strings.TrimSpace(c.Graph.RemoteURL) != "" {
		needsAllowlist = true
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
