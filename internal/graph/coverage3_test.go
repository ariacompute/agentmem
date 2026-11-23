package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// --- shared stubs ----------------------------------------------------------

// aclFunc answers CheckACL per id so a test can fail or hide one endpoint
// without hiding the whole query.
type aclFunc struct {
	*metaStub
	check func(subject, id string) (bool, error)
}

func (a *aclFunc) CheckACL(_ context.Context, subject, id string) (bool, error) {
	return a.check(subject, id)
}

// docsStub is the ACL-checked write path. Keeping it a pair of funcs lets a
// case make Get succeed and PutAs fail, which the real store only does on ACL.
type docsStub struct {
	get func(ctx context.Context, subject, id string) (*okf.Document, error)
	put func(ctx context.Context, subject, id string, d *okf.Document) (WriteResult, error)
}

func (d *docsStub) Get(ctx context.Context, subject, id string) (*okf.Document, error) {
	return d.get(ctx, subject, id)
}

func (d *docsStub) PutAs(ctx context.Context, subject, id string, doc *okf.Document) (WriteResult, error) {
	if d.put != nil {
		return d.put(ctx, subject, id, doc)
	}
	return WriteResult{ConceptID: id, ContentHash: "hash", CommitSHA: "commit"}, nil
}

// flakyBodies fails the second List/Get call. Both readers guard a "the first
// read proved the bundle is fine" assumption that only a stateful stub reaches.
type flakyBodies struct {
	bodyStub
	gets  int
	lists int
}

func (f *flakyBodies) Get(id string) (*okf.Document, error) {
	f.gets++
	if f.gets > 1 {
		return nil, boom("get")
	}
	return f.bodyStub.Get(id)
}

func (f *flakyBodies) List() ([]string, error) {
	f.lists++
	if f.lists > 1 {
		return nil, boom("list")
	}
	return f.bodyStub.List()
}

func graphHash(t *testing.T, doc *okf.Document) string {
	t.Helper()
	data, err := marshalDocument(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return okf.Hash(data)
}

// freshIndex answers GetGraphNode with the stored hash, so EnsureFresh returns
// without projecting and the query path under test stays isolated.
func freshIndex(hash string) *indexStub {
	return &indexStub{get: func(id string) (meta.GraphNodeRecord, error) {
		return meta.GraphNodeRecord{ConceptID: id, NodeType: "metric", Label: id, ContentHash: hash}, nil
	}}
}

// --- query.go: Node -------------------------------------------------------

func TestSearcherNodeRejectsUnreadableAndUnhealthySources(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "metric", Title: "A"}
	hash := graphHash(t, doc)

	if _, err := searcher(&indexStub{}, &metaStub{}, &bodyStub{}).Node(ctx, "alice", "../escape"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid id: %v", err)
	}
	if _, err := searcher(&indexStub{}, &metaStub{aclErr: boom("acl")}, &bodyStub{}).Node(ctx, "alice", "metrics/a"); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("acl failure: %v", err)
	}
	hidden := &metaStub{acl: map[string]bool{"metrics/a": false}}
	if _, err := searcher(&indexStub{}, hidden, &bodyStub{}).Node(ctx, "alice", "metrics/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden node: %v", err)
	}
	if _, err := searcher(&indexStub{}, &metaStub{}, &bodyStub{}).Node(ctx, "alice", "metrics/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing body: %v", err)
	}

	// A stored row the index cannot read is not a not-found answer.
	bodies := &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}
	if _, err := searcher(&indexStub{}, &metaStub{}, bodies).Node(ctx, "alice", "metrics/a"); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("index failure: %v", err)
	}
	edgesDown := &indexStub{
		get:   freshIndex(hash).get,
		edges: func([]string, string) ([]meta.GraphEdgeRecord, error) { return nil, boom("edges") },
	}
	if _, err := searcher(edgesDown, &metaStub{}, bodies).Node(ctx, "alice", "metrics/a"); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("edges: %v", err)
	}

	// An unreadable neighbour surfaces as an error, not a redacted edge.
	aclDown := &aclFunc{metaStub: &metaStub{}, check: func(_, id string) (bool, error) {
		if id == "metrics/b" {
			return false, boom("acl")
		}
		return true, nil
	}}
	withNeighbour := &indexStub{
		get: freshIndex(hash).get,
		edges: func([]string, string) ([]meta.GraphEdgeRecord, error) {
			return []meta.GraphEdgeRecord{{FromID: "metrics/a", ToID: "metrics/b", EdgeType: "links_to"}}, nil
		},
	}
	if _, err := searcher(withNeighbour, aclDown, bodies).Node(ctx, "alice", "metrics/a"); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("neighbour acl: %v", err)
	}
}

