package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/bridges"
	"agentmem/internal/config"
	"agentmem/internal/okf"
	"agentmem/internal/plugins/pluginbase"
)

func TestDescriptorsAndKinds(t *testing.T) {
	ds, err := Descriptors()
	if err != nil {
		t.Fatalf("descriptors: %v", err)
	}
	if len(ds) != 4 {
		t.Fatalf("want 4 plugins, got %d", len(ds))
	}
	kinds := Kinds()
	for i, d := range ds {
		if d.Name != kinds[i] || d.Kind != kinds[i] {
			t.Fatalf("descriptor/kind mismatch: %+v / %v", d, kinds)
		}
		if d.SdkBoundary.Sidecar == "" || d.SdkBoundary.CredentialsRef == "" ||
			len(d.SdkBoundary.Endpoints) == 0 || len(d.SdkBoundary.CapabilityMap) == 0 {
			t.Fatalf("incomplete sdk boundary: %+v", d)
		}
		if !d.Capabilities.Query {
			t.Fatalf("plugin %s cannot query", d.Name)
		}
	}
}

func TestKindsAcceptedByConfig(t *testing.T) {
	for _, kind := range Kinds() {
		cfg := config.Default()
		cfg.Adapters = append(cfg.Adapters, config.AdapterConfig{Name: kind, Kind: kind, Enabled: true})
		if err := cfg.Validate(); err != nil {
			t.Fatalf("config rejected plugin kind %q: %v", kind, err)
		}
	}
}

// The legacy generic-kind declarations must not drift from the plugin
// manifests: both feed the same routing capability decisions.
func TestCapabilitiesMatchLegacyBridgeSpecs(t *testing.T) {
	specs, err := bridges.EngineSpecs()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]bridges.EngineSpec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	ds, err := Descriptors()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		s, ok := byName[d.Name]
		if !ok {
			t.Fatalf("no legacy bridge spec for plugin %q", d.Name)
		}
		a, b := d.Capabilities, s.Capabilities
		if a.Vector != b.Vector || a.Semantic != b.Semantic || a.Graph != b.Graph || a.PageLevel != b.PageLevel {
			t.Fatalf("%s capability drift: plugin=%+v spec=%+v", d.Name, a, b)
		}
		if strings.Contains(s.Notes, "TODO") {
			t.Fatalf("legacy spec %s still carries an annotation boundary", d.Name)
		}
	}
}

func TestRegisterAllIsIdempotent(t *testing.T) {
	r := adapter.NewRegistry()
	if err := RegisterAll(r); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAll(r); err != nil {
		t.Fatalf("second registration: %v", err)
	}
	for _, kind := range Kinds() {
		if !r.HasKind(kind) {
			t.Fatalf("kind %q not registered", kind)
		}
	}
}

func TestPluginsContractOffline(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Adapters = nil
	for _, kind := range Kinds() {
		cfg.Adapters = append(cfg.Adapters, config.AdapterConfig{
			Name: kind, Kind: kind, Enabled: true, Endpoint: "", TimeoutMS: 3000,
		})
	}
	r := adapter.NewRegistry()
	if err := RegisterAll(r); err != nil {
		t.Fatal(err)
	}
	if err := r.Build(cfg, adapter.Deps{Embedder: adapter.NewHashingEmbedder(64)}); err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := r.StartAll(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	for name, err := range r.HealthAll(ctx) {
		if err != nil {
			t.Fatalf("health %s: %v", name, err)
		}
	}
	doc := &okf.Document{Type: "Concept", Title: "Orders", Description: "orders", Body: "one row per order"}
	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			a, ok := r.Get(kind)
			if !ok {
				t.Fatal("adapter not built")
			}
			p, ok := a.(*pluginbase.Plugin)
			if !ok {
				t.Fatalf("%T is not a pluginbase.Plugin", a)
			}
			if p.State() != pluginbase.StateRunning || p.Status()["mode"] != "offline-mock" {
				t.Fatalf("unexpected status: %+v", p.Status())
			}
			if !strings.Contains(p.Fingerprint(), "plugin/"+kind+"/") {
				t.Fatalf("fingerprint = %q", p.Fingerprint())
			}
			if err := p.Put(ctx, "tables/orders", doc); err != nil {
				t.Fatalf("put: %v", err)
			}
			hits, err := p.Query(ctx, adapter.Query{Text: "orders", TopK: 5})
			if err != nil || len(hits) == 0 {
				t.Fatalf("query: %v / %+v", err, hits)
			}
			want := p.Descriptor().CitationScheme
			if !strings.HasPrefix(hits[0].Citation, want) || hits[0].Source == "" {
				t.Fatalf("citation/source = %q / %q, want prefix %q", hits[0].Citation, hits[0].Source, want)
			}
			if err := p.Delete(ctx, "tables/orders"); err != nil {
				t.Fatalf("delete: %v", err)
			}
		})
	}
	if err := r.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	a, _ := r.Get(Kinds()[0])
	if err := a.Health(ctx); !errors.Is(err, pluginbase.ErrClosed) {
		t.Fatalf("closed plugin health = %v, want ErrClosed", err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatalf("close must be idempotent: %v", err)
	}
}
