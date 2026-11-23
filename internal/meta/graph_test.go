package meta

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
)

func graphFixture(t *testing.T) *FakeStore {
	t.Helper()
	ctx := context.Background()
	f := NewFakeStore()
	nodes := []GraphNodeRecord{
		{ConceptID: "metrics/revenue", NodeType: "metric", Label: "Revenue", Description: "Net revenue", Visibility: "public", ContentHash: "h1"},
		{ConceptID: "metrics/margin", NodeType: "metric", Label: "Gross Margin", Visibility: "public", ContentHash: "h2"},
		{ConceptID: "tables/orders", NodeType: "table", Label: "Orders", Visibility: "public", ContentHash: "h3"},
		{ConceptID: "secret/plan", NodeType: "metric", Label: "Secret Plan", Visibility: "private", Owner: "alice", ContentHash: "h4"},
	}
	for _, n := range nodes {
		if err := f.UpsertConcept(ctx, ConceptMeta{ConceptID: n.ConceptID, ContentHash: n.ContentHash, Visibility: n.Visibility, Owner: n.Owner, Status: "stable"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.UpsertGraphNode(ctx, nodes[0], []GraphEdgeRecord{
		{FromID: "metrics/revenue", ToID: "metrics/margin", EdgeType: "derived_from", Origin: EdgeOriginDeclared, Evidence: "sources[0]"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.UpsertGraphNode(ctx, nodes[1], []GraphEdgeRecord{
		{FromID: "metrics/margin", ToID: "tables/orders", EdgeType: "computed_from", Origin: EdgeOriginDeclared},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.UpsertGraphNode(ctx, nodes[2], nil); err != nil {
		t.Fatal(err)
	}
	if err := f.UpsertGraphNode(ctx, nodes[3], nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFakeGraphNodeLifecycle(t *testing.T) {
	ctx := context.Background()
	f := graphFixture(t)
	n, err := f.GetGraphNode(ctx, "metrics/revenue")
	if err != nil || n.Label != "Revenue" || n.Properties == nil {
		t.Fatalf("get = %+v (%v)", n, err)
	}
	if _, err := f.GetGraphNode(ctx, "missing"); !IsNotFound(err) {
		t.Fatalf("missing node error = %v", err)
	}
	// Re-upserting a node replaces its declared edges wholesale.
	if err := f.UpsertGraphNode(ctx, n, nil); err != nil {
		t.Fatal(err)
	}
	res, err := f.GraphNeighbors(ctx, GraphWalk{Sources: []string{"metrics/revenue"}, Depth: 1, Direction: "out"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || len(res.Edges) != 0 {
		t.Fatalf("edges not replaced: %+v", res)
	}
	// Deleting a node also removes every edge that touches it.
	if err := f.DeleteGraphNode(ctx, "metrics/margin"); err != nil {
		t.Fatal(err)
	}
	res, _ = f.GraphNeighbors(ctx, GraphWalk{Sources: []string{"metrics/revenue"}, Depth: 2, Direction: "both"})
	if len(res.Nodes) != 1 || len(res.Edges) != 0 {
		t.Fatalf("edges survived delete: %+v", res)
	}
}

func TestFakeGraphWalkDirectionsAndFilters(t *testing.T) {
	ctx := context.Background()
	f := graphFixture(t)
	cases := []struct {
		name string
		q    GraphWalk
		want []string
	}{
		{"out depth 1", GraphWalk{Sources: []string{"metrics/revenue"}, Depth: 1, Direction: "out"}, []string{"metrics/margin", "metrics/revenue"}},
		{"in depth 1", GraphWalk{Sources: []string{"metrics/margin"}, Depth: 1, Direction: "in"}, []string{"metrics/margin", "metrics/revenue"}},
		{"both depth 2", GraphWalk{Sources: []string{"metrics/revenue"}, Depth: 2, Direction: "both"}, []string{"metrics/margin", "metrics/revenue", "tables/orders"}},
		{"edge type filter", GraphWalk{Sources: []string{"metrics/revenue"}, Depth: 2, Direction: "out", EdgeTypes: []string{"computed_from"}}, []string{"metrics/revenue"}},
		{"node type filter", GraphWalk{Sources: []string{"metrics/revenue"}, Depth: 2, Direction: "both", NodeTypes: []string{"table"}}, []string{"tables/orders"}},
		{"depth zero", GraphWalk{Sources: []string{"metrics/revenue"}}, []string{"metrics/revenue"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := f.GraphNeighbors(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(res.Nodes))
			for _, n := range res.Nodes {
				got = append(got, n.ConceptID)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("nodes = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("nodes = %v, want %v", got, tc.want)
				}
			}
			if res.Depths == nil {
				t.Fatal("depths must be populated")
			}
		})
	}
	if res, _ := f.GraphNeighbors(ctx, GraphWalk{Sources: []string{" "}}); len(res.Nodes) != 0 {
		t.Fatalf("blank source must be ignored: %+v", res)
	}
}

func TestFakeGraphWalkTruncatesAndSkipsDangling(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	if err := f.UpsertGraphNode(ctx, GraphNodeRecord{ConceptID: "a", NodeType: "concept", Visibility: "public"}, []GraphEdgeRecord{
		{FromID: "a", ToID: "b", EdgeType: "related"},
		{FromID: "a", ToID: "missing", EdgeType: "related"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.UpsertGraphNode(ctx, GraphNodeRecord{ConceptID: "b", NodeType: "concept", Visibility: "public"}, []GraphEdgeRecord{
		{FromID: "b", ToID: "c", EdgeType: "related"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.UpsertGraphNode(ctx, GraphNodeRecord{ConceptID: "c", NodeType: "concept", Visibility: "public"}, nil); err != nil {
		t.Fatal(err)
	}
	res, err := f.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}, Depth: 2, MaxNodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || len(res.Nodes) != 2 {
		t.Fatalf("truncation = %+v", res)
	}
	res, err = f.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 2 {
		t.Fatalf("dangling target must be dropped: %+v", res)
	}
}

func TestFakeGraphEdgesIncludesDangling(t *testing.T) {
	ctx := context.Background()
	f := NewFakeStore()
	if err := f.UpsertGraphNode(ctx, GraphNodeRecord{ConceptID: "a", Visibility: "public"}, []GraphEdgeRecord{
		{FromID: "a", ToID: "b", EdgeType: "out_edge"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.UpsertGraphNode(ctx, GraphNodeRecord{ConceptID: "b", Visibility: "public"}, []GraphEdgeRecord{
		{FromID: "b", ToID: "c", EdgeType: "dangling"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := f.GraphEdges(ctx, []string{"b"}, "out")
	if err != nil || len(out) != 1 || out[0].EdgeType != "dangling" {
		t.Fatalf("dangling edges = %+v (%v)", out, err)
	}
	in, err := f.GraphEdges(ctx, []string{"a"}, "in")
	if err != nil || len(in) != 0 {
		t.Fatalf("incoming = %+v (%v)", in, err)
	}
	both, err := f.GraphEdges(ctx, []string{"a"}, "both")
	if err != nil || len(both) != 1 {
		t.Fatalf("both = %+v (%v)", both, err)
	}
	if got, err := f.GraphEdges(ctx, nil, "both"); err != nil || len(got) != 0 {
		t.Fatalf("empty ids = %+v (%v)", got, err)
	}
}

func TestFakeGraphStaleNodes(t *testing.T) {
	ctx := context.Background()
	f := graphFixture(t)
	stale, err := f.StaleGraphNodes(ctx, []string{"metrics/revenue", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale["metrics/revenue"] {
		t.Fatalf("stale = %+v", stale)
	}
	if _, err := f.StaleGraphNodes(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresGraphEdgesAndStaleNodes(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "graph_edges" WHERE from_id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"from_id", "to_id", "edge_type"}).AddRow("a", "b", "t"))
	if out, err := s.GraphEdges(ctx, []string{"a"}, "out"); err != nil || len(out) != 1 {
		t.Fatalf("out edges = %+v (%v)", out, err)
	}
	mock.ExpectQuery(`SELECT \* FROM "graph_edges" WHERE to_id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"from_id", "to_id", "edge_type"}))
	if out, err := s.GraphEdges(ctx, []string{"a"}, "in"); err != nil || len(out) != 0 {
		t.Fatalf("in edges = %+v (%v)", out, err)
	}
	mock.ExpectQuery(`SELECT \* FROM "graph_edges" WHERE from_id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"from_id", "to_id", "edge_type"}))
	if out, err := s.GraphEdges(ctx, []string{"a"}, "both"); err != nil || len(out) != 0 {
		t.Fatalf("both edges = %+v (%v)", out, err)
	}
	if out, err := s.GraphEdges(ctx, nil, "both"); err != nil || len(out) != 0 {
		t.Fatalf("empty ids = %+v (%v)", out, err)
	}

	mock.ExpectQuery(`LEFT JOIN concepts_meta`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id"}).AddRow("a"))
	stale, err := s.StaleGraphNodes(ctx, []string{"a", "b"})
	if err != nil || len(stale) != 2 || !stale["a"] || stale["b"] {
		t.Fatalf("stale = %+v (%v)", stale, err)
	}
	if stale, err := s.StaleGraphNodes(ctx, nil); err != nil || len(stale) != 0 {
		t.Fatalf("empty stale = %+v (%v)", stale, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFakeGraphShortestPath(t *testing.T) {
	ctx := context.Background()
	f := graphFixture(t)
	res, err := f.GraphShortestPath(ctx, GraphPath{From: "metrics/revenue", To: "tables/orders", MaxDepth: 3})
	if err != nil || !res.Found {
		t.Fatalf("path = %+v (%v)", res, err)
	}
	if len(res.Nodes) != 3 || res.Nodes[0].ConceptID != "metrics/revenue" || res.Nodes[2].ConceptID != "tables/orders" {
		t.Fatalf("path nodes = %+v", res.Nodes)
	}
	if len(res.Edges) != 2 || res.Edges[0].EdgeType != "derived_from" {
		t.Fatalf("path edges = %+v", res.Edges)
	}
	// Reverse direction must follow the declared orientation.
	if res, _ := f.GraphShortestPath(ctx, GraphPath{From: "tables/orders", To: "metrics/revenue", MaxDepth: 3, Direction: "out"}); res.Found {
		t.Fatalf("out-direction path should not exist: %+v", res)
	}
	if res, _ := f.GraphShortestPath(ctx, GraphPath{From: "tables/orders", To: "metrics/revenue", MaxDepth: 3, Direction: "in"}); !res.Found {
		t.Fatal("in-direction path should exist")
	}
	if res, _ := f.GraphShortestPath(ctx, GraphPath{From: "metrics/revenue", To: "metrics/revenue"}); !res.Found || len(res.Nodes) != 1 {
		t.Fatalf("self path = %+v", res)
	}
	if res, _ := f.GraphShortestPath(ctx, GraphPath{From: "metrics/revenue", To: "nope", MaxDepth: 2}); res.Found {
		t.Fatal("missing target must not be found")
	}
	if _, err := f.GraphShortestPath(ctx, GraphPath{}); err == nil {
		t.Fatal("empty endpoints must error")
	}
	if res, _ := f.GraphShortestPath(ctx, GraphPath{From: "metrics/revenue", To: "tables/orders", MaxDepth: 1}); res.Found {
		t.Fatal("depth limit must be honoured")
	}
}

func TestFakeGraphSearchRanksAndFilters(t *testing.T) {
	ctx := context.Background()
	f := graphFixture(t)
	res, err := f.SearchGraphNodes(ctx, GraphSearch{Text: "revenue"})
	if err != nil || len(res) != 1 || res[0].ConceptID != "metrics/revenue" {
		t.Fatalf("search = %+v (%v)", res, err)
	}
	res, _ = f.SearchGraphNodes(ctx, GraphSearch{Text: "metrics"})
	if len(res) != 2 {
		t.Fatalf("prefix search = %+v", res)
	}
	res, _ = f.SearchGraphNodes(ctx, GraphSearch{Text: "metrics", NodeTypes: []string{"table"}})
	if len(res) != 0 {
		t.Fatalf("node type filter = %+v", res)
	}
	res, _ = f.SearchGraphNodes(ctx, GraphSearch{NodeTypes: []string{"metric"}})
	if len(res) != 3 {
		t.Fatalf("type-only search = %+v", res)
	}
	res, _ = f.SearchGraphNodes(ctx, GraphSearch{Text: "orders", Limit: 1})
	if len(res) != 1 || res[0].ConceptID != "tables/orders" {
		t.Fatalf("limit = %+v", res)
	}
}

func TestFakeGraphStatsVisibilityAndStale(t *testing.T) {
	ctx := context.Background()
	f := graphFixture(t)
	stats, err := f.GraphStats(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Nodes != 3 || stats.Edges != 2 || stats.NodeTypes["metric"] != 2 || stats.EdgeTypes["derived_from"] != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.Stale != 0 {
		t.Fatalf("unexpected stale count = %+v", stats)
	}
	alice, _ := f.GraphStats(ctx, "alice")
	if alice.Nodes != 4 {
		t.Fatalf("owner stats = %+v", alice)
	}
	// A body change that the index has not seen marks the node stale.
	if err := f.UpsertConcept(ctx, ConceptMeta{ConceptID: "metrics/revenue", ContentHash: "new", Visibility: "public"}); err != nil {
		t.Fatal(err)
	}
	stats, _ = f.GraphStats(ctx, "bob")
	if stats.Stale != 1 {
		t.Fatalf("stale after body change = %+v", stats)
	}
	if err := f.DeleteConceptForTest(ctx, "metrics/margin"); err != nil {
		t.Fatal(err)
	}
	stats, _ = f.GraphStats(ctx, "bob")
	if stats.Stale != 2 || stats.Edges != 2 {
		t.Fatalf("stale after delete = %+v", stats)
	}
}

// DeleteConceptForTest removes only the metadata row so the "meta missing"
// branch of the staleness rule can be exercised offline.
func (f *FakeStore) DeleteConceptForTest(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.concepts, id)
	return ctx.Err()
}

func TestFakeGraphPrune(t *testing.T) {
	ctx := context.Background()
	f := graphFixture(t)
	removed, err := f.PruneGraphNodes(ctx, []string{"metrics/revenue", "metrics/margin", "tables/orders"})
	if err != nil || removed != 1 {
		t.Fatalf("prune removed=%d (%v)", removed, err)
	}
	stats, _ := f.GraphStats(ctx, "alice")
	if stats.Nodes != 3 {
		t.Fatalf("stats after prune = %+v", stats)
	}
	removed, err = f.PruneGraphNodes(ctx, nil)
	if err != nil || removed != 3 {
		t.Fatalf("prune all removed=%d (%v)", removed, err)
	}
}

func TestStringMapValuerScanner(t *testing.T) {
	m := StringMap{"unit": "percent"}
	v, err := m.Value()
	if err != nil {
		t.Fatal(err)
	}
	var got StringMap
	if err := got.Scan(v); err != nil || got["unit"] != "percent" {
		t.Fatalf("scan = %+v (%v)", got, err)
	}
	var fromBytes StringMap
	if err := fromBytes.Scan([]byte(`{"a":"b"}`)); err != nil || fromBytes["a"] != "b" {
		t.Fatalf("bytes scan = %+v (%v)", fromBytes, err)
	}
	var empty StringMap
	if err := empty.Scan(nil); err != nil || empty != nil {
		t.Fatalf("nil scan = %+v (%v)", empty, err)
	}
	if err := empty.Scan([]byte("")); err != nil || empty != nil {
		t.Fatalf("empty scan = %+v (%v)", empty, err)
	}
	if err := empty.Scan(42); err == nil {
		t.Fatal("unsupported type must error")
	}
	var nilMap StringMap
	if v, err := nilMap.Value(); err != nil || v != "{}" {
		t.Fatalf("nil map value = %v (%v)", v, err)
	}
}

func TestNormalizeGraphQueries(t *testing.T) {
	if q := NormalizeWalk(GraphWalk{Depth: -1, Direction: "sideways"}); q.Depth != 0 || q.Direction != "both" || q.MaxNodes != 200 {
		t.Fatalf("walk = %+v", q)
	}
	if q := NormalizeWalk(GraphWalk{Depth: 99, Direction: "out", MaxNodes: 5}); q.Depth != 8 || q.Direction != "out" || q.MaxNodes != 5 {
		t.Fatalf("walk clamp = %+v", q)
	}
	if q := NormalizeSearch(GraphSearch{}); q.Limit != 20 || q.MaxNodes != 20 {
		t.Fatalf("search = %+v", q)
	}
	if q := NormalizeSearch(GraphSearch{Limit: 500}); q.Limit != 200 || q.MaxNodes != 200 {
		t.Fatalf("search clamp = %+v", q)
	}
	if q := NormalizePath(GraphPath{MaxDepth: 99, Direction: "in"}); q.MaxDepth != 8 || q.Direction != "in" || q.MaxNodes != 200 {
		t.Fatalf("path = %+v", q)
	}
}

func TestFakeGraphSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := graphFixture(t)
	snap := f.Export()
	if len(snap.GraphNodes) != 4 || len(snap.GraphEdges) != 2 {
		t.Fatalf("snapshot = %d nodes %d edges", len(snap.GraphNodes), len(snap.GraphEdges))
	}
	g := NewFakeStore()
	g.Import(snap)
	if n, err := g.GetGraphNode(ctx, "metrics/revenue"); err != nil || n.Label != "Revenue" {
		t.Fatalf("imported node = %+v (%v)", n, err)
	}
	if stats, _ := g.GraphStats(ctx, "bob"); stats.Edges != 2 {
		t.Fatalf("imported stats = %+v", stats)
	}
	empty := NewFakeStore()
	empty.Import(Snapshot{})
	if stats, _ := empty.GraphStats(ctx, ""); stats.Nodes != 0 {
		t.Fatalf("empty import = %+v", stats)
	}
}

func TestIsNotFound(t *testing.T) {
	if IsNotFound(nil) {
		t.Fatal("nil must not be not-found")
	}
	if !IsNotFound(errNotFound) {
		t.Fatal("fake sentinel must be recognised")
	}
	if IsNotFound(errors.New("boom")) {
		t.Fatal("generic error must not be not-found")
	}
	if !IsNotFound(gorm.ErrRecordNotFound) {
		t.Fatal("gorm sentinel must be recognised")
	}
}

func TestPostgresGraphUpsertAndDelete(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "graph_nodes"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM "graph_edges" WHERE from_id`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO "graph_edges"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := s.UpsertGraphNode(ctx, GraphNodeRecord{ConceptID: "a", NodeType: "metric"}, []GraphEdgeRecord{
		{FromID: "a", ToID: "b", EdgeType: "related"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "graph_nodes"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM "graph_edges" WHERE from_id`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := s.UpsertGraphNode(ctx, GraphNodeRecord{ConceptID: "a"}, nil); err != nil {
		t.Fatalf("upsert without edges: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := s.DeleteGraphNode(ctx, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresGraphWalkAndSearchAndStats(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectQuery(`WITH RECURSIVE walk`).
		WillReturnRows(sqlmock.NewRows([]string{"node_id", "depth"}).AddRow("a", 0).AddRow("b", 1))
	mock.ExpectQuery(`SELECT \* FROM "graph_nodes" WHERE concept_id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "node_type", "label", "visibility", "content_hash"}).
			AddRow("a", "metric", "A", "public", "h1").
			AddRow("b", "metric", "B", "public", "h2"))
	mock.ExpectQuery(`SELECT \* FROM "graph_edges" WHERE from_id IN`).
		WillReturnRows(sqlmock.NewRows([]string{"from_id", "to_id", "edge_type"}).
			AddRow("a", "b", "related"))
	res, err := s.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}, Depth: 1, Direction: "out", EdgeTypes: []string{"related"}})
	if err != nil || len(res.Nodes) != 2 || len(res.Edges) != 1 {
		t.Fatalf("walk = %+v (%v)", res, err)
	}

	// Empty sources short-circuit without a query.
	if res, err := s.GraphNeighbors(ctx, GraphWalk{Sources: []string{" "}}); err != nil || len(res.Nodes) != 0 {
		t.Fatalf("empty sources = %+v (%v)", res, err)
	}

	mock.ExpectQuery(`SELECT \* FROM "graph_nodes"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "node_type", "label"}).
			AddRow("metrics/b", "metric", "B").
			AddRow("metrics/a", "metric", "A"))
	out, err := s.SearchGraphNodes(ctx, GraphSearch{Text: "metrics"})
	if err != nil || len(out) != 2 || out[0].ConceptID != "metrics/a" {
		t.Fatalf("search = %+v (%v)", out, err)
	}

	mock.ExpectQuery(`FROM graph_nodes g LEFT JOIN concepts_meta m`).
		WillReturnRows(sqlmock.NewRows([]string{"node_type", "nodes", "stale"}).AddRow("metric", 2, 1))
	mock.ExpectQuery(`FROM graph_edges e`).
		WillReturnRows(sqlmock.NewRows([]string{"edge_type", "edges"}).AddRow("related", 1))
	stats, err := s.GraphStats(ctx, "alice")
	if err != nil || stats.Nodes != 2 || stats.Edges != 1 || stats.Stale != 1 {
		t.Fatalf("stats = %+v (%v)", stats, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresGraphPruneAndErrors(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	if removed, err := s.PruneGraphNodes(ctx, []string{"a"}); err != nil || removed != 2 {
		t.Fatalf("prune = %d (%v)", removed, err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT count\(\*\) FROM "graph_nodes"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
	mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectCommit()
	if removed, err := s.PruneGraphNodes(ctx, nil); err != nil || removed != 4 {
		t.Fatalf("prune all = %d (%v)", removed, err)
	}

	mock.ExpectQuery(`SELECT \* FROM "graph_nodes"`).
		WillReturnError(errors.New("boom"))
	if _, err := s.GetGraphNode(ctx, "a"); err == nil {
		t.Fatal("query error must surface")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresGraphCancelledContext(t *testing.T) {
	s, _ := newMockStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}}); err == nil {
		t.Fatal("cancelled walk must error")
	}
	if _, err := s.SearchGraphNodes(ctx, GraphSearch{}); err == nil {
		t.Fatal("cancelled search must error")
	}
	if _, err := s.GraphStats(ctx, "s"); err == nil {
		t.Fatal("cancelled stats must error")
	}
}

func TestPostgresGraphUpsertError(t *testing.T) {
	ctx := context.Background()
	s, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "graph_nodes"`).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	if err := s.UpsertGraphNode(ctx, GraphNodeRecord{ConceptID: "a"}, nil); err == nil {
		t.Fatal("upsert error must surface")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEscapeLikeAndSearchRankLess(t *testing.T) {
	if got := escapeLike(`50%_off\`); got != `50\%\_off\\` {
		t.Fatalf("escape = %q", got)
	}
	if !searchRankLess(
		GraphNodeRecord{ConceptID: "a", Label: "A"},
		GraphNodeRecord{ConceptID: "b", Label: "B"},
		"a",
	) {
		t.Fatal("exact match must rank first")
	}
}
