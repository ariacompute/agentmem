package graph

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// Projector keeps meta.GraphIndex in step with the canonical bodies. It never
// writes bodies: Git remains the only authority and the index is disposable.
type Projector struct {
	Bodies BodyReader
	Meta   Metadata
	Index  meta.GraphIndex
	Log    *slog.Logger
}

// RebuildReport summarises a full reindex.
type RebuildReport struct {
	Nodes    int      `json:"nodes"`
	Edges    int      `json:"edges"`
	Pruned   int      `json:"pruned"`
	Warnings []string `json:"warnings"`
}

func (p *Projector) logger() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// LoadSchema merges every ontology-schema document in the bundle. Conflicts
// resolve deterministically: the document with the smallest id wins, and every
// conflict is reported instead of hidden.
func (p *Projector) LoadSchema(ctx context.Context) (okf.GraphSchema, []string, error) {
	if err := ctx.Err(); err != nil {
		return okf.GraphSchema{}, nil, err
	}
	ids, err := p.Bodies.List()
	if err != nil {
		return okf.GraphSchema{}, nil, fmt.Errorf("%w: list bundle: %v", ErrUnhealthy, err)
	}
	sort.Strings(ids)
	out := okf.GraphSchema{NodeTypes: map[string]okf.NodeType{}, EdgeTypes: map[string]okf.EdgeType{}}
	var warnings []string
	for _, id := range ids {
		doc, err := p.Bodies.Get(id)
		if err != nil {
			warnings = append(warnings, "unreadable_schema_document: "+id)
			continue
		}
		if doc.Type != schemaDocumentType {
			continue
		}
		s, err := doc.GraphSchema()
		if err != nil {
			warnings = append(warnings, "invalid_schema_document: "+id)
			continue
		}
		for name, nt := range s.NodeTypes {
			if _, exists := out.NodeTypes[name]; exists {
				warnings = append(warnings, "duplicate_node_type: "+name)
				continue
			}
			out.NodeTypes[name] = nt
		}
		for name, et := range s.EdgeTypes {
			if _, exists := out.EdgeTypes[name]; exists {
				warnings = append(warnings, "duplicate_edge_type: "+name)
				continue
			}
			out.EdgeTypes[name] = et
		}
	}
	sort.Strings(warnings)
	return out, dedupeStrings(warnings), nil
}

// schemaDocumentType marks the ontology document. The id convention is
// "ontology", but any document of this type participates, because OKF
// conformance never requires a particular path.
const schemaDocumentType = "ontology-schema"

// Sync projects one document, resolving the schema itself. It is the path used
// after a canonical write.
func (p *Projector) Sync(ctx context.Context, id string) (meta.GraphNodeRecord, []meta.GraphEdgeRecord, []string, error) {
	schema, schemaWarnings, err := p.LoadSchema(ctx)
	if err != nil {
		return meta.GraphNodeRecord{}, nil, nil, err
	}
	node, edges, warnings, err := p.SyncWith(ctx, id, schema)
	if err != nil {
		return node, edges, warnings, err
	}
	return node, edges, append(schemaWarnings, warnings...), nil
}

