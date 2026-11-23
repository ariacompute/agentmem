package meta

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// GraphIndex is the rebuildable adjacency projection of the OKF graph
// declarations. PostgreSQL stores it only so queries do not have to walk the
// bundle; the authoritative bodies stay in Git. The port has no *gorm.DB in its
// signatures, exactly like MetaStore and SQLExecutor.
type GraphIndex interface {
	UpsertGraphNode(ctx context.Context, node GraphNodeRecord, edges []GraphEdgeRecord) error
	DeleteGraphNode(ctx context.Context, conceptID string) error
	GetGraphNode(ctx context.Context, conceptID string) (GraphNodeRecord, error)
	GraphNeighbors(ctx context.Context, q GraphWalk) (GraphResult, error)
	GraphEdges(ctx context.Context, ids []string, direction string) ([]GraphEdgeRecord, error)
	GraphShortestPath(ctx context.Context, q GraphPath) (GraphPathResult, error)
	SearchGraphNodes(ctx context.Context, q GraphSearch) ([]GraphNodeRecord, error)
	StaleGraphNodes(ctx context.Context, ids []string) (map[string]bool, error)
	GraphStats(ctx context.Context, subject string) (GraphStats, error)
	PruneGraphNodes(ctx context.Context, keep []string) (int, error)
}

