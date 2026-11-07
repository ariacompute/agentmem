package compute

import (
	"errors"
	"strings"
	"testing"
)

func TestBindValidValues(t *testing.T) {
	c, err := LoadContract(testBundle(t), "computations/revenue")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(c, map[string]any{"year": 2026})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if !strings.Contains(b.Statement, "$1") || strings.Contains(b.Statement, ":year") {
		t.Fatalf("statement = %q", b.Statement)
	}
	if len(b.Args) != 1 || b.Args[0] != int64(2026) {
		t.Fatalf("args = %+v", b.Args)
	}
	if b.ParamsDigest == "" {
		t.Fatal("missing params digest")
	}
	b2, _ := Bind(c, map[string]any{"year": "2026"})
	if b2.ParamsDigest != b.ParamsDigest {
		t.Fatal("string/integer forms of the same value must share a digest")
	}
	if b3, _ := Bind(c, map[string]any{"year": 2025}); b3.ParamsDigest == b.ParamsDigest {
		t.Fatal("different values must produce different digests")
	}
}

func TestBindRejectsInvalidValues(t *testing.T) {
	c, _ := LoadContract(testBundle(t), "computations/revenue")
	cases := []struct {
		name   string
		values map[string]any
		want   error
	}{
		{"missing required", map[string]any{}, ErrInvalidParams},
		{"undeclared", map[string]any{"year": 2026, "quarter": 1}, ErrInvalidParams},
		{"wrong type", map[string]any{"year": "not-a-year"}, ErrInvalidParams},
		{"rewrite computation", map[string]any{"year": 2026, "computation": "SELECT 1"}, ErrComputationRewrite},
		{"rewrite sql", map[string]any{"year": 2026, "sql": "SELECT 1"}, ErrComputationRewrite},
		{"rewrite runtime", map[string]any{"year": 2026, "runtime": "bigquery"}, ErrComputationRewrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Bind(c, tc.values); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestBindRejectsUndeclaredSQLPlaceholder(t *testing.T) {
	c, _ := LoadContract(testBundle(t), "computations/revenue")
	c.SQL = "SELECT * FROM orders WHERE year = :year AND region = :region"
	if _, err := Bind(c, map[string]any{"year": 2026}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("got %v, want ErrInvalidParams", err)
	}
}

func TestBindParameterTypes(t *testing.T) {
	c, _ := LoadContract(testBundle(t), "computations/inline")
	b, err := Bind(c, map[string]any{"threshold": "12.5"})
	if err != nil || b.Args[0] != 12.5 {
		t.Fatalf("number coercion: %v / %+v", err, b.Args)
	}
	if _, err := Bind(c, map[string]any{"threshold": true}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("boolean into number: %v", err)
	}
}