// SyncWith projects one document against an already loaded schema.
func (p *Projector) SyncWith(ctx context.Context, id string, schema okf.GraphSchema) (meta.GraphNodeRecord, []meta.GraphEdgeRecord, []string, error) {
	if err := ctx.Err(); err != nil {
		return meta.GraphNodeRecord{}, nil, nil, err
	}
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return meta.GraphNodeRecord{}, nil, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	doc, err := p.Bodies.Get(clean)
	if err != nil {
		return meta.GraphNodeRecord{}, nil, nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	m, err := p.Meta.GetConcept(ctx, clean)
	if err != nil && !meta.IsNotFound(err) {
		return meta.GraphNodeRecord{}, nil, nil, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	hash, commit, err := contentIdentity(doc, m)
	if err != nil {
		return meta.GraphNodeRecord{}, nil, nil, err
	}
	node, edges, warnings, err := NodeFrom(clean, doc, hash, commit)
	if err != nil {
		return meta.GraphNodeRecord{}, nil, nil, err
	}
	// Validate before projecting: an invalid declaration must never replace a
	// previously valid projection.
	declWarnings, err := ValidateDecl(ctx, schema, node, edges, p.lookup)
	if err != nil {
		return meta.GraphNodeRecord{}, nil, nil, err
	}
	if err := p.Index.UpsertGraphNode(ctx, node, edges); err != nil {
		return meta.GraphNodeRecord{}, nil, nil, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	warnings = append(warnings, declWarnings...)
	return node, edges, dedupeStrings(warnings), nil
}

// contentIdentity derives the hash from the body itself so a rebuild matches
// what a canonical write would have stored.
func contentIdentity(doc *okf.Document, m meta.ConceptMeta) (string, string, error) {
	data, err := doc.Marshal()
	if err != nil {
		return "", "", fmt.Errorf("%w: marshal document: %v", ErrInvalid, err)
	}
	hash := okf.Hash(data)
	if m.ContentHash == hash {
		return hash, m.CommitSHA, nil
	}
	return hash, "", nil
}

// Rebuild reindexes every concept, prunes rows whose body is gone and returns a
// deterministic report.
func (p *Projector) Rebuild(ctx context.Context) (RebuildReport, error) {
	if err := ctx.Err(); err != nil {
		return RebuildReport{}, err
	}
	ids, err := p.Bodies.List()
	if err != nil {
		return RebuildReport{}, fmt.Errorf("%w: list bundle: %v", ErrUnhealthy, err)
	}
	schema, schemaWarnings, err := p.LoadSchema(ctx)
	if err != nil {
		return RebuildReport{}, err
	}
	report := RebuildReport{Warnings: append([]string{}, schemaWarnings...)}
	// Two passes: every node must exist before ontology validation can resolve
	// target types, otherwise the outcome would depend on document ordering.
	type projected struct {
		id    string
		node  meta.GraphNodeRecord
		edges []meta.GraphEdgeRecord
		warns []string
	}
	batch := make([]projected, 0, len(ids))
	for _, id := range ids {
		doc, err := p.Bodies.Get(id)
		if err != nil {
			report.Warnings = append(report.Warnings, "unreadable_document: "+id)
			continue
		}
		m, err := p.Meta.GetConcept(ctx, id)
		if err != nil && !meta.IsNotFound(err) {
			return report, fmt.Errorf("%w: %v", ErrUnhealthy, err)
		}
		hash, commit, err := contentIdentity(doc, m)
		if err != nil {
			report.Warnings = append(report.Warnings, "invalid_document: "+id)
			continue
		}
		node, edges, warnings, err := NodeFrom(id, doc, hash, commit)
		if err != nil {
			report.Warnings = append(report.Warnings, "invalid_declaration: "+id)
			continue
		}
		if err := p.Index.UpsertGraphNode(ctx, node, nil); err != nil {
			return report, fmt.Errorf("%w: %v", ErrUnhealthy, err)
		}
		batch = append(batch, projected{id: id, node: node, edges: edges, warns: warnings})
	}
	for _, item := range batch {
		declWarnings, err := ValidateDecl(ctx, schema, item.node, item.edges, p.lookup)
		if err != nil {
			// A rejected declaration must not leave a half-indexed node behind.
			if derr := p.Index.DeleteGraphNode(ctx, item.id); derr != nil {
				return report, fmt.Errorf("%w: %v", ErrUnhealthy, derr)
			}
			report.Warnings = append(report.Warnings, "declaration_rejected: "+item.id)
			continue
		}
		if err := p.Index.UpsertGraphNode(ctx, item.node, item.edges); err != nil {
			return report, fmt.Errorf("%w: %v", ErrUnhealthy, err)
		}
		report.Nodes++
		report.Edges += len(item.edges)
		report.Warnings = append(report.Warnings, item.warns...)
		report.Warnings = append(report.Warnings, declWarnings...)
	}
	pruned, err := p.Index.PruneGraphNodes(ctx, ids)
	if err != nil {
		return report, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	report.Pruned = pruned
	sort.Strings(report.Warnings)
	report.Warnings = dedupeStrings(report.Warnings)
	p.logger().Info("graph index rebuilt", "nodes", report.Nodes, "edges", report.Edges, "pruned", report.Pruned)
	return report, nil
}

// EnsureFresh lazily repairs one row whose body changed under another process
// (CLI write, git checkout). A missing row is projected on demand.
func (p *Projector) EnsureFresh(ctx context.Context, id string) (meta.GraphNodeRecord, bool, error) {
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return meta.GraphNodeRecord{}, false, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	doc, err := p.Bodies.Get(clean)
	if err != nil {
		return meta.GraphNodeRecord{}, false, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	// The body hash, not the metadata hash, is the freshness signal: a git
	// checkout or another CLI process can change the body without touching
	// concepts_meta.
	data, err := doc.Marshal()
	if err != nil {
		return meta.GraphNodeRecord{}, false, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	hash := okf.Hash(data)
	n, err := p.Index.GetGraphNode(ctx, clean)
	if err == nil && n.ContentHash == hash {
		return n, false, nil
	}
	if err != nil && !meta.IsNotFound(err) {
		return meta.GraphNodeRecord{}, false, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	node, _, warnings, err := p.Sync(ctx, clean)
	if err != nil {
		return meta.GraphNodeRecord{}, false, err
	}
	for _, w := range warnings {
		p.logger().Warn("graph projection warning", "concept_id", clean, "warning", w)
	}
	return node, true, nil
}

// lookup resolves an edge target's node type for ontology validation.
func (p *Projector) lookup(ctx context.Context, id string) (string, bool) {
	n, err := p.Index.GetGraphNode(ctx, id)
	if err != nil || strings.TrimSpace(n.NodeType) == "" {
		return "", false
	}
	return n.NodeType, true
}
