package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/okf"
)

// brokenAdapter fails with text that must never reach a client: driver errors
// can embed credentials, addresses and SQL.
type brokenAdapter struct{ name string }

func (b brokenAdapter) Name() string { return b.name }

func (b brokenAdapter) Capabilities() adapter.Capabilities {
	return adapter.Capabilities{Put: true, Query: true, Delete: true}
}

func (b brokenAdapter) Put(context.Context, string, *okf.Document) error {
	return errors.New(`driver: pq: password=hunter2 host=10.1.2.3`)
}

func (b brokenAdapter) Query(context.Context, adapter.Query) ([]adapter.Hit, error) {
	return nil, errors.New(`driver: pq: password=hunter2 host=10.1.2.3`)
}

func (b brokenAdapter) Delete(context.Context, string) error { return nil }

func (b brokenAdapter) Fingerprint() string { return "broken/v1" }

func (b brokenAdapter) Health(context.Context) error { return nil }

func TestRedactErrorDropsTheUnderlyingText(t *testing.T) {
	if got := redactError("channel failed", errors.New("dsn=postgres://user:secret@db")); got != "channel failed" {
		t.Fatalf("redactError = %q", got)
	}
	if got := redactError("channel failed", nil); got != "" {
		t.Fatalf("nil error should stay empty, got %q", got)
	}
}

func TestQueryChannelErrorsAreRedacted(t *testing.T) {
	_, s, r := newWriteFixture(t)
	if err := r.RegisterAdapter(brokenAdapter{name: "broken"}); err != nil {
		t.Fatal(err)
	}
	q := NewReadRouter(s, r, &fixedDecisioner{d: RouteDecision{
		Targets: []string{"broken"}, Reason: "test", RuleID: "test", Confidence: 1, Source: SourceRule,
	}})
	res, err := q.Query(context.Background(), "alice", "churn", 5)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := res.Errors["broken"]
	if !ok {
		t.Fatalf("missing channel error: %+v", res.Errors)
	}
	if got != "channel failed" {
		t.Fatalf("channel error leaked internals: %q", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Fatalf("credential reached the response: %q", got)
	}
}

func TestProjectionErrorsAreRedacted(t *testing.T) {
	w, _, r := newWriteFixture(t)
	if err := r.RegisterAdapter(brokenAdapter{name: "broken"}); err != nil {
		t.Fatal(err)
	}
	w.Decisioner = &fixedDecisioner{d: RouteDecision{
		Targets: []string{"okf", "broken"}, Reason: "test", RuleID: "test", Confidence: 1, Source: SourceRule,
	}}
	res, err := w.Put(context.Background(), "alice", "metrics/redact", simpleDoc("Metric", "churn definition"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Projections) != 1 {
		t.Fatalf("projections = %+v", res.Projections)
	}
	if got := res.Projections[0].Error; got != "projection failed" {
		t.Fatalf("projection error leaked internals: %q", got)
	}
}
