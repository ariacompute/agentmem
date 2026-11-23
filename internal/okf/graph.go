package okf

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Graph declarations are an agentmem extension carried as frontmatter keys, in
// the same spirit as visibility/owner: they round-trip verbatim through Extra,
// so unknown sub-keys survive, and only the typed accessors below interpret
// them. Git stays the single authority for the declaration; PostgreSQL keeps a
// rebuildable adjacency index.
const (
	graphKey       = "graph"
	graphSchemaKey = "graph_schema"

	maxGraphEdges      = 64
	maxGraphProperties = 32
	maxGraphValueBytes = 256
	maxGraphEvidence   = 1024
	maxGraphIdentBytes = 128
	maxGraphTypes      = 256
)

// GraphNode is the node declaration of one concept document. Type defaults to
// the document type; an explicit override lets one document act as another
// ontology node type without duplicating the body.
type GraphNode struct {
	Type       string
	Label      string
	Properties map[string]string
}

// GraphEdge is one declared outgoing relation. To is a concept id inside the
// same bundle; Evidence is a free-form pointer at the material that justifies
// the relation (a source id, resource path or short note).
type GraphEdge struct {
	To         string
	Type       string
	Evidence   string
	Properties map[string]string
}

// GraphDecl is the whole graph declaration of one concept.
type GraphDecl struct {
	Node  GraphNode
	Edges []GraphEdge
}

// Empty reports whether the document declares nothing graph-related.
func (g GraphDecl) Empty() bool {
	return g.Node.Type == "" && g.Node.Label == "" && len(g.Node.Properties) == 0 && len(g.Edges) == 0
}

// NodeType declares one legal node type.
//
// The JSON tags keep `/v1/graph/schema` in the same snake_case shape as every
// other response instead of leaking Go field names onto the wire. Frontmatter
// parsing is manual (see Document.GraphSchema), so the tags are wire-only.
type NodeType struct {
	Description string `json:"description,omitempty"`
}

// EdgeType declares one legal relation and the node types it may connect.
type EdgeType struct {
	From        []string `json:"from,omitempty"`
	To          []string `json:"to,omitempty"`
	Description string   `json:"description,omitempty"`
}

// GraphSchema is the typed ontology declared by ontology-schema documents.
type GraphSchema struct {
	NodeTypes map[string]NodeType `json:"node_types"`
	EdgeTypes map[string]EdgeType `json:"edge_types"`
}

// Empty reports whether the schema declares nothing.
func (s GraphSchema) Empty() bool {
	return len(s.NodeTypes) == 0 && len(s.EdgeTypes) == 0
}

type graphNodeOut struct {
	Type       string            `yaml:"type,omitempty"`
	Label      string            `yaml:"label,omitempty"`
	Properties map[string]string `yaml:"properties,omitempty"`
}

type graphEdgeOut struct {
	To         string            `yaml:"to"`
	Type       string            `yaml:"type"`
	Evidence   string            `yaml:"evidence,omitempty"`
	Properties map[string]string `yaml:"properties,omitempty"`
}

type graphDeclOut struct {
	Node  *graphNodeOut  `yaml:"node,omitempty"`
	Edges []graphEdgeOut `yaml:"edges,omitempty"`
}

type nodeTypeOut struct {
	Description string `yaml:"description,omitempty"`
}

type edgeTypeOut struct {
	From        []string `yaml:"from,omitempty"`
	To          []string `yaml:"to,omitempty"`
	Description string   `yaml:"description,omitempty"`
}

type graphSchemaOut struct {
	NodeTypes map[string]nodeTypeOut `yaml:"node_types,omitempty"`
	EdgeTypes map[string]edgeTypeOut `yaml:"edge_types,omitempty"`
}

// Graph parses and validates the document's graph declaration. A missing
// declaration is not an error; a malformed one is, because the writer asked for
// a structural promise that cannot be honoured.
func (d *Document) Graph() (GraphDecl, error) {
	n := d.extraNode(graphKey)
	if n == nil {
		return GraphDecl{}, nil
	}
	if n.Kind != yaml.MappingNode {
		return GraphDecl{}, fmt.Errorf("okf: frontmatter key 'graph' must be a mapping")
	}
	var out GraphDecl
	if node := mappingValue(n, "node"); node != nil {
		if node.Kind != yaml.MappingNode {
			return GraphDecl{}, fmt.Errorf("okf: graph.node must be a mapping")
		}
		props, err := parseProperties("graph.node.properties", mappingValue(node, "properties"))
		if err != nil {
			return GraphDecl{}, err
		}
		out.Node = GraphNode{
			Type:       strings.TrimSpace(scalar(mappingValue(node, "type"))),
			Label:      strings.TrimSpace(scalar(mappingValue(node, "label"))),
			Properties: props,
		}
	}
	if edges := mappingValue(n, "edges"); edges != nil {
		if edges.Kind != yaml.SequenceNode {
			return GraphDecl{}, fmt.Errorf("okf: graph.edges must be a list")
		}
		for _, item := range edges.Content {
			if item.Kind != yaml.MappingNode {
				return GraphDecl{}, fmt.Errorf("okf: graph.edges entries must be mappings")
			}
			props, err := parseProperties(fmt.Sprintf("graph.edges[%d].properties", len(out.Edges)), mappingValue(item, "properties"))
			if err != nil {
				return GraphDecl{}, err
			}
			out.Edges = append(out.Edges, GraphEdge{
				To:         strings.TrimSpace(scalar(mappingValue(item, "to"))),
				Type:       strings.TrimSpace(scalar(mappingValue(item, "type"))),
				Evidence:   strings.TrimSpace(scalar(mappingValue(item, "evidence"))),
				Properties: props,
			})
		}
	}
	if err := out.normalize(); err != nil {
		return GraphDecl{}, err
	}
	return out, nil
}

