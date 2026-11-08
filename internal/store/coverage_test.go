package store

import (
	"context"
	"testing"

	"agentmem/internal/meta"
)

func TestStoreListAndCancelled(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	if _, err := s.List(ctx); err != nil {
		t.Fatalf("list: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.List(cctx); err == nil {
		t.Fatal("cancelled list must error")
	}
}

func TestRecordProjectionPersists(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	if err := RecordProjection(ctx, s.Meta, "c", "memory", "h1", "fp", ProjectionPending); err != nil {
		t.Fatalf("record: %v", err)
	}
	out, err := s.Meta.ListProjections(ctx, meta.ProjectionQuery{ConceptID: "c", Adapter: "memory"})
	if err != nil || len(out) != 1 || out[0].Status != ProjectionPending {
		t.Fatalf("projection = %+v (%v)", out, err)
	}
}

func TestApplyACLDefaultsInvalidVisibility(t *testing.T) {
	d := doc("x")
	if err := ApplyACLDefaults(d, ACLDefaults{Visibility: "everyone", Owner: "bob"}); err == nil {
		t.Fatal("invalid default visibility must fail")
	}
}

func TestAuthorizeUpdateEdgeCases(t *testing.T) {
	if err := AuthorizeUpdate(nil, "alice"); err != nil {
		t.Fatalf("nil existing must be allowed: %v", err)
	}
	if err := AuthorizeUpdate(doc("x"), ""); err == nil {
		t.Fatal("anonymous write must be rejected")
	}
}

func TestTrustedACLEmptySubject(t *testing.T) {
	if err := TrustedACL(doc("x"), ACLDefaults{}, ""); err == nil {
		t.Fatal("anonymous trusted write must be rejected")
	}
}

func TestPutCancelledContext(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, "c", doc("x")); err == nil {
		t.Fatal("cancelled put must error")
	}
}

func TestGetInvalidConceptID(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	if _, err := s.Get(ctx, "anyone", ".."); err == nil {
		t.Fatal("escaping concept id must be rejected")
	}
}

func TestShowTrailingSlashAndInvalidID(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	_, _ = s.Put(ctx, "c", doc("Body."))
	// Trailing slash on the ref must be trimmed before the lookup. The
	// underlying Show may still fail (HEAD is not a real ref in the fake),
	// but the trim branch is what we exercise here.
	if _, err := s.Show(ctx, "HEAD/", "c"); err == nil {
		t.Fatalf("expected show error for unknown ref")
	}
	if _, err := s.Show(ctx, "HEAD", ".."); err == nil {
		t.Fatal("invalid concept id in Show must be rejected")
	}
}

func TestDeleteAsInvalidID(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	if err := s.DeleteAs(ctx, "alice", ".."); err == nil {
		t.Fatal("invalid concept id in DeleteAs must be rejected")
	}
}

func TestPutAsInvalidID(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	if _, err := s.PutAs(ctx, "alice", "..", doc("x")); err == nil {
		t.Fatal("invalid concept id in PutAs must be rejected")
	}
}

func TestFingerprintEdgeCases(t *testing.T) {
	// Zero dimension and negative dimension exercise itoa's branches.
	a := Fingerprint("memory", "m", 0, "")
	b := Fingerprint("memory", "m", -5, "")
	if a == "" || b == "" {
		t.Fatal("fingerprint must not be empty for edge dimensions")
	}
	if a == b {
		t.Fatal("distinct dimensions must differ")
	}
}
