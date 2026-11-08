package okf

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// yamlNode is an internal alias so document.go stays free of yaml imports.
type yamlNode = yaml.Node

var knownKeys = map[string]bool{
	"type": true, "title": true, "description": true, "resource": true,
	"tags": true, "sources": true, "generated": true, "verified": true,
	"status": true, "stale_after": true, "okf_version": true,
	"runtime": true, "parameters": true, "computation": true,
	"executor": true, "attester": true,
}

var canonicalOrder = []string{
	"type", "title", "description", "resource", "tags",
	"status", "generated", "verified", "stale_after", "sources",
	"runtime", "parameters", "computation", "executor", "attester",
}

// Parsing budgets defend against YAML alias bombs and pathologically deep
// documents. OKF conformance stays permissive: unknown keys and types are
// still accepted; only resource consumption is bounded.
const (
	maxFrontmatterBytes = 1 << 20
	maxYAMLNodes        = 10000
	maxYAMLDepth        = 32
	maxYAMLAliases      = 32
)

var aliasTokenRe = regexp.MustCompile(`(?:^|[\s,\[{])[&*][A-Za-z_][A-Za-z0-9_-]*`)

func guardFrontmatter(fm []byte) error {
	if len(fm) > maxFrontmatterBytes {
		return fmt.Errorf("okf: frontmatter exceeds %d bytes", maxFrontmatterBytes)
	}
	if n := len(aliasTokenRe.FindAll(fm, -1)); n > maxYAMLAliases {
		return fmt.Errorf("okf: frontmatter exceeds %d YAML alias/anchor tokens", maxYAMLAliases)
	}
	return nil
}

func checkNodeBudget(n *yaml.Node, depth int, count *int) error {
	if depth > maxYAMLDepth {
		return fmt.Errorf("okf: frontmatter exceeds YAML depth %d", maxYAMLDepth)
	}
	if *count++; *count > maxYAMLNodes {
		return fmt.Errorf("okf: frontmatter exceeds %d YAML nodes", maxYAMLNodes)
	}
	for _, child := range n.Content {
		if err := checkNodeBudget(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

// SplitFrontmatter separates the leading YAML block from the markdown body.
func SplitFrontmatter(data []byte) (fm []byte, body []byte, err error) {
	if len(data) > maxDocumentBytes {
		return nil, nil, fmt.Errorf("okf: document exceeds %d bytes", maxDocumentBytes)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimSuffix(lines[0], "\r")) != "---" {
		return nil, nil, errors.New("okf: missing opening frontmatter delimiter")
	}
	start := 1
	end := -1
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(strings.TrimSuffix(lines[i], "\r")) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, nil, errors.New("okf: missing closing frontmatter delimiter")
	}
	fm = []byte(strings.Join(lines[start:end], "\n"))
	body = []byte(strings.Join(lines[end+1:], "\n"))
	body = []byte(strings.TrimPrefix(string(body), "\n"))
	return fm, body, nil
}

// ParseDocument parses a concept document with permissive conformance.
func ParseDocument(data []byte) (*Document, error) {
	fm, body, err := SplitFrontmatter(data)
	if err != nil {
		return nil, err
	}
	if err := guardFrontmatter(fm); err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(fm, &root); err != nil {
		return nil, fmt.Errorf("okf: invalid frontmatter: %w", err)
	}
	count := 0
	if err := checkNodeBudget(&root, 0, &count); err != nil {
		return nil, err
	}
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = *root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("okf: frontmatter must be a mapping")
	}
	d := &Document{Body: string(body), extra: map[string]*yaml.Node{}}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		key := k.Value
		d.order = append(d.order, key)
		if knownKeys[key] {
			d.decodeKnown(key, v)
		} else {
			cp := *v
			d.extra[key] = &cp
		}
	}
	return d, nil
}

func (d *Document) decodeKnown(key string, v *yaml.Node) {
	switch key {
	case "type":
		d.Type = scalar(v)
	case "title":
		d.Title = scalar(v)
	case "description":
		d.Description = scalar(v)
	case "resource":
		d.Resource = scalar(v)
	case "status":
		d.Status = scalar(v)
	case "tags":
		d.Tags = stringList(v)
	case "stale_after":
		if t, ok := parseTime(v); ok {
			d.StaleAfter = &t
		}
	case "generated":
		if e, ok := parseActor(v); ok {
			d.Generated = &e
		}
	case "verified":
		d.Verified = parseVerified(v)
	case "sources":
		d.Sources = parseSources(v)
	case "runtime", "parameters", "computation", "executor", "attester":
		d.ensureAttested()
		decodeAttestedKey(d.Attested, key, v)
	}
}

func (d *Document) ensureAttested() {
	if d.Attested == nil {
		d.Attested = &Attested{}
	}
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func stringList(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, scalar(c))
	}
	return out
}

func parseTime(n *yaml.Node) (time.Time, bool) {
	s := scalar(n)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func parseActor(n *yaml.Node) (ActorEvent, bool) {
	if n == nil || n.Kind != yaml.MappingNode {
		return ActorEvent{}, false
	}
	e := ActorEvent{By: scalar(mappingValue(n, "by"))}
	if t, ok := parseTime(mappingValue(n, "at")); ok {
		e.At = t
	}
	return e, true
}

// parseVerified accepts both a bare mapping and a list (OKF v0.2 §5.2).
func parseVerified(n *yaml.Node) []ActorEvent {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.MappingNode {
		if e, ok := parseActor(n); ok {
			return []ActorEvent{e}
		}
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]ActorEvent, 0, len(n.Content))
	for _, c := range n.Content {
		if e, ok := parseActor(c); ok {
			out = append(out, e)
		}
	}
	return out
}

