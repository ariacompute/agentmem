// Package manifest defines the declarative plugin contract for third-party
// engines. It is the formal boundary for real SDK / sidecar integration: the
// wire protocol, endpoints, capability mapping and credential reference are
// data, not comments, so they can be validated and tested offline.
package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"agentmem/internal/adapter"
	"gopkg.in/yaml.v3"
)

// SdkBoundary describes exactly where agentmem ends and a real engine SDK
// begins. CredentialsRef is a reference (name of an environment variable or
// secret), never a credential value.
type SdkBoundary struct {
	Sidecar        string            `yaml:"sidecar"`
	WireProtocol   string            `yaml:"wire_protocol"`
	Endpoints      []string          `yaml:"endpoints"`
	CapabilityMap  map[string]string `yaml:"capability_map"`
	CredentialsRef string            `yaml:"credentials_ref"`
}

type capabilityYAML struct {
	Vector    bool `yaml:"vector"`
	Semantic  bool `yaml:"semantic"`
	Graph     bool `yaml:"graph"`
	PageLevel bool `yaml:"page_level"`
}

type manifestYAML struct {
	Name            string         `yaml:"name"`
	Kind            string         `yaml:"kind"`
	Engine          string         `yaml:"engine"`
	Version         string         `yaml:"version"`
	Bridge          string         `yaml:"bridge"`
	ProtocolVersion string         `yaml:"protocol_version"`
	TimeoutMS       int            `yaml:"timeout_ms"`
	OfflineMock     bool           `yaml:"offline_mock"`
	CitationScheme  string         `yaml:"citation_scheme"`
	Capabilities    capabilityYAML `yaml:"capabilities"`
	SdkBoundary     SdkBoundary    `yaml:"sdk_boundary"`
}

// Descriptor is the validated, immutable form of a plugin manifest.
type Descriptor struct {
	Name            string
	Kind            string
	Engine          string
	Version         string
	Bridge          string
	ProtocolVersion string
	TimeoutMS       int
	OfflineMock     bool
	CitationScheme  string
	Capabilities    adapter.Capabilities
	SdkBoundary     SdkBoundary
	Fingerprint     string
}

// Parse strictly validates a plugin manifest. Unknown keys are rejected so a
// typo in the SDK boundary can never silently weaken the contract.
func Parse(data []byte) (Descriptor, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var raw manifestYAML
	if err := dec.Decode(&raw); err != nil {
		return Descriptor{}, fmt.Errorf("plugins: parse manifest: %w", err)
	}
	if err := validate(&raw); err != nil {
		return Descriptor{}, err
	}
	caps := adapter.Capabilities{
		Put:       true,
		Query:     true,
		Delete:    true,
		Vector:    raw.Capabilities.Vector,
		Semantic:  raw.Capabilities.Semantic,
		Graph:     raw.Capabilities.Graph,
		PageLevel: raw.Capabilities.PageLevel,
	}
	d := Descriptor{
		Name: raw.Name, Kind: raw.Kind, Engine: raw.Engine, Version: raw.Version,
		Bridge: raw.Bridge, ProtocolVersion: raw.ProtocolVersion,
		TimeoutMS: raw.TimeoutMS, OfflineMock: raw.OfflineMock,
		CitationScheme: raw.CitationScheme, Capabilities: caps,
		SdkBoundary: raw.SdkBoundary,
	}
	d.Fingerprint = fingerprint(&d)
	return d, nil
}

func validate(r *manifestYAML) error {
	if r.Name == "" || r.Kind == "" || r.Engine == "" || r.Version == "" {
		return errors.New("plugins: manifest requires name, kind, engine and version")
	}
	if r.Kind != r.Name {
		return fmt.Errorf("plugins: manifest kind %q must equal name %q", r.Kind, r.Name)
	}
	switch r.Bridge {
	case "http", "cli":
	default:
		return fmt.Errorf("plugins: manifest bridge %q must be http or cli", r.Bridge)
	}
	if r.ProtocolVersion == "" || r.CitationScheme == "" {
		return errors.New("plugins: manifest requires protocol_version and citation_scheme")
	}
	if r.TimeoutMS < 1 {
		return errors.New("plugins: manifest timeout_ms must be positive")
	}
	c := r.Capabilities
	if !c.Vector && !c.Semantic && !c.Graph && !c.PageLevel {
		return errors.New("plugins: manifest must declare at least one retrieval capability")
	}
	b := r.SdkBoundary
	if b.Sidecar == "" || b.WireProtocol == "" || b.CredentialsRef == "" {
		return errors.New("plugins: sdk_boundary requires sidecar, wire_protocol and credentials_ref")
	}
	if len(b.Endpoints) == 0 || len(b.CapabilityMap) == 0 {
		return errors.New("plugins: sdk_boundary requires endpoints and capability_map")
	}
	if b.WireProtocol != r.ProtocolVersion {
		return fmt.Errorf("plugins: sdk_boundary.wire_protocol %q != protocol_version %q", b.WireProtocol, r.ProtocolVersion)
	}
	declared := map[string]bool{"vector": c.Vector, "semantic": c.Semantic, "graph": c.Graph, "page_level": c.PageLevel}
	keys := make([]string, 0, len(b.CapabilityMap))
	for k := range b.CapabilityMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		on, ok := declared[k]
		if !ok {
			return fmt.Errorf("plugins: capability_map has undeclared key %q", k)
		}
		if !on || strings.TrimSpace(b.CapabilityMap[k]) == "" {
			return fmt.Errorf("plugins: capability_map %q must describe an enabled capability", k)
		}
	}
	return nil
}

// fingerprint binds a projection to the exact protocol + capability contract.
// Changing the manifest version, endpoints or capabilities forces a rebuild.
func fingerprint(d *Descriptor) string {
	keys := make([]string, 0, len(d.SdkBoundary.CapabilityMap))
	for k := range d.SdkBoundary.CapabilityMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(d.Name + "|" + d.Version + "|" + d.Bridge + "|" + d.ProtocolVersion + "|")
	sb.WriteString(fmt.Sprintf("%t/%t/%t/%t|", d.Capabilities.Vector, d.Capabilities.Semantic,
		d.Capabilities.Graph, d.Capabilities.PageLevel))
	sb.WriteString(strings.Join(d.SdkBoundary.Endpoints, ",") + "|")
	for _, k := range keys {
		sb.WriteString(k + "=" + d.SdkBoundary.CapabilityMap[k] + ";")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return "plugin/" + d.Name + "/" + hex.EncodeToString(sum[:])[:24]
}
