package compute

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

func TestErrorStringAndIs(t *testing.T) {
	if (&Error{Kind: "x"}).Error() != "compute: x" {
		t.Fatal("empty message")
	}
	if (&Error{Kind: "x", Message: "m"}).Error() != "compute: x: m" {
		t.Fatal("with message")
	}
	if !(&Error{Kind: "x"}).Is(&Error{Kind: "x"}) {
		t.Fatal("is equal kind")
	}
	if (&Error{Kind: "x"}).Is(&Error{Kind: "y"}) {
		t.Fatal("is different kind")
	}
}

func TestClass(t *testing.T) {
	if Class(nil) != "passed" {
		t.Fatal("nil")
	}
	if Class(&Error{Kind: "execution"}) != "execution" {
		t.Fatal("compute error")
	}
	if Class(errors.New("boom")) != "internal" {
		t.Fatal("other error")
	}
}

func TestDeriveResult(t *testing.T) {
	if DeriveResult(meta.SQLRows{}) != "" {
		t.Fatal("empty")
	}
	if DeriveResult(meta.SQLRows{Values: [][]any{{}}}) != "" {
		t.Fatal("empty row")
	}
	if DeriveResult(meta.SQLRows{Values: [][]any{{"x"}}}) != "x" {
		t.Fatal("single cell")
	}
	multi := meta.SQLRows{Values: [][]any{{"a", "b"}, {"c"}}}
	if out := DeriveResult(multi); out == "" || out[0] != '[' {
		t.Fatalf("multi-row = %q", out)
	}
}

func TestCoerceBranches(t *testing.T) {
	p := func(typ string) okf.Parameter { return okf.Parameter{Name: "p", Type: typ} }
	cases := []struct {
		typ string
		in  any
		ok  bool
		out any
	}{
		{"integer", int(5), true, int64(5)},
		{"integer", int32(5), true, int64(5)},
		{"integer", int64(7), true, int64(7)},
		{"integer", float64(8), true, int64(8)},
		{"integer", float64(8.5), false, nil},
		{"integer", json.Number("9"), true, int64(9)},
		{"integer", json.Number("x"), false, nil},
		{"integer", "10", true, int64(10)},
		{"integer", "x", false, nil},
		{"integer", true, false, nil},
		{"number", int(3), true, float64(3)},
		{"number", int64(3), true, float64(3)},
		{"number", float64(2.5), true, float64(2.5)},
		{"number", float64(math.NaN()), false, nil},
		{"number", float64(math.Inf(1)), false, nil},
		{"number", json.Number("4.5"), true, float64(4.5)},
		{"number", json.Number("x"), false, nil},
		{"number", "5.5", true, float64(5.5)},
		{"number", "x", false, nil},
		{"number", true, false, nil},
		{"boolean", true, true, true},
		{"boolean", "true", true, true},
		{"boolean", "x", false, nil},
		{"boolean", 1, false, nil},
		{"string", "hello", true, "hello"},
		{"string", 1, false, nil},
		{"date", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), true, "2026-01-02"},
		{"date", "2026-01-02", true, "2026-01-02"},
		{"date", "2026-01-02T03:04:05Z", true, "2026-01-02"},
		{"date", "nope", false, nil},
		{"date", 1, false, nil},
		{"weird", "x", false, nil},
	}
	for _, c := range cases {
		got, err := coerce(p(c.typ), c.in)
		if c.ok {
			if err != nil {
				t.Fatalf("%s %v err: %v", c.typ, c.in, err)
			}
			if got != c.out {
				t.Fatalf("%s %v = %v want %v", c.typ, c.in, got, c.out)
			}
		} else if err == nil {
			t.Fatalf("%s %v should fail", c.typ, c.in)
		}
	}
}

func TestBindBranches(t *testing.T) {
	c := Contract{
		Parameters: []okf.Parameter{
			{Name: "year", Type: "integer", Required: true},
			{Name: "region", Type: "string", Required: false},
		},
		SQL: "SELECT * FROM t WHERE year = :year AND region = :region",
	}
	if _, err := Bind(c, map[string]any{"computation": "x"}); err == nil {
		t.Fatal("reserved key must fail")
	}
	if _, err := Bind(c, map[string]any{"unknown": 1}); err == nil {
		t.Fatal("undeclared key must fail")
	}
	if _, err := Bind(c, map[string]any{}); err == nil {
		t.Fatal("missing required must fail")
	}
	if _, err := Bind(c, map[string]any{"year": nil}); err == nil {
		t.Fatal("nil required must fail")
	}
	bc, err := Bind(c, map[string]any{"year": 2026})
	if err != nil {
		t.Fatalf("optional missing: %v", err)
	}
	if bc.Args[1] != nil {
		t.Fatal("optional absent value must bind to nil")
	}
	bad := Contract{Parameters: []okf.Parameter{{Name: "a", Type: "integer"}}, SQL: "SELECT :b"}
	if _, err := Bind(bad, map[string]any{"a": 1}); err == nil {
		t.Fatal("undeclared SQL placeholder must fail")
	}
}
