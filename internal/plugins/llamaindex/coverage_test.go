package llamaindex

import (
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
)

func TestCoverage(t *testing.T) {
	if _, err := Descriptor(); err != nil {
		t.Fatal(err)
	}
	r := adapter.NewRegistry()
	if err := Register(r); err != nil {
		t.Fatal(err)
	}
	if _, err := New(config.AdapterConfig{Name: "llamaindex", Kind: "llamaindex", Enabled: true}, adapter.Deps{}); err != nil {
		t.Fatal(err)
	}
}
