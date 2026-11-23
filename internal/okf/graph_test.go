package okf

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestGraphDeclParsesAndRoundTrips(t *testing.T) {
	src := `---
type: metric
title: Gross Margin
graph:
  node:
    label: Gross Margin
    properties:
      unit: percent
  edges:
    - { to: metrics/revenue, type: derived_from }
    - { to: tables/fact_sales, type: computed_from, evidence: "sources[0]" }
  x_unmodeled: keep-me
---
body
`
	d, err := ParseDocument([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := d.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if g.Node.Label != "Gross Margin" || g.Node.Properties["unit"] != "percent" {
		t.Fatalf("node = %+v", g.Node)
	}
	if len(g.Edges) != 2 || g.Edges[0].To != "metrics/revenue" || g.Edges[0].Type != "derived_from" {
		t.Fatalf("edges = %+v", g.Edges)
	}
	if g.Edges[1].Evidence != "sources[0]" {
		t.Fatalf("evidence lost: %+v", g.Edges[1])
	}
	if g.Empty() {
		t.Fatal("declaration must not be empty")
	}

	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseDocument(out)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := back.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if len(g2.Edges) != 2 || g2.Edges[1].To != "tables/fact_sales" || g2.Node.Label != "Gross Margin" {
		t.Fatalf("roundtrip = %+v", g2)
	}
	// Unknown sub-keys must survive verbatim rather than being dropped by the
	// typed accessor.
	if !strings.Contains(string(out), "x_unmodeled: keep-me") {
		t.Fatalf("unknown graph sub-key lost:\n%s", out)
	}
}

func TestGraphDeclMissingAndNodeTypeOverride(t *testing.T) {
	d := &Document{Type: "metric"}
	g, err := d.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if !g.Empty() {
		t.Fatalf("missing declaration must be empty: %+v", g)
	}

	d2, err := ParseDocument([]byte("---\ntype: metric\ngraph:\n  node:\n    type: table\n    label: Sales\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	g2, err := d2.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if g2.Node.Type != "table" {
		t.Fatalf("node type override = %q", g2.Node.Type)
	}
}

func TestSetGraphValidatesAndClears(t *testing.T) {
	d := &Document{Type: "metric"}
	decl := GraphDecl{
		Node:  GraphNode{Label: "Revenue", Properties: map[string]string{"unit": "usd"}},
		Edges: []GraphEdge{{To: "tables/orders.md", Type: "computed_from", Evidence: "sources[0]"}},
	}
	if err := d.SetGraph(decl); err != nil {
		t.Fatal(err)
	}
	data, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Targets are normalised to concept ids (no .md suffix) exactly like the
	// rest of the store.
	if !strings.Contains(string(data), "to: tables/orders\n") {
		t.Fatalf("target not normalised:\n%s", data)
	}
	if err := d.SetGraph(GraphDecl{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Extra()["graph"]; ok {
		t.Fatal("empty declaration must clear the key")
	}
	g, err := d.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if !g.Empty() {
		t.Fatal("cleared declaration must be empty")
	}
}

func TestGraphDeclRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"graph not mapping":  "---\ntype: T\ngraph: nope\n---\n",
		"node not mapping":   "---\ntype: T\ngraph:\n  node: nope\n---\n",
		"edges not list":     "---\ntype: T\ngraph:\n  edges: nope\n---\n",
		"edge not mapping":   "---\ntype: T\ngraph:\n  edges:\n    - nope\n---\n",
		"empty target":       "---\ntype: T\ngraph:\n  edges:\n    - type: derived_from\n---\n",
		"escaping target":    "---\ntype: T\ngraph:\n  edges:\n    - {to: ../escape, type: derived_from}\n---\n",
		"reserved target":    "---\ntype: T\ngraph:\n  edges:\n    - {to: log, type: derived_from}\n---\n",
		"missing edge type":  "---\ntype: T\ngraph:\n  edges:\n    - {to: a/b}\n---\n",
		"illegal edge type":  "---\ntype: T\ngraph:\n  edges:\n    - {to: a/b, type: \"bad type\"}\n---\n",
		"duplicate edge":     "---\ntype: T\ngraph:\n  edges:\n    - {to: a/b, type: t}\n    - {to: a/b, type: t}\n---\n",
		"illegal node type":  "---\ntype: T\ngraph:\n  node:\n    type: \"bad type\"\n---\n",
		"properties mapping": "---\ntype: T\ngraph:\n  node:\n    properties: nope\n---\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := ParseDocument([]byte(src))
			if err != nil {
				t.Fatalf("permissive parse must succeed: %v", err)
			}
			if _, err := d.Graph(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestGraphDeclBudgets(t *testing.T) {
	propKey := strings.Repeat("k", maxGraphIdentBytes+1)
	cases := map[string]GraphDecl{
		"too many edges": {Edges: make([]GraphEdge, maxGraphEdges+1)},
		"long label":     {Node: GraphNode{Label: strings.Repeat("x", maxGraphValueBytes+1)}},
		"long evidence": {Edges: []GraphEdge{
			{To: "a/b", Type: "t", Evidence: strings.Repeat("x", maxGraphEvidence+1)},
		}},
		"too many properties": {Node: GraphNode{Properties: manyProps(maxGraphProperties + 1)}},
		"long property value": {Node: GraphNode{Properties: map[string]string{
			"k": strings.Repeat("x", maxGraphValueBytes+1),
		}}},
		"long property key":  {Node: GraphNode{Properties: map[string]string{propKey: "v"}}},
		"blank property key": {Node: GraphNode{Properties: map[string]string{" ": "v"}}},
		"long node type":     {Node: GraphNode{Type: strings.Repeat("t", maxGraphIdentBytes+1)}},
	}
	for name, decl := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (&Document{Type: "T"}).SetGraph(decl); err == nil {
				t.Fatal("expected budget error")
			}
		})
	}
}

func TestGraphSchemaParsesAndRoundTrips(t *testing.T) {
	src := `---
type: ontology-schema
graph_schema:
  node_types:
    metric: { description: A named measure }
    table: {}
  edge_types:
    derived_from: { from: [metric], to: [metric] }
    computed_from: { from: [metric], to: [table], description: Column level }
---
`
	d, err := ParseDocument([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	s, err := d.GraphSchema()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.NodeTypes) != 2 || s.NodeTypes["metric"].Description != "A named measure" {
		t.Fatalf("node types = %+v", s.NodeTypes)
	}
	if len(s.EdgeTypes) != 2 || s.EdgeTypes["computed_from"].To[0] != "table" {
		t.Fatalf("edge types = %+v", s.EdgeTypes)
	}
	if s.Empty() {
		t.Fatal("schema must not be empty")
	}

	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseDocument(out)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := back.GraphSchema()
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.EdgeTypes) != 2 || len(s2.NodeTypes) != 2 {
		t.Fatalf("roundtrip = %+v", s2)
	}
}

func TestSetGraphSchemaValidatesAndClears(t *testing.T) {
	d := &Document{Type: "ontology-schema"}
	s := GraphSchema{
		NodeTypes: map[string]NodeType{"metric": {}},
		EdgeTypes: map[string]EdgeType{"derived_from": {From: []string{"metric"}, To: []string{"metric"}}},
	}
	if err := d.SetGraphSchema(s); err != nil {
		t.Fatal(err)
	}
	data, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "graph_schema:") {
		t.Fatalf("schema not serialised:\n%s", data)
	}
	if err := d.SetGraphSchema(GraphSchema{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Extra()["graph_schema"]; ok {
		t.Fatal("empty schema must clear the key")
	}

	if err := (&Document{Type: "T"}).SetGraphSchema(GraphSchema{
		NodeTypes: map[string]NodeType{"bad type": {}},
	}); err == nil {
		t.Fatal("illegal node type must be rejected")
	}
	if err := (&Document{Type: "T"}).SetGraphSchema(GraphSchema{
		EdgeTypes: map[string]EdgeType{"bad type": {}},
	}); err == nil {
		t.Fatal("illegal edge type must be rejected")
	}
	if err := (&Document{Type: "T"}).SetGraphSchema(GraphSchema{
		EdgeTypes: map[string]EdgeType{"t": {From: []string{"bad type"}}},
	}); err == nil {
		t.Fatal("illegal endpoint type must be rejected")
	}
	many := make(map[string]NodeType, maxGraphTypes+1)
	for i := 0; i <= maxGraphTypes; i++ {
		many[fmt.Sprintf("t%d", i)] = NodeType{}
	}
	if err := (&Document{Type: "T"}).SetGraphSchema(GraphSchema{NodeTypes: many}); err == nil {
		t.Fatal("type budget must be enforced")
	}
}

func TestGraphSchemaRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"schema not mapping":     "---\ntype: T\ngraph_schema: nope\n---\n",
		"node_types not mapping": "---\ntype: T\ngraph_schema:\n  node_types: nope\n---\n",
		"node type not mapping":  "---\ntype: T\ngraph_schema:\n  node_types:\n    metric: nope\n---\n",
		"edge_types not mapping": "---\ntype: T\ngraph_schema:\n  edge_types: nope\n---\n",
		"edge type not mapping":  "---\ntype: T\ngraph_schema:\n  edge_types:\n    t: nope\n---\n",
		"from not list":          "---\ntype: T\ngraph_schema:\n  edge_types:\n    t: {from: nope}\n---\n",
		"illegal node type":      "---\ntype: T\ngraph_schema:\n  node_types:\n    \"bad type\": {}\n---\n",
		"illegal endpoint":       "---\ntype: T\ngraph_schema:\n  edge_types:\n    t: {from: [\"bad type\"]}\n---\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := ParseDocument([]byte(src))
			if err != nil {
				t.Fatalf("permissive parse must succeed: %v", err)
			}
			if _, err := d.GraphSchema(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestGraphSchemaTypeBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\ntype: T\ngraph_schema:\n  node_types:\n")
	for i := 0; i <= maxGraphTypes; i++ {
		fmt.Fprintf(&b, "    t%d: {}\n", i)
	}
	b.WriteString("---\n")
	d, err := ParseDocument([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.GraphSchema(); err == nil {
		t.Fatal("expected node type budget error")
	}
}

func manyProps(n int) map[string]string {
	out := make(map[string]string, n)
	for i := 0; i < n; i++ {
		out[strings.Repeat("p", i%8+1)+string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}
	return out
}

// minIdent keeps generated type names unique and legal.
func minIdent(i int) int { return 1 + i%8 }

func TestGraphSchemaJSONMatchesTheWireContract(t *testing.T) {
	schema := GraphSchema{
		NodeTypes: map[string]NodeType{"metric": {Description: "a measure"}},
		EdgeTypes: map[string]EdgeType{"derived_from": {From: []string{"metric"}, To: []string{"metric"}}},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"node_types":{"metric":{"description":"a measure"}},` +
		`"edge_types":{"derived_from":{"from":["metric"],"to":["metric"]}}}`
	if string(data) != want {
		t.Fatalf("schema wire shape drifted:\n got %s\nwant %s", data, want)
	}
}
