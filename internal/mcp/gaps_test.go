package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agentmem/internal/compute"
	"agentmem/internal/graph"
)

// validArguments is the smallest payload each tool accepts, so a failure can
// only come from the backend or the scope check.
var validArguments = map[string]string{
	"search_terms":        `{"query":"q","limit":5}`,
	"get_node":            `{"concept_id":"x"}`,
	"neighbors":           `{"concept_id":"x","direction":"out","limit":5}`,
	"path":                `{"from":"a","to":"b","max_depth":2,"direction":"in"}`,
	"check_answerable":    `{"question":"q"}`,
	"graph_schema":        `{}`,
	"graph_stats":         `{}`,
	"graph_retrieve":      `{"query":"q","top_k":5}`,
	"query":               `{"query":"q","top_k":5}`,
	"list_concepts":       `{}`,
	"read_knowledge":      `{"concept_id":"x"}`,
	"list_decisions":      `{"limit":5}`,
	"list_computations":   `{"limit":5}`,
	"put_knowledge":       `{"concept_id":"x","document":{"type":"Concept"}}`,
	"put_graph_node":      `{"concept_id":"x","node_type":"Concept","description":"d"}`,
	"append_graph_edge":   `{"concept_id":"x","edges":[{"to":"y","type":"related"}]}`,
	"run_computation":     `{"concept_id":"ac/1"}`,
	"rebuild_graph_index": `{}`,
}

func TestEveryToolFailsClosedOnBackendError(t *testing.T) {
	for name, args := range validArguments {
		t.Run(name, func(t *testing.T) {
			server, backend := newTestServer(t, Config{Write: true})
			backend.err = errors.New("backend failed")
			backend.computeErr = errors.New("backend failed")
			backend.putErr = errors.New("backend failed")
			backend.putNodeErr = errors.New("backend failed")
			backend.edgesErr = errors.New("backend failed")
			backend.rebuildErr = errors.New("backend failed")
			response := callTool(t, server, fullPrincipal(t), name, args)
			switch name {
			case "run_computation":
				// A rejected run is a tool result carrying the verdict, never a
				// protocol-level error.
				result, _ := response.Result.(map[string]any)
				if result == nil || result["isError"] != true {
					t.Fatalf("result = %+v", response)
				}
			case "read_knowledge":
				// Missing and forbidden are one indistinguishable answer.
				if response.Error == nil || response.Error.Code != CodeNotFound {
					t.Fatalf("error = %+v", response.Error)
				}
			case "put_knowledge":
				if response.Error == nil || response.Error.Code != CodeInvalidParams || response.Error.Message != "write rejected" {
					t.Fatalf("error = %+v", response.Error)
				}
			default:
				if response.Error == nil || response.Error.Code != CodeInternal || response.Error.Message != "internal error" {
					t.Fatalf("error = %+v", response.Error)
				}
			}
		})
	}
}

func TestEveryToolRejectsUndecodableArguments(t *testing.T) {
	server, _ := newTestServer(t, Config{Write: true})
	// Tools that take no arguments never decode, so they cannot reject a body.
	for _, name := range []string{"search_terms", "get_node", "neighbors", "path", "check_answerable",
		"graph_retrieve", "query", "read_knowledge", "list_decisions", "list_computations",
		"put_knowledge", "put_graph_node", "append_graph_edge", "run_computation"} {
		t.Run(name, func(t *testing.T) {
			response := callTool(t, server, fullPrincipal(t), name, `"nope"`)
			if response.Error == nil || response.Error.Code != CodeInvalidParams || response.Error.Message != "invalid arguments" {
				t.Fatalf("error = %+v", response.Error)
			}
		})
	}
}

