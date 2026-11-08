package vcs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitRejectsPathAmbiguities(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundle")
	repo, err := NewGit(root, "tester", "t@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"../escape.md",
		"a/../../escape.md", // filepath.Clean must not launder this
		"a/../../../outside.md",
		"/etc/passwd",
		"C:/secret.md",
		"a/\x00.md",
		"COM1.md",
		"a/b.md ",
	} {
		if _, err := repo.Commit(context.Background(), "bad", map[string][]byte{name: []byte("x")}); err == nil {
			t.Fatalf("Commit(%q) must fail closed", name)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.md")); !os.IsNotExist(err) {
		t.Fatal("a commit wrote outside the repository")
	}
}

func TestCommitRejectsSymlinkedParent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := NewGit(root, "tester", "t@example.com")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := repo.Commit(context.Background(), "bad",
		map[string][]byte{"link/escape.md": []byte("x")}); err == nil {
		t.Fatal("a symlinked parent directory must not be written through")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.md")); !os.IsNotExist(err) {
		t.Fatal("a file was written outside the repository")
	}
}

func TestCommitWritesPrivateFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundle")
	repo, err := NewGit(root, "tester", "t@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(context.Background(), "ok",
		map[string][]byte{"tables/a.md": []byte("v1")}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "tables", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("committed file mode = %o, want 0600", info.Mode().Perm())
	}
}
