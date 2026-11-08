package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

func TestEvidenceNilStore(t *testing.T) {
	var e *Evidence
	if err := e.Record(context.Background(), DecisionTask{}, RouteDecision{}); err != nil {
		t.Fatalf("nil record = %v", err)
	}
	if out, err := e.List(context.Background(), "c", 5); err != nil || len(out) != 0 {
		t.Fatalf("nil list = %+v (%v)", out, err)
	}
	empty := &Evidence{}
	if err := empty.Record(context.Background(), DecisionTask{}, RouteDecision{}); err != nil {
		t.Fatal(err)
	}
	if out, _ := empty.List(context.Background(), "c", 5); len(out) != 0 {
		t.Fatalf("empty list = %+v", out)
	}
}

func TestEvidenceRecordStoreError(t *testing.T) {
	e := NewEvidence(meta.NewFakeStore())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Record(ctx, DecisionTask{ConceptID: "c"}, RouteDecision{RuleID: "r"}); err == nil {
		t.Fatal("cancelled store must error")
	}
}

func TestExtractWriteFeaturesBranches(t *testing.T) {
	if f := ExtractWriteFeatures(nil, true); f.QueryKind != "write" || !f.HasCanonical {
		t.Fatalf("nil doc = %+v", f)
	}
	auth := ExtractWriteFeatures(&okf.Document{Type: "Metric", Body: "x"}, false)
	if !auth.Authoritative {
		t.Fatal("metric must be authoritative")
	}
	sens := ExtractWriteFeatures(&okf.Document{Type: "Note", Status: "deprecated"}, false)
	if !sens.Sensitive {
		t.Fatal("deprecated must be sensitive")
	}
	structured := ExtractWriteFeatures(&okf.Document{Type: "Note", Body: "a | --- | b"}, false)
	if !structured.Structured {
		t.Fatal("table body must be structured")
	}
}

func TestExtractQueryFeaturesKinds(t *testing.T) {
	if ExtractQueryFeatures("define revenue", 0).QueryKind != "definition" {
		t.Fatal("definition")
	}
	if ExtractQueryFeatures("similar examples", 0).QueryKind != "exploratory" {
		t.Fatal("exploratory")
	}
	if ExtractQueryFeatures("compare across all documents", 0).QueryKind != "aggregate" {
		t.Fatal("aggregate")
	}
	if ExtractQueryFeatures("hello", 0).QueryKind != "general" {
		t.Fatal("general")
	}
}

func TestDigestBranches(t *testing.T) {
	if Digest(nil) != "" {
		t.Fatal("nil digest")
	}
	long := &okf.Document{Type: "T", Title: strings.Repeat("a", 250), Description: "d"}
	if len(Digest(long)) != 200 {
		t.Fatalf("truncated digest len = %d", len(Digest(long)))
	}
}

func TestNewRuleEngineBranches(t *testing.T) {
	if len(NewRuleEngine().RuleIDs()) != 7 {
		t.Fatal("default rules")
	}
	if len(NewRuleEngine([]Rule{}).RuleIDs()) != 7 {
		t.Fatal("empty group must default")
	}
	custom := NewRuleEngine([]Rule{{ID: "only", Match: func(DecisionTask) (RouteDecision, bool) {
		return RouteDecision{}, false
	}}})
	if ids := custom.RuleIDs(); len(ids) != 1 || ids[0] != "only" {
		t.Fatalf("custom ids = %v", ids)
	}
	if _, ok := custom.Decide(DecisionTask{}); ok {
		t.Fatal("no rule should match")
	}
}

func TestStubDecisionerBranches(t *testing.T) {
	s := NewStubDecisioner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Decide(ctx, DecisionTask{}); err == nil {
		t.Fatal("cancelled ctx must error")
	}
	// Read with a non-canonical eligible target routes to it.
	d, _ := s.Decide(context.Background(), DecisionTask{Kind: "read", Features: Features{QueryKind: "general"}, Eligible: []string{"okf", "memory"}})
	if len(d.Targets) != 1 || d.Targets[0] != "memory" {
		t.Fatalf("read rag = %+v", d)
	}
	// Read with only canonical eligible falls back to canonical.
	d, _ = s.Decide(context.Background(), DecisionTask{Kind: "read", Features: Features{QueryKind: "general"}, Eligible: []string{"okf"}})
	if d.Targets[0] != CanonicalTarget {
		t.Fatalf("read canonical = %+v", d)
	}
	// Write with a rag target produces canonical + rag (or canonical only).
	for _, text := range []string{"a", "b", "c", "d"} {
		d, _ = s.Decide(context.Background(), DecisionTask{Kind: "write", Text: text, Eligible: []string{"okf", "memory"}})
		if d.Targets[0] != CanonicalTarget {
			t.Fatalf("write must lead canonical: %+v", d.Targets)
		}
	}
}

