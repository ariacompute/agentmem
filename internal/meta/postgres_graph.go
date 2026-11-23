package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IsNotFound reports whether an error means "no such row" for either the
// PostgreSQL or the in-memory implementation. Callers map it to a uniform 404
// so a private node and a missing node are indistinguishable.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var nf *notFoundError
	return errors.As(err, &nf) || errors.Is(err, gorm.ErrRecordNotFound)
}

func (s *PostgresStore) UpsertGraphNode(ctx context.Context, node GraphNodeRecord, edges []GraphEdgeRecord) error {
	now := time.Now().UTC()
	if node.Properties == nil {
		node.Properties = StringMap{}
	}
	node.UpdatedAt = now
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "concept_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"node_type", "label", "description", "visibility", "owner", "status",
				"trust", "properties", "source_count", "has_resource", "content_hash",
				"commit_sha", "updated_at",
			}),
		}).Create(&node).Error; err != nil {
			return err
		}
		// Declared edges are replaced wholesale: the body is the authority, so a
		// removed line must remove the row.
		if err := tx.Where("from_id = ?", node.ConceptID).Delete(&GraphEdgeRecord{}).Error; err != nil {
			return err
		}
		if len(edges) == 0 {
			return nil
		}
		for i := range edges {
			if edges[i].Properties == nil {
				edges[i].Properties = StringMap{}
			}
			edges[i].UpdatedAt = now
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "from_id"}, {Name: "to_id"}, {Name: "edge_type"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"origin", "evidence", "properties", "content_hash", "updated_at",
			}),
		}).CreateInBatches(&edges, 100).Error
	})
}

func (s *PostgresStore) DeleteGraphNode(ctx context.Context, conceptID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("concept_id = ?", conceptID).Delete(&GraphNodeRecord{}).Error; err != nil {
			return err
		}
		return tx.Where("from_id = ? OR to_id = ?", conceptID, conceptID).Delete(&GraphEdgeRecord{}).Error
	})
}

func (s *PostgresStore) GetGraphNode(ctx context.Context, conceptID string) (GraphNodeRecord, error) {
	var n GraphNodeRecord
	err := s.db.WithContext(ctx).Where("concept_id = ?", conceptID).First(&n).Error
	return n, err
}

// graphWalkSQL expands a bounded neighbourhood in one statement. UNION (not
// UNION ALL) makes cycles terminate; MIN(depth) keeps one row per node.
const graphWalkSQL = `
WITH RECURSIVE walk(node_id, depth) AS (
    SELECT value, 0 FROM jsonb_array_elements_text(?::jsonb) AS seed(value)
  UNION
    SELECT CASE WHEN e.from_id = walk.node_id THEN e.to_id ELSE e.from_id END, walk.depth + 1
    FROM walk
    JOIN graph_edges e ON
      (? = 'both' AND (e.from_id = walk.node_id OR e.to_id = walk.node_id))
      OR (? = 'out' AND e.from_id = walk.node_id)
      OR (? = 'in' AND e.to_id = walk.node_id)
    WHERE walk.depth < ?
      AND (? = '' OR e.edge_type IN (SELECT jsonb_array_elements_text(?::jsonb)))
)
SELECT node_id, MIN(depth) AS depth FROM walk GROUP BY node_id ORDER BY depth, node_id LIMIT ?`

type graphWalkRow struct {
	NodeID string `gorm:"column:node_id"`
	Depth  int    `gorm:"column:depth"`
}

