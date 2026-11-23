package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// NodeInput is a partial node upsert. Nil fields keep the current value, so a
// caller can update a label without restating the whole document.
type NodeInput struct {
	ConceptID   string
	NodeType    *string
	Label       *string
	Description *string
	Properties  map[string]string
	Edges       []okf.GraphEdge
	EdgesSet    bool
}

// Writer applies graph writes through the canonical store and then repairs the
// projection. A failed projection never rolls back the body: it is reported as
// a warning exactly like the RAG projections.
type Writer struct {
	Meta      Metadata
	Store     DocumentWriter
	Projector *Projector
	Searcher  *Searcher
}

// PutNode creates or updates one node.
func (w *Writer) PutNode(ctx context.Context, subject string, in NodeInput) (WriteResult, []string, error) {
	clean, err := okf.ValidateConceptID(in.ConceptID)
	if err != nil {
		return WriteResult{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	doc, err := w.load(ctx, subject, clean)
	if err != nil {
		return WriteResult{}, nil, err
	}
	if in.NodeType != nil {
		v := strings.TrimSpace(*in.NodeType)
		if v == "" {
			return WriteResult{}, nil, fmt.Errorf("%w: node_type must not be empty", ErrInvalid)
		}
		doc.Type = v
	}
	if in.Label != nil {
		doc.Title = strings.TrimSpace(*in.Label)
	}
	if in.Description != nil {
		doc.Description = strings.TrimSpace(*in.Description)
	}
	decl, err := doc.Graph()
	if err != nil {
		return WriteResult{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if in.Properties != nil {
		decl.Node.Properties = in.Properties
	}
	if in.EdgesSet {
		decl.Edges = in.Edges
	}
	if err := doc.SetGraph(decl); err != nil {
		return WriteResult{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return w.commit(ctx, subject, clean, doc)
}

// AddEdges appends declared relations, merging duplicates by (type, target).
func (w *Writer) AddEdges(ctx context.Context, subject, conceptID string, edges []okf.GraphEdge) (WriteResult, []string, error) {
	clean, err := okf.ValidateConceptID(conceptID)
	if err != nil {
		return WriteResult{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(edges) == 0 {
		return WriteResult{}, nil, fmt.Errorf("%w: at least one edge is required", ErrInvalid)
	}
	doc, err := w.load(ctx, subject, clean)
	if err != nil {
		return WriteResult{}, nil, err
	}
	decl, err := doc.Graph()
	if err != nil {
		return WriteResult{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	seen := map[string]bool{}
	for _, e := range decl.Edges {
		seen[e.Type+"\x00"+e.To] = true
	}
	for _, e := range edges {
		key := e.Type + "\x00" + e.To
		if seen[key] {
			continue
		}
		seen[key] = true
		decl.Edges = append(decl.Edges, e)
	}
	if err := doc.SetGraph(decl); err != nil {
		return WriteResult{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return w.commit(ctx, subject, clean, doc)
}

// load fetches the canonical document. A missing document is a new one, but a
// document the subject cannot see stays invisible so writes cannot probe for
// existence.
func (w *Writer) load(ctx context.Context, subject, id string) (*okf.Document, error) {
	doc, err := w.Store.Get(ctx, subject, id)
	if err == nil {
		return doc, nil
	}
	if _, merr := w.Meta.GetConcept(ctx, id); merr == nil {
		return nil, ErrNotFound
	} else if !meta.IsNotFound(merr) {
		return nil, fmt.Errorf("%w: %v", ErrUnhealthy, merr)
	}
	return &okf.Document{Type: "concept"}, nil
}

func (w *Writer) commit(ctx context.Context, subject, id string, doc *okf.Document) (WriteResult, []string, error) {
	schema, schemaWarnings, err := w.Projector.LoadSchema(ctx)
	if err != nil {
		return WriteResult{}, nil, err
	}
	probe, edges, _, err := NodeFrom(id, doc, "", "")
	if err != nil {
		return WriteResult{}, nil, err
	}
	if _, err := ValidateDecl(ctx, schema, probe, edges, w.lookup); err != nil {
		return WriteResult{}, nil, err
	}
	res, err := w.Store.PutAs(ctx, subject, id, doc)
	if err != nil {
		if ok, aerr := w.Meta.CheckACL(ctx, subject, id); aerr == nil && !ok {
			return WriteResult{}, nil, ErrNotFound
		}
		return WriteResult{}, nil, err
	}
	if w.Searcher != nil {
		w.Searcher.InvalidateSchema()
	}
	_, _, syncWarnings, perr := w.Projector.Sync(ctx, id)
	warnings := append([]string{}, syncWarnings...)
	if perr != nil {
		warnings = append(warnings, "index_sync_failed: "+id)
	}
	warnings = append(schemaWarnings, warnings...)
	return WriteResult{
		ConceptID: res.ConceptID, ContentHash: res.ContentHash,
		CommitSHA: res.CommitSHA, Unchanged: res.Unchanged,
	}, dedupeStrings(warnings), nil
}

// lookup resolves target node types from the index, falling back to reading the
// target body so a batch of new documents can reference each other.
func (w *Writer) lookup(ctx context.Context, id string) (string, bool) {
	if n, err := w.Projector.Index.GetGraphNode(ctx, id); err == nil && n.NodeType != "" {
		return n.NodeType, true
	}
	doc, err := w.Projector.Bodies.Get(id)
	if err != nil {
		return "", false
	}
	decl, err := doc.Graph()
	if err == nil && decl.Node.Type != "" {
		return decl.Node.Type, true
	}
	if doc.Type == "" {
		return "", false
	}
	return doc.Type, true
}

// ErrNoDocument lets callers distinguish "nothing to sync" from a real failure.
var ErrNoDocument = errors.New("graph: no such document")
