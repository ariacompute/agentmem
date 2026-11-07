// Package config loads and strictly validates agentmem runtime configuration.
//
// The single configuration source is a .env file (see .env.example) plus
// process environment overrides. YAML is deliberately not a config format:
// compose consumes the same .env through env_file and ${...} interpolation.
package config

import (
	"errors"
	"fmt"
	"strings"
)

// EnvPrefix is the prefix of every agentmem configuration variable.
const EnvPrefix = "AGENTMEM_"

// ErrUnsupported reports a configured capability that has no implementation.
var ErrUnsupported = errors.New("config: unsupported capability")

// Config is the root runtime configuration.
type Config struct {
	Server   ServerConfig    `json:"server" yaml:"server"`
	Git      GitConfig       `json:"git" yaml:"git"`
	Postgres PostgresConfig  `json:"postgres" yaml:"postgres"`
	Router   RouterConfig    `json:"router" yaml:"router"`
	Compute  ComputeConfig   `json:"compute" yaml:"compute"`
	Adapters []AdapterConfig `json:"adapters" yaml:"adapters"`
	ACL      ACLConfig       `json:"acl" yaml:"acl"`
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
	FailClosed    bool    `json:"fail_closed" yaml:"fail_closed"`
}

type AdapterConfig struct {
	Name      string `json:"name" yaml:"name"`
	Kind      string `json:"kind" yaml:"kind"`
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	Endpoint  string `json:"endpoint,omitempty" yaml:"endpoint"`
	TimeoutMS int    `json:"timeout_ms,omitempty" yaml:"timeout_ms"`
}

type ACLConfig struct {
	DefaultVisibility string `json:"default_visibility" yaml:"default_visibility"`
	DefaultOwner      string `json:"default_owner" yaml:"default_owner"`
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
				MinConfidence: 0.5, Fallback: "okf", FailClosed: true,
			},
			ConflictPolicy: "okf_wins",
		},
		Compute: DefaultCompute(),
		Adapters: []AdapterConfig{
			{Name: "okf", Kind: "okf", Enabled: true},
			{Name: "memory", Kind: "memory", Enabled: true},
		},
		ACL: ACLConfig{DefaultVisibility: "private", DefaultOwner: "system"},
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
	return nil
}

// VectorDSN returns the effective DSN for the pgvector adapter.
func (c *Config) VectorDSN() string {
	if c.Postgres.VectorDSN != "" {
		return c.Postgres.VectorDSN
	}
	return c.Postgres.DSN
}
