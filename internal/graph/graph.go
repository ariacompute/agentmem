// Package graph turns the OKF graph declarations into a queryable knowledge
// graph. The bodies in Git are the only authority; this package parses those
// declarations, validates them against the typed ontology and projects a
// rebuildable adjacency index into meta.GraphIndex.
package graph

import (
	"context"
	"errors"
	"time"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// Error categories. Handlers map them to stable status codes so a private node
// and a missing node stay indistinguishable (both 404).
var (
	ErrNotFound  = errors.New("graph: not found")
	ErrInvalid   = errors.New("graph: invalid declaration")
	ErrUnhealthy = errors.New("graph: index unavailable")
	ErrDisabled  = errors.New("graph: disabled")
)

// BodyReader reads canonical documents for projection. okf.Bundle satisfies it.
type BodyReader interface {
	Get(id string) (*okf.Document, error)
	List() ([]string, error)
}

// DocumentReader is the ACL-checked read path. store.OKFStore satisfies it.
type DocumentReader interface {
	Get(ctx context.Context, subject, id string) (*okf.Document, error)
}

// WriteResult mirrors store.PutResult without importing the store package, so
// the graph package keeps depending only on okf, meta and netsafe.
type WriteResult struct {
	ConceptID   string
	ContentHash string
	CommitSHA   string
	Unchanged   bool
}

// DocumentWriter is the canonical read/write path owned by the store.
type DocumentWriter interface {
	Get(ctx context.Context, subject, id string) (*okf.Document, error)
	PutAs(ctx context.Context, subject, id string, d *okf.Document) (WriteResult, error)
}

// Metadata is the structured-state port the graph package needs.
type Metadata interface {
	CheckACL(ctx context.Context, subject, conceptID string) (bool, error)
	GetConcept(ctx context.Context, conceptID string) (meta.ConceptMeta, error)
}

// SourceView is one OKF source, echoed in node detail so lineage is clickable.
type SourceView struct {
	ID           string     `json:"id,omitempty"`
	Resource     string     `json:"resource"`
	Title        string     `json:"title,omitempty"`
	Author       string     `json:"author,omitempty"`
	LastModified *time.Time `json:"last_modified,omitempty"`
}

// ComputationView is the Attested Computation summary of a node.
type ComputationView struct {
	Runtime     string   `json:"runtime"`
	Computation string   `json:"computation,omitempty"`
	Parameters  []string `json:"parameters,omitempty"`
}

// EdgeView is one directed relation. Origin says whether the claim was
// authored ("declared") or derived from the body ("link", "source", "resource").
type EdgeView struct {
	FromID     string            `json:"from_id"`
	ToID       string            `json:"to_id"`
	EdgeType   string            `json:"edge_type"`
	Origin     string            `json:"origin"`
	Evidence   string            `json:"evidence,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// NodeView is the full node detail: index facts plus the body-derived lineage.
type NodeView struct {
	ConceptID   string            `json:"concept_id"`
	NodeType    string            `json:"node_type"`
	Label       string            `json:"label"`
	Description string            `json:"description,omitempty"`
	Status      string            `json:"status,omitempty"`
	Trust       string            `json:"trust"`
	Stale       bool              `json:"stale"`
	Visibility  string            `json:"visibility"`
	Properties  map[string]string `json:"properties,omitempty"`
	Resource    string            `json:"resource,omitempty"`
	Sources     []SourceView      `json:"sources"`
	Computation *ComputationView  `json:"computation,omitempty"`
	Out         []EdgeView        `json:"out"`
	In          []EdgeView        `json:"in"`
	Warnings    []string          `json:"warnings"`
	ContentHash string            `json:"content_hash"`
	CommitSHA   string            `json:"commit_sha,omitempty"`
}

// NodeHit is a list-row projection.
type NodeHit struct {
	ConceptID   string `json:"concept_id"`
	NodeType    string `json:"node_type"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Trust       string `json:"trust"`
	Status      string `json:"status,omitempty"`
	Stale       bool   `json:"stale"`
}

// WalkQuery is a bounded neighbourhood request.
type WalkQuery struct {
	From      string
	Depth     int
	Direction string
	EdgeTypes []string
	NodeTypes []string
	Limit     int
}

// SubgraphView is the expanded neighbourhood.
type SubgraphView struct {
	Root      string         `json:"root"`
	Nodes     []NodeHit      `json:"nodes"`
	Edges     []EdgeView     `json:"edges"`
	Depths    map[string]int `json:"depths"`
	Truncated bool           `json:"truncated"`
}

// PathQuery is a shortest-path request.
type PathQuery struct {
	From      string
	To        string
	MaxDepth  int
	Direction string
	EdgeTypes []string
	Limit     int
}

// PathView is the shortest path, if any.
type PathView struct {
	From  string     `json:"from"`
	To    string     `json:"to"`
	Found bool       `json:"found"`
	Hops  int        `json:"hops"`
	Nodes []NodeHit  `json:"nodes"`
	Edges []EdgeView `json:"edges"`
}

// SearchQuery is a deterministic keyword lookup.
type SearchQuery struct {
	Text      string
	NodeTypes []string
	Limit     int
}

// TermMatch is one question token resolved to a node.
type TermMatch struct {
	Term      string  `json:"term"`
	ConceptID string  `json:"concept_id"`
	Label     string  `json:"label"`
	NodeType  string  `json:"node_type"`
	Score     float64 `json:"score"`
}

// Gap is one concrete reason an answer cannot be certified.
type Gap struct {
	Kind      string `json:"kind"`
	ConceptID string `json:"concept_id,omitempty"`
	Detail    string `json:"detail"`
}

// Coverage is the answerability verdict. Insufficient evidence is reported as
// gaps instead of a fabricated answer.
type Coverage struct {
	Question string      `json:"question"`
	Verdict  string      `json:"verdict"`
	Coverage float64     `json:"coverage"`
	Minimum  float64     `json:"minimum"`
	Matched  []TermMatch `json:"matched"`
	Gaps     []Gap       `json:"gaps"`
	Terms    []string    `json:"terms"`
}

// Verdicts.
const (
	VerdictAnswerable   = "answerable"
	VerdictPartial      = "partial"
	VerdictInsufficient = "insufficient"
)

// Gap kinds.
const (
	GapMissingTerm     = "missing_term"
	GapDanglingEdge    = "dangling_edge"
	GapStale           = "stale"
	GapUnverified      = "unverified"
	GapNoSource        = "no_source"
	GapUnknownEdgeType = "unknown_edge_type"
	GapEmptyIndex      = "empty_index"
)

// hitOf turns an indexed row into the public list projection.
func hitOf(n meta.GraphNodeRecord, stale bool) NodeHit {
	return NodeHit{
		ConceptID: n.ConceptID, NodeType: n.NodeType, Label: n.Label,
		Description: n.Description, Trust: n.Trust, Status: n.Status, Stale: stale,
	}
}

// edgeOf renders an indexed edge for the wire.
func edgeOf(e meta.GraphEdgeRecord) EdgeView {
	return EdgeView{
		FromID: e.FromID, ToID: e.ToID, EdgeType: e.EdgeType,
		Origin: e.Origin, Evidence: e.Evidence, Properties: e.Properties,
	}
}
