package mcp

import (
	"context"
	"encoding/json"
	"time"

	"agentmem/internal/auth"
	"agentmem/internal/compute"
	"agentmem/internal/graph"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/router"
)

// Backend is the application port this package consumes. Its signatures mirror
// the service use cases exactly, so the MCP surface reuses the same ACL checks,
// the same Git write path and the same audit records as REST and CLI. The
// implementation lives in the service layer; mcp never imports it.
type Backend interface {
	Query(ctx context.Context, subject, text string, topK int) (router.QueryResult, error)
	Put(ctx context.Context, subject, id string, doc *okf.Document) (router.WriteResult, error)
	Knowledge(ctx context.Context, subject, id string) (*okf.Document, error)
	Concepts(ctx context.Context) ([]string, error)
	Decisions(ctx context.Context, conceptID string, limit int) ([]meta.DecisionRecord, error)
	Computations(ctx context.Context, conceptID string, limit int) ([]meta.ComputationRecord, error)
	RunComputation(ctx context.Context, req compute.Request) (compute.Envelope, error)

	GraphEnabled() bool
	MaxGraphDepth() int
	GraphStats(ctx context.Context, subject string) (meta.GraphStats, error)
	GraphNode(ctx context.Context, subject, id string) (graph.NodeView, error)
	GraphNeighbors(ctx context.Context, subject string, q graph.WalkQuery) (graph.SubgraphView, error)
	GraphPath(ctx context.Context, subject string, q graph.PathQuery) (graph.PathView, error)
	GraphSearch(ctx context.Context, subject string, q graph.SearchQuery) ([]graph.NodeHit, error)
	GraphSchema(ctx context.Context) (okf.GraphSchema, []string, error)
	GraphAnswerability(ctx context.Context, subject, question string) (graph.Coverage, error)
	GraphRetrieve(ctx context.Context, subject, text string, topK int) (graph.RetrieveResult, error)
	GraphPutNode(ctx context.Context, subject string, in graph.NodeInput) (graph.WriteResult, []string, error)
	GraphAddEdges(ctx context.Context, subject, id string, edges []okf.GraphEdge) (graph.WriteResult, []string, error)
	GraphRebuild(ctx context.Context) (graph.RebuildReport, error)
}

// Config bounds the server. The zero value is the safe, offline default: write
// tools stay hidden, sessions are bounded and results are clamped.
type Config struct {
	// Write registers the mutating tools. It defaults to false so an existing
	// deployment keeps the original read-only MCP surface.
	Write bool
	// MaxLimit clamps every tool limit (1..MaxLimit).
	MaxLimit int
	// MaxDepth clamps neighbourhood and path depth.
	MaxDepth int
	// SessionTTL and MaxSessions bound the in-memory session table.
	SessionTTL  time.Duration
	MaxSessions int
}

const (
	defaultMaxLimit    = 200
	defaultMaxDepth    = 8
	defaultSessionTTL  = 30 * time.Minute
	defaultMaxSessions = 128
)

// Tool is one MCP tool. Scope is the capability the caller must hold; Mutating
// tools are only registered when Config.Write is true.
type Tool struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
	Scope        auth.Permission
	Mutating     bool
	run          func(ctx context.Context, subject string, args json.RawMessage) (any, error)
}

// ToolError turns a domain rejection into an MCP tool result with isError=true
// instead of a protocol error, which is the shape an LLM client can reason
// about. Structured is optional evidence that is safe to return.
type ToolError struct {
	Message    string
	Structured any
}

func (e *ToolError) Error() string { return e.Message }

// Server is the protocol server. It holds no per-request state: the tool table
// is immutable after construction and the session table is lock-protected.
type Server struct {
	backend  Backend
	cfg      Config
	sessions *Sessions
	tools    map[string]*Tool
	ordered  []*Tool
}

// NewServer builds the server and its tool registry.
func NewServer(backend Backend, cfg Config) *Server {
	if cfg.MaxLimit <= 0 {
		cfg.MaxLimit = defaultMaxLimit
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = defaultMaxDepth
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultSessionTTL
	}
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = defaultMaxSessions
	}
	s := &Server{
		backend:  backend,
		cfg:      cfg,
		sessions: NewSessions(cfg.SessionTTL, cfg.MaxSessions),
		tools:    make(map[string]*Tool),
	}
	s.registerTools()
	return s
}

// Sessions exposes the session table to the HTTP transport, which must be able
// to delete a session when a client disconnects its event stream.
func (s *Server) Sessions() *Sessions { return s.sessions }

// DeleteSession removes a session and reports whether it existed.
func (s *Server) DeleteSession(id string) bool { return s.sessions.Delete(id) }

// WriteEnabled reports whether the mutating tools are registered.
func (s *Server) WriteEnabled() bool { return s.cfg.Write }

func (s *Server) add(t *Tool) {
	s.tools[t.Name] = t
	s.ordered = append(s.ordered, t)
}
