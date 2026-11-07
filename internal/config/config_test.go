package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := writeEnv(t, "AGENTMEM_ADDR=127.0.0.1:9000\nAGENTMEM_NOPE=1\n")
	if _, err := Load(p); err == nil {
		t.Fatal("the closed AGENTMEM_* whitelist must reject unknown keys")
	}
}

func TestLoadIgnoresComposeKeys(t *testing.T) {
	p := writeEnv(t, "POSTGRES_PASSWORD=placeholder\nPOSTGRES_USER=agentmem\nAGENTMEM_MODE=dev\n")
	if _, err := Load(p); err != nil {
		t.Fatalf("non-AGENTMEM_* compose keys must be ignored: %v", err)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Router.SystemOne.TimeoutMS != 5000 || cfg.Server.Mode != "dev" {
		t.Fatalf("defaults drifted: %+v", cfg)
	}
}

func TestLoadEnvFileValues(t *testing.T) {
	p := writeEnv(t, strings.Join([]string{
		"# comment",
		"AGENTMEM_ADDR=0.0.0.0:9000",
		"export AGENTMEM_MODE=production",
		`AGENTMEM_PG_DSN="postgres://u:p@db:5432/agentmem?sslmode=disable"`,
		"AGENTMEM_GIT_ROOT='/srv/bundle' # inline comment",
		"AGENTMEM_COMPUTE_TIMEOUT_MS=1200",
		"AGENTMEM_COMPUTE_MAX_ROWS=7",
		"AGENTMEM_SYSTEMONE_FAIL_CLOSED=false",
		"AGENTMEM_SYSTEMONE_MIN_CONFIDENCE=0.75",
		"AGENTMEM_ACL_DEFAULT_VISIBILITY=team",
		"AGENTMEM_ADAPTERS='[{\"name\":\"okf\",\"kind\":\"okf\",\"enabled\":true},{\"name\":\"llamaindex\",\"kind\":\"llamaindex\",\"enabled\":true}]'",
	}, "\n")+"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != "0.0.0.0:9000" || cfg.Server.Mode != "production" {
		t.Fatalf("server = %+v", cfg.Server)
	}
	if cfg.Postgres.DSN != "postgres://u:p@db:5432/agentmem?sslmode=disable" {
		t.Fatalf("quoted DSN = %q", cfg.Postgres.DSN)
	}
	if cfg.Git.Root != "/srv/bundle" {
		t.Fatalf("single-quoted root = %q", cfg.Git.Root)
	}
	if cfg.Compute.TimeoutMS != 1200 || cfg.Compute.MaxRows != 7 {
		t.Fatalf("compute = %+v", cfg.Compute)
	}
	if cfg.Router.SystemOne.FailClosed || cfg.Router.SystemOne.MinConfidence != 0.75 {
		t.Fatalf("systemone = %+v", cfg.Router.SystemOne)
	}
	if cfg.ACL.DefaultVisibility != "team" || len(cfg.Adapters) != 2 ||
		cfg.Adapters[1].Kind != "llamaindex" {
		t.Fatalf("acl/adapters = %+v / %+v", cfg.ACL, cfg.Adapters)
	}
}

func TestProcessEnvOverridesEnvFile(t *testing.T) {
	p := writeEnv(t, "AGENTMEM_MODE=dev\nAGENTMEM_ADDR=127.0.0.1:9000\n")
	t.Setenv("AGENTMEM_MODE", "production")
	t.Setenv("AGENTMEM_PG_DSN", "postgres://example")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Mode != "production" || cfg.Postgres.DSN != "postgres://example" {
		t.Fatalf("process env must win: %+v", cfg)
	}
	if cfg.Server.Addr != "127.0.0.1:9000" {
		t.Fatalf("untouched file value lost: %q", cfg.Server.Addr)
	}
}

func TestEnvFileVarSelectsPath(t *testing.T) {
	p := writeEnv(t, "AGENTMEM_ADDR=127.0.0.1:9999\n")
	t.Setenv(EnvFileVar, p)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != "127.0.0.1:9999" {
		t.Fatalf("AGENTMEM_ENV_FILE ignored: %q", cfg.Server.Addr)
	}
}

func TestMalformedEnvFileFailsClosed(t *testing.T) {
	cases := map[string]string{
		"missing equals":     "AGENTMEM_ADDR\n",
		"duplicate key":      "AGENTMEM_ADDR=a\nAGENTMEM_ADDR=b\n",
		"bad integer":        "AGENTMEM_COMPUTE_MAX_ROWS=many\n",
		"bad boolean":        "AGENTMEM_SYSTEMONE_FAIL_CLOSED=maybe\n",
		"bad float":          "AGENTMEM_SYSTEMONE_MIN_CONFIDENCE=high\n",
		"unterminated quote": "AGENTMEM_GIT_ROOT='unclosed\n",
		"bad adapters JSON":  "AGENTMEM_ADAPTERS='[{\"name\":\"x\",\"kind\":\"okf\",\"enabled\":true}'\n",
		"unknown JSON field": "AGENTMEM_ADAPTERS='[{\"name\":\"x\",\"kind\":\"okf\",\"enabled\":true,\"nope\":1}]'\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeEnv(t, content)); err == nil {
				t.Fatal("malformed .env must fail closed")
			}
		})
	}
}

