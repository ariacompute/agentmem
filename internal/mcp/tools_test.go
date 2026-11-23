package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentmem/internal/auth"
	"agentmem/internal/compute"
	"agentmem/internal/graph"
	"agentmem/internal/okf"
)

func toolNames(t *testing.T, server *Server) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, tool := range server.listTools() {
		out[tool["name"].(string)] = tool
	}
	return out
}

func callTool(t *testing.T, server *Server, principal auth.Principal, name, args string) Response {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	out, failure := server.Handle(context.Background(), principal, []byte(body), Options{})
	if failure != nil {
		t.Fatalf("%s: transport failure %+v", name, failure)
	}
	var response Response
	if err := json.Unmarshal(out.Payload, &response); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return response
}

func TestToolsListReadOnlyByDefault(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	tools := toolNames(t, server)
	if len(tools) != 13 {
		t.Fatalf("read tools = %d (%v)", len(tools), tools)
	}
	for _, name := range []string{
		"search_terms", "get_node", "neighbors", "path", "check_answerable",
		"graph_schema", "graph_stats", "graph_retrieve", "query",
		"list_concepts", "read_knowledge", "list_decisions", "list_computations",
	} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("missing read tool %q", name)
		}
	}
	for _, name := range []string{
		"put_knowledge", "put_graph_node", "append_graph_edge", "run_computation", "rebuild_graph_index",
	} {
		if _, ok := tools[name]; ok {
			t.Fatalf("write tool %q must be hidden by default", name)
		}
	}
	node := tools["get_node"]
	if node["outputSchema"] == nil || node["inputSchema"] == nil {
		t.Fatalf("schemas missing: %v", node)
	}
	annotations, _ := node["annotations"].(map[string]any)
	if annotations["readOnlyHint"] != true {
		t.Fatalf("annotations = %v", node["annotations"])
	}
}

func TestToolsListIncludesWritesWhenEnabled(t *testing.T) {
	server, _ := newTestServer(t, Config{Write: true})
	tools := toolNames(t, server)
	if len(tools) != 18 {
		t.Fatalf("tools = %d", len(tools))
	}
	for _, name := range []string{
		"put_knowledge", "put_graph_node", "append_graph_edge", "run_computation", "rebuild_graph_index",
	} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("missing write tool %q", name)
		}
		annotations, _ := tool["annotations"].(map[string]any)
		if annotations["readOnlyHint"] != false {
			t.Fatalf("%s annotations = %v", name, tool["annotations"])
		}
	}
}

func TestToolScopeEnforcedBeforeBackend(t *testing.T) {
	server, backend := newTestServer(t, Config{Write: true})
	reader := readerPrincipal(t)

	response := callTool(t, server, reader, "put_knowledge", `{"concept_id":"x","document":{"type":"Concept"}}`)
	if response.Error == nil || response.Error.Code != CodeForbidden {
		t.Fatalf("write without scope = %+v", response)
	}
	if backend.putCalls != 0 {
		t.Fatal("a forbidden write must never reach the backend")
	}

	// The audit scope is separate from the read scope.
	response = callTool(t, server, reader, "list_decisions", `{}`)
	if response.Error == nil || response.Error.Code != CodeForbidden {
		t.Fatalf("audit without scope = %+v", response)
	}
}

func TestHiddenWriteToolLooksUnknown(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	response := callTool(t, server, fullPrincipal(t), "rebuild_graph_index", `{}`)
	if response.Error == nil || response.Error.Code != CodeInvalidParams || response.Error.Message != "unknown tool" {
		t.Fatalf("hidden write = %+v", response)
	}
}

