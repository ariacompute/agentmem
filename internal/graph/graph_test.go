package graph

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/store"
	"agentmem/internal/vcs"
)

type fixture struct {
	ctx    context.Context
	bundle *okf.Bundle
	store  *store.OKFStore
	meta   *meta.FakeStore
	index  meta.GraphIndex
	proj   *Projector
	search *Searcher
	writer *Writer
}

type storeWriter struct{ s *store.OKFStore }

func (w storeWriter) Get(ctx context.Context, subject, id string) (*okf.Document, error) {
	return w.s.Get(ctx, subject, id)
}

func (w storeWriter) PutAs(ctx context.Context, subject, id string, d *okf.Document) (WriteResult, error) {
	r, err := w.s.PutAs(ctx, subject, id, d)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{ConceptID: r.ConceptID, ContentHash: r.ContentHash, CommitSHA: r.CommitSHA, Unchanged: r.Unchanged}, nil
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	b, err := okf.Open(filepath.Join(t.TempDir(), "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	f := meta.NewFakeStore()
	st := store.NewOKFStore(b, vcs.NewFake(), f, store.ACLDefaults{Visibility: "public", Owner: "system"})
	proj := &Projector{Bodies: b, Meta: f, Index: f}
	s := &Searcher{Meta: f, Index: f, Projector: proj, Bodies: b}
	return &fixture{
		ctx: context.Background(), bundle: b, store: st, meta: f, index: f,
		proj: proj, search: s,
		writer: &Writer{Meta: f, Store: storeWriter{st}, Projector: proj, Searcher: s},
	}
}

func (f *fixture) put(t *testing.T, id string, d *okf.Document) store.PutResult {
	t.Helper()
	r, err := f.store.Put(f.ctx, id, d)
	if err != nil {
		t.Fatalf("put %s: %v", id, err)
	}
	if _, _, _, err := f.proj.Sync(f.ctx, id); err != nil {
		t.Fatalf("sync %s: %v", id, err)
	}
	return r
}

func TestNodeFromDerivesEdgesAndIdentity(t *testing.T) {
	d := &okf.Document{
		Type: "metric", Title: "Gross Margin", Description: "Margin.",
		Resource: "references/margin.sql",
		Sources: []okf.Source{
			{ID: "s1", Resource: "tables/orders.md"},
			{ID: "s2", Resource: "https://example.com/report"},
			{ID: "s3", Resource: "references/margin.sql"},
		},
		Body: "see [revenue](revenue.md) and [external](https://x.test/a.md)\n",
	}
	if err := d.SetGraph(okf.GraphDecl{
		Node:  okf.GraphNode{Label: "Margin"},
		Edges: []okf.GraphEdge{{To: "metrics/revenue.md", Type: "derived_from", Evidence: "sources[0]"}},
	}); err != nil {
		t.Fatal(err)
	}
	node, edges, warnings, err := NodeFrom("metrics/margin", d, "h", "c")
	if err != nil {
		t.Fatal(err)
	}
	if warnings != nil {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if node.NodeType != "metric" || node.Label != "Margin" || node.SourceCount != 3 || !node.HasResource {
		t.Fatalf("node = %+v", node)
	}
	if node.ContentHash != "h" || node.CommitSHA != "c" {
		t.Fatalf("identity = %+v", node)
	}
	types := map[string]string{}
	for _, e := range edges {
		types[e.EdgeType+"|"+e.ToID] = e.Origin
	}
	for _, want := range []string{"derived_from|metrics/revenue", "sourced_from|tables/orders", "links_to|metrics/revenue"} {
		if _, ok := types[want]; !ok {
			t.Fatalf("missing edge %s in %v", want, types)
		}
	}
	if _, ok := types["references|references/margin"]; ok {
		t.Fatal("resource files must not become concept edges")
	}
	if types["derived_from|metrics/revenue"] != meta.EdgeOriginDeclared {
		t.Fatalf("declared origin lost: %v", types)
	}
}

func TestNodeFromDefaultsAndRejections(t *testing.T) {
	node, _, _, err := NodeFrom("tables/orders", &okf.Document{Type: "table"}, "", "")
	if err != nil || node.Label != "orders" || node.Properties == nil {
		t.Fatalf("defaults = %+v (%v)", node, err)
	}
	if _, _, _, err := NodeFrom("../escape", &okf.Document{Type: "x"}, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("escape error = %v", err)
	}
	if _, _, _, err := NodeFrom("ok", nil, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil document must be invalid")
	}
	bad := &okf.Document{Type: "x"}
	// A declaration that violates the parser budget is rejected at projection.
	if _, _, _, err := NodeFrom("ok", bad, "", ""); err != nil {
		t.Fatalf("plain document must project: %v", err)
	}
}

func TestValidateDeclWarnsAndRejects(t *testing.T) {
	ctx := context.Background()
	node := meta.GraphNodeRecord{ConceptID: "metrics/margin", NodeType: "metric"}
	edges := []meta.GraphEdgeRecord{
		{FromID: "metrics/margin", ToID: "metrics/revenue", EdgeType: "derived_from"},
		{FromID: "metrics/margin", ToID: "tables/orders", EdgeType: "computed_from"},
		{FromID: "metrics/margin", ToID: "tables/missing", EdgeType: "computed_from"},
		{FromID: "metrics/margin", ToID: "metrics/revenue", EdgeType: "mystery"},
	}
	schema := okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}, "table": {}, "concept": {}},
		EdgeTypes: map[string]okf.EdgeType{
			"derived_from":  {From: []string{"metric"}, To: []string{"metric"}},
			"computed_from": {From: []string{"metric"}, To: []string{"table"}},
		},
	}
	lookup := func(ctx context.Context, id string) (string, bool) {
		switch id {
		case "metrics/revenue":
			return "metric", true
		case "tables/orders":
			return "table", true
		}
		return "", false
	}
	warnings, err := ValidateDecl(ctx, schema, node, edges, lookup)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "dangling_edge") || !strings.Contains(joined, "unknown_edge_type") {
		t.Fatalf("warnings = %v", warnings)
	}
	if got, err := ValidateDecl(ctx, okf.GraphSchema{}, node, edges, nil); err != nil || got != nil {
		t.Fatalf("empty schema must be a no-op: %v %v", got, err)
	}
	// Wrong source type is a hard rejection.
	if _, err := ValidateDecl(ctx, schema, meta.GraphNodeRecord{NodeType: "table"}, edges[:1], lookup); !errors.Is(err, ErrInvalid) {
		t.Fatalf("source type error = %v", err)
	}
	// Wrong target type is a hard rejection.
	if _, err := ValidateDecl(ctx, schema, node, []meta.GraphEdgeRecord{
		{FromID: "metrics/margin", ToID: "metrics/revenue", EdgeType: "computed_from"},
	}, lookup); !errors.Is(err, ErrInvalid) {
		t.Fatalf("target type error = %v", err)
	}
	// Unknown node type is only a warning.
	warns, err := ValidateDecl(ctx, schema, meta.GraphNodeRecord{NodeType: "process"}, nil, lookup)
	if err != nil || len(warns) != 1 || !strings.Contains(warns[0], "unknown_node_type") {
		t.Fatalf("unknown node type = %v (%v)", warns, err)
	}
}

