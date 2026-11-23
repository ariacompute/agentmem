package service

import (
	"context"
	"errors"
	"testing"

	"agentmem/internal/config"
	"agentmem/internal/graph"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/router"
)

// stubQuery is an in-memory graph read surface: it records what the service
// asked for and returns whatever the case configured, so delegation and fusion
// are testable without a bundle.
type stubQuery struct {
	node      graph.NodeView
	subgraph  graph.SubgraphView
	path      graph.PathView
	hits      []graph.NodeHit
	coverage  graph.Coverage
	stats     meta.GraphStats
	schema    okf.GraphSchema
	warnings  []string
	searchErr error
	walkErr   error
	minimum   float64
	walks     []graph.WalkQuery
	queries   []graph.SearchQuery
}

func (s *stubQuery) Node(context.Context, string, string) (graph.NodeView, error) {
	return s.node, nil
}

func (s *stubQuery) Neighbors(_ context.Context, _ string, q graph.WalkQuery) (graph.SubgraphView, error) {
	s.walks = append(s.walks, q)
	if s.walkErr != nil {
		return graph.SubgraphView{}, s.walkErr
	}
	return s.subgraph, nil
}

func (s *stubQuery) Path(context.Context, string, graph.PathQuery) (graph.PathView, error) {
	return s.path, nil
}

func (s *stubQuery) Search(_ context.Context, _ string, q graph.SearchQuery) ([]graph.NodeHit, error) {
	s.queries = append(s.queries, q)
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return s.hits, nil
}

func (s *stubQuery) Answerability(_ context.Context, _, _ string, minimum float64) (graph.Coverage, error) {
	s.minimum = minimum
	return s.coverage, nil
}

func (s *stubQuery) Stats(context.Context, string) (meta.GraphStats, error) {
	return s.stats, nil
}

func (s *stubQuery) Schema(context.Context) (okf.GraphSchema, []string, error) {
	return s.schema, s.warnings, nil
}

// failingDecisioner makes the read router fail, which is the only way the text
// ranker reports an error to GraphRetrieve.
type failingDecisioner struct{}

func (failingDecisioner) Decide(context.Context, router.DecisionTask) (router.RouteDecision, error) {
	return router.RouteDecision{}, errors.New("decision failed")
}

func TestGraphSurfaceDisabled(t *testing.T) {
	svc := &KnowledgeService{}
	if svc.GraphEnabled() {
		t.Fatal("a service without a graph must report it disabled")
	}
	ctx := context.Background()
	if _, err := svc.GraphStats(ctx, "alice"); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("stats: %v", err)
	}
	if _, err := svc.GraphNode(ctx, "alice", "metrics/a"); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("node: %v", err)
	}
	if _, err := svc.GraphNeighbors(ctx, "alice", graph.WalkQuery{}); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("neighbors: %v", err)
	}
	if _, err := svc.GraphPath(ctx, "alice", graph.PathQuery{}); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("path: %v", err)
	}
	if _, err := svc.GraphSearch(ctx, "alice", graph.SearchQuery{}); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("search: %v", err)
	}
	if _, _, err := svc.GraphSchema(ctx); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("schema: %v", err)
	}
	if _, err := svc.GraphAnswerability(ctx, "alice", "q"); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("answerability: %v", err)
	}
	if _, _, err := svc.GraphPutNode(ctx, "alice", graph.NodeInput{}); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("put node: %v", err)
	}
	if _, _, err := svc.GraphAddEdges(ctx, "alice", "metrics/a", nil); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("edges: %v", err)
	}
	if _, err := svc.GraphRebuild(ctx); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("rebuild: %v", err)
	}
	if _, err := svc.GraphRetrieve(ctx, "alice", "q", 5); !errors.Is(err, ErrGraphDisabled) {
		t.Fatalf("retrieve: %v", err)
	}
}

