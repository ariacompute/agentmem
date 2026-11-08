package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	// EnvFileDefault is the .env path used when neither --env nor
	// AGENTMEM_ENV_FILE is supplied. Docker Compose reads the same file.
	EnvFileDefault = ".env"
	// EnvFileVar points at an alternate .env file.
	EnvFileVar = "AGENTMEM_ENV_FILE"
	// AdaptersVar carries the adapter list as a JSON array.
	AdaptersVar = "AGENTMEM_ADAPTERS"
)

type envSetter func(*Config, string) error

func strSetter(pick func(*Config) *string) envSetter {
	return func(c *Config, v string) error { *pick(c) = v; return nil }
}

func intSetter(pick func(*Config) *int) envSetter {
	return func(c *Config, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("expected an integer: %w", err)
		}
		*pick(c) = n
		return nil
	}
}

func boolSetter(pick func(*Config) *bool) envSetter {
	return func(c *Config, v string) error {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("expected a boolean: %w", err)
		}
		*pick(c) = b
		return nil
	}
}

func floatSetter(pick func(*Config) *float64) envSetter {
	return func(c *Config, v string) error {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return fmt.Errorf("expected a number: %w", err)
		}
		*pick(c) = f
		return nil
	}
}

func adaptersSetter(c *Config, v string) error {
	dec := json.NewDecoder(strings.NewReader(v))
	dec.DisallowUnknownFields()
	var list []AdapterConfig
	if err := dec.Decode(&list); err != nil {
		return fmt.Errorf("expected a JSON array of adapters: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing data after the adapters array")
	}
	c.Adapters = list
	return nil
}

// tokensSetter uses the same strict JSON discipline as adaptersSetter: unknown
// fields and trailing data are hard errors, and an empty value means "no
// tokens" so the documented .env.example placeholder stays loadable.
func tokensSetter(c *Config, v string) error {
	if strings.TrimSpace(v) == "" {
		c.Auth.Tokens = nil
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(v))
	dec.DisallowUnknownFields()
	var list []AuthTokenSpec
	if err := dec.Decode(&list); err != nil {
		return fmt.Errorf("expected a JSON array of auth tokens: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing data after the tokens array")
	}
	c.Auth.Tokens = list
	return nil
}

func listSetter(pick func(*Config) *[]string) envSetter {
	return func(c *Config, v string) error {
		if strings.TrimSpace(v) == "" {
			*pick(c) = nil
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				return errors.New("empty list entry")
			}
			out = append(out, p)
		}
		*pick(c) = out
		return nil
	}
}

// envSpecs is the closed whitelist of AGENTMEM_* keys. An unknown key in a
// .env file is a hard error — the dotenv equivalent of KnownFields(true).
var envSpecs = []struct {
	key string
	set envSetter
}{
	{EnvFileVar, func(*Config, string) error { return nil }}, // selects the file itself
	{"AGENTMEM_ADDR", strSetter(func(c *Config) *string { return &c.Server.Addr })},
	{"AGENTMEM_MODE", strSetter(func(c *Config) *string { return &c.Server.Mode })},
	{"AGENTMEM_GIT_ROOT", strSetter(func(c *Config) *string { return &c.Git.Root })},
	{"AGENTMEM_GIT_AUTHOR_NAME", strSetter(func(c *Config) *string { return &c.Git.AuthorName })},
	{"AGENTMEM_GIT_AUTHOR_EMAIL", strSetter(func(c *Config) *string { return &c.Git.AuthorEmail })},
	{"AGENTMEM_PG_DSN", strSetter(func(c *Config) *string { return &c.Postgres.DSN })},
	{"AGENTMEM_VECTOR_DSN", strSetter(func(c *Config) *string { return &c.Postgres.VectorDSN })},
	{"AGENTMEM_PG_MAX_OPEN_CONNS", intSetter(func(c *Config) *int { return &c.Postgres.MaxOpenConns })},
	{"AGENTMEM_PG_MAX_IDLE_CONNS", intSetter(func(c *Config) *int { return &c.Postgres.MaxIdleConns })},
	{"AGENTMEM_PG_CONN_MAX_LIFETIME", strSetter(func(c *Config) *string { return &c.Postgres.ConnMaxLifetime })},
	{"AGENTMEM_PG_LOG_LEVEL", strSetter(func(c *Config) *string { return &c.Postgres.LogLevel })},
	{"AGENTMEM_CONFLICT_POLICY", strSetter(func(c *Config) *string { return &c.Router.ConflictPolicy })},
	{"AGENTMEM_SYSTEMONE_ENDPOINT", strSetter(func(c *Config) *string { return &c.Router.SystemOne.Endpoint })},
	{"AGENTMEM_SYSTEMONE_TIMEOUT_MS", intSetter(func(c *Config) *int { return &c.Router.SystemOne.TimeoutMS })},
	{"AGENTMEM_SYSTEMONE_CACHE_CAPACITY", intSetter(func(c *Config) *int { return &c.Router.SystemOne.CacheCapacity })},
	{"AGENTMEM_SYSTEMONE_MAX_CHOICES", intSetter(func(c *Config) *int { return &c.Router.SystemOne.MaxChoices })},
	{"AGENTMEM_SYSTEMONE_MIN_CONFIDENCE", floatSetter(func(c *Config) *float64 { return &c.Router.SystemOne.MinConfidence })},
	{"AGENTMEM_SYSTEMONE_FALLBACK", strSetter(func(c *Config) *string { return &c.Router.SystemOne.Fallback })},
	{"AGENTMEM_COMPUTE_TIMEOUT_MS", intSetter(func(c *Config) *int { return &c.Compute.TimeoutMS })},
	{"AGENTMEM_COMPUTE_MAX_ROWS", intSetter(func(c *Config) *int { return &c.Compute.MaxRows })},
	{AdaptersVar, adaptersSetter},
	{"AGENTMEM_ACL_DEFAULT_VISIBILITY", strSetter(func(c *Config) *string { return &c.ACL.DefaultVisibility })},
	{"AGENTMEM_ACL_DEFAULT_OWNER", strSetter(func(c *Config) *string { return &c.ACL.DefaultOwner })},
	{"AGENTMEM_AUTH_ENABLED", boolSetter(func(c *Config) *bool { return &c.Auth.Enabled })},
	{"AGENTMEM_AUTH_TOKENS", tokensSetter},
	{"AGENTMEM_ALLOWED_ORIGINS", listSetter(func(c *Config) *[]string { return &c.Auth.AllowedOrigins })},
	{"AGENTMEM_EGRESS_ALLOWED_HOSTS", listSetter(func(c *Config) *[]string { return &c.Egress.AllowedHosts })},
	{"AGENTMEM_RATE_RPS", intSetter(func(c *Config) *int { return &c.RateLimit.RPS })},
	{"AGENTMEM_RATE_BURST", intSetter(func(c *Config) *int { return &c.RateLimit.Burst })},
	// Read directly by the optional Postgres integration test; declared so the
	// closed whitelist also covers process-environment keys.
	{"AGENTMEM_TEST_PG_DSN", func(*Config, string) error { return nil }},
}

