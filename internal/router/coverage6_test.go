package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// auditMeta injects failures into the audit/metadata writes of the port.
type auditMeta struct {
	meta.MetaStore
	appendErr error
	listErr   error
	nilList   bool
	upsertErr error
}

func (m auditMeta) AppendDecision(ctx context.Context, d meta.DecisionRecord) error {
	if m.appendErr != nil {
		return m.appendErr
	}
	return m.MetaStore.AppendDecision(ctx, d)
}

func (m auditMeta) ListDecisions(ctx context.Context, q meta.DecisionQuery) ([]meta.DecisionRecord, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if m.nilList {
		return nil, nil
	}
	return m.MetaStore.ListDecisions(ctx, q)
}

func (m auditMeta) UpsertConcept(ctx context.Context, c meta.ConceptMeta) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	return m.MetaStore.UpsertConcept(ctx, c)
}

func TestCompositeWithoutFallbackFailsClosed(t *testing.T) {
	c := &Composite{Rules: NewRuleEngine()}
	d, err := c.Decide(context.Background(), DecisionTask{
		Kind: "read", Text: "ambiguous",
		Eligible: []string{"okf", "memory"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleID != "fail_closed" || !strings.HasPrefix(d.Reason, "fail-closed: no fallback") {
		t.Fatalf("missing fallback must fail closed: %+v", d)
	}
}

func TestEvidenceListErrorAndNil(t *testing.T) {
	failing := NewEvidence(auditMeta{MetaStore: meta.NewFakeStore(), listErr: errors.New("query down")})
	if _, err := failing.List(context.Background(), "c", 5); err == nil {
		t.Fatal("list error must surface")
	}
	nilStore := NewEvidence(auditMeta{MetaStore: meta.NewFakeStore(), nilList: true})
	out, err := nilStore.List(context.Background(), "c", 5)
	if err != nil || out == nil || len(out) != 0 {
		t.Fatalf("nil records must normalise to an empty slice: %+v (%v)", out, err)
	}
}

func TestReadRouterCanonicalHitsListError(t *testing.T) {
	_, s, r := newWriteFixture(t)
	rr := NewReadRouter(s, r, nil)
	if err := os.RemoveAll(s.Bundle.Root); err != nil {
		t.Fatal(err)
	}
	if n := rr.canonicalHits("metrics"); n != 0 {
		t.Fatalf("unreadable bundle must yield no hits: %d", n)
	}
}

func TestReadRouterDecisionError(t *testing.T) {
	_, s, r := newWriteFixture(t)
	rr := NewReadRouter(s, r, errDecisioner{})
	if _, err := rr.Query(context.Background(), "alice", "q", 5); err == nil {
		t.Fatal("decision error must abort the query")
	}
}

func TestReadRouterEvidenceError(t *testing.T) {
	_, s, r := newWriteFixture(t)
	rr := NewReadRouter(s, r, &fixedDecisioner{d: RouteDecision{Targets: []string{CanonicalTarget}, RuleID: "t", Source: SourceRule}})
	rr.Evidence = NewEvidence(auditMeta{MetaStore: s.Meta, appendErr: errors.New("audit down")})
	if _, err := rr.Query(context.Background(), "alice", "q", 5); err == nil {
		t.Fatal("audit error must abort the query")
	}
}

func TestMergeHitsOrderingAndConflictSort(t *testing.T) {
	byTarget := map[string][]adapter.Hit{
		"okf": {
			{ConceptID: "b", Source: CanonicalTarget, Score: 0.9, Snippet: "canonical b"},
			{ConceptID: "a", Source: CanonicalTarget, Score: 0.9, Snippet: "canonical a"},
			{ConceptID: "e", Source: CanonicalTarget, Score: 0.8, Snippet: "canonical e"},
		},
		"memory": {
			{ConceptID: "a", Source: "memory", Score: 0.9, Snippet: "external a"},
			{ConceptID: "c", Source: "memory", Score: 0.9, Snippet: "external c"},
			{ConceptID: "e", Source: "memory", Score: 0.7, Snippet: "external e"},
			{ConceptID: "d", Source: "memory", Score: 0.1, Snippet: "external d"},
		},
	}
	out, conflicts := mergeHits(byTarget, 10)
	if len(out) != 5 {
		t.Fatalf("merged = %+v", out)
	}
	if out[0].Source != CanonicalTarget || out[0].ConceptID != "a" || out[1].ConceptID != "b" || out[2].ConceptID != "e" {
		t.Fatalf("canonical-first ordering broken: %+v", out)
	}
	if out[3].ConceptID != "c" || out[4].ConceptID != "d" || out[3].Source != "memory" {
		t.Fatalf("external tail ordering broken: %+v", out)
	}
	if len(conflicts) != 2 || conflicts[0].ConceptID != "a" || conflicts[1].ConceptID != "e" {
		t.Fatalf("conflicts not surfaced: %+v", conflicts)
	}
}

func TestRuleR6ExploratoryWithoutRAG(t *testing.T) {
	d, ok := NewRuleEngine().Decide(DecisionTask{
		Kind: "read", Features: Features{QueryKind: "exploratory"}, Eligible: []string{CanonicalTarget},
	})
	if !ok || d.RuleID != "R6_exploratory_query" || len(d.Targets) != 1 || d.Targets[0] != CanonicalTarget {
		t.Fatalf("R6 without RAG = %+v (ok=%v)", d, ok)
	}
}

func TestSystemOneReadBodyError(t *testing.T) {
	// A truncated body (Content-Length promises more than is sent) fails the read.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("hijack unsupported")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n{\"choice\":")
		_ = buf.Flush()
	}))
	defer srv.Close()
	d := NewSystemOneDecisioner(SystemOneConfig{Endpoint: srv.URL, AllowedHosts: []string{"127.0.0.1"}})
	if _, err := d.Decide(context.Background(), DecisionTask{Kind: "read", Text: "q", Eligible: []string{"okf", "memory"}}); err == nil {
		t.Fatal("truncated body must error")
	}
}

