package router

import (
	"context"
	"testing"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/okf"
)

func seedReadFixture(t *testing.T) (*ReadRouter, *WriteRouter, *adapter.Registry) {
	t.Helper()
	w, s, r := newWriteFixture(t)
	if _, err := w.Put(context.Background(), "alice", "metrics/churn", simpleDoc("Metric", "churn definition")); err != nil {
		t.Fatal(err)
	}
	// Create a projection with a different snippet to exercise conflict marking.
	mem, _ := r.Get("memory")
	if err := mem.Put(context.Background(), "metrics/churn", simpleDoc("Metric", "churn definition")); err != nil {
		t.Fatal(err)
	}
	return NewReadRouter(s, r, NewComposite(NewRuleEngine(), NewStubDecisioner())), w, r
}

func TestDefinitionQueryUsesCanonicalOnly(t *testing.T) {
	q, _, _ := seedReadFixture(t)
	res, err := q.Query(context.Background(), "alice", "define churn", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Decision.Targets) != 1 || res.Decision.Targets[0] != CanonicalTarget || res.Decision.RuleID != "R3_definition_query" {
		t.Fatalf("decision = %+v", res.Decision)
	}
	if len(res.Hits) != 1 || res.Hits[0].Source != "okf" {
		t.Fatalf("hits = %+v", res.Hits)
	}
}

func TestExploratoryQueryUsesRAG(t *testing.T) {
	q, _, _ := seedReadFixture(t)
	res, err := q.Query(context.Background(), "alice", "similar examples of churn", 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.RuleID != "R6_exploratory_query" || contains(res.Decision.Targets, CanonicalTarget) {
		t.Fatalf("decision = %+v", res.Decision)
	}
	if len(res.Hits) == 0 || res.Hits[0].Source != "memory" {
		t.Fatalf("hits = %+v", res.Hits)
	}
}

func TestDualPathCanonicalWinsAndConflictIsSurfaced(t *testing.T) {
	q, _, _ := seedReadFixture(t)
	q.Decisioner = &fixedDecisioner{d: RouteDecision{
		Targets: []string{"memory", "okf"}, Reason: "dual", RuleID: "test", Confidence: 1, Source: SourceRule,
	}}
	res, err := q.Query(context.Background(), "alice", "churn", 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range res.Hits {
		if h.ConceptID == "metrics/churn" {
			found = true
			if h.Source != "okf" {
				t.Fatalf("canonical must win, got %+v", h)
			}
		}
	}
	if !found {
		t.Fatalf("concept missing from merged hits: %+v", res.Hits)
	}
	if len(res.Conflicts) == 0 || res.Conflicts[0].Source != "memory" {
		t.Fatalf("conflict not surfaced: %+v", res.Conflicts)
	}
}

func TestQueryFiltersByACL(t *testing.T) {
	w, s, r := newWriteFixture(t)
	private := simpleDoc("Note", "private body")
	private.SetVisibility("private")
	if _, err := w.Put(context.Background(), "alice", "private/secret", private); err != nil {
		t.Fatal(err)
	}
	mem, _ := r.Get("memory")
	_ = mem.Put(context.Background(), "private/secret", private)
	q := NewReadRouter(s, r, &fixedDecisioner{d: RouteDecision{
		Targets: []string{"memory"}, Reason: "test", RuleID: "test", Confidence: 1, Source: SourceRule,
	}})
	res, err := q.Query(context.Background(), "bob", "private", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		if h.ConceptID == "private/secret" {
			t.Fatal("private concept leaked through query results")
		}
	}
	res, err = q.Query(context.Background(), "alice", "private", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("owner should see the concept: %+v", res.Hits)
	}
}

func TestQueryTimeoutIsVisibleNotSilent(t *testing.T) {
	_, s, r := newWriteFixture(t)
	_ = r.RegisterAdapter(&slowAdapter{})
	q := NewReadRouter(s, r, &fixedDecisioner{d: RouteDecision{
		Targets: []string{"slow"}, Reason: "test", RuleID: "test", Confidence: 1, Source: SourceRule,
	}})
	q.QueryTimeout = 10 * time.Millisecond
	res, err := q.Query(context.Background(), "alice", "anything", 5)
	if err == nil && res.Errors["slow"] == "" {
		t.Fatalf("timeout must be reported: %+v (%v)", res, err)
	}
}

type slowAdapter struct{}

func (s *slowAdapter) Name() string                       { return "slow" }
func (s *slowAdapter) Capabilities() adapter.Capabilities { return adapter.Capabilities{Query: true} }
func (s *slowAdapter) Fingerprint() string                { return "slow-v1" }
func (s *slowAdapter) Health(context.Context) error       { return nil }
func (s *slowAdapter) Put(context.Context, string, *okf.Document) error {
	return adapter.ErrUnsupported
}
func (s *slowAdapter) Query(ctx context.Context, _ adapter.Query) ([]adapter.Hit, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return nil, nil
	}
}
func (s *slowAdapter) Delete(context.Context, string) error { return adapter.ErrUnsupported }
