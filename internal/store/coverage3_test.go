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

// failingMeta fails the two metadata calls the canonical store depends on.
type failingMeta struct {
	*meta.FakeStore
	upsertErr error
	aclErr    error
}

func (f failingMeta) UpsertConcept(context.Context, meta.ConceptMeta) error { return f.upsertErr }
func (f failingMeta) CheckACL(context.Context, string, string) (bool, error) {
	if f.aclErr != nil {
		return false, f.aclErr
	}
	return f.FakeStore.CheckACL(context.Background(), "", "")
}

func TestTrustedACLRejectsInvalidDefaultVisibility(t *testing.T) {
	d := &okf.Document{Type: "Concept", Body: "b"}
	err := TrustedACL(d, ACLDefaults{Visibility: "world"}, "alice")
	if err == nil {
		t.Fatal("an invalid default visibility must be rejected for a trusted write")
	}
}

func TestPutRejectsInvalidACLDefaults(t *testing.T) {
	s, _ := newStore(t, "world")
	if _, err := s.Put(context.Background(), "team/a", doc("d")); err == nil {
		t.Fatal("an invalid configured visibility must fail the write")
	}
}

func TestPutSurfacesBundleWriteFailure(t *testing.T) {
	s, root := newStore(t, "public")
	if err := os.Chmod(root, 0o500); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	if _, err := s.Put(context.Background(), "team/a", doc("d")); err == nil {
		t.Fatal("an unwritable bundle must fail the write")
	}
}

// logAsDirectory makes index/log collection fail after the body was written.
func TestPutSurfacesIndexCollectionFailure(t *testing.T) {
	s, _ := newStore(t, "public")
	s.Bundle.LogWrites = false
	p, err := s.Bundle.Path("team/a")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(filepath.Join(dir, "log.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(context.Background(), "team/a", doc("d")); err == nil {
		t.Fatal("an unreadable log entry must fail the commit file collection")
	}
}

func TestPutSurfacesMetadataFailure(t *testing.T) {
	b, err := okf.Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewOKFStore(b,
		vcs.NewFake(),
		failingMeta{FakeStore: meta.NewFakeStore(), upsertErr: errors.New("upsert refused")},
		ACLDefaults{Visibility: "public", Owner: "alice"})
	if _, err := s.Put(context.Background(), "team/a", doc("d")); err == nil {
		t.Fatal("a failing metadata write must be reported")
	}
}

func TestCommitFilesRejectsInvalidConcept(t *testing.T) {
	s, _ := storeFixture2(t)
	if _, err := s.commitFiles("../escape"); err == nil {
		t.Fatal("an escaping concept id must fail path resolution")
	}
}

func TestDeleteSurfacesIndexCollectionFailure(t *testing.T) {
	s, _ := newStore(t, "public")
	s.Bundle.LogWrites = false
	ctx := context.Background()
	if _, err := s.Put(ctx, "team/a", doc("d")); err != nil {
		t.Fatal(err)
	}
	p, err := s.Bundle.Path("team/a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(p), "log.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "team/a"); err == nil {
		t.Fatal("an unreadable log entry must fail the deletion commit")
	}
}

func TestGetSurfacesACLFailure(t *testing.T) {
	b, err := okf.Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewOKFStore(b,
		vcs.NewFake(),
		failingMeta{FakeStore: meta.NewFakeStore(), aclErr: errors.New("acl refused")},
		ACLDefaults{Visibility: "public", Owner: "alice"})
	if _, err := s.Get(context.Background(), "alice", "team/a"); err == nil {
		t.Fatal("a failing ACL lookup must be reported")
	}
}
