package graph

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// --- stubs -----------------------------------------------------------------

type bodyStub struct {
	docs    map[string]*okf.Document
	getErr  map[string]error
	list    []string
	listErr error
}

func (b *bodyStub) Get(id string) (*okf.Document, error) {
	if err := b.getErr[id]; err != nil {
		return nil, err
	}
	doc, ok := b.docs[id]
	if !ok {
		return nil, errors.New("okf: no such document")
	}
	return doc, nil
}

func (b *bodyStub) List() ([]string, error) {
	if b.listErr != nil {
		return nil, b.listErr
	}
	if b.list != nil {
		return b.list, nil
	}
	ids := make([]string, 0, len(b.docs))
	for id := range b.docs {
		ids = append(ids, id)
	}
	return ids, nil
}

type metaStub struct {
	acl      map[string]bool
	aclErr   error
	concepts map[string]meta.ConceptMeta
	metaErr  map[string]error
	fake     *meta.FakeStore
}

func (m *metaStub) CheckACL(_ context.Context, _ string, id string) (bool, error) {
	if m.aclErr != nil {
		return false, m.aclErr
	}
	if m.acl == nil {
		return true, nil
	}
	ok, set := m.acl[id]
	if !set {
		return true, nil
	}
	return ok, nil
}

func (m *metaStub) GetConcept(ctx context.Context, id string) (meta.ConceptMeta, error) {
	if err := m.metaErr[id]; err != nil {
		return meta.ConceptMeta{}, err
	}
	c, ok := m.concepts[id]
	if !ok {
		// Delegate so "no such row" carries the sentinel meta.IsNotFound knows.
		if m.fake == nil {
			m.fake = meta.NewFakeStore()
		}
		return m.fake.GetConcept(ctx, id)
	}
	return c, nil
}

type indexStub struct {
	meta.GraphIndex
	upsert func(meta.GraphNodeRecord, []meta.GraphEdgeRecord) error
	del    func(string) error
	get    func(string) (meta.GraphNodeRecord, error)
	nodes  func(meta.GraphWalk) (meta.GraphResult, error)
	edges  func([]string, string) ([]meta.GraphEdgeRecord, error)
	path   func(meta.GraphPath) (meta.GraphPathResult, error)
	search func(meta.GraphSearch) ([]meta.GraphNodeRecord, error)
	stale  func([]string) (map[string]bool, error)
	stats  func(string) (meta.GraphStats, error)
	prune  func([]string) (int, error)
}

func (s *indexStub) UpsertGraphNode(_ context.Context, n meta.GraphNodeRecord, e []meta.GraphEdgeRecord) error {
	if s.upsert != nil {
		return s.upsert(n, e)
	}
	return nil
}

func (s *indexStub) DeleteGraphNode(_ context.Context, id string) error {
	if s.del != nil {
		return s.del(id)
	}
	return nil
}

func (s *indexStub) GetGraphNode(_ context.Context, id string) (meta.GraphNodeRecord, error) {
	if s.get != nil {
		return s.get(id)
	}
	return meta.GraphNodeRecord{}, errStubNotFound
}

func (s *indexStub) GraphNeighbors(_ context.Context, q meta.GraphWalk) (meta.GraphResult, error) {
	if s.nodes != nil {
		return s.nodes(q)
	}
	return meta.GraphResult{}, nil
}

func (s *indexStub) GraphEdges(_ context.Context, ids []string, direction string) ([]meta.GraphEdgeRecord, error) {
	if s.edges != nil {
		return s.edges(ids, direction)
	}
	return nil, nil
}

func (s *indexStub) GraphShortestPath(_ context.Context, q meta.GraphPath) (meta.GraphPathResult, error) {
	if s.path != nil {
		return s.path(q)
	}
	return meta.GraphPathResult{}, nil
}

func (s *indexStub) SearchGraphNodes(_ context.Context, q meta.GraphSearch) ([]meta.GraphNodeRecord, error) {
	if s.search != nil {
		return s.search(q)
	}
	return nil, nil
}

func (s *indexStub) StaleGraphNodes(_ context.Context, ids []string) (map[string]bool, error) {
	if s.stale != nil {
		return s.stale(ids)
	}
	return map[string]bool{}, nil
}

func (s *indexStub) GraphStats(_ context.Context, subject string) (meta.GraphStats, error) {
	if s.stats != nil {
		return s.stats(subject)
	}
	return meta.GraphStats{}, nil
}