func TestDecisionerHelpers(t *testing.T) {
	if got := pruneEligible([]string{"", " b ", "a", "a"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("prune = %v", got)
	}
	if !contains([]string{"a", "b"}, "b") || contains([]string{"a"}, "z") {
		t.Fatal("contains")
	}
	if firstNonCanonical([]string{"okf"}) != "" || firstNonCanonical([]string{"okf", "m"}) != "m" {
		t.Fatal("firstNonCanonical")
	}
	if got := nonCanonical([]string{"okf", "m"}); len(got) != 1 || got[0] != "m" {
		t.Fatalf("nonCanonical = %v", got)
	}
	if err := validateTarget("m", []string{"okf"}); !errors.Is(err, ErrNoEligibleTargets) {
		t.Fatalf("validateTarget = %v", err)
	}
	if err := validateTarget("okf", []string{"okf"}); err != nil {
		t.Fatal(err)
	}
}

func TestSanitizeDecisionBranches(t *testing.T) {
	// Empty targets fail closed.
	d := sanitizeDecision(RouteDecision{Targets: []string{"ghost"}}, []string{"okf"})
	if !strings.HasPrefix(d.Reason, "fail-closed") {
		t.Fatalf("empty = %+v", d)
	}
	// Empty source defaults to rule; canonical leads.
	d = sanitizeDecision(RouteDecision{Targets: []string{"m", CanonicalTarget}}, []string{"okf", "m"})
	if d.Source != SourceRule || d.Targets[0] != CanonicalTarget {
		t.Fatalf("sanitize = %+v", d)
	}
}

func TestMergeHitsConflictAndOrdering(t *testing.T) {
	byTarget := map[string][]adapter.Hit{
		CanonicalTarget: {{ConceptID: "c", Source: CanonicalTarget, Score: 0.9, Snippet: "canonical"}},
		"memory":        {{ConceptID: "c", Source: "memory", Score: 0.5, Snippet: "other"}},
	}
	out, conflicts := mergeHits(byTarget, 5)
	if len(out) != 1 || out[0].Snippet != "canonical" {
		t.Fatalf("merged = %+v", out)
	}
	if len(conflicts) != 1 || conflicts[0].Canonical != "canonical" || conflicts[0].External != "other" {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	// Score tie breaks by concept id, and topK truncates.
	byTarget = map[string][]adapter.Hit{
		"memory": {{ConceptID: "b", Source: "memory", Score: 0.5}, {ConceptID: "a", Source: "memory", Score: 0.5}},
	}
	out, _ = mergeHits(byTarget, 1)
	if len(out) != 1 || out[0].ConceptID != "a" {
		t.Fatalf("tie/truncate = %+v", out)
	}
}

func TestNewReadRouterDefaults(t *testing.T) {
	w, s, r := newWriteFixture(t)
	_ = w
	rr := NewReadRouter(s, r, nil)
	if rr.Decisioner == nil {
		t.Fatal("nil decisioner must default")
	}
	// Empty registry → canonical only.
	empty := NewReadRouter(s, adapter.NewRegistry(), nil)
	if got := empty.eligibleTargets(); len(got) != 1 || got[0] != CanonicalTarget {
		t.Fatalf("eligible = %v", got)
	}
}

func TestReadRouterQueryBranches(t *testing.T) {
	_, s, r := newWriteFixture(t)
	ctx := context.Background()
	if _, err := NewReadRouter(s, r, nil).Query(func() context.Context {
		c, cancel := context.WithCancel(context.Background())
		cancel()
		return c
	}(), "alice", "x", 5); err == nil {
		t.Fatal("cancelled query must error")
	}

	// A decision naming an unregistered target must be reported, not panic.
	rr := NewReadRouter(s, r, &fixedDecisioner{d: RouteDecision{
		Targets: []string{CanonicalTarget, "ghost"}, RuleID: "t", Source: SourceRule, Confidence: 1,
	}})
	res, err := rr.Query(ctx, "alice", "orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Errors["ghost"] != "adapter not registered" {
		t.Fatalf("errors = %+v", res.Errors)
	}
}

func TestWriteRouterPutEdgeCases(t *testing.T) {
	w, _, _ := newWriteFixture(t)
	ctx := context.Background()
	if _, err := w.Put(func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }(), "alice", "c", simpleDoc("Note", "b")); err == nil {
		t.Fatal("cancelled put")
	}
	if _, err := w.Put(ctx, "alice", "c", nil); err == nil {
		t.Fatal("nil doc")
	}
	if _, err := w.Put(ctx, "  ", "c", simpleDoc("Note", "b")); err == nil {
		t.Fatal("anonymous write")
	}
	if _, err := w.Put(ctx, "alice", "../escape", simpleDoc("Note", "b")); err == nil {
		t.Fatal("invalid id")
	}
	// Unknown projection target is recorded as failed.
	w.Decisioner = &fixedDecisioner{d: RouteDecision{Targets: []string{CanonicalTarget, "ghost"}, RuleID: "t", Source: SourceRule}}
	res, err := w.Put(ctx, "alice", "notes/g", simpleDoc("Note", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Projections) != 1 || res.Projections[0].Status != "failed" {
		t.Fatalf("ghost projection = %+v", res.Projections)
	}
}
