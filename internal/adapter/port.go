// Package adapter defines the plugin contract for retrieval channels.
//
// Ports are defined in the consuming layer; implementations never leak their
// concrete backends (gorm, HTTP clients, in-memory indexes) into callers.
package adapter

import (
	"context"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// Capabilities declares what a channel can do. Unsupported operations must
// return ErrUnsupported instead of silently doing nothing.
type Capabilities struct {
	Put       bool
	Query     bool
	Delete    bool
	Vector    bool
	Semantic  bool
	Graph     bool
	PageLevel bool
}

// Query is the common retrieval request.
type Query struct {
	Text    string
	TopK    int
	Vector  meta.Vector
	Filters map[string]string
}

// Hit is one retrieval result, always carrying its source channel.
type Hit struct {
	ConceptID string  `json:"concept_id"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
	Source    string  `json:"source"`
	Citation  string  `json:"citation"`
}

// Adapter is the single plugin contract shared by built-ins and third parties.
// conceptID is passed explicitly because an OKF document has no identity field.
type Adapter interface {
	Name() string
	Capabilities() Capabilities
	Put(ctx context.Context, conceptID string, doc *okf.Document) error
	Query(ctx context.Context, q Query) ([]Hit, error)
	Delete(ctx context.Context, conceptID string) error
	Fingerprint() string
	Health(ctx context.Context) error
}
