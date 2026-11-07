package compute

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadContractFromResource(t *testing.T) {
	b := testBundle(t)
	c, err := LoadContract(b, "computations/revenue")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Runtime != "postgres" || c.Source != "resource:references/revenue.sql" {
		t.Fatalf("contract = %+v", c)
	}
	if !c.Verified || c.StaleAfter == nil || len(c.Parameters) != 1 {
		t.Fatalf("trust fields = %+v", c)
	}
	if !strings.Contains(c.SQL, ":year") || c.ContractHash == "" {
		t.Fatalf("sql/hash = %q / %q", c.SQL, c.ContractHash)
	}
	c2, err := LoadContract(b, "computations/revenue")
	if err != nil || c2.ContractHash != c.ContractHash {
		t.Fatal("contract hash is not stable")
	}
}

func TestLoadContractFromInlineBlock(t *testing.T) {
	c, err := LoadContract(testBundle(t), "computations/inline")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Source != "inline" || !strings.Contains(c.SQL, ":threshold") {
		t.Fatalf("inline contract = %+v", c)
	}
}

func TestLoadContractRejectsMissingAndUnsafe(t *testing.T) {
	b := testBundle(t)
	writeResource(t, b, "computations/noattested.md", "---\ntype: Concept\n---\nplain\n")
	writeResource(t, b, "computations/multi.md", strings.Replace(postgresDoc,
		"computation: references/revenue.sql", "computation: references/multi.sql", 1))
	writeResource(t, b, "references/multi.sql", "SELECT 1; DROP TABLE orders")
	writeResource(t, b, "computations/write.md", strings.Replace(postgresDoc,
		"computation: references/revenue.sql", "computation: references/write.sql", 1))
	writeResource(t, b, "references/write.sql", "WITH x AS (SELECT 1) UPDATE orders SET amount = 0")

	for _, id := range []string{"computations/missing", "computations/noattested", "computations/multi", "computations/write"} {
		if _, err := LoadContract(b, id); !errors.Is(err, ErrContract) {
			t.Fatalf("%s: got %v, want ErrContract", id, err)
		}
	}
}

func TestSafeStatementRejectsMutation(t *testing.T) {
	for _, sql := range []string{
		"DELETE FROM orders", "SELECT * FROM orders; SELECT 1",
		"SELECT * INTO tmp FROM orders", "INSERT INTO orders VALUES (1)",
	} {
		if _, err := SafeStatement(sql); err == nil {
			t.Fatalf("SafeStatement accepted %q", sql)
		}
	}
	if _, err := SafeStatement("SELECT 1 FROM orders -- trailing comment"); err != nil {
		t.Fatalf("read-only statement rejected: %v", err)
	}
}
