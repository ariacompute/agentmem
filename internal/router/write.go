package router

import (
	"context"
	"fmt"
	"strings"

	"agentmem/internal/adapter"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/store"
)

// ProjectionResult records the outcome of one canonical→RAG projection.
type ProjectionResult struct {
	Target string `json:"target"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// WriteResult is the unified outcome of a routed write.
type WriteResult struct {
	ConceptID   string             `json:"concept_id"`
	ContentHash string             `json:"content_hash"`
	CommitSHA   string             `json:"commit_sha"`
	Decision    RouteDecision      `json:"decision"`
	Projections []ProjectionResult `json:"projections"`
	Unchanged   bool               `json:"unchanged"`
}

// WriteRouter is the single write-path orchestrator: canonical body first,
// metadata transaction second, idempotent projections last.
type WriteRouter struct {
	Store      *store.OKFStore
	Registry   *adapter.Registry
	Decisioner Decisioner
	Evidence   *Evidence
}

func NewWriteRouter(s *store.OKFStore, r *adapter.Registry, d Decisioner) *WriteRouter {
	if d == nil {
		d = NewComposite(NewRuleEngine(), NewStubDecisioner())
	}
	return &WriteRouter{Store: s, Registry: r, Decisioner: d, Evidence: NewEvidence(s.Meta)}
}

func (w *WriteRouter) eligibleTargets() []string {
	seen := map[string]bool{CanonicalTarget: true}
	out := []string{CanonicalTarget}
	for _, name := range w.Registry.Names() {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Put routes a document to the canonical body and/or third-party RAG channels.
// Projection failures never roll back the canonical write; they are persisted
// as failed projections for later migration reconciliation.
func (w *WriteRouter) Put(ctx context.Context, subject, id string, doc *okf.Document) (WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	if doc == nil {
		return WriteResult{}, fmt.Errorf("router: nil document")
	}
	if strings.TrimSpace(subject) == "" {
		return WriteResult{}, fmt.Errorf("router: anonymous write rejected")
	}
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return WriteResult{}, err
	}
	// The owner comes from the authenticated principal, never from the request.
	if err := store.TrustedACL(doc, w.Store.ACL, subject); err != nil {
		return WriteResult{}, err
	}
	canonicalExists := false
	if existing, err := w.Store.Bundle.Get(clean); err == nil {
		canonicalExists = true
		if err := store.AuthorizeUpdate(existing, subject); err != nil {
			return WriteResult{}, err
		}
	}
	task := DecisionTask{
		Kind:      "write",
		ConceptID: clean,
		Text:      Digest(doc),
		Features:  ExtractWriteFeatures(doc, canonicalExists),
		Eligible:  w.eligibleTargets(),
	}
	decision, err := w.Decisioner.Decide(ctx, task)
	if err != nil {
		return WriteResult{}, fmt.Errorf("router: decide: %w", err)
	}
	if err := w.Evidence.Record(ctx, task, decision); err != nil {
		return WriteResult{}, fmt.Errorf("router: audit: %w", err)
	}
	result := WriteResult{ConceptID: clean, Decision: decision}

	if contains(decision.Targets, CanonicalTarget) {
		put, err := w.Store.PutAs(ctx, subject, clean, doc)
		if err != nil {
			return WriteResult{}, err
		}
		result.ContentHash, result.CommitSHA, result.Unchanged = put.ContentHash, put.CommitSHA, put.Unchanged
	} else {
		// Projection-only knowledge: metadata records the hash and visibility,
		// but no body is written to Git (the RAG copy is rebuildable).
		data, err := doc.Marshal()
		if err != nil {
			return WriteResult{}, err
		}
		hash := okf.Hash(data)
		if err := w.Store.Meta.UpsertConcept(ctx, meta.ConceptMeta{
			ConceptID: clean, ContentHash: hash, Visibility: doc.Visibility(),
			Owner: doc.Owner(), Status: "projection-only",
		}); err != nil {
			return WriteResult{}, fmt.Errorf("router: record projection-only metadata: %w", err)
		}
		result.ContentHash = hash
	}

	for _, target := range nonCanonical(decision.Targets) {
		a, ok := w.Registry.Get(target)
		if !ok {
			result.Projections = append(result.Projections, ProjectionResult{
				Target: target, Status: store.ProjectionFailed, Error: "adapter not registered",
			})
			continue
		}
		var putErr error
		if a.Capabilities().Put {
			putErr = a.Put(ctx, clean, doc)
		} else {
			putErr = fmt.Errorf("%w: Put", adapter.ErrUnsupported)
		}
		status := store.ProjectionSynced
		errText := ""
		if putErr != nil {
			status, errText = store.ProjectionFailed, redactError("projection failed", putErr)
			_ = store.RecordProjection(ctx, w.Store.Meta, clean, target, result.ContentHash, a.Fingerprint(), store.ProjectionFailed)
		} else if err := store.RecordProjection(ctx, w.Store.Meta, clean, target, result.ContentHash, a.Fingerprint(), store.ProjectionSynced); err != nil {
			status, errText = store.ProjectionFailed, redactError("projection record failed", err)
		}
		result.Projections = append(result.Projections, ProjectionResult{Target: target, Status: status, Error: errText})
	}
	return result, nil
}
