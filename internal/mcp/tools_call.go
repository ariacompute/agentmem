package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agentmem/internal/auth"
	"agentmem/internal/compute"
	"agentmem/internal/graph"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

const (
	defaultAuditLimit = 20
	maxAuditLimit     = 1000
	maxTopK           = 100
)

func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return invalid("invalid arguments")
	}
	return nil
}

// limit validates a result limit. Zero keeps the service default rather than
// silently applying the smallest legal value.
func (s *Server) limit(n int) (int, error) {
	if n < 0 || n > s.cfg.MaxLimit {
		return 0, invalid("limit is out of range")
	}
	return n, nil
}

func (s *Server) depth(n, def int) (int, error) {
	if n < 0 || n > s.cfg.MaxDepth {
		return 0, invalid("depth is out of range")
	}
	if n == 0 {
		if def > s.cfg.MaxDepth {
			return s.cfg.MaxDepth, nil
		}
		return def, nil
	}
	return n, nil
}

func topK(n int) (int, error) {
	if n < 0 || n > maxTopK {
		return 0, invalid("top_k is out of range")
	}
	return n, nil
}

func auditLimit(n int) (int, error) {
	if n < 0 || n > maxAuditLimit {
		return 0, invalid("limit is out of range")
	}
	if n == 0 {
		return defaultAuditLimit, nil
	}
	return n, nil
}

func direction(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "both":
		return "both", nil
	case "out", "in":
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", invalid("direction must be out, in or both")
	}
}

// graphError maps the graph package's sentinels to stable protocol errors. Any
// other failure stays an internal error so no driver text reaches the client.
func graphError(err error) error {
	switch {
	case errors.Is(err, graph.ErrDisabled):
		return &domainError{code: CodeMethodNotFound, msg: "graph is disabled"}
	case errors.Is(err, graph.ErrNotFound):
		return notFound("node not found or access denied")
	case errors.Is(err, graph.ErrInvalid):
		return invalid("graph request rejected")
	default:
		return err
	}
}