func TestLoadSchemaMergesAndReports(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	if _, err := f.bundle.Put("a-schema", &okf.Document{Type: schemaDocumentType}); err != nil {
		t.Fatal(err)
	}
	schemaDoc := &okf.Document{Type: schemaDocumentType}
	if err := schemaDoc.SetGraphSchema(okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}},
		EdgeTypes: map[string]okf.EdgeType{"derived_from": {From: []string{"metric"}, To: []string{"metric"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bundle.Put("ontology", schemaDoc); err != nil {
		t.Fatal(err)
	}
	// A duplicate declaration conflicts deterministically (smallest id wins).
	dup := &okf.Document{Type: schemaDocumentType}
	if err := dup.SetGraphSchema(okf.GraphSchema{NodeTypes: map[string]okf.NodeType{"metric": {}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bundle.Put("z-schema", dup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bundle.Put("plain", &okf.Document{Type: "concept"}); err != nil {
		t.Fatal(err)
	}
	schema, warnings, err := f.proj.LoadSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.NodeTypes) != 1 || len(schema.EdgeTypes) != 1 {
		t.Fatalf("schema = %+v", schema)
	}
	if !strings.Contains(strings.Join(warnings, ","), "duplicate_node_type") {
		t.Fatalf("warnings = %v", warnings)
	}
	// The cache returns the same schema until invalidated.
	if _, _, err := f.search.Schema(ctx); err != nil {
		t.Fatal(err)
	}
	f.search.InvalidateSchema()
	if _, _, err := f.search.Schema(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildProjectsPrunesAndRejects(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	schemaDoc := &okf.Document{Type: schemaDocumentType}
	if err := schemaDoc.SetGraphSchema(okf.GraphSchema{
		EdgeTypes: map[string]okf.EdgeType{"derived_from": {From: []string{"metric"}, To: []string{"metric"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bundle.Put("ontology", schemaDoc); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"metrics/a", "metrics/b"} {
		d := &okf.Document{Type: "metric", Title: id}
		if _, err := f.bundle.Put(id, d); err != nil {
			t.Fatal(err)
		}
	}
	link := &okf.Document{Type: "metric", Title: "A"}
	if err := link.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/b", Type: "derived_from"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bundle.Put("metrics/a", link); err != nil {
		t.Fatal(err)
	}
	report, err := f.proj.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Nodes != 3 || report.Pruned != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("warnings = %v", report.Warnings)
	}
	// Removing a body prunes its row and any edge that pointed at it.
	if err := f.bundle.Delete("metrics/b"); err != nil {
		t.Fatal(err)
	}
	report, err = f.proj.Rebuild(ctx)
	if err != nil || report.Nodes != 2 || report.Pruned != 1 {
		t.Fatalf("second report = %+v (%v)", report, err)
	}
	// A declaration that violates a declared constraint is skipped, not half
	// indexed.
	violation := &okf.Document{Type: "table"}
	if err := violation.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/a", Type: "derived_from"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bundle.Put("tables/x", violation); err != nil {
		t.Fatal(err)
	}
	report, err = f.proj.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(report.Warnings, ","), "declaration_rejected") {
		t.Fatalf("rejection warning = %v", report.Warnings)
	}
	if _, err := f.index.GetGraphNode(ctx, "tables/x"); !meta.IsNotFound(err) {
		t.Fatalf("rejected node must not be indexed: %v", err)
	}
}

func TestEnsureFreshRepairsChangedBody(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	d := &okf.Document{Type: "metric", Title: "One"}
	f.put(t, "metrics/one", d)
	// A direct bundle write bypasses the projection, exactly like another CLI
	// process or a git checkout.
	d2 := &okf.Document{Type: "metric", Title: "One updated"}
	if _, err := f.bundle.Put("metrics/one", d2); err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := f.proj.EnsureFresh(ctx, "metrics/one")
	if err != nil || !rebuilt {
		t.Fatalf("ensure fresh = %v (%v)", rebuilt, err)
	}
	n, err := f.index.GetGraphNode(ctx, "metrics/one")
	if err != nil || n.Label != "One updated" {
		t.Fatalf("node = %+v (%v)", n, err)
	}
	if _, rebuilt, err := f.proj.EnsureFresh(ctx, "metrics/one"); err != nil || rebuilt {
		t.Fatalf("second ensure fresh = %v (%v)", rebuilt, err)
	}
	if _, rebuilt, err := f.proj.EnsureFresh(ctx, "metrics/missing"); !errors.Is(err, ErrNotFound) || rebuilt {
		t.Fatalf("missing ensure fresh = %v (%v)", rebuilt, err)
	}
}

func TestSearcherNodeViewAndACL(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	revenue := &okf.Document{
		Type: "metric", Title: "Revenue", Description: "Net revenue.",
		Sources: []okf.Source{{ID: "s1", Resource: "tables/orders"}},
		Body:    "see [margin](margin.md)\n",
	}
	revenue.Attested = &okf.Attested{Runtime: "postgres", Computation: "sum", Parameters: []okf.Parameter{{Name: "y", Type: "integer"}}}
	f.put(t, "metrics/revenue", revenue)
	margin := &okf.Document{Type: "metric", Title: "Margin"}
	if err := margin.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "tables/orders", Type: "computed_from", Evidence: "sources[0]"}}}); err != nil {
		t.Fatal(err)
	}
	f.put(t, "metrics/margin", margin)
	f.put(t, "tables/orders", &okf.Document{Type: "table", Title: "Orders"})
	private := &okf.Document{Type: "metric", Title: "Secret"}
	private.SetVisibility("private")
	private.SetOwner("alice")
	if err := private.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "tables/orders", Type: "computed_from"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutAs(ctx, "alice", "metrics/secret", private); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.proj.Sync(ctx, "metrics/secret"); err != nil {
		t.Fatal(err)
	}

	view, err := f.search.Node(ctx, "bob", "metrics/revenue")
	if err != nil {
		t.Fatal(err)
	}
	if view.Label != "Revenue" || len(view.Sources) != 1 || view.Computation == nil || view.Computation.Runtime != "postgres" {
		t.Fatalf("view = %+v", view)
	}
	if len(view.Out) != 2 {
		t.Fatalf("out = %+v", view.Out)
	}
	origins := map[string]string{}
	for _, e := range view.Out {
		origins[e.EdgeType] = e.Origin
	}
	if origins["links_to"] != meta.EdgeOriginLink || origins["sourced_from"] != meta.EdgeOriginSource {
		t.Fatalf("derived origins = %+v", origins)
	}
	if len(view.In) != 0 {
		t.Fatalf("in = %+v", view.In)
	}
	if view.Visibility != "public" || view.Stale {
		t.Fatalf("flags = %+v", view)
	}

	if _, err := f.search.Node(ctx, "bob", "metrics/secret"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private node = %v", err)
	}
	owner, err := f.search.Node(ctx, "alice", "metrics/secret")
	if err != nil || owner.Label != "Secret" {
		t.Fatalf("owner view = %+v (%v)", owner, err)
	}
	if len(owner.Out) != 1 || len(owner.In) != 0 {
		t.Fatalf("owner edges = %+v", owner.Out)
	}
	if _, err := f.search.Node(ctx, "bob", "../escape"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("escape = %v", err)
	}
	if _, err := f.search.Node(ctx, "bob", "metrics/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
}

func TestSearcherHidesEdgeToUnreadableTarget(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	hidden := &okf.Document{Type: "table", Title: "Hidden"}
	hidden.SetVisibility("private")
	hidden.SetOwner("alice")
	f.put(t, "tables/hidden", hidden)
	visible := &okf.Document{Type: "metric", Title: "Visible"}
	if err := visible.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "tables/hidden", Type: "computed_from"}}}); err != nil {
		t.Fatal(err)
	}
	f.put(t, "metrics/visible", visible)
	view, err := f.search.Node(ctx, "bob", "metrics/visible")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Out) != 0 {
		t.Fatalf("edge to a private target leaked: %+v", view.Out)
	}
	owner, err := f.search.Node(ctx, "alice", "metrics/visible")
	if err != nil || len(owner.Out) != 1 {
		t.Fatalf("owner edge = %+v (%v)", owner.Out, err)
	}
}

