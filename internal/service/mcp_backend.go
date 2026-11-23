package service

import (
	"context"

	"agentmem/internal/compute"
	"agentmem/internal/graph"
	"agentmem/internal/mcp"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/router"
)

// MCPBackend adapts KnowledgeService to the MCP protocol port.
//
// Every method forwards to an existing use case, so the MCP surface inherits
// exactly the same ACL filtering, Git write path and audit records as REST and
// CLI. The adapter is deliberately thin: if a tool needs behaviour the service
// does not already implement, the tool does not exist.
type MCPBackend struct{ svc *KnowledgeService }

// NewMCPBackend returns the MCP port implementation for svc.
func NewMCPBackend(svc *KnowledgeService) mcp.Backend { return &MCPBackend{svc: svc} }

func (b *MCPBackend) Query(ctx context.Context, subject, text string, topK int) (router.QueryResult, error) {
	return b.svc.Query(ctx, subject, text, topK)
}

func (b *MCPBackend) Put(ctx context.Context, subject, id string, doc *okf.Document) (router.WriteResult, error) {
	return b.svc.Put(ctx, subject, id, doc)
}

func (b *MCPBackend) Knowledge(ctx context.Context, subject, id string) (*okf.Document, error) {
	return b.svc.Store.Get(ctx, subject, id)
}

func (b *MCPBackend) Concepts(ctx context.Context) ([]string, error) {
	return b.svc.Concepts(ctx)
}

func (b *MCPBackend) Decisions(ctx context.Context, conceptID string, limit int) ([]meta.DecisionRecord, error) {
	return b.svc.Decisions(ctx, conceptID, limit)
}

func (b *MCPBackend) Computations(ctx context.Context, conceptID string, limit int) ([]meta.ComputationRecord, error) {
	return b.svc.ComputationAudit(ctx, conceptID, limit)
}

func (b *MCPBackend) RunComputation(ctx context.Context, req compute.Request) (compute.Envelope, error) {
	return b.svc.RunComputation(ctx, req)
}

func (b *MCPBackend) GraphEnabled() bool { return b.svc.GraphEnabled() }

func (b *MCPBackend) MaxGraphDepth() int { return b.svc.MaxGraphDepth() }

func (b *MCPBackend) GraphStats(ctx context.Context, subject string) (meta.GraphStats, error) {
	return b.svc.GraphStats(ctx, subject)
}

func (b *MCPBackend) GraphNode(ctx context.Context, subject, id string) (graph.NodeView, error) {
	return b.svc.GraphNode(ctx, subject, id)
}

func (b *MCPBackend) GraphNeighbors(ctx context.Context, subject string, q graph.WalkQuery) (graph.SubgraphView, error) {
	return b.svc.GraphNeighbors(ctx, subject, q)
}

func (b *MCPBackend) GraphPath(ctx context.Context, subject string, q graph.PathQuery) (graph.PathView, error) {
	return b.svc.GraphPath(ctx, subject, q)
}

func (b *MCPBackend) GraphSearch(ctx context.Context, subject string, q graph.SearchQuery) ([]graph.NodeHit, error) {
	return b.svc.GraphSearch(ctx, subject, q)
}

func (b *MCPBackend) GraphSchema(ctx context.Context) (okf.GraphSchema, []string, error) {
	return b.svc.GraphSchema(ctx)
}

func (b *MCPBackend) GraphAnswerability(ctx context.Context, subject, question string) (graph.Coverage, error) {
	return b.svc.GraphAnswerability(ctx, subject, question)
}

func (b *MCPBackend) GraphRetrieve(ctx context.Context, subject, text string, topK int) (graph.RetrieveResult, error) {
	return b.svc.GraphRetrieve(ctx, subject, text, topK)
}

func (b *MCPBackend) GraphPutNode(ctx context.Context, subject string, in graph.NodeInput) (graph.WriteResult, []string, error) {
	return b.svc.GraphPutNode(ctx, subject, in)
}

func (b *MCPBackend) GraphAddEdges(ctx context.Context, subject, id string, edges []okf.GraphEdge) (graph.WriteResult, []string, error) {
	return b.svc.GraphAddEdges(ctx, subject, id, edges)
}

func (b *MCPBackend) GraphRebuild(ctx context.Context) (graph.RebuildReport, error) {
	return b.svc.GraphRebuild(ctx)
}
