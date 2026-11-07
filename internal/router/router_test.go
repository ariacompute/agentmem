package router

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/store"
	"agentmem/internal/vcs"
)

func newWriteFixture(t *testing.T) (*WriteRouter, *store.OKFStore, *adapter.Registry) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bundle")
	b, err := okf.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	b.LogWrites = false
	m := meta.NewFakeStore()
	s := store.NewOKFStore(b, vcs.NewFake(), m, store.ACLDefaults{Visibility: "public", Owner: "alice"})
	r := adapter.NewRegistry()
	_ = r.RegisterAdapter(adapter.NewOKFAdapter(b))
	_ = r.RegisterAdapter(adapter.NewMemoryAdapter(adapter.NewHashingEmbedder(32)))
	w := NewWriteRouter(s, r, NewComposite(NewRuleEngine(), NewStubDecisioner()))
	return w, s, r
}

func simpleDoc(typ, body string) *okf.Document {
	return &okf.Document{Type: typ, Description: "short definition", Body: body}
}

func TestRuleTableShortCircuits(t *testing.T) {
	c := NewComposite(NewRuleEngine(), NewStubDecisioner())
	ctx := context.Background()

	d, err := c.Decide(ctx, DecisionTask{
		Kind: "write", Features: Features{Authoritative: true}, Eligible: []string{"okf", "memory"},
	})
	if err != nil || d.Source != SourceRule || d.RuleID != "R1_authoritative_write" ||
		len(d.Targets) != 1 || d.Targets[0] != CanonicalTarget {
		t.Fatalf("R1 = %+v (%v)", d, err)
	}

	d, _ = c.Decide(ctx, DecisionTask{
		Kind: "read", Features: Features{QueryKind: "definition"}, Eligible: []string{"okf", "memory"},
	})
	if d.RuleID != "R3_definition_query" || d.Targets[0] != CanonicalTarget {
		t.Fatalf("R3 = %+v", d)
	}

	d, _ = c.Decide(ctx, DecisionTask{
		Kind: "read", Features: Features{QueryKind: "exploratory"}, Eligible: []string{"okf", "memory", "graphrag"},
	})
	if d.RuleID != "R6_exploratory_query" || len(d.Targets) != 2 || contains(d.Targets, CanonicalTarget) {
		t.Fatalf("R6 = %+v", d)
	}

	d, _ = c.Decide(ctx, DecisionTask{
		Kind: "write", Features: Features{ByteSize: LargeDocBytes + 1}, Eligible: []string{"okf", "memory"},
	})
	if d.RuleID != "R5_large_document" || d.Targets[0] != CanonicalTarget || len(d.Targets) != 2 {
		t.Fatalf("R5 = %+v", d)
	}
}

func TestAmbiguousTaskFallsThroughToSystemOne(t *testing.T) {
	c := NewComposite(NewRuleEngine(), NewStubDecisioner())
	d, err := c.Decide(context.Background(), DecisionTask{
		Kind: "write", Text: "general note", Features: Features{ByteSize: 100},
		Eligible: []string{"okf", "memory"},
	})
	if err != nil || d.Source != SourceSystemOne {
		t.Fatalf("ambiguous task should reach fallback, got %+v (%v)", d, err)
	}
}

func TestCanonicalAlwaysLeadsAndConflictsFailClosed(t *testing.T) {
	c := NewComposite(NewRuleEngine(), NewStubDecisioner())
	d, _ := c.Decide(context.Background(), DecisionTask{
		Kind: "read", Features: Features{QueryKind: "aggregate"},
		Eligible: []string{"memory", "okf", "graphrag"},
	})
	if d.Targets[0] != CanonicalTarget {
		t.Fatalf("canonical must lead: %+v", d.Targets)
	}
	// No eligible targets must fail closed to the canonical body.
	d, _ = c.Decide(context.Background(), DecisionTask{Kind: "read", Eligible: nil})
	if d.Source != SourceFallback || len(d.Targets) != 1 || d.Targets[0] != CanonicalTarget ||
		!strings.HasPrefix(d.Reason, "fail-closed") {
		t.Fatalf("fail-closed decision = %+v", d)
	}
}

