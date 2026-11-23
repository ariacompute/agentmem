package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentmem/internal/mcp"
)

// initializeMCP performs the handshake and returns the issued session id.
func initializeMCP(t *testing.T, base string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcp.Version20250618 + `"}}`
	resp, out := doJSON(t, http.MethodPost, base+"/mcp", headers, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize = %d %s", resp.StatusCode, out)
	}
	session := resp.Header.Get(mcpSessionHeader)
	if session == "" {
		t.Fatalf("no session header: %v", resp.Header)
	}
	return resp, session
}

func TestMCPStreamableHTTPSession(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// An SSE-preferring client receives the initialize response as one event.
	resp, session := initializeMCP(t, srv.URL, map[string]string{"Accept": "application/json, text/event-stream"})
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type = %q", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get(mcpVersionHeader) != mcp.Version20250618 {
		t.Fatalf("version header = %q", resp.Header.Get(mcpVersionHeader))
	}

	// A subsequent call with the session answers plain JSON.
	resp2, out := doJSON(t, http.MethodPost, srv.URL+"/mcp",
		map[string]string{mcpSessionHeader: session, mcpVersionHeader: mcp.Version20250618},
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("tools/list = %d %s", resp2.StatusCode, out)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("body %q: %v", out, err)
	}
	if payload["result"] == nil {
		t.Fatalf("payload = %v", payload)
	}

	// The event stream stays open until the client goes away; the headers and
	// opening flush arrive first, which is what a client depends on.
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(mcpSessionHeader, session)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if stream.StatusCode != http.StatusOK || !strings.HasPrefix(stream.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream = %d %q", stream.StatusCode, stream.Header.Get("Content-Type"))
	}
	cancel()
	_ = stream.Body.Close()

	// DELETE ends the session and the next call is rejected.
	resp3, out := doJSON(t, http.MethodDelete, srv.URL+"/mcp", map[string]string{mcpSessionHeader: session}, "")
	if resp3.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d %s", resp3.StatusCode, out)
	}
	resp4, out := doJSON(t, http.MethodPost, srv.URL+"/mcp",
		map[string]string{mcpSessionHeader: session}, `{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if resp4.StatusCode != http.StatusNotFound || !strings.Contains(out, "session not found") {
		t.Fatalf("post after delete = %d %s", resp4.StatusCode, out)
	}
}

func TestMCPEventStreamHeartbeatAndClose(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	_, session := initializeMCP(t, srv.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(mcpSessionHeader, session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// The stream is still open: no keep-alive ping is due within the window,
	// and nothing but a comment frame may ever be written.
	buf := make([]byte, 16)
	n, readErr := resp.Body.Read(buf)
	if n > 0 && !strings.HasPrefix(string(buf[:n]), ":") {
		t.Fatalf("unexpected frame %q", buf[:n])
	}
	if readErr == nil {
		t.Fatal("stream must not end before the client cancels it")
	}
}

func TestMCPRejectsUnknownSessionAndVersion(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, out := doJSON(t, http.MethodPost, srv.URL+"/mcp",
		map[string]string{mcpSessionHeader: "nope"}, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(out, "-32004") {
		t.Fatalf("unknown session = %d %s", resp.StatusCode, out)
	}

	resp, out = doJSON(t, http.MethodPost, srv.URL+"/mcp",
		map[string]string{mcpVersionHeader: "1999-01-01"}, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(out, "-32600") {
		t.Fatalf("bad version = %d %s", resp.StatusCode, out)
	}

	// A stateful revision announced by the header without a session is refused.
	resp, out = doJSON(t, http.MethodPost, srv.URL+"/mcp",
		map[string]string{mcpVersionHeader: mcp.Version20250618}, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing session = %d %s", resp.StatusCode, out)
	}
}

func TestMCPBatchOverSSE(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	_, session := initializeMCP(t, srv.URL, nil)

	resp, out := doJSON(t, http.MethodPost, srv.URL+"/mcp",
		map[string]string{mcpSessionHeader: session, "Accept": "text/event-stream"},
		`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("batch = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(out, "event: message") {
		t.Fatalf("event body = %q", out)
	}
	data := out[strings.Index(out, "data: ")+len("data: "):]
	var responses []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &responses); err != nil || len(responses) != 2 {
		t.Fatalf("batch payload %q: %v", data, err)
	}
}

func TestMCPWriteToolsFollowTheSwitch(t *testing.T) {
	readOnly := Options{
		Auth: mustAuthUnchecked(nil, false), LocalSubject: "system",
		RateRPS: 100000, RateBurst: 100000,
	}
	srv := newTestServerWithOptions(t, readOnly)
	defer srv.Close()
	_, session := initializeMCP(t, srv.URL, nil)
	_, out := doJSON(t, http.MethodPost, srv.URL+"/mcp",
		map[string]string{mcpSessionHeader: session}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if strings.Contains(out, "put_knowledge") {
		t.Fatalf("write tool leaked: %s", out)
	}

	enabled := readOnly
	enabled.MCP = mcp.Config{Write: true}
	srv2 := newTestServerWithOptions(t, enabled)
	defer srv2.Close()
	_, session2 := initializeMCP(t, srv2.URL, nil)
	_, out = doJSON(t, http.MethodPost, srv2.URL+"/mcp",
		map[string]string{mcpSessionHeader: session2}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !strings.Contains(out, "put_knowledge") {
		t.Fatalf("write tool missing: %s", out)
	}
}

func TestMCPTransportRegistry(t *testing.T) {
	transport := newMCPTransport(newTestService(t), mcp.Config{})
	first := transport.register("session")
	second := transport.register("session")
	select {
	case <-first:
	default:
		t.Fatal("a replaced stream must be closed")
	}
	// Unregistering a superseded stream must not disturb the current one.
	transport.unregister("session", first)
	transport.closeStream("session")
	select {
	case <-second:
	default:
		t.Fatal("closeStream must close the active stream")
	}
	transport.closeStream("session")
	transport.unregister("session", second)
	transport.unregister("unknown", second)
}

func TestWriteMCPFailureUsesFixedStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	writeMCPFailure(rec, &mcp.Failure{Code: mcp.CodeSession, Message: "session not found"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "-32004") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestMCPUnsupportedMethod(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	resp, out := doJSON(t, http.MethodPatch, srv.URL+"/mcp", nil, "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("patch = %d %s", resp.StatusCode, out)
	}
	if allow := resp.Header.Get("Allow"); allow != "GET, POST, DELETE" {
		t.Fatalf("allow = %q", allow)
	}
	if !strings.Contains(out, "method not allowed") {
		t.Fatalf("body = %s", out)
	}
}
