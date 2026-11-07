package okf

import (
	"strings"
	"testing"
	"time"
)

const sample = `---
type: Metric
title: Monthly churn
description: Churn definition owned by Finance.
tags: [finance, churn]
verified: { by: human:alice, at: 2026-06-25T09:00:00Z }
status: stable
stale_after: 2026-12-31T00:00:00Z
generated: { by: reference_agent/gemini, at: 2026-06-20T22:53:05Z }
custom_key:
  nested: value
---
# Definition

See [orders](/tables/orders.md).
`

func TestParseBareVerifiedMapping(t *testing.T) {
	d, err := ParseDocument([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Verified) != 1 {
		t.Fatalf("bare verified mapping must become a one-element list, got %d", len(d.Verified))
	}
	if d.Verified[0].By != "human:alice" {
		t.Fatalf("unexpected verifier: %q", d.Verified[0].By)
	}
	if got := d.TrustTier(); got != TrustHumanReviewed {
		t.Fatalf("trust tier = %s", got)
	}
}

func TestParseVerifiedListAndMachineTier(t *testing.T) {
	d, err := ParseDocument([]byte("---\ntype: X\nverified:\n  - by: process:nightly\n  - by: agent/x\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Verified) != 2 || d.TrustTier() != TrustMachineConfirmed {
		t.Fatalf("verified=%d tier=%s", len(d.Verified), d.TrustTier())
	}
}

func TestUnknownKeysRoundTrip(t *testing.T) {
	d, err := ParseDocument([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Extra()["custom_key"] {
		t.Fatal("unknown frontmatter key lost")
	}
	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "nested: value") {
		t.Fatalf("unknown key not serialized:\n%s", out)
	}
	d2, err := ParseDocument(out)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Title != "Monthly churn" || d2.Description != d.Description || !d2.Extra()["custom_key"] {
		t.Fatal("round-trip lost fields")
	}
	if d2.Body != d.Body {
		t.Fatalf("body changed: %q vs %q", d2.Body, d.Body)
	}
}

func TestPermissiveConformance(t *testing.T) {
	// Missing optional fields, unknown type and unknown keys must not be rejected.
	d, err := ParseDocument([]byte("---\ntype: Whatever\nextra: 1\n---\ntext\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.EffectiveStatus() != StatusStable || d.TrustTier() != TrustUnverified {
		t.Fatal("defaults wrong")
	}
	// Only `type` is required.
	bad, err := ParseDocument([]byte("---\ntitle: no type\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("missing type must fail Validate")
	}
}

func TestInvalidFrontmatter(t *testing.T) {
	if _, err := ParseDocument([]byte("no frontmatter")); err == nil {
		t.Fatal("missing delimiter must fail")
	}
	if _, err := ParseDocument([]byte("---\ntype: [unterminated\n---\nbody")); err == nil {
		t.Fatal("invalid YAML must fail")
	}
}

func TestStaleness(t *testing.T) {
	d, _ := ParseDocument([]byte(sample))
	before, _ := time.Parse(time.RFC3339, "2026-11-01T00:00:00Z")
	after, _ := time.Parse(time.RFC3339, "2027-01-01T00:00:00Z")
	if d.IsStale(before) || !d.IsStale(after) {
		t.Fatal("stale_after comparison wrong")
	}
}

func TestSourcesAndActor(t *testing.T) {
	d, err := ParseDocument([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if d.Generated == nil || d.Generated.By != "reference_agent/gemini" {
		t.Fatalf("generated not parsed: %+v", d.Generated)
	}
	if len(d.Tags) != 2 {
		t.Fatalf("tags = %v", d.Tags)
	}
}

func TestAttestedContract(t *testing.T) {
	doc := "---\ntype: Attested Computation\nruntime: bigquery\nparameters:\n  - { name: year, type: integer, required: true }\ncomputation: refs/revenue.sql\nexecutor:\n  resource: skills/run-on-bq.md\n  receipt: [job_id, executed_sql]\nattester:\n  resource: attesters/sql_equality.py\n---\n# Computation\n"
	d, err := ParseDocument([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if d.Attested == nil || d.Attested.Runtime != "bigquery" || len(d.Attested.Parameters) != 1 {
		t.Fatalf("attested = %+v", d.Attested)
	}
	if !d.Attested.Parameters[0].Required || d.Attested.Executor.Resource != "skills/run-on-bq.md" {
		t.Fatalf("executor/params wrong: %+v", d.Attested)
	}
	if err := d.Attested.Validate(); err != nil {
		t.Fatal(err)
	}
	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "runtime: bigquery") || !strings.Contains(string(out), "sql_equality.py") {
		t.Fatalf("attested contract lost on marshal:\n%s", out)
	}
}

func TestVisibilityExtension(t *testing.T) {
	d, _ := ParseDocument([]byte("---\ntype: X\n---\nbody\n"))
	d.SetVisibility("team")
	d.SetOwner("alice")
	out, _ := d.Marshal()
	d2, err := ParseDocument(out)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Visibility() != "team" || d2.Owner() != "alice" {
		t.Fatalf("extensions lost: %q %q", d2.Visibility(), d2.Owner())
	}
}