// SetGraph validates and stores a graph declaration, replacing any previous one.
func (d *Document) SetGraph(decl GraphDecl) error {
	if err := decl.normalize(); err != nil {
		return err
	}
	if decl.Empty() {
		d.clearExtra(graphKey)
		return nil
	}
	out := graphDeclOut{Edges: make([]graphEdgeOut, 0, len(decl.Edges))}
	if decl.Node.Type != "" || decl.Node.Label != "" || len(decl.Node.Properties) > 0 {
		out.Node = &graphNodeOut{
			Type: decl.Node.Type, Label: decl.Node.Label, Properties: decl.Node.Properties,
		}
	}
	for _, e := range decl.Edges {
		out.Edges = append(out.Edges, graphEdgeOut{
			To: e.To, Type: e.Type, Evidence: e.Evidence, Properties: e.Properties,
		})
	}
	n, err := nodeOf(out)
	if err != nil {
		return fmt.Errorf("okf: encode graph declaration: %w", err)
	}
	d.setExtraNode(graphKey, n)
	return nil
}

// GraphSchema parses and validates the document's ontology schema declaration.
func (d *Document) GraphSchema() (GraphSchema, error) {
	n := d.extraNode(graphSchemaKey)
	if n == nil {
		return GraphSchema{}, nil
	}
	if n.Kind != yaml.MappingNode {
		return GraphSchema{}, fmt.Errorf("okf: frontmatter key 'graph_schema' must be a mapping")
	}
	out := GraphSchema{}
	if types := mappingValue(n, "node_types"); types != nil {
		if types.Kind != yaml.MappingNode {
			return GraphSchema{}, fmt.Errorf("okf: graph_schema.node_types must be a mapping")
		}
		if len(types.Content)/2 > maxGraphTypes {
			return GraphSchema{}, fmt.Errorf("okf: graph_schema declares more than %d node types", maxGraphTypes)
		}
		out.NodeTypes = map[string]NodeType{}
		for i := 0; i+1 < len(types.Content); i += 2 {
			name := strings.TrimSpace(types.Content[i].Value)
			if err := validateGraphIdent("node type", name); err != nil {
				return GraphSchema{}, err
			}
			v := types.Content[i+1]
			if v.Kind != yaml.MappingNode {
				return GraphSchema{}, fmt.Errorf("okf: graph_schema.node_types.%s must be a mapping", name)
			}
			out.NodeTypes[name] = NodeType{Description: scalar(mappingValue(v, "description"))}
		}
	}
	if types := mappingValue(n, "edge_types"); types != nil {
		if types.Kind != yaml.MappingNode {
			return GraphSchema{}, fmt.Errorf("okf: graph_schema.edge_types must be a mapping")
		}
		if len(types.Content)/2 > maxGraphTypes {
			return GraphSchema{}, fmt.Errorf("okf: graph_schema declares more than %d edge types", maxGraphTypes)
		}
		out.EdgeTypes = map[string]EdgeType{}
		for i := 0; i+1 < len(types.Content); i += 2 {
			name := strings.TrimSpace(types.Content[i].Value)
			if err := validateGraphIdent("edge type", name); err != nil {
				return GraphSchema{}, err
			}
			v := types.Content[i+1]
			if v.Kind != yaml.MappingNode {
				return GraphSchema{}, fmt.Errorf("okf: graph_schema.edge_types.%s must be a mapping", name)
			}
			et := EdgeType{Description: scalar(mappingValue(v, "description"))}
			for _, end := range []struct {
				key  string
				dest *[]string
			}{{"from", &et.From}, {"to", &et.To}} {
				list := mappingValue(v, end.key)
				if list == nil {
					continue
				}
				if list.Kind != yaml.SequenceNode {
					return GraphSchema{}, fmt.Errorf("okf: graph_schema.edge_types.%s.%s must be a list", name, end.key)
				}
				for _, c := range list.Content {
					t := strings.TrimSpace(scalar(c))
					if err := validateGraphIdent("node type", t); err != nil {
						return GraphSchema{}, err
					}
					*end.dest = append(*end.dest, t)
				}
			}
			out.EdgeTypes[name] = et
		}
	}
	return out, nil
}

