package compute

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewServiceDefaults(t *testing.T) {
	s := NewService(nil, nil, nil, 0, 0)
	if s.Timeout != 5*time.Second || s.MaxRows != 100 {
		t.Fatalf("defaults = %+v", s)
	}
	s2 := NewService(nil, nil, nil, time.Second, 7)
	if s2.Timeout != time.Second || s2.MaxRows != 7 {
		t.Fatalf("explicit = %+v", s2)
	}
}

func TestServiceRunEmptyConcept(t *testing.T) {
	svc := &Service{}
	if _, err := svc.Run(context.Background(), Request{ConceptID: ""}); err == nil {
		t.Fatal("empty concept must error")
	}
}

func TestServiceListNilAudit(t *testing.T) {
	svc := &Service{}
	out, err := svc.List(context.Background(), "c", 5)
	if err != nil || len(out) != 0 {
		t.Fatalf("nil audit list = %+v (%v)", out, err)
	}
}

func TestAttesterClock(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := (Attester{Now: func() time.Time { return fixed }}).clock(); !got.Equal(fixed) {
		t.Fatalf("clock = %v", got)
	}
	if (Attester{}).clock().IsZero() {
		t.Fatal("default clock must be non-zero")
	}
}

func TestSafeStatementEmptyAndMultiple(t *testing.T) {
	if _, err := SafeStatement("   "); err == nil {
		t.Fatal("empty statement must error")
	}
	if _, err := SafeStatement("SELECT 1; SELECT 2"); err == nil {
		t.Fatal("multi statement must error")
	}
}

func TestComputationBodyBranches(t *testing.T) {
	sql, source, err := computationBody(nil, "", "```sql\nSELECT 1\n```")
	if err != nil || sql != "SELECT 1" || source != "inline" {
		t.Fatalf("inline = %q %q %v", sql, source, err)
	}
	if _, _, err := computationBody(nil, "foo bar", ""); err == nil {
		t.Fatal("invalid ref must error")
	}
	sql, source, err = computationBody(nil, "SELECT 1\nSELECT 2", "")
	if err != nil || source != "inline-frontmatter" || !strings.Contains(sql, "\n") {
		t.Fatalf("frontmatter = %q %q %v", sql, source, err)
	}
}

func TestCanonicalSQLBranches(t *testing.T) {
	cases := map[string]string{
		"SELECT a -- comment\nFROM t": "SELECT a FROM t",
		"SELECT /* c */ 1":            "SELECT 1",
		"SELECT 'it''s' FROM t":       "SELECT 'it''s' FROM t",
		"SELECT :Name FROM t":         "SELECT ? FROM t",
		"SELECT 1+1":                  "SELECT 1 + 1",
	}
	for in, want := range cases {
		if got := canonicalSQL(in); got != want {
			t.Fatalf("canonicalSQL(%q) = %q want %q", in, got, want)
		}
	}
	// Unterminated block comment exercises the i = len(s) branch.
	_ = canonicalSQL("SELECT /* unterminated")
	if !strings.Contains(canonicalSQL("select from where"), "SELECT") {
		t.Fatal("keywords must uppercase")
	}
}

func TestCanonicalSQLEvictsCache(t *testing.T) {
	// Push more than maxCanonicalCache unique statements to trigger the reset.
	for i := 0; i < maxCanonicalCache+10; i++ {
		CanonicalSQL("SELECT " + strings.Repeat("x", i%20) + "_" + strings.Repeat("y", i/20))
	}
}

func TestFakeExecutorCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (FakeExecutor{}).Query(ctx, "SELECT 1", nil, 1); err == nil {
		t.Fatal("cancelled fake executor must error")
	}
}

func TestRuntimeRegistryRegisterBranches(t *testing.T) {
	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Register(nil); err == nil {
		t.Fatal("nil runtime must error")
	}
	if err := r.Register(&unsupportedRuntime{name: ""}); err == nil {
		t.Fatal("empty name must error")
	}
	if err := r.Register(&unsupportedRuntime{name: "postgres"}); err == nil {
		t.Fatal("duplicate must error")
	}
}
