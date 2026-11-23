package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmem/internal/auth"
	"agentmem/internal/compute"
	"agentmem/internal/graph"
	"agentmem/internal/mcp"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/router"
	"agentmem/internal/service"
)

func testSubject(t *testing.T) auth.Principal {
	t.Helper()
	p, err := auth.DeclaredPrincipal("system", []auth.Permission{
		auth.PermHealthRead, auth.PermKnowledgeRead, auth.PermKnowledgeWrite,
		auth.PermMigrateRun, auth.PermAuditRead, auth.PermComputeRun, auth.PermGraphWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// call runs one handler directly. The principal is attached only when asked, so
// both the authenticated and the "middleware never ran" paths are reachable.
func call(t *testing.T, method, target, body string, svc *service.KnowledgeService, handler http.HandlerFunc, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		req = req.WithContext(auth.WithPrincipal(req.Context(), testSubject(t)))
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// failingGraph injects one error into every read path of the graph port.
type failingGraph struct {
	graph.Query
	err error
}

func (f failingGraph) Node(context.Context, string, string) (graph.NodeView, error) {
	return graph.NodeView{}, f.err
}

func (f failingGraph) Neighbors(context.Context, string, graph.WalkQuery) (graph.SubgraphView, error) {
	return graph.SubgraphView{}, f.err
}

func (f failingGraph) Path(context.Context, string, graph.PathQuery) (graph.PathView, error) {
	return graph.PathView{}, f.err
}

func (f failingGraph) Search(context.Context, string, graph.SearchQuery) ([]graph.NodeHit, error) {
	return nil, f.err
}

func (f failingGraph) Answerability(context.Context, string, string, float64) (graph.Coverage, error) {
	return graph.Coverage{}, f.err
}

func (f failingGraph) Stats(context.Context, string) (meta.GraphStats, error) {
	return meta.GraphStats{}, f.err
}

func (f failingGraph) Schema(context.Context) (okf.GraphSchema, []string, error) {
	return okf.GraphSchema{}, nil, f.err
}

// failingMeta fails the three list calls the handlers expose.
type failingMeta struct {
	meta.MetaStore
	err error
}

func (f failingMeta) ListConcepts(context.Context, int) ([]meta.ConceptMeta, error) {
	return nil, f.err
}

func (f failingMeta) ListDecisions(context.Context, meta.DecisionQuery) ([]meta.DecisionRecord, error) {
	return nil, f.err
}

func (f failingMeta) ListComputations(context.Context, meta.ComputationQuery) ([]meta.ComputationRecord, error) {
	return nil, f.err
}

type failingDecisioner struct{ err error }

func (f failingDecisioner) Decide(context.Context, router.DecisionTask) (router.RouteDecision, error) {
	return router.RouteDecision{}, f.err
}

func TestHandlersRejectMissingPrincipal(t *testing.T) {
	svc := newTestService(t)
	cases := []struct {
		name    string
		method  string
		target  string
		body    string
		handler http.HandlerFunc
	}{
		{"graph stats", http.MethodGet, "/v1/graph", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphStats(w, r, svc)
		}},
		{"graph node", http.MethodGet, "/v1/graph/node?id=x", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphNode(w, r, svc)
		}},
		{"graph neighbors", http.MethodGet, "/v1/graph/neighbors?from=x", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphNeighbors(w, r, svc)
		}},
		{"graph path", http.MethodGet, "/v1/graph/path?from=x&to=y", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphPath(w, r, svc)
		}},
		{"graph search", http.MethodGet, "/v1/graph/search?q=x", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphSearch(w, r, svc)
		}},
		{"graph answerability", http.MethodPost, "/v1/graph/answerability", `{"question":"q"}`, func(w http.ResponseWriter, r *http.Request) {
			handleGraphAnswerability(w, r, svc)
		}},
		{"graph retrieve", http.MethodPost, "/v1/graph/retrieve", `{"query":"q"}`, func(w http.ResponseWriter, r *http.Request) {
			handleGraphRetrieve(w, r, svc)
		}},
		{"graph node write", http.MethodPut, "/v1/graph/node", `{"concept_id":"x"}`, func(w http.ResponseWriter, r *http.Request) {
			handleGraphPutNode(w, r, svc)
		}},
		{"graph edges", http.MethodPost, "/v1/graph/edges", `{"concept_id":"x","edges":[{"to":"y","type":"related"}]}`, func(w http.ResponseWriter, r *http.Request) {
			handleGraphEdges(w, r, svc)
		}},
		{"computation", http.MethodPost, "/v1/computations", `{"concept_id":"computations/revenue"}`, func(w http.ResponseWriter, r *http.Request) {
			handleComputation(w, r, svc)
		}},
		{"query", http.MethodPost, "/v1/query", `{"query":"x"}`, func(w http.ResponseWriter, r *http.Request) {
			handleQuery(w, r, svc)
		}},
		{"put", http.MethodPost, "/v1/knowledge", `{"concept_id":"x","document":{"type":"Concept"}}`, func(w http.ResponseWriter, r *http.Request) {
			handlePut(w, r, svc)
		}},
		{"get", http.MethodGet, "/v1/knowledge?id=x", "", func(w http.ResponseWriter, r *http.Request) {
			handleGet(w, r, svc)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, tc.method, tc.target, tc.body, svc, tc.handler, false)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "authentication required") {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlersRejectBadJSON(t *testing.T) {
	svc := newTestService(t)
	cases := []struct {
		name    string
		method  string
		target  string
		handler http.HandlerFunc
	}{
		{"query", http.MethodPost, "/v1/query", func(w http.ResponseWriter, r *http.Request) {
			handleQuery(w, r, svc)
		}},
		{"answerability", http.MethodPost, "/v1/graph/answerability", func(w http.ResponseWriter, r *http.Request) {
			handleGraphAnswerability(w, r, svc)
		}},
		{"retrieve", http.MethodPost, "/v1/graph/retrieve", func(w http.ResponseWriter, r *http.Request) {
			handleGraphRetrieve(w, r, svc)
		}},
		{"node write", http.MethodPut, "/v1/graph/node", func(w http.ResponseWriter, r *http.Request) {
			handleGraphPutNode(w, r, svc)
		}},
		{"edges", http.MethodPost, "/v1/graph/edges", func(w http.ResponseWriter, r *http.Request) {
			handleGraphEdges(w, r, svc)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, tc.method, tc.target, `{`, svc, tc.handler, true)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid JSON") {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestQueryAndMigrateRejectBadInput(t *testing.T) {
	svc := newTestService(t)
	rec := call(t, http.MethodPost, "/v1/query", `{"query":""}`, svc, func(w http.ResponseWriter, r *http.Request) {
		handleQuery(w, r, svc)
	}, true)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "query is empty") {
		t.Fatalf("empty query = %d %s", rec.Code, rec.Body.String())
	}

	rec = call(t, http.MethodPost, "/v1/query", `{"query":"x","top_k":101}`, svc, func(w http.ResponseWriter, r *http.Request) {
		handleQuery(w, r, svc)
	}, true)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "top_k is out of range") {
		t.Fatalf("top_k = %d %s", rec.Code, rec.Body.String())
	}

	handler := NewHandler(svc, openOptions())
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/migrate", strings.NewReader(`{`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("migrate = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGraphPathRejectsOutOfRangeFlags(t *testing.T) {
	srv := newTestServer(t)
	resp, body := doJSON(t, http.MethodGet, srv.URL+"/v1/graph/path?from=a&to=b&max_depth=99", nil, "")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "out of range") {
		t.Fatalf("max_depth = %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/v1/graph/path?from=a&to=b&direction=sideways", nil, "")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "direction must be out, in or both") {
		t.Fatalf("direction = %d %s", resp.StatusCode, body)
	}
}

func TestGraphFailureMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"disabled", graph.ErrDisabled, http.StatusNotFound, "not found"},
		{"not found", graph.ErrNotFound, http.StatusNotFound, "node not found or access denied"},
		{"invalid", graph.ErrInvalid, http.StatusUnprocessableEntity, "graph request rejected"},
		{"internal", errors.New("boom"), http.StatusInternalServerError, "internal error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			svc.Graph = failingGraph{err: tc.err}
			rec := call(t, http.MethodGet, "/v1/graph", "", svc, func(w http.ResponseWriter, r *http.Request) {
				handleGraphStats(w, r, svc)
			}, true)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestEveryGraphReadSurfaceReportsFailures(t *testing.T) {
	svc := newTestService(t)
	svc.Graph = failingGraph{err: errors.New("boom")}
	cases := []struct {
		name    string
		method  string
		target  string
		body    string
		handler http.HandlerFunc
	}{
		{"node", http.MethodGet, "/v1/graph/node?id=x", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphNode(w, r, svc)
		}},
		{"neighbors", http.MethodGet, "/v1/graph/neighbors?from=x", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphNeighbors(w, r, svc)
		}},
		{"path", http.MethodGet, "/v1/graph/path?from=x&to=y", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphPath(w, r, svc)
		}},
		{"search", http.MethodGet, "/v1/graph/search?q=x", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphSearch(w, r, svc)
		}},
		{"schema", http.MethodGet, "/v1/graph/schema", "", func(w http.ResponseWriter, r *http.Request) {
			handleGraphSchema(w, r, svc)
		}},
		{"answerability", http.MethodPost, "/v1/graph/answerability", `{"question":"q"}`, func(w http.ResponseWriter, r *http.Request) {
			handleGraphAnswerability(w, r, svc)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, tc.method, tc.target, tc.body, svc, tc.handler, true)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// Retrieval degrades to warnings when a ranker fails, so its hard failure is
// only the "graph disabled" guard.
func TestGraphRetrieveReportsADisabledGraph(t *testing.T) {
	svc := newTestService(t)
	svc.Graph = nil
	rec := call(t, http.MethodPost, "/v1/graph/retrieve", `{"query":"q"}`, svc,
		func(w http.ResponseWriter, r *http.Request) { handleGraphRetrieve(w, r, svc) }, true)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGraphWriteAndReindexNeedTheBuiltinBackend(t *testing.T) {
	svc := newTestService(t)
	svc.GraphWriter = nil
	svc.GraphProjector = nil

	rec := call(t, http.MethodPut, "/v1/graph/node", `{"concept_id":"metrics/x","node_type":"metric"}`, svc,
		func(w http.ResponseWriter, r *http.Request) { handleGraphPutNode(w, r, svc) }, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "graph request rejected") {
		t.Fatalf("node write = %d %s", rec.Code, rec.Body.String())
	}

	rec = call(t, http.MethodPost, "/v1/graph/edges", `{"concept_id":"metrics/x","edges":[{"to":"metrics/y","type":"related"}]}`, svc,
		func(w http.ResponseWriter, r *http.Request) { handleGraphEdges(w, r, svc) }, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "graph request rejected") {
		t.Fatalf("edges = %d %s", rec.Code, rec.Body.String())
	}

	rec = call(t, http.MethodPost, "/v1/graph/index", "", svc,
		func(w http.ResponseWriter, r *http.Request) { handleGraphIndex(w, r, svc) }, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "graph request rejected") {
		t.Fatalf("index = %d %s", rec.Code, rec.Body.String())
	}
}

func TestServiceFailureSurfacesAreRedacted(t *testing.T) {
	t.Run("query", func(t *testing.T) {
		svc := newTestService(t)
		svc.Read.Decisioner = failingDecisioner{err: errors.New("driver exploded")}
		rec := httptest.NewRecorder()
		srv := NewHandler(svc, openOptions())
		req := httptest.NewRequest(http.MethodPost, "/v1/query", strings.NewReader(`{"query":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "driver exploded") {
			t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("decisions", func(t *testing.T) {
		svc := newTestService(t)
		svc.Evidence = router.NewEvidence(failingMeta{MetaStore: svc.Store.Meta, err: errors.New("boom")})
		rec := httptest.NewRecorder()
		NewHandler(svc, openOptions()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/decisions", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("decisions = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("concepts", func(t *testing.T) {
		svc := newTestService(t)
		// Listing walks the bundle, so a missing root is the honest failure.
		svc.Store.Bundle = &okf.Bundle{Root: filepath.Join(t.TempDir(), "missing")}
		srv := NewHandler(svc, openOptions())
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/concepts", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("concepts = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("computation audit", func(t *testing.T) {
		svc := newTestService(t)
		svc.Compute.Audit = failingMeta{MetaStore: svc.Store.Meta, err: errors.New("boom")}
		srv := NewHandler(svc, openOptions())
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/computations", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("audit = %d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestComputationAttestationRejectionIsUnprocessable(t *testing.T) {
	svc := newTestService(t)
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"columns":     []string{"revenue"},
			"rows":        [][]any{{1250}},
			"body_digest": strings.Repeat("0", 64),
		})
	}))
	defer sidecar.Close()
	spec := compute.SidecarSpec{Runtime: "bigquery", Bridge: "http", Endpoint: sidecar.URL}
	if err := svc.Compute.Runtimes.Configure([]compute.SidecarSpec{spec}, []string{"127.0.0.1"}, nil); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/computations",
		strings.NewReader(`{"concept_id":"computations/bigquery","parameters":{"year":2026}}`))
	req.Header.Set("Content-Type", "application/json")
	NewHandler(svc, openOptions()).ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %s: %v", rec.Body.String(), err)
	}
	if rec.Code != http.StatusUnprocessableEntity || body["display_allowed"] != false || body["display_value"] != nil {
		t.Fatalf("response = %d %v", rec.Code, body)
	}
}

func TestAuditLimitDefaultsWhenAbsent(t *testing.T) {
	srv := newTestServer(t)
	resp, body := doJSON(t, http.MethodGet, srv.URL+"/v1/decisions", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decisions without limit = %d %s", resp.StatusCode, body)
	}
}

func TestLogThrottleReportsSuppressedCounts(t *testing.T) {
	throttle := &logThrottle{lastSecond: time.Now().Unix() - 1, suppressed: 3}
	throttle.Warn("authentication failed", http.MethodGet, "/v1/knowledge")
	if throttle.suppressed != 0 || throttle.lastSecond != time.Now().Unix() {
		t.Fatalf("throttle = %+v", throttle)
	}
}

// --- MCP transport -------------------------------------------------------

func TestMCPPostRejectsUnreadableBody(t *testing.T) {
	transport := newMCPTransport(newTestService(t), mcp.Config{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", io.NopCloser(errReader{}))
	rec := httptest.NewRecorder()
	transport.post(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("body refused") }

func TestMCPPostRequiresPrincipal(t *testing.T) {
	transport := newMCPTransport(newTestService(t), mcp.Config{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	rec := httptest.NewRecorder()
	transport.post(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMCPDeleteUnknownSession(t *testing.T) {
	transport := newMCPTransport(newTestService(t), mcp.Config{})
	rec := httptest.NewRecorder()
	transport.close(rec, httptest.NewRequest(http.MethodDelete, "/mcp", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "session not found") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

// scriptedWriter is a minimal SSE destination: it can fail the opening flush,
// the ping write or the ping flush on demand.
type scriptedWriter struct {
	header    http.Header
	flushes   int
	failOpen  bool
	failWrite bool
	failPing  bool
	onFlush   func()
}

func (w *scriptedWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *scriptedWriter) Write(p []byte) (int, error) {
	if w.failWrite {
		return 0, errors.New("write refused")
	}
	return len(p), nil
}

func (w *scriptedWriter) WriteHeader(int) {}

func (w *scriptedWriter) FlushError() error {
	w.flushes++
	if w.onFlush != nil {
		w.onFlush()
	}
	if w.flushes == 1 && w.failOpen {
		return errors.New("flush refused")
	}
	if w.flushes > 1 && w.failPing {
		return errors.New("ping flush refused")
	}
	return nil
}

func startedStream(t *testing.T, transport *mcpTransport) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcp.Version20250618 + `"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), testSubject(t)))
	transport.post(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize = %d %s", rec.Code, rec.Body.String())
	}
	return rec.Header().Get(mcpSessionHeader)
}

func TestMCPStreamOpeningFlushFailure(t *testing.T) {
	transport := newMCPTransport(newTestService(t), mcp.Config{})
	id := startedStream(t, transport)
	writer := &scriptedWriter{failOpen: true}
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(mcpSessionHeader, id)
	transport.stream(writer, req)
	if transport.hasStream(id) {
		t.Fatal("a stream that cannot flush must not register")
	}
}

func (t *mcpTransport) hasStream(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.streams[id]
	return ok
}

func TestMCPStreamPingFailures(t *testing.T) {
	previousPing, previousLife := mcpSSEPingPeriod, mcpSSEMaxLifetime
	mcpSSEPingPeriod, mcpSSEMaxLifetime = time.Millisecond, time.Minute
	t.Cleanup(func() { mcpSSEPingPeriod, mcpSSEMaxLifetime = previousPing, previousLife })

	for _, tc := range []struct {
		name   string
		writer *scriptedWriter
	}{
		{"write", &scriptedWriter{failWrite: true}},
		{"flush", &scriptedWriter{failPing: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := newMCPTransport(newTestService(t), mcp.Config{})
			id := startedStream(t, transport)
			req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
			req.Header.Set(mcpSessionHeader, id)
			done := make(chan struct{})
			go func() {
				transport.stream(tc.writer, req)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("the stream did not stop after the ping failure")
			}
		})
	}
}

func TestMCPStreamLifetimeAndClose(t *testing.T) {
	previousPing, previousLife := mcpSSEPingPeriod, mcpSSEMaxLifetime
	mcpSSEPingPeriod, mcpSSEMaxLifetime = time.Hour, 5*time.Millisecond
	t.Cleanup(func() { mcpSSEPingPeriod, mcpSSEMaxLifetime = previousPing, previousLife })

	transport := newMCPTransport(newTestService(t), mcp.Config{})
	id := startedStream(t, transport)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(mcpSessionHeader, id)
	done := make(chan struct{})
	go func() {
		transport.stream(&scriptedWriter{}, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream outlived its maximum lifetime")
	}

	mcpSSEMaxLifetime = time.Hour
	id = startedStream(t, transport)
	req = httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(mcpSessionHeader, id)
	done = make(chan struct{})
	go func() {
		transport.stream(&scriptedWriter{}, req)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !transport.hasStream(id) {
		if time.Now().After(deadline) {
			t.Fatal("the stream never registered")
		}
		time.Sleep(time.Millisecond)
	}
	transport.closeStream(id)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("closeStream did not stop the stream")
	}
}
