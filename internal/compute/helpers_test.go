package compute

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agentmem/internal/okf"
)

const sanctionedSQL = `SELECT SUM(amount) AS revenue_ytd
FROM orders
WHERE EXTRACT(YEAR FROM placed_at) = :year
  AND status = 'booked'
`

const postgresDoc = `---
type: Attested Computation
title: Revenue YTD
runtime: postgres
status: stable
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
stale_after: 2099-12-31T00:00:00Z
---
# Computation

Sanctioned revenue query.
`

const inlineDoc = `---
type: Attested Computation
title: Inline
runtime: postgres
parameters:
  - name: threshold
    type: number
    required: true
computation: inline
---
# Computation

` + "```sql\n" + `SELECT COUNT(*) FROM orders WHERE amount > :threshold
` + "```\n"

func writeResource(t *testing.T, b *okf.Bundle, rel, content string) {
	t.Helper()
	p := filepath.Join(b.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testBundle(t *testing.T) *okf.Bundle {
	t.Helper()
	b, err := okf.Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	b.LogWrites = false
	writeResource(t, b, "references/revenue.sql", sanctionedSQL)
	writeResource(t, b, "computations/revenue.md", postgresDoc)
	writeResource(t, b, "computations/inline.md", inlineDoc)
	return b
}

func validPair(t *testing.T) (BoundComputation, Receipt) {
	t.Helper()
	c, err := LoadContract(testBundle(t), "computations/revenue")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(c, map[string]any{"year": 2026})
	if err != nil {
		t.Fatal(err)
	}
	rt := &postgresRuntime{exec: FakeExecutor{}, maxRows: 100}
	r, err := rt.Execute(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return b, r
}
