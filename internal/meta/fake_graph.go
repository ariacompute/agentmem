package meta

import (
	"context"
	"sort"
	"strings"
	"time"
)

// The in-memory GraphIndex mirrors the PostgreSQL contract: single-hop edges
// are stored once, BFS derives reverse reachability, and every ordering is
// deterministic so offline tests and CLI output are stable.

func (f *FakeStore) UpsertGraphNode(ctx context.Context, node GraphNodeRecord, edges []GraphEdgeRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	if node.Properties == nil {
		node.Properties = StringMap{}
	}
	node.UpdatedAt = now
	f.graphNodes[node.ConceptID] = node
	for k, e := range f.graphEdges {
		if e.FromID == node.ConceptID {
			delete(f.graphEdges, k)
		}
	}
	for _, e := range edges {
		if e.Properties == nil {
			e.Properties = StringMap{}
		}
		e.UpdatedAt = now
		f.graphEdges[edgeKey(e)] = e
	}
	return ctx.Err()
}

func (f *FakeStore) DeleteGraphNode(ctx context.Context, conceptID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.graphNodes, conceptID)
	for k, e := range f.graphEdges {
		if e.FromID == conceptID || e.ToID == conceptID {
			delete(f.graphEdges, k)
		}
	}
	return ctx.Err()
}

func (f *FakeStore) GetGraphNode(ctx context.Context, conceptID string) (GraphNodeRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.graphNodes[conceptID]
	if !ok {
		return GraphNodeRecord{}, errNotFound
	}
	return n, ctx.Err()
}

func (f *FakeStore) GraphNeighbors(ctx context.Context, q GraphWalk) (GraphResult, error) {
	if err := ctx.Err(); err != nil {
		return GraphResult{}, err
	}
	q = NormalizeWalk(q)
	f.mu.Lock()
	nodes := make(map[string]GraphNodeRecord, len(f.graphNodes))
	for k, v := range f.graphNodes {
		nodes[k] = v
	}
	edges := make([]GraphEdgeRecord, 0, len(f.graphEdges))
	for _, e := range f.graphEdges {
		edges = append(edges, e)
	}
	f.mu.Unlock()
	sortEdges(edges)

	depths := map[string]int{}
	frontier := make([]string, 0, len(q.Sources))
	for _, s := range q.Sources {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := depths[s]; ok {
			continue
		}
		depths[s] = 0
		frontier = append(frontier, s)
	}
	sort.Strings(frontier)
	for depth := 1; depth <= q.Depth && len(frontier) > 0; depth++ {
		next := map[string]bool{}
		for _, id := range frontier {
			for _, e := range edges {
				if !edgeTypeAllowed(q.EdgeTypes, e.EdgeType) {
					continue
				}
				neighbor, ok := neighborOf(e, id, q.Direction)
				if !ok {
					continue
				}
				if _, seen := depths[neighbor]; seen {
					continue
				}
				next[neighbor] = true
			}
		}
		frontier = frontier[:0]
		for id := range next {
			depths[id] = depth
			frontier = append(frontier, id)
		}
		sort.Strings(frontier)
	}

	ids := make([]string, 0, len(depths))
	for id := range depths {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if depths[ids[i]] != depths[ids[j]] {
			return depths[ids[i]] < depths[ids[j]]
		}
		return ids[i] < ids[j]
	})
	truncated := false
	if len(ids) > q.MaxNodes {
		ids = ids[:q.MaxNodes]
		truncated = true
	}
	out := GraphResult{Depths: map[string]int{}, Truncated: truncated}
	reached := map[string]bool{}
	for _, id := range ids {
		n, ok := nodes[id]
		if !ok {
			// A dangling edge target has no body; it is surfaced through the
			// answerability warnings instead of being invented here.
			continue
		}
		if !nodeTypeAllowed(q.NodeTypes, n.NodeType) {
			continue
		}
		out.Nodes = append(out.Nodes, n)
		out.Depths[id] = depths[id]
		reached[id] = true
	}
	sortNodes(out.Nodes)
	for _, e := range edges {
		if reached[e.FromID] && reached[e.ToID] && edgeTypeAllowed(q.EdgeTypes, e.EdgeType) {
			out.Edges = append(out.Edges, e)
		}
	}
	sortEdges(out.Edges)
	return out, ctx.Err()
}

