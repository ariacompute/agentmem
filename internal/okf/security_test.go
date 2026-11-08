package okf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func newSecurityBundle(t *testing.T) (*Bundle, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bundle")
	b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return b, root
}

func TestBundleRejectsSymlinkEscape(t *testing.T) {
	b, root := newSecurityBundle(t)
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("---\ntype: Concept\n---\nsecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := b.Get("link"); err == nil {
		t.Fatal("Get must not follow a symlink out of the bundle")
	}
	if _, err := b.ReadResource("link.md"); err == nil {
		t.Fatal("ReadResource must not follow a symlink out of the bundle")
	}
	if err := b.Delete("link"); err == nil {
		t.Fatal("Delete must not remove a file outside the bundle")
	}
}

func TestBundleRejectsSymlinkedParentDirectory(t *testing.T) {
	b, root := newSecurityBundle(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := b.Put("nested/escape", &Document{Type: "Concept", Body: "# x\n"}); err == nil {
		t.Fatal("Put must not write through a symlinked parent directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.md")); !os.IsNotExist(err) {
		t.Fatal("a file was written outside the bundle")
	}
}

func TestBundleRejectsPlatformAmbiguousPaths(t *testing.T) {
	b, _ := newSecurityBundle(t)
	for _, id := range []string{
		"CON", "concepts/NUL.md", "C:/secret", "dir/name /x",
		"a/../../escape", "a\\b",
	} {
		if _, err := b.Path(id); err == nil {
			t.Fatalf("Path(%q) must fail closed", id)
		}
	}
}

func TestAtomicWriteIsPrivateUniqueAndClean(t *testing.T) {
	b, _ := newSecurityBundle(t)
	if _, err := b.Put("a/b", &Document{Type: "Concept", Body: "# x\n"}); err != nil {
		t.Fatal(err)
	}
	p, err := b.Path("a/b")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("concept file mode = %o, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".agentmem-") || strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
	// Concurrent writes must not share a fixed temporary name. Separate
	// directories also keep index/log rebuilding out of the race.
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			_, err := b.Put(fmt.Sprintf("a/c%d/concept", i), &Document{Type: "Concept", Body: "# x\n"})
			done <- err
		}(i)
	}
	for i := 0; i < 4; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent write failed: %v", err)
		}
	}
}

func TestParseDocumentRejectsAliasBomb(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("---\ntype: Concept\n")
	for i := 0; i < maxYAMLAliases+2; i++ {
		fmt.Fprintf(&sb, "k%d: &anchor%d value\n", i, i)
	}
	sb.WriteString("---\n# Body\n")
	if _, err := ParseDocument([]byte(sb.String())); err == nil {
		t.Fatal("an anchor/alias flood must be rejected before YAML expansion")
	}
}

func TestParseDocumentRejectsDeepAndWideYAML(t *testing.T) {
	deep := &yaml.Node{Kind: yaml.DocumentNode}
	cur := deep
	for i := 0; i < maxYAMLDepth+2; i++ {
		child := &yaml.Node{Kind: yaml.MappingNode}
		cur.Content = append(cur.Content, child)
		cur = child
	}
	count := 0
	if err := checkNodeBudget(deep, 0, &count); err == nil {
		t.Fatal("pathologically deep YAML must be rejected")
	}

	wide := &yaml.Node{Kind: yaml.DocumentNode}
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i < maxYAMLNodes+1; i++ {
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode})
	}
	wide.Content = []*yaml.Node{mapping}
	count = 0
	if err := checkNodeBudget(wide, 0, &count); err == nil {
		t.Fatal("pathologically wide YAML must be rejected")
	}
}

func TestParseDocumentRejectsOversizedInput(t *testing.T) {
	if _, err := ParseDocument(make([]byte, maxDocumentBytes+1)); err == nil {
		t.Fatal("oversized document must be rejected")
	}
	if _, _, err := SplitFrontmatter(make([]byte, maxDocumentBytes+1)); err == nil {
		t.Fatal("SplitFrontmatter must enforce the same size budget")
	}
}
