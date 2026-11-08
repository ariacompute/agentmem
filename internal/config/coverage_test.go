package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidEnvKey(t *testing.T) {
	if validEnvKey("") {
		t.Fatal("empty key must be invalid")
	}
	if !validEnvKey("AGENTMEM_X") {
		t.Fatal("valid key rejected")
	}
	if validEnvKey("BAD-KEY") {
		t.Fatal("dash must be invalid")
	}
	if validEnvKey("1BAD") {
		t.Fatal("leading digit must be invalid")
	}
	if !validEnvKey("OK2") {
		t.Fatal("digit after first position must be valid")
	}
}

func TestParseEnvValue(t *testing.T) {
	if v, _ := parseEnvValue(""); v != "" {
		t.Fatalf("empty value = %q", v)
	}
	if v, err := parseEnvValue("'abc'"); v != "abc" || err != nil {
		t.Fatalf("single-quoted = %q %v", v, err)
	}
	if _, err := parseEnvValue("'abc"); err == nil {
		t.Fatal("unterminated single quote must fail")
	}
	if v, err := parseEnvValue(`"a\"b"`); err != nil || v != `a"b` {
		t.Fatalf("double-quoted escape = %q %v", v, err)
	}
	if _, err := parseEnvValue(`"abc`); err == nil {
		t.Fatal("unterminated double quote must fail")
	}
	if v, _ := parseEnvValue("hello # comment"); v != "hello" {
		t.Fatalf("inline comment = %q", v)
	}
}

func TestAdaptersSetterErrors(t *testing.T) {
	c := Default()
	if err := adaptersSetter(c, "not json"); err == nil {
		t.Fatal("malformed adapters json must fail")
	}
	if err := adaptersSetter(c, `[{"name":"x"}] extra`); err == nil {
		t.Fatal("trailing data must fail")
	}
}

func TestTokensSetterErrors(t *testing.T) {
	c := Default()
	if err := tokensSetter(c, "not json"); err == nil {
		t.Fatal("malformed tokens json must fail")
	}
	if err := tokensSetter(c, ""); err != nil {
		t.Fatal("empty tokens value must be allowed")
	}
}

func TestListSetterErrors(t *testing.T) {
	c := Default()
	s := listSetter(func(cc *Config) *[]string { return &cc.Egress.AllowedHosts })
	if err := s(c, "a,,b"); err == nil {
		t.Fatal("empty list entry must fail")
	}
}

func TestReadEnvFileMissingAndPresent(t *testing.T) {
	if m, err := readEnvFile(filepath.Join(t.TempDir(), "nope.env")); err != nil || len(m) != 0 {
		t.Fatalf("missing file: %v %v", m, err)
	}
	p := filepath.Join(t.TempDir(), "x.env")
	if err := os.WriteFile(p, []byte("AGENTMEM_MODE=production\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := readEnvFile(p)
	if err != nil || m["AGENTMEM_MODE"] != "production" {
		t.Fatalf("present file: %v %v", m, err)
	}
}