func TestToolCallWithoutArgumentsUsesEmptyObject(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_node"}}`
	out, failure := server.Handle(context.Background(), readerPrincipal(t), []byte(body), Options{})
	if failure != nil {
		t.Fatal(failure)
	}
	var response Response
	if err := json.Unmarshal(out.Payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Message != "concept_id is required" {
		t.Fatalf("error = %+v", response.Error)
	}
}

func TestCallToolRejectsMalformedParams(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	for _, params := range []string{`{}`, `"nope"`, `{"name":""}`} {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`
		out, failure := server.Handle(context.Background(), readerPrincipal(t), []byte(body), Options{})
		if failure != nil {
			t.Fatal(failure)
		}
		var response Response
		if err := json.Unmarshal(out.Payload, &response); err != nil {
			t.Fatal(err)
		}
		if response.Error == nil || response.Error.Code != CodeInvalidParams || response.Error.Message != "invalid params" {
			t.Fatalf("%s: error = %+v", params, response.Error)
		}
	}
}

func TestCancelledToolCallIsReported(t *testing.T) {
	server, backend := newTestServer(t, Config{})
	backend.err = context.Canceled
	response := callTool(t, server, readerPrincipal(t), "graph_stats", `{}`)
	if response.Error == nil || response.Error.Code != CodeInternal || response.Error.Message != "request cancelled" {
		t.Fatalf("error = %+v", response.Error)
	}
}

func TestListConceptsAnswersAnEmptyArray(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	response := callTool(t, server, readerPrincipal(t), "list_concepts", `{}`)
	if response.Error != nil {
		t.Fatalf("error = %+v", response.Error)
	}
	structured := response.Result.(map[string]any)["structuredContent"].(map[string]any)
	concepts, ok := structured["concepts"].([]any)
	if !ok || len(concepts) != 0 {
		t.Fatalf("concepts = %#v", structured["concepts"])
	}
}

func TestGraphWriteFailuresAreMapped(t *testing.T) {
	cases := []struct {
		name  string
		write func(*stubBackend, error)
		err   error
		tool  string
		args  string
		code  int
	}{
		{"node not found", func(b *stubBackend, err error) { b.putNodeErr = err }, graph.ErrNotFound, "put_graph_node", `{"concept_id":"x"}`, CodeNotFound},
		{"edge rejected", func(b *stubBackend, err error) { b.edgesErr = err }, graph.ErrInvalid, "append_graph_edge", `{"concept_id":"x","edges":[{"to":"y","type":"related"}]}`, CodeInvalidParams},
		{"reindex rejected", func(b *stubBackend, err error) { b.rebuildErr = err }, graph.ErrInvalid, "rebuild_graph_index", `{}`, CodeInvalidParams},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, backend := newTestServer(t, Config{Write: true})
			tc.write(backend, tc.err)
			response := callTool(t, server, fullPrincipal(t), tc.tool, tc.args)
			if response.Error == nil || response.Error.Code != tc.code {
				t.Fatalf("error = %+v", response.Error)
			}
		})
	}
}

func TestRunComputationSuccessCarriesTheEnvelope(t *testing.T) {
	server, backend := newTestServer(t, Config{Write: true})
	backend.envelope = compute.Envelope{ConceptID: "ac/1", DisplayAllowed: true, DisplayValue: "42"}
	response := callTool(t, server, fullPrincipal(t), "run_computation", `{"concept_id":"ac/1"}`)
	if response.Error != nil {
		t.Fatalf("error = %+v", response.Error)
	}
	result := response.Result.(map[string]any)
	if result["isError"] != false {
		t.Fatalf("result = %+v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["display_value"] != "42" {
		t.Fatalf("structured = %+v", structured)
	}
}

func TestProtocolFallbacks(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := readerPrincipal(t)

	// A single non-object payload is a parse error, not a panic.
	out, failure := server.Handle(context.Background(), principal, []byte(`7`), Options{})
	if failure == nil || failure.Code != CodeParse || failure.Status != 400 {
		t.Fatalf("scalar payload = %+v (out %s)", failure, out.Payload)
	}

	// The header wins over the params revision.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + Version20250326 + `"}}`
	headerOut, failure := server.Handle(context.Background(), principal, []byte(body), Options{Version: Version20250618})
	if failure != nil {
		t.Fatal(failure)
	}
	if result := decodeResult(t, headerOut); result["protocolVersion"] != Version20250618 {
		t.Fatalf("version = %v", result["protocolVersion"])
	}

	// Absent and empty params both negotiate the newest revision.
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":2,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":""}}`,
	} {
		out, failure := server.Handle(context.Background(), principal, []byte(payload), Options{Stateless: true})
		if failure != nil {
			t.Fatal(failure)
		}
		if result := decodeResult(t, out); result["protocolVersion"] != LatestVersion() {
			t.Fatalf("%s = %v", payload, result["protocolVersion"])
		}
	}
}

func TestBatchParseAndFailurePaths(t *testing.T) {
	server, _ := newTestServer(t, Config{})
	principal := readerPrincipal(t)

	if _, failure := server.Handle(context.Background(), principal, []byte(`[`), Options{}); failure == nil ||
		failure.Code != CodeParse || failure.Status != 400 {
		t.Fatalf("broken batch = %+v", failure)
	}

	// A stateful revision announced by the header without a session fails the
	// whole batch before any element is answered.
	body := `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`
	_, failure := server.Handle(context.Background(), principal, []byte(body), Options{Version: Version20250618})
	if failure == nil || failure.Code != CodeSession || failure.Status != 404 {
		t.Fatalf("batch without session = %+v", failure)
	}
}

func TestToolSpecificArgumentBounds(t *testing.T) {
	server, _ := newTestServer(t, Config{Write: true})
	principal := fullPrincipal(t)
	cases := []struct {
		tool string
		args string
		want string
	}{
		{"neighbors", `{}`, "concept_id is required"},
		{"neighbors", `{"concept_id":"x","limit":1000}`, "limit is out of range"},
		{"neighbors", `{"concept_id":"x","depth":99}`, "depth is out of range"},
		{"path", `{"from":"a","to":"b","max_depth":99}`, "depth is out of range"},
		{"path", `{"from":"a","to":"b","direction":"sideways"}`, "direction must be out, in or both"},
		{"query", `{"query":"  "}`, "query is empty"},
		{"query", `{"query":"q","top_k":101}`, "top_k is out of range"},
		{"graph_retrieve", `{"query":"  "}`, "query is empty"},
		{"graph_retrieve", `{"query":"q","top_k":101}`, "top_k is out of range"},
		{"list_computations", `{"limit":5000}`, "limit is out of range"},
		{"put_graph_node", `{"concept_id":"  "}`, "concept_id is required"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.want, func(t *testing.T) {
			response := callTool(t, server, principal, tc.tool, tc.args)
			if response.Error == nil || response.Error.Code != CodeInvalidParams || response.Error.Message != tc.want {
				t.Fatalf("%s: error = %+v", tc.args, response.Error)
			}
		})
	}
}

func TestRandomIDReportsEntropyFailure(t *testing.T) {
	previous := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	t.Cleanup(func() { randRead = previous })

	if _, err := randomID(); err == nil {
		t.Fatal("a failed entropy read must surface")
	}
}