func TestSearcherNodeOrdersEdgesAndReportsSchemaGaps(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "metric", Title: "A"}
	hash := graphHash(t, doc)

	schemaDoc := &okf.Document{Type: schemaDocumentType}
	if err := schemaDoc.SetGraphSchema(okf.GraphSchema{NodeTypes: map[string]okf.NodeType{"metric": {}}}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	bodies := &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc, "ontology": schemaDoc}}
	idx := &indexStub{
		get: freshIndex(hash).get,
		edges: func([]string, string) ([]meta.GraphEdgeRecord, error) {
			return []meta.GraphEdgeRecord{
				{FromID: "metrics/b", ToID: "metrics/a", EdgeType: "derived_from"},
				{FromID: "metrics/a", ToID: "metrics/c", EdgeType: "links_to"},
				{FromID: "metrics/a", ToID: "metrics/b", EdgeType: "derived_from"},
				{FromID: "elsewhere/x", ToID: "elsewhere/y", EdgeType: "links_to"},
			}, nil
		},
	}
	view, err := searcher(idx, &metaStub{}, bodies).Node(ctx, "alice", "metrics/a")
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	if len(view.In) != 1 || view.In[0].FromID != "metrics/b" {
		t.Fatalf("incoming edges: %+v", view.In)
	}
	if len(view.Out) != 2 || view.Out[0].ToID != "metrics/b" || view.Out[1].ToID != "metrics/c" {
		t.Fatalf("outgoing edges: %+v", view.Out)
	}
	if !strings.Contains(strings.Join(view.Warnings, ","), GapUnknownEdgeType+": derived_from") ||
		!strings.Contains(strings.Join(view.Warnings, ","), GapUnknownEdgeType+": links_to") {
		t.Fatalf("warnings: %v", view.Warnings)
	}
}

// --- query.go: Neighbors --------------------------------------------------

