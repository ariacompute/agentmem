package graph

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// --- projector: schema ------------------------------------------------------

func TestLoadSchemaErrorPaths(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := (&Projector{Bodies: &bodyStub{}, Meta: &metaStub{}}).LoadSchema(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}

	listErr := &Projector{Bodies: &bodyStub{listErr: boom("list")}, Meta: &metaStub{}}
	if _, _, err := listErr.LoadSchema(context.Background()); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("list: %v", err)
	}

	unreadable := &Projector{
		Bodies: &bodyStub{
			docs:   map[string]*okf.Document{"ontology": schemaDocument("metric")},
			getErr: map[string]error{"ontology": boom("get")},
		},
		Meta: &metaStub{},
	}
	_, warnings, err := unreadable.LoadSchema(context.Background())
	if err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	if len(warnings) != 1 || warnings[0] != "unreadable_schema_document: ontology" {
		t.Fatalf("warnings: %v", warnings)
	}

	broken, err := okf.ParseDocument([]byte("---\ntype: ontology-schema\ngraph_schema: nope\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	invalid := &Projector{Bodies: &bodyStub{docs: map[string]*okf.Document{"ontology": broken}}, Meta: &metaStub{}}
	_, warnings, err = invalid.LoadSchema(context.Background())
	if err != nil {
		t.Fatalf("invalid: %v", err)
	}
	if len(warnings) != 1 || warnings[0] != "invalid_schema_document: ontology" {
		t.Fatalf("warnings: %v", warnings)
	}
}

func TestLoadSchemaReportsDuplicateEdgeTypes(t *testing.T) {
	first := &okf.Document{Type: "ontology-schema"}
	if err := first.SetGraphSchema(okf.GraphSchema{
		EdgeTypes: map[string]okf.EdgeType{"links_to": {}, "other": {}},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	second := &okf.Document{Type: "ontology-schema"}
	if err := second.SetGraphSchema(okf.GraphSchema{
		EdgeTypes: map[string]okf.EdgeType{"links_to": {}},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	p := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"a": first, "b": second}, list: []string{"a", "b"}},
		Meta:   &metaStub{},
	}
	_, warnings, err := p.LoadSchema(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(warnings) != 1 || warnings[0] != "duplicate_edge_type: links_to" {
		t.Fatalf("warnings: %v", warnings)
	}
}

// --- projector: sync -------------------------------------------------------

func TestSyncPropagatesSchemaAndProjectionFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	p := &Projector{Bodies: &bodyStub{}, Meta: &metaStub{}, Index: &indexStub{}}
	if _, _, _, err := p.Sync(canceled, "metrics/a"); err == nil {
		t.Fatal("a canceled context must fail the schema load")
	}

	// The schema loads but the body is gone: the projection cannot be rebuilt.
	p = &Projector{Bodies: &bodyStub{}, Meta: &metaStub{}, Index: &indexStub{}}
	if _, _, _, err := p.Sync(context.Background(), "metrics/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing body: %v", err)
	}
}

func TestSyncWithRejectsInvalidInputs(t *testing.T) {
	doc := &okf.Document{Type: "metric"}
	p := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}},
		Meta:   &metaStub{},
		Index:  &indexStub{},
		Log:    slog.New(slog.DiscardHandler),
	}
	if _, _, _, err := p.SyncWith(context.Background(), "", okf.GraphSchema{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid id: %v", err)
	}
	if _, _, _, err := p.SyncWith(context.Background(), "metrics/absent", okf.GraphSchema{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing body: %v", err)
	}

	brokenMeta := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}},
		Meta:   &metaStub{metaErr: map[string]error{"metrics/a": boom("meta")}},
		Index:  &indexStub{},
	}
	if _, _, _, err := brokenMeta.SyncWith(context.Background(), "metrics/a", okf.GraphSchema{}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("meta: %v", err)
	}

	malformed, err := okf.ParseDocument([]byte("---\ntype: metric\ngraph: nope\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	badDecl := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": malformed}},
		Meta:   &metaStub{},
		Index:  &indexStub{},
	}
	if _, _, _, err := badDecl.SyncWith(context.Background(), "metrics/a", okf.GraphSchema{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("declaration: %v", err)
	}

	rejecting := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}, list: []string{"metrics/a"}},
		Meta:   &metaStub{},
		Index:  &indexStub{},
	}
	schema := okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}},
		EdgeTypes: map[string]okf.EdgeType{"derived_from": {From: []string{"table"}}},
	}
	withEdge := &okf.Document{Type: "metric"}
	if err := withEdge.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/b", Type: "derived_from"}}}); err != nil {
		t.Fatalf("graph: %v", err)
	}
	rejecting.Bodies = &bodyStub{docs: map[string]*okf.Document{"metrics/a": withEdge}}
	if _, _, _, err := rejecting.SyncWith(context.Background(), "metrics/a", schema); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ontology: %v", err)
	}

	indexDown := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}},
		Meta:   &metaStub{},
		Index: &indexStub{upsert: func(meta.GraphNodeRecord, []meta.GraphEdgeRecord) error {
			return boom("upsert")
		}},
	}
	if _, _, _, err := indexDown.SyncWith(context.Background(), "metrics/a", okf.GraphSchema{}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("upsert: %v", err)
	}
}

