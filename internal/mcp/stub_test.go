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

// stubBackend is the offline port used by the protocol tests. Every method
// answers a zero value unless err is set, so a new tool cannot silently pass by
// accident and one switch covers every failure path.
type stubBackend struct {
	err error

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

	putNodeErr error
	edgesErr   error
	rebuildErr error
}

func (b *stubBackend) Query(context.Context, string, string, int) (router.QueryResult, error) {
	if b.err != nil {
		return router.QueryResult{}, b.err
	}
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
	if b.err != nil {
		return nil, b.err
	}
	return b.doc, b.docErr
}

func (b *stubBackend) Concepts(context.Context) ([]string, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.concepts, nil
}

func (b *stubBackend) Decisions(context.Context, string, int) ([]meta.DecisionRecord, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.decisions, nil
}

func (b *stubBackend) Computations(context.Context, string, int) ([]meta.ComputationRecord, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.comps, nil
}

func (b *stubBackend) RunComputation(context.Context, compute.Request) (compute.Envelope, error) {
	return b.envelope, b.computeErr
}

func (b *stubBackend) GraphEnabled() bool { return true }

func (b *stubBackend) MaxGraphDepth() int { return 8 }

func (b *stubBackend) GraphStats(context.Context, string) (meta.GraphStats, error) {
	if b.err != nil {
		return meta.GraphStats{}, b.err
	}
	return b.stats, nil
}

func (b *stubBackend) GraphNode(context.Context, string, string) (graph.NodeView, error) {
	b.nodeCalls++
	if b.err != nil {
		return graph.NodeView{}, b.err
	}
	return b.node, b.nodeErr
}

func (b *stubBackend) GraphNeighbors(context.Context, string, graph.WalkQuery) (graph.SubgraphView, error) {
	if b.err != nil {
		return graph.SubgraphView{}, b.err
	}
	return graph.SubgraphView{Root: "root"}, nil
}

func (b *stubBackend) GraphPath(context.Context, string, graph.PathQuery) (graph.PathView, error) {
	if b.err != nil {
		return graph.PathView{}, b.err
	}
	return graph.PathView{From: "a", To: "b", Found: true}, nil
}

func (b *stubBackend) GraphSearch(context.Context, string, graph.SearchQuery) ([]graph.NodeHit, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.hits, b.searchErr
}

func (b *stubBackend) GraphSchema(context.Context) (okf.GraphSchema, []string, error) {
	if b.err != nil {
		return okf.GraphSchema{}, nil, b.err
	}
	return b.schema, b.warnings, nil
}

func (b *stubBackend) GraphAnswerability(context.Context, string, string) (graph.Coverage, error) {
	if b.err != nil {
		return graph.Coverage{}, b.err
	}
	return b.coverage, nil
}

func (b *stubBackend) GraphRetrieve(context.Context, string, string, int) (graph.RetrieveResult, error) {
	if b.err != nil {
		return graph.RetrieveResult{}, b.err
	}
	return b.retrieve, nil
}

func (b *stubBackend) GraphPutNode(context.Context, string, graph.NodeInput) (graph.WriteResult, []string, error) {
	b.nodeCalls++
	if b.putNodeErr != nil {
		return graph.WriteResult{}, nil, b.putNodeErr
	}
	return graph.WriteResult{ConceptID: "n", ContentHash: "h"}, []string{"warn"}, nil
}

func (b *stubBackend) GraphAddEdges(context.Context, string, string, []okf.GraphEdge) (graph.WriteResult, []string, error) {
	b.edgesCalls++
	if b.edgesErr != nil {
		return graph.WriteResult{}, nil, b.edgesErr
	}
	return graph.WriteResult{ConceptID: "n", ContentHash: "h"}, nil, nil
}

func (b *stubBackend) GraphRebuild(context.Context) (graph.RebuildReport, error) {
	b.rebuildCalls++
	if b.rebuildErr != nil {
		return graph.RebuildReport{}, b.rebuildErr
	}
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