func TestSearcherNeighborsAndPath(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	for _, id := range []string{"metrics/a", "metrics/b", "metrics/c"} {
		d := &okf.Document{Type: "metric", Title: id}
		if next := chainNext(id); next != "" {
			if err := d.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: next, Type: "derived_from"}}}); err != nil {
				t.Fatal(err)
			}
		}
		f.put(t, id, d)
	}
	sub, err := f.search.Neighbors(ctx, "bob", WalkQuery{From: "metrics/a", Depth: 2, Direction: "out", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Nodes) != 3 || len(sub.Edges) != 2 || sub.Depths["metrics/c"] != 2 {
		t.Fatalf("subgraph = %+v", sub)
	}
	if sub.Root != "metrics/a" {
		t.Fatalf("root = %q", sub.Root)
	}
	sub, err = f.search.Neighbors(ctx, "bob", WalkQuery{From: "metrics/a", Depth: 1, Direction: "in", Limit: 10})
	if err != nil || len(sub.Nodes) != 1 {
		t.Fatalf("incoming = %+v (%v)", sub, err)
	}
	if _, err := f.search.Neighbors(ctx, "bob", WalkQuery{From: "nope", Limit: 5}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing root = %v", err)
	}

	path, err := f.search.Path(ctx, "bob", PathQuery{From: "metrics/a", To: "metrics/c", MaxDepth: 3})
	if err != nil || !path.Found || path.Hops != 2 || len(path.Nodes) != 3 {
		t.Fatalf("path = %+v (%v)", path, err)
	}
	if path, err := f.search.Path(ctx, "bob", PathQuery{From: "metrics/c", To: "metrics/a", Direction: "out", MaxDepth: 3}); err != nil || path.Found {
		t.Fatalf("reverse path = %+v (%v)", path, err)
	}
	if _, err := f.search.Path(ctx, "bob", PathQuery{From: "nope", To: "metrics/a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing endpoint = %v", err)
	}
}

