package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/vcs"
)

func newStore(t *testing.T, visibility string) (*OKFStore, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bundle")
	b, err := okf.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	s := NewOKFStore(b, vcs.NewFake(), meta.NewFakeStore(), ACLDefaults{Visibility: visibility, Owner: "alice"})
	return s, root
}

func doc(desc string) *okf.Document {
	return &okf.Document{Type: "Concept", Description: desc, Body: "# Body\n"}
}

func TestPutCommitsBodyAndMetadata(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	r, err := s.Put(ctx, "tables/orders", doc("Orders."))
	if err != nil {
		t.Fatal(err)
	}
	if r.ContentHash == "" || r.CommitSHA == "" || r.Unchanged {
		t.Fatalf("put result = %+v", r)
	}
	m, err := s.Meta.GetConcept(ctx, "tables/orders")
	if err != nil || m.ContentHash != r.ContentHash || m.CommitSHA != r.CommitSHA {
		t.Fatalf("metadata mismatch: %+v (%v)", m, err)
	}
	history, _ := s.History(ctx, 10)
	if len(history) != 1 || !strings.Contains(history[0].Message, "tables/orders") {
		t.Fatalf("history = %+v", history)
	}
	show, err := s.Show(ctx, r.CommitSHA, "tables/orders")
	if err != nil || !strings.Contains(string(show), "type: Concept") {
		t.Fatalf("historical show = %q (%v)", show, err)
	}
}

func TestPutIsIdempotentByContentHash(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	first, _ := s.Put(ctx, "c", doc("Same."))
	second, err := s.Put(ctx, "c", doc("Same."))
	if err != nil || !second.Unchanged || second.ContentHash != first.ContentHash {
		t.Fatalf("second put = %+v (%v)", second, err)
	}
	history, _ := s.History(ctx, 10)
	if len(history) != 1 {
		t.Fatalf("duplicate write created a commit: %+v", history)
	}
	third, _ := s.Put(ctx, "c", doc("Changed."))
	if third.Unchanged || third.ContentHash == first.ContentHash {
		t.Fatalf("changed content not detected: %+v", third)
	}
	history, _ = s.History(ctx, 10)
	if len(history) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(history))
	}
	patch, err := s.Diff(ctx, "", "")
	if err != nil || !strings.Contains(patch, "c.md") {
		t.Fatalf("diff = %q (%v)", patch, err)
	}
}

func TestACLIsEnforcedOnRead(t *testing.T) {
	ctx := context.Background()
	pub, _ := newStore(t, "public")
	_, _ = pub.Put(ctx, "public/c", doc("Public."))
	if _, err := pub.Get(ctx, "anyone", "public/c"); err != nil {
		t.Fatalf("public read denied: %v", err)
	}
	priv, _ := newStore(t, "private")
	_, _ = priv.Put(ctx, "private/c", doc("Private."))
	if _, err := priv.Get(ctx, "bob", "private/c"); err == nil {
		t.Fatal("private read must be denied for non-owner")
	}
	if _, err := priv.Get(ctx, "alice", "private/c"); err != nil {
		t.Fatalf("owner read denied: %v", err)
	}
}

func TestDeleteMarksDeprecated(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	_, _ = s.Put(ctx, "c", doc("Body."))
	if err := s.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Meta.GetConcept(ctx, "c")
	if m.Status != "deprecated" {
		t.Fatalf("metadata should be deprecated, got %q", m.Status)
	}
	if _, err := s.Bundle.Get("c"); err == nil {
		t.Fatal("body should be gone")
	}
	// Re-put after delete must not be treated as unchanged.
	r, err := s.Put(ctx, "c", doc("Body."))
	if err != nil || r.Unchanged {
		t.Fatalf("re-put after delete = %+v (%v)", r, err)
	}
}

func TestFingerprintDetectsDrift(t *testing.T) {
	a := Fingerprint("pgvector", "embed-v1", 1536, "http://e")
	b := Fingerprint("pgvector", "embed-v2", 1536, "http://e")
	c := Fingerprint("pgvector", "embed-v1", 1536, "http://e")
	if a == b || a != c {
		t.Fatal("embedding fingerprint drift detection wrong")
	}
}

func TestValidateVisibility(t *testing.T) {
	for _, v := range []string{"private", "team", "public"} {
		if err := ValidateVisibility(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateVisibility("everyone"); err == nil {
		t.Fatal("invalid visibility must fail")
	}
}

func TestApplyACLDefaults(t *testing.T) {
	d := doc("x")
	if err := ApplyACLDefaults(d, ACLDefaults{Visibility: "team", Owner: "bob"}); err != nil {
		t.Fatal(err)
	}
	if d.Visibility() != "team" || d.Owner() != "bob" {
		t.Fatalf("defaults = %q %q", d.Visibility(), d.Owner())
	}
	// Existing values are never overwritten.
	_ = ApplyACLDefaults(d, ACLDefaults{Visibility: "public", Owner: "carol"})
	if d.Visibility() != "team" || d.Owner() != "bob" {
		t.Fatal("existing ACL values overwritten")
	}
}
