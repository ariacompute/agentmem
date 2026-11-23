package okf

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEncodeNodeFailureIsReported keeps the encoder seam honest: every caller
// that stores a YAML node must surface a failure instead of writing nil.
func TestEncodeNodeFailureIsReported(t *testing.T) {
	previous := encodeNode
	encodeNode = func(any) (*yaml.Node, error) { return nil, errors.New("encode refused") }
	t.Cleanup(func() { encodeNode = previous })

	doc, err := ParseDocument([]byte("---\ntype: Concept\ncustom: value\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Marshal(); err == nil {
		t.Fatal("Marshal must surface an encoder failure")
	}
	if err := doc.SetGraph(GraphDecl{Node: GraphNode{Type: "metric"}, Edges: []GraphEdge{{To: "a", Type: "related"}}}); err == nil {
		t.Fatal("SetGraph must surface an encoder failure")
	}
	if err := doc.SetGraphSchema(GraphSchema{NodeTypes: map[string]NodeType{"metric": {}}}); err == nil {
		t.Fatal("SetGraphSchema must surface an encoder failure")
	}
}

func TestGraphSchemaAbsentIsEmpty(t *testing.T) {
	doc, err := ParseDocument([]byte("---\ntype: Concept\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := doc.GraphSchema()
	if err != nil || !schema.Empty() {
		t.Fatalf("schema = %+v, %v", schema, err)
	}
}

func TestGraphSchemaRejectsTooManyEdgeTypes(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\ntype: ontology-schema\ngraph_schema:\n  edge_types:\n")
	for i := 0; i < maxGraphTypes+1; i++ {
		fmt.Fprintf(&b, "    edge_%d:\n      from: [metric]\n      to: [metric]\n", i)
	}
	b.WriteString("---\nbody\n")
	doc, err := ParseDocument([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.GraphSchema(); err == nil {
		t.Fatal("an oversized schema must be rejected")
	}
}

func TestGraphSchemaRejectsMalformedEntries(t *testing.T) {
	cases := map[string]string{
		"invalid edge type":  "---\ntype: ontology-schema\ngraph_schema:\n  edge_types:\n    \"bad name\":\n      from: [metric]\n---\nbody\n",
		"missing endpoints":  "---\ntype: ontology-schema\ngraph_schema:\n  edge_types:\n    related:\n      description: no endpoints\n---\nbody\n",
		"non-mapping schema": "---\ntype: ontology-schema\ngraph_schema: []\n---\nbody\n",
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			doc, err := ParseDocument([]byte(document))
			if err != nil {
				t.Fatal(err)
			}
			schema, err := doc.GraphSchema()
			if name == "missing endpoints" {
				// Absent endpoints are permissive; the entry is still parsed.
				if err != nil || len(schema.EdgeTypes) != 1 {
					t.Fatalf("schema = %+v, %v", schema, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("schema = %+v", schema)
			}
		})
	}
}

func TestGraphParseRejectsInvalidEdgeProperties(t *testing.T) {
	// A list is rejected while parsing properties, before the declaration is
	// normalised.
	document := "---\ntype: Concept\ngraph:\n  edges:\n    - to: other\n      type: related\n      properties: [a, b]\n---\nbody\n"
	doc, err := ParseDocument([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Graph(); err == nil {
		t.Fatal("invalid edge properties must be rejected")
	}
}

func TestSetGraphRejectsInvalidEdgeProperties(t *testing.T) {
	doc, err := ParseDocument([]byte("---\ntype: Concept\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = doc.SetGraph(GraphDecl{
		Node:  GraphNode{Type: "metric"},
		Edges: []GraphEdge{{To: "other", Type: "related", Properties: map[string]string{"": "v"}}},
	})
	if err == nil {
		t.Fatal("invalid properties must be rejected")
	}
}

func TestClearExtraOnAnEmptyDocument(t *testing.T) {
	doc := &Document{}
	if err := doc.SetGraphSchema(GraphSchema{}); err != nil {
		t.Fatalf("empty schema on a bare document = %v", err)
	}
}