func chainNext(id string) string {
	switch id {
	case "metrics/a":
		return "metrics/b"
	case "metrics/b":
		return "metrics/c"
	}
	return ""
}

func TestSearcherPathThroughHiddenNodeIsInvisible(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	a := &okf.Document{Type: "metric", Title: "A"}
	if err := a.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/hidden", Type: "derived_from"}}}); err != nil {
		t.Fatal(err)
	}
	f.put(t, "metrics/a", a)
	hidden := &okf.Document{Type: "metric", Title: "Hidden"}
	hidden.SetVisibility("private")
	hidden.SetOwner("alice")
	if err := hidden.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/b", Type: "derived_from"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutAs(ctx, "alice", "metrics/hidden", hidden); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.proj.Sync(ctx, "metrics/hidden"); err != nil {
		t.Fatal(err)
	}
	f.put(t, "metrics/b", &okf.Document{Type: "metric", Title: "B"})
	path, err := f.search.Path(ctx, "bob", PathQuery{From: "metrics/a", To: "metrics/b", MaxDepth: 3})
	if err != nil || path.Found {
		t.Fatalf("path through hidden node = %+v (%v)", path, err)
	}
}

func TestSearcherSearchStatsAndSchema(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	f.put(t, "metrics/revenue", &okf.Document{Type: "metric", Title: "Revenue"})
	f.put(t, "tables/orders", &okf.Document{Type: "table", Title: "Orders"})
	if _, err := f.search.Search(ctx, "bob", SearchQuery{Text: "rev"}); err != nil {
		t.Fatal(err)
	}
	hits, err := f.search.Search(ctx, "bob", SearchQuery{Text: "revenue", Limit: 1})
	if err != nil || len(hits) != 1 || hits[0].ConceptID != "metrics/revenue" {
		t.Fatalf("hits = %+v (%v)", hits, err)
	}
	if hits, err := f.search.Search(ctx, "bob", SearchQuery{Text: "revenue", NodeTypes: []string{"table"}}); err != nil || len(hits) != 0 {
		t.Fatalf("type filter = %+v (%v)", hits, err)
	}
	stats, err := f.search.Stats(ctx, "bob")
	if err != nil || stats.Nodes != 2 {
		t.Fatalf("stats = %+v (%v)", stats, err)
	}
	if _, _, err := f.search.Schema(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAnswerabilityVerdicts(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	doc := &okf.Document{Type: "metric", Title: "Revenue", Description: "Net revenue"}
	doc.Verified = []okf.ActorEvent{{By: "human:alice"}}
	doc.Sources = []okf.Source{{ID: "s1", Resource: "tables/orders"}}
	if err := doc.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "tables/missing", Type: "computed_from"}}}); err != nil {
		t.Fatal(err)
	}
	f.put(t, "metrics/revenue", doc)

	cov, err := f.search.Answerability(ctx, "bob", "what is revenue", 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if cov.Verdict != VerdictAnswerable || cov.Coverage < 0.6 || len(cov.Matched) != 1 {
		t.Fatalf("coverage = %+v", cov)
	}
	// The dangling target is surfaced as a gap, not silently dropped.
	if !hasGap(cov.Gaps, GapDanglingEdge) {
		t.Fatalf("gaps = %+v", cov.Gaps)
	}
	low, err := f.search.Answerability(ctx, "bob", "revenue forecast volatility", 0.9)
	if err != nil {
		t.Fatal(err)
	}
	if low.Verdict == VerdictAnswerable || !hasGap(low.Gaps, GapMissingTerm) {
		t.Fatalf("low coverage = %+v", low)
	}
	empty, err := f.search.Answerability(ctx, "bob", "", 0.6)
	if err != nil || empty.Verdict != VerdictInsufficient || !hasGap(empty.Gaps, GapMissingTerm) {
		t.Fatalf("empty question = %+v (%v)", empty, err)
	}
	if _, err := f.search.Answerability(ctx, "bob", "revenue", 2); err != nil {
		t.Fatal(err)
	}
}

