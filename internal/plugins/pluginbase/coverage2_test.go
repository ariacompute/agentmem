package pluginbase

import (
	"context"
	"testing"

	"agentmem/internal/config"
)

func TestPluginNameFallbackAndCLI(t *testing.T) {
	// Empty config name falls back to the descriptor name.
	p, err := New(testDesc(), config.AdapterConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "stub" {
		t.Fatalf("name = %q", p.Name())
	}

	// A CLI-bridge descriptor builds the same plugin shape.
	desc := testDesc()
	desc.Bridge = "cli"
	cliPlugin, err := New(desc, config.AdapterConfig{Name: "stub"})
	if err != nil {
		t.Fatal(err)
	}
	if cliPlugin.Name() == "" {
		t.Fatal("cli plugin must have a name")
	}
}

func TestPluginHealthWhileOpen(t *testing.T) {
	p, err := New(testDesc(), config.AdapterConfig{Name: "stub"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Health(context.Background()); err != nil {
		t.Fatalf("health while open = %v", err)
	}
}
