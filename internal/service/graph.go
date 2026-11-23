package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"agentmem/internal/graph"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// ErrGraphDisabled is returned when the graph feature is switched off. Handlers
// map it to the same JSON 404 as an unknown path, so a disabled feature is not
// advertised.
var ErrGraphDisabled = graph.ErrDisabled

// GraphEnabled reports whether the graph surfaces are assembled.
func (s *KnowledgeService) GraphEnabled() bool { return s.Graph != nil }

// MaxGraphDepth is the configured traversal ceiling, clamped to the port's
// hard limits so a bad .env value can never widen a request.
func (s *KnowledgeService) MaxGraphDepth() int {
	depth := 3
	if s.Config != nil && s.Config.Graph.MaxDepth > 0 {
		depth = s.Config.Graph.MaxDepth
	}
	if depth > 8 {
		depth = 8
	}
	return depth
}

func (s *KnowledgeService) requireGraph() error {
	if s.Graph == nil {
		return ErrGraphDisabled
	}
	return nil
}

// GraphStats returns visibility-scoped aggregate counts.
func (s *KnowledgeService) GraphStats(ctx context.Context, subject string) (meta.GraphStats, error) {
	if err := s.requireGraph(); err != nil {
		return meta.GraphStats{}, err
	}
	return s.Graph.Stats(ctx, subject)
}

// GraphNode returns one node with lineage and both edge directions.
func (s *KnowledgeService) GraphNode(ctx context.Context, subject, id string) (graph.NodeView, error) {
	if err := s.requireGraph(); err != nil {
		return graph.NodeView{}, err
	}
	return s.Graph.Node(ctx, subject, id)
}

// GraphNeighbors expands a bounded neighbourhood.
func (s *KnowledgeService) GraphNeighbors(ctx context.Context, subject string, q graph.WalkQuery) (graph.SubgraphView, error) {
	if err := s.requireGraph(); err != nil {
		return graph.SubgraphView{}, err
	}
	return s.Graph.Neighbors(ctx, subject, q)
}

// GraphPath returns the shortest readable path between two nodes.
func (s *KnowledgeService) GraphPath(ctx context.Context, subject string, q graph.PathQuery) (graph.PathView, error) {
	if err := s.requireGraph(); err != nil {
		return graph.PathView{}, err
	}
	return s.Graph.Path(ctx, subject, q)
}

// GraphSearch runs a deterministic keyword lookup.
func (s *KnowledgeService) GraphSearch(ctx context.Context, subject string, q graph.SearchQuery) ([]graph.NodeHit, error) {
	if err := s.requireGraph(); err != nil {
		return nil, err
	}
	return s.Graph.Search(ctx, subject, q)
}

// GraphSchema returns the typed ontology and its warnings.
func (s *KnowledgeService) GraphSchema(ctx context.Context) (okf.GraphSchema, []string, error) {
	if err := s.requireGraph(); err != nil {
		return okf.GraphSchema{}, nil, err
	}
	return s.Graph.Schema(ctx)
}

// GraphAnswerability reports coverage and concrete gaps for a question.
func (s *KnowledgeService) GraphAnswerability(ctx context.Context, subject, question string) (graph.Coverage, error) {
	if err := s.requireGraph(); err != nil {
		return graph.Coverage{}, err
	}
	minimum := 0.6
	if s.Config != nil {
		minimum = s.Config.Graph.CoverageMin
	}
	return s.Graph.Answerability(ctx, subject, question, minimum)
}

// GraphPutNode creates or updates a node through the canonical store.
func (s *KnowledgeService) GraphPutNode(ctx context.Context, subject string, in graph.NodeInput) (graph.WriteResult, []string, error) {
	if err := s.requireGraph(); err != nil {
		return graph.WriteResult{}, nil, err
	}
	if s.GraphWriter == nil {
		// A remote read engine never accepts writes: the canonical store is
		// always local, but the graph writer is only assembled for builtin.
		return graph.WriteResult{}, nil, fmt.Errorf("%w: graph writes require the builtin backend", graph.ErrInvalid)
	}
	return s.GraphWriter.PutNode(ctx, subject, in)
}

// GraphAddEdges appends declared relations to an existing node.
func (s *KnowledgeService) GraphAddEdges(ctx context.Context, subject, id string, edges []okf.GraphEdge) (graph.WriteResult, []string, error) {
	if err := s.requireGraph(); err != nil {
		return graph.WriteResult{}, nil, err
	}
	if s.GraphWriter == nil {
		return graph.WriteResult{}, nil, fmt.Errorf("%w: graph writes require the builtin backend", graph.ErrInvalid)
	}
	return s.GraphWriter.AddEdges(ctx, subject, id, edges)
}

