package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// RetrieveResult is the fused output of the text ranker and the graph ranker.
type RetrieveResult struct {
	Query    string    `json:"query"`
	Hits     []Fused   `json:"hits"`
	Nodes    []NodeHit `json:"nodes"`
	Warnings []string  `json:"warnings,omitempty"`
}

// Query is the read surface. The built-in searcher and the optional remote
// engine both satisfy it, so the service can switch backends without changing
// handlers.
type Query interface {
	Node(ctx context.Context, subject, conceptID string) (NodeView, error)
	Neighbors(ctx context.Context, subject string, q WalkQuery) (SubgraphView, error)
	Path(ctx context.Context, subject string, q PathQuery) (PathView, error)
	Search(ctx context.Context, subject string, q SearchQuery) ([]NodeHit, error)
	Answerability(ctx context.Context, subject, question string, minimum float64) (Coverage, error)
	Stats(ctx context.Context, subject string) (meta.GraphStats, error)
	Schema(ctx context.Context) (okf.GraphSchema, []string, error)
}

// Searcher answers graph queries from the local index, applying ACL filtering
// before anything is counted or returned.
type Searcher struct {
	Meta      Metadata
	Index     meta.GraphIndex
	Projector *Projector
	Bodies    BodyReader

	mu           sync.Mutex
	schema       okf.GraphSchema
	schemaWarns  []string
	schemaLoaded bool
}

// InvalidateSchema drops the cached ontology after a write.
func (s *Searcher) InvalidateSchema() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schemaLoaded = false
}

func (s *Searcher) Schema(ctx context.Context) (okf.GraphSchema, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schemaLoaded {
		return s.schema, s.schemaWarns, nil
	}
	schema, warnings, err := s.Projector.LoadSchema(ctx)
	if err != nil {
		return okf.GraphSchema{}, nil, err
	}
	s.schema, s.schemaWarns, s.schemaLoaded = schema, warnings, true
	return schema, warnings, nil
}

func (s *Searcher) allowed(ctx context.Context, subject, id string, memo map[string]bool) (bool, error) {
	if v, ok := memo[id]; ok {
		return v, nil
	}
	ok, err := s.Meta.CheckACL(ctx, subject, id)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	memo[id] = ok
	return ok, nil
}

// Node returns one node with its lineage and both edge directions. Edges whose
// other endpoint is not readable are dropped, never rendered as a redacted row.
func (s *Searcher) Node(ctx context.Context, subject, conceptID string) (NodeView, error) {
	clean, err := okf.ValidateConceptID(conceptID)
	if err != nil {
		return NodeView{}, ErrNotFound
	}
	memo := map[string]bool{}
	ok, err := s.allowed(ctx, subject, clean, memo)
	if err != nil {
		return NodeView{}, err
	}
	if !ok {
		return NodeView{}, ErrNotFound
	}
	n, _, err := s.Projector.EnsureFresh(ctx, clean)
	if err != nil {
		if errors.Is(err, ErrNotFound) || meta.IsNotFound(err) {
			return NodeView{}, ErrNotFound
		}
		return NodeView{}, err
	}
	doc, err := s.Bodies.Get(clean)
	if err != nil {
		return NodeView{}, ErrNotFound
	}
	view := NodeView{
		ConceptID: n.ConceptID, NodeType: n.NodeType, Label: n.Label,
		Description: n.Description, Status: n.Status, Trust: n.Trust,
		Visibility: n.Visibility, Properties: n.Properties,
		Resource: strings.TrimSpace(doc.Resource), ContentHash: n.ContentHash, CommitSHA: n.CommitSHA,
		Sources: []SourceView{}, Out: []EdgeView{}, In: []EdgeView{}, Warnings: []string{},
	}
	for _, src := range doc.Sources {
		view.Sources = append(view.Sources, SourceView{
			ID: src.ID, Resource: src.Resource, Title: src.Title, Author: src.Author, LastModified: src.LastModified,
		})
	}
	if doc.Attested != nil && doc.Attested.Runtime != "" {
		c := &ComputationView{Runtime: doc.Attested.Runtime, Computation: doc.Attested.Computation}
		for _, p := range doc.Attested.Parameters {
			c.Parameters = append(c.Parameters, p.Name+":"+p.Type)
		}
		view.Computation = c
	}
	if stale, err := s.Index.StaleGraphNodes(ctx, []string{clean}); err == nil {
		view.Stale = stale[clean]
	}
	edges, warnings, err := s.nodeEdges(ctx, subject, clean, memo)
	if err != nil {
		return NodeView{}, err
	}
	view.Warnings = append(view.Warnings, warnings...)
	for _, e := range edges {
		if e.FromID == clean {
			view.Out = append(view.Out, e)
		}
		if e.ToID == clean {
			view.In = append(view.In, e)
		}
	}
	schema, schemaWarnings, err := s.Schema(ctx)
	if err == nil && !schema.Empty() {
		for _, e := range edges {
			if _, ok := schema.EdgeTypes[e.EdgeType]; !ok {
				view.Warnings = append(view.Warnings, GapUnknownEdgeType+": "+e.EdgeType)
			}
		}
		view.Warnings = append(view.Warnings, schemaWarnings...)
	}
	sort.Strings(view.Warnings)
	view.Warnings = dedupeStrings(view.Warnings)
	return view, nil
}

