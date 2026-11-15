package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

// A root that sits below a regular file resolves with ENOTDIR, which is not
// os.ErrNotExist and must be reported instead of silently accepted.
func TestResolveRejectsRootBelowRegularFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "plain")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(filepath.Join(file, "bundle"), "a.md"); err == nil {
		t.Fatal("a root below a regular file must fail")
	}
}

// A path component that is a regular file makes Lstat fail with ENOTDIR while
// walking to the nearest existing ancestor.
func TestResolveRejectsComponentBelowRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "plain"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, "plain/child.md"); err == nil {
		t.Fatal("a component below a regular file must fail")
	}
}

// A symlink loop makes EvalSymlinks fail after Lstat succeeded.
func TestResolveRejectsSymlinkLoop(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "loop")
	if err := os.Symlink("loop", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Resolve(root, "loop"); err == nil {
		t.Fatal("a symlink loop must fail")
	}
}
