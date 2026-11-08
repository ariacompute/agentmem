package safepath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsUnsafePaths(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"NUL":               "a\x00b.md",
		"backslash":         "a\\b.md",
		"colon":             "C:/secret.md",
		"absolute":          "/etc/passwd",
		"parent":            "../outside.md",
		"clean escape":      "a/../../outside.md",
		"dot only":          ".",
		"trailing space":    "concepts/a.md ",
		"trailing dot":      "concepts/a.md.",
		"control character": "a\n.md",
		"windows CON":       "CON.md",
		"windows COM1":      "concepts/COM1.md",
		"windows LPT9":      "LPT9.txt",
		"too long":          strings.Repeat("a", MaxRelLength+1) + ".md",
	}
	for name, rel := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Validate(rel); err == nil {
				t.Fatalf("Validate(%q) must fail closed", rel)
			}
		})
	}
}

func TestValidateAcceptsNestedPaths(t *testing.T) {
	for _, rel := range []string{"concepts/a.md", "a/b/c.sql", "index.md", "dir/.hidden"} {
		if err := Validate(rel); err != nil {
			t.Fatalf("Validate(%q) = %v", rel, err)
		}
	}
}

func TestResolveKeepsPathInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(root, "a/b/c.md")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "a", "b", "c.md")
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveAllowsFreshRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created-yet")
	if _, err := Resolve(root, "concepts/a.md"); err != nil {
		t.Fatalf("a root that does not exist yet must still be usable: %v", err)
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestResolveRejectsFileSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, secret, filepath.Join(root, "link.md"))
	if _, err := Resolve(root, "link.md"); err == nil {
		t.Fatal("a symlink pointing outside the root must be rejected")
	}
}

func TestResolveRejectsParentDirectorySymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	dir := filepath.Join(outside, "sub")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, dir, filepath.Join(root, "nested"))
	if _, err := Resolve(root, "nested/new.md"); err == nil {
		t.Fatal("a symlinked parent directory must not allow writes outside the root")
	}
}

func TestResolveAllowsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "real")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, inside, filepath.Join(root, "alias"))
	if _, err := Resolve(root, "alias/new.md"); err != nil {
		t.Fatalf("a symlink staying inside the root must be allowed: %v", err)
	}
}

func TestResolveRejectsUnsafeRel(t *testing.T) {
	if _, err := Resolve(t.TempDir(), "../escape.md"); err == nil {
		t.Fatal("Resolve must validate rel before joining")
	}
}
