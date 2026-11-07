package okf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newDoc(typ, desc string) *Document {
	return &Document{Type: typ, Description: desc, Body: "# Body\n\nSee [missing](/tables/missing.md) and [peer](./peer.md).\n"}
}

func TestPutGetListDelete(t *testing.T) {
	b, err := Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := b.Put("tables/orders", newDoc("BigQuery Table", "One row per order."))
	if err != nil || h == "" {
		t.Fatalf("Put: %v %q", err, h)
	}
	d, err := b.Get("tables/orders")
	if err != nil || d.Type != "BigQuery Table" {
		t.Fatalf("Get: %v %+v", err, d)
	}
	ids, err := b.List()
	if err != nil || len(ids) != 1 || ids[0] != "tables/orders" {
		t.Fatalf("List: %v %v", err, ids)
	}
	if err := b.Delete("tables/orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get("tables/orders"); err == nil {
		t.Fatal("deleted concept still readable")
	}
}

func TestPathTraversalRejected(t *testing.T) {
	b, _ := Open(filepath.Join(t.TempDir(), "bundle"))
	for _, id := range []string{"../escape", "/etc/passwd", "a/../../b", "..", "tables/../index", "index", "log", "a\\b"} {
		if _, err := b.Path(id); err == nil {
			t.Fatalf("path %q must be rejected", id)
		}
	}
}

func TestIndexIsProgressiveAndLocal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundle")
	b, _ := Open(root)
	_, _ = b.Put("tables/orders", newDoc("Table", "Orders."))
	_, _ = b.Put("metrics/churn", newDoc("Metric", "Churn."))
	// Root index only lists subdirectories, no concept bodies.
	data, err := os.ReadFile(filepath.Join(root, "index.md"))
	if err == nil {
		if strings.Contains(string(data), "# Body") {
			t.Fatal("root index leaked concept bodies")
		}
	}
	// Only the affected directory index is regenerated.
	if _, err := os.Stat(filepath.Join(root, "tables", "index.md")); err != nil {
		t.Fatal("tables/index.md missing")
	}
	if _, err := os.Stat(filepath.Join(root, "metrics", "index.md")); err != nil {
		t.Fatal("metrics/index.md missing")
	}
}

func TestBrokenLinksTolerated(t *testing.T) {
	b, _ := Open(filepath.Join(t.TempDir(), "bundle"))
	_, _ = b.Put("tables/orders", newDoc("Table", "Orders."))
	links, err := b.Links("tables/orders")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"tables/missing": true, "tables/peer": true}
	for _, l := range links {
		if !want[l] {
			t.Fatalf("unexpected link %q", l)
		}
	}
	if len(links) != 2 {
		t.Fatalf("links = %v", links)
	}
}

func TestLogWritten(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundle")
	b, _ := Open(root)
	_, _ = b.Put("tables/orders", newDoc("Table", "Orders."))
	_, _ = b.Put("tables/customers", newDoc("Table", "Customers."))
	data, err := os.ReadFile(filepath.Join(root, "tables", "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "**Update**") != 2 {
		t.Fatalf("log entries missing:\n%s", data)
	}
}

func TestHashStableAndDifferent(t *testing.T) {
	a := Hash([]byte("same"))
	if a != Hash([]byte("same")) || a == Hash([]byte("other")) {
		t.Fatal("content hash unstable")
	}
}

func TestPutRejectsInvalidDocument(t *testing.T) {
	b, _ := Open(filepath.Join(t.TempDir(), "bundle"))
	if _, err := b.Put("x", &Document{}); err == nil {
		t.Fatal("document without type must be rejected")
	}
}