func (s *indexStub) PruneGraphNodes(_ context.Context, keep []string) (int, error) {
	if s.prune != nil {
		return s.prune(keep)
	}
	return 0, nil
}

func boom(name string) error { return errors.New(name + " failed") }

// searcher builds a Searcher whose projector can answer the schema question;
// every read path touches it, so a nil projector would panic instead of failing.
func searcher(idx meta.GraphIndex, m Metadata, bodies BodyReader) *Searcher {
	if bodies == nil {
		bodies = &bodyStub{}
	}
	return &Searcher{
		Index: idx, Meta: m, Bodies: bodies,
		Projector: &Projector{Bodies: bodies, Meta: m, Index: idx},
	}
}

// errStubNotFound is what the index reports for a row that was never written;
// meta.IsNotFound does not recognise it, so callers must handle it as a plain
// error unless the case says otherwise.
var errStubNotFound = errors.New("meta: graph node not found")

// --- decl.go ---------------------------------------------------------------

func TestNodeFromRejectsMalformedDeclarations(t *testing.T) {
	bad, err := okf.ParseDocument([]byte("---\ntype: metric\ngraph: not-a-mapping\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, _, _, err := NodeFrom("metrics/a", bad, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed graph block: %v", err)
	}

	if _, _, _, err := NodeFrom("", &okf.Document{Type: "metric"}, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid id: %v", err)
	}
	if _, _, _, err := NodeFrom("metrics/a", nil, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil document: %v", err)
	}
}

func TestNodeFromDefaultsTypeAndLabel(t *testing.T) {
	for name, doc := range map[string]*okf.Document{
		"untyped":       {},
		"untitled":      {Type: "custom"},
		"titled":        {Type: "custom", Title: "Custom"},
		"label from id": {},
	} {
		node, _, _, err := NodeFrom("metrics/a", doc, "hash", "sha")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if node.NodeType == "" || node.Label == "" {
			t.Fatalf("%s: defaults missing: %+v", name, node)
		}
		if node.Properties == nil {
			t.Fatalf("%s: properties must never be nil", name)
		}
	}
}

func TestNodeFromDeduplicatesDeclaredEdges(t *testing.T) {
	// Two sources pointing at the same concept collapse into one derived edge;
	// the declared form is rejected earlier by SetGraph, so sources are the
	// reachable duplicate.
	doc := &okf.Document{Type: "metric", Title: "A", Sources: []okf.Source{
		{ID: "s1", Resource: "metrics/b"},
		{ID: "s2", Resource: "metrics/b"},
		{ID: "s3", Resource: "metrics/c"},
	}}
	_, edges, _, err := NodeFrom("metrics/a", doc, "", "")
	if err != nil {
		t.Fatalf("node from: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("duplicates must collapse: %+v", edges)
	}
}

func TestEdgeTargetRejectsNonConceptReferences(t *testing.T) {
	cases := map[string]bool{
		"":                        false,
		"  ":                      false,
		"https://example/x":       false,
		"/absolute/path":          false,
		"#anchor":                 false,
		"data/revenue.sql":        false,
		"metrics/revenue#section": true,
		"metrics/revenue.md":      true,
		"METRICS/Revenue":         true,
		"..":                      false,
	}
	for raw, want := range cases {
		got, ok := edgeTarget(raw)
		if ok != want {
			t.Fatalf("edgeTarget(%q) = %q,%v want %v", raw, got, ok, want)
		}
	}
}

func TestSortEdgesOrdersByEveryField(t *testing.T) {
	edges := []meta.GraphEdgeRecord{
		{FromID: "b", ToID: "a", EdgeType: "x"},
		{FromID: "a", ToID: "b", EdgeType: "y"},
		{FromID: "a", ToID: "a", EdgeType: "z"},
		{FromID: "a", ToID: "a", EdgeType: "a"},
	}
	sortEdges(edges)
	want := []string{"a|a|a", "a|a|z", "a|b|y", "b|a|x"}
	for i, e := range edges {
		got := e.FromID + "|" + e.ToID + "|" + e.EdgeType
		if got != want[i] {
			t.Fatalf("order %d = %s want %s", i, got, want[i])
		}
	}
}

func TestValidateDeclWithoutALookupCannotResolveTargets(t *testing.T) {
	schema := okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}},
		EdgeTypes: map[string]okf.EdgeType{"derived_from": {From: []string{"metric"}, To: []string{"metric"}}},
	}
	node := meta.GraphNodeRecord{ConceptID: "metrics/a", NodeType: "metric"}
	edges := []meta.GraphEdgeRecord{{FromID: "metrics/a", ToID: "metrics/b", EdgeType: "derived_from"}}
	warnings, err := ValidateDecl(context.Background(), schema, node, edges, nil)
	if err != nil {
		t.Fatalf("nil lookup must not fail: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("no lookup means no dangling warning: %v", warnings)
	}
}

