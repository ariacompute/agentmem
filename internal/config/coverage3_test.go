package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRejectsEachStructuralRule(t *testing.T) {
	cases := map[string]func(*Config){
		"empty addr":         func(c *Config) { c.Server.Addr = "  " },
		"bad mode":           func(c *Config) { c.Server.Mode = "staging" },
		"empty git root":     func(c *Config) { c.Git.Root = "" },
		"bad pool sizes":     func(c *Config) { c.Postgres.MaxOpenConns = 0 },
		"bad system one":     func(c *Config) { c.Router.SystemOne.MaxChoices = 0 },
		"bad visibility":     func(c *Config) { c.ACL.DefaultVisibility = "world" },
		"bad rate limit":     func(c *Config) { c.RateLimit.RPS = 0 },
		"bad adapter kind":   func(c *Config) { c.Adapters = []AdapterConfig{{Name: "x", Kind: "nope", Enabled: true}} },
		"empty adapter name": func(c *Config) { c.Adapters = []AdapterConfig{{Name: " ", Kind: "okf"}} },
		"duplicate adapter": func(c *Config) {
			c.Adapters = []AdapterConfig{{Name: "a", Kind: "okf"}, {Name: "a", Kind: "okf"}}
		},
	}
	for name, mutate := range cases {
		cfg := Default()
		mutate(cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestValidateRejectsInvalidComputeBounds(t *testing.T) {
	cfg := Default()
	cfg.Compute.TimeoutMS = 120001
	if err := cfg.Validate(); err == nil {
		t.Fatal("an out-of-range compute timeout must be rejected")
	}
	cfg = Default()
	cfg.Compute.MaxRows = 10001
	if err := cfg.Validate(); err == nil {
		t.Fatal("an out-of-range compute row budget must be rejected")
	}
}

func TestValidateRejectsCLISidecarWithEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Compute.Sidecars = []ComputeSidecarConfig{{
		Runtime: "dbt", Bridge: "cli", Command: "dbt-runner", Endpoint: "http://engine.invalid",
	}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("a cli sidecar must not carry an endpoint")
	}
}

func TestValidateRequiresAllowlistForAdapterEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Adapters = []AdapterConfig{{Name: "engine", Kind: "http", Enabled: true, Endpoint: "http://engine.invalid"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("an enabled http adapter with an endpoint must require an allowlist")
	}
	// A disabled adapter and a CLI endpoint never require the allowlist.
	cfg.Adapters = []AdapterConfig{
		{Name: "disabled", Kind: "http", Endpoint: "http://engine.invalid"},
		{Name: "cli", Kind: "cli", Enabled: true, Endpoint: "engine-runner"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("no outbound endpoint is enabled: %v", err)
	}
}

func TestLoadDefaultsPathWhenUnset(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(EnvFileVar, "")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("missing .env must fall back to defaults: %v", err)
	}
	if cfg.Server.Mode != "dev" {
		t.Fatalf("mode = %q", cfg.Server.Mode)
	}
}

func TestLoadRejectsUnreadableEnvFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Fatal("a directory passed as the .env path must fail")
	}
}

func TestLoadRejectsInvalidEnvKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("BAD KEY=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("an invalid key must fail closed")
	}
}

func TestLoadRejectsTrailingTextAfterQuotedValue(t *testing.T) {
	for _, line := range []string{
		"AGENTMEM_ACL_DEFAULT_OWNER='alice' trailing",
		"AGENTMEM_ACL_DEFAULT_OWNER=\"alice\" trailing",
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("%q must fail closed", line)
		}
	}
}

func TestLoadRejectsInvalidDoubleQuotedEscape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("AGENTMEM_ACL_DEFAULT_OWNER=\"\\q\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("an invalid escape sequence must fail closed")
	}
}

func TestLoadRejectsTrailingJSONData(t *testing.T) {
	for _, key := range []string{"AGENTMEM_AUTH_TOKENS", "AGENTMEM_COMPUTE_SIDECARS"} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		if err := os.WriteFile(path, []byte(key+"=[] []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("%s with trailing data must fail closed", key)
		}
	}
}

func TestLoadAcceptsTestDSNKey(t *testing.T) {
	t.Setenv("AGENTMEM_TEST_PG_DSN", "postgres://example/db")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatalf("the reserved test DSN key must be accepted: %v", err)
	}
	if cfg.Server.Mode != "dev" {
		t.Fatalf("mode = %q", cfg.Server.Mode)
	}
}
