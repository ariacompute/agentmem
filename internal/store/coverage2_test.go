package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/vcs"
)

func storeFixture2(t *testing.T) (*OKFStore, *okf.Bundle) {
	t.Helper()
	b, err := okf.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewOKFStore(b, vcs.NewFake(), meta.NewFakeStore(), ACLDefaults{Visibility: "public", Owner: "alice"})
	return s, b
}

type errRepo struct{}

func (errRepo) Commit(context.Context, string, map[string][]byte) (string, error) {
	return "", errors.New("commit boom")
}
func (errRepo) Head(context.Context) (string, error)          { return "", nil }
func (errRepo) Log(context.Context, int) ([]vcs.Entry, error) { return nil, nil }
func (errRepo) Show(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("show boom")
}
func (errRepo) Diff(context.Context, string, string) (string, error) { return "", nil }

func TestStoreCov2ApplyACLDefaults(t *testing.T) {
	d := &okf.Document{Type: "T"}
	if err := ApplyACLDefaults(d, ACLDefaults{}); err != nil {
		t.Fatal(err)
	}
	if d.Visibility() != "private" || d.Owner() != "system" {
		t.Fatalf("defaults = %q/%q", d.Visibility(), d.Owner())
	}
	if err := ApplyACLDefaults(&okf.Document{Type: "T"}, ACLDefaults{Visibility: "weird"}); err == nil {
		t.Fatal("invalid default visibility must error")
	}
	bad := &okf.Document{Type: "T"}
	bad.SetVisibility("weird")
	if err := ApplyACLDefaults(bad, ACLDefaults{}); err == nil {
		t.Fatal("invalid existing visibility must error")
	}
}

func TestStoreCov2TrustedACL(t *testing.T) {
	if err := TrustedACL(&okf.Document{Type: "T"}, ACLDefaults{}, "  "); err == nil {
		t.Fatal("empty subject")
	}
	bad := &okf.Document{Type: "T"}
	bad.SetVisibility("weird")
	if err := TrustedACL(bad, ACLDefaults{}, "alice"); err == nil {
		t.Fatal("invalid visibility")
	}
	mismatch := &okf.Document{Type: "T"}
	mismatch.SetOwner("bob")
	if err := TrustedACL(mismatch, ACLDefaults{}, "alice"); err == nil {
		t.Fatal("owner mismatch")
	}
	ok := &okf.Document{Type: "T"}
	ok.SetOwner("alice")
	if err := TrustedACL(ok, ACLDefaults{}, "alice"); err != nil {
		t.Fatal(err)
	}
	fresh := &okf.Document{Type: "T"}
	if err := TrustedACL(fresh, ACLDefaults{Visibility: "team"}, "alice"); err != nil {
		t.Fatal(err)
	}
	if fresh.Owner() != "alice" {
		t.Fatalf("owner = %q", fresh.Owner())
	}
}