func TestMaxGraphDepthFallsBackAndClamps(t *testing.T) {
	if got := (&KnowledgeService{}).MaxGraphDepth(); got != 3 {
		t.Fatalf("nil config depth: %d", got)
	}
	unset := config.Default()
	unset.Graph.MaxDepth = 0
	if got := (&KnowledgeService{Config: unset}).MaxGraphDepth(); got != 3 {
		t.Fatalf("unset depth: %d", got)
	}
	wide := config.Default()
	wide.Graph.MaxDepth = 12
	if got := (&KnowledgeService{Config: wide}).MaxGraphDepth(); got != 8 {
		t.Fatalf("clamped depth: %d", got)
	}
	configured := config.Default()
	configured.Graph.MaxDepth = 5
	if got := (&KnowledgeService{Config: configured}).MaxGraphDepth(); got != 5 {
		t.Fatalf("configured depth: %d", got)
	}
}

func TestGraphReadsDelegateToThePort(t *testing.T) {
	stub := &stubQuery{
		node:     graph.NodeView{ConceptID: "metrics/a"},
		subgraph: graph.SubgraphView{Root: "metrics/a"},
		path:     graph.PathView{From: "metrics/a", To: "metrics/b", Found: true},
		hits:     []graph.NodeHit{{ConceptID: "metrics/a"}},
		coverage: graph.Coverage{Verdict: graph.VerdictAnswerable},
		stats:    meta.GraphStats{Nodes: 2, Edges: 1},
		schema:   okf.GraphSchema{NodeTypes: map[string]okf.NodeType{"metric": {}}},
		warnings: []string{"unknown_edge_type: links_to"},
	}
	cfg := config.Default()
	cfg.Graph.CoverageMin = 0.75
	svc := &KnowledgeService{Config: cfg, Graph: stub}
	if !svc.GraphEnabled() {
		t.Fatal("graph must report enabled")
	}
	ctx := context.Background()

	stats, err := svc.GraphStats(ctx, "alice")
	if err != nil || stats.Nodes != 2 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
	node, err := svc.GraphNode(ctx, "alice", "metrics/a")
	if err != nil || node.ConceptID != "metrics/a" {
		t.Fatalf("node: %+v %v", node, err)
	}
	sub, err := svc.GraphNeighbors(ctx, "alice", graph.WalkQuery{From: "metrics/a", Depth: 1})
	if err != nil || sub.Root != "metrics/a" {
		t.Fatalf("neighbors: %+v %v", sub, err)
	}
	path, err := svc.GraphPath(ctx, "alice", graph.PathQuery{From: "metrics/a", To: "metrics/b"})
	if err != nil || !path.Found {
		t.Fatalf("path: %+v %v", path, err)
	}
	hits, err := svc.GraphSearch(ctx, "alice", graph.SearchQuery{Text: "a"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("search: %+v %v", hits, err)
	}
	schema, warnings, err := svc.GraphSchema(ctx)
	if err != nil || len(schema.NodeTypes) != 1 || len(warnings) != 1 {
		t.Fatalf("schema: %+v %v %v", schema, warnings, err)
	}
	cov, err := svc.GraphAnswerability(ctx, "alice", "what is a")
	if err != nil || cov.Verdict != graph.VerdictAnswerable {
		t.Fatalf("answerability: %+v %v", cov, err)
	}
	if stub.minimum != 0.75 {
		t.Fatalf("coverage minimum not passed through: %v", stub.minimum)
	}
}

func TestGraphWritesRequireTheBuiltinBackend(t *testing.T) {
	svc := &KnowledgeService{Graph: &stubQuery{}}
	ctx := context.Background()
	if _, _, err := svc.GraphPutNode(ctx, "alice", graph.NodeInput{ConceptID: "metrics/a"}); !errors.Is(err, graph.ErrInvalid) {
		t.Fatalf("put node: %v", err)
	}
	if _, _, err := svc.GraphAddEdges(ctx, "alice", "metrics/a", nil); !errors.Is(err, graph.ErrInvalid) {
		t.Fatalf("edges: %v", err)
	}
	if _, err := svc.GraphRebuild(ctx); !errors.Is(err, graph.ErrInvalid) {
		t.Fatalf("rebuild: %v", err)
	}
}

func TestGraphRetrieveWarnsWhenARankerIsUnavailable(t *testing.T) {
	stub := &stubQuery{searchErr: errors.New("index unavailable")}
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	svc.Graph = stub
	svc.Read = router.NewReadRouter(svc.Store, svc.Registry, failingDecisioner{})
	res, err := svc.GraphRetrieve(context.Background(), "alice", "margin", 0)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("no ranker should have produced hits: %+v", res.Hits)
	}
	if len(res.Warnings) != 2 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	if res.Query != "margin" {
		t.Fatalf("query echo: %q", res.Query)
	}
	if got := stub.queries[0].Limit; got != 10 {
		t.Fatalf("default top_k: %d", got)
	}
}