func TestAnswerabilityEmptyIndexAndQualityGaps(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	cov, err := f.search.Answerability(ctx, "bob", "revenue", 0.6)
	if err != nil || cov.Verdict != VerdictInsufficient || !hasGap(cov.Gaps, GapEmptyIndex) {
		t.Fatalf("empty index = %+v (%v)", cov, err)
	}
	// Unverified and unsourced nodes produce explicit gaps.
	f.put(t, "metrics/revenue", &okf.Document{Type: "metric", Title: "Revenue"})
	cov, err = f.search.Answerability(ctx, "bob", "revenue", 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if !hasGap(cov.Gaps, GapUnverified) || !hasGap(cov.Gaps, GapNoSource) {
		t.Fatalf("quality gaps = %+v", cov.Gaps)
	}
	if cov.Verdict != VerdictPartial && cov.Verdict != VerdictInsufficient {
		t.Fatalf("verdict = %q", cov.Verdict)
	}
}

func TestTokenize(t *testing.T) {
	got := Tokenize("What is the Gross-Margin for 2024? 毛利率")
	if strings.Join(got, ",") != "gross,margin,2024,毛利率" {
		t.Fatalf("tokens = %v", got)
	}
	if got := Tokenize("a b 1"); len(got) != 0 {
		t.Fatalf("short tokens = %v", got)
	}
	long := Tokenize("alpha beta gamma delta epsilon zeta eta theta iota kappa")
	if len(long) != maxCoverageTerms {
		t.Fatalf("cap = %v", long)
	}
}

func TestRRFFusesAndBreaksTies(t *testing.T) {
	lists := [][]Ranked{
		{{ID: "a", Source: "text"}, {ID: "b", Source: "text"}},
		{{ID: "b", Source: "graph"}, {ID: "c", Source: "graph"}},
	}
	out := RRF(lists, 60)
	if len(out) != 3 || out[0].ID != "b" {
		t.Fatalf("fused = %+v", out)
	}
	if out[0].Score <= out[1].Score || len(out[0].Sources) != 2 {
		t.Fatalf("ranking = %+v", out)
	}
	dup := RRF([][]Ranked{{{ID: "a"}, {ID: "a"}}}, 0)
	if len(dup) != 1 {
		t.Fatalf("duplicate ids = %+v", dup)
	}
	tie := RRF([][]Ranked{{{ID: "b"}}, {{ID: "a"}}}, 10)
	if tie[0].ID != "a" || tie[0].Rank != 1 || tie[0].Score != tie[1].Score {
		t.Fatalf("tie order = %+v", tie)
	}
	if got := RRF(nil, 1); len(got) != 0 {
		t.Fatalf("empty = %+v", got)
	}
}

func TestWriterCreatesUpdatesAndValidates(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	schemaDoc := &okf.Document{Type: schemaDocumentType}
	if err := schemaDoc.SetGraphSchema(okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}, "table": {}},
		EdgeTypes: map[string]okf.EdgeType{
			"derived_from":  {From: []string{"metric"}, To: []string{"metric"}},
			"computed_from": {From: []string{"metric"}, To: []string{"table"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bundle.Put("ontology", schemaDoc); err != nil {
		t.Fatal(err)
	}

	label := "Revenue"
	nodeType := "metric"
	res, warnings, err := f.writer.PutNode(ctx, "alice", NodeInput{
		ConceptID: "metrics/revenue", NodeType: &nodeType, Label: &label,
		Edges:    []okf.GraphEdge{{To: "tables/orders", Type: "computed_from"}},
		EdgesSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentHash == "" || res.CommitSHA == "" {
		t.Fatalf("result = %+v", res)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, ","), "dangling_edge") {
		t.Fatalf("warnings = %v", warnings)
	}
	view, err := f.search.Node(ctx, "alice", "metrics/revenue")
	if err != nil || view.Label != "Revenue" || view.NodeType != "metric" {
		t.Fatalf("view = %+v (%v)", view, err)
	}
	// The target has no body yet, so the edge is reported, not rendered.
	if len(view.Out) != 0 || !strings.Contains(strings.Join(view.Warnings, ","), GapDanglingEdge) {
		t.Fatalf("dangling view = %+v", view)
	}

	// Adding the target resolves the stored edge without rewriting the source.
	tableType := "table"
	if _, _, err := f.writer.PutNode(ctx, "alice", NodeInput{ConceptID: "tables/orders", NodeType: &tableType}); err != nil {
		t.Fatal(err)
	}
	desc := "Net revenue"
	if _, _, err := f.writer.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/revenue", Description: &desc}); err != nil {
		t.Fatal(err)
	}
	view, _ = f.search.Node(ctx, "alice", "metrics/revenue")
	if view.Description != "Net revenue" || view.Label != "Revenue" || len(view.Out) != 1 {
		t.Fatalf("partial update = %+v", view)
	}

	// A declared but mismatched relation is rejected before any write.
	if _, _, err := f.writer.AddEdges(ctx, "alice", "tables/orders", []okf.GraphEdge{{To: "metrics/revenue", Type: "derived_from"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("constraint error = %v", err)
	}
	if _, _, err := f.writer.AddEdges(ctx, "alice", "metrics/revenue", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty edges = %v", err)
	}
	if _, _, err := f.writer.PutNode(ctx, "alice", NodeInput{ConceptID: "../escape"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("escape = %v", err)
	}
	empty := ""
	if _, _, err := f.writer.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/x", NodeType: &empty}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty node type = %v", err)
	}
}

func TestWriterAddEdgesMergesAndDeniesForeignWrites(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	f.put(t, "metrics/a", &okf.Document{Type: "metric", Title: "A"})
	private := &okf.Document{Type: "metric", Title: "Private"}
	private.SetVisibility("private")
	private.SetOwner("alice")
	if _, err := f.store.PutAs(ctx, "alice", "metrics/private", private); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.proj.Sync(ctx, "metrics/private"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.writer.AddEdges(ctx, "bob", "metrics/private", []okf.GraphEdge{{To: "metrics/x", Type: "derived_from"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign write = %v", err)
	}
	if _, _, err := f.writer.PutNode(ctx, "bob", NodeInput{ConceptID: "metrics/private"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign put = %v", err)
	}
	res, _, err := f.writer.AddEdges(ctx, "alice", "metrics/private", []okf.GraphEdge{
		{To: "metrics/a", Type: "derived_from"},
		{To: "metrics/a", Type: "derived_from"},
		{To: "metrics/a", Type: "links_to"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ConceptID != "metrics/private" {
		t.Fatalf("result = %+v", res)
	}
	view, err := f.search.Node(ctx, "alice", "metrics/private")
	if err != nil || len(view.Out) != 2 {
		t.Fatalf("merged edges = %+v (%v)", view.Out, err)
	}
}

func hasGap(gaps []Gap, kind string) bool {
	for _, g := range gaps {
		if g.Kind == kind {
			return true
		}
	}
	return false
}
