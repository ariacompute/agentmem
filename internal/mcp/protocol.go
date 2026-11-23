// Package mcp implements the Model Context Protocol server. It is a protocol
// layer only: the HTTP transport lives in internal/handlers and the stdio
// transport in internal/cli, so both share one dispatcher, one tool registry
// and one authorisation rule.
//
// The package never imports service, handlers or cli. Tools reach the
// application through the Backend port, which the service layer implements, so
// an MCP write can never bypass the store's ACL and Git is still the only
// authority for bodies.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"agentmem/internal/auth"
)

// Supported protocol revisions, newest first. A client that asks for an
// unknown revision is answered with LatestVersion and decides whether to
// continue; the server never silently downgrades a requested revision.
const (
	Version20250618 = "2025-06-18"
	Version20250326 = "2025-03-26"
	Version20241105 = "2024-11-05"
)

var supportedVersions = []string{Version20250618, Version20250326, Version20241105}

// LatestVersion is the revision advertised when a client asks for something
// this server does not implement.
func LatestVersion() string { return supportedVersions[0] }

func supportedVersion(v string) bool {
	for _, s := range supportedVersions {
		if s == v {
			return true
		}
	}
	return false
}

// IsStateful reports whether a negotiated revision requires a session. The
// 2024-11-05 revision predates Streamable HTTP, so it stays stateless.
func IsStateful(version string) bool {
	return version == Version20250618 || version == Version20250326
}

// JSON-RPC 2.0 error codes, plus the implementation-defined -3200x range the
// MCP specification reserves for transports.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeAuthRequired   = -32001
	CodeNotFound       = -32002
	CodeForbidden      = -32003
	CodeSession        = -32004
)

// Request is one JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Error is the JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Response is one JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Failure is a transport-level rejection: the request never reached a tool and
// the whole payload must be answered with this error. Status is the HTTP status
// the HTTP transport must use; the stdio transport ignores it.
type Failure struct {
	Code    int
	Message string
	Status  int
}

func (f *Failure) Error() string { return f.Message }

// Options carries what the transport already knows about the request: the
// negotiated revision from the MCP-Protocol-Version header, the Mcp-Session-Id
// header, and whether the transport has no sessions at all (stdio).
type Options struct {
	Version   string
	SessionID string
	Stateless bool
}

// Outcome is the transport's answer. Payload is nil when no response is due
// (a notification-only payload); SessionID is set once initialize created a
// session; Version is the negotiated revision for response headers.
type Outcome struct {
	Payload   json.RawMessage
	SessionID string
	Version   string
}

// maxBatch bounds one JSON-RPC batch so a single request cannot allocate an
// unbounded number of responses.
const maxBatch = 32

// Handle processes one JSON-RPC payload: either a single object or a batch
// array. Tool calls are authorised against p, the principal the transport
// established; a subject in the payload is never read.
func (s *Server) Handle(ctx context.Context, p auth.Principal, body []byte, opts Options) (Outcome, *Failure) {
	if opts.Version != "" && !supportedVersion(opts.Version) {
		return Outcome{}, &Failure{Code: CodeInvalidRequest, Message: "unsupported protocol version", Status: 400}
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return Outcome{}, parseFailure()
	}
	if trimmed[0] == '[' {
		return s.handleBatch(ctx, p, trimmed, opts)
	}
	var req Request
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return Outcome{}, parseFailure()
	}
	state := &callState{version: opts.Version}
	resp, failure := s.handleOne(ctx, p, req, state, opts)
	if failure != nil {
		return Outcome{}, failure
	}
	out := Outcome{SessionID: state.sessionID, Version: state.version}
	if resp != nil {
		// A Response only carries strings, numbers and pre-encoded JSON, so
		// encoding cannot fail.
		payload, _ := json.Marshal(resp)
		out.Payload = payload
	}
	return out, nil
}