func TestGraphRetrieveFusesGraphNeighbourhood(t *testing.T) {
	stub := &stubQuery{
		hits: []graph.NodeHit{
			{ConceptID: "metrics/a"},
			{ConceptID: "metrics/b"},
			{ConceptID: "metrics/c"},
			{ConceptID: "metrics/d"},
		},
		subgraph: graph.SubgraphView{Nodes: []graph.NodeHit{
			{ConceptID: "metrics/a"},
			{ConceptID: "tables/fact_sales"},
		}},
	}
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	svc.Config.Graph.RRFK = 10
	svc.Graph = stub
	svc.Read = router.NewReadRouter(svc.Store, svc.Registry, failingDecisioner{})
	res, err := svc.GraphRetrieve(context.Background(), "alice", "margin", 200)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(stub.queries) != 1 || stub.queries[0].Limit != 100 {
		t.Fatalf("top_k clamp: %+v", stub.queries)
	}
	// Only the three strongest hits are expanded one hop.
	if len(stub.walks) != 3 {
		t.Fatalf("walk count: %d", len(stub.walks))
	}
	if len(res.Hits) == 0 || res.Hits[0].ID != "metrics/a" {
		t.Fatalf("fused order: %+v", res.Hits)
	}
	fused := map[string]bool{}
	for _, hit := range res.Hits {
		fused[hit.ID] = true
	}
	if !fused["tables/fact_sales"] {
		t.Fatalf("neighbour missing from the fused list: %+v", res.Hits)
	}
	// Nodes follow the fused order and never repeat.
	seen := map[string]bool{}
	for _, node := range res.Nodes {
		if seen[node.ConceptID] {
			t.Fatalf("duplicate node: %s", node.ConceptID)
		}
		seen[node.ConceptID] = true
	}
	if len(res.Nodes) != len(res.Hits) {
		t.Fatalf("node/hit mismatch: %d vs %d", len(res.Nodes), len(res.Hits))
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("only the text ranker should warn: %v", res.Warnings)
	}
}

func TestGraphRetrieveTruncatesToTopK(t *testing.T) {
	stub := &stubQuery{hits: []graph.NodeHit{
		{ConceptID: "metrics/a"}, {ConceptID: "metrics/b"}, {ConceptID: "metrics/c"},
	}}
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	svc.Graph = stub
	svc.Read = router.NewReadRouter(svc.Store, svc.Registry, failingDecisioner{})
	res, err := svc.GraphRetrieve(context.Background(), "tester", "margin", 2)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("fused list must be cut to top_k: %+v", res.Hits)
	}
}

func TestGraphRetrieveDropsFailuresAndDuplicates(t *testing.T) {
	stub := &stubQuery{
		hits:    []graph.NodeHit{{ConceptID: "metrics/a"}},
		walkErr: errors.New("walk failed"),
	}
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	svc.Graph = stub
	svc.Read = router.NewReadRouter(svc.Store, svc.Registry, failingDecisioner{})
	res, err := svc.GraphRetrieve(context.Background(), "alice", "margin", 3)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ID != "metrics/a" {
		t.Fatalf("hits: %+v", res.Hits)
	}
	if len(res.Nodes) != 1 {
		t.Fatalf("nodes: %+v", res.Nodes)
	}
}

