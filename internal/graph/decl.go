package graph

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// Derived relation types. They are always marked with their origin so a reader
// can tell an authored claim from a mechanical inference.
const (
	EdgeSourcedFrom = "sourced_from"
	EdgeReferences  = "references"
	EdgeLinksTo     = "links_to"
)

// NodeFrom builds the index record and the edge set for one document. The
// content hash and commit come from the canonical write that produced the body.
func NodeFrom(id string, doc *okf.Document, contentHash, commitSHA string) (meta.GraphNodeRecord, []meta.GraphEdgeRecord, []string, error) {
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return meta.GraphNodeRecord{}, nil, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if doc == nil {
		return meta.GraphNodeRecord{}, nil, nil, fmt.Errorf("%w: nil document", ErrInvalid)
	}
	decl, err := doc.Graph()
	if err != nil {
		return meta.GraphNodeRecord{}, nil, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	nodeType := decl.Node.Type
	if nodeType == "" {
		nodeType = strings.TrimSpace(doc.Type)
	}
	if nodeType == "" {
		nodeType = "concept"
	}
	label := decl.Node.Label
	if label == "" {
		label = strings.TrimSpace(doc.Title)
	}
	if label == "" {
		label = defaultLabel(clean)
	}
	node := meta.GraphNodeRecord{
		ConceptID:   clean,
		NodeType:    nodeType,
		Label:       label,
		Description: strings.TrimSpace(doc.Description),
		Visibility:  doc.Visibility(),
		Owner:       doc.Owner(),
		Status:      doc.EffectiveStatus(),
		Trust:       string(doc.TrustTier()),
		Properties:  decl.Node.Properties,
		SourceCount: len(doc.Sources),
		HasResource: strings.TrimSpace(doc.Resource) != "",
		ContentHash: contentHash,
		CommitSHA:   commitSHA,
	}
	if node.Properties == nil {
		node.Properties = meta.StringMap{}
	}

	var warnings []string
	var edges []meta.GraphEdgeRecord
	seen := map[string]bool{}
	add := func(to, edgeType, origin, evidence string) {
		target, ok := edgeTarget(to)
		if !ok || target == clean {
			return
		}
		key := edgeType + "\x00" + target
		if seen[key] {
			return
		}
		seen[key] = true
		edges = append(edges, meta.GraphEdgeRecord{
			FromID: clean, ToID: target, EdgeType: edgeType, Origin: origin, Evidence: evidence,
		})
	}
	for _, e := range decl.Edges {
		add(e.To, e.Type, meta.EdgeOriginDeclared, e.Evidence)
	}
	// Derived edges reuse material the document already carries: sources,
	// resource and markdown cross-links. They are always labelled as derived.
	for _, s := range doc.Sources {
		add(s.Resource, EdgeSourcedFrom, meta.EdgeOriginSource, s.ID)
	}
	add(doc.Resource, EdgeReferences, meta.EdgeOriginResource, "")
	for _, link := range okf.ConceptLinks(clean, doc.Body) {
		add(link, EdgeLinksTo, meta.EdgeOriginLink, "")
	}
	sortEdges(edges)
	return node, edges, warnings, nil
}

func defaultLabel(id string) string {
	base := id
	if i := strings.LastIndex(id, "/"); i >= 0 {
		base = id[i+1:]
	}
	return base
}

// edgeTarget accepts only bundle-relative concept references. URLs, anchors and
// absolute paths are lineage, not graph targets, so they never become edges.
func edgeTarget(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "#") {
		return "", false
	}
	raw = strings.SplitN(raw, "#", 2)[0]
	raw = strings.TrimSpace(raw)
	base := raw
	if i := strings.LastIndex(raw, "/"); i >= 0 {
		base = raw[i+1:]
	}
	if strings.Contains(base, ".") && !strings.HasSuffix(base, ".md") {
		// revenue.sql and friends are resources, not concepts.
		return "", false
	}
	clean, err := okf.ValidateConceptID(raw)
	if err != nil {
		return "", false
	}
	return clean, true
}

// TargetLookup resolves the node type of an edge target. A missing target is
// not an error: OKF allows broken links, and the graph reports them as
// dangling edges.
type TargetLookup func(ctx context.Context, id string) (nodeType string, ok bool)

// ValidateDecl enforces the typed ontology. Declared relation types are hard
// constraints (422 when the endpoint types do not match); unknown types, unknown
// node types and missing targets are permissive warnings, matching OKF
// conformance.
func ValidateDecl(ctx context.Context, schema okf.GraphSchema, node meta.GraphNodeRecord, edges []meta.GraphEdgeRecord, lookup TargetLookup) ([]string, error) {
	if schema.Empty() {
		return nil, nil
	}
	var warnings []string
	if len(schema.NodeTypes) > 0 {
		if _, ok := schema.NodeTypes[node.NodeType]; !ok {
			warnings = append(warnings, fmt.Sprintf("unknown_node_type: %s", node.NodeType))
		}
	}
	unknownEdge := map[string]bool{}
	for _, e := range edges {
		def, ok := schema.EdgeTypes[e.EdgeType]
		if !ok {
			unknownEdge[e.EdgeType] = true
			continue
		}
		if len(def.From) > 0 && !contains(def.From, node.NodeType) {
			return nil, fmt.Errorf("%w: edge %q is not allowed from node type %q", ErrInvalid, e.EdgeType, node.NodeType)
		}
		if lookup == nil {
			continue
		}
		targetType, found := lookup(ctx, e.ToID)
		if !found {
			warnings = append(warnings, fmt.Sprintf("dangling_edge: %s -> %s", e.EdgeType, e.ToID))
			continue
		}
		if len(def.To) > 0 && !contains(def.To, targetType) {
			return nil, fmt.Errorf("%w: edge %q cannot target node type %q", ErrInvalid, e.EdgeType, targetType)
		}
	}
	for t := range unknownEdge {
		warnings = append(warnings, fmt.Sprintf("unknown_edge_type: %s", t))
	}
	sort.Strings(warnings)
	return dedupeStrings(warnings), nil
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func sortEdges(edges []meta.GraphEdgeRecord) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.FromID != b.FromID {
			return a.FromID < b.FromID
		}
		if a.ToID != b.ToID {
			return a.ToID < b.ToID
		}
		return a.EdgeType < b.EdgeType
	})
}