func parseSources(n *yaml.Node) []Source {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]Source, 0, len(n.Content))
	for _, c := range n.Content {
		s := Source{
			ID: scalar(mappingValue(c, "id")), Resource: scalar(mappingValue(c, "resource")),
			Title: scalar(mappingValue(c, "title")), Author: scalar(mappingValue(c, "author")),
		}
		if u := mappingValue(c, "usage_count"); u != nil {
			var count int
			if _, err := fmt.Sscan(scalar(u), &count); err == nil {
				s.UsageCount = count
			}
		}
		if t, ok := parseTime(mappingValue(c, "last_modified")); ok {
			s.LastModified = &t
		}
		out = append(out, s)
	}
	return out
}

type actorOut struct {
	By string `yaml:"by"`
	At string `yaml:"at,omitempty"`
}

type sourceOut struct {
	ID           string `yaml:"id,omitempty"`
	Resource     string `yaml:"resource"`
	Title        string `yaml:"title,omitempty"`
	Author       string `yaml:"author,omitempty"`
	UsageCount   int    `yaml:"usage_count,omitempty"`
	LastModified string `yaml:"last_modified,omitempty"`
}

type paramOut struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Required bool   `yaml:"required"`
}

type resourceOut struct {
	Resource string   `yaml:"resource"`
	Receipt  []string `yaml:"receipt,omitempty"`
}

func (d *Document) valueFor(key string) (any, bool) {
	switch key {
	case "type":
		return d.Type, d.Type != ""
	case "title":
		return d.Title, d.Title != ""
	case "description":
		return d.Description, d.Description != ""
	case "resource":
		return d.Resource, d.Resource != ""
	case "status":
		return d.Status, d.Status != ""
	case "tags":
		return d.Tags, len(d.Tags) > 0
	case "stale_after":
		if d.StaleAfter == nil {
			return nil, false
		}
		return formatTime(*d.StaleAfter), true
	case "generated":
		if d.Generated == nil {
			return nil, false
		}
		a := actorOut{By: d.Generated.By}
		if !d.Generated.At.IsZero() {
			a.At = formatTime(d.Generated.At)
		}
		return a, true
	case "verified":
		if len(d.Verified) == 0 {
			return nil, false
		}
		out := make([]actorOut, 0, len(d.Verified))
		for _, e := range d.Verified {
			a := actorOut{By: e.By}
			if !e.At.IsZero() {
				a.At = formatTime(e.At)
			}
			out = append(out, a)
		}
		return out, true
	case "sources":
		if len(d.Sources) == 0 {
			return nil, false
		}
		out := make([]sourceOut, 0, len(d.Sources))
		for _, s := range d.Sources {
			so := sourceOut{ID: s.ID, Resource: s.Resource, Title: s.Title, Author: s.Author, UsageCount: s.UsageCount}
			if s.LastModified != nil {
				so.LastModified = formatTime(*s.LastModified)
			}
			out = append(out, so)
		}
		return out, true
	case "runtime":
		if d.Attested == nil || d.Attested.Runtime == "" {
			return nil, false
		}
		return d.Attested.Runtime, true
	case "parameters":
		if d.Attested == nil || len(d.Attested.Parameters) == 0 {
			return nil, false
		}
		out := make([]paramOut, 0, len(d.Attested.Parameters))
		for _, p := range d.Attested.Parameters {
			out = append(out, paramOut{Name: p.Name, Type: p.Type, Required: p.Required})
		}
		return out, true
	case "computation":
		if d.Attested == nil || d.Attested.Computation == "" {
			return nil, false
		}
		return d.Attested.Computation, true
	case "executor":
		if d.Attested == nil || d.Attested.Executor == nil || d.Attested.Executor.Resource == "" {
			return nil, false
		}
		return resourceOut{Resource: d.Attested.Executor.Resource, Receipt: d.Attested.Executor.Receipt}, true
	case "attester":
		if d.Attested == nil || d.Attested.Attester == nil || d.Attested.Attester.Resource == "" {
			return nil, false
		}
		return resourceOut{Resource: d.Attested.Attester.Resource}, true
	}
	return nil, false
}

func nodeOf(v any) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		return nil, err
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0], nil
	}
	return &n, nil
}

// Marshal re-serializes frontmatter and body, preserving unknown keys.
func (d *Document) Marshal() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	emitted := map[string]bool{}
	appendPair := func(key string, node *yaml.Node) {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
		emitted[key] = true
	}
	for _, key := range d.order {
		if knownKeys[key] {
			v, ok := d.valueFor(key)
			if !ok {
				continue
			}
			n, err := nodeOf(v)
			if err != nil {
				return nil, err
			}
			appendPair(key, n)
		} else if n, ok := d.extra[key]; ok && n != nil {
			appendPair(key, n)
		}
	}
	for _, key := range canonicalOrder {
		if emitted[key] {
			continue
		}
		v, ok := d.valueFor(key)
		if !ok {
			continue
		}
		n, err := nodeOf(v)
		if err != nil {
			return nil, err
		}
		appendPair(key, n)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := "---\n" + buf.String() + "---\n" + d.Body
	return []byte(out), nil
}

func (d *Document) extraString(k string) string {
	if d.extra == nil {
		return ""
	}
	n := d.extra[k]
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func (d *Document) setExtraScalar(k, v string) {
	if d.extra == nil {
		d.extra = map[string]*yaml.Node{}
	}
	found := false
	for _, x := range d.order {
		if x == k {
			found = true
			break
		}
	}
	if !found {
		d.order = append(d.order, k)
	}
	d.extra[k] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}