func TestToolArgumentValidation(t *testing.T) {
	server, _ := newTestServer(t, Config{Write: true})
	principal := fullPrincipal(t)
	cases := []struct {
		name string
		args string
	}{
		{"get_node", `{}`},
		{"neighbors", `{"concept_id":"x","direction":"sideways"}`},
		{"neighbors", `{"concept_id":"x","depth":99}`},
		{"path", `{"from":"a"}`},
		{"check_answerable", `{}`},
		{"search_terms", `{"query":"x","limit":100000}`},
		{"graph_retrieve", `{"query":"x","top_k":101}`},
		{"query", `{}`},
		{"read_knowledge", `{}`},
		{"list_decisions", `{"limit":5000}`},
		{"put_knowledge", `{"concept_id":"x"}`},
		{"append_graph_edge", `{"concept_id":"x","edges":[]}`},
		{"run_computation", `{}`},
	}
	for _, tc := range cases {
		response := callTool(t, server, principal, tc.name, tc.args)
		if response.Error == nil || response.Error.Code != CodeInvalidParams {
			t.Fatalf("%s %s = %+v", tc.name, tc.args, response)
		}
	}

	// Arguments that do not decode are rejected rather than ignored.
	for _, name := range []string{"get_node", "search_terms", "put_knowledge"} {
		response := callTool(t, server, principal, name, `"nope"`)
		if response.Error == nil || response.Error.Code != CodeInvalidParams {
			t.Fatalf("%s bad arguments = %+v", name, response)
		}
	}
}

func TestToolStructuredContent(t *testing.T) {
	server, backend := newTestServer(t, Config{})
	backend.node = graph.NodeView{ConceptID: "metrics/revenue", NodeType: "metric", Label: "Revenue"}
	response := callTool(t, server, readerPrincipal(t), "get_node", `{"concept_id":"metrics/revenue"}`)
	if response.Error != nil {
		t.Fatalf("get_node = %+v", response.Error)
	}
	result, ok := response.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %T", response.Result)
	}
	if result["isError"] != false || result["structuredContent"] == nil {
		t.Fatalf("result = %v", result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %v", result["content"])
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" || !strings.Contains(block["text"].(string), "metrics/revenue") {
		t.Fatalf("content block = %v", block)
	}
}

func TestSearchTermsWrapsArray(t *testing.T) {
	server, backend := newTestServer(t, Config{})
	backend.hits = []graph.NodeHit{{ConceptID: "a", Label: "A"}}
	response := callTool(t, server, readerPrincipal(t), "search_terms", `{"query":"a"}`)
	result := response.Result.(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	nodes, ok := structured["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("structured = %v", structured)
	}
}

func TestGraphErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{graph.ErrNotFound, CodeNotFound},
		{graph.ErrInvalid, CodeInvalidParams},
		{graph.ErrDisabled, CodeMethodNotFound},
		{errors.New("boom"), CodeInternal},
	}
	for _, tc := range cases {
		server, backend := newTestServer(t, Config{})
		backend.nodeErr = tc.err
		response := callTool(t, server, readerPrincipal(t), "get_node", `{"concept_id":"x"}`)
		if response.Error == nil || response.Error.Code != tc.code {
			t.Fatalf("%v = %+v", tc.err, response)
		}
	}
}

func TestReadKnowledgeHidesExistence(t *testing.T) {
	server, backend := newTestServer(t, Config{})
	backend.docErr = errors.New("denied")
	response := callTool(t, server, readerPrincipal(t), "read_knowledge", `{"concept_id":"private/x"}`)
	if response.Error == nil || response.Error.Code != CodeNotFound ||
		response.Error.Message != "document not found or access denied" {
		t.Fatalf("read_knowledge = %+v", response)
	}

	backend.docErr = nil
	backend.doc = &okf.Document{Type: "Concept", Title: "T", Body: "B"}
	response = callTool(t, server, readerPrincipal(t), "read_knowledge", `{"concept_id":"okf/x"}`)
	structured := response.Result.(map[string]any)["structuredContent"].(map[string]any)
	if structured["title"] != "T" || structured["body"] != "B" {
		t.Fatalf("structured = %v", structured)
	}
}