// --- answer.go -------------------------------------------------------------

func TestAnswerabilityClampsMinimumAndReportsEmptyIndex(t *testing.T) {
	idx := &indexStub{stats: func(string) (meta.GraphStats, error) { return meta.GraphStats{}, nil }}
	s := searcher(idx, &metaStub{}, nil)
	cov, err := s.Answerability(context.Background(), "alice", "what is margin", 0)
	if err != nil {
		t.Fatalf("answerability: %v", err)
	}
	if cov.Minimum != 0.6 {
		t.Fatalf("minimum: %v", cov.Minimum)
	}
	if len(cov.Gaps) != 1 || cov.Gaps[0].Kind != GapEmptyIndex {
		t.Fatalf("gaps: %+v", cov.Gaps)
	}

	cov, err = s.Answerability(context.Background(), "alice", "margin", 5)
	if err != nil {
		t.Fatalf("answerability: %v", err)
	}
	if cov.Minimum != 1 {
		t.Fatalf("minimum clamp: %v", cov.Minimum)
	}
}

func TestAnswerabilityUnhealthyIndex(t *testing.T) {
	stats := searcher(&indexStub{stats: func(string) (meta.GraphStats, error) { return meta.GraphStats{}, boom("stats") }}, &metaStub{}, nil)
	if _, err := stats.Answerability(context.Background(), "alice", "margin", 0.5); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("stats: %v", err)
	}
	search := searcher(&indexStub{
		stats:  func(string) (meta.GraphStats, error) { return meta.GraphStats{Nodes: 1}, nil },
		search: func(meta.GraphSearch) ([]meta.GraphNodeRecord, error) { return nil, boom("search") },
	}, &metaStub{}, nil)
	if _, err := search.Answerability(context.Background(), "alice", "margin", 0.5); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("search: %v", err)
	}
}

func TestAnswerabilityReportsStaleUnverifiedAndUnsourcedGaps(t *testing.T) {
	record := meta.GraphNodeRecord{
		ConceptID: "metrics/a", Label: "A", NodeType: "metric",
		Trust: "unverified", SourceCount: 0,
	}
	idx := &indexStub{
		stats:  func(string) (meta.GraphStats, error) { return meta.GraphStats{Nodes: 1}, nil },
		search: func(meta.GraphSearch) ([]meta.GraphNodeRecord, error) { return []meta.GraphNodeRecord{record}, nil },
		stale:  func([]string) (map[string]bool, error) { return map[string]bool{"metrics/a": true}, nil },
	}
	s := searcher(idx, &metaStub{}, nil)
	cov, err := s.Answerability(context.Background(), "alice", "margin", 0.6)
	if err != nil {
		t.Fatalf("answerability: %v", err)
	}
	for _, kind := range []string{GapStale, GapUnverified, GapNoSource} {
		if !hasGap(cov.Gaps, kind) {
			t.Fatalf("missing %s in %+v", kind, cov.Gaps)
		}
	}
	if cov.Verdict == VerdictAnswerable {
		t.Fatalf("an unverified, unsourced node must never be certified: %+v", cov)
	}
}

