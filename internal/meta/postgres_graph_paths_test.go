package meta

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

var errBackend = errors.New("backend unavailable")

func TestPostgresGraphWriteFailures(t *testing.T) {
	node := GraphNodeRecord{ConceptID: "a", NodeType: "metric"}
	edge := GraphEdgeRecord{FromID: "a", ToID: "b", EdgeType: "related"}

	t.Run("node upsert", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectExec(`INSERT INTO "graph_nodes"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if err := s.UpsertGraphNode(context.Background(), node, nil); err == nil {
			t.Fatal("a failed upsert must surface")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("stale edge delete", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectExec(`INSERT INTO "graph_nodes"`).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if err := s.UpsertGraphNode(context.Background(), node, nil); err == nil {
			t.Fatal("a failed edge delete must surface")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("edge insert", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectExec(`INSERT INTO "graph_nodes"`).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`INSERT INTO "graph_edges"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if err := s.UpsertGraphNode(context.Background(), node, []GraphEdgeRecord{edge}); err == nil {
			t.Fatal("a failed edge insert must surface")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("node delete", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if err := s.DeleteGraphNode(context.Background(), "a"); err == nil {
			t.Fatal("a failed delete must surface")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("edge delete", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if err := s.DeleteGraphNode(context.Background(), "a"); err == nil {
			t.Fatal("a failed edge delete must surface")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostgresGraphNeighborsPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("walk failure", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectQuery(`WITH RECURSIVE walk`).WillReturnError(errBackend)
		if _, err := s.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}}); err == nil {
			t.Fatal("a failed walk must surface")
		}
	})

	t.Run("node hydration failure", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectQuery(`WITH RECURSIVE walk`).WillReturnRows(
			sqlmock.NewRows([]string{"node_id", "depth"}).AddRow("a", 0))
		mock.ExpectQuery(`SELECT \* FROM "graph_nodes"`).WillReturnError(errBackend)
		if _, err := s.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}}); err == nil {
			t.Fatal("a failed node hydration must surface")
		}
	})

	t.Run("edge hydration failure", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectQuery(`WITH RECURSIVE walk`).WillReturnRows(
			sqlmock.NewRows([]string{"node_id", "depth"}).AddRow("a", 0))
		mock.ExpectQuery(`SELECT \* FROM "graph_nodes"`).WillReturnRows(
			sqlmock.NewRows([]string{"concept_id", "node_type"}).AddRow("a", "metric"))
		mock.ExpectQuery(`SELECT \* FROM "graph_edges"`).WillReturnError(errBackend)
		if _, err := s.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}}); err == nil {
			t.Fatal("a failed edge hydration must surface")
		}
	})

	t.Run("truncated and filtered", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectQuery(`WITH RECURSIVE walk`).WillReturnRows(
			sqlmock.NewRows([]string{"node_id", "depth"}).AddRow("a", 0).AddRow("b", 1))
		mock.ExpectQuery(`SELECT \* FROM "graph_nodes"`).WillReturnRows(
			sqlmock.NewRows([]string{"concept_id", "node_type"}).AddRow("a", "table"))
		mock.ExpectQuery(`SELECT \* FROM "graph_edges"`).WillReturnRows(
			sqlmock.NewRows([]string{"from_id", "to_id", "edge_type"}))
		result, err := s.GraphNeighbors(ctx, GraphWalk{
			Sources: []string{"a"}, MaxNodes: 1, NodeTypes: []string{"metric"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Truncated || len(result.Nodes) != 0 {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("empty source set", func(t *testing.T) {
		s, _ := newMockStore(t)
		result, err := s.GraphNeighbors(ctx, GraphWalk{Sources: []string{"  "}})
		if err != nil || len(result.Depths) != 0 {
			t.Fatalf("result = %+v, %v", result, err)
		}
	})

	t.Run("no rows", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectQuery(`WITH RECURSIVE walk`).WillReturnRows(
			sqlmock.NewRows([]string{"node_id", "depth"}))
		result, err := s.GraphNeighbors(ctx, GraphWalk{Sources: []string{"a"}})
		if err != nil || len(result.Nodes) != 0 {
			t.Fatalf("result = %+v, %v", result, err)
		}
	})
}

func TestPostgresGraphEdgesAndPath(t *testing.T) {
	ctx := context.Background()

	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "graph_edges"`).WillReturnError(errBackend)
	if _, err := s.GraphEdges(ctx, []string{"a"}, "out"); err == nil {
		t.Fatal("a failed edge read must surface")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.GraphEdges(cancelled, []string{"a"}, "both"); err == nil {
		t.Fatal("a cancelled context must surface")
	}

	// GraphShortestPath delegates to the level walk; a failing seed walk is
	// enough to prove the delegation.
	s, mock = newMockStore(t)
	mock.ExpectQuery(`WITH RECURSIVE walk`).WillReturnError(errBackend)
	if _, err := s.GraphShortestPath(ctx, GraphPath{From: "a", To: "b"}); err == nil {
		t.Fatal("a failed path must surface")
	}
}

func TestPostgresGraphSearchRanking(t *testing.T) {
	ctx := context.Background()

	// Already ordered: the insertion sort breaks on the first comparison.
	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "graph_nodes"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "node_type", "label"}).
			AddRow("a", "metric", "A").
			AddRow("b", "metric", "B"))
	nodes, err := s.SearchGraphNodes(ctx, GraphSearch{Text: "  ", NodeTypes: []string{"metric"}, Limit: 10})
	if err != nil || len(nodes) != 2 || nodes[0].ConceptID != "a" {
		t.Fatalf("ordering = %+v, %v", nodes, err)
	}

	// Equal rank and label: the concept id is the final tie-break.
	s, mock = newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "graph_nodes"`).
		WillReturnRows(sqlmock.NewRows([]string{"concept_id", "node_type", "label"}).
			AddRow("b", "metric", "Same").
			AddRow("a", "metric", "Same"))
	nodes, err = s.SearchGraphNodes(ctx, GraphSearch{Text: "  ", Limit: 10})
	if err != nil || len(nodes) != 2 || nodes[0].ConceptID != "a" {
		t.Fatalf("tie-break = %+v, %v", nodes, err)
	}

	s, mock = newMockStore(t)
	mock.ExpectQuery(`SELECT \* FROM "graph_nodes"`).WillReturnError(errBackend)
	if _, err := s.SearchGraphNodes(ctx, GraphSearch{Text: "x"}); err == nil {
		t.Fatal("a failed search must surface")
	}
}

func TestPostgresGraphStatsPaths(t *testing.T) {
	ctx := context.Background()

	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT g.node_type, count`).WillReturnError(errBackend)
	if _, err := s.GraphStats(ctx, "alice"); err == nil {
		t.Fatal("a failed node count must surface")
	}

	s, mock = newMockStore(t)
	mock.ExpectQuery(`SELECT g.node_type, count`).WillReturnRows(
		sqlmock.NewRows([]string{"node_type", "nodes", "stale"}).AddRow("metric", 2, 1))
	mock.ExpectQuery(`SELECT e.edge_type, count`).WillReturnError(errBackend)
	if _, err := s.GraphStats(ctx, "alice"); err == nil {
		t.Fatal("a failed edge count must surface")
	}

	s, mock = newMockStore(t)
	mock.ExpectQuery(`SELECT g.node_type, count`).WillReturnRows(
		sqlmock.NewRows([]string{"node_type", "nodes", "stale"}).AddRow("metric", 2, 1))
	mock.ExpectQuery(`SELECT e.edge_type, count`).WillReturnRows(
		sqlmock.NewRows([]string{"edge_type", "edges"}).AddRow("related", 3))
	stats, err := s.GraphStats(ctx, "alice")
	if err != nil || stats.Nodes != 2 || stats.Edges != 3 || stats.Stale != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
}

func TestPostgresGraphStaleness(t *testing.T) {
	ctx := context.Background()

	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT g.concept_id FROM graph_nodes g`).WillReturnError(errBackend)
	if _, err := s.StaleGraphNodes(ctx, []string{"a"}); err == nil {
		t.Fatal("a failed staleness read must surface")
	}

	s, mock = newMockStore(t)
	mock.ExpectQuery(`SELECT g.concept_id FROM graph_nodes g`).WillReturnRows(
		sqlmock.NewRows([]string{"concept_id"}).AddRow("a"))
	stale, err := s.StaleGraphNodes(ctx, []string{"a", "b"})
	if err != nil || !stale["a"] || stale["b"] {
		t.Fatalf("stale = %+v, %v", stale, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.StaleGraphNodes(cancelled, []string{"a"}); err == nil {
		t.Fatal("a cancelled context must surface")
	}
}

func TestPostgresPruneGraphPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("count failure", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT count\(\*\) FROM "graph_nodes"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if _, err := s.PruneGraphNodes(ctx, nil); err == nil {
			t.Fatal("a failed count must surface")
		}
	})

	t.Run("delete nodes failure", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT count\(\*\) FROM "graph_nodes"`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if _, err := s.PruneGraphNodes(ctx, nil); err == nil {
			t.Fatal("a failed delete must surface")
		}
	})

	t.Run("delete edges failure", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT count\(\*\) FROM "graph_nodes"`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if _, err := s.PruneGraphNodes(ctx, nil); err == nil {
			t.Fatal("a failed edge delete must surface")
		}
	})

	t.Run("full prune", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT count\(\*\) FROM "graph_nodes"`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
		mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnResult(sqlmock.NewResult(0, 4))
		mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		removed, err := s.PruneGraphNodes(ctx, nil)
		if err != nil || removed != 4 {
			t.Fatalf("removed = %d, %v", removed, err)
		}
	})

	t.Run("keep set failure", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnError(errBackend)
		mock.ExpectRollback()
		if _, err := s.PruneGraphNodes(ctx, []string{"a"}); err == nil {
			t.Fatal("a failed keep-set delete must surface")
		}
	})

	t.Run("keep set prunes edges", func(t *testing.T) {
		s, mock := newMockStore(t)
		mock.ExpectBegin()
		mock.ExpectExec(`DELETE FROM "graph_nodes"`).WillReturnResult(sqlmock.NewResult(0, 3))
		mock.ExpectExec(`DELETE FROM "graph_edges"`).WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectCommit()
		removed, err := s.PruneGraphNodes(ctx, []string{"a"})
		if err != nil || removed != 3 {
			t.Fatalf("removed = %d, %v", removed, err)
		}
	})
}

func TestScanRowsReportsScanFailure(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery(`SELECT 1`).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(1))
	rows, err := s.db.Raw("SELECT 1").Rows()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	previous := scanRow
	scanRow = func(*sql.Rows, []any) error { return errBackend }
	t.Cleanup(func() { scanRow = previous })

	if _, err := scanRows(rows, 10); err == nil {
		t.Fatal("a scan failure must surface")
	}
}