func (s *Server) handleBatch(ctx context.Context, p auth.Principal, body []byte, opts Options) (Outcome, *Failure) {
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return Outcome{}, parseFailure()
	}
	if len(raw) == 0 || len(raw) > maxBatch {
		return Outcome{}, &Failure{Code: CodeInvalidRequest, Message: "invalid request", Status: 400}
	}
	state := &callState{version: opts.Version}
	responses := make([]Response, 0, len(raw))
	for _, item := range raw {
		var req Request
		if err := json.Unmarshal(item, &req); err != nil {
			responses = append(responses, Response{
				JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &Error{Code: CodeParse, Message: "parse error"},
			})
			continue
		}
		resp, failure := s.handleOne(ctx, p, req, state, opts)
		if failure != nil {
			return Outcome{}, failure
		}
		if resp != nil {
			responses = append(responses, *resp)
		}
	}
	out := Outcome{SessionID: state.sessionID, Version: state.version}
	if len(responses) == 0 {
		return out, nil
	}
	// Every element is a Response: strings, numbers and pre-encoded JSON.
	payload, _ := json.Marshal(responses)
	out.Payload = payload
	return out, nil
}

// callState tracks the mutable negotiation facts of one payload, so a batch
// answered in order keeps the revision initialize established.
type callState struct {
	version   string
	sessionID string
}

func (s *Server) handleOne(ctx context.Context, p auth.Principal, req Request, state *callState, opts Options) (*Response, *Failure) {
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		return errorResponse(req.ID, CodeInvalidRequest, "invalid request"), nil
	}
	// Notifications carry no id and expect no body.
	if len(req.ID) == 0 && strings.HasPrefix(req.Method, "notifications/") {
		return nil, nil
	}
	if req.Method != "initialize" {
		if err := s.bindSession(opts, state); err != nil {
			return nil, err
		}
	}
	switch req.Method {
	case "initialize":
		return s.initialize(req, state, opts), nil
	case "ping":
		return resultResponse(req.ID, map[string]any{}), nil
	case "tools/list":
		return resultResponse(req.ID, map[string]any{"tools": s.listTools()}), nil
	case "tools/call":
		return s.callTool(ctx, req, p), nil
	default:
		return errorResponse(req.ID, CodeMethodNotFound, "method not found"), nil
	}
}

// bindSession resolves the revision and session for every non-initialize
// request. A stateful revision without a session is rejected rather than
// served anonymously, so a client cannot skip the handshake and still call
// tools.
func (s *Server) bindSession(opts Options, state *callState) *Failure {
	if opts.Stateless {
		if state.version == "" {
			state.version = Version20241105
		}
		return nil
	}
	if opts.SessionID == "" {
		if opts.Version != "" && IsStateful(opts.Version) {
			return &Failure{Code: CodeSession, Message: "Mcp-Session-Id header is required", Status: 404}
		}
		if state.version == "" {
			state.version = Version20241105
		}
		return nil
	}
	session, ok := s.sessions.Touch(opts.SessionID)
	if !ok {
		return &Failure{Code: CodeSession, Message: "session not found", Status: 404}
	}
	state.version = session.Version
	return nil
}

func (s *Server) initialize(req Request, state *callState, opts Options) *Response {
	version := negotiatedVersion(req.Params)
	if opts.Version != "" && supportedVersion(opts.Version) {
		// The header wins: a client that already pinned a revision must not be
		// silently moved by a differing params value.
		version = opts.Version
	}
	if !opts.Stateless && IsStateful(version) {
		session, err := s.sessions.Create(version)
		if err != nil {
			return errorResponse(req.ID, CodeSession, "session capacity reached")
		}
		state.sessionID = session.ID
	}
	state.version = version
	return resultResponse(req.ID, map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo":   map[string]any{"name": "agentmem", "version": "mcp"},
		"instructions": "Read-only knowledge tools are always available. Write tools require AGENTMEM_MCP_WRITE=true and the matching scope.",
	})
}

// negotiatedVersion answers with the client's revision when it is supported and
// with the newest revision otherwise, as the specification requires.
func negotiatedVersion(params json.RawMessage) string {
	if len(params) == 0 {
		return LatestVersion()
	}
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return LatestVersion()
	}
	v := strings.TrimSpace(p.ProtocolVersion)
	if v == "" {
		return LatestVersion()
	}
	if supportedVersion(v) {
		return v
	}
	return LatestVersion()
}

func resultResponse(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: "2.0", ID: idOrNull(id), Result: result}
}

func errorResponse(id json.RawMessage, code int, message string) *Response {
	return &Response{JSONRPC: "2.0", ID: idOrNull(id), Error: &Error{Code: code, Message: message}}
}

func parseFailure() *Failure {
	return &Failure{Code: CodeParse, Message: "parse error", Status: 400}
}

func idOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
