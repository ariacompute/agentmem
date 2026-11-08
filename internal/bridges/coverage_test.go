package bridges

import (
	"testing"

	"agentmem/internal/config"
)

func TestBridgeCapabilitiesAndFingerprint(t *testing.T) {
	cli, err := NewCLIBridge(config.AdapterConfig{Name: "stub"}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	_ = cli.Capabilities()
	if cli.Fingerprint() != "bridge-cli/v1:stub" {
		t.Fatalf("cli fingerprint = %q", cli.Fingerprint())
	}

	httpB, err := NewHTTPBridge(config.AdapterConfig{Name: "h"}, testSpec())
	if err != nil {
		t.Fatal(err)
	}
	_ = httpB.Capabilities()
	if httpB.Fingerprint() != "bridge-http/v1:h" {
		t.Fatalf("http fingerprint = %q", httpB.Fingerprint())
	}
}