// --- projector: rebuild ----------------------------------------------------

func TestRebuildErrorPaths(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	p := &Projector{Bodies: &bodyStub{}, Meta: &metaStub{}, Index: &indexStub{}}
	if _, err := p.Rebuild(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}

	p = &Projector{Bodies: &bodyStub{listErr: boom("list")}, Meta: &metaStub{}, Index: &indexStub{}}
	if _, err := p.Rebuild(context.Background()); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("list: %v", err)
	}

	doc := &okf.Document{Type: "metric", Title: "A"}
	okBody := func(extra map[string]*okf.Document) *bodyStub {
		docs := map[string]*okf.Document{"metrics/a": doc}
		for id, d := range extra {
			docs[id] = d
		}
		return &bodyStub{docs: docs, list: []string{"metrics/a"}}
	}

	// An unreadable document is a warning, not a failure.
	unreadable := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}, list: []string{"metrics/a"}, getErr: map[string]error{"metrics/a": boom("get")}},
		Meta:   &metaStub{}, Index: &indexStub{},
	}
	report, err := unreadable.Rebuild(context.Background())
	if err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	// The same unreadable id is also a schema candidate, so both warnings appear.
	want := []string{"unreadable_document: metrics/a", "unreadable_schema_document: metrics/a"}
	if len(report.Warnings) != len(want) || report.Warnings[0] != want[0] || report.Warnings[1] != want[1] {
		t.Fatalf("warnings: %v", report.Warnings)
	}

	metaDown := &Projector{Bodies: okBody(nil), Meta: &metaStub{metaErr: map[string]error{"metrics/a": boom("meta")}}, Index: &indexStub{}}
	if _, err := metaDown.Rebuild(context.Background()); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("meta: %v", err)
	}

	malformed, err := okf.ParseDocument([]byte("---\ntype: metric\ngraph: nope\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	invalidDecl := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": malformed}, list: []string{"metrics/a"}},
		Meta:   &metaStub{}, Index: &indexStub{},
	}
	report, err = invalidDecl.Rebuild(context.Background())
	if err != nil {
		t.Fatalf("invalid: %v", err)
	}
	if len(report.Warnings) != 1 || report.Warnings[0] != "invalid_declaration: metrics/a" {
		t.Fatalf("warnings: %v", report.Warnings)
	}

	firstUpsert := &Projector{
		Bodies: okBody(nil), Meta: &metaStub{},
		Index: &indexStub{upsert: func(meta.GraphNodeRecord, []meta.GraphEdgeRecord) error { return boom("upsert") }},
	}
	if _, err := firstUpsert.Rebuild(context.Background()); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("first upsert: %v", err)
	}

	// A rejected declaration deletes the half-indexed node; a failing delete and
	// a failing second upsert both abort the rebuild.
	rejected := &okf.Document{Type: "metric"}
	if err := rejected.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/b", Type: "derived_from"}}}); err != nil {
		t.Fatalf("graph: %v", err)
	}
	schema := okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}},
		EdgeTypes: map[string]okf.EdgeType{"derived_from": {From: []string{"table"}}},
	}
	schemaDoc := &okf.Document{Type: "ontology-schema"}
	if err := schemaDoc.SetGraphSchema(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	bodies := &bodyStub{
		docs: map[string]*okf.Document{"metrics/a": rejected, "ontology": schemaDoc},
		list: []string{"metrics/a", "ontology"},
	}
	deleteDown := &Projector{Bodies: bodies, Meta: &metaStub{}, Index: &indexStub{del: func(string) error { return boom("delete") }}}
	if _, err := deleteDown.Rebuild(context.Background()); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("delete: %v", err)
	}
	rejectedReport, err := (&Projector{Bodies: bodies, Meta: &metaStub{}, Index: &indexStub{}}).Rebuild(context.Background())
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if len(rejectedReport.Warnings) == 0 || rejectedReport.Warnings[0] != "declaration_rejected: metrics/a" {
		t.Fatalf("warnings: %v", rejectedReport.Warnings)
	}

	valid := &okf.Document{Type: "metric"}
	if err := valid.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/b", Type: "derived_from"}}}); err != nil {
		t.Fatalf("graph: %v", err)
	}
	allowAll := &okf.Document{Type: schemaDocumentType}
	if err := allowAll.SetGraphSchema(okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}},
		EdgeTypes: map[string]okf.EdgeType{"derived_from": {From: []string{"metric"}, To: []string{"metric"}}},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	secondUpsert := &Projector{
		Bodies: &bodyStub{
			docs: map[string]*okf.Document{"metrics/a": valid, "ontology": allowAll},
			list: []string{"metrics/a", "ontology"},
		},
		Meta: &metaStub{},
		Index: &indexStub{
			get: func(string) (meta.GraphNodeRecord, error) {
				return meta.GraphNodeRecord{ConceptID: "metrics/b", NodeType: "metric"}, nil
			},
			upsert: func(_ meta.GraphNodeRecord, edges []meta.GraphEdgeRecord) error {
				if len(edges) > 0 {
					return boom("second upsert")
				}
				return nil
			},
		},
	}
	if _, err := secondUpsert.Rebuild(context.Background()); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("second upsert: %v", err)
	}

	pruneDown := &Projector{Bodies: okBody(nil), Meta: &metaStub{}, Index: &indexStub{prune: func([]string) (int, error) { return 0, boom("prune") }}}
	if _, err := pruneDown.Rebuild(context.Background()); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("prune: %v", err)
	}
}

