package service

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"agentmem/internal/compute"
	"agentmem/internal/config"
	"agentmem/internal/graph"
	"agentmem/internal/okf"
)

// TestMCPBackendForwardsEveryUseCase pins the adapter to the service: every
// method must reach the same use case (and therefore the same ACL and Git path)
// as REST and the CLI.
func TestMCPBackendForwardsEveryUseCase(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Root = filepath.Join(t.TempDir(), "bundle")
	svc, err := Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	backend := NewMCPBackend(svc)
	ctx := context.Background()
	subject := "alice"

	doc := &okf.Document{Type: "Concept", Title: "T", Body: "# T\n"}
	if _, err := backend.Put(ctx, subject, "concepts/backend", doc); err != nil {
		t.Fatalf("put: %v", err)
	}
	read, err := backend.Knowledge(ctx, subject, "concepts/backend")
	if err != nil || read.Title != "T" {
		t.Fatalf("knowledge = %+v, %v", read, err)
	}
	if _, err := backend.Knowledge(ctx, subject, "concepts/missing"); err == nil {
		t.Fatal("a missing document must stay indistinguishable from a forbidden one")
	}

	concepts, err := backend.Concepts(ctx)
	if err != nil || len(concepts) != 1 {
		t.Fatalf("concepts = %v, %v", concepts, err)
	}
	if _, err := backend.Query(ctx, subject, "title", 5); err != nil {
		t.Fatalf("query: %v", err)
	}
	if _, err := backend.Decisions(ctx, "concepts/backend", 5); err != nil {
		t.Fatalf("decisions: %v", err)
	}
	if _, err := backend.Computations(ctx, "", 5); err != nil {
		t.Fatalf("computations: %v", err)
	}

	stats, err := backend.GraphStats(ctx, subject)
	if err != nil {
		t.Fatalf("graph stats: %v", err)
	}
	if want, _ := svc.GraphStats(ctx, subject); !reflect.DeepEqual(stats, want) {
		t.Fatalf("graph stats diverged: %+v vs %+v", stats, want)
	}
	if _, err := backend.GraphNode(ctx, subject, "concepts/backend"); err != nil {
		t.Fatalf("graph node: %v", err)
	}
	if _, err := backend.GraphNeighbors(ctx, subject, graph.WalkQuery{From: "concepts/backend", Depth: 1, Direction: "both"}); err != nil {
		t.Fatalf("graph neighbors: %v", err)
	}
	if _, err := backend.GraphPath(ctx, subject, graph.PathQuery{From: "concepts/backend", To: "concepts/backend"}); err != nil {
		t.Fatalf("graph path: %v", err)
	}
	if _, err := backend.GraphSearch(ctx, subject, graph.SearchQuery{Text: "T"}); err != nil {
		t.Fatalf("graph search: %v", err)
	}
	if _, _, err := backend.GraphSchema(ctx); err != nil {
		t.Fatalf("graph schema: %v", err)
	}
	if _, err := backend.GraphAnswerability(ctx, subject, "T"); err != nil {
		t.Fatalf("graph answerability: %v", err)
	}
	if _, err := backend.GraphRetrieve(ctx, subject, "T", 5); err != nil {
		t.Fatalf("graph retrieve: %v", err)
	}

	if _, _, err := backend.GraphPutNode(ctx, subject, graph.NodeInput{ConceptID: "concepts/node"}); err != nil {
		t.Fatalf("graph put node: %v", err)
	}
	if _, _, err := backend.GraphAddEdges(ctx, subject, "concepts/node",
		[]okf.GraphEdge{{To: "concepts/backend", Type: "related"}}); err != nil {
		t.Fatalf("graph add edges: %v", err)
	}
	if _, err := backend.GraphRebuild(ctx); err != nil {
		t.Fatalf("graph rebuild: %v", err)
	}

	if !backend.GraphEnabled() {
		t.Fatal("the builtin graph must be enabled")
	}
	if got, want := backend.MaxGraphDepth(), svc.MaxGraphDepth(); got != want {
		t.Fatalf("max depth = %d, want %d", got, want)
	}

	// An unsupported computation still crosses the adapter and returns the
	// domain error, never a panic.
	if _, err := backend.RunComputation(ctx, compute.Request{ConceptID: "computations/missing"}); err == nil {
		t.Fatal("a missing computation must be rejected")
	}
}
