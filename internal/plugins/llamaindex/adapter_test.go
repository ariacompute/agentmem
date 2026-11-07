package llamaindex

import (
	"context"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

func TestPluginDescriptorAndOfflineContract(t *testing.T) {
	d, err := Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "llamaindex" || d.Bridge != "http" || !d.Capabilities.Vector || !d.Capabilities.Semantic {
		t.Fatalf("descriptor = %+v", d)
	}
	a, err := New(config.AdapterConfig{Name: "llamaindex", Kind: "llamaindex"}, adapter.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if err := a.Put(ctx, "c1", &okf.Document{Type: "Concept", Body: "hybrid retrieval"}); err != nil {
		t.Fatal(err)
	}
	hits, err := a.Query(ctx, adapter.Query{Text: "hybrid", TopK: 3})
	if err != nil || len(hits) == 0 {
		t.Fatalf("query: %v / %+v", err, hits)
	}
	if hits[0].Citation != "llamaindex:/c1" {
		t.Fatalf("citation = %q", hits[0].Citation)
	}
}