func TestStoreCov2AuthorizeUpdate(t *testing.T) {
	if err := AuthorizeUpdate(nil, "alice"); err != nil {
		t.Fatal("nil existing")
	}
	if err := AuthorizeUpdate(&okf.Document{Type: "T"}, ""); err == nil {
		t.Fatal("anonymous")
	}
	owned := &okf.Document{Type: "T"}
	owned.SetOwner("alice")
	if err := AuthorizeUpdate(owned, "bob"); err == nil {
		t.Fatal("owner mismatch")
	}
	if err := AuthorizeUpdate(owned, "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCov2PutBranches(t *testing.T) {
	s, _ := storeFixture2(t)
	ctx := context.Background()
	cancel, done := context.WithCancel(context.Background())
	done()
	if _, err := s.Put(cancel, "x", &okf.Document{Type: "T"}); err == nil {
		t.Fatal("cancelled put")
	}
	if _, err := s.Put(ctx, "x", &okf.Document{}); err == nil {
		t.Fatal("marshal error (missing type)")
	}
	if _, err := s.Put(ctx, "../x", &okf.Document{Type: "T"}); err == nil {
		t.Fatal("invalid id")
	}
	doc := &okf.Document{Type: "T", Body: "b"}
	if _, err := s.Put(ctx, "concepts/x", doc); err != nil {
		t.Fatal(err)
	}
	r2, err := s.Put(ctx, "concepts/x", &okf.Document{Type: "T", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Unchanged {
		t.Fatalf("second put should be unchanged: %+v", r2)
	}
}

func TestStoreCov2PutCommitError(t *testing.T) {
	b, _ := okf.Open(t.TempDir())
	s := NewOKFStore(b, errRepo{}, meta.NewFakeStore(), ACLDefaults{Visibility: "public", Owner: "alice"})
	if _, err := s.Put(context.Background(), "concepts/x", &okf.Document{Type: "T", Body: "b"}); err == nil {
		t.Fatal("commit error must propagate")
	}
}

func TestStoreCov2DeleteBranches(t *testing.T) {
	s, b := storeFixture2(t)
	ctx := context.Background()
	if err := s.Delete(ctx, ""); err == nil {
		t.Fatal("invalid id")
	}
	if err := s.Delete(ctx, "missing"); err == nil {
		t.Fatal("missing concept")
	}
	// Body in bundle but not in metadata: delete succeeds without a metadata row.
	if _, err := b.Put("concepts/z", &okf.Document{Type: "T", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "concepts/z"); err != nil {
		t.Fatal(err)
	}

	b2, _ := okf.Open(t.TempDir())
	s2 := NewOKFStore(b2, errRepo{}, meta.NewFakeStore(), ACLDefaults{Visibility: "public", Owner: "alice"})
	if _, err := b2.Put("concepts/y", &okf.Document{Type: "T", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete(ctx, "concepts/y"); err == nil {
		t.Fatal("delete commit error must propagate")
	}
}

func TestStoreCov2DeleteAsBranches(t *testing.T) {
	s, _ := storeFixture2(t)
	ctx := context.Background()
	if err := s.DeleteAs(ctx, "", "x"); err == nil {
		t.Fatal("anonymous delete")
	}
	if err := s.DeleteAs(ctx, "alice", ""); err == nil {
		t.Fatal("invalid id")
	}
	if err := s.DeleteAs(ctx, "alice", "missing"); err == nil {
		t.Fatal("missing concept")
	}
	if _, err := s.PutAs(ctx, "alice", "concepts/d", &okf.Document{Type: "T", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAs(ctx, "bob", "concepts/d"); err == nil {
		t.Fatal("wrong owner delete must fail")
	}
	if err := s.DeleteAs(ctx, "alice", "concepts/d"); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCov2GetDeniedAndInvalid(t *testing.T) {
	b, _ := okf.Open(t.TempDir())
	s := NewOKFStore(b, vcs.NewFake(), meta.NewFakeStore(), ACLDefaults{Visibility: "private", Owner: "alice"})
	ctx := context.Background()
	if _, err := s.Get(ctx, "alice", ""); err == nil {
		t.Fatal("invalid id")
	}
	if _, err := s.Put(ctx, "concepts/p", &okf.Document{Type: "T", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "bob", "concepts/p"); err == nil {
		t.Fatal("private concept must deny bob")
	}
	if _, err := s.Get(ctx, "alice", "concepts/p"); err != nil {
		t.Fatalf("owner get: %v", err)
	}
}

func TestStoreCov2CommitFilesError(t *testing.T) {
	s, b := storeFixture2(t)
	// A directory where the concept file is expected makes ReadFile fail with a
	// non-NotExist error.
	if err := os.MkdirAll(filepath.Join(b.Root, "concepts", "x.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.commitFiles("concepts/x"); err == nil {
		t.Fatal("commitFiles must surface read errors")
	}
}

func TestStoreCov2ListShowHistoryDiff(t *testing.T) {
	s, _ := storeFixture2(t)
	ctx := context.Background()
	cancel, done := context.WithCancel(context.Background())
	done()
	if _, err := s.List(cancel); err == nil {
		t.Fatal("cancelled list")
	}
	if _, err := s.Show(ctx, "", ""); err == nil {
		t.Fatal("invalid show id")
	}
	_, _ = s.History(ctx, 1)
	_, _ = s.Diff(ctx, "", "")
}
