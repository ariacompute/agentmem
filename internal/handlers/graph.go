package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"agentmem/internal/graph"
	"agentmem/internal/okf"
	"agentmem/internal/service"
)

const graphMaxLimit = 200

// graphRoute answers every /v1/graph* path. A disabled feature, an unknown
// sub-path and a vanished node all answer the same JSON 404, so the surface
// never advertises what it cannot serve.
func graphRoute(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	if !svc.GraphEnabled() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.URL.Path {
	case "/v1/graph":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		handleGraphStats(w, r, svc)
	case "/v1/graph/node":
		if r.Method != http.MethodPut {
			if r.Method == http.MethodGet {
				handleGraphNode(w, r, svc)
				return
			}
			methodNotAllowed(w)
			return
		}
		handleGraphPutNode(w, r, svc)
	case "/v1/graph/neighbors":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		handleGraphNeighbors(w, r, svc)
	case "/v1/graph/path":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		handleGraphPath(w, r, svc)
	case "/v1/graph/search":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		handleGraphSearch(w, r, svc)
	case "/v1/graph/schema":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		handleGraphSchema(w, r, svc)
	case "/v1/graph/answerability":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		handleGraphAnswerability(w, r, svc)
	case "/v1/graph/retrieve":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		handleGraphRetrieve(w, r, svc)
	case "/v1/graph/index":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		handleGraphIndex(w, r, svc)
	case "/v1/graph/edges":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		handleGraphEdges(w, r, svc)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func handleGraphStats(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	stats, err := svc.GraphStats(r.Context(), subject)
	if err != nil {
		graphFailure(w, "graph-stats", err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func handleGraphNode(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	view, err := svc.GraphNode(r.Context(), subject, id)
	if err != nil {
		graphFailure(w, "graph-node", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func handleGraphNeighbors(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	q := r.URL.Query()
	from := strings.TrimSpace(q.Get("from"))
	if from == "" {
		writeError(w, http.StatusBadRequest, "from is required")
		return
	}
	depth, ok := parseBounded(w, q.Get("depth"), 1, svc.MaxGraphDepth())
	if !ok {
		return
	}
	if depth == 0 {
		depth = 1
	}
	limit, ok := parseBounded(w, q.Get("limit"), 1, graphMaxLimit)
	if !ok {
		return
	}
	direction, ok := parseDirection(w, q.Get("direction"))
	if !ok {
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	view, err := svc.GraphNeighbors(r.Context(), subject, graph.WalkQuery{
		From: from, Depth: depth, Direction: direction,
		EdgeTypes: splitList(q.Get("edge_types")), NodeTypes: splitList(q.Get("node_types")),
		Limit: limit,
	})
	if err != nil {
		graphFailure(w, "graph-neighbors", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func handleGraphPath(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	q := r.URL.Query()
	from := strings.TrimSpace(q.Get("from"))
	to := strings.TrimSpace(q.Get("to"))
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	depth, ok := parseBounded(w, q.Get("max_depth"), 1, svc.MaxGraphDepth())
	if !ok {
		return
	}
	if depth == 0 {
		depth = 3
	}
	direction, ok := parseDirection(w, q.Get("direction"))
	if !ok {
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	view, err := svc.GraphPath(r.Context(), subject, graph.PathQuery{
		From: from, To: to, MaxDepth: depth, Direction: direction,
		EdgeTypes: splitList(q.Get("edge_types")), Limit: graphMaxLimit,
	})
	if err != nil {
		graphFailure(w, "graph-path", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func handleGraphSearch(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	q := r.URL.Query()
	limit, ok := parseBounded(w, q.Get("limit"), 1, graphMaxLimit)
	if !ok {
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	hits, err := svc.GraphSearch(r.Context(), subject, graph.SearchQuery{
		Text: q.Get("q"), NodeTypes: splitList(q.Get("node_types")), Limit: limit,
	})
	if err != nil {
		graphFailure(w, "graph-search", err)
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

func handleGraphSchema(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	schema, warnings, err := svc.GraphSchema(r.Context())
	if err != nil {
		graphFailure(w, "graph-schema", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node_types": schema.NodeTypes, "edge_types": schema.EdgeTypes, "warnings": warnings,
	})
}

type answerabilityRequest struct {
	Question string `json:"question"`
}

func handleGraphAnswerability(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	var req answerabilityRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeError(w, http.StatusBadRequest, "question is required")
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	cov, err := svc.GraphAnswerability(r.Context(), subject, req.Question)
	if err != nil {
		graphFailure(w, "graph-answerability", err)
		return
	}
	writeJSON(w, http.StatusOK, cov)
}

type retrieveRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

func handleGraphRetrieve(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	var req retrieveRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "query is empty")
		return
	}
	if req.TopK < 0 || req.TopK > maxTopK {
		writeError(w, http.StatusBadRequest, "top_k is out of range")
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	res, err := svc.GraphRetrieve(r.Context(), subject, req.Query, req.TopK)
	if err != nil {
		graphFailure(w, "graph-retrieve", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func handleGraphIndex(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	report, err := svc.GraphRebuild(r.Context())
	if err != nil {
		graphFailure(w, "graph-index", err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

type graphEdgeInput struct {
	To       string `json:"to"`
	Type     string `json:"type"`
	Evidence string `json:"evidence"`
}

type graphNodeInput struct {
	ConceptID    string            `json:"concept_id"`
	NodeType     string            `json:"node_type"`
	Label        string            `json:"label"`
	Description  string            `json:"description"`
	Properties   map[string]string `json:"properties"`
	Edges        []graphEdgeInput  `json:"edges"`
	ReplaceEdges bool              `json:"replace_edges"`
}

func handleGraphPutNode(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	var req graphNodeInput
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ConceptID) == "" {
		writeError(w, http.StatusBadRequest, "concept_id is required")
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	in := graph.NodeInput{ConceptID: req.ConceptID, Properties: req.Properties, EdgesSet: req.ReplaceEdges}
	if req.NodeType != "" {
		in.NodeType = &req.NodeType
	}
	if req.Label != "" {
		in.Label = &req.Label
	}
	if req.Description != "" {
		in.Description = &req.Description
	}
	if req.ReplaceEdges {
		in.Edges = toGraphEdges(req.Edges)
	}
	res, warnings, err := svc.GraphPutNode(r.Context(), subject, in)
	if err != nil {
		graphFailure(w, "graph-put-node", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"concept_id": res.ConceptID, "content_hash": res.ContentHash,
		"commit_sha": res.CommitSHA, "unchanged": res.Unchanged, "warnings": warnings,
	})
}

type graphEdgesRequest struct {
	ConceptID string           `json:"concept_id"`
	Edges     []graphEdgeInput `json:"edges"`
}

func handleGraphEdges(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	var req graphEdgesRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ConceptID) == "" || len(req.Edges) == 0 {
		writeError(w, http.StatusBadRequest, "concept_id and edges are required")
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	res, warnings, err := svc.GraphAddEdges(r.Context(), subject, req.ConceptID, toGraphEdges(req.Edges))
	if err != nil {
		graphFailure(w, "graph-edges", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"concept_id": res.ConceptID, "content_hash": res.ContentHash,
		"commit_sha": res.CommitSHA, "unchanged": res.Unchanged, "warnings": warnings,
	})
}

func toGraphEdges(in []graphEdgeInput) []okf.GraphEdge {
	out := make([]okf.GraphEdge, 0, len(in))
	for _, e := range in {
		out = append(out, okf.GraphEdge{To: e.To, Type: e.Type, Evidence: e.Evidence})
	}
	return out
}

// splitList parses a repeated comma-separated filter.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseBounded reads an optional integer query parameter and enforces its
// range. An absent value returns 0 so the service layer can apply its own
// documented default instead of the smallest legal value.
func parseBounded(w http.ResponseWriter, raw string, min, max int) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		writeError(w, http.StatusBadRequest, "numeric parameter is out of range")
		return 0, false
	}
	return n, true
}

func parseDirection(w http.ResponseWriter, raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "both":
		return "both", true
	case "out", "in":
		return strings.ToLower(strings.TrimSpace(raw)), true
	default:
		writeError(w, http.StatusBadRequest, "direction must be out, in or both")
		return "", false
	}
}

// graphFailure maps graph error categories to stable status codes without
// echoing the underlying error.
func graphFailure(w http.ResponseWriter, class string, err error) {
	switch {
	case errors.Is(err, graph.ErrDisabled):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, graph.ErrNotFound):
		writeError(w, http.StatusNotFound, "node not found or access denied")
	case errors.Is(err, graph.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "graph request rejected")
	default:
		serverError(w, class, err)
	}
}
