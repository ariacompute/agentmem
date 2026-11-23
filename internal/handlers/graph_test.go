package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agentmem/internal/config"
	"agentmem/internal/service"
)

// newGraphServer builds a service whose bundle already carries a two-node graph
// so the read routes have something to answer.
func newGraphServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := newTestServer(t)
	put := func(body string) {
		resp, out := doJSON(t, http.MethodPut, srv.URL+"/v1/graph/node", nil, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("seed put = %d %s", resp.StatusCode, out)
		}
	}
	put(`{"concept_id":"tables/orders","node_type":"table","label":"Orders"}`)
	put(`{"concept_id":"metrics/revenue","node_type":"metric","label":"Revenue","description":"Net revenue","edges":[{"to":"tables/orders","type":"computed_from","evidence":"sources[0]"}],"replace_edges":true}`)
	return srv
}

func TestGraphHTTPFlow(t *testing.T) {
	srv := newGraphServer(t)
	defer srv.Close()

	resp, body := doJSON(t, http.MethodGet, srv.URL+"/v1/graph/node?id=metrics/revenue", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("node = %d %s", resp.StatusCode, body)
	}
	var node map[string]any
	if err := json.Unmarshal([]byte(body), &node); err != nil {
		t.Fatal(err)
	}
	if node["label"] != "Revenue" || node["node_type"] != "metric" {
		t.Fatalf("node = %v", node)
	}
	if out, _ := node["out"].([]any); len(out) != 1 {
		t.Fatalf("out = %v", node["out"])
	}

	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/graph/neighbors?from=metrics/revenue&depth=1&direction=out&edge_types=computed_from", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"nodes"`) {
		t.Fatalf("neighbors = %d %s", resp.StatusCode, body)
	}
	var sub map[string]any
	if err := json.Unmarshal([]byte(body), &sub); err != nil {
		t.Fatal(err)
	}
	if len(sub["nodes"].([]any)) != 2 {
		t.Fatalf("neighbors = %v", sub["nodes"])
	}

	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/graph/path?from=metrics/revenue&to=tables/orders&max_depth=2", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"found":true`) {
		t.Fatalf("path = %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/graph/search?q=revenue&node_types=metric", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "metrics/revenue") {
		t.Fatalf("search = %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/graph", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats = %d %s", resp.StatusCode, body)
	}
	var stats map[string]any
	if err := json.Unmarshal([]byte(body), &stats); err != nil {
		t.Fatal(err)
	}
	if stats["nodes"].(float64) < 2 {
		t.Fatalf("stats = %v", stats)
	}

	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/graph/schema", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "node_types") {
		t.Fatalf("schema = %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, srv.URL+"/v1/graph/answerability",
		nil, `{"question":"what is revenue"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"verdict"`) || !strings.Contains(body, `"gaps"`) {
		t.Fatalf("answerability = %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, srv.URL+"/v1/graph/retrieve", nil, `{"query":"revenue","top_k":5}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "metrics/revenue") {
		t.Fatalf("retrieve = %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, srv.URL+"/v1/graph/edges",
		nil, `{"concept_id":"metrics/revenue","edges":[{"to":"metrics/margin","type":"derived_from"}]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "commit_sha") {
		t.Fatalf("edges = %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodPost, srv.URL+"/v1/graph/index", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "pruned") {
		t.Fatalf("index = %d %s", resp.StatusCode, body)
	}
}

func TestGraphHTTPValidation(t *testing.T) {
	srv := newGraphServer(t)
	defer srv.Close()

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"node without id", http.MethodGet, "/v1/graph/node", "", http.StatusBadRequest},
		{"node missing", http.MethodGet, "/v1/graph/node?id=does/not/exist", "", http.StatusNotFound},
		{"node escaped", http.MethodGet, "/v1/graph/node?id=../escape", "", http.StatusNotFound},
		{"neighbors without from", http.MethodGet, "/v1/graph/neighbors", "", http.StatusBadRequest},
		{"neighbors bad depth", http.MethodGet, "/v1/graph/neighbors?from=tables/orders&depth=9", "", http.StatusBadRequest},
		{"neighbors depth zero", http.MethodGet, "/v1/graph/neighbors?from=tables/orders&depth=0", "", http.StatusBadRequest},
		{"neighbors bad limit", http.MethodGet, "/v1/graph/neighbors?from=tables/orders&limit=0", "", http.StatusBadRequest},
		{"neighbors bad direction", http.MethodGet, "/v1/graph/neighbors?from=tables/orders&direction=up", "", http.StatusBadRequest},
		{"path without to", http.MethodGet, "/v1/graph/path?from=tables/orders", "", http.StatusBadRequest},
		{"search bad limit", http.MethodGet, "/v1/graph/search?q=a&limit=201", "", http.StatusBadRequest},
		{"answerability empty", http.MethodPost, "/v1/graph/answerability", `{"question":""}`, http.StatusBadRequest},
		{"retrieve empty", http.MethodPost, "/v1/graph/retrieve", `{"query":""}`, http.StatusBadRequest},
		{"retrieve bad top_k", http.MethodPost, "/v1/graph/retrieve", `{"query":"x","top_k":101}`, http.StatusBadRequest},
		{"put without concept", http.MethodPut, "/v1/graph/node", `{"node_type":"metric"}`, http.StatusBadRequest},
		{"put empty node type", http.MethodPut, "/v1/graph/node", `{"concept_id":"metrics/x","node_type":" "}`, http.StatusUnprocessableEntity},
		{"put bad edge", http.MethodPut, "/v1/graph/node", `{"concept_id":"metrics/x","edges":[{"to":"metrics/y","type":"bad type"}],"replace_edges":true}`, http.StatusUnprocessableEntity},
		{"edges without edges", http.MethodPost, "/v1/graph/edges", `{"concept_id":"metrics/x"}`, http.StatusBadRequest},
		{"edges invalid", http.MethodPost, "/v1/graph/edges", `{"concept_id":"metrics/x","edges":[{"to":"../escape","type":"t"}]}`, http.StatusUnprocessableEntity},
		{"upload too large", http.MethodPost, "/v1/graph/answerability", strings.Repeat("x", maxRequestBytes+10), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, tc.method, srv.URL+tc.path, nil, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("%s %s = %d (%s), want %d", tc.method, tc.path, resp.StatusCode, body, tc.want)
			}
		})
	}
}

func TestGraphDisabledAnswers404(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Root = filepath.Join(t.TempDir(), "bundle")
	writeHandlerBundle(t, cfg.Git.Root)
	cfg.Graph.Enabled = false
	svc, err := service.Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	srv := httptest.NewServer(NewHandler(svc, openOptions()))
	defer srv.Close()

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/graph"},
		{http.MethodGet, "/v1/graph/node?id=x"},
		{http.MethodGet, "/v1/graph/neighbors?from=x"},
		{http.MethodPost, "/v1/graph/index"},
		{http.MethodPost, "/mcp"},
	} {
		resp, body := doJSON(t, tc.method, srv.URL+tc.path, nil, "")
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "not found") {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, resp.StatusCode, body)
		}
	}
}

func TestMCPProtocol(t *testing.T) {
	srv := newGraphServer(t)
	defer srv.Close()

	call := func(body string) (int, map[string]any) {
		resp, out := doJSON(t, http.MethodPost, srv.URL+"/mcp", nil, body)
		var payload map[string]any
		if out != "" {
			_ = json.Unmarshal([]byte(out), &payload)
		}
		return resp.StatusCode, payload
	}

	status, payload := call(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusOK || payload["result"].(map[string]any)["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("initialize = %d %v", status, payload)
	}
	if _, ok := payload["result"].(map[string]any)["serverInfo"]; !ok {
		t.Fatalf("initialize result = %v", payload)
	}

	status, payload = call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("tools/list = %d", status)
	}
	tools := payload["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("tools = %v", tools)
	}

	for _, tool := range []string{
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_terms","arguments":{"query":"revenue"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_node","arguments":{"concept_id":"metrics/revenue"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"neighbors","arguments":{"concept_id":"metrics/revenue","depth":1}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"path","arguments":{"from":"metrics/revenue","to":"tables/orders"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"check_answerable","arguments":{"question":"what is revenue"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"ping"}`,
	} {
		status, payload = call(tool)
		if status != http.StatusOK || payload["result"] == nil {
			t.Fatalf("call %s = %d %v", tool, status, payload)
		}
	}

	// A tool result is rendered as text content plus structured content.
	status, payload = call(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"get_node","arguments":{"concept_id":"metrics/revenue"}}}`)
	result := payload["result"].(map[string]any)
	if len(result["content"].([]any)) != 1 || result["isError"] != false {
		t.Fatalf("tool content = %v", result)
	}

	cases := []struct {
		name, body string
		want       int
		code       float64
	}{
		{"unknown tool", `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"drop_all","arguments":{}}}`, http.StatusOK, -32602},
		{"bad arguments", `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"get_node","arguments":"nope"}}`, http.StatusOK, -32602},
		{"missing tool name", `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{}}`, http.StatusOK, -32602},
		{"missing node", `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"get_node","arguments":{"concept_id":"nope"}}}`, http.StatusOK, -32002},
		{"unknown method", `{"jsonrpc":"2.0","id":14,"method":"tools/subscribe"}`, http.StatusOK, -32601},
		{"not json", `{`, http.StatusOK, -32700},
		{"wrong version", `{"jsonrpc":"1.0","id":15,"method":"ping"}`, http.StatusOK, -32600},
		{"invalid params", `{"jsonrpc":"2.0","id":16,"method":"tools/call","params":"nope"}`, http.StatusOK, -32602},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := doJSON(t, http.MethodPost, srv.URL+"/mcp", nil, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d (%s)", resp.StatusCode, out)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("non-JSON error body %q", out)
			}
			errBody, ok := payload["error"].(map[string]any)
			if !ok || errBody["code"].(float64) != tc.code {
				t.Fatalf("error = %v", payload["error"])
			}
			if strings.Contains(out, "SQL") || strings.Contains(out, "postgres") {
				t.Fatalf("error body leaked detail: %s", out)
			}
		})
	}

	// Notifications are accepted without a JSON-RPC body.
	resp, _ := doJSON(t, http.MethodPost, srv.URL+"/mcp", nil, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification = %d", resp.StatusCode)
	}
	// An oversized body is rejected before any parsing.
	resp, _ = doJSON(t, http.MethodPost, srv.URL+"/mcp", nil, strings.Repeat("x", maxRequestBytes+10))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d", resp.StatusCode)
	}
	// A non-POST method answers 405.
	resp, _ = doJSON(t, http.MethodGet, srv.URL+"/mcp", nil, "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp = %d", resp.StatusCode)
	}
}
