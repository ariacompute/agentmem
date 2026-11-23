package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentmem/internal/auth"
	"agentmem/internal/mcp"
	"agentmem/internal/service"
)

// mcpTransport is the HTTP binding of the MCP server. It owns only transport
// concerns: the JSON-RPC payload, session headers and the server-sent event
// stream. Tool authorisation and protocol negotiation live in internal/mcp, so
// the stdio transport behaves identically.

const (
	mcpSessionHeader  = "Mcp-Session-Id"
	mcpVersionHeader  = "MCP-Protocol-Version"
	mcpSSEPingPeriod  = 15 * time.Second
	mcpSSEMaxLifetime = 5 * time.Minute
)

func newMCPTransport(svc *service.KnowledgeService, cfg mcp.Config) *mcpTransport {
	return &mcpTransport{
		srv:     mcp.NewServer(service.NewMCPBackend(svc), cfg),
		streams: make(map[string]chan struct{}),
	}
}

type mcpTransport struct {
	srv *mcp.Server

	mu      sync.Mutex
	streams map[string]chan struct{}
}

// serve dispatches one /mcp request. A disabled graph feature answers JSON 404
// before any method handling, exactly like the /v1/graph surfaces.
func (t *mcpTransport) serve(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		t.post(w, r)
	case http.MethodGet:
		t.stream(w, r)
	case http.MethodDelete:
		t.close(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		methodNotAllowed(w)
	}
}

func (t *mcpTransport) post(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(body) > maxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	out, failure := t.srv.Handle(r.Context(), principal, body, mcp.Options{
		Version:   r.Header.Get(mcpVersionHeader),
		SessionID: r.Header.Get(mcpSessionHeader),
	})
	if failure != nil {
		writeMCPFailure(w, failure)
		return
	}
	if out.SessionID != "" {
		w.Header().Set(mcpSessionHeader, out.SessionID)
	}
	if out.Version != "" {
		w.Header().Set(mcpVersionHeader, out.Version)
	}
	if out.Payload == nil {
		// A notification-only payload acknowledges without a body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if acceptsEventStream(r) {
		writeMCPEvent(w, out.Payload)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Payload)
}

// stream opens the server-to-client event stream. The stream only carries
// keep-alive comments: every result is answered on the POST that requested it,
// so an idle connection can never be mistaken for a lost response.
func (t *mcpTransport) stream(w http.ResponseWriter, r *http.Request) {
	session, ok := t.srv.Sessions().Touch(r.Header.Get(mcpSessionHeader))
	if !ok {
		writeMCPFailure(w, &mcp.Failure{Code: mcp.CodeSession, Message: "session not found", Status: http.StatusNotFound})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	// The serve loop sets WriteTimeout, which would otherwise cut every event
	// stream after 30 seconds. Clearing the deadline here is the supported way
	// to keep one long-lived response alive; an in-memory recorder does not
	// support it, and the stream still works there.
	_ = controller.SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	if err := controller.Flush(); err != nil {
		return
	}
	closed := t.register(session.ID)
	defer t.unregister(session.ID, closed)

	ticker := time.NewTicker(mcpSSEPingPeriod)
	defer ticker.Stop()
	lifetime := time.NewTimer(mcpSSEMaxLifetime)
	defer lifetime.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-closed:
			return
		case <-lifetime.C:
			return
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}

// close ends a session and any event stream attached to it.
func (t *mcpTransport) close(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get(mcpSessionHeader)
	if id == "" || !t.srv.DeleteSession(id) {
		writeMCPFailure(w, &mcp.Failure{Code: mcp.CodeSession, Message: "session not found", Status: http.StatusNotFound})
		return
	}
	t.closeStream(id)
	w.WriteHeader(http.StatusNoContent)
}

func (t *mcpTransport) register(id string) chan struct{} {
	closed := make(chan struct{})
	t.mu.Lock()
	if previous, ok := t.streams[id]; ok {
		// One stream per session: the newest connection wins.
		close(previous)
	}
	t.streams[id] = closed
	t.mu.Unlock()
	return closed
}

func (t *mcpTransport) unregister(id string, closed chan struct{}) {
	t.mu.Lock()
	if current, ok := t.streams[id]; ok && current == closed {
		delete(t.streams, id)
	}
	t.mu.Unlock()
}

func (t *mcpTransport) closeStream(id string) {
	t.mu.Lock()
	if closed, ok := t.streams[id]; ok {
		close(closed)
		delete(t.streams, id)
	}
	t.mu.Unlock()
}

// acceptsEventStream reports whether the client is willing to receive the
// response as an SSE event, which is what Streamable HTTP clients prefer.
func acceptsEventStream(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		if strings.HasPrefix(strings.TrimSpace(part), "text/event-stream") {
			return true
		}
	}
	return false
}

func writeMCPEvent(w http.ResponseWriter, payload []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// A JSON-RPC payload is single-line JSON, so one data field is enough and
	// no escaping is needed.
	_, _ = w.Write([]byte("event: message\ndata: "))
	_, _ = w.Write(payload)
	_, _ = w.Write([]byte("\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// writeMCPFailure answers a transport-level rejection with the JSON-RPC error
// object and the status the protocol layer chose.
func writeMCPFailure(w http.ResponseWriter, failure *mcp.Failure) {
	status := failure.Status
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	payload, err := json.Marshal(mcp.Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage("null"),
		Error:   &mcp.Error{Code: failure.Code, Message: failure.Message},
	})
	if err != nil {
		writeError(w, status, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