func TestSearcherNeighborsErrorPaths(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "metric"}
	hash := graphHash(t, doc)
	bodies := &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}

	if _, err := searcher(&indexStub{}, &metaStub{}, bodies).Neighbors(ctx, "alice", WalkQuery{From: "../escape"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid root: %v", err)
	}
	if _, err := searcher(&indexStub{}, &metaStub{aclErr: boom("acl")}, bodies).Neighbors(ctx, "alice", WalkQuery{From: "metrics/a"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("acl: %v", err)
	}
	hidden := &metaStub{acl: map[string]bool{"metrics/a": false}}
	if _, err := searcher(&indexStub{}, hidden, bodies).Neighbors(ctx, "alice", WalkQuery{From: "metrics/a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden root: %v", err)
	}
	if _, err := searcher(&indexStub{}, &metaStub{}, &bodyStub{}).Neighbors(ctx, "alice", WalkQuery{From: "metrics/a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing body: %v", err)
	}
	if _, err := searcher(&indexStub{}, &metaStub{}, bodies).Neighbors(ctx, "alice", WalkQuery{From: "metrics/a"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("index: %v", err)
	}

	walkDown := &indexStub{
		get:   freshIndex(hash).get,
		nodes: func(meta.GraphWalk) (meta.GraphResult, error) { return meta.GraphResult{}, boom("walk") },
	}
	if _, err := searcher(walkDown, &metaStub{}, bodies).Neighbors(ctx, "alice", WalkQuery{From: "metrics/a"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("walk: %v", err)
	}
	staleDown := &indexStub{
		get:   freshIndex(hash).get,
		nodes: func(meta.GraphWalk) (meta.GraphResult, error) { return meta.GraphResult{}, nil },
		stale: func([]string) (map[string]bool, error) { return nil, boom("stale") },
	}
	if _, err := searcher(staleDown, &metaStub{}, bodies).Neighbors(ctx, "alice", WalkQuery{From: "metrics/a"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("stale: %v", err)
	}
}

func TestSearcherNeighborsFiltersHiddenNodesAndTheirEdges(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "metric"}
	hash := graphHash(t, doc)
	bodies := &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}
	idx := &indexStub{
		get: freshIndex(hash).get,
		nodes: func(meta.GraphWalk) (meta.GraphResult, error) {
			return meta.GraphResult{
				Nodes: []meta.GraphNodeRecord{
					{ConceptID: "metrics/a", NodeType: "metric"},
					{ConceptID: "metrics/b", NodeType: "metric"},
					{ConceptID: "metrics/c", NodeType: "metric"},
				},
				Edges: []meta.GraphEdgeRecord{
					{FromID: "metrics/a", ToID: "metrics/b", EdgeType: "links_to"},
					{FromID: "metrics/a", ToID: "metrics/c", EdgeType: "links_to"},
				},
				Depths: map[string]int{"metrics/a": 0, "metrics/b": 1, "metrics/c": 1},
			}, nil
		},
	}
	partial := &aclFunc{metaStub: &metaStub{}, check: func(_, id string) (bool, error) {
		return id != "metrics/b", nil
	}}
	view, err := searcher(idx, partial, bodies).Neighbors(ctx, "alice", WalkQuery{From: "metrics/a", Depth: 1})
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(view.Nodes) != 2 || len(view.Edges) != 1 || view.Edges[0].ToID != "metrics/c" {
		t.Fatalf("filtered view = %+v", view)
	}

	failing := &aclFunc{metaStub: &metaStub{}, check: func(_, id string) (bool, error) {
		if id == "metrics/c" {
			return false, boom("acl")
		}
		return true, nil
	}}
	if _, err := searcher(idx, failing, bodies).Neighbors(ctx, "alice", WalkQuery{From: "metrics/a"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("node acl: %v", err)
	}
}

// --- query.go: Path -------------------------------------------------------

func TestSearcherPathErrorPaths(t *testing.T) {
	ctx := context.Background()
	if _, err := searcher(&indexStub{}, &metaStub{}, &bodyStub{}).Path(ctx, "alice", PathQuery{From: "../escape", To: "metrics/b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid from: %v", err)
	}
	if _, err := searcher(&indexStub{}, &metaStub{}, &bodyStub{}).Path(ctx, "alice", PathQuery{From: "metrics/a", To: "../escape"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid to: %v", err)
	}
	if _, err := searcher(&indexStub{}, &metaStub{aclErr: boom("acl")}, &bodyStub{}).Path(ctx, "alice", PathQuery{From: "metrics/a", To: "metrics/b"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("acl: %v", err)
	}
	hidden := &metaStub{acl: map[string]bool{"metrics/b": false}}
	if _, err := searcher(&indexStub{}, hidden, &bodyStub{}).Path(ctx, "alice", PathQuery{From: "metrics/a", To: "metrics/b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden endpoint: %v", err)
	}
	pathDown := &indexStub{path: func(meta.GraphPath) (meta.GraphPathResult, error) { return meta.GraphPathResult{}, boom("path") }}
	if _, err := searcher(pathDown, &metaStub{}, &bodyStub{}).Path(ctx, "alice", PathQuery{From: "metrics/a", To: "metrics/b"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("path: %v", err)
	}

	// An unreadable middle hop fails the query, not just the path.
	hopDown := &indexStub{path: func(meta.GraphPath) (meta.GraphPathResult, error) {
		return meta.GraphPathResult{
			Found: true,
			Nodes: []meta.GraphNodeRecord{
				{ConceptID: "metrics/a"}, {ConceptID: "metrics/c"}, {ConceptID: "metrics/b"},
			},
			Edges: []meta.GraphEdgeRecord{
				{FromID: "metrics/a", ToID: "metrics/c", EdgeType: "links_to"},
				{FromID: "metrics/c", ToID: "metrics/b", EdgeType: "links_to"},
			},
		}, nil
	}}
	hopACL := &aclFunc{metaStub: &metaStub{}, check: func(_, id string) (bool, error) {
		if id == "metrics/c" {
			return false, boom("acl")
		}
		return true, nil
	}}
	if _, err := searcher(hopDown, hopACL, &bodyStub{}).Path(ctx, "alice", PathQuery{From: "metrics/a", To: "metrics/b"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("hop acl: %v", err)
	}
}

// --- query.go: Search, Stats, Schema --------------------------------------

func TestSearcherSearchClampsLimitAndFilters(t *testing.T) {
	ctx := context.Background()
	var asked meta.GraphSearch
	idx := &indexStub{search: func(q meta.GraphSearch) ([]meta.GraphNodeRecord, error) {
		asked = q
		return []meta.GraphNodeRecord{
			{ConceptID: "metrics/a", NodeType: "metric"},
			{ConceptID: "metrics/b", NodeType: "metric"},
			{ConceptID: "metrics/c", NodeType: "metric"},
		}, nil
	}}
	partial := &aclFunc{metaStub: &metaStub{}, check: func(_, id string) (bool, error) {
		return id != "metrics/b", nil
	}}
	hits, err := searcher(idx, partial, &bodyStub{}).Search(ctx, "alice", SearchQuery{Text: "rev", Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if asked.Limit != 200 {
		t.Fatalf("wide limit = %d", asked.Limit)
	}
	if len(hits) != 2 || hits[0].ConceptID != "metrics/a" || hits[1].ConceptID != "metrics/c" {
		t.Fatalf("hits = %+v", hits)
	}

	// A page is filled after filtering: two visible matches stop at the limit.
	limited, err := searcher(idx, &metaStub{}, &bodyStub{}).Search(ctx, "alice", SearchQuery{Text: "rev", Limit: 2})
	if err != nil || len(limited) != 2 {
		t.Fatalf("limited = %+v (%v)", limited, err)
	}
}

func TestSearcherSearchStatsAndSchemaErrorPaths(t *testing.T) {
	ctx := context.Background()
	if _, err := searcher(&indexStub{search: func(meta.GraphSearch) ([]meta.GraphNodeRecord, error) { return nil, boom("search") }},
		&metaStub{}, &bodyStub{}).Search(ctx, "alice", SearchQuery{Text: "x"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("search: %v", err)
	}
	staleDown := &indexStub{
		search: func(meta.GraphSearch) ([]meta.GraphNodeRecord, error) { return nil, nil },
		stale:  func([]string) (map[string]bool, error) { return nil, boom("stale") },
	}
	if _, err := searcher(staleDown, &metaStub{}, &bodyStub{}).Search(ctx, "alice", SearchQuery{Text: "x"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("stale: %v", err)
	}
	aclDown := &indexStub{search: func(meta.GraphSearch) ([]meta.GraphNodeRecord, error) {
		return []meta.GraphNodeRecord{{ConceptID: "metrics/a", NodeType: "metric"}}, nil
	}}
	if _, err := searcher(aclDown, &metaStub{aclErr: boom("acl")}, &bodyStub{}).Search(ctx, "alice", SearchQuery{Text: "x"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("acl: %v", err)
	}
	if _, err := searcher(&indexStub{stats: func(string) (meta.GraphStats, error) { return meta.GraphStats{}, boom("stats") }},
		&metaStub{}, &bodyStub{}).Stats(ctx, "alice"); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("stats: %v", err)
	}

	schemaDown := searcher(&indexStub{}, &metaStub{}, &bodyStub{listErr: boom("list")})
	if _, _, err := schemaDown.Schema(ctx); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("schema: %v", err)
	}
}

// --- writer.go ------------------------------------------------------------

func malformedDoc(t *testing.T) *okf.Document {
	t.Helper()
	doc, err := okf.ParseDocument([]byte("---\ntype: metric\ngraph: nope\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func writerWith(store *docsStub, bodies *bodyStub, m *metaStub, idx *indexStub) *Writer {
	return &Writer{
		Meta: m, Store: store,
		Projector: &Projector{Bodies: bodies, Meta: m, Index: idx},
	}
}

func TestWriterRejectsMalformedDocuments(t *testing.T) {
	ctx := context.Background()
	malformed := malformedDoc(t)
	store := &docsStub{get: func(context.Context, string, string) (*okf.Document, error) {
		return malformed, nil
	}}
	w := writerWith(store, &bodyStub{}, &metaStub{}, &indexStub{})

	if _, _, err := w.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/a"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("put malformed: %v", err)
	}
	if _, _, err := w.AddEdges(ctx, "alice", "metrics/a", []okf.GraphEdge{{To: "metrics/b", Type: "links_to"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("add malformed: %v", err)
	}

	if _, _, err := w.AddEdges(ctx, "alice", "../escape", []okf.GraphEdge{{To: "metrics/b", Type: "links_to"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("escape: %v", err)
	}
	plain := &docsStub{get: func(context.Context, string, string) (*okf.Document, error) {
		return &okf.Document{Type: "metric"}, nil
	}}
	w = writerWith(plain, &bodyStub{}, &metaStub{}, &indexStub{})
	if _, _, err := w.AddEdges(ctx, "alice", "metrics/a", []okf.GraphEdge{{To: `a\b`, Type: "links_to"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid target: %v", err)
	}
	// Properties are normalised by SetGraph, so a blank key is rejected.
	if _, _, err := w.PutNode(ctx, "alice", NodeInput{
		ConceptID: "metrics/a", Properties: map[string]string{" ": "x"},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("properties: %v", err)
	}
}

func TestWriterAddEdgesMarksExistingEdgesBeforeAppending(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "metric"}
	if err := doc.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/b", Type: "links_to"}}}); err != nil {
		t.Fatalf("graph: %v", err)
	}
	bodies := &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}, list: []string{}}
	var written *okf.Document
	w := writerWith(&docsStub{
		get: func(context.Context, string, string) (*okf.Document, error) { return doc, nil },
		put: func(_ context.Context, _, id string, d *okf.Document) (WriteResult, error) {
			written = d
			return WriteResult{ConceptID: id, ContentHash: "hash", CommitSHA: "commit"}, nil
		},
	}, bodies, &metaStub{}, &indexStub{})

	res, _, err := w.AddEdges(ctx, "alice", "metrics/a", []okf.GraphEdge{
		{To: "metrics/b", Type: "links_to"},
		{To: "metrics/c", Type: "links_to"},
	})
	if err != nil {
		t.Fatalf("add edges: %v", err)
	}
	if res.ConceptID != "metrics/a" || written == nil {
		t.Fatalf("result = %+v", res)
	}
	decl, err := written.Graph()
	if err != nil || len(decl.Edges) != 2 {
		t.Fatalf("merged edges = %+v (%v)", decl.Edges, err)
	}
}

func TestWriterLoadSurfacesMetadataFailures(t *testing.T) {
	ctx := context.Background()
	store := &docsStub{get: func(context.Context, string, string) (*okf.Document, error) {
		return nil, boom("store")
	}}
	w := writerWith(store, &bodyStub{}, &metaStub{metaErr: map[string]error{"metrics/a": boom("meta")}}, &indexStub{})
	if _, _, err := w.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/a"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("meta: %v", err)
	}
}

func TestWriterCommitErrorPaths(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "metric", Title: "A"}
	store := &docsStub{get: func(context.Context, string, string) (*okf.Document, error) { return doc, nil }}

	schemaDown := writerWith(store, &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}, listErr: boom("list")},
		&metaStub{}, &indexStub{})
	if _, _, err := schemaDown.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/a"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("schema: %v", err)
	}

	// The body is readable but the write is denied by ACL: never leak existence.
	denied := &docsStub{
		get: func(context.Context, string, string) (*okf.Document, error) { return doc, nil },
		put: func(context.Context, string, string, *okf.Document) (WriteResult, error) {
			return WriteResult{}, boom("put")
		},
	}
	w := writerWith(denied, &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}, &metaStub{acl: map[string]bool{"metrics/a": false}}, &indexStub{})
	if _, _, err := w.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("denied: %v", err)
	}
	// An unreadable ACL answer is a plain failure, not a not-found.
	w = writerWith(denied, &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}, &metaStub{aclErr: boom("acl")}, &indexStub{})
	if _, _, err := w.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/a"}); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("acl failure: %v", err)
	}
	// The ACL answers "allowed": the store error is returned untouched.
	w = writerWith(denied, &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}, &metaStub{}, &indexStub{})
	if _, _, err := w.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/a"}); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("store failure: %v", err)
	}

	// A failed projection never rolls back the body: it is reported as a warning.
	indexDown := &indexStub{upsert: func(meta.GraphNodeRecord, []meta.GraphEdgeRecord) error { return boom("upsert") }}
	w = writerWith(&docsStub{get: func(context.Context, string, string) (*okf.Document, error) { return doc, nil }},
		&bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}, &metaStub{}, indexDown)
	res, warnings, err := w.PutNode(ctx, "alice", NodeInput{ConceptID: "metrics/a"})
	if err != nil {
		t.Fatalf("sync warning: %v", err)
	}
	if res.ConceptID != "metrics/a" || len(warnings) != 1 || warnings[0] != "index_sync_failed: metrics/a" {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestWriterLookupFallsBackToTheBody(t *testing.T) {
	ctx := context.Background()
	typed := &okf.Document{Type: "metric"}
	if err := typed.SetGraph(okf.GraphDecl{Node: okf.GraphNode{Type: "table"}}); err != nil {
		t.Fatalf("graph: %v", err)
	}
	bodies := &bodyStub{docs: map[string]*okf.Document{
		"metrics/typed":   typed,
		"metrics/plain":   {Type: "metric"},
		"metrics/untyped": {},
	}}
	w := &Writer{Projector: &Projector{Bodies: bodies, Meta: &metaStub{}, Index: &indexStub{}}}

	if got, ok := w.lookup(ctx, "metrics/typed"); !ok || got != "table" {
		t.Fatalf("declared node type = %q (%v)", got, ok)
	}
	if got, ok := w.lookup(ctx, "metrics/plain"); !ok || got != "metric" {
		t.Fatalf("document type = %q (%v)", got, ok)
	}
	if _, ok := w.lookup(ctx, "metrics/untyped"); ok {
		t.Fatal("an untyped body must not resolve")
	}
	if _, ok := w.lookup(ctx, "metrics/absent"); ok {
		t.Fatal("a missing body must not resolve")
	}
}

// --- projector: identity seams -------------------------------------------

func TestSyncAndRebuildReportMarshalFailures(t *testing.T) {
	previous := marshalDocument
	t.Cleanup(func() { marshalDocument = previous })
	marshalDocument = func(*okf.Document) ([]byte, error) { return nil, boom("marshal") }

	doc := &okf.Document{Type: "metric"}
	p := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}, list: []string{"metrics/a"}},
		Meta:   &metaStub{}, Index: &indexStub{},
	}
	if _, _, _, err := p.Sync(context.Background(), "metrics/a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sync: %v", err)
	}
	report, err := p.Rebuild(context.Background())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if len(report.Warnings) != 1 || report.Warnings[0] != "invalid_document: metrics/a" {
		t.Fatalf("warnings = %v", report.Warnings)
	}
}