func TestExampleEnvFileIsValid(t *testing.T) {
	p := filepath.Join("..", "..", ".env.example")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf(".env.example must load cleanly: %v", err)
	}
	if cfg.Server.Mode != "dev" {
		t.Fatalf("the example must stay offline-safe, got mode %q", cfg.Server.Mode)
	}
	if len(cfg.Adapters) < 2 {
		t.Fatalf("example adapters = %+v", cfg.Adapters)
	}
	found := map[string]bool{}
	for _, k := range KnownEnvKeys() {
		found[k] = true
	}
	for _, required := range []string{
		"AGENTMEM_PG_DSN", "AGENTMEM_MODE", "AGENTMEM_ADAPTERS",
		"AGENTMEM_COMPUTE_TIMEOUT_MS", "AGENTMEM_SYSTEMONE_ENDPOINT",
	} {
		if !found[required] {
			t.Fatalf("missing supported key %q", required)
		}
	}
}

func TestProductionRequiresDSN(t *testing.T) {
	c := Default()
	c.Server.Mode = "production"
	if err := c.Validate(); err == nil {
		t.Fatal("production without DSN must fail")
	}
}

func TestUnsupportedCapability(t *testing.T) {
	c := Default()
	c.Router.ConflictPolicy = "rag_wins"
	if err := c.Validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
	c = Default()
	c.Adapters = append(c.Adapters, AdapterConfig{Name: "x", Kind: "mystery"})
	if err := c.Validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported for adapter kind, got %v", err)
	}
}

func TestDuplicateAdapterName(t *testing.T) {
	c := Default()
	c.Adapters = append(c.Adapters, AdapterConfig{Name: "memory", Kind: "memory"})
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate adapter names must fail")
	}
}

func TestEnginePluginKindsAndComputeBounds(t *testing.T) {
	c := Default()
	for _, kind := range []string{"llamaindex", "pageindex", "graphrag", "lightrag"} {
		c.Adapters = append(c.Adapters, AdapterConfig{Name: kind, Kind: kind, Enabled: true})
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("engine plugin kinds must be accepted: %v", err)
	}
	if c.Compute != DefaultCompute() {
		t.Fatalf("compute defaults drifted: %+v", c.Compute)
	}
	c.Compute.MaxRows = 10001
	if err := c.Validate(); err == nil {
		t.Fatal("compute.max_rows above the bound must fail closed")
	}
	c = Default()
	c.Compute.TimeoutMS = -1
	if err := c.Validate(); err == nil {
		t.Fatal("negative compute timeout must fail closed")
	}
}

func TestVectorDSNFallback(t *testing.T) {
	c := Default()
	c.Postgres.DSN = "postgres://main"
	if got := c.VectorDSN(); got != "postgres://main" {
		t.Fatalf("vector DSN should reuse main DSN, got %q", got)
	}
	c.Postgres.VectorDSN = "postgres://vector"
	if got := c.VectorDSN(); got != "postgres://vector" {
		t.Fatalf("vector DSN override ignored: %q", got)
	}
}