func TestWriteToolsReachTheBackendWithTheTrustedSubject(t *testing.T) {
	server, backend := newTestServer(t, Config{Write: true})
	principal := fullPrincipal(t)

	response := callTool(t, server, principal, "put_knowledge",
		`{"concept_id":"x","document":{"type":"Concept","title":"T","visibility":"public","owner":"admin"}}`)
	if response.Error != nil {
		t.Fatalf("put_knowledge = %+v", response.Error)
	}
	if backend.putCalls != 1 || backend.putSubject != "admin" || backend.putID != "x" {
		t.Fatalf("backend call = %+v", backend)
	}
	if backend.putDoc.Type != "Concept" || backend.putDoc.Visibility() != "public" {
		t.Fatalf("document = %+v", backend.putDoc)
	}

	response = callTool(t, server, principal, "put_graph_node", `{"concept_id":"x","label":"L","replace_edges":true}`)
	if response.Error != nil {
		t.Fatalf("put_graph_node = %+v", response.Error)
	}
	structured := response.Result.(map[string]any)["structuredContent"].(map[string]any)
	if structured["warnings"] == nil {
		t.Fatalf("warnings missing: %v", structured)
	}

	if response = callTool(t, server, principal, "append_graph_edge",
		`{"concept_id":"x","edges":[{"to":"y","type":"computed_from"}]}`); response.Error != nil {
		t.Fatalf("append_graph_edge = %+v", response.Error)
	}
	if backend.edgesCalls != 1 {
		t.Fatal("append_graph_edge did not call the backend")
	}

	if response = callTool(t, server, principal, "rebuild_graph_index", `{}`); response.Error != nil {
		t.Fatalf("rebuild_graph_index = %+v", response.Error)
	}
	if backend.rebuildCalls != 1 {
		t.Fatal("rebuild_graph_index did not call the backend")
	}
}

func TestPutRejectionIsRedacted(t *testing.T) {
	server, backend := newTestServer(t, Config{Write: true})
	backend.putErr = errors.New("secret sql parameter")
	response := callTool(t, server, fullPrincipal(t), "put_knowledge",
		`{"concept_id":"x","document":{"type":"Concept"}}`)
	if response.Error == nil || response.Error.Code != CodeInvalidParams || response.Error.Message != "write rejected" {
		t.Fatalf("put rejection = %+v", response)
	}
}

func TestComputationRejectionIsAToolResult(t *testing.T) {
	server, backend := newTestServer(t, Config{Write: true})
	backend.computeErr = errors.New("driver exploded")
	backend.envelope = compute.Envelope{ConceptID: "ac/1", Verdict: compute.Verdict{Passed: true}}
	response := callTool(t, server, fullPrincipal(t), "run_computation", `{"concept_id":"ac/1"}`)
	if response.Error != nil {
		t.Fatalf("must be a tool result, got %+v", response.Error)
	}
	result := response.Result.(map[string]any)
	if result["isError"] != true {
		t.Fatalf("result = %v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["envelope"] == nil {
		t.Fatalf("envelope missing: %v", structured)
	}
	if strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "driver exploded") {
		t.Fatal("tool result leaked the driver error")
	}
}

func TestAuditToolsReturnArrays(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := fullPrincipal(t)
	for _, name := range []string{"list_decisions", "list_computations"} {
		response := callTool(t, server, principal, name, `{}`)
		if response.Error != nil {
			t.Fatalf("%s = %+v", name, response.Error)
		}
		structured := response.Result.(map[string]any)["structuredContent"].(map[string]any)
		if len(structured) != 1 {
			t.Fatalf("%s = %v", name, structured)
		}
		for _, value := range structured {
			if _, ok := value.([]any); !ok {
				t.Fatalf("%s value = %T", name, value)
			}
		}
	}
}

func TestToolListAndStats(t *testing.T) {
	server, backend := newTestServer(t, Config{})
	backend.concepts = []string{"a", "b"}
	response := callTool(t, server, readerPrincipal(t), "list_concepts", `{}`)
	structured := response.Result.(map[string]any)["structuredContent"].(map[string]any)
	if concepts, _ := structured["concepts"].([]any); len(concepts) != 2 {
		t.Fatalf("concepts = %v", structured)
	}

	for _, name := range []string{"graph_stats", "graph_schema", "path", "neighbors", "check_answerable", "graph_retrieve", "query"} {
		args := `{}`
		switch name {
		case "path":
			args = `{"from":"a","to":"b"}`
		case "neighbors":
			args = `{"concept_id":"a"}`
		case "check_answerable":
			args = `{"question":"q"}`
		case "graph_retrieve", "query":
			args = `{"query":"q"}`
		}
		if response := callTool(t, server, readerPrincipal(t), name, args); response.Error != nil {
			t.Fatalf("%s = %+v", name, response.Error)
		}
	}
}
