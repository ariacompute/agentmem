package vcs

import (
	"context"
	"testing"
)

func TestNewGitErrors(t *testing.T) {
	if _, err := NewGit("", "a", "a@b"); err == nil {
		t.Fatal("empty root")
	}
	root := t.TempDir()
	if _, err := NewGit(root, "", ""); err != nil {
		t.Fatal(err)
	}
	// Re-opening an existing repository uses PlainOpen (not init).
	if _, err := NewGit(root, "a", "a@b"); err != nil {
		t.Fatal(err)
	}
}

func TestGitCommitHeadLogShowDiff(t *testing.T) {
	ctx := context.Background()
	g, err := NewGit(t.TempDir(), "a", "a@b")
	if err != nil {
		t.Fatal(err)
	}
	if h, err := g.Head(ctx); err != nil || h != "" {
		t.Fatalf("unborn head = %q (%v)", h, err)
	}
	if out, err := g.Log(ctx, 0); err != nil || out != nil {
		t.Fatalf("unborn log = %+v (%v)", out, err)
	}

	cancel, done := context.WithCancel(context.Background())
	done()
	if _, err := g.Commit(cancel, "m", map[string][]byte{"a": []byte("x")}); err == nil {
		t.Fatal("cancelled commit")
	}
	if _, err := g.Commit(ctx, "escape", map[string][]byte{"../x": []byte("x")}); err == nil {
		t.Fatal("path escape")
	}

	h1, err := g.Commit(ctx, "first", map[string][]byte{"a.md": []byte("one"), "b.md": []byte("two")})
	if err != nil {
		t.Fatal(err)
	}
	if h1 == "" {
		t.Fatal("empty commit hash")
	}
	if h, err := g.Head(ctx); err != nil || h != h1 {
		t.Fatalf("head = %q (%v)", h, err)
	}
	if out, err := g.Log(ctx, 5); err != nil || len(out) != 1 {
		t.Fatalf("log = %+v (%v)", out, err)
	}
	if data, err := g.Show(ctx, "", "a.md"); err != nil || string(data) != "one" {
		t.Fatalf("show head = %q (%v)", data, err)
	}
	if data, err := g.Show(ctx, h1, "b.md"); err != nil || string(data) != "two" {
		t.Fatalf("show ref = %q (%v)", data, err)
	}
	if _, err := g.Show(ctx, "", "missing.md"); err == nil {
		t.Fatal("show missing")
	}
	if _, err := g.Show(ctx, "0000000000000000000000000000000000000000", "a.md"); err == nil {
		t.Fatal("show unknown ref")
	}
	// Root commit with empty refs yields an empty patch.
	if patch, err := g.Diff(ctx, "", ""); err != nil || patch != "" {
		t.Fatalf("root diff = %q (%v)", patch, err)
	}

	h2, err := g.Commit(ctx, "second", map[string][]byte{"a.md": []byte("changed")})
	if err != nil {
		t.Fatal(err)
	}
	if patch, err := g.Diff(ctx, "", ""); err != nil || patch == "" {
		t.Fatalf("parent diff = %q (%v)", patch, err)
	}
	if _, err := g.Diff(ctx, h1, h2); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Diff(ctx, "0000000000000000000000000000000000000000", ""); err == nil {
		t.Fatal("unknown from must error")
	}
}

func TestFakeRepositoryBranches(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	if _, err := f.Show(ctx, "", "x"); err == nil {
		t.Fatal("empty show")
	}
	if _, err := f.Diff(ctx, "", ""); err == nil {
		t.Fatal("empty diff")
	}
	cancel, done := context.WithCancel(context.Background())
	done()
	if _, err := f.Commit(cancel, "m", nil); err == nil {
		t.Fatal("cancelled commit")
	}

	h1, err := f.Commit(ctx, "one", map[string][]byte{"a.md": []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Commit(ctx, "two", map[string][]byte{"b.md": []byte("y")}); err != nil {
		t.Fatal(err)
	}
	if head, err := f.Head(ctx); err != nil || head == "" {
		t.Fatalf("head = %q (%v)", head, err)
	}
	if out, err := f.Log(ctx, 0); err != nil || len(out) != 2 {
		t.Fatalf("log = %+v (%v)", out, err)
	}
	if data, err := f.Show(ctx, "", "a.md"); err != nil || string(data) != "x" {
		t.Fatalf("show = %q (%v)", data, err)
	}
	if _, err := f.Show(ctx, "", "missing.md"); err == nil {
		t.Fatal("show missing")
	}
	if _, err := f.Show(ctx, h1, "a.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Show(ctx, "unknown", "a.md"); err == nil {
		t.Fatal("unknown ref")
	}
	if patch, err := f.Diff(ctx, "", ""); err != nil || patch == "" {
		t.Fatalf("diff = %q (%v)", patch, err)
	}
	if _, err := f.Diff(ctx, "", "unknown"); err == nil {
		t.Fatal("diff unknown ref")
	}
}
