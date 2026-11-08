package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/config"
)

func offlineRoot(t *testing.T) string {
	root := t.TempDir()
	t.Setenv("AGENTMEM_GIT_ROOT", root)
	return root
}

func TestPostgresMode(t *testing.T) {
	if got := postgresMode(&config.Config{}); got != "offline fake" {
		t.Fatalf("empty dsn = %q", got)
	}
	if got := postgresMode(&config.Config{
		Postgres: config.PostgresConfig{DSN: "postgres://x"},
	}); got != "configured" {
		t.Fatalf("dsn = %q", got)
	}
}

func TestBuildAuthenticatorScopes(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.Enabled = true
	cfg.Auth.Tokens = []config.AuthTokenSpec{
		{TokenSHA256: strings.Repeat("a", 64), Subject: "alice", Scopes: []string{"knowledge:read"}},
	}
	if _, err := buildAuthenticator(cfg); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	bad := config.Default()
	bad.Auth.Enabled = true
	bad.Auth.Tokens = []config.AuthTokenSpec{
		{TokenSHA256: strings.Repeat("a", 64), Subject: "alice", Scopes: []string{"nope"}},
	}
	if _, err := buildAuthenticator(bad); err == nil {
		t.Fatal("unknown scope must error")
	}
}

func TestParamFlagsBehavior(t *testing.T) {
	p := &paramFlags{values: map[string]any{}}
	if err := p.Set("year=2026"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := p.Set("=x"); err == nil {
		t.Fatal("empty key must error")
	}
	if err := p.Set("noequals"); err == nil {
		t.Fatal("missing '=' must error")
	}
	if !strings.Contains(p.String(), "year=2026") {
		t.Fatalf("string = %q", p.String())
	}
	p2 := &paramFlags{}
	if p2.String() != "" {
		t.Fatalf("empty params string = %q", p2.String())
	}
}

func TestQueryCommandRequiresQ(t *testing.T) {
	offlineRoot(t)
	if err := Run([]string{"query", "--env", "../../nonexistent.env"}, &bytes.Buffer{}); err == nil {
		t.Fatal("query without --q must fail")
	}
}

func TestMigrateCommandOffline(t *testing.T) {
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"migrate", "--env", "../../nonexistent.env", "--dry-run", "--limit", "5"}, &buf); err != nil {
		t.Fatalf("migrate: %v / %s", err, buf.String())
	}
	var rep map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("migrate output = %q (%v)", buf.String(), err)
	}
}

func TestConceptsCommandOffline(t *testing.T) {
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"concepts", "--env", "../../nonexistent.env"}, &buf); err != nil {
		t.Fatalf("concepts: %v / %s", err, buf.String())
	}
	var ids []string
	if err := json.Unmarshal(buf.Bytes(), &ids); err != nil {
		t.Fatalf("concepts output = %q (%v)", buf.String(), err)
	}
}

func TestDecisionsCommandOffline(t *testing.T) {
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"decisions", "--env", "../../nonexistent.env", "--id", "x", "--limit", "3"}, &buf); err != nil {
		t.Fatalf("decisions: %v / %s", err, buf.String())
	}
	var recs []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &recs); err != nil {
		t.Fatalf("decisions output = %q (%v)", buf.String(), err)
	}
}

func TestComputationsCommandOffline(t *testing.T) {
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"computations", "--env", "../../nonexistent.env", "--id", "x", "--limit", "3"}, &buf); err != nil {
		t.Fatalf("computations: %v / %s", err, buf.String())
	}
	var recs []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &recs); err != nil {
		t.Fatalf("computations output = %q (%v)", buf.String(), err)
	}
}

func TestEvalCommandErrors(t *testing.T) {
	if err := Run([]string{"eval", "--dataset", "does-not-exist.jsonl"}, &bytes.Buffer{}); err == nil {
		t.Fatal("missing dataset must fail")
	}
	f := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(f, []byte("this is not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"eval", "--dataset", f}, &bytes.Buffer{}); err == nil {
		t.Fatal("malformed jsonl must fail")
	}
}

func TestServeCommandAssembleError(t *testing.T) {
	// A System One endpoint without an egress allowlist fails validation,
	// which makes service.Assemble return an error.
	t.Setenv("AGENTMEM_ROUTER_SYSTEMONE_ENDPOINT", "http://unreachable.example")
	t.Setenv("AGENTMEM_EGRESS_ALLOWED_HOSTS", "")
	var buf bytes.Buffer
	if err := Run([]string{"serve", "--env", "../../nonexistent.env"}, &buf); err == nil {
		t.Fatal("serve must fail when assembly fails")
	}
}

func TestServeCommandListenError(t *testing.T) {
	// With a valid offline assembly, an invalid listen address makes
	// ListenAndServe fail, which must surface as an error (covers the
	// --addr override and the error return path).
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"serve", "--env", "../../nonexistent.env", "--addr", "not-a-valid-addr:!!"}, &buf); err == nil {
		t.Fatal("serve must fail on a bad listen address")
	}
}
