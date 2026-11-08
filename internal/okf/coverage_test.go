package okf

import (
	"path/filepath"
	"testing"
)

func TestAttestedReceiptFieldsAndRuntimeID(t *testing.T) {
	doc := "---\ntype: Attested Computation\nruntime: BigQuery\nparameters:\n  - { name: year, type: integer, required: true }\ncomputation: refs/revenue.sql\nexecutor:\n  resource: skills/x.md\n  receipt: [job_id, executed_sql]\n---\n# c\n"
	d, err := ParseDocument([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	a := d.Attested
	if got := a.ReceiptFields(); len(got) != 2 || got[0] != "job_id" {
		t.Fatalf("receipt = %v", got)
	}
	if a.RuntimeID() != "bigquery" {
		t.Fatalf("runtime id = %q", a.RuntimeID())
	}
	var nilA *Attested
	if nilA.ReceiptFields() != nil {
		t.Fatal("nil attested receipt must be nil")
	}
	if nilA.RuntimeID() != "" {
		t.Fatal("nil attested runtime must be empty")
	}
}

func TestAttestedValidateErrors(t *testing.T) {
	cases := []*Attested{
		{},              // missing runtime
		{Runtime: "bq"}, // missing computation
		{Runtime: "bq", Computation: "x", Parameters: []Parameter{{Name: "y", Type: "integer"}, {Name: "y", Type: "integer"}}}, // dup
		{Runtime: "bq", Computation: "x", Parameters: []Parameter{{Name: "y", Type: "float"}}},                                 // bad type
		{Runtime: "bq", Computation: "x", Parameters: []Parameter{{Name: "", Type: "integer"}}},                                // empty name
	}
	for i, a := range cases {
		err := a.Validate()
		if err == nil {
			t.Fatalf("case %d should fail", i)
		}
		if err.Error() == "" {
			t.Fatalf("case %d error message empty", i)
		}
	}
	// nil receiver
	var nilA *Attested
	if nilA.Validate() == nil {
		t.Fatal("nil attested validate must fail")
	}
}

func TestParseSourcesAndActorWindow(t *testing.T) {
	doc := "---\ntype: X\nsources:\n  - id: s1\n    resource: file.md\n    title: T\n    author: A\n    usage_count: 3\n    last_modified: 2026-01-01T00:00:00Z\n    usage_window: { from: 2026-01-01T00:00:00Z, to: 2026-02-01T00:00:00Z }\n---\nbody\n"
	d, err := ParseDocument([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sources) != 1 {
		t.Fatalf("sources = %d", len(d.Sources))
	}
	s := d.Sources[0]
	if s.ID != "s1" || s.Resource != "file.md" || s.Author != "A" || s.UsageCount != 3 {
		t.Fatalf("source = %+v", s)
	}
	if s.LastModified == nil {
		t.Fatalf("source last_modified = %+v", s)
	}
}

func TestReadResourceMissing(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadResource("does-not-exist.md"); err == nil {
		t.Fatal("missing resource must error")
	}
}
