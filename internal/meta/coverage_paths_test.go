package meta

import (
	"context"
	"errors"
	"testing"
)

// scriptedWalk answers each call from a fixed list, so a traversal can be
// driven through branches a real store would rarely produce.
type scriptedWalk struct {
	calls   int
	results []GraphResult
	errs    []error
}

func (w *scriptedWalk) walk(context.Context, GraphWalk) (GraphResult, error) {
	index := w.calls
	w.calls++
	if index < len(w.errs) && w.errs[index] != nil {
		return GraphResult{}, w.errs[index]
	}
	if index < len(w.results) {
		return w.results[index], nil
	}
	return GraphResult{}, nil
}

func TestShortestPathLevelError(t *testing.T) {
	script := &scriptedWalk{
		results: []GraphResult{{Nodes: []GraphNodeRecord{{ConceptID: "a"}}}},
		errs:    []error{nil, errors.New("level refused")},
	}
	if _, err := shortestPathByLevel(context.Background(), script.walk, GraphPath{From: "a", To: "b"}); err == nil {
		t.Fatal("a failed level walk must surface")
	}
}

func TestShortestPathDirectionFilters(t *testing.T) {
	// An incoming edge during an "out" walk is skipped.
	out := &scriptedWalk{results: []GraphResult{
		{Nodes: []GraphNodeRecord{{ConceptID: "a"}}},
		{Nodes: []GraphNodeRecord{{ConceptID: "a"}, {ConceptID: "b"}},
			Edges: []GraphEdgeRecord{{FromID: "b", ToID: "a", EdgeType: "related"}}},
	}}
	result, err := shortestPathByLevel(context.Background(), out.walk, GraphPath{From: "a", To: "b", Direction: "out"})
	if err != nil || result.Found {
		t.Fatalf("out = %+v, %v", result, err)
	}

	// An outgoing edge during an "in" walk is skipped.
	in := &scriptedWalk{results: []GraphResult{
		{Nodes: []GraphNodeRecord{{ConceptID: "a"}}},
		{Nodes: []GraphNodeRecord{{ConceptID: "a"}, {ConceptID: "b"}},
			Edges: []GraphEdgeRecord{{FromID: "a", ToID: "b", EdgeType: "related"}}},
	}}
	result, err = shortestPathByLevel(context.Background(), in.walk, GraphPath{From: "a", To: "b", Direction: "in"})
	if err != nil || result.Found {
		t.Fatalf("in = %+v, %v", result, err)
	}
}