func TestWriteRouterRejectsForeignOwner(t *testing.T) {
	w, _, _ := newWriteFixture(t)
	doc := simpleDoc("Note", "body")
	doc.SetOwner("bob")
	if _, err := w.Put(context.Background(), "alice", "notes/owned", doc); err == nil {
		t.Fatal("owner mismatch must be rejected")
	}
}

func TestWriteRouterDecideError(t *testing.T) {
	w, _, _ := newWriteFixture(t)
	w.Decisioner = errDecisioner{}
	if _, err := w.Put(context.Background(), "alice", "notes/a", simpleDoc("Note", "b")); err == nil {
		t.Fatal("decision error must abort the write")
	}
}

func TestWriteRouterAuditError(t *testing.T) {
	w, s, _ := newWriteFixture(t)
	w.Evidence = NewEvidence(auditMeta{MetaStore: s.Meta, appendErr: errors.New("audit down")})
	if _, err := w.Put(context.Background(), "alice", "notes/a", simpleDoc("Note", "b")); err == nil {
		t.Fatal("audit error must abort the write")
	}
}

func TestWriteRouterCanonicalPutError(t *testing.T) {
	w, _, _ := newWriteFixture(t)
	w.Decisioner = &fixedDecisioner{d: RouteDecision{Targets: []string{CanonicalTarget}, RuleID: "t", Source: SourceRule}}
	// A document without a type cannot be marshalled to OKF.
	if _, err := w.Put(context.Background(), "alice", "notes/bad", &okf.Document{Body: "x"}); err == nil {
		t.Fatal("invalid canonical body must fail the write")
	}
}

func TestWriteRouterProjectionOnlyMarshalError(t *testing.T) {
	w, _, _ := newWriteFixture(t)
	w.Decisioner = &fixedDecisioner{d: RouteDecision{Targets: []string{"memory"}, RuleID: "t", Source: SourceRule}}
	if _, err := w.Put(context.Background(), "alice", "notes/bad", &okf.Document{Body: "x"}); err == nil {
		t.Fatal("invalid projection-only body must fail the write")
	}
}

func TestWriteRouterProjectionOnlyMetaError(t *testing.T) {
	w, s, _ := newWriteFixture(t)
	w.Store.Meta = auditMeta{MetaStore: s.Meta, upsertErr: errors.New("meta down")}
	w.Decisioner = &fixedDecisioner{d: RouteDecision{Targets: []string{"memory"}, RuleID: "t", Source: SourceRule}}
	if _, err := w.Put(context.Background(), "alice", "notes/a", simpleDoc("Note", "b")); err == nil {
		t.Fatal("metadata error must abort the projection-only write")
	}
}

func TestWriteRouterProjectionRecordError(t *testing.T) {
	w, s, _ := newWriteFixture(t)
	w.Store.Meta = migrationMeta{MetaStore: s.Meta, recordErr: errors.New("projection table down")}
	w.Decisioner = &fixedDecisioner{d: RouteDecision{Targets: []string{CanonicalTarget, "memory"}, RuleID: "t", Source: SourceRule}}
	res, err := w.Put(context.Background(), "alice", "notes/a", simpleDoc("Note", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Projections) != 1 || res.Projections[0].Status != "failed" || res.Projections[0].Error == "" {
		t.Fatalf("record failure must surface as a failed projection: %+v", res.Projections)
	}
}
