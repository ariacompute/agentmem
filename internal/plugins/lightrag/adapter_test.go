package lightrag

import (
	"context"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
	"agentmem/internal/okf"
)

func TestPluginDescriptorAndHybridCapabilities(t *testing.T) {
	d, err := Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	c := d.Capabilities
	if !c.Vector || !c.Semantic || !c.Graph || c.PageLevel {
		t.Fatalf("capabilities = %+v", c)
	}
	if len(d.SdkBoundary.CapabilityMap) != 3 {
		t.Fatalf("capability_map = %+v", d.SdkBoundary.CapabilityMap)
	}
	a, err := New(config.AdapterConfig{Name: "lightrag", Kind: "lightrag"}, adapter.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Put(ctx, "c1", &okf.Document{Type: "Concept", Body: "hybrid graph vector"}); err != nil {
		t.Fatal(err)
	}
	hits, err := a.Query(ctx, adapter.Query{Text: "hybrid", TopK: 2})
	if err != nil || len(hits) == 0 {
		t.Fatalf("query: %v / %+v", err, hits)
	}
	if hits[0].Source != "lightrag" || hits[0].Citation != "lightrag:/c1" {
		t.Fatalf("provenance = %+v", hits[0])
	}
}
