package safepath

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCoverageBranches(t *testing.T) {
	invalid := []string{
		"",
		strings.Repeat("a", MaxRelLength+1),
		"a\x00b",
		"a\\b",
		"a:b",
		"a\x01b",
		".",
		"..",
		"/abs",
		"../x",
		"CON/x",
		"a./b",
		"con",
		"trail ",
	}
	for _, rel := range invalid {
		if err := Validate(rel); err == nil {
			t.Fatalf("expected %q to be invalid", rel)
		}
	}
	if err := Validate("a/b.md"); err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
}

func TestResolveCoverageBranches(t *testing.T) {
	if _, err := Resolve("", "x"); err == nil {
		t.Fatal("empty root")
	}
	if _, err := Resolve(t.TempDir(), "../escape"); err == nil {
		t.Fatal("escaping path")
	}
	root := t.TempDir()
	if _, err := Resolve(root, "a/b.md"); err != nil {
		t.Fatalf("valid resolve: %v", err)
	}
	// A not-yet-existing root returns the joined target.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := Resolve(missing, "a.md"); err != nil {
		t.Fatalf("missing root: %v", err)
	}
}
