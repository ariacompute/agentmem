package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliSQL = `SELECT SUM(amount) FROM orders WHERE year = :year
`

const cliDoc = `---
type: Attested Computation
title: Revenue YTD
runtime: postgres
verified:
  - by: human:jsmith@acme
parameters:
  - name: year
    type: integer
    required: true
computation: references/revenue.sql
executor:
  resource: runtime/postgres.md
  receipt: [job_id, executed_sql, result]
attester:
  resource: attesters/sql_equality.go
---
# Computation
`

func cliComputeRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bundle")
	t.Setenv("AGENTMEM_GIT_ROOT", root)
	for rel, data := range map[string]string{
		"references/revenue.sql":   cliSQL,
		"computations/revenue.md":  cliDoc,
		"computations/bigquery.md": strings.Replace(cliDoc, "runtime: postgres", "runtime: bigquery", 1),
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestComputeCommandOffline(t *testing.T) {
	cliComputeRoot(t)
	var buf bytes.Buffer
	if err := Run([]string{
		"compute", "--env", "../../nonexistent.env",
		"--id", "computations/revenue", "--subject", "analyst:1",
		"--param", "year=2026",
	}, &buf); err != nil {
		t.Fatalf("compute: %v / %s", err, buf.String())
	}
	var env struct {
		DisplayAllowed bool   `json:"display_allowed"`
		DisplayValue   string `json:"display_value"`
		JobID          string `json:"job_id"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output = %q (%v)", buf.String(), err)
	}
	if !env.DisplayAllowed || env.DisplayValue == "" || env.JobID == "" {
		t.Fatalf("envelope = %+v", env)
	}

	buf.Reset()
	if err := Run([]string{
		"computations", "--env", "../../nonexistent.env",
		"--id", "computations/revenue", "--limit", "10",
	}, &buf); err != nil {
		t.Fatalf("computations: %v / %s", err, buf.String())
	}
	var records []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &records); err != nil || len(records) != 1 {
		t.Fatalf("audit = %q (%v)", buf.String(), err)
	}
	if records[0]["verdict"] != "passed" || records[0]["subject"] != "analyst:1" {
		t.Fatalf("audit record = %+v", records[0])
	}
}

func TestComputeCommandRejectsInvalidInput(t *testing.T) {
	cliComputeRoot(t)
	cases := [][]string{
		{"compute", "--env", "../../nonexistent.env", "--id", "computations/revenue", "--param", "year=oops"},
		{"compute", "--env", "../../nonexistent.env", "--id", "computations/bigquery", "--param", "year=2026"},
		{"compute", "--env", "../../nonexistent.env", "--id", "computations/revenue", "--param", "bad"},
		{"compute", "--env", "../../nonexistent.env"},
	}
	for i, args := range cases {
		if err := Run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("case %d should fail", i)
		}
	}
}

func TestUsageListsComputeCommands(t *testing.T) {
	var buf bytes.Buffer
	if err := Run([]string{"help"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "compute") || !strings.Contains(buf.String(), "computations") {
		t.Fatalf("usage = %q", buf.String())
	}
}
