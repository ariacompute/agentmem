package vcs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage"
)

func TestNewGitRejectsRootBelowRegularFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGit(filepath.Join(blocker, "repo"), "a", "a@b"); err == nil {
		t.Fatal("a root below a regular file must fail")
	}
}

func TestNewGitReportsBrokenRepository(t *testing.T) {
	root := t.TempDir()
	// A .git file that does not point at a real git directory is not
	// "repository not exists", so PlainOpen reports an error instead of init.
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not-a-gitdir-link\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGit(root, "a", "a@b"); err == nil {
		t.Fatal("a malformed repository must fail to open")
	}
}

func TestCommitRequiresWorktree(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	g := &GitRepository{root: dir, repo: repo, author: object.Signature{Name: "a", Email: "a@b"}}
	if _, err := g.Commit(context.Background(), "m", map[string][]byte{"a.md": []byte("x")}); err == nil {
		t.Fatal("a bare repository has no worktree and must fail")
	}
}

func TestCommitReportsFilesystemFailures(t *testing.T) {
	ctx := context.Background()
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	// A parent that is a regular file cannot become a directory.
	if err := os.WriteFile(filepath.Join(g.root, "plain"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ctx, "m", map[string][]byte{"plain/child.md": []byte("x")}); err == nil {
		t.Fatal("a file component below a regular file must fail")
	}
	// A target that is a directory cannot be written as a file.
	if err := os.MkdirAll(filepath.Join(g.root, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ctx, "m", map[string][]byte{"adir": []byte("x")}); err == nil {
		t.Fatal("a directory target must fail the write")
	}
}

func TestCommitReportsStagingFailure(t *testing.T) {
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	previous := stageFile
	stageFile = func(*git.Worktree, string) (plumbing.Hash, error) {
		return plumbing.ZeroHash, errors.New("stage refused")
	}
	t.Cleanup(func() { stageFile = previous })
	if _, err := g.Commit(context.Background(), "m", map[string][]byte{"a.md": []byte("x")}); err == nil {
		t.Fatal("a failing stage must be reported")
	} else if !strings.Contains(err.Error(), "vcs: stage a.md:") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommitReportsEmptyCommit(t *testing.T) {
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(context.Background(), "m", nil); err == nil {
		t.Fatal("a commit without files must fail")
	}
}

// corruptBranch makes the branch ref unreadable.
func corruptBranch(t *testing.T, root, content string) {
	t.Helper()
	refDir := filepath.Join(root, ".git", "refs", "heads")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "master"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// errReferenceStorer fails every reference lookup with a non-notfound error.
type errReferenceStorer struct {
	storage.Storer
	err error
}

func (e errReferenceStorer) Reference(plumbing.ReferenceName) (*plumbing.Reference, error) {
	return nil, e.err
}

// breakReferences makes every reference lookup fail with a hard I/O error.
func breakReferences(g *GitRepository) {
	g.repo.Storer = errReferenceStorer{Storer: g.repo.Storer, err: errors.New("storer broken")}
}

func TestHeadAndLogReportMalformedRef(t *testing.T) {
	ctx := context.Background()
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ctx, "first", map[string][]byte{"a.md": []byte("one")}); err != nil {
		t.Fatal(err)
	}
	breakReferences(g)
	if _, err := g.Head(ctx); err == nil {
		t.Fatal("a malformed HEAD ref must be reported")
	}
	if _, err := g.Log(ctx, 0); err == nil {
		t.Fatal("Log must surface a malformed HEAD ref")
	}
	if _, err := g.Show(ctx, "", "a.md"); err == nil {
		t.Fatal("Show with an empty ref must surface a malformed HEAD")
	}
	if _, err := g.Diff(ctx, "", ""); err == nil {
		t.Fatal("Diff with empty refs must surface a malformed HEAD")
	}
}

func TestLogReportsMissingObjects(t *testing.T) {
	ctx := context.Background()
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ctx, "first", map[string][]byte{"a.md": []byte("one")}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ctx, "second", map[string][]byte{"a.md": []byte("two")}); err != nil {
		t.Fatal(err)
	}
	if out, err := g.Log(ctx, 1); err != nil || len(out) != 1 {
		t.Fatalf("limit must stop iteration: %+v (%v)", out, err)
	}

	// A branch that points at a commit outside the object store cannot be walked.
	corruptBranch(t, g.root, "0000000000000000000000000000000000000000\n")
	if _, err := g.Log(ctx, 0); err == nil {
		t.Fatal("Log must surface a missing root commit")
	}
}

func TestLogReportsBrokenParentChain(t *testing.T) {
	ctx := context.Background()
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	missingParent := plumbing.NewHash("1111111111111111111111111111111111111111")
	tree := writeTree(t, g, "a.md", "one")
	orphan := writeCommit(t, g, tree, []plumbing.Hash{missingParent}, "orphan")
	setBranch(t, g, orphan)

	if _, err := g.Log(ctx, 0); err == nil {
		t.Fatal("Log must surface a broken parent chain")
	}
}

func TestShowReportsUnreadableBlob(t *testing.T) {
	ctx := context.Background()
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	missingBlob := plumbing.NewHash("2222222222222222222222222222222222222222")
	tree := writeTreeEntry(t, g, "a.md", missingBlob)
	commit := writeCommit(t, g, tree, nil, "orphan")
	if _, err := g.Show(ctx, commit.String(), "a.md"); err == nil {
		t.Fatal("a commit tree referencing a missing blob must fail Show")
	}
}

func TestDiffReportsMissingObjects(t *testing.T) {
	ctx := context.Background()
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	first, err := g.Commit(ctx, "first", map[string][]byte{"a.md": []byte("one")})
	if err != nil {
		t.Fatal(err)
	}
	missingTree := plumbing.NewHash("3333333333333333333333333333333333333333")
	src := writeCommit(t, g, missingTree, nil, "source")
	dst := writeCommit(t, g, writeTree(t, g, "a.md", "two"), []plumbing.Hash{src}, "destination")

	// Resolving the destination commit fails before any tree is read.
	if _, err := g.Diff(ctx, first, "4444444444444444444444444444444444444444"); err == nil {
		t.Fatal("a destination commit outside the store must fail")
	}
	if _, err := g.Diff(ctx, "", "4444444444444444444444444444444444444444"); err == nil {
		t.Fatal("an implicit-parent diff must fail for an unknown destination")
	}
	// The source tree of the implicit parent is missing.
	if _, err := g.Diff(ctx, "", dst.String()); err == nil {
		t.Fatal("a missing source tree must fail the diff")
	}
	// The destination tree is fine but the source tree is missing.
	if _, err := g.Diff(ctx, src.String(), dst.String()); err == nil {
		t.Fatal("a missing explicit source tree must fail the diff")
	}
	if _, err := g.Diff(ctx, "5555555555555555555555555555555555555555", first); err == nil {
		t.Fatal("a source commit outside the store must fail")
	}
}

// writeTree stores a tree with one real blob and returns its hash.
func writeTree(t *testing.T, g *GitRepository, name, content string) plumbing.Hash {
	t.Helper()
	tree := &object.Tree{Entries: []object.TreeEntry{{
		Name: name, Mode: 0o100644, Hash: writeBlob(t, g, content),
	}}}
	return storeTree(t, g, tree)
}

// writeTreeEntry stores a tree whose file points at an arbitrary (possibly
// missing) blob hash.
func writeTreeEntry(t *testing.T, g *GitRepository, name string, blob plumbing.Hash) plumbing.Hash {
	t.Helper()
	tree := &object.Tree{Entries: []object.TreeEntry{{Name: name, Mode: 0o100644, Hash: blob}}}
	return storeTree(t, g, tree)
}

func writeBlob(t *testing.T, g *GitRepository, content string) plumbing.Hash {
	t.Helper()
	return storeEncoded(t, g, plumbing.BlobObject, func(enc plumbing.EncodedObject) error {
		w, err := enc.Writer()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
		return w.Close()
	})
}

func storeTree(t *testing.T, g *GitRepository, tree *object.Tree) plumbing.Hash {
	t.Helper()
	return storeEncoded(t, g, plumbing.TreeObject, func(enc plumbing.EncodedObject) error {
		return tree.Encode(enc)
	})
}

// storeEncoded writes one encoded object of the given type.
func storeEncoded(t *testing.T, g *GitRepository, typ plumbing.ObjectType, encode func(plumbing.EncodedObject) error) plumbing.Hash {
	t.Helper()
	enc := g.repo.Storer.NewEncodedObject()
	enc.SetType(typ)
	if err := encode(enc); err != nil {
		t.Fatal(err)
	}
	hash, err := g.repo.Storer.SetEncodedObject(enc)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func writeCommit(t *testing.T, g *GitRepository, tree plumbing.Hash, parents []plumbing.Hash, message string) plumbing.Hash {
	t.Helper()
	sig := object.Signature{Name: "a", Email: "a@b", When: time.Unix(1700000000, 0).UTC()}
	c := &object.Commit{
		Author:       sig,
		Committer:    sig,
		Message:      message,
		TreeHash:     tree,
		ParentHashes: parents,
	}
	return storeEncoded(t, g, plumbing.CommitObject, func(enc plumbing.EncodedObject) error {
		return c.Encode(enc)
	})
}

// setBranch repoints HEAD (and its master ref) at the given commit.
func setBranch(t *testing.T, g *GitRepository, hash plumbing.Hash) {
	t.Helper()
	if err := g.repo.Storer.SetReference(plumbing.NewHashReference("refs/heads/master", hash)); err != nil {
		t.Fatal(err)
	}
	if err := g.repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/master")); err != nil {
		t.Fatal(err)
	}
}

func TestCommitReportsInjectedFilesystemFailures(t *testing.T) {
	ctx := context.Background()
	newRepo := func(t *testing.T) *GitRepository {
		t.Helper()
		g, err := NewGit(t.TempDir(), "a", "a@b")
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	t.Run("mkdir", func(t *testing.T) {
		g := newRepo(t)
		previous := mkdirAll
		mkdirAll = func(string, os.FileMode) error { return errors.New("mkdir refused") }
		t.Cleanup(func() { mkdirAll = previous })
		if _, err := g.Commit(ctx, "m", map[string][]byte{"sub/a.md": []byte("x")}); err == nil {
			t.Fatal("a failing directory creation must be reported")
		}
	})
	t.Run("write", func(t *testing.T) {
		g := newRepo(t)
		previous := writeFile
		writeFile = func(string, []byte, os.FileMode) error { return errors.New("write refused") }
		t.Cleanup(func() { writeFile = previous })
		if _, err := g.Commit(ctx, "m", map[string][]byte{"a.md": []byte("x")}); err == nil {
			t.Fatal("a failing write must be reported")
		}
	})
}

// unreadableObject is a blob whose contents cannot be streamed.
type unreadableObject struct{ *plumbing.MemoryObject }

func (unreadableObject) Reader() (io.ReadCloser, error) {
	return nil, errors.New("blob unreadable")
}

// brokenBlobStorer serves one blob hash as an unreadable object.
type brokenBlobStorer struct {
	storage.Storer
	hash plumbing.Hash
}

func (b brokenBlobStorer) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	if h == b.hash {
		obj := &plumbing.MemoryObject{}
		obj.SetType(plumbing.BlobObject)
		obj.SetSize(3)
		return unreadableObject{obj}, nil
	}
	return b.Storer.EncodedObject(t, h)
}

func TestShowReportsUnreadableContents(t *testing.T) {
	ctx := context.Background()
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	blob := writeBlob(t, g, "one")
	tree := writeTreeEntry(t, g, "a.md", blob)
	commit := writeCommit(t, g, tree, nil, "orphan")
	g.repo.Storer = brokenBlobStorer{Storer: g.repo.Storer, hash: blob}
	if _, err := g.Show(ctx, commit.String(), "a.md"); err == nil {
		t.Fatal("a blob that cannot be read must fail Show")
	}
}
