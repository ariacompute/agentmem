package okf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenErrors(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("empty root must error")
	}
	base := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(base, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(base); err == nil {
		t.Fatal("root that is a file must error")
	}
}

func TestValidateConceptIDBranches(t *testing.T) {
	if _, err := ValidateConceptID(""); err == nil {
		t.Fatal("empty")
	}
	if _, err := ValidateConceptID("a\\b"); err == nil {
		t.Fatal("backslash")
	}
	if _, err := ValidateConceptID("a\x00b"); err == nil {
		t.Fatal("null")
	}
	if _, err := ValidateConceptID(".."); err == nil {
		t.Fatal("dotdot")
	}
	if _, err := ValidateConceptID("../x"); err == nil {
		t.Fatal("escape")
	}
	if _, err := ValidateConceptID("/abs"); err == nil {
		t.Fatal("absolute")
	}
	if _, err := ValidateConceptID("index"); err == nil {
		t.Fatal("reserved index")
	}
	if _, err := ValidateConceptID("log.md"); err == nil {
		t.Fatal("reserved log")
	}
	if got, err := ValidateConceptID("x/y.md"); err != nil || got != "x/y" {
		t.Fatalf("valid = %q (%v)", got, err)
	}
}

func TestReadResourceErrors(t *testing.T) {
	b, _ := Open(t.TempDir())
	if _, err := b.ReadResource(""); err == nil {
		t.Fatal("empty path")
	}
	if _, err := b.ReadResource("../escape.sql"); err == nil {
		t.Fatal("traversal")
	}
	if _, err := b.ReadResource("missing.sql"); err == nil {
		t.Fatal("missing file")
	}
}

func TestPutAndGetAndDeleteErrors(t *testing.T) {
	b, _ := Open(t.TempDir())
	if _, err := b.Put("", &Document{Type: "T"}); err == nil {
		t.Fatal("empty id")
	}
	if _, err := b.Put("x", &Document{}); err == nil {
		t.Fatal("missing type")
	}
	if _, err := b.Put("index", &Document{Type: "T"}); err == nil {
		t.Fatal("reserved id")
	}
	if _, err := b.Get("../x"); err == nil {
		t.Fatal("get traversal")
	}
	if _, err := b.Get("missing"); err == nil {
		t.Fatal("get missing")
	}
	if err := b.Delete(""); err == nil {
		t.Fatal("delete empty")
	}
	if err := b.Delete("missing"); err == nil {
		t.Fatal("delete missing")
	}
	// Put then delete (LogWrites is on) exercises the log append path.
	if _, err := b.Put("notes/a", &Document{Type: "Note", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete("notes/a"); err != nil {
		t.Fatal(err)
	}
}

func TestBundleListSkipsReserved(t *testing.T) {
	b, _ := Open(t.TempDir())
	if _, err := b.Put("concepts/a", &Document{Type: "T", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("concepts/b", &Document{Type: "T", Body: "y"}); err != nil {
		t.Fatal(err)
	}
	ids, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found["concepts/a"] || !found["concepts/b"] {
		t.Fatalf("list = %v", ids)
	}
	for _, id := range ids {
		if strings.HasSuffix(id, "index") || strings.HasSuffix(id, "log") {
			t.Fatalf("reserved id leaked: %v", ids)
		}
	}
}

func TestBundleLinks(t *testing.T) {
	b, _ := Open(t.TempDir())
	body := "[a](../other.md) [b](sib.md) [c](/root.md) [d](http://x/y.md) [e](bad#frag.md) [f](dup.md) [g](dup.md)"
	if _, err := b.Put("concepts/a", &Document{Type: "T", Body: body}); err != nil {
		t.Fatal(err)
	}
	links, err := b.Links("concepts/a")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"other": true, "concepts/sib": true, "root": true, "concepts/dup": true}
	if len(links) != len(want) {
		t.Fatalf("links = %v", links)
	}
	for _, l := range links {
		if !want[l] {
			t.Fatalf("unexpected link %q in %v", l, links)
		}
	}
	if _, err := b.Links("missing"); err == nil {
		t.Fatal("links on missing must error")
	}
}

func TestRebuildIndexSubdirs(t *testing.T) {
	root := t.TempDir()
	b, _ := Open(root)
	dir := filepath.Join(root, "concepts")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("concepts/a", &Document{Type: "T", Description: "described"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("concepts/b", &Document{Type: "T"}); err != nil {
		t.Fatal(err)
	}
	if err := b.RebuildIndex(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "Subdirectories") || !strings.Contains(s, "described") || !strings.Contains(s, "OKF concept.") {
		t.Fatalf("index = %q", s)
	}
}

func TestAppendLogBranches(t *testing.T) {
	root := t.TempDir()
	b, _ := Open(root)
	dir := filepath.Join(root, "concepts")
	if err := b.AppendLog(dir, "Update", "first"); err != nil {
		t.Fatal(err)
	}
	if err := b.AppendLog(dir, "Update", "second"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "log.md"))
	if !strings.Contains(string(data), "first") || !strings.Contains(string(data), "second") {
		t.Fatalf("log = %q", data)
	}
	// A log with no dated section exercises the append branch.
	if err := os.WriteFile(filepath.Join(dir, "log.md"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.AppendLog(dir, "Deprecation", "third"); err != nil {
		t.Fatal(err)
	}
}

func TestCleanIDBranches(t *testing.T) {
	if cleanID("x/y.md") != "x/y" {
		t.Fatal("valid cleanID")
	}
	if cleanID("..") != ".." {
		t.Fatalf("invalid cleanID = %q", cleanID(".."))
	}
}

func TestAtomicWriteError(t *testing.T) {
	base := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(base, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(base, "child", "x.md"), []byte("d")); err == nil {
		t.Fatal("mkdir error must propagate")
	}
}

func TestDecodeAttestedParametersNonSequence(t *testing.T) {
	d, err := ParseDocument([]byte("---\ntype: T\nparameters: not-a-list\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Attested == nil || d.Attested.Parameters != nil {
		t.Fatalf("parameters = %+v", d.Attested)
	}
}

func TestParseVerifiedSequenceWithScalar(t *testing.T) {
	d, err := ParseDocument([]byte("---\ntype: T\nverified:\n  - just-a-scalar\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Verified) != 0 {
		t.Fatalf("verified = %+v", d.Verified)
	}
}

func TestBundleGetInvalidFrontmatter(t *testing.T) {
	b, _ := Open(t.TempDir())
	p, err := b.Path("bad")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not a frontmatter document"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get("bad"); err == nil {
		t.Fatal("invalid document must not parse")
	}
}
