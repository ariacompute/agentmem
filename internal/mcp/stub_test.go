package mcp

import (
	"context"
	"testing"

	"agentmem/internal/auth"
	"agentmem/internal/compute"
	"agentmem/internal/graph"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/router"
)

// stubBackend is the offline port used by the protocol tests. Only the calls a
// test asserts on are configurable; everything else answers a zero value so a
// new tool cannot silently pass by accident.
type stubBackend struct {
	node       graph.NodeView
	nodeErr    error
	hits       []graph.NodeHit
	searchErr  error
	doc        *okf.Document
	docErr     error
	concepts   []string
	decisions  []meta.DecisionRecord
	comps      []meta.ComputationRecord
	stats      meta.GraphStats
	schema     okf.GraphSchema
	warnings   []string
	coverage   graph.Coverage
	retrieve   graph.RetrieveResult
	query      router.QueryResult
	queryErr   error
	envelope   compute.Envelope
	computeErr error

	putSubject string
	putID      string
	putDoc     *okf.Document
	putErr     error
	putCalls   int

	rebuildCalls int
	nodeCalls    int
	edgesCalls   int
}

func (b *stubBackend) Query(context.Context, string, string, int) (router.QueryResult, error) {
	return b.query, b.queryErr
}

func (b *stubBackend) Put(_ context.Context, subject, id string, doc *okf.Document) (router.WriteResult, error) {
	b.putCalls++
	b.putSubject, b.putID, b.putDoc = subject, id, doc
	if b.putErr != nil {
		return router.WriteResult{}, b.putErr
	}
	return router.WriteResult{ConceptID: id, ContentHash: "hash", CommitSHA: "sha"}, nil
}

func (b *stubBackend) Knowledge(context.Context, string, string) (*okf.Document, error) {
	return b.doc, b.docErr
}

func (b *stubBackend) Concepts(context.Context) ([]string, error) { return b.concepts, nil }

func (b *stubBackend) Decisions(context.Context, string, int) ([]meta.DecisionRecord, error) {
	return b.decisions, nil
}

func (b *stubBackend) Computations(context.Context, string, int) ([]meta.ComputationRecord, error) {
	return b.comps, nil
}

func (b *stubBackend) RunComputation(context.Context, compute.Request) (compute.Envelope, error) {
	return b.envelope, b.computeErr
}

func (b *stubBackend) GraphEnabled() bool { return true }

func (b *stubBackend) MaxGraphDepth() int { return 8 }

func (b *stubBackend) GraphStats(context.Context, string) (meta.GraphStats, error) {
	return b.stats, nil
}

func (b *stubBackend) GraphNode(context.Context, string, string) (graph.NodeView, error) {
	b.nodeCalls++
	return b.node, b.nodeErr
}

func (b *stubBackend) GraphNeighbors(context.Context, string, graph.WalkQuery) (graph.SubgraphView, error) {
	return graph.SubgraphView{Root: "root"}, nil
}

func (b *stubBackend) GraphPath(context.Context, string, graph.PathQuery) (graph.PathView, error) {
	return graph.PathView{From: "a", To: "b", Found: true}, nil
}

func (b *stubBackend) GraphSearch(context.Context, string, graph.SearchQuery) ([]graph.NodeHit, error) {
	return b.hits, b.searchErr
}

func (b *stubBackend) GraphSchema(context.Context) (okf.GraphSchema, []string, error) {
	return b.schema, b.warnings, nil
}

func (b *stubBackend) GraphAnswerability(context.Context, string, string) (graph.Coverage, error) {
	return b.coverage, nil
}

func (b *stubBackend) GraphRetrieve(context.Context, string, string, int) (graph.RetrieveResult, error) {
	return b.retrieve, nil
}

func (b *stubBackend) GraphPutNode(context.Context, string, graph.NodeInput) (graph.WriteResult, []string, error) {
	b.nodeCalls++
	return graph.WriteResult{ConceptID: "n", ContentHash: "h"}, []string{"warn"}, nil
}

func (b *stubBackend) GraphAddEdges(context.Context, string, string, []okf.GraphEdge) (graph.WriteResult, []string, error) {
	b.edgesCalls++
	return graph.WriteResult{ConceptID: "n", ContentHash: "h"}, nil, nil
}

func (b *stubBackend) GraphRebuild(context.Context) (graph.RebuildReport, error) {
	b.rebuildCalls++
	return graph.RebuildReport{Nodes: 1, Edges: 2}, nil
}

func readerPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	p, err := auth.DeclaredPrincipal("reader", []auth.Permission{auth.PermKnowledgeRead})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func fullPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	p, err := auth.DeclaredPrincipal("admin", []auth.Permission{
		auth.PermHealthRead, auth.PermKnowledgeRead, auth.PermKnowledgeWrite,
		auth.PermMigrateRun, auth.PermAuditRead, auth.PermComputeRun, auth.PermGraphWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
