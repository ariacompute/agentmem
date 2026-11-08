package okf

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestEffectiveStatusBranches(t *testing.T) {
	if (&Document{}).EffectiveStatus() != StatusStable {
		t.Fatal("default status")
	}
	if (&Document{Status: StatusDraft}).EffectiveStatus() != StatusDraft {
		t.Fatal("explicit status")
	}
}

func TestGuardFrontmatterLimits(t *testing.T) {
	if err := guardFrontmatter(make([]byte, maxFrontmatterBytes+1)); err == nil {
		t.Fatal("oversized must error")
	}
	if err := guardFrontmatter([]byte(strings.Repeat(" &a", 40))); err == nil {
		t.Fatal("alias bomb must error")
	}
}

func TestSplitFrontmatterErrors(t *testing.T) {
	if _, _, err := SplitFrontmatter([]byte("no delimiter")); err == nil {
		t.Fatal("missing open")
	}
	if _, _, err := SplitFrontmatter([]byte("---\nabc\n")); err == nil {
		t.Fatal("missing close")
	}
	if _, _, err := SplitFrontmatter(make([]byte, maxDocumentBytes+1)); err == nil {
		t.Fatal("oversized document")
	}
}

func TestParseDocumentNonMapping(t *testing.T) {
	if _, err := ParseDocument([]byte("---\n- a\n- b\n---\nbody")); err == nil {
		t.Fatal("sequence frontmatter must error")
	}
	if _, err := ParseDocument([]byte("---\nfoo: [\n---\nbody")); err == nil {
		t.Fatal("invalid yaml must error")
	}
}

func TestDecodeKnownAllKeys(t *testing.T) {
	data := []byte(`---
type: Metric
title: Revenue
description: total revenue
resource: r
status: stable
tags: [a, b]
stale_after: 2030-01-01
generated:
  by: alice
  at: 2026-01-01T00:00:00Z
verified:
  - by: human:bob
    at: 2026-01-02T00:00:00Z
sources:
  - id: s1
    resource: file.md
    title: T
    author: A
    usage_count: 3
    last_modified: 2026-01-03T00:00:00Z
runtime: postgres
parameters:
  - name: year
    type: integer
    required: true
computation: SELECT 1
executor:
  resource: runtime/postgres.md
  receipt: [job_id]
attester:
  resource: attester/x.md
---
body
`)
	d, err := ParseDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if d.Type != "Metric" || d.Title != "Revenue" || d.Status != "stable" || len(d.Tags) != 2 {
		t.Fatalf("scalar fields = %+v", d)
	}
	if d.StaleAfter == nil || d.Generated == nil || len(d.Verified) != 1 || len(d.Sources) != 1 {
		t.Fatalf("structured fields = %+v", d)
	}
	if d.Sources[0].UsageCount != 3 || d.Sources[0].LastModified == nil {
		t.Fatalf("source = %+v", d.Sources[0])
	}
	if d.Attested == nil || d.Attested.Runtime != "postgres" || len(d.Attested.Parameters) != 1 ||
		d.Attested.Computation != "SELECT 1" || d.Attested.Executor == nil || d.Attested.Attester == nil {
		t.Fatalf("attested = %+v", d.Attested)
	}
}

func TestParseVerifiedBareMapping(t *testing.T) {
	data := []byte("---\ntype: T\nverified:\n  by: human:x\n  at: 2026-01-01\n---\n")
	d, err := ParseDocument(data)
	if err != nil || len(d.Verified) != 1 {
		t.Fatalf("bare verified = %+v (%v)", d, err)
	}
}

