package router

import (
	"context"
	"log/slog"

	"agentmem/internal/meta"
)

// Evidence writes a replayable routing audit trail. Only metadata is stored;
// document bodies, credentials and PII are never logged.
type Evidence struct {
	store meta.MetaStore
}

func NewEvidence(s meta.MetaStore) *Evidence { return &Evidence{store: s} }

func (e *Evidence) Record(ctx context.Context, task DecisionTask, d RouteDecision) error {
	slog.Info("router.decision",
		"kind", task.Kind,
		"concept_id", task.ConceptID,
		"rule_id", d.RuleID,
		"source", d.Source,
		"confidence", d.Confidence,
		"targets", d.Targets,
	)
	if e == nil || e.store == nil {
		return nil
	}
	return e.store.AppendDecision(ctx, meta.DecisionRecord{
		ConceptID:  task.ConceptID,
		RuleID:     d.RuleID,
		Reason:     d.Reason,
		Source:     d.Source,
		Confidence: d.Confidence,
		Targets:    append(meta.StringList(nil), d.Targets...),
	})
}

// List returns recorded decisions for replay/audit.
func (e *Evidence) List(ctx context.Context, conceptID string, limit int) ([]meta.DecisionRecord, error) {
	if e == nil || e.store == nil {
		return []meta.DecisionRecord{}, nil
	}
	records, err := e.store.ListDecisions(ctx, meta.DecisionQuery{ConceptID: conceptID, Limit: limit})
	if err != nil {
		return nil, err
	}
	if records == nil {
		return []meta.DecisionRecord{}, nil
	}
	return records, nil
}