// GraphEdges returns the stored edges that touch any of the ids, including
// edges whose other endpoint has no body. Node detail and answerability need
// that asymmetry: a dangling edge is a gap to report, not a node to invent.
func (f *FakeStore) GraphEdges(ctx context.Context, ids []string, direction string) ([]GraphEdgeRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			want[id] = true
		}
	}
	f.mu.Lock()
	out := make([]GraphEdgeRecord, 0, len(f.graphEdges))
	for _, e := range f.graphEdges {
		switch direction {
		case "out":
			if !want[e.FromID] {
				continue
			}
		case "in":
			if !want[e.ToID] {
				continue
			}
		default:
			if !want[e.FromID] && !want[e.ToID] {
				continue
			}
		}
		out = append(out, e)
	}
	f.mu.Unlock()
	sortEdges(out)
	return out, nil
}

func (f *FakeStore) GraphShortestPath(ctx context.Context, q GraphPath) (GraphPathResult, error) {
	return shortestPathByLevel(ctx, f.GraphNeighbors, q)
}

func (f *FakeStore) SearchGraphNodes(ctx context.Context, q GraphSearch) ([]GraphNodeRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q = NormalizeSearch(q)
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	f.mu.Lock()
	out := make([]GraphNodeRecord, 0, len(f.graphNodes))
	for _, n := range f.graphNodes {
		if !nodeTypeAllowed(q.NodeTypes, n.NodeType) {
			continue
		}
		if needle != "" && !nodeMatches(n, needle) {
			continue
		}
		out = append(out, n)
	}
	f.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		ri, rj := searchRank(out[i], needle), searchRank(out[j], needle)
		if ri != rj {
			return ri < rj
		}
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].ConceptID < out[j].ConceptID
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (f *FakeStore) StaleGraphNodes(ctx context.Context, ids []string) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		n, ok := f.graphNodes[id]
		if !ok {
			continue
		}
		m, ok := f.concepts[id]
		out[id] = !ok || m.ContentHash != n.ContentHash
	}
	return out, nil
}

func (f *FakeStore) GraphStats(ctx context.Context, subject string) (GraphStats, error) {
	if err := ctx.Err(); err != nil {
		return GraphStats{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := GraphStats{NodeTypes: map[string]int{}, EdgeTypes: map[string]int{}}
	visible := map[string]bool{}
	for id, n := range f.graphNodes {
		if !visibilityAllows(subject, n.Visibility, n.Owner) {
			continue
		}
		visible[id] = true
		out.Nodes++
		out.NodeTypes[n.NodeType]++
		m, ok := f.concepts[id]
		if !ok || m.ContentHash != n.ContentHash {
			out.Stale++
		}
	}
	for _, e := range f.graphEdges {
		if !visible[e.FromID] || !visible[e.ToID] {
			continue
		}
		out.Edges++
		out.EdgeTypes[e.EdgeType]++
	}
	return out, nil
}

func (f *FakeStore) PruneGraphNodes(ctx context.Context, keep []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keepSet := make(map[string]bool, len(keep))
	for _, id := range keep {
		keepSet[id] = true
	}
	removed := 0
	for id := range f.graphNodes {
		if keepSet[id] {
			continue
		}
		delete(f.graphNodes, id)
		removed++
	}
	for k, e := range f.graphEdges {
		if !keepSet[e.FromID] || !keepSet[e.ToID] {
			delete(f.graphEdges, k)
		}
	}
	return removed, ctx.Err()
}

func neighborOf(e GraphEdgeRecord, id, direction string) (string, bool) {
	switch direction {
	case "out":
		if e.FromID == id {
			return e.ToID, true
		}
	case "in":
		if e.ToID == id {
			return e.FromID, true
		}
	default:
		if e.FromID == id {
			return e.ToID, true
		}
		if e.ToID == id {
			return e.FromID, true
		}
	}
	return "", false
}

func edgeTypeAllowed(types []string, t string) bool {
	if len(types) == 0 {
		return true
	}
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

func nodeTypeAllowed(types []string, t string) bool {
	if len(types) == 0 {
		return true
	}
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

func nodeMatches(n GraphNodeRecord, needle string) bool {
	for _, field := range []string{n.ConceptID, n.Label, n.Description, n.NodeType} {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

// searchRank prefers an exact id, then an id or label prefix, then anything
// else that contains the term.
func searchRank(n GraphNodeRecord, needle string) int {
	id := strings.ToLower(n.ConceptID)
	label := strings.ToLower(n.Label)
	switch {
	case needle == "" || id == needle || label == needle:
		return 0
	case strings.HasPrefix(id, needle) || strings.HasPrefix(label, needle):
		return 1
	default:
		return 2
	}
}
