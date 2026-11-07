package graphrag

import (
	"context"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

func TestPluginDescriptorAndCLIOfflineContract(t *testing.T) {
	d, err := Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	if d.Bridge != "cli" || !d.Capabilities.Graph || d.Capabilities.Vector {
		t.Fatalf("descriptor = %+v", d)
	}
	a, err := New(config.AdapterConfig{Name: "graphrag", Kind: "graphrag"}, adapter.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if err := a.Put(ctx, "community/c1", &okf.Document{Type: "Concept", Body: "entity graph summary"}); err != nil {
		t.Fatal(err)
	}
	hits, err := a.Query(ctx, adapter.Query{Text: "graph", TopK: 2})
	if err != nil || len(hits) == 0 {
		t.Fatalf("query: %v / %+v", err, hits)
	}
	if hits[0].Citation != "graphrag://community/community/c1" {
		t.Fatalf("citation = %q", hits[0].Citation)
	}
}