func TestShortestPathSkipsNodesOutsideTheBudget(t *testing.T) {
	script := &scriptedWalk{results: []GraphResult{
		{Nodes: []GraphNodeRecord{{ConceptID: "a"}}},
		// The edge points at "b" but the node budget did not include it.
		{Nodes: []GraphNodeRecord{{ConceptID: "a"}},
			Edges: []GraphEdgeRecord{{FromID: "a", ToID: "b", EdgeType: "related"}}},
	}}
	result, err := shortestPathByLevel(context.Background(), script.walk, GraphPath{From: "a", To: "b"})
	if err != nil || result.Found {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestShortestPathFindsTheTarget(t *testing.T) {
	script := &scriptedWalk{results: []GraphResult{
		{Nodes: []GraphNodeRecord{{ConceptID: "a"}}},
		{Nodes: []GraphNodeRecord{{ConceptID: "a"}, {ConceptID: "b"}},
			Edges: []GraphEdgeRecord{{FromID: "a", ToID: "b", EdgeType: "related"}}},
	}}
	result, err := shortestPathByLevel(context.Background(), script.walk, GraphPath{From: "a", To: "b"})
	if err != nil || !result.Found || len(result.Nodes) != 2 || len(result.Edges) != 1 {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestBuildPathReportsABrokenChain(t *testing.T) {
	parent := map[string]GraphEdgeRecord{"b": {FromID: "x", ToID: "b"}}
	result := buildPath("a", "b", parent, map[string]GraphNodeRecord{})
	if result.Found {
		t.Fatalf("result = %+v", result)
	}
}

func TestNodeByIDFallsBackToAnEmptyRecord(t *testing.T) {
	if got := nodeByID(nil, "a"); got.ConceptID != "a" {
		t.Fatalf("node = %+v", got)
	}
	if got := nodeByID([]GraphNodeRecord{{ConceptID: "a", Label: "A"}}, "a"); got.Label != "A" {
		t.Fatalf("node = %+v", got)
	}
}

func TestVisibilityAllows(t *testing.T) {
	cases := []struct {
		subject, visibility, owner string
		want                       bool
	}{
		{"", "public", "", true},
		{"alice", "team", "bob", true},
		{"", "team", "bob", false},
		{"alice", "private", "alice", true},
		{"bob", "private", "alice", false},
		{"", "mystery", "", false},
	}
	for _, tc := range cases {
		if got := visibilityAllows(tc.subject, tc.visibility, tc.owner); got != tc.want {
			t.Fatalf("%s/%s/%s = %v", tc.subject, tc.visibility, tc.owner, got)
		}
	}
}

func TestStringMapScanAcceptsText(t *testing.T) {
	var m StringMap
	if err := m.Scan(`{"a":"b"}`); err != nil || m["a"] != "b" {
		t.Fatalf("string source = %+v, %v", m, err)
	}
	var raw StringMap
	if err := raw.Scan([]byte(`{"a":"b"}`)); err != nil || raw["a"] != "b" {
		t.Fatalf("byte source = %+v, %v", raw, err)
	}
}

func TestFakeGraphHonoursCancellation(t *testing.T) {
	store := NewFakeStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := map[string]func() error{
		"neighbors": func() error {
			_, err := store.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}})
			return err
		},
		"edges": func() error {
			_, err := store.GraphEdges(ctx, []string{"a"}, "both")
			return err
		},
		"search": func() error {
			_, err := store.SearchGraphNodes(ctx, GraphSearch{Text: "x"})
			return err
		},
		"stale": func() error {
			_, err := store.StaleGraphNodes(ctx, []string{"a"})
			return err
		},
		"stats": func() error {
			_, err := store.GraphStats(ctx, "alice")
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
}

func TestFakeGraphDeduplicatesSourcesAndFilters(t *testing.T) {
	store := NewFakeStore()
	if err := store.UpsertGraphNode(context.Background(), GraphNodeRecord{
		ConceptID: "a", NodeType: "metric", Visibility: "public",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGraphNode(context.Background(), GraphNodeRecord{
		ConceptID: "b", NodeType: "table", Visibility: "public",
	}, []GraphEdgeRecord{{FromID: "a", ToID: "b", EdgeType: "related"}}); err != nil {
		t.Fatal(err)
	}

	result, err := store.GraphNeighbors(context.Background(), GraphWalk{
		Sources: []string{"a", "a", "b"}, Depth: 1, NodeTypes: []string{"metric"},
	})
	if err != nil || len(result.Nodes) != 1 {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestFakeGraphSearchRanksAndTruncates(t *testing.T) {
	store := NewFakeStore()
	for id, label := range map[string]string{"c": "Same", "b": "Same", "a": "Same"} {
		if err := store.UpsertGraphNode(context.Background(), GraphNodeRecord{
			ConceptID: id, NodeType: "metric", Label: label, Visibility: "public",
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := store.SearchGraphNodes(context.Background(), GraphSearch{Text: "", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].ConceptID != "a" || nodes[1].ConceptID != "b" {
		t.Fatalf("nodes = %+v", nodes)
	}

	// Different ranks (exact vs prefix) exercise the rank comparison directly.
	for _, id := range []string{"ab", "abc"} {
		if err := store.UpsertGraphNode(context.Background(), GraphNodeRecord{
			ConceptID: id, NodeType: "metric", Label: id, Visibility: "public",
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err = store.SearchGraphNodes(context.Background(), GraphSearch{Text: "ab", Limit: 5})
	if err != nil || len(nodes) < 2 || nodes[0].ConceptID != "ab" {
		t.Fatalf("ranked = %+v, %v", nodes, err)
	}
}

func TestFakeGraphStatsIgnoresInvisibleEdges(t *testing.T) {
	store := NewFakeStore()
	if err := store.UpsertGraphNode(context.Background(), GraphNodeRecord{
		ConceptID: "a", NodeType: "metric", Visibility: "public",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGraphNode(context.Background(), GraphNodeRecord{
		ConceptID: "b", NodeType: "metric", Visibility: "private", Owner: "alice",
	}, []GraphEdgeRecord{{FromID: "a", ToID: "b", EdgeType: "related"}}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.GraphStats(context.Background(), "bob")
	if err != nil || stats.Nodes != 1 || stats.Edges != 0 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
}
