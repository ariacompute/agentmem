package okf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadResource(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(b.Root, "references", "revenue.sql")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("SELECT 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := b.ReadResource("references/revenue.sql")
	if err != nil || string(data) != "SELECT 1\n" {
		t.Fatalf("read: %v / %q", err, data)
	}
	for _, bad := range []string{
		"", "../outside.sql", "../../etc/passwd.sql", "/etc/passwd.sql",
		"references\\revenue.sql", "references/../../../etc/passwd.sql",
	} {
		if _, err := b.ReadResource(bad); err == nil {
			t.Fatalf("traversal accepted: %q", bad)
		}
	}
}

func TestExtractInlineSQL(t *testing.T) {
	body := "# Title\n\n```sql\nSELECT 1 FROM t\n```\n\ntext\n"
	if got := ExtractInlineSQL(body); got != "SELECT 1 FROM t" {
		t.Fatalf("inline = %q", got)
	}
	if ExtractInlineSQL("no fence") != "" {
		t.Fatal("expected no inline block")
	}
}

func TestAttestedValidateStrictAtExecutionOnly(t *testing.T) {
	a := &Attested{Runtime: "postgres", Computation: "inline",
		Parameters: []Parameter{{Name: "year", Type: "integer", Required: true}}}
	if err := a.Validate(); err != nil {
		t.Fatalf("valid contract: %v", err)
	}
	if a.RuntimeID() != "postgres" {
		t.Fatalf("runtime id = %q", a.RuntimeID())
	}
	bad := []*Attested{
		{Computation: "inline"},
		{Runtime: "postgres"},
		{Runtime: "postgres", Computation: "inline", Parameters: []Parameter{{Name: "x", Type: "object"}}},
		{Runtime: "postgres", Computation: "inline", Parameters: []Parameter{{Name: "x", Type: "integer"}, {Name: "x", Type: "integer"}}},
	}
	for i, x := range bad {
		if err := x.Validate(); err == nil {
			t.Fatalf("case %d should fail validation", i)
		}
	}
	if strings.Join(ValidParameterTypes(), "") == "" {
		t.Fatal("parameter type list is empty")
	}
}
