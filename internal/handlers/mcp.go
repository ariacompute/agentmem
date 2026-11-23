package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"agentmem/internal/graph"
	"agentmem/internal/service"
)

// MCP is a read-only JSON-RPC endpoint. It exposes the same graph reads the
// HTTP API does and has no write tool at all: an MCP client can never mutate
// knowledge, so the transport adds no new privilege boundary.

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

const mcpProtocolVersion = "2024-11-05"

func mcpRoute(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	if !svc.GraphEnabled() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return
	}
	var req mcpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &mcpError{Code: -32700, Message: "parse error"}})
		return
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: idOrNull(req.ID),
			Error: &mcpError{Code: -32600, Message: "invalid request"}})
		return
	}
	// Notifications carry no id and expect no body.
	if len(req.ID) == 0 && strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rerr := mcpDispatch(r, svc, req)
	if rerr != nil {
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: idOrNull(req.ID), Error: rerr})
		return
	}
	writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: idOrNull(req.ID), Result: result})
}

func mcpDispatch(r *http.Request, svc *service.KnowledgeService, req mcpRequest) (any, *mcpError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agentmem", "version": "graph"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		return mcpCall(r, svc, req.Params)
	default:
		return nil, &mcpError{Code: -32601, Message: "method not found"}
	}
}

func mcpTools() []map[string]any {
	object := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	num := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	arr := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	return []map[string]any{
		{
			"name": "search_terms", "description": "Find graph nodes by keyword.",
			"inputSchema": object(map[string]any{"query": str("search text"), "limit": num("max hits, 1..200")}, "query"),
		},
		{
			"name": "get_node", "description": "Read one node with lineage and edges.",
			"inputSchema": object(map[string]any{"concept_id": str("concept id")}, "concept_id"),
		},
		{
			"name": "neighbors", "description": "Expand a bounded neighbourhood.",
			"inputSchema": object(map[string]any{
				"concept_id": str("root concept id"), "depth": num("1..8"),
				"direction": str("out, in or both"), "edge_types": arr("edge type filter"),
				"node_types": arr("node type filter"), "limit": num("max nodes, 1..200"),
			}, "concept_id"),
		},
		{
			"name": "path", "description": "Shortest readable path between two nodes.",
			"inputSchema": object(map[string]any{
				"from": str("start concept id"), "to": str("target concept id"),
				"max_depth": num("1..8"), "direction": str("out, in or both"),
				"edge_types": arr("edge type filter"),
			}, "from", "to"),
		},
		{
			"name": "check_answerable", "description": "Report ontology coverage and gaps for a question.",
			"inputSchema": object(map[string]any{"question": str("natural language question")}, "question"),
		},
	}
}

type mcpCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func mcpCall(r *http.Request, svc *service.KnowledgeService, raw json.RawMessage) (any, *mcpError) {
	var call mcpCallParams
	if err := json.Unmarshal(raw, &call); err != nil || strings.TrimSpace(call.Name) == "" {
		return nil, &mcpError{Code: -32602, Message: "invalid params"}
	}
	subject, ok := subjectFrom(r)
	if !ok {
		return nil, &mcpError{Code: -32001, Message: "authentication required"}
	}
	var (
		out any
		err error
	)
	switch call.Name {
	case "search_terms":
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if jerr := json.Unmarshal(call.Arguments, &args); jerr != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid arguments"}
		}
		out, err = svc.GraphSearch(r.Context(), subject, graph.SearchQuery{Text: args.Query, Limit: clampLimit(args.Limit)})
	case "get_node":
		var args struct {
			ConceptID string `json:"concept_id"`
		}
		if jerr := json.Unmarshal(call.Arguments, &args); jerr != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid arguments"}
		}
		out, err = svc.GraphNode(r.Context(), subject, args.ConceptID)
	case "neighbors":
		var args struct {
			ConceptID string   `json:"concept_id"`
			Depth     int      `json:"depth"`
			Direction string   `json:"direction"`
			EdgeTypes []string `json:"edge_types"`
			NodeTypes []string `json:"node_types"`
			Limit     int      `json:"limit"`
		}
		if jerr := json.Unmarshal(call.Arguments, &args); jerr != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid arguments"}
		}
		out, err = svc.GraphNeighbors(r.Context(), subject, graph.WalkQuery{
			From: args.ConceptID, Depth: args.Depth, Direction: args.Direction,
			EdgeTypes: args.EdgeTypes, NodeTypes: args.NodeTypes, Limit: clampLimit(args.Limit),
		})
	case "path":
		var args struct {
			From      string   `json:"from"`
			To        string   `json:"to"`
			MaxDepth  int      `json:"max_depth"`
			Direction string   `json:"direction"`
			EdgeTypes []string `json:"edge_types"`
		}
		if jerr := json.Unmarshal(call.Arguments, &args); jerr != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid arguments"}
		}
		out, err = svc.GraphPath(r.Context(), subject, graph.PathQuery{
			From: args.From, To: args.To, MaxDepth: args.MaxDepth,
			Direction: args.Direction, EdgeTypes: args.EdgeTypes, Limit: graphMaxLimit,
		})
	case "check_answerable":
		var args struct {
			Question string `json:"question"`
		}
		if jerr := json.Unmarshal(call.Arguments, &args); jerr != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid arguments"}
		}
		out, err = svc.GraphAnswerability(r.Context(), subject, args.Question)
	default:
		return nil, &mcpError{Code: -32602, Message: "unknown tool"}
	}
	if err != nil {
		return nil, mcpGraphError(err)
	}
	text, merr := json.Marshal(out)
	if merr != nil {
		return nil, &mcpError{Code: -32603, Message: "internal error"}
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": out,
		"isError":           false,
	}, nil
}

func mcpGraphError(err error) *mcpError {
	switch {
	case errors.Is(err, graph.ErrDisabled):
		return &mcpError{Code: -32601, Message: "graph is disabled"}
	case errors.Is(err, graph.ErrNotFound):
		return &mcpError{Code: -32002, Message: "node not found or access denied"}
	case errors.Is(err, graph.ErrInvalid):
		return &mcpError{Code: -32602, Message: "graph request rejected"}
	default:
		return &mcpError{Code: -32603, Message: "internal error"}
	}
}

func clampLimit(n int) int {
	if n <= 0 {
		return 20
	}
	if n > graphMaxLimit {
		return graphMaxLimit
	}
	return n
}

func idOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

func writeMCP(w http.ResponseWriter, resp mcpResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