func TestAnswerabilitySkipsUnreadableHitsAndUnhealthyEdges(t *testing.T) {
	record := meta.GraphNodeRecord{ConceptID: "metrics/private", NodeType: "metric"}
	idx := &indexStub{
		stats:  func(string) (meta.GraphStats, error) { return meta.GraphStats{Nodes: 2}, nil },
		search: func(meta.GraphSearch) ([]meta.GraphNodeRecord, error) { return []meta.GraphNodeRecord{record}, nil },
	}
	s := searcher(idx, &metaStub{acl: map[string]bool{"metrics/private": false}}, nil)
	cov, err := s.Answerability(context.Background(), "alice", "private", 0.6)
	if err != nil {
		t.Fatalf("answerability: %v", err)
	}
	if !hasGap(cov.Gaps, GapMissingTerm) {
		t.Fatalf("a hidden hit must read as a missing term: %+v", cov.Gaps)
	}

	visible := meta.GraphNodeRecord{ConceptID: "metrics/a", NodeType: "metric", Trust: "human-reviewed", SourceCount: 1}
	idx.search = func(meta.GraphSearch) ([]meta.GraphNodeRecord, error) { return []meta.GraphNodeRecord{visible}, nil }
	idx.edges = func([]string, string) ([]meta.GraphEdgeRecord, error) { return nil, boom("edges") }
	if _, err := s.Answerability(context.Background(), "alice", "margin", 0.6); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("edges: %v", err)
	}

	idx.edges = func([]string, string) ([]meta.GraphEdgeRecord, error) {
		return []meta.GraphEdgeRecord{{FromID: "metrics/a", ToID: "metrics/missing", EdgeType: "derived_from"}}, nil
	}
	schemaBodies := &bodyStub{docs: map[string]*okf.Document{"ontology": schemaDocument("other")}}
	s.Projector = &Projector{Bodies: schemaBodies, Meta: &metaStub{}, Index: idx}
	s.InvalidateSchema()
	cov, err = s.Answerability(context.Background(), "alice", "margin", 0.6)
	if err != nil {
		t.Fatalf("answerability: %v", err)
	}
	if !hasGap(cov.Gaps, GapDanglingEdge) {
		t.Fatalf("dangling gap: %+v", cov.Gaps)
	}
	if !hasGap(cov.Gaps, GapUnknownEdgeType) {
		t.Fatalf("unknown edge type gap: %+v", cov.Gaps)
	}
}

func TestAnswerabilityACLFailureSurfaces(t *testing.T) {
	idx := &indexStub{
		stats: func(string) (meta.GraphStats, error) { return meta.GraphStats{Nodes: 1}, nil },
		search: func(meta.GraphSearch) ([]meta.GraphNodeRecord, error) {
			return []meta.GraphNodeRecord{{ConceptID: "metrics/a"}}, nil
		},
	}
	s := searcher(idx, &metaStub{aclErr: boom("acl")}, nil)
	if _, err := s.Answerability(context.Background(), "alice", "margin", 0.6); err == nil {
		t.Fatal("an ACL failure must not be swallowed")
	}
}

func TestCapGapsSortsDedupesAndCaps(t *testing.T) {
	gaps := []Gap{
		{Kind: "b", ConceptID: "z", Detail: "d"},
		{Kind: "a", ConceptID: "y", Detail: "d"},
		{Kind: "a", ConceptID: "y", Detail: "d"},
		{Kind: "a", ConceptID: "x", Detail: "d"},
		{Kind: "a", ConceptID: "x", Detail: "c"},
	}
	out := capGaps(gaps, 3)
	if len(out) != 3 {
		t.Fatalf("cap: %+v", out)
	}
	if out[0].ConceptID != "x" || out[0].Detail != "c" {
		t.Fatalf("sort: %+v", out)
	}
	if out[1].Detail != "d" || out[2].ConceptID != "y" {
		t.Fatalf("sort: %+v", out)
	}
}

func TestCapGapsDropsDuplicatesOnly(t *testing.T) {
	out := capGaps([]Gap{{Kind: "a"}, {Kind: "a"}}, 10)
	if len(out) != 1 {
		t.Fatalf("dedupe: %+v", out)
	}
}

// schemaDocument builds an ontology document that declares one node type and no
// edge types, so every indexed relation is unknown.
func schemaDocument(nodeTypes ...string) *okf.Document {
	doc := &okf.Document{Type: schemaDocumentTypeValue}
	nodeTypesMap := map[string]okf.NodeType{}
	for _, name := range nodeTypes {
		nodeTypesMap[name] = okf.NodeType{}
	}
	if err := doc.SetGraphSchema(okf.GraphSchema{NodeTypes: nodeTypesMap, EdgeTypes: map[string]okf.EdgeType{}}); err != nil {
		panic(err)
	}
	return doc
}

const schemaDocumentTypeValue = "ontology-schema"

var _ = strings.TrimSpace