func TestSyncWithRejectsCanceledContext(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	p := &Projector{Bodies: &bodyStub{}, Meta: &metaStub{}, Index: &indexStub{}}
	if _, _, _, err := p.SyncWith(canceled, "metrics/a", okf.GraphSchema{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

// --- remote.go ------------------------------------------------------------

func TestRemoteOptionalFiltersAndNilOutcome(t *testing.T) {
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		switch r.URL.Path {
		case "/v1/graph/neighbors":
			_, _ = w.Write([]byte(`{"root":"metrics/a","nodes":[],"edges":[],"depths":{}}`))
		case "/v1/graph/path":
			_, _ = w.Write([]byte(`{"found":false}`))
		case "/v1/graph/schema":
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	r, err := NewRemote(RemoteConfig{Endpoint: srv.URL, Token: "secret", AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.Neighbors(ctx, "alice", WalkQuery{
		From: "metrics/a", NodeTypes: []string{"metric", "table"},
	}); err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if got := queries[len(queries)-1].Get("node_types"); got != "metric,table" {
		t.Fatalf("node types = %q", got)
	}
	if _, err := r.Path(ctx, "alice", PathQuery{
		From: "metrics/a", To: "metrics/b", EdgeTypes: []string{"derived_from"},
	}); err != nil {
		t.Fatalf("path: %v", err)
	}
	if got := queries[len(queries)-1].Get("edge_types"); got != "derived_from" {
		t.Fatalf("edge types = %q", got)
	}

	// A 200 with no decoded body is success, not an empty decode target.
	if err := r.call(ctx, "alice", http.MethodGet, "/v1/graph/noop", nil, nil, nil); err != nil {
		t.Fatalf("nil out: %v", err)
	}
	// A body that cannot be encoded and a missing context both fail closed.
	if err := r.call(ctx, "", http.MethodPost, "/v1/graph/p", nil, make(chan int), &struct{}{}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("encode: %v", err)
	}
	if err := r.call(nil, "", http.MethodGet, "/v1/graph/p", nil, nil, &struct{}{}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("nil context: %v", err)
	}
	if _, _, err := r.Schema(ctx); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("schema: %v", err)
	}
}

// --- second reads that fail after a first read succeeded -------------------

func TestRebuildFailsWhenTheSchemaPassFails(t *testing.T) {
	doc := &okf.Document{Type: "metric"}
	p := &Projector{
		Bodies: &flakyBodies{bodyStub: bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}},
		Meta:   &metaStub{}, Index: &indexStub{},
	}
	if _, err := p.Rebuild(context.Background()); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("rebuild: %v", err)
	}
}

func TestSearcherNodeTreatsAnUnreadableBodyAsMissing(t *testing.T) {
	ctx := context.Background()
	doc := &okf.Document{Type: "metric"}
	bodies := &flakyBodies{bodyStub: bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}}}
	if _, err := searcher(freshIndex(graphHash(t, doc)), &metaStub{}, bodies).Node(ctx, "alice", "metrics/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("node: %v", err)
	}
}

func TestAnswerabilityBoundsEdgeChecks(t *testing.T) {
	ctx := context.Background()
	edges := make([]meta.GraphEdgeRecord, 0, 201)
	for i := 0; i < 201; i++ {
		edges = append(edges, meta.GraphEdgeRecord{
			FromID: "metrics/a", ToID: fmt.Sprintf("metrics/x%d", i), EdgeType: "links_to",
		})
	}
	idx := &indexStub{
		stats: func(string) (meta.GraphStats, error) { return meta.GraphStats{Nodes: 2}, nil },
		search: func(q meta.GraphSearch) ([]meta.GraphNodeRecord, error) {
			if q.Text == "revenue" {
				return []meta.GraphNodeRecord{{ConceptID: "metrics/a", NodeType: "metric", Label: "Revenue"}}, nil
			}
			return []meta.GraphNodeRecord{{ConceptID: "metrics/b", NodeType: "metric", Label: "Margin"}}, nil
		},
		edges: func([]string, string) ([]meta.GraphEdgeRecord, error) { return edges, nil },
	}
	// Empty schema keeps every fixture edge a plain dangling target.
	cov, err := searcher(idx, &metaStub{}, &bodyStub{list: []string{}}).Answerability(ctx, "alice", "revenue margin", 0.6)
	if err != nil {
		t.Fatalf("answerability: %v", err)
	}
	if len(cov.Matched) != 2 {
		t.Fatalf("matched = %+v", cov.Matched)
	}
}