func (s *Server) addSearchTools() {
	s.add(&Tool{
		Name:        "search_terms",
		Description: "Find graph nodes by keyword.",
		Scope:       auth.PermKnowledgeRead,
		InputSchema: objectSchema(map[string]any{
			"query":      stringSchema("search text"),
			"limit":      intSchema("max hits, 0 for the service default, at most 200"),
			"node_types": arraySchema("node type filter", map[string]any{"type": "string"}),
		}, "query"),
		OutputSchema: nodeListOutput,
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				Query     string   `json:"query"`
				Limit     int      `json:"limit"`
				NodeTypes []string `json:"node_types"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			limit, err := s.limit(in.Limit)
			if err != nil {
				return nil, err
			}
			hits, err := s.backend.GraphSearch(ctx, subject, graph.SearchQuery{
				Text: strings.TrimSpace(in.Query), NodeTypes: in.NodeTypes, Limit: limit,
			})
			if err != nil {
				return nil, graphError(err)
			}
			return map[string]any{"nodes": hits}, nil
		},
	})
}

func (s *Server) addReadTools() {
	s.add(&Tool{
		Name:         "get_node",
		Description:  "Read one graph node with lineage and edges.",
		Scope:        auth.PermKnowledgeRead,
		InputSchema:  objectSchema(map[string]any{"concept_id": stringSchema("concept id")}, "concept_id"),
		OutputSchema: objectSchema(map[string]any{"node": objectSchema(map[string]any{})}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID string `json:"concept_id"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.ConceptID) == "" {
				return nil, invalid("concept_id is required")
			}
			view, err := s.backend.GraphNode(ctx, subject, in.ConceptID)
			if err != nil {
				return nil, graphError(err)
			}
			return view, nil
		},
	})
	s.add(&Tool{
		Name:        "neighbors",
		Description: "Expand a bounded neighbourhood.",
		Scope:       auth.PermKnowledgeRead,
		InputSchema: objectSchema(map[string]any{
			"concept_id": stringSchema("root concept id"),
			"depth":      intSchema("1..8"),
			"direction":  stringSchema("out, in or both"),
			"edge_types": arraySchema("edge type filter", map[string]any{"type": "string"}),
			"node_types": arraySchema("node type filter", map[string]any{"type": "string"}),
			"limit":      intSchema("max nodes, at most 200"),
		}, "concept_id"),
		OutputSchema: objectSchema(map[string]any{"subgraph": objectSchema(map[string]any{})}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID string   `json:"concept_id"`
				Depth     int      `json:"depth"`
				Direction string   `json:"direction"`
				EdgeTypes []string `json:"edge_types"`
				NodeTypes []string `json:"node_types"`
				Limit     int      `json:"limit"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.ConceptID) == "" {
				return nil, invalid("concept_id is required")
			}
			depth, err := s.depth(in.Depth, 1)
			if err != nil {
				return nil, err
			}
			limit, err := s.limit(in.Limit)
			if err != nil {
				return nil, err
			}
			dir, err := direction(in.Direction)
			if err != nil {
				return nil, err
			}
			view, err := s.backend.GraphNeighbors(ctx, subject, graph.WalkQuery{
				From: in.ConceptID, Depth: depth, Direction: dir,
				EdgeTypes: in.EdgeTypes, NodeTypes: in.NodeTypes, Limit: limit,
			})
			if err != nil {
				return nil, graphError(err)
			}
			return view, nil
		},
	})
	s.add(&Tool{
		Name:        "path",
		Description: "Shortest readable path between two nodes.",
		Scope:       auth.PermKnowledgeRead,
		InputSchema: objectSchema(map[string]any{
			"from":       stringSchema("start concept id"),
			"to":         stringSchema("target concept id"),
			"max_depth":  intSchema("1..8"),
			"direction":  stringSchema("out, in or both"),
			"edge_types": arraySchema("edge type filter", map[string]any{"type": "string"}),
		}, "from", "to"),
		OutputSchema: objectSchema(map[string]any{"path": objectSchema(map[string]any{})}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				From      string   `json:"from"`
				To        string   `json:"to"`
				MaxDepth  int      `json:"max_depth"`
				Direction string   `json:"direction"`
				EdgeTypes []string `json:"edge_types"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.From) == "" || strings.TrimSpace(in.To) == "" {
				return nil, invalid("from and to are required")
			}
			depth, err := s.depth(in.MaxDepth, 3)
			if err != nil {
				return nil, err
			}
			dir, err := direction(in.Direction)
			if err != nil {
				return nil, err
			}
			view, err := s.backend.GraphPath(ctx, subject, graph.PathQuery{
				From: in.From, To: in.To, MaxDepth: depth, Direction: dir,
				EdgeTypes: in.EdgeTypes, Limit: s.cfg.MaxLimit,
			})
			if err != nil {
				return nil, graphError(err)
			}
			return view, nil
		},
	})
	s.add(&Tool{
		Name:         "check_answerable",
		Description:  "Report ontology coverage and gaps for a question.",
		Scope:        auth.PermKnowledgeRead,
		InputSchema:  objectSchema(map[string]any{"question": stringSchema("natural language question")}, "question"),
		OutputSchema: objectSchema(map[string]any{"coverage": objectSchema(map[string]any{})}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				Question string `json:"question"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Question) == "" {
				return nil, invalid("question is required")
			}
			cov, err := s.backend.GraphAnswerability(ctx, subject, in.Question)
			if err != nil {
				return nil, graphError(err)
			}
			return cov, nil
		},
	})
	s.add(&Tool{
		Name:         "graph_schema",
		Description:  "Read the merged node and edge type ontology.",
		Scope:        auth.PermKnowledgeRead,
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: objectSchema(map[string]any{"node_types": objectSchema(map[string]any{}), "edge_types": objectSchema(map[string]any{}), "warnings": arraySchema("schema warnings", map[string]any{"type": "string"})}),
		run: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			schema, warnings, err := s.backend.GraphSchema(ctx)
			if err != nil {
				return nil, graphError(err)
			}
			if warnings == nil {
				warnings = []string{}
			}
			return map[string]any{
				"node_types": schema.NodeTypes, "edge_types": schema.EdgeTypes, "warnings": warnings,
			}, nil
		},
	})
	s.add(&Tool{
		Name:         "graph_stats",
		Description:  "Read visibility-scoped index size and freshness.",
		Scope:        auth.PermKnowledgeRead,
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: objectSchema(map[string]any{"stats": objectSchema(map[string]any{})}),
		run: func(ctx context.Context, subject string, _ json.RawMessage) (any, error) {
			stats, err := s.backend.GraphStats(ctx, subject)
			if err != nil {
				return nil, graphError(err)
			}
			return stats, nil
		},
	})
	s.add(&Tool{
		Name:        "graph_retrieve",
		Description: "Fuse text hits and graph neighbours into one ranked list.",
		Scope:       auth.PermKnowledgeRead,
		InputSchema: objectSchema(map[string]any{
			"query": stringSchema("search text"),
			"top_k": intSchema("1..100"),
		}, "query"),
		OutputSchema: objectSchema(map[string]any{"hits": arraySchema("fused hits", objectItems())}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				Query string `json:"query"`
				TopK  int    `json:"top_k"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Query) == "" {
				return nil, invalid("query is empty")
			}
			k, err := topK(in.TopK)
			if err != nil {
				return nil, err
			}
			res, err := s.backend.GraphRetrieve(ctx, subject, in.Query, k)
			if err != nil {
				return nil, graphError(err)
			}
			return res, nil
		},
	})
	s.add(&Tool{
		Name:        "query",
		Description: "Route and execute a knowledge query through the RAG adapters.",
		Scope:       auth.PermKnowledgeRead,
		InputSchema: objectSchema(map[string]any{
			"query": stringSchema("query text"),
			"top_k": intSchema("1..100"),
		}, "query"),
		OutputSchema: objectSchema(map[string]any{"hits": arraySchema("retrieved hits", objectItems()), "decision": objectSchema(map[string]any{})}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				Query string `json:"query"`
				TopK  int    `json:"top_k"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Query) == "" {
				return nil, invalid("query is empty")
			}
			k, err := topK(in.TopK)
			if err != nil {
				return nil, err
			}
			res, err := s.backend.Query(ctx, subject, in.Query, k)
			if err != nil {
				return nil, err
			}
			return res, nil
		},
	})
	s.add(&Tool{
		Name:         "list_concepts",
		Description:  "List canonical concept ids.",
		Scope:        auth.PermKnowledgeRead,
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: conceptOutput,
		run: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			ids, err := s.backend.Concepts(ctx)
			if err != nil {
				return nil, err
			}
			if ids == nil {
				ids = []string{}
			}
			return map[string]any{"concepts": ids}, nil
		},
	})
	s.add(&Tool{
		Name:        "read_knowledge",
		Description: "Read one canonical knowledge body, subject to its ACL.",
		Scope:       auth.PermKnowledgeRead,
		InputSchema: objectSchema(map[string]any{"concept_id": stringSchema("concept id")}, "concept_id"),
		OutputSchema: objectSchema(map[string]any{
			"concept_id": stringSchema("concept id"), "type": stringSchema("OKF type"),
			"title": stringSchema("title"), "body": stringSchema("markdown body"),
		}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID string `json:"concept_id"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.ConceptID) == "" {
				return nil, invalid("concept_id is required")
			}
			doc, err := s.backend.Knowledge(ctx, subject, in.ConceptID)
			if err != nil {
				// Private and missing documents stay indistinguishable.
				return nil, notFound("document not found or access denied")
			}
			return map[string]any{
				"concept_id": in.ConceptID, "type": doc.Type, "title": doc.Title,
				"description": doc.Description, "body": doc.Body,
				"status": doc.EffectiveStatus(), "trust_tier": doc.TrustTier(),
				"visibility": doc.Visibility(), "owner": doc.Owner(),
			}, nil
		},
	})
	s.add(&Tool{
		Name:        "list_decisions",
		Description: "Read the routing audit trail.",
		Scope:       auth.PermAuditRead,
		InputSchema: objectSchema(map[string]any{
			"concept_id": stringSchema("filter by concept id"),
			"limit":      intSchema("max records, at most 1000"),
		}),
		OutputSchema: decisionOutput,
		run: func(ctx context.Context, _ string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID string `json:"concept_id"`
				Limit     int    `json:"limit"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			limit, err := auditLimit(in.Limit)
			if err != nil {
				return nil, err
			}
			records, err := s.backend.Decisions(ctx, in.ConceptID, limit)
			if err != nil {
				return nil, err
			}
			if records == nil {
				records = []meta.DecisionRecord{}
			}
			return map[string]any{"decisions": records}, nil
		},
	})
	s.add(&Tool{
		Name:        "list_computations",
		Description: "Read Attested Computation verdicts.",
		Scope:       auth.PermAuditRead,
		InputSchema: objectSchema(map[string]any{
			"concept_id": stringSchema("filter by concept id"),
			"limit":      intSchema("max records, at most 1000"),
		}),
		OutputSchema: computationOutput,
		run: func(ctx context.Context, _ string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID string `json:"concept_id"`
				Limit     int    `json:"limit"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			limit, err := auditLimit(in.Limit)
			if err != nil {
				return nil, err
			}
			records, err := s.backend.Computations(ctx, in.ConceptID, limit)
			if err != nil {
				return nil, err
			}
			if records == nil {
				records = []meta.ComputationRecord{}
			}
			return map[string]any{"computations": records}, nil
		},
	})
}

