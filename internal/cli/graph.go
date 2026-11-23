package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"agentmem/internal/graph"
	"agentmem/internal/okf"
)

const graphUsage = `graph actions:
  stats        Show node/edge counts and index freshness
  node         Show one node with lineage and edges (--id)
  neighbors    Expand a neighbourhood (--from [--depth 1..8] [--direction out|in|both])
  path         Shortest path (--from --to [--max-depth] [--direction])
  search       Keyword lookup (--q [--node-types a,b] [--limit])
  answerable   Coverage and gaps for a question (--q)
  retrieve     Fused text+graph retrieval (--q [--top-k])
  put          Create or update a node (--id [--node-type] [--label] [--description])
  edge         Append a declared relation (--id --to --type [--evidence])
  reindex      Rebuild the projection from Git
`

// cmdGraph dispatches the graph sub-commands. Every action except put/edge is
// read-only; those two go through the canonical store, never the index.
func cmdGraph(args []string, out io.Writer) error {
	if len(args) == 0 {
		_, _ = io.WriteString(out, graphUsage)
		return nil
	}
	action, rest := args[0], args[1:]
	switch action {
	case "help", "-h", "--help":
		_, _ = io.WriteString(out, graphUsage)
		return nil
	case "stats":
		return cmdGraphStats(rest, out)
	case "node":
		return cmdGraphNode(rest, out)
	case "neighbors":
		return cmdGraphNeighbors(rest, out)
	case "path":
		return cmdGraphPath(rest, out)
	case "search":
		return cmdGraphSearch(rest, out)
	case "answerable":
		return cmdGraphAnswerable(rest, out)
	case "retrieve":
		return cmdGraphRetrieve(rest, out)
	case "put":
		return cmdGraphPut(rest, out)
	case "edge":
		return cmdGraphEdge(rest, out)
	case "reindex":
		return cmdGraphReindex(rest, out)
	default:
		return fmt.Errorf("graph: unknown action %q\n%s", action, graphUsage)
	}
}

func graphStringList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cmdGraphStats(args []string, out io.Writer) error {
	fs := newFlags("graph stats")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	stats, err := svc.GraphStats(context.Background(), *subject)
	if err != nil {
		return err
	}
	return printJSON(out, stats)
}