var envSetterByKey = func() map[string]envSetter {
	m := make(map[string]envSetter, len(envSpecs))
	for _, s := range envSpecs {
		m[s.key] = s.set
	}
	return m
}()

// KnownEnvKeys returns the sorted, closed set of supported AGENTMEM_* keys.
func KnownEnvKeys() []string {
	out := make([]string, 0, len(envSetterByKey))
	for k := range envSetterByKey {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Load reads the .env file, layers process environment variables on top and
// validates. A missing file falls back to Default() so the binary runs out of
// the box in dev mode; malformed content and unknown keys always fail closed.
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv(EnvFileVar)
	}
	if path == "" {
		path = EnvFileDefault
	}
	// The closed whitelist covers the process environment too: a typo in an
	// injected AGENTMEM_* variable must not be silently ignored.
	if err := rejectUnknownProcessEnvKeys(); err != nil {
		return nil, err
	}
	fileValues, err := readEnvFile(path)
	if err != nil {
		return nil, err
	}
	if err := rejectUnknownEnvKeys(path, fileValues); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(fileValues)+len(envSetterByKey))
	for k, v := range fileValues {
		values[k] = v
	}
	for _, k := range KnownEnvKeys() {
		if v := os.Getenv(k); v != "" {
			values[k] = v
		}
	}
	cfg := Default()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		set, ok := envSetterByKey[k]
		if !ok {
			continue // non-AGENTMEM_* keys belong to Docker Compose
		}
		if err := set(cfg, values[k]); err != nil {
			return nil, fmt.Errorf("config: %s: %w", k, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func rejectUnknownProcessEnvKeys() error {
	var unknown []string
	for _, entry := range os.Environ() {
		key := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			key = entry[:i]
		}
		if !strings.HasPrefix(key, EnvPrefix) {
			continue
		}
		if _, ok := envSetterByKey[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("config: unknown process environment keys %s", strings.Join(unknown, ", "))
	}
	return nil
}

func rejectUnknownEnvKeys(path string, values map[string]string) error {
	var unknown []string
	for k := range values {
		if !strings.HasPrefix(k, EnvPrefix) {
			continue // POSTGRES_* and other compose variables are not ours
		}
		if _, ok := envSetterByKey[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("config: %s: unknown keys %s", path, strings.Join(unknown, ", "))
	}
	return nil
}

func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return parseEnvFile(data, path)
}

// parseEnvFile implements the dotenv subset agentmem needs: KEY=VALUE,
// optional `export`, # comments, single/double quotes and ` #` inline
// comments. Duplicate keys are rejected rather than silently overwritten.
func parseEnvFile(data []byte, path string) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, line := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "export ") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("config: %s:%d: expected KEY=VALUE", path, lineNo)
		}
		key := strings.TrimSpace(trimmed[:eq])
		if !validEnvKey(key) {
			return nil, fmt.Errorf("config: %s:%d: invalid key %q", path, lineNo, key)
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("config: %s:%d: duplicate key %q", path, lineNo, key)
		}
		value, err := parseEnvValue(trimmed[eq+1:])
		if err != nil {
			return nil, fmt.Errorf("config: %s:%d: %w", path, lineNo, err)
		}
		out[key] = value
	}
	return out, nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func parseEnvValue(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", errors.New("unterminated single-quoted value")
		}
		tail := strings.TrimSpace(s[2+end:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", errors.New("unexpected text after quoted value")
		}
		return s[1 : 1+end], nil
	case '"':
		i := 1
		for i < len(s) {
			if s[i] == '\\' {
				i += 2
				continue
			}
			if s[i] == '"' {
				break
			}
			i++
		}
		if i >= len(s) {
			return "", errors.New("unterminated double-quoted value")
		}
		value, err := strconv.Unquote(s[:i+1])
		if err != nil {
			return "", fmt.Errorf("invalid quoted value: %w", err)
		}
		tail := strings.TrimSpace(s[i+1:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", errors.New("unexpected text after quoted value")
		}
		return value, nil
	default:
		if idx := strings.Index(s, " #"); idx >= 0 {
			s = strings.TrimSpace(s[:idx])
		}
		return s, nil
	}
}