func (s *PostgresStore) GraphNeighbors(ctx context.Context, q GraphWalk) (GraphResult, error) {
	if err := ctx.Err(); err != nil {
		return GraphResult{}, err
	}
	q = NormalizeWalk(q)
	sources := make([]string, 0, len(q.Sources))
	for _, src := range q.Sources {
		src = strings.TrimSpace(src)
		if src != "" {
			sources = append(sources, src)
		}
	}
	if len(sources) == 0 {
		return GraphResult{Depths: map[string]int{}}, nil
	}
	// sources is a []string, so encoding cannot fail.
	seed, _ := json.Marshal(sources)
	typesJSON := []byte("[]")
	marker := ""
	if len(q.EdgeTypes) > 0 {
		marker = "1"
		typesJSON, _ = json.Marshal(q.EdgeTypes)
	}
	var rows []graphWalkRow
	err := s.db.WithContext(ctx).Raw(graphWalkSQL,
		string(seed), q.Direction, q.Direction, q.Direction, q.Depth,
		marker, string(typesJSON), q.MaxNodes+1,
	).Scan(&rows).Error
	if err != nil {
		return GraphResult{}, fmt.Errorf("meta: graph walk: %w", err)
	}
	truncated := false
	if len(rows) > q.MaxNodes {
		rows = rows[:q.MaxNodes]
		truncated = true
	}
	depths := make(map[string]int, len(rows))
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		depths[r.NodeID] = r.Depth
		ids = append(ids, r.NodeID)
	}
	out := GraphResult{Depths: depths, Truncated: truncated}
	if len(ids) == 0 {
		return out, nil
	}
	var nodes []GraphNodeRecord
	if err := s.db.WithContext(ctx).Where("concept_id IN ?", ids).Find(&nodes).Error; err != nil {
		return GraphResult{}, fmt.Errorf("meta: graph nodes: %w", err)
	}
	reached := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if !nodeTypeAllowed(q.NodeTypes, n.NodeType) {
			continue
		}
		out.Nodes = append(out.Nodes, n)
		reached[n.ConceptID] = true
	}
	sortNodes(out.Nodes)
	var edges []GraphEdgeRecord
	if err := s.db.WithContext(ctx).
		Where("from_id IN ? AND to_id IN ?", ids, ids).
		Find(&edges).Error; err != nil {
		return GraphResult{}, fmt.Errorf("meta: graph edges: %w", err)
	}
	for _, e := range edges {
		if reached[e.FromID] && reached[e.ToID] && edgeTypeAllowed(q.EdgeTypes, e.EdgeType) {
			out.Edges = append(out.Edges, e)
		}
	}
	sortEdges(out.Edges)
	return out, nil
}

func (s *PostgresStore) GraphEdges(ctx context.Context, ids []string, direction string) ([]GraphEdgeRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []GraphEdgeRecord{}, nil
	}
	var out []GraphEdgeRecord
	tx := s.db.WithContext(ctx)
	switch direction {
	case "out":
		tx = tx.Where("from_id IN ?", ids)
	case "in":
		tx = tx.Where("to_id IN ?", ids)
	default:
		tx = tx.Where("from_id IN ? OR to_id IN ?", ids, ids)
	}
	if err := tx.Order("from_id, to_id, edge_type").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("meta: graph edges: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) GraphShortestPath(ctx context.Context, q GraphPath) (GraphPathResult, error) {
	return shortestPathByLevel(ctx, s.GraphNeighbors, q)
}

func (s *PostgresStore) SearchGraphNodes(ctx context.Context, q GraphSearch) ([]GraphNodeRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q = NormalizeSearch(q)
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	tx := s.db.WithContext(ctx).Model(&GraphNodeRecord{})
	if len(q.NodeTypes) > 0 {
		tx = tx.Where("node_type IN ?", q.NodeTypes)
	}
	if needle != "" {
		like := "%" + escapeLike(needle) + "%"
		tx = tx.Where("LOWER(concept_id) LIKE ? OR LOWER(label) LIKE ? OR LOWER(description) LIKE ?", like, like, like)
	}
	var out []GraphNodeRecord
	err := tx.Order(clause.OrderBy{Columns: []clause.OrderByColumn{
		{Column: clause.Column{Name: "concept_id"}},
	}}).Limit(q.Limit).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("meta: graph search: %w", err)
	}
	// Ranking happens in Go so the in-memory and SQL implementations share one
	// definition of "exact / prefix / contains".
	sortBySearchRank(out, needle)
	return out, nil
}

func sortBySearchRank(nodes []GraphNodeRecord, needle string) {
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0; j-- {
			a, b := nodes[j-1], nodes[j]
			if searchRankLess(b, a, needle) {
				nodes[j-1], nodes[j] = b, a
				continue
			}
			break
		}
	}
}

func searchRankLess(a, b GraphNodeRecord, needle string) bool {
	ra, rb := searchRank(a, needle), searchRank(b, needle)
	if ra != rb {
		return ra < rb
	}
	if a.Label != b.Label {
		return a.Label < b.Label
	}
	return a.ConceptID < b.ConceptID
}

// escapeLike neutralises LIKE wildcards so a search term is matched literally.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (s *PostgresStore) StaleGraphNodes(ctx context.Context, ids []string) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var stale []string
	err := s.db.WithContext(ctx).Raw(`
SELECT g.concept_id FROM graph_nodes g
  LEFT JOIN concepts_meta m ON m.concept_id = g.concept_id
 WHERE g.concept_id IN ?
   AND (m.concept_id IS NULL OR m.content_hash <> g.content_hash)`, ids).Scan(&stale).Error
	if err != nil {
		return nil, fmt.Errorf("meta: graph staleness: %w", err)
	}
	for _, id := range ids {
		out[id] = false
	}
	for _, id := range stale {
		out[id] = true
	}
	return out, nil
}