func cmdGraphNode(args []string, out io.Writer) error {
	fs := newFlags("graph node")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	id := fs.String("id", "", "concept id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("graph node: --id is required")
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	view, err := svc.GraphNode(context.Background(), *subject, *id)
	if err != nil {
		return err
	}
	return printJSON(out, view)
}

func cmdGraphNeighbors(args []string, out io.Writer) error {
	fs := newFlags("graph neighbors")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	from := fs.String("from", "", "root concept id")
	depth := fs.Int("depth", 1, "traversal depth 1..8")
	direction := fs.String("direction", "both", "out|in|both")
	edgeTypes := fs.String("edge-types", "", "comma separated edge type filter")
	nodeTypes := fs.String("node-types", "", "comma separated node type filter")
	limit := fs.Int("limit", 100, "max nodes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("graph neighbors: --from is required")
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	view, err := svc.GraphNeighbors(context.Background(), *subject, graph.WalkQuery{
		From: *from, Depth: *depth, Direction: *direction,
		EdgeTypes: graphStringList(*edgeTypes), NodeTypes: graphStringList(*nodeTypes), Limit: *limit,
	})
	if err != nil {
		return err
	}
	return printJSON(out, view)
}

func cmdGraphPath(args []string, out io.Writer) error {
	fs := newFlags("graph path")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	from := fs.String("from", "", "start concept id")
	to := fs.String("to", "", "target concept id")
	maxDepth := fs.Int("max-depth", 3, "maximum hops 1..8")
	direction := fs.String("direction", "both", "out|in|both")
	edgeTypes := fs.String("edge-types", "", "comma separated edge type filter")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" || *to == "" {
		return fmt.Errorf("graph path: --from and --to are required")
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	view, err := svc.GraphPath(context.Background(), *subject, graph.PathQuery{
		From: *from, To: *to, MaxDepth: *maxDepth, Direction: *direction,
		EdgeTypes: graphStringList(*edgeTypes), Limit: 200,
	})
	if err != nil {
		return err
	}
	return printJSON(out, view)
}

func cmdGraphSearch(args []string, out io.Writer) error {
	fs := newFlags("graph search")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	q := fs.String("q", "", "search text")
	nodeTypes := fs.String("node-types", "", "comma separated node type filter")
	limit := fs.Int("limit", 20, "max hits")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	hits, err := svc.GraphSearch(context.Background(), *subject, graph.SearchQuery{
		Text: *q, NodeTypes: graphStringList(*nodeTypes), Limit: *limit,
	})
	if err != nil {
		return err
	}
	return printJSON(out, hits)
}

func cmdGraphAnswerable(args []string, out io.Writer) error {
	fs := newFlags("graph answerable")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	q := fs.String("q", "", "question")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *q == "" {
		return fmt.Errorf("graph answerable: --q is required")
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	cov, err := svc.GraphAnswerability(context.Background(), *subject, *q)
	if err != nil {
		return err
	}
	return printJSON(out, cov)
}

func cmdGraphRetrieve(args []string, out io.Writer) error {
	fs := newFlags("graph retrieve")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	q := fs.String("q", "", "query text")
	topK := fs.Int("top-k", 5, "result limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *q == "" {
		return fmt.Errorf("graph retrieve: --q is required")
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	res, err := svc.GraphRetrieve(context.Background(), *subject, *q, *topK)
	if err != nil {
		return err
	}
	return printJSON(out, res)
}

func cmdGraphPut(args []string, out io.Writer) error {
	fs := newFlags("graph put")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	id := fs.String("id", "", "concept id")
	nodeType := fs.String("node-type", "", "node type (defaults to the document type)")
	label := fs.String("label", "", "display label")
	desc := fs.String("description", "", "description")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("graph put: --id is required")
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	in := graph.NodeInput{ConceptID: *id}
	if *nodeType != "" {
		in.NodeType = nodeType
	}
	if *label != "" {
		in.Label = label
	}
	if *desc != "" {
		in.Description = desc
	}
	res, warnings, err := svc.GraphPutNode(context.Background(), *subject, in)
	if err != nil {
		return err
	}
	return printJSON(out, map[string]any{
		"concept_id": res.ConceptID, "content_hash": res.ContentHash,
		"commit_sha": res.CommitSHA, "unchanged": res.Unchanged, "warnings": warnings,
	})
}

func cmdGraphEdge(args []string, out io.Writer) error {
	fs := newFlags("graph edge")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	subject := fs.String("subject", "", "requesting subject")
	id := fs.String("id", "", "source concept id")
	to := fs.String("to", "", "target concept id")
	kind := fs.String("type", "", "edge type")
	evidence := fs.String("evidence", "", "evidence pointer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *to == "" || *kind == "" {
		return fmt.Errorf("graph edge: --id, --to and --type are required")
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	res, warnings, err := svc.GraphAddEdges(context.Background(), *subject, *id,
		[]okf.GraphEdge{{To: *to, Type: *kind, Evidence: *evidence}})
	if err != nil {
		return err
	}
	return printJSON(out, map[string]any{
		"concept_id": res.ConceptID, "content_hash": res.ContentHash,
		"commit_sha": res.CommitSHA, "unchanged": res.Unchanged, "warnings": warnings,
	})
}

func cmdGraphReindex(args []string, out io.Writer) error {
	fs := newFlags("graph reindex")
	envPath := fs.String("env", "", "path to .env file (default .env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := assemble(*envPath)
	if err != nil {
		return err
	}
	defer svc.Close()
	report, err := svc.GraphRebuild(context.Background())
	if err != nil {
		return err
	}
	return printJSON(out, report)
}