func TestContentIdentityReportsMarshalFailures(t *testing.T) {
	previous := marshalDocument
	t.Cleanup(func() { marshalDocument = previous })
	marshalDocument = func(*okf.Document) ([]byte, error) { return nil, boom("marshal") }

	if _, _, err := contentIdentity(&okf.Document{Type: "metric"}, meta.ConceptMeta{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("identity: %v", err)
	}
	p := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": {Type: "metric"}}},
		Meta:   &metaStub{},
		Index:  &indexStub{},
	}
	if _, _, err := p.EnsureFresh(context.Background(), "metrics/a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ensure fresh: %v", err)
	}
}

func TestEnsureFreshErrorPaths(t *testing.T) {
	p := &Projector{Bodies: &bodyStub{}, Meta: &metaStub{}, Index: &indexStub{}}
	if _, _, err := p.EnsureFresh(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid id: %v", err)
	}
	if _, _, err := p.EnsureFresh(context.Background(), "metrics/absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing body: %v", err)
	}

	doc := &okf.Document{Type: "metric"}
	down := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}},
		Meta:   &metaStub{},
		Index:  &indexStub{get: func(string) (meta.GraphNodeRecord, error) { return meta.GraphNodeRecord{}, boom("index") }},
	}
	if _, _, err := down.EnsureFresh(context.Background(), "metrics/a"); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("index: %v", err)
	}

	// A recognised "no row" answer lets the projection run, whose failure surfaces.
	rejecting := &Projector{
		Bodies: &bodyStub{docs: map[string]*okf.Document{"metrics/a": doc}},
		Meta:   &metaStub{metaErr: map[string]error{"metrics/a": boom("meta")}},
		Index:  meta.NewFakeStore(),
	}
	if _, _, err := rejecting.EnsureFresh(context.Background(), "metrics/a"); !errors.Is(err, ErrUnhealthy) || !strings.Contains(err.Error(), "meta failed") {
		t.Fatalf("projection: %v", err)
	}
}

func TestEnsureFreshLogsProjectionWarnings(t *testing.T) {
	doc := &okf.Document{Type: "metric"}
	if err := doc.SetGraph(okf.GraphDecl{Edges: []okf.GraphEdge{{To: "metrics/b", Type: "links_to"}}}); err != nil {
		t.Fatalf("graph: %v", err)
	}
	schema := &okf.Document{Type: "ontology-schema"}
	if err := schema.SetGraphSchema(okf.GraphSchema{
		NodeTypes: map[string]okf.NodeType{"metric": {}},
		EdgeTypes: map[string]okf.EdgeType{"derived_from": {}},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}
	p := &Projector{
		Bodies: &bodyStub{
			docs: map[string]*okf.Document{"metrics/a": doc, "ontology": schema},
			list: []string{"metrics/a", "ontology"},
		},
		Meta:  &metaStub{},
		Index: meta.NewFakeStore(),
		Log:   slog.New(slog.DiscardHandler),
	}
	node, rebuilt, err := p.EnsureFresh(context.Background(), "metrics/a")
	if err != nil {
		t.Fatalf("ensure fresh: %v", err)
	}
	if !rebuilt || node.ConceptID != "metrics/a" {
		t.Fatalf("result: %+v %v", node, rebuilt)
	}
}

func TestProjectorLoggerFallsBackToTheDefault(t *testing.T) {
	if got := (&Projector{}).logger(); got == nil {
		t.Fatal("a nil logger must fall back to slog.Default")
	}
	custom := slog.New(slog.DiscardHandler)
	if got := (&Projector{Log: custom}).logger(); got != custom {
		t.Fatal("a configured logger must win")
	}
}