// nodeEdges loads the depth-1 neighbourhood and keeps only readable edges that
// touch the node. Dangling targets become warnings rather than fake nodes.
func (s *Searcher) nodeEdges(ctx context.Context, subject, id string, memo map[string]bool) ([]EdgeView, []string, error) {
	edges, err := s.Index.GraphEdges(ctx, []string{id}, "both")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	var warnings []string
	out := make([]EdgeView, 0, len(edges))
	for _, e := range edges {
		if e.FromID != id && e.ToID != id {
			continue
		}
		other := e.ToID
		if other == id {
			other = e.FromID
		}
		// Existence is checked before ACL: a dangling edge has no metadata row,
		// and must surface as a warning rather than look like a private node.
		if _, err := s.Index.GetGraphNode(ctx, other); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %s -> %s", GapDanglingEdge, e.EdgeType, other))
			continue
		}
		ok, err := s.allowed(ctx, subject, other, memo)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		out = append(out, edgeOf(e))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromID != out[j].FromID {
			return out[i].FromID < out[j].FromID
		}
		if out[i].ToID != out[j].ToID {
			return out[i].ToID < out[j].ToID
		}
		return out[i].EdgeType < out[j].EdgeType
	})
	return out, warnings, nil
}

// Neighbors expands a bounded neighbourhood and filters both nodes and edges by
// readability before counting.
func (s *Searcher) Neighbors(ctx context.Context, subject string, q WalkQuery) (SubgraphView, error) {
	clean, err := okf.ValidateConceptID(q.From)
	if err != nil {
		return SubgraphView{}, ErrNotFound
	}
	memo := map[string]bool{}
	ok, err := s.allowed(ctx, subject, clean, memo)
	if err != nil {
		return SubgraphView{}, err
	}
	if !ok {
		return SubgraphView{}, ErrNotFound
	}
	if _, _, err := s.Projector.EnsureFresh(ctx, clean); err != nil {
		if errors.Is(err, ErrNotFound) || meta.IsNotFound(err) {
			return SubgraphView{}, ErrNotFound
		}
		return SubgraphView{}, err
	}
	res, err := s.Index.GraphNeighbors(ctx, meta.GraphWalk{
		Sources: []string{clean}, Depth: q.Depth, Direction: q.Direction,
		EdgeTypes: q.EdgeTypes, NodeTypes: q.NodeTypes, MaxNodes: q.Limit,
	})
	if err != nil {
		return SubgraphView{}, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	ids := make([]string, 0, len(res.Nodes))
	for _, n := range res.Nodes {
		ids = append(ids, n.ConceptID)
	}
	stale, err := s.Index.StaleGraphNodes(ctx, ids)
	if err != nil {
		return SubgraphView{}, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	view := SubgraphView{
		Root: clean, Nodes: []NodeHit{}, Edges: []EdgeView{},
		Depths: map[string]int{}, Truncated: res.Truncated,
	}
	visible := map[string]bool{}
	for _, n := range res.Nodes {
		ok, err := s.allowed(ctx, subject, n.ConceptID, memo)
		if err != nil {
			return SubgraphView{}, err
		}
		if !ok {
			continue
		}
		visible[n.ConceptID] = true
		view.Nodes = append(view.Nodes, hitOf(n, stale[n.ConceptID]))
		view.Depths[n.ConceptID] = res.Depths[n.ConceptID]
	}
	for _, e := range res.Edges {
		if visible[e.FromID] && visible[e.ToID] {
			view.Edges = append(view.Edges, edgeOf(e))
		}
	}
	return view, nil
}

// Path returns the shortest path only when every step is readable.
func (s *Searcher) Path(ctx context.Context, subject string, q PathQuery) (PathView, error) {
	from, err := okf.ValidateConceptID(q.From)
	if err != nil {
		return PathView{}, ErrNotFound
	}
	to, err := okf.ValidateConceptID(q.To)
	if err != nil {
		return PathView{}, ErrNotFound
	}
	memo := map[string]bool{}
	for _, id := range []string{from, to} {
		ok, err := s.allowed(ctx, subject, id, memo)
		if err != nil {
			return PathView{}, err
		}
		if !ok {
			return PathView{}, ErrNotFound
		}
	}
	res, err := s.Index.GraphShortestPath(ctx, meta.GraphPath{
		From: from, To: to, MaxDepth: q.MaxDepth, Direction: q.Direction,
		EdgeTypes: q.EdgeTypes, MaxNodes: q.Limit,
	})
	if err != nil {
		return PathView{}, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	view := PathView{From: from, To: to, Found: res.Found, Nodes: []NodeHit{}, Edges: []EdgeView{}, Hops: len(res.Edges)}
	if !res.Found {
		return view, nil
	}
	ids := make([]string, 0, len(res.Nodes))
	for _, n := range res.Nodes {
		ok, err := s.allowed(ctx, subject, n.ConceptID, memo)
		if err != nil {
			return PathView{}, err
		}
		if !ok {
			// A hidden hop makes the whole path invisible.
			return PathView{From: from, To: to, Found: false, Nodes: []NodeHit{}, Edges: []EdgeView{}}, nil
		}
		ids = append(ids, n.ConceptID)
	}
	stale, _ := s.Index.StaleGraphNodes(ctx, ids)
	for _, n := range res.Nodes {
		view.Nodes = append(view.Nodes, hitOf(n, stale[n.ConceptID]))
	}
	for _, e := range res.Edges {
		view.Edges = append(view.Edges, edgeOf(e))
	}
	return view, nil
}

// Search runs a deterministic keyword lookup and filters before truncating, so
// a page never comes back short because of hidden rows.
func (s *Searcher) Search(ctx context.Context, subject string, q SearchQuery) ([]NodeHit, error) {
	q = SearchQuery{Text: q.Text, NodeTypes: q.NodeTypes, Limit: q.Limit}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	wide := meta.GraphSearch{Text: q.Text, NodeTypes: q.NodeTypes, Limit: limit * 4}
	if wide.Limit > 200 {
		wide.Limit = 200
	}
	nodes, err := s.Index.SearchGraphNodes(ctx, wide)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ConceptID)
	}
	stale, err := s.Index.StaleGraphNodes(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	memo := map[string]bool{}
	out := make([]NodeHit, 0, limit)
	for _, n := range nodes {
		ok, err := s.allowed(ctx, subject, n.ConceptID, memo)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, hitOf(n, stale[n.ConceptID]))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Searcher) Stats(ctx context.Context, subject string) (meta.GraphStats, error) {
	stats, err := s.Index.GraphStats(ctx, subject)
	if err != nil {
		return meta.GraphStats{}, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	return stats, nil
}