// GraphNodeRecord is one indexed concept node. Description, trust and source
// counts are duplicated from the body purely so list views stay a single query;
// a mismatch with concepts_meta.content_hash marks the row stale and triggers a
// lazy rebuild from Git.
type GraphNodeRecord struct {
	ConceptID   string    `gorm:"primaryKey;size:512" json:"concept_id"`
	NodeType    string    `gorm:"size:128;not null;index" json:"node_type"`
	Label       string    `gorm:"size:512" json:"label"`
	Description string    `gorm:"type:text" json:"description"`
	Visibility  string    `gorm:"size:32;not null" json:"visibility"`
	Owner       string    `gorm:"size:128" json:"owner"`
	Status      string    `gorm:"size:32" json:"status"`
	Trust       string    `gorm:"size:32" json:"trust"`
	Properties  StringMap `gorm:"type:jsonb" json:"properties"`
	SourceCount int       `json:"source_count"`
	HasResource bool      `json:"has_resource"`
	ContentHash string    `gorm:"size:64;not null;index" json:"content_hash"`
	CommitSHA   string    `gorm:"size:64" json:"commit_sha"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (GraphNodeRecord) TableName() string { return "graph_nodes" }

// GraphEdgeRecord is one directed relation. Only declared outgoing edges are
// written; reverse traversal is derived at query time.
type GraphEdgeRecord struct {
	FromID      string    `gorm:"primaryKey;size:512" json:"from_id"`
	ToID        string    `gorm:"primaryKey;size:512" json:"to_id"`
	EdgeType    string    `gorm:"primaryKey;size:128" json:"edge_type"`
	Origin      string    `gorm:"size:32;not null" json:"origin"`
	Evidence    string    `gorm:"size:1024" json:"evidence"`
	Properties  StringMap `gorm:"type:jsonb" json:"properties"`
	ContentHash string    `gorm:"size:64" json:"content_hash"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (GraphEdgeRecord) TableName() string { return "graph_edges" }

// Edge origins. Declared edges come from frontmatter, derived ones from the
// body (markdown links, sources, resource) and are always marked as such so a
// reader never mistakes an inference for an authored claim.
const (
	EdgeOriginDeclared = "declared"
	EdgeOriginLink     = "link"
	EdgeOriginSource   = "source"
	EdgeOriginResource = "resource"
)

// GraphWalk is a bounded neighbourhood expansion.
type GraphWalk struct {
	Sources   []string
	Depth     int
	Direction string // "out" | "in" | "both"
	EdgeTypes []string
	NodeTypes []string
	MaxNodes  int
}

// GraphResult is one expanded neighbourhood plus the edges between the reached
// nodes.
type GraphResult struct {
	Nodes     []GraphNodeRecord
	Edges     []GraphEdgeRecord
	Depths    map[string]int
	Truncated bool
}

// GraphPath is a shortest-path query.
type GraphPath struct {
	From      string
	To        string
	MaxDepth  int
	Direction string
	EdgeTypes []string
	MaxNodes  int
}

// GraphPathResult reports whether a path was found and, when it was, the node
// sequence plus the directed edges that connect it.
type GraphPathResult struct {
	Found bool
	Nodes []GraphNodeRecord
	Edges []GraphEdgeRecord
}

// GraphSearch is a deterministic keyword lookup over indexed nodes.
type GraphSearch struct {
	Text      string
	NodeTypes []string
	EdgeTypes []string
	Limit     int
	MaxNodes  int
}

// GraphStats is the aggregate size and freshness of the index. Counts are
// visibility scoped, so a caller never learns the size of data it cannot read.
type GraphStats struct {
	Nodes     int            `json:"nodes"`
	Edges     int            `json:"edges"`
	NodeTypes map[string]int `json:"node_types"`
	EdgeTypes map[string]int `json:"edge_types"`
	Stale     int            `json:"stale"`
}

// StringMap is a JSONB-backed map (Valuer/Scanner pair) for graph properties.
type StringMap map[string]string

func (m StringMap) Value() (driver.Value, error) {
	if m == nil {
		return "{}", nil
	}
	return json.Marshal(m)
}

func (m *StringMap) Scan(src any) error {
	if src == nil {
		*m = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("meta: cannot scan %T into StringMap", src)
	}
	if len(b) == 0 {
		*m = nil
		return nil
	}
	return json.Unmarshal(b, m)
}

// NormalizeWalk clamps a walk to the port contract: depth in [0, 8], a known
// direction and a positive node budget.
func NormalizeWalk(q GraphWalk) GraphWalk {
	if q.Depth < 0 {
		q.Depth = 0
	}
	if q.Depth > 8 {
		q.Depth = 8
	}
	switch q.Direction {
	case "out", "in":
	default:
		q.Direction = "both"
	}
	if q.MaxNodes <= 0 {
		q.MaxNodes = 200
	}
	return q
}

// NormalizeSearch clamps a search request.
func NormalizeSearch(q GraphSearch) GraphSearch {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	if q.MaxNodes <= 0 || q.MaxNodes < q.Limit {
		q.MaxNodes = q.Limit
	}
	return q
}

// NormalizePath clamps a path request.
func NormalizePath(q GraphPath) GraphPath {
	if q.MaxDepth <= 0 {
		q.MaxDepth = 3
	}
	if q.MaxDepth > 8 {
		q.MaxDepth = 8
	}
	switch q.Direction {
	case "out", "in":
	default:
		q.Direction = "both"
	}
	if q.MaxNodes <= 0 {
		q.MaxNodes = 200
	}
	return q
}

// shortestPathByLevel runs a breadth-first search one level at a time. Both the
// in-memory and the PostgreSQL implementation delegate to it, so the two can
// never disagree about direction handling, cycle avoidance or tie-breaking.
func shortestPathByLevel(ctx context.Context, walk func(context.Context, GraphWalk) (GraphResult, error), q GraphPath) (GraphPathResult, error) {
	q = NormalizePath(q)
	from := strings.TrimSpace(q.From)
	to := strings.TrimSpace(q.To)
	if from == "" || to == "" {
		return GraphPathResult{}, fmt.Errorf("meta: graph path needs a source and a target")
	}
	seed, err := walk(ctx, GraphWalk{Sources: []string{from}, Depth: 0, Direction: q.Direction, MaxNodes: 1})
	if err != nil {
		return GraphPathResult{}, err
	}
	start := nodeByID(seed.Nodes, from)
	if from == to {
		return GraphPathResult{Found: true, Nodes: []GraphNodeRecord{start}}, nil
	}
	visited := map[string]bool{from: true}
	parent := map[string]GraphEdgeRecord{}
	nodes := map[string]GraphNodeRecord{from: start}
	frontier := []string{from}
	for depth := 1; depth <= q.MaxDepth && len(frontier) > 0; depth++ {
		res, err := walk(ctx, GraphWalk{
			Sources: frontier, Depth: 1, Direction: q.Direction,
			EdgeTypes: q.EdgeTypes, MaxNodes: q.MaxNodes,
		})
		if err != nil {
			return GraphPathResult{}, err
		}
		for _, n := range res.Nodes {
			nodes[n.ConceptID] = n
		}
		next := make([]string, 0, len(res.Nodes))
		for _, e := range res.Edges {
			var current, other string
			switch {
			case visited[e.FromID] && !visited[e.ToID]:
				current, other = e.FromID, e.ToID
			case visited[e.ToID] && !visited[e.FromID]:
				current, other = e.ToID, e.FromID
			default:
				continue
			}
			if current != "" && q.Direction == "out" && e.FromID != current {
				continue
			}
			if current != "" && q.Direction == "in" && e.ToID != current {
				continue
			}
			// A seed-only walk returns the source; skip nodes that never
			// appeared in the node budget.
			if _, ok := nodes[other]; !ok {
				continue
			}
			parent[other] = e
			visited[other] = true
			next = append(next, other)
		}
		if _, ok := parent[to]; ok {
			return buildPath(from, to, parent, nodes), nil
		}
		frontier = next
	}
	return GraphPathResult{Found: false}, nil
}

func buildPath(from, to string, parent map[string]GraphEdgeRecord, nodes map[string]GraphNodeRecord) GraphPathResult {
	ids := []string{to}
	edges := []GraphEdgeRecord{}
	cur := to
	for cur != from {
		e, ok := parent[cur]
		if !ok {
			return GraphPathResult{Found: false}
		}
		edges = append([]GraphEdgeRecord{e}, edges...)
		if e.FromID == cur {
			cur = e.ToID
		} else {
			cur = e.FromID
		}
		ids = append([]string{cur}, ids...)
	}
	out := GraphPathResult{Found: true}
	for _, id := range ids {
		out.Nodes = append(out.Nodes, nodes[id])
	}
	out.Edges = edges
	return out
}

func nodeByID(nodes []GraphNodeRecord, id string) GraphNodeRecord {
	for _, n := range nodes {
		if n.ConceptID == id {
			return n
		}
	}
	return GraphNodeRecord{ConceptID: id}
}

// sortNodes orders nodes by depth then id, which is the order every traversal
// and API response exposes.
func sortNodes(nodes []GraphNodeRecord) {
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0 && nodes[j].ConceptID < nodes[j-1].ConceptID; j-- {
			nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
		}
	}
}

func sortEdges(edges []GraphEdgeRecord) {
	for i := 1; i < len(edges); i++ {
		for j := i; j > 0 && edgeKey(edges[j]) < edgeKey(edges[j-1]); j-- {
			edges[j], edges[j-1] = edges[j-1], edges[j]
		}
	}
}

func edgeKey(e GraphEdgeRecord) string {
	return e.FromID + "\x00" + e.ToID + "\x00" + e.EdgeType
}

func visibilityAllows(subject, visibility, owner string) bool {
	switch visibility {
	case "public":
		return true
	case "team":
		return subject != ""
	case "private":
		return subject != "" && subject == owner
	default:
		return false
	}
}
