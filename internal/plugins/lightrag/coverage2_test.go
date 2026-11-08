package lightrag

import (
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
)

func TestNewBadEndpointErrors(t *testing.T) {
	if _, err := New(config.AdapterConfig{Name: "lightrag", Kind: "lightrag", Enabled: true, Endpoint: "://bad"}, adapter.Deps{}); err == nil {
		t.Fatal("an invalid endpoint must fail plugin construction")
	}
}
