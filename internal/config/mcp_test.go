package config

import (
	"strings"
	"testing"
)

func TestMCPDefaultsAreReadOnly(t *testing.T) {
	m := DefaultMCP()
	if m.Write {
		t.Fatal("write tools must default to disabled")
	}
	if m.SessionTTLMS != 1800000 || m.MaxSessions != 128 {
		t.Fatalf("defaults = %+v", m)
	}
	if Default().MCP != m {
		t.Fatalf("Default().MCP = %+v", Default().MCP)
	}
}

func TestMCPEnvKeysLoad(t *testing.T) {
	p := writeEnv(t, strings.Join([]string{
		"AGENTMEM_MCP_WRITE=true",
		"AGENTMEM_MCP_SESSION_TTL_MS=600000",
		"AGENTMEM_MCP_MAX_SESSIONS=16",
	}, "\n")+"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("MCP keys must be known: %v", err)
	}
	if !cfg.MCP.Write || cfg.MCP.SessionTTLMS != 600000 || cfg.MCP.MaxSessions != 16 {
		t.Fatalf("mcp = %+v", cfg.MCP)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate = %v", err)
	}
}

func TestMCPBoundsFailClosed(t *testing.T) {
	for _, body := range []string{
		"AGENTMEM_MCP_SESSION_TTL_MS=1000",
		"AGENTMEM_MCP_SESSION_TTL_MS=999999999",
		"AGENTMEM_MCP_MAX_SESSIONS=0",
		"AGENTMEM_MCP_MAX_SESSIONS=99999",
	} {
		p := writeEnv(t, body+"\n")
		if _, err := Load(p); err == nil {
			t.Fatalf("%s must fail validation", body)
		}
	}
}

func TestMCPTokenKeyIsKnownButIgnored(t *testing.T) {
	// The stdio transport reads AGENTMEM_MCP_TOKEN from the process environment
	// only; the closed whitelist must still recognise the key in a file so a
	// stale .env does not fail validation, and the value must not be stored.
	p := writeEnv(t, "AGENTMEM_MCP_TOKEN=secret-value\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("AGENTMEM_MCP_TOKEN must be a known key: %v", err)
	}
	if cfg.MCP.Write {
		t.Fatal("a token must not flip the write switch")
	}
	// Only the fact that the file declared the key is kept, so `agentmem mcp`
	// can explain why the value is ignored; the value itself is dropped.
	if !cfg.MCP.TokenInEnvFile {
		t.Fatal("a .env token must be recorded as ignored")
	}

	// An empty placeholder is not a declaration, and a process-environment token
	// never sets the file flag.
	empty := writeEnv(t, "AGENTMEM_MCP_TOKEN=\n")
	cfg, err = Load(empty)
	if err != nil {
		t.Fatalf("an empty token placeholder must load: %v", err)
	}
	if cfg.MCP.TokenInEnvFile {
		t.Fatal("an empty .env token must not count as a declaration")
	}
	t.Setenv("AGENTMEM_MCP_TOKEN", "from-env")
	plain := writeEnv(t, "AGENTMEM_MCP_WRITE=false\n")
	cfg, err = Load(plain)
	if err != nil {
		t.Fatalf("load = %v", err)
	}
	if cfg.MCP.TokenInEnvFile {
		t.Fatal("a process-environment token is not a .env declaration")
	}
}

func TestMCPWriteMustBeBoolean(t *testing.T) {
	p := writeEnv(t, "AGENTMEM_MCP_WRITE=yes-please\n")
	if _, err := Load(p); err == nil {
		t.Fatal("a non-boolean write switch must fail closed")
	}
}
