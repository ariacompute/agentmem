package manifest

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func baseManifest() manifestYAML {
	return manifestYAML{
		Name: "pageindex", Kind: "pageindex", Engine: "pageindex", Version: "1.0.0",
		Bridge: "http", ProtocolVersion: "v1", TimeoutMS: 3000, OfflineMock: true,
		CitationScheme: "pageindex:/",
		Capabilities:   capabilityYAML{Vector: true},
		SdkBoundary: SdkBoundary{
			Sidecar: "true", WireProtocol: "v1", Endpoints: []string{"healthz"},
			CapabilityMap: map[string]string{"vector": "vectors"}, CredentialsRef: "PAGEINDEX_KEY",
		},
	}
}

func parseManifest(t *testing.T, m manifestYAML) (Descriptor, error) {
	t.Helper()
	b, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return Parse(b)
}

func TestManifestValidationBranches(t *testing.T) {
	if _, err := parseManifest(t, baseManifest()); err != nil {
		t.Fatalf("baseline must parse: %v", err)
	}
	cases := map[string]func(*manifestYAML){
		"missing name":         func(m *manifestYAML) { m.Name = "" },
		"kind mismatch":        func(m *manifestYAML) { m.Kind = "other" },
		"bad bridge":           func(m *manifestYAML) { m.Bridge = "grpc" },
		"missing protocol":     func(m *manifestYAML) { m.ProtocolVersion = "" },
		"missing citation":     func(m *manifestYAML) { m.CitationScheme = "" },
		"non-positive timeout": func(m *manifestYAML) { m.TimeoutMS = 0 },
		"no capability":        func(m *manifestYAML) { m.Capabilities = capabilityYAML{} },
		"missing sidecar":      func(m *manifestYAML) { m.SdkBoundary.Sidecar = "" },
		"missing wire":         func(m *manifestYAML) { m.SdkBoundary.WireProtocol = "" },
		"missing credentials":  func(m *manifestYAML) { m.SdkBoundary.CredentialsRef = "" },
		"no endpoints":         func(m *manifestYAML) { m.SdkBoundary.Endpoints = nil },
		"no capability_map":    func(m *manifestYAML) { m.SdkBoundary.CapabilityMap = nil },
		"wire mismatch":        func(m *manifestYAML) { m.SdkBoundary.WireProtocol = "v2" },
		"undeclared key": func(m *manifestYAML) {
			m.SdkBoundary.CapabilityMap = map[string]string{"vector": "v", "foo": "x"}
		},
		"disabled declared key": func(m *manifestYAML) {
			m.Capabilities = capabilityYAML{Semantic: true}
			m.SdkBoundary.CapabilityMap = map[string]string{"semantic": "s", "vector": "v"}
		},
		"empty capability description": func(m *manifestYAML) {
			m.SdkBoundary.CapabilityMap = map[string]string{"vector": ""}
		},
	}
	for name, mutate := range cases {
		m := baseManifest()
		mutate(&m)
		if _, err := parseManifest(t, m); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestManifestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("name: x\nbogus: 1\n")); err == nil {
		t.Fatal("unknown key must fail closed")
	}
	if _, err := Parse([]byte("::: not yaml")); err == nil {
		t.Fatal("malformed yaml must fail")
	}
}

func TestManifestFingerprint(t *testing.T) {
	d, err := parseManifest(t, baseManifest())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.Fingerprint, "plugin/pageindex/") {
		t.Fatalf("fingerprint = %q", d.Fingerprint)
	}
	if d.Capabilities.Put != true || d.Capabilities.Query != true || d.Capabilities.Delete != true {
		t.Fatalf("caps = %+v", d.Capabilities)
	}
}