func TestScalarHelperEdgeCases(t *testing.T) {
	if scalar(nil) != "" {
		t.Fatal("nil scalar")
	}
	if stringList(&yaml.Node{Kind: yaml.ScalarNode}) != nil {
		t.Fatal("non-sequence stringList")
	}
	if _, ok := parseTime(&yaml.Node{Kind: yaml.ScalarNode, Value: "not-a-date"}); ok {
		t.Fatal("bad time")
	}
	if mappingValue(nil, "x") != nil {
		t.Fatal("nil mappingValue")
	}
	if _, ok := parseActor(nil); ok {
		t.Fatal("nil parseActor")
	}
	if parseVerified(nil) != nil {
		t.Fatal("nil parseVerified")
	}
	if parseVerified(&yaml.Node{Kind: yaml.ScalarNode}) != nil {
		t.Fatal("scalar parseVerified")
	}
	if parseSources(nil) != nil {
		t.Fatal("nil parseSources")
	}
}

func TestValueForAllKeys(t *testing.T) {
	d := &Document{
		Type: "T", Title: "t", Description: "d", Resource: "r", Status: "stable", Tags: []string{"a"},
	}
	now := mustParse(t, "2026-01-01T00:00:00Z")
	d.StaleAfter = &now
	d.Generated = &ActorEvent{By: "alice", At: now}
	d.Verified = []ActorEvent{{By: "human:bob", At: now}}
	d.Sources = []Source{{ID: "s", Resource: "f", LastModified: &now}}
	d.Attested = &Attested{
		Runtime: "postgres", Computation: "SELECT 1",
		Parameters: []Parameter{{Name: "y", Type: "integer", Required: true}},
		Executor:   &ExecutorSpec{Resource: "runtime/pg.md", Receipt: []string{"job_id"}},
		Attester:   &AttesterSpec{Resource: "attester/a.md"},
	}
	for _, key := range []string{"type", "title", "description", "resource", "status", "tags", "stale_after",
		"generated", "verified", "sources", "runtime", "parameters", "computation", "executor", "attester"} {
		if _, ok := d.valueFor(key); !ok {
			t.Fatalf("valueFor(%q) missing", key)
		}
	}
	if _, ok := d.valueFor("unknown"); ok {
		t.Fatal("unknown key must be false")
	}
	// Empty document: none of the optional values apply.
	empty := &Document{Type: "T"}
	for _, key := range []string{"title", "stale_after", "generated", "verified", "sources", "runtime", "parameters", "computation", "executor", "attester"} {
		if _, ok := empty.valueFor(key); ok {
			t.Fatalf("empty valueFor(%q) should be false", key)
		}
	}
}

func TestMarshalBranches(t *testing.T) {
	if _, err := (&Document{}).Marshal(); err == nil {
		t.Fatal("missing type must error")
	}
	d, err := ParseDocument([]byte("---\ntype: T\ntitle: hi\ncustom: keep\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.SetVisibility("private")
	d.SetOwner("alice")
	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseDocument(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.Type != "T" || back.Title != "hi" || back.Visibility() != "private" || back.Owner() != "alice" {
		t.Fatalf("roundtrip = %+v", back)
	}
	if !back.Extra()["custom"] {
		t.Fatal("unknown key lost")
	}
}

func TestExtraStringAndSetScalar(t *testing.T) {
	zero := &Document{}
	if zero.Visibility() != "" {
		t.Fatal("nil extra visibility")
	}
	zero.SetVisibility("public")
	if zero.Visibility() != "public" {
		t.Fatal("set visibility")
	}
	// Setting twice must not duplicate the order entry.
	zero.SetVisibility("private")
	if zero.Visibility() != "private" {
		t.Fatal("update visibility")
	}
	if zero.extraString("owner") != "" {
		t.Fatal("missing owner")
	}
}

func TestCheckNodeBudget(t *testing.T) {
	n := &yaml.Node{Kind: yaml.ScalarNode}
	if err := checkNodeBudget(n, maxYAMLDepth+1, new(int)); err == nil {
		t.Fatal("depth budget")
	}
	count := maxYAMLNodes
	if err := checkNodeBudget(n, 0, &count); err == nil {
		t.Fatal("node budget")
	}
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, ok := parseTime(&yaml.Node{Kind: yaml.ScalarNode, Value: s})
	if !ok {
		t.Fatalf("parseTime(%q) failed", s)
	}
	return v
}