// GraphRebuild reindexes the whole bundle.
func (s *KnowledgeService) GraphRebuild(ctx context.Context) (graph.RebuildReport, error) {
	if err := s.requireGraph(); err != nil {
		return graph.RebuildReport{}, err
	}
	if s.GraphProjector == nil {
		return graph.RebuildReport{}, fmt.Errorf("%w: reindex requires the builtin backend", graph.ErrInvalid)
	}
	return s.GraphProjector.Rebuild(ctx)
}

// GraphRetrieve fuses the text ranker with the graph ranker using RRF. It is a
// new read shape: the existing /v1/query contract is unchanged.
func (s *KnowledgeService) GraphRetrieve(ctx context.Context, subject, text string, topK int) (graph.RetrieveResult, error) {
	if err := s.requireGraph(); err != nil {
		return graph.RetrieveResult{}, err
	}
	if topK <= 0 {
		topK = 10
	}
	if topK > 100 {
		topK = 100
	}
	out := graph.RetrieveResult{Query: text, Hits: []graph.Fused{}, Nodes: []graph.NodeHit{}}
	lists := [][]graph.Ranked{}

	textRes, terr := s.Read.Query(ctx, subject, text, topK)
	if terr == nil {
		list := make([]graph.Ranked, 0, len(textRes.Hits))
		for i, h := range textRes.Hits {
			list = append(list, graph.Ranked{ID: h.ConceptID, Source: "text", Rank: i + 1})
		}
		lists = append(lists, list)
	} else {
		out.Warnings = append(out.Warnings, "text_ranker_unavailable")
	}

	hits, err := s.Graph.Search(ctx, subject, graph.SearchQuery{Text: text, Limit: topK})
	if err != nil {
		out.Warnings = append(out.Warnings, "graph_search_unavailable")
	} else {
		list := make([]graph.Ranked, 0, len(hits))
		nodes := map[string]graph.NodeHit{}
		for i, h := range hits {
			list = append(list, graph.Ranked{ID: h.ConceptID, Source: "graph", Rank: i + 1})
			nodes[h.ConceptID] = h
		}
		lists = append(lists, list)
		// Expand the strongest graph hits one hop: neighbours are context, not
		// new answers, so they enter the fusion list at a lower rank.
		seen := map[string]bool{}
		for i, h := range hits {
			if i >= 3 {
				break
			}
			sub, err := s.Graph.Neighbors(ctx, subject, graph.WalkQuery{From: h.ConceptID, Depth: 1, Limit: topK})
			if err != nil {
				continue
			}
			for _, n := range sub.Nodes {
				if n.ConceptID == h.ConceptID || seen[n.ConceptID] {
					continue
				}
				seen[n.ConceptID] = true
				nodes[n.ConceptID] = n
			}
		}
		if len(seen) > 0 {
			ids := make([]string, 0, len(seen))
			for id := range seen {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			expanded := make([]graph.Ranked, 0, len(ids))
			for i, id := range ids {
				expanded = append(expanded, graph.Ranked{ID: id, Source: "graph", Rank: i + 1})
			}
			lists = append(lists, expanded)
		}
		for _, h := range hits {
			out.Nodes = append(out.Nodes, h)
		}
		for _, n := range nodes {
			if !containsNode(out.Nodes, n.ConceptID) {
				out.Nodes = append(out.Nodes, n)
			}
		}
	}

	k := 60
	if s.Config != nil && s.Config.Graph.RRFK > 0 {
		k = s.Config.Graph.RRFK
	}
	out.Hits = graph.RRF(lists, k)
	if len(out.Hits) > topK {
		out.Hits = out.Hits[:topK]
	}
	// Keep only the nodes that survived the fusion cut, in fused order.
	byID := map[string]graph.NodeHit{}
	for _, n := range out.Nodes {
		byID[n.ConceptID] = n
	}
	ordered := make([]graph.NodeHit, 0, len(out.Hits))
	for _, f := range out.Hits {
		if n, ok := byID[f.ID]; ok {
			ordered = append(ordered, n)
		}
	}
	out.Nodes = ordered
	out.Warnings = dedupe(out.Warnings)
	return out, nil
}

func containsNode(nodes []graph.NodeHit, id string) bool {
	for _, n := range nodes {
		if n.ConceptID == id {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