// SetGraphSchema validates and stores a typed ontology schema.
func (d *Document) SetGraphSchema(s GraphSchema) error {
	if len(s.NodeTypes) > maxGraphTypes || len(s.EdgeTypes) > maxGraphTypes {
		return fmt.Errorf("okf: graph_schema exceeds %d declared types", maxGraphTypes)
	}
	out := graphSchemaOut{}
	if len(s.NodeTypes) > 0 {
		out.NodeTypes = make(map[string]nodeTypeOut, len(s.NodeTypes))
		for name, nt := range s.NodeTypes {
			if err := validateGraphIdent("node type", name); err != nil {
				return err
			}
			out.NodeTypes[name] = nodeTypeOut{Description: nt.Description}
		}
	}
	if len(s.EdgeTypes) > 0 {
		out.EdgeTypes = make(map[string]edgeTypeOut, len(s.EdgeTypes))
		for name, et := range s.EdgeTypes {
			if err := validateGraphIdent("edge type", name); err != nil {
				return err
			}
			for _, t := range append(append([]string{}, et.From...), et.To...) {
				if err := validateGraphIdent("node type", t); err != nil {
					return err
				}
			}
			out.EdgeTypes[name] = edgeTypeOut{From: et.From, To: et.To, Description: et.Description}
		}
	}
	if s.Empty() {
		d.clearExtra(graphSchemaKey)
		return nil
	}
	n, err := nodeOf(out)
	if err != nil {
		return fmt.Errorf("okf: encode graph schema: %w", err)
	}
	d.setExtraNode(graphSchemaKey, n)
	return nil
}

// normalize applies the structural budgets and canonical formatting shared by
// the parser and the setter, so both sides reject the same malformed input.
func (g *GraphDecl) normalize() error {
	if len(g.Edges) > maxGraphEdges {
		return fmt.Errorf("okf: graph declares more than %d edges", maxGraphEdges)
	}
	if err := validateGraphIdent("node type", g.Node.Type); err != nil {
		return err
	}
	if len(g.Node.Label) > maxGraphValueBytes {
		return fmt.Errorf("okf: graph.node.label exceeds %d bytes", maxGraphValueBytes)
	}
	if err := normalizeProperties("graph.node.properties", g.Node.Properties); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i := range g.Edges {
		e := &g.Edges[i]
		clean, err := ValidateConceptID(e.To)
		if err != nil {
			return fmt.Errorf("okf: graph.edges[%d].to: %w", i, err)
		}
		e.To = clean
		if e.Type == "" {
			return fmt.Errorf("okf: graph.edges[%d].type is required", i)
		}
		if err := validateGraphIdent("edge type", e.Type); err != nil {
			return err
		}
		if len(e.Evidence) > maxGraphEvidence {
			return fmt.Errorf("okf: graph.edges[%d].evidence exceeds %d bytes", i, maxGraphEvidence)
		}
		if err := normalizeProperties(fmt.Sprintf("graph.edges[%d].properties", i), e.Properties); err != nil {
			return err
		}
		key := e.Type + "\x00" + e.To
		if seen[key] {
			return fmt.Errorf("okf: graph declares duplicate edge %q -> %q", e.Type, e.To)
		}
		seen[key] = true
	}
	return nil
}

func validateGraphIdent(kind, v string) error {
	if v == "" {
		return nil
	}
	if len(v) > maxGraphIdentBytes {
		return fmt.Errorf("okf: %s exceeds %d bytes", kind, maxGraphIdentBytes)
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.', r == ':':
		default:
			return fmt.Errorf("okf: %s %q contains an illegal character", kind, v)
		}
	}
	return nil
}

func normalizeProperties(field string, props map[string]string) error {
	if len(props) > maxGraphProperties {
		return fmt.Errorf("okf: %s declares more than %d properties", field, maxGraphProperties)
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.TrimSpace(k) == "" || len(k) > maxGraphIdentBytes {
			return fmt.Errorf("okf: %s has an invalid property key %q", field, k)
		}
		if len(props[k]) > maxGraphValueBytes {
			return fmt.Errorf("okf: %s.%s exceeds %d bytes", field, k, maxGraphValueBytes)
		}
	}
	return nil
}

func parseProperties(field string, n *yaml.Node) (map[string]string, error) {
	if n == nil {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("okf: %s must be a mapping", field)
	}
	out := make(map[string]string, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = scalar(n.Content[i+1])
	}
	return out, nil
}
