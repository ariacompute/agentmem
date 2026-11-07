package manifest

import (
	"strings"
	"testing"
)

const valid = `
name: demo
kind: demo
engine: demo
version: "1.0.0"
bridge: http
protocol_version: agentmem-bridge/v1
timeout_ms: 3000
offline_mock: true
citation_scheme: "demo:/"
capabilities:
  vector: true
  semantic: false
  graph: false
  page_level: false
sdk_boundary:
  sidecar: "Demo sidecar"
  wire_protocol: agentmem-bridge/v1
  endpoints: ["GET /healthz", "POST /query"]
  capability_map:
    vector: "Demo vector index"
  credentials_ref: "AGENTMEM_DEMO_TOKEN"
`

func TestParseValid(t *testing.T) {
	d, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Name != "demo" || !d.Capabilities.Vector || d.Capabilities.Graph {
		t.Fatalf("descriptor = %+v", d)
	}
	if !d.Capabilities.Put || !d.Capabilities.Query || !d.Capabilities.Delete {
		t.Fatal("bridge plugins must declare put/query/delete")
	}
	if !strings.HasPrefix(d.Fingerprint, "plugin/demo/") {
		t.Fatalf("fingerprint = %q", d.Fingerprint)
	}
	d2, err := Parse([]byte(valid))
	if err != nil || d2.Fingerprint != d.Fingerprint {
		t.Fatal("fingerprint is not deterministic")
	}
}

func TestParseFingerprintChangesWithContract(t *testing.T) {
	d1, _ := Parse([]byte(valid))
	d2, _ := Parse([]byte(strings.Replace(valid, `version: "1.0.0"`, `version: "2.0.0"`, 1)))
	if d1.Fingerprint == d2.Fingerprint {
		t.Fatal("version change must change fingerprint")
	}
	d3, _ := Parse([]byte(strings.Replace(valid, "Demo vector index", "Other index", 1)))
	if d1.Fingerprint == d3.Fingerprint {
		t.Fatal("sdk boundary change must change fingerprint")
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"unknown key":        strings.Replace(valid, "timeout_ms: 3000", "timeout_ms: 3000\nunknown_field: 1", 1),
		"missing boundary":   strings.Replace(valid, "  credentials_ref: \"AGENTMEM_DEMO_TOKEN\"\n", "", 1),
		"kind mismatch":      strings.Replace(valid, "kind: demo", "kind: other", 1),
		"wrong wire version": strings.Replace(valid, "wire_protocol: agentmem-bridge/v1", "wire_protocol: other", 1),
		"undeclared cap":     strings.Replace(valid, "  capability_map:\n", "  capability_map:\n    graph: nope\n", 1),
		"disabled cap":       strings.Replace(valid, "  capability_map:\n    vector: \"Demo vector index\"", "  capability_map:\n    semantic: \"not enabled\"", 1),
		"no capabilities":    strings.Replace(valid, "  vector: true", "  vector: false", 1),
		"invalid bridge":     strings.Replace(valid, "bridge: http", "bridge: grpc", 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