func (s *Server) addWriteTools() {
	if !s.cfg.Write {
		return
	}
	edgeSchema := objectSchema(map[string]any{
		"to":       stringSchema("target concept id"),
		"type":     stringSchema("declared edge type"),
		"evidence": stringSchema("evidence reference"),
	}, "to", "type")
	s.add(&Tool{
		Name:        "put_knowledge",
		Description: "Write a canonical knowledge concept. Requires knowledge:write.",
		Scope:       auth.PermKnowledgeWrite,
		Mutating:    true,
		InputSchema: objectSchema(map[string]any{
			"concept_id": stringSchema("concept id"),
			"document": objectSchema(map[string]any{
				"type":        stringSchema("OKF type (required)"),
				"title":       stringSchema("title"),
				"description": stringSchema("description"),
				"body":        stringSchema("markdown body"),
				"tags":        arraySchema("tags", map[string]any{"type": "string"}),
				"visibility":  stringSchema("private, team or public"),
				"owner":       stringSchema("owner (must match the caller)"),
			}, "type"),
		}, "concept_id", "document"),
		OutputSchema: objectSchema(map[string]any{
			"concept_id": stringSchema("concept id"), "content_hash": stringSchema("content hash"),
			"commit_sha": stringSchema("git commit"), "unchanged": boolSchema("no-op write"),
		}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID string `json:"concept_id"`
				Document  struct {
					Type        string   `json:"type"`
					Title       string   `json:"title"`
					Description string   `json:"description"`
					Body        string   `json:"body"`
					Tags        []string `json:"tags"`
					Visibility  string   `json:"visibility"`
					Owner       string   `json:"owner"`
				} `json:"document"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.ConceptID) == "" || strings.TrimSpace(in.Document.Type) == "" {
				return nil, invalid("concept_id and document.type are required")
			}
			doc := &okf.Document{
				Type: in.Document.Type, Title: in.Document.Title,
				Description: in.Document.Description, Body: in.Document.Body,
				Tags: in.Document.Tags,
			}
			if in.Document.Visibility != "" {
				doc.SetVisibility(in.Document.Visibility)
			}
			if in.Document.Owner != "" {
				doc.SetOwner(in.Document.Owner)
			}
			res, err := s.backend.Put(ctx, subject, in.ConceptID, doc)
			if err != nil {
				return nil, invalid("write rejected")
			}
			return res, nil
		},
	})
	s.add(&Tool{
		Name:        "put_graph_node",
		Description: "Upsert one typed graph node. Requires graph:write.",
		Scope:       auth.PermGraphWrite,
		Mutating:    true,
		InputSchema: objectSchema(map[string]any{
			"concept_id":    stringSchema("concept id"),
			"node_type":     stringSchema("node type"),
			"label":         stringSchema("label"),
			"description":   stringSchema("description"),
			"properties":    stringMapSchema("node properties"),
			"edges":         arraySchema("declared edges", edgeSchema),
			"replace_edges": boolSchema("replace the declared edge set"),
		}, "concept_id"),
		OutputSchema: writeOutput(),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID    string            `json:"concept_id"`
				NodeType     string            `json:"node_type"`
				Label        string            `json:"label"`
				Description  string            `json:"description"`
				Properties   map[string]string `json:"properties"`
				Edges        []edgeInput       `json:"edges"`
				ReplaceEdges bool              `json:"replace_edges"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.ConceptID) == "" {
				return nil, invalid("concept_id is required")
			}
			node := graph.NodeInput{
				ConceptID: in.ConceptID, Properties: in.Properties, EdgesSet: in.ReplaceEdges,
			}
			if in.NodeType != "" {
				node.NodeType = &in.NodeType
			}
			if in.Label != "" {
				node.Label = &in.Label
			}
			if in.Description != "" {
				node.Description = &in.Description
			}
			if in.ReplaceEdges {
				node.Edges = toEdges(in.Edges)
			}
			res, warnings, err := s.backend.GraphPutNode(ctx, subject, node)
			if err != nil {
				return nil, graphError(err)
			}
			return writeResult(res, warnings), nil
		},
	})
	s.add(&Tool{
		Name:        "append_graph_edge",
		Description: "Append one declared relation to an existing node. Requires graph:write.",
		Scope:       auth.PermGraphWrite,
		Mutating:    true,
		InputSchema: objectSchema(map[string]any{
			"concept_id": stringSchema("source concept id"),
			"edges":      arraySchema("edges to append", edgeSchema),
		}, "concept_id", "edges"),
		OutputSchema: writeOutput(),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID string      `json:"concept_id"`
				Edges     []edgeInput `json:"edges"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.ConceptID) == "" || len(in.Edges) == 0 {
				return nil, invalid("concept_id and edges are required")
			}
			res, warnings, err := s.backend.GraphAddEdges(ctx, subject, in.ConceptID, toEdges(in.Edges))
			if err != nil {
				return nil, graphError(err)
			}
			return writeResult(res, warnings), nil
		},
	})
	s.add(&Tool{
		Name:        "run_computation",
		Description: "Execute an Attested Computation. Requires compute:run.",
		Scope:       auth.PermComputeRun,
		Mutating:    true,
		InputSchema: objectSchema(map[string]any{
			"concept_id": stringSchema("Attested Computation concept id"),
			"parameters": map[string]any{
				"type": "object", "description": "declared parameter values",
				"additionalProperties": true,
			},
		}, "concept_id"),
		OutputSchema: objectSchema(map[string]any{"envelope": objectSchema(map[string]any{})}),
		run: func(ctx context.Context, subject string, raw json.RawMessage) (any, error) {
			var in struct {
				ConceptID  string         `json:"concept_id"`
				Parameters map[string]any `json:"parameters"`
			}
			if err := decodeArgs(raw, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.ConceptID) == "" {
				return nil, invalid("concept_id is required")
			}
			env, err := s.backend.RunComputation(ctx, compute.Request{
				ConceptID: in.ConceptID, Subject: subject, Parameters: in.Parameters,
			})
			if err != nil {
				return map[string]any{"envelope": env}, &ToolError{
					Message: "computation rejected", Structured: map[string]any{"envelope": env},
				}
			}
			return env, nil
		},
	})
	s.add(&Tool{
		Name:         "rebuild_graph_index",
		Description:  "Rebuild the graph projection from Git. Requires migrate:run.",
		Scope:        auth.PermMigrateRun,
		Mutating:     true,
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: objectSchema(map[string]any{"nodes": intSchema("indexed nodes"), "edges": intSchema("indexed edges"), "pruned": intSchema("removed rows")}),
		run: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			report, err := s.backend.GraphRebuild(ctx)
			if err != nil {
				return nil, graphError(err)
			}
			return report, nil
		},
	})
}

type edgeInput struct {
	To       string `json:"to"`
	Type     string `json:"type"`
	Evidence string `json:"evidence"`
}

func toEdges(in []edgeInput) []okf.GraphEdge {
	out := make([]okf.GraphEdge, 0, len(in))
	for _, e := range in {
		out = append(out, okf.GraphEdge{To: e.To, Type: e.Type, Evidence: e.Evidence})
	}
	return out
}

func writeOutput() map[string]any {
	return objectSchema(map[string]any{
		"concept_id": stringSchema("concept id"), "content_hash": stringSchema("content hash"),
		"commit_sha": stringSchema("git commit"), "unchanged": boolSchema("no-op write"),
		"warnings": arraySchema("projection warnings", map[string]any{"type": "string"}),
	})
}

func writeResult(res graph.WriteResult, warnings []string) map[string]any {
	if warnings == nil {
		warnings = []string{}
	}
	return map[string]any{
		"concept_id": res.ConceptID, "content_hash": res.ContentHash,
		"commit_sha": res.CommitSHA, "unchanged": res.Unchanged, "warnings": warnings,
	}
}
