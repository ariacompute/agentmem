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

func TestPutAsBindsOwnerToTrustedSubject(t *testing.T) {
	s, _ := newStore(t, "private")
	ctx := context.Background()
	d := doc("Owned.")
	// The configured default owner is "alice"; a trusted write by "carol"
	// must bind the owner to carol, not to the static default.
	if _, err := s.PutAs(ctx, "carol", "owned/c", d); err != nil {
		t.Fatal(err)
	}
	if d.Owner() != "carol" || d.Visibility() != "private" {
		t.Fatalf("ACL defaults = owner %q visibility %q", d.Owner(), d.Visibility())
	}
	if _, err := s.Get(ctx, "bob", "owned/c"); err == nil {
		t.Fatal("private body readable by a non-owner")
	}
	if _, err := s.Get(ctx, "carol", "owned/c"); err != nil {
		t.Fatalf("owner read denied: %v", err)
	}
}

func TestPutAsRejectsAnonymousAndForeignOwner(t *testing.T) {
	s, _ := newStore(t, "public")
	ctx := context.Background()
	if _, err := s.PutAs(ctx, "", "anon/c", doc("Body.")); err == nil {
		t.Fatal("anonymous write must be rejected")
	}
	foreign := doc("x")
	foreign.SetOwner("bob")
	if _, err := s.PutAs(ctx, "alice", "foreign/c", foreign); err == nil {
		t.Fatal("a writer must not mint a document owned by someone else")
	}
}

func TestPutAsRejectsOverwritingAnotherOwnersBody(t *testing.T) {
	s, _ := newStore(t, "private")
	ctx := context.Background()
	if _, err := s.PutAs(ctx, "alice", "private/c", doc("Original.")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutAs(ctx, "bob", "private/c", doc("Tampered.")); err == nil {
		t.Fatal("bob must not overwrite alice's private body")
	}
	got, err := s.Get(ctx, "alice", "private/c")
	if err != nil || !strings.Contains(got.Description, "Original") {
		t.Fatalf("body was tampered with: %+v (%v)", got, err)
	}
	if _, err := s.PutAs(ctx, "alice", "private/c", doc("Updated.")); err != nil {
		t.Fatalf("owner must be able to update: %v", err)
	}
}

func TestDeleteAsEnforcesOwnership(t *testing.T) {
	s, _ := newStore(t, "private")
	ctx := context.Background()
	if _, err := s.PutAs(ctx, "alice", "private/c", doc("Body.")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAs(ctx, "", "private/c"); err == nil {
		t.Fatal("anonymous delete must be rejected")
	}
	if err := s.DeleteAs(ctx, "bob", "private/c"); err == nil {
		t.Fatal("non-owner delete must be rejected")
	}
	if err := s.DeleteAs(ctx, "alice", "private/c"); err != nil {
		t.Fatalf("owner delete denied: %v", err)
	}
}

func TestTrustedACLRejectsInvalidVisibility(t *testing.T) {
	d := doc("x")
	d.SetVisibility("everyone")
	if err := TrustedACL(d, ACLDefaults{}, "alice"); err == nil {
		t.Fatal("invalid visibility must fail closed")
	}
	owned := doc("x")
	owned.SetOwner("alice")
	if err := AuthorizeUpdate(owned, "bob"); err == nil {
		t.Fatal("foreign update must fail closed")
	}
	if err := AuthorizeUpdate(owned, "alice"); err != nil {
		t.Fatalf("owner update denied: %v", err)
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