func TestGraphWritesAndProjectionThroughTheCanonicalStore(t *testing.T) {
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	ctx := context.Background()

	res, warnings, err := svc.GraphPutNode(ctx, "tester", graph.NodeInput{ConceptID: "metrics/graph-test"})
	if err != nil {
		t.Fatalf("put node: %v", err)
	}
	if res.ConceptID != "metrics/graph-test" || res.ContentHash == "" {
		t.Fatalf("write result: %+v", res)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	appendRes, _, err := svc.GraphAddEdges(ctx, "tester", "metrics/graph-test",
		[]okf.GraphEdge{{To: "tables/missing", Type: "computed_from"}})
	if err != nil {
		t.Fatalf("add edges: %v", err)
	}
	if appendRes.ConceptID != "metrics/graph-test" {
		t.Fatalf("append result: %+v", appendRes)
	}

	report, err := svc.GraphRebuild(ctx)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if report.Nodes < 1 {
		t.Fatalf("rebuild report: %+v", report)
	}

	if _, err := svc.GraphNode(ctx, "tester", "metrics/graph-test"); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if _, err := svc.GraphNode(ctx, "tester", "metrics/absent"); err == nil {
		t.Fatal("a missing node must not be readable")
	}
}

// sqlOnlyMeta implements the metadata port and SQL execution but not the graph
// index, which is what an external store looks like to assembleGraph.
type sqlOnlyMeta struct {
	meta.MetaStore
	meta.SQLExecutor
}

// failingGraphMeta delegates everything to a fake but refuses to prune, which
// is the last step of a rebuild and therefore the way a bootstrap rebuild can
// fail without a corrupt bundle.
type failingGraphMeta struct {
	meta.MetaStore
	meta.SQLExecutor
	meta.GraphIndex
}

func (failingGraphMeta) PruneGraphNodes(context.Context, []string) (int, error) {
	return 0, errors.New("prune failed")
}

func TestAssembleSkipsTheGraphWhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	cfg.Graph.Enabled = false
	svc, err := Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	defer func() { _ = svc.Close() }()
	if svc.Graph != nil || svc.GraphWriter != nil || svc.GraphProjector != nil {
		t.Fatal("a disabled graph must not be assembled")
	}
	if got := svc.Health(context.Background())["graph"]; got != "disabled" {
		t.Fatalf("health: %q", got)
	}
}

func TestAssembleRequiresGraphIndex(t *testing.T) {
	fake := meta.NewFakeStore()
	withOpenMetadata(t, sqlOnlyMeta{MetaStore: fake, SQLExecutor: fake})
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("a store without GraphIndex must fail assembly")
	}
}

func TestAssembleSurvivesAFailedBootstrapRebuild(t *testing.T) {
	fake := meta.NewFakeStore()
	withOpenMetadata(t, failingGraphMeta{MetaStore: fake, SQLExecutor: fake, GraphIndex: fake})
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	svc, err := Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatalf("a failed bootstrap must not abort assembly: %v", err)
	}
	defer func() { _ = svc.Close() }()
	if svc.Graph == nil {
		t.Fatal("the read surface must still be assembled")
	}
}

