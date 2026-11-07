// Package config loads and strictly validates agentmem runtime configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvPrefix is the prefix of every runtime environment override.
const EnvPrefix = "AGENTMEM_"

// ErrUnsupported reports a configured capability that has no implementation.
var ErrUnsupported = errors.New("config: unsupported capability")

// Config is the root runtime configuration.
type Config struct {
	Server   ServerConfig    `yaml:"server"`
	Git      GitConfig       `yaml:"git"`
	Postgres PostgresConfig  `yaml:"postgres"`
	Router   RouterConfig    `yaml:"router"`
	Adapters []AdapterConfig `yaml:"adapters"`
	ACL      ACLConfig       `yaml:"acl"`
}

type ServerConfig struct {
	Addr string `yaml:"addr"`
	Mode string `yaml:"mode"`
}

type GitConfig struct {
	Root        string `yaml:"root"`
	AuthorName  string `yaml:"author_name"`
	AuthorEmail string `yaml:"author_email"`
}

type PostgresConfig struct {
	DSN             string `yaml:"dsn"`
	MaxOpenConns    int    `yaml:"max_open_conns"`
	MaxIdleConns    int    `yaml:"max_idle_conns"`
	ConnMaxLifetime string `yaml:"conn_max_lifetime"`
	LogLevel        string `yaml:"log_level"`
	VectorDSN       string `yaml:"vector_dsn"`
}

type RouterConfig struct {
	SystemOne      SystemOneConfig `yaml:"systemone"`
	ConflictPolicy string          `yaml:"conflict_policy"`
}

type SystemOneConfig struct {
	Endpoint      string  `yaml:"endpoint"`
	TimeoutMS     int     `yaml:"timeout_ms"`
	CacheCapacity int     `yaml:"cache_capacity"`
	MaxChoices    int     `yaml:"max_choices"`
	MinConfidence float64 `yaml:"min_confidence"`
	Fallback      string  `yaml:"fallback"`
	FailClosed    bool    `yaml:"fail_closed"`
}

type AdapterConfig struct {
	Name      string `yaml:"name"`
	Kind      string `yaml:"kind"`
	Enabled   bool   `yaml:"enabled"`
	Endpoint  string `yaml:"endpoint"`
	TimeoutMS int    `yaml:"timeout_ms"`
}

type ACLConfig struct {
	DefaultVisibility string `yaml:"default_visibility"`
	DefaultOwner      string `yaml:"default_owner"`
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
		Adapters: []AdapterConfig{
			{Name: "okf", Kind: "okf", Enabled: true},
			{Name: "memory", Kind: "memory", Enabled: true},
		},
		ACL: ACLConfig{DefaultVisibility: "private", DefaultOwner: "system"},
	}
}

// Load reads YAML, applies AGENTMEM_* overrides, and validates. A missing file
// yields Default so the binary runs out of the box in dev mode.
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv(EnvPrefix + "CONFIG")
	}
	if path == "" {
		path = "config.yaml"
	}
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	applyEnv(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	set := func(key string, dst *string) {
		if v := os.Getenv(EnvPrefix + key); v != "" {
			*dst = v
		}
	}
	set("ADDR", &cfg.Server.Addr)
	set("MODE", &cfg.Server.Mode)
	set("GIT_ROOT", &cfg.Git.Root)
	set("PG_DSN", &cfg.Postgres.DSN)
	set("VECTOR_DSN", &cfg.Postgres.VectorDSN)
	set("SYSTEMONE_ENDPOINT", &cfg.Router.SystemOne.Endpoint)
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
	seen := map[string]bool{}
	for _, a := range c.Adapters {
		if strings.TrimSpace(a.Name) == "" || seen[a.Name] {
			return fmt.Errorf("config: duplicate or empty adapter name %q", a.Name)
		}
		seen[a.Name] = true
		switch a.Kind {
		case "okf", "memory", "pgvector", "http", "cli":
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
