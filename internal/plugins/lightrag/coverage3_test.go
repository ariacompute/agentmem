package lightrag

import (
	"errors"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/config"
)

// TestNewPropagatesDescriptorError pins the fail-closed behaviour for a broken
// embedded manifest: the plugin must not be built at all.
func TestNewPropagatesDescriptorError(t *testing.T) {
	if _, err := Descriptor(); err != nil {
		t.Fatalf("embedded manifest must be valid: %v", err)
	}
	saved := loadOnce.err
	loadOnce.err = errors.New("broken manifest")
	t.Cleanup(func() { loadOnce.err = saved })

	if _, err := New(config.AdapterConfig{Name: "lightrag", Kind: "lightrag", Enabled: true}, adapter.Deps{}); err == nil {
		t.Fatal("a broken descriptor must fail plugin construction")
	}
}
