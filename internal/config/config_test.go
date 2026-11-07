package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  addr: x\n  nope: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("KnownFields(true) must reject unknown keys")
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Router.SystemOne.TimeoutMS != 5000 {
		t.Fatalf("System One default timeout drifted: %d", cfg.Router.SystemOne.TimeoutMS)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("AGENTMEM_PG_DSN", "postgres://example")
	t.Setenv("AGENTMEM_MODE", "production")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Postgres.DSN != "postgres://example" || cfg.Server.Mode != "production" {
		t.Fatalf("env overrides not applied: %+v", cfg)
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

func TestEnginePluginKindsAndComputeSection(t *testing.T) {
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

func TestLoadComputeSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	data := "compute:\n  timeout_ms: 1200\n  max_rows: 7\n"
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil || cfg.Compute.TimeoutMS != 1200 || cfg.Compute.MaxRows != 7 {
		t.Fatalf("compute section: %v / %+v", err, cfg.Compute)
	}
	if err := os.WriteFile(p, []byte("compute:\n  unknown: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("unknown compute key must be rejected")
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