func TestAssembleRemoteGraphBackend(t *testing.T) {
	t.Setenv("AGENTMEM_TEST_GRAPH_TOKEN", "graph-token")
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	cfg.Graph.Backend = "remote"
	cfg.Graph.RemoteURL = "https://graph.example/v1/graph"
	cfg.Graph.RemoteTokenEnv = "AGENTMEM_TEST_GRAPH_TOKEN"
	cfg.Egress.AllowedHosts = []string{"graph.example"}
	svc, err := Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	defer func() { _ = svc.Close() }()
	if svc.GraphWriter != nil {
		t.Fatal("a remote backend must not accept writes")
	}
	if got := svc.Health(context.Background())["graph"]; got != "remote" {
		t.Fatalf("health: %q", got)
	}
	if svc.GraphProjector != nil {
		t.Fatal("a remote backend must not keep a local projector")
	}
	if _, _, err := svc.GraphPutNode(context.Background(), "tester", graph.NodeInput{}); !errors.Is(err, graph.ErrInvalid) {
		t.Fatalf("remote write must be rejected: %v", err)
	}
	if _, err := svc.GraphRebuild(context.Background()); !errors.Is(err, graph.ErrInvalid) {
		t.Fatalf("remote reindex must be rejected: %v", err)
	}
}

func TestAssembleRejectsARemoteGraphOutsideTheAllowlist(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	cfg.Graph.Backend = "remote"
	cfg.Graph.RemoteURL = "https://evil.example/v1/graph"
	cfg.Graph.RemoteTokenEnv = "AGENTMEM_TEST_GRAPH_TOKEN"
	cfg.Egress.AllowedHosts = []string{"graph.example"}
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("an off-allowlist graph engine must fail assembly")
	}
}

func TestProjectGraphSkipsSyncErrorsAndLogsWarnings(t *testing.T) {
	// No projector and a blank id: both guards return before touching the store.
	(&KnowledgeService{}).projectGraph(context.Background(), "metrics/a")
	(&KnowledgeService{}).projectGraph(context.Background(), "   ")

	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	ctx := context.Background()

	// A projectable body plus a schema that rejects nothing still logs the
	// projection warnings (an unknown edge type is a warning, not an error).
	ontology := &okf.Document{Type: "ontology-schema"}
	if err := ontology.SetGraphSchema(okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}},
		EdgeTypes: map[string]okf.EdgeType{"derived_from": {From: []string{"metric"}, To: []string{"metric"}}},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := svc.Store.Put(ctx, "ontology", ontology); err != nil {
		t.Fatalf("put schema: %v", err)
	}
	warned := &okf.Document{Type: "metric", Title: "Warned"}
	decl := okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/elsewhere", Type: "links_to"}}}
	if err := warned.SetGraph(decl); err != nil {
		t.Fatalf("graph: %v", err)
	}
	if _, err := svc.Store.Put(ctx, "metrics/warned", warned); err != nil {
		t.Fatalf("put warned: %v", err)
	}
	svc.projectGraph(ctx, "metrics/warned")

	// A store that cannot write turns the sync into a logged warning.
	broken := &KnowledgeService{GraphProjector: svc.GraphProjector}
	broken.GraphProjector = &graph.Projector{Bodies: svc.Store.Bundle, Meta: svc.Store.Meta,
		Index: failingUpsertIndex{GraphIndex: svc.Store.Meta.(meta.GraphIndex)}}
	broken.projectGraph(ctx, "metrics/warned")
}

// failingUpsertIndex refuses every projection write.
type failingUpsertIndex struct{ meta.GraphIndex }

func (failingUpsertIndex) UpsertGraphNode(context.Context, meta.GraphNodeRecord, []meta.GraphEdgeRecord) error {
	return errors.New("index write failed")
}

func TestStoreDocumentsWrapsErrors(t *testing.T) {
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	docs := storeDocuments{s: svc.Store}
	if _, err := docs.PutAs(context.Background(), "", "metrics/x", &okf.Document{Type: "metric"}); err == nil {
		t.Fatal("an anonymous write must fail")
	}
	if _, err := docs.Get(context.Background(), "tester", "metrics/absent"); err == nil {
		t.Fatal("a missing document must fail")
	}
}

func TestPutReportsWriteFailures(t *testing.T) {
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	if _, err := svc.Put(context.Background(), "", "metrics/x", &okf.Document{Type: "metric"}); err == nil {
		t.Fatal("an anonymous write must fail")
	}
}

