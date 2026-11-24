package okf

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func newBundleAt(t *testing.T) *Bundle {
	t.Helper()
	b, err := Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeConcept(t *testing.T, b *Bundle, id, body string) {
	t.Helper()
	if _, err := b.Put(id, &Document{Type: "Concept", Title: "T", Body: body}); err != nil {
		t.Fatal(err)
	}
}

func withReadDir(t *testing.T, fn func(string) ([]os.DirEntry, error)) func() {
	t.Helper()
	previous := readDir
	readDir = fn
	t.Cleanup(func() { readDir = previous })
	return func() { readDir = previous }
}

func withWalkDir(t *testing.T, fn func(fs.FS, string, fs.WalkDirFunc) error) {
	t.Helper()
	previous := walkDir
	walkDir = fn
	t.Cleanup(func() { walkDir = previous })
}

func TestOpenReportsUnresolvableWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Open("relative/bundle"); err == nil {
		t.Fatal("a missing working directory must fail path resolution")
	}
}

func TestReadResourceReportsReadFailure(t *testing.T) {
	b := newBundleAt(t)
	// A directory opens successfully but cannot be read as a file.
	if err := os.MkdirAll(filepath.Join(b.Root, "refs", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadResource("refs/nested"); err == nil {
		t.Fatal("a resource that cannot be read must be reported")
	}

	missing := newBundleAt(t)
	if _, err := missing.ReadResource("refs/absent.sql"); err == nil {
		t.Fatal("a missing resource must be reported")
	}
}

func TestReadResourceRejectsOversizedFile(t *testing.T) {
	b := newBundleAt(t)
	p := filepath.Join(b.Root, "refs", "big.sql")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("x", maxResourceBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadResource("refs/big.sql"); err == nil {
		t.Fatal("an oversized resource must be rejected")
	}
}

func TestPutRejectsReservedAndUnwritableTargets(t *testing.T) {
	b := newBundleAt(t)
	if _, err := b.Put("log", &Document{Type: "Concept"}); err == nil {
		t.Fatal("a reserved concept id must fail path resolution")
	}

	unwritable := newBundleAt(t)
	withCreateTemp(t, nil, errors.New("temp refused"))
	if _, err := unwritable.Put("a", &Document{Type: "Concept"}); err == nil {
		t.Fatal("a failing atomic write must be reported")
	}
}

func TestPutReportsIndexAndLogFailures(t *testing.T) {
	restore := withReadDir(t, func(string) ([]os.DirEntry, error) { return nil, errors.New("read refused") })
	if _, err := newBundleAt(t).Put("a", &Document{Type: "Concept"}); err == nil {
		t.Fatal("a failing index rebuild must be reported")
	}
	restore()

	// log.md as a directory can be written around but not appended to.
	b := newBundleAt(t)
	if err := os.MkdirAll(filepath.Join(b.Root, "log.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("a", &Document{Type: "Concept"}); err == nil {
		t.Fatal("an unreadable log entry must fail the append")
	}
}

func TestGetReportsReadAndSizeFailures(t *testing.T) {
	b := newBundleAt(t)
	writeConcept(t, b, "team/a", "body")
	// A directory opens successfully but cannot be read as a document.
	dir, err := b.Path("team/dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get("team/dir"); err == nil {
		t.Fatal("a document that cannot be read must be reported")
	}
	if _, err := b.Get("team/missing"); err == nil {
		t.Fatal("a missing concept must be reported")
	}

	big := newBundleAt(t)
	p, err := big.Path("team/big")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ntype: Concept\n---\n"+strings.Repeat("x", maxDocumentBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := big.Get("team/big"); err == nil {
		t.Fatal("an oversized document must be rejected")
	}
}

func TestDeleteSucceedsAndReportsIndexFailure(t *testing.T) {
	b := newBundleAt(t)
	b.LogWrites = false
	writeConcept(t, b, "team/a", "body")
	if err := b.Delete("team/a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := b.Get("team/a"); err == nil {
		t.Fatal("the concept must be gone")
	}

	unreadable := newBundleAt(t)
	writeConcept(t, unreadable, "team/a", "body")
	unreadable.LogWrites = false
	withReadDir(t, func(string) ([]os.DirEntry, error) { return nil, errors.New("read refused") })
	if err := unreadable.Delete("team/a"); err == nil {
		t.Fatal("a failed index rebuild must be reported")
	}
}

func TestListReportsWalkAndBudgetFailures(t *testing.T) {
	b := newBundleAt(t)
	writeConcept(t, b, "team/a", "body")
	writeConcept(t, b, "team/b", "body")
	ids, err := b.List()
	if err != nil || len(ids) != 2 {
		t.Fatalf("list = %+v (%v)", ids, err)
	}

	previous := maxBundleEntries
	maxBundleEntries = 1
	t.Cleanup(func() { maxBundleEntries = previous })
	if _, err := b.List(); err == nil {
		t.Fatal("the traversal budget must be enforced")
	}
	maxBundleEntries = previous

	withWalkDir(t, func(_ fs.FS, root string, fn fs.WalkDirFunc) error {
		return fn(root, nil, errors.New("walk refused"))
	})
	if _, err := b.List(); err == nil {
		t.Fatal("a failed walk must be reported")
	}
}

func TestRebuildIndexReportsFilesystemFailures(t *testing.T) {
	b := newBundleAt(t)
	blocker := filepath.Join(b.Root, "plain")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.RebuildIndex(blocker); err == nil {
		t.Fatal("a directory that cannot be created must be reported")
	}

	withReadDir(t, func(string) ([]os.DirEntry, error) { return nil, errors.New("read refused") })
	if err := b.RebuildIndex(filepath.Join(b.Root, "team")); err == nil {
		t.Fatal("a failed directory read must be reported")
	}
}

func TestAppendLogEdges(t *testing.T) {
	b := newBundleAt(t)
	dir := filepath.Join(b.Root, "team")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.AppendLog(dir, "Update", "team/a"); err != nil {
		t.Fatal(err)
	}
	// An existing dated heading keeps the newest entry first.
	existing := "# Directory Update Log\n\n## 2000-01-01\n* old\n"
	if err := os.WriteFile(filepath.Join(dir, "log.md"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.AppendLog(dir, "Update", "team/b"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "team/b") || !strings.Contains(string(data), "* old") {
		t.Fatalf("log = %q", data)
	}

	blocker := filepath.Join(b.Root, "plain")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.AppendLog(blocker, "Update", "x"); err == nil {
		t.Fatal("a directory below a regular file must be reported")
	}

	badLog := filepath.Join(b.Root, "badlog")
	if err := os.MkdirAll(filepath.Join(badLog, "log.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.AppendLog(badLog, "Update", "x"); err == nil {
		t.Fatal("an unreadable log entry must be reported")
	}
}

// stubTempFile fails on demand.
type stubTempFile struct {
	name     string
	chmodErr error
	writeErr error
	syncErr  error
	closeErr error
}

func (s *stubTempFile) Name() string              { return s.name }
func (s *stubTempFile) Chmod(os.FileMode) error   { return s.chmodErr }
func (s *stubTempFile) Write([]byte) (int, error) { return 0, s.writeErr }
func (s *stubTempFile) Sync() error               { return s.syncErr }
func (s *stubTempFile) Close() error              { return s.closeErr }

func withCreateTemp(t *testing.T, f tmpFile, err error) {
	t.Helper()
	previous := createTemp
	createTemp = func(string) (tmpFile, error) { return f, err }
	t.Cleanup(func() { createTemp = previous })
}

func TestAtomicWriteReportsDurabilityFailures(t *testing.T) {
	target := filepath.Join(t.TempDir(), "a.md")

	withCreateTemp(t, nil, errors.New("cannot create"))
	if err := atomicWrite(target, []byte("x")); err == nil {
		t.Fatal("a temporary file that cannot be created must be reported")
	}
	cases := map[string]*stubTempFile{
		"chmod": {name: target, chmodErr: errors.New("chmod refused")},
		"write": {name: target, writeErr: errors.New("write refused")},
		"sync":  {name: target, syncErr: errors.New("sync refused")},
		"close": {name: target, closeErr: errors.New("close refused")},
	}
	for name, file := range cases {
		withCreateTemp(t, file, nil)
		if err := atomicWrite(target, []byte("x")); err == nil {
			t.Fatalf("%s failure must be reported", name)
		}
	}
}

func TestParseDocumentRejectsDeepNesting(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("---\n")
	for i := 0; i < maxYAMLDepth+2; i++ {
		sb.WriteString(strings.Repeat("  ", i) + "k:\n")
	}
	sb.WriteString(strings.Repeat("  ", maxYAMLDepth+3) + "v: 1\n---\nbody\n")
	if _, err := ParseDocument([]byte(sb.String())); err == nil {
		t.Fatal("a document beyond the YAML depth budget must be rejected")
	}
}

// failingMarshaler is a value whose YAML form cannot be produced.
type failingMarshaler struct{}

func (failingMarshaler) MarshalYAML() (any, error) { return nil, errors.New("cannot marshal") }

func TestNodeOfReportsMarshalFailure(t *testing.T) {
	if _, err := nodeOf(failingMarshaler{}); err == nil {
		t.Fatal("a value that YAML cannot represent must fail")
	}
	if _, err := nodeFromYAML([]byte("{not yaml")); err == nil {
		t.Fatal("invalid YAML must fail")
	}
	n, err := nodeFromYAML(nil)
	if err != nil || n == nil {
		t.Fatalf("empty YAML must still return a node: %v", err)
	}
}

func TestMarshalSkipsDuplicateKeys(t *testing.T) {
	dup, err := ParseDocument([]byte("---\ntype: Concept\ntype: Concept\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := dup.Marshal()
	if err != nil {
		t.Fatalf("duplicate keys must be tolerated: %v", err)
	}
	if strings.Count(string(out), "type:") != 1 {
		t.Fatalf("a duplicate key must be emitted once: %q", out)
	}
}

func TestPutReportsMarshalFailure(t *testing.T) {
	b := newBundleAt(t)
	d := &Document{Type: "Concept", order: []string{"loop"}, extra: map[string]*yaml.Node{
		"loop": {Kind: yaml.Kind(99)},
	}}
	if _, err := b.Put("team/a", d); err == nil {
		t.Fatal("an unencodable document must fail the write")
	}
}

func TestMarshalSkipsEmptyKnownKeys(t *testing.T) {
	d, err := ParseDocument([]byte("---\ntype: Concept\ndescription:\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "description:") {
		t.Fatalf("an empty known key must be dropped: %q", out)
	}
}

func TestMarshalReportsUnencodableNode(t *testing.T) {
	// An unknown node kind cannot be encoded; the error must surface instead of
	// writing a truncated document.
	d := &Document{Type: "Concept", order: []string{"loop"}, extra: map[string]*yaml.Node{
		"loop": {Kind: yaml.Kind(99)},
	}}
	if _, err := d.Marshal(); err == nil {
		t.Fatal("an unencodable node must fail serialisation")
	}
}