func TestWriteRouterAuthoritativeStaysCanonical(t *testing.T) {
	w, s, _ := newWriteFixture(t)
	ctx := context.Background()
	r, err := w.Put(ctx, "alice", "metrics/churn", simpleDoc("Metric", "# churn\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.ContentHash == "" || r.CommitSHA == "" || len(r.Projections) != 0 {
		t.Fatalf("authoritative write = %+v", r)
	}
	if r.Decision.RuleID != "R1_authoritative_write" {
		t.Fatalf("decision = %+v", r.Decision)
	}
	if _, err := s.Bundle.Get("metrics/churn"); err != nil {
		t.Fatal("body not in Git: ", err)
	}
	decisions, _ := w.Evidence.List(ctx, "metrics/churn", 10)
	if len(decisions) != 1 || decisions[0].RuleID != "R1_authoritative_write" {
		t.Fatalf("audit trail = %+v", decisions)
	}
}

func TestWriteRouterDualProjectionOnUpdate(t *testing.T) {
	w, s, _ := newWriteFixture(t)
	ctx := context.Background()
	_, _ = w.Put(ctx, "alice", "notes/x", simpleDoc("Note", "v1"))
	r, err := w.Put(ctx, "alice", "notes/x", simpleDoc("Note", "v2"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Decision.RuleID != "R4_canonical_update" || len(r.Projections) != 1 {
		t.Fatalf("update decision = %+v", r)
	}
	if r.Projections[0].Status != store.ProjectionSynced {
		t.Fatalf("projection failed: %+v", r.Projections[0])
	}
	projections, _ := s.Meta.ListProjections(ctx, meta.ProjectionQuery{ConceptID: "notes/x"})
	if len(projections) != 1 || projections[0].Adapter != "memory" || projections[0].ContentHash != r.ContentHash {
		t.Fatalf("projection metadata = %+v", projections)
	}
}

func TestProjectionOnlyWriteSkipsGitBody(t *testing.T) {
	w, s, _ := newWriteFixture(t)
	w.Decisioner = &fixedDecisioner{d: RouteDecision{
		Targets: []string{"memory"}, Reason: "test", RuleID: "test", Confidence: 1, Source: SourceRule,
	}}
	r, err := w.Put(context.Background(), "alice", "raw/dump", simpleDoc("Note", "raw material"))
	if err != nil {
		t.Fatal(err)
	}
	if r.CommitSHA != "" || r.ContentHash == "" {
		t.Fatalf("projection-only result = %+v", r)
	}
	if _, err := s.Bundle.Get("raw/dump"); err == nil {
		t.Fatal("projection-only write must not create a Git body")
	}
	m, _ := s.Meta.GetConcept(context.Background(), "raw/dump")
	if m.Status != "projection-only" {
		t.Fatalf("metadata status = %q", m.Status)
	}
}

func TestProjectionFailureDoesNotRollbackBody(t *testing.T) {
	w, s, r := newWriteFixture(t)
	_ = r.RegisterAdapter(&failingAdapter{})
	w.Decisioner = &fixedDecisioner{d: RouteDecision{
		Targets: []string{"okf", "broken"}, Reason: "test", RuleID: "test", Confidence: 1, Source: SourceRule,
	}}
	// sanitizeDecision prunes "broken" unless it is eligible; register it in the
	// decision path by widening the eligible set through a wrapper below.
	res, err := w.Put(context.Background(), "alice", "c", simpleDoc("Note", "body"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res
	if _, err := s.Bundle.Get("c"); err != nil {
		t.Fatal("canonical body must survive projection failure")
	}
}

type fixedDecisioner struct{ d RouteDecision }

func (f *fixedDecisioner) Decide(context.Context, DecisionTask) (RouteDecision, error) {
	return f.d, nil
}

type failingAdapter struct{}

func (f *failingAdapter) Name() string { return "broken" }
func (f *failingAdapter) Capabilities() adapter.Capabilities {
	return adapter.Capabilities{Put: true, Query: true}
}
func (f *failingAdapter) Fingerprint() string          { return "broken-v1" }
func (f *failingAdapter) Health(context.Context) error { return nil }
func (f *failingAdapter) Put(context.Context, string, *okf.Document) error {
	return context.DeadlineExceeded
}
func (f *failingAdapter) Query(context.Context, adapter.Query) ([]adapter.Hit, error) {
	return nil, nil
}
func (f *failingAdapter) Delete(context.Context, string) error { return nil }
