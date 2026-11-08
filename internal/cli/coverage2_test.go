package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliCovSQL = `SELECT SUM(amount) AS revenue_ytd
FROM orders
WHERE EXTRACT(YEAR FROM placed_at) = :year
`

const cliCovDoc = `---
type: Attested Computation
title: Revenue YTD
runtime: postgres
parameters:
  - name: year
    type: integer
    required: true
computation: references/revenue.sql
executor:
  resource: runtime/postgres.md
  receipt: [job_id, executed_sql, result]
---
# Computation
`

func writeCLIBundle(t *testing.T, root string) {
	t.Helper()
	for rel, data := range map[string]string{
		"references/revenue.sql":  cliCovSQL,
		"computations/revenue.md": cliCovDoc,
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueryCommandSuccess(t *testing.T) {
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"query", "--env", "../../nonexistent.env", "--q", "revenue"}, &buf); err != nil {
		t.Fatalf("query: %v / %s", err, buf.String())
	}
}

func TestPutCommandSuccessAndMissingID(t *testing.T) {
	offlineRoot(t)
	if err := Run([]string{"put"}, &bytes.Buffer{}); err == nil {
		t.Fatal("put without id must fail")
	}
	var buf bytes.Buffer
	if err := Run([]string{"put", "--env", "../../nonexistent.env", "--id", "concepts/x",
		"--type", "Concept", "--subject", "alice", "--body", "b"}, &buf); err != nil {
		t.Fatalf("put: %v / %s", err, buf.String())
	}
}

func TestComputeCommandSuccessAndMissingID(t *testing.T) {
	root := offlineRoot(t)
	writeCLIBundle(t, root)
	if err := Run([]string{"compute"}, &bytes.Buffer{}); err == nil {
		t.Fatal("compute without id must fail")
	}
	var buf bytes.Buffer
	if err := Run([]string{"compute", "--env", "../../nonexistent.env", "--id", "computations/revenue",
		"--param", "year=2026"}, &buf); err != nil {
		t.Fatalf("compute: %v / %s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "display_allowed") {
		t.Fatalf("compute output = %s", buf.String())
	}
}

func TestValidateCommandSuccess(t *testing.T) {
	offlineRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{"validate", "--env", "../../nonexistent.env"}, &buf); err != nil {
		t.Fatalf("validate: %v / %s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "ok") {
		t.Fatalf("validate output = %s", buf.String())
	}
}

func TestServeAuthenticatorError(t *testing.T) {
	t.Setenv("AGENTMEM_AUTH_ENABLED", "true")
	t.Setenv("AGENTMEM_AUTH_TOKENS", `[{"token_sha256":"`+strings.Repeat("a", 64)+`","subject":"a","scopes":["nope"]}]`)
	var buf bytes.Buffer
	if err := Run([]string{"serve", "--env", "../../nonexistent.env"}, &buf); err == nil {
		t.Fatal("serve must fail on an unknown auth scope")
	}
}