func TestDedupeDropsBlanksAndRepeats(t *testing.T) {
	if got := dedupe(nil); got != nil {
		t.Fatalf("empty input: %v", got)
	}
	got := dedupe([]string{"", "  ", "a", "a", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("dedupe: %v", got)
	}
}

func TestHealthReportsAnUnavailableGraph(t *testing.T) {
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	// Health reads the registry too, so drop only the read surface.
	svc.Graph = nil
	if got := svc.Health(context.Background())["graph"]; got != "unavailable" {
		t.Fatalf("health: %q", got)
	}
}

func TestGraphRetrieveUsesTheTextRanker(t *testing.T) {
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	ctx := context.Background()
	doc := &okf.Document{Type: "metric", Title: "Gross margin", Body: "# Gross margin\n\nmargin definition"}
	if _, err := svc.Put(ctx, "tester", "metrics/margin", doc); err != nil {
		t.Fatalf("put: %v", err)
	}
	res, err := svc.GraphRetrieve(ctx, "tester", "margin", 1)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("top_k must cut the fused list: %+v", res.Hits)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("no ranker should warn: %v", res.Warnings)
	}
}

// canonicalDecisioner routes reads at the canonical adapter so the text ranker
// returns real hits; the rule engine would pick the memory stub.
type canonicalDecisioner struct{}

func (canonicalDecisioner) Decide(context.Context, router.DecisionTask) (router.RouteDecision, error) {
	return router.RouteDecision{
		Targets: []string{"okf"}, RuleID: "test_read", Source: "rule", Confidence: 1,
	}, nil
}

func TestGraphRetrieveFusesCanonicalHitsAndTruncates(t *testing.T) {
	svc := assembleOffline(t)
	defer func() { _ = svc.Close() }()
	ctx := context.Background()
	svc.Read = router.NewReadRouter(svc.Store, svc.Registry, canonicalDecisioner{})
	for _, id := range []string{"metrics/margin-a", "metrics/margin-b"} {
		doc := &okf.Document{Type: "metric", Title: "Margin " + id, Body: "# margin\n\nmargin definition"}
		if _, err := svc.Put(ctx, "tester", id, doc); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	res, err := svc.GraphRetrieve(ctx, "tester", "margin", 1)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("top_k must cut the fused list: %+v", res.Hits)
	}
	if res.Hits[0].Score == 0 {
		t.Fatalf("a fused hit must carry a score: %+v", res.Hits)
	}
	hasText := false
	for _, source := range res.Hits {
		for _, s := range source.Sources {
			if s == "text" {
				hasText = true
			}
		}
	}
	if !hasText {
		t.Fatalf("the text ranker should have contributed: %+v", res.Hits)
	}
}

func TestOpenMetadataPostgresBranch(t *testing.T) {
	cfg := config.Default()
	cfg.Postgres.DSN = "postgres://user:placeholder@db.example.test:5432/agentmem"
	previous := openPostgres
	t.Cleanup(func() { openPostgres = previous })

	openPostgres = func(context.Context, meta.Options) (*meta.PostgresStore, error) {
		return nil, errors.New("unreachable")
	}
	if _, _, _, err := openMetadata(context.Background(), cfg); err == nil {
		t.Fatal("a failing PostgreSQL open must fail the seam")
	}

	openPostgres = func(context.Context, meta.Options) (*meta.PostgresStore, error) {
		return &meta.PostgresStore{}, nil
	}
	store, vectors, closer, err := openMetadata(context.Background(), cfg)
	if err != nil {
		t.Fatalf("seam: %v", err)
	}
	if store == nil || vectors == nil {
		t.Fatalf("the PostgreSQL branch must return the store and vectors: %v %v", store, vectors)
	}
	if closer == nil {
		t.Fatal("the PostgreSQL branch must return a closer")
	}
}
