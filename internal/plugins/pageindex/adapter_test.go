package pageindex

import (
	"context"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

func TestPluginDescriptorAndPageCitations(t *testing.T) {
	d, err := Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	if !d.Capabilities.PageLevel || !d.Capabilities.Semantic || d.Capabilities.Graph {
		t.Fatalf("capabilities = %+v", d.Capabilities)
	}
	a, err := New(config.AdapterConfig{Name: "pageindex", Kind: "pageindex"}, adapter.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Put(ctx, "manual/handbook", &okf.Document{Type: "Concept", Body: "page 12 details"}); err != nil {
		t.Fatal(err)
	}
	hits, err := a.Query(ctx, adapter.Query{Text: "page", TopK: 2})
	if err != nil || len(hits) == 0 {
		t.Fatalf("query: %v / %+v", err, hits)
	}
	if hits[0].Citation != "pageindex://page/manual/handbook" {
		t.Fatalf("citation = %q", hits[0].Citation)
	}
}