const graphVisibleNode = `((g.visibility = 'public') OR (g.visibility = 'team' AND ? <> '') OR (g.visibility = 'private' AND ? <> '' AND g.owner = ?))`

func (s *PostgresStore) GraphStats(ctx context.Context, subject string) (GraphStats, error) {
	if err := ctx.Err(); err != nil {
		return GraphStats{}, err
	}
	out := GraphStats{NodeTypes: map[string]int{}, EdgeTypes: map[string]int{}}
	var nodeRows []struct {
		NodeType string `gorm:"column:node_type"`
		Nodes    int    `gorm:"column:nodes"`
		Stale    int    `gorm:"column:stale"`
	}
	nodeSQL := `SELECT g.node_type, count(*) AS nodes,
       count(*) FILTER (WHERE m.concept_id IS NULL OR m.content_hash <> g.content_hash) AS stale
  FROM graph_nodes g LEFT JOIN concepts_meta m ON m.concept_id = g.concept_id
 WHERE ` + graphVisibleNode + `
 GROUP BY g.node_type ORDER BY g.node_type`
	if err := s.db.WithContext(ctx).Raw(nodeSQL, subject, subject, subject).Scan(&nodeRows).Error; err != nil {
		return GraphStats{}, fmt.Errorf("meta: graph stats: %w", err)
	}
	for _, r := range nodeRows {
		out.Nodes += r.Nodes
		out.Stale += r.Stale
		out.NodeTypes[r.NodeType] += r.Nodes
	}
	var edgeRows []struct {
		EdgeType string `gorm:"column:edge_type"`
		Edges    int    `gorm:"column:edges"`
	}
	edgeSQL := `SELECT e.edge_type, count(*) AS edges
  FROM graph_edges e
  JOIN graph_nodes a ON a.concept_id = e.from_id
  JOIN graph_nodes b ON b.concept_id = e.to_id
 WHERE ((a.visibility = 'public') OR (a.visibility = 'team' AND ? <> '') OR (a.visibility = 'private' AND ? <> '' AND a.owner = ?))
   AND ((b.visibility = 'public') OR (b.visibility = 'team' AND ? <> '') OR (b.visibility = 'private' AND ? <> '' AND b.owner = ?))
 GROUP BY e.edge_type ORDER BY e.edge_type`
	if err := s.db.WithContext(ctx).Raw(edgeSQL,
		subject, subject, subject, subject, subject, subject,
	).Scan(&edgeRows).Error; err != nil {
		return GraphStats{}, fmt.Errorf("meta: graph stats: %w", err)
	}
	for _, r := range edgeRows {
		out.Edges += r.Edges
		out.EdgeTypes[r.EdgeType] += r.Edges
	}
	return out, nil
}

func (s *PostgresStore) PruneGraphNodes(ctx context.Context, keep []string) (int, error) {
	if len(keep) == 0 {
		var total int64
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&GraphNodeRecord{}).Count(&total).Error; err != nil {
				return err
			}
			if err := tx.Where("1 = 1").Delete(&GraphNodeRecord{}).Error; err != nil {
				return err
			}
			return tx.Where("1 = 1").Delete(&GraphEdgeRecord{}).Error
		})
		if err != nil {
			return 0, fmt.Errorf("meta: prune graph: %w", err)
		}
		return int(total), nil
	}
	// keep is a []string, so encoding cannot fail.
	encoded, _ := json.Marshal(keep)
	removed := int64(0)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("NOT (concept_id IN (SELECT jsonb_array_elements_text(?::jsonb)))", string(encoded)).
			Delete(&GraphNodeRecord{})
		if res.Error != nil {
			return res.Error
		}
		removed = res.RowsAffected
		return tx.Where(`NOT EXISTS (SELECT 1 FROM graph_nodes n WHERE n.concept_id = graph_edges.from_id)
			OR NOT EXISTS (SELECT 1 FROM graph_nodes n WHERE n.concept_id = graph_edges.to_id)`).
			Delete(&GraphEdgeRecord{}).Error
	})
	if err != nil {
		return 0, fmt.Errorf("meta: prune graph: %w", err)
	}
	return int(removed), nil
}
