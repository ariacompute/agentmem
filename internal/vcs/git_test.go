package vcs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitBackendCommitShowLogDiff(t *testing.T) {
	repo, err := NewGit(filepath.Join(t.TempDir(), "bundle"), "tester", "t@example.com")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h1, err := repo.Commit(ctx, "first", map[string][]byte{"tables/a.md": []byte("v1")})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := repo.Commit(ctx, "second", map[string][]byte{"tables/a.md": []byte("v2")})
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head(ctx)
	if err != nil || head != h2 {
		t.Fatalf("head = %q, want %q (%v)", head, h2, err)
	}
	data, err := repo.Show(ctx, "", "tables/a.md")
	if err != nil || string(data) != "v2" {
		t.Fatalf("show = %q (%v)", data, err)
	}
	data, err = repo.Show(ctx, h1, "tables/a.md")
	if err != nil || string(data) != "v1" {
		t.Fatalf("historical show = %q (%v)", data, err)
	}
	entries, err := repo.Log(ctx, 10)
	if err != nil || len(entries) != 2 || entries[0].Message != "second" {
		t.Fatalf("log = %+v (%v)", entries, err)
	}
	patch, err := repo.Diff(ctx, "", "")
	if err != nil || !strings.Contains(patch, "tables/a.md") {
		t.Fatalf("diff = %q (%v)", patch, err)
	}
}

func TestGitRejectsTraversal(t *testing.T) {
	repo, _ := NewGit(filepath.Join(t.TempDir(), "bundle"), "", "")
	if _, err := repo.Commit(context.Background(), "bad", map[string][]byte{"../escape.md": []byte("x")}); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}

func TestFakeContract(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	if h, _ := f.Head(ctx); h != "" {
		t.Fatal("unborn head must be empty")
	}
	h1, err := f.Commit(ctx, "one", map[string][]byte{"a.md": []byte("1")})
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := f.Commit(ctx, "two", map[string][]byte{"a.md": []byte("2")})
	if head, _ := f.Head(ctx); head != h2 {
		t.Fatal("fake head mismatch")
	}
	if d, err := f.Show(ctx, h1, "a.md"); err != nil || string(d) != "1" {
		t.Fatalf("fake historical show = %q (%v)", d, err)
	}
	if entries, _ := f.Log(ctx, 10); len(entries) != 2 || entries[0].Message != "two" {
		t.Fatalf("fake log = %+v", entries)
	}
	if d, _ := f.Diff(ctx, "", ""); !strings.Contains(d, "a.md") {
		t.Fatalf("fake diff = %q", d)
	}
	if _, err := f.Show(ctx, "", "missing.md"); err == nil {
		t.Fatal("missing file must fail")
	}
}

func TestBothBackendsShareContract(t *testing.T) {
	backends := []Repository{
		NewFake(),
		mustGit(t),
	}
	for i, b := range backends {
		h, err := b.Commit(context.Background(), "seed", map[string][]byte{"x.md": []byte("body")})
		if err != nil || h == "" {
			t.Fatalf("backend %d commit: %q %v", i, h, err)
		}
		data, err := b.Show(context.Background(), "", "x.md")
		if err != nil || string(data) != "body" {
			t.Fatalf("backend %d show: %q %v", i, data, err)
		}
	}
}

func mustGit(t *testing.T) Repository {
	t.Helper()
	r, err := NewGit(filepath.Join(t.TempDir(), "bundle"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
