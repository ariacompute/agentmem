package router

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"agentmem/internal/adapter"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/store"
)

// Migration directions.
const (
	DirectionToRAG       = "canonical_to_rag"
	DirectionToCanonical = "rag_to_canonical"
)

// MigrationOptions controls an incremental reconciliation job.
type MigrationOptions struct {
	Job       string `json:"job"`
	Direction string `json:"direction"`
	Target    string `json:"target"`
	DryRun    bool   `json:"dry_run"`
	Limit     int    `json:"limit"`
}

// MigrationReport is a resumable, idempotent job report.
type MigrationReport struct {
	Job           string   `json:"job"`
	Direction     string   `json:"direction"`
	Target        string   `json:"target"`
	DryRun        bool     `json:"dry_run"`
	Scanned       int      `json:"scanned"`
	Planned       int      `json:"planned"`
	Synced        int      `json:"synced"`
	Skipped       int      `json:"skipped"`
	Failed        int      `json:"failed"`
	Invalidated   int      `json:"invalidated"`
	LastConceptID string   `json:"last_concept_id"`
	Done          bool     `json:"done"`
	Errors        []string `json:"errors,omitempty"`
}

// Migrator reconciles the canonical body with rebuildable RAG projections.
// Cursors live in PostgreSQL so a job can resume after interruption.
type Migrator struct {
	Store    *store.OKFStore
	Registry *adapter.Registry
}

func NewMigrator(s *store.OKFStore, r *adapter.Registry) *Migrator {
	return &Migrator{Store: s, Registry: r}
}

func (m *Migrator) Run(ctx context.Context, opt MigrationOptions) (MigrationReport, error) {
	if err := ctx.Err(); err != nil {
		return MigrationReport{}, err
	}
	if opt.Job == "" {
		opt.Job = opt.Direction + ":" + opt.Target
	}
	if opt.Limit <= 0 {
		opt.Limit = 100
	}
	a, ok := m.Registry.Get(opt.Target)
	if !ok || opt.Target == CanonicalTarget {
		return MigrationReport{}, fmt.Errorf("router: migration target %q is not a RAG adapter", opt.Target)
	}
	report := MigrationReport{
		Job: opt.Job, Direction: opt.Direction, Target: opt.Target, DryRun: opt.DryRun,
	}
	cursor, err := m.Store.Meta.MigrationCursor(ctx, opt.Job)
	if err != nil {
		return report, err
	}
	report.LastConceptID = cursor.LastConceptID
	switch opt.Direction {
	case DirectionToRAG:
		err = m.toRAG(ctx, opt, a, &report, &cursor)
	case DirectionToCanonical:
		err = m.toCanonical(ctx, opt, a, &report, &cursor)
	default:
		return report, fmt.Errorf("router: unknown migration direction %q", opt.Direction)
	}
	if err != nil {
		return report, err
	}
	if !opt.DryRun {
		if err := m.Store.Meta.SaveMigrationCursor(ctx, opt.Job, cursor); err != nil {
			return report, err
		}
	}
	report.LastConceptID = cursor.LastConceptID
	return report, nil
}

func (m *Migrator) toRAG(ctx context.Context, opt MigrationOptions, a adapter.Adapter, rep *MigrationReport, cur *meta.Cursor) error {
	ids, err := m.Store.Bundle.List()
	if err != nil {
		return err
	}
	sort.Strings(ids)
	pending := make([]string, 0, len(ids))
	for _, id := range ids {
		if id > cur.LastConceptID {
			pending = append(pending, id)
		}
	}
	rep.Done = len(pending) <= opt.Limit
	if len(pending) > opt.Limit {
		pending = pending[:opt.Limit]
	}
	fp := a.Fingerprint()
	for _, id := range pending {
		rep.Scanned++
		hash, err := m.hashOf(id)
		if err != nil {
			rep.Failed++
			rep.Errors = append(rep.Errors, id+": "+err.Error())
			continue
		}
		projections, err := m.Store.Meta.ListProjections(ctx, meta.ProjectionQuery{ConceptID: id, Adapter: opt.Target})
		if err != nil {
			return err
		}
		upToDate := false
		for _, p := range projections {
			if p.Status == store.ProjectionSynced && p.ContentHash == hash && p.Fingerprint == fp {
				upToDate = true
			}
		}
		if upToDate {
			rep.Skipped++
		} else {
			rep.Planned++
			if !opt.DryRun {
				doc, err := m.Store.Bundle.Get(id)
				if err != nil {
					rep.Failed++
					rep.Errors = append(rep.Errors, id+": "+err.Error())
				} else if err := a.Put(ctx, id, doc); err != nil {
					rep.Failed++
					rep.Errors = append(rep.Errors, id+": "+err.Error())
					_ = store.RecordProjection(ctx, m.Store.Meta, id, opt.Target, hash, fp, store.ProjectionFailed)
				} else if err := store.RecordProjection(ctx, m.Store.Meta, id, opt.Target, hash, fp, store.ProjectionSynced); err != nil {
					rep.Failed++
					rep.Errors = append(rep.Errors, id+": "+err.Error())
				} else {
					rep.Synced++
				}
			}
		}
		cur.LastConceptID = id
	}
	return nil
}

// toCanonical audits projections from the RAG side: orphans (no canonical
// body) are deleted and marked stale, drifted hashes are flagged for rebuild.
func (m *Migrator) toCanonical(ctx context.Context, opt MigrationOptions, a adapter.Adapter, rep *MigrationReport, cur *meta.Cursor) error {
	projections, err := m.Store.Meta.ListProjections(ctx, meta.ProjectionQuery{Adapter: opt.Target})
	if err != nil {
		return err
	}
	sort.Slice(projections, func(i, j int) bool { return projections[i].ConceptID < projections[j].ConceptID })
	pending := make([]meta.Projection, 0, len(projections))
	for _, p := range projections {
		if p.ConceptID > cur.LastConceptID {
			pending = append(pending, p)
		}
	}
	rep.Done = len(pending) <= opt.Limit
	if len(pending) > opt.Limit {
		pending = pending[:opt.Limit]
	}
	for _, p := range pending {
		rep.Scanned++
		hash, err := m.hashOf(p.ConceptID)
		orphan := err != nil && errors.Is(err, os.ErrNotExist)
		if err != nil && !orphan {
			rep.Failed++
			rep.Errors = append(rep.Errors, p.ConceptID+": "+err.Error())
			continue
		}
		if !orphan && p.ContentHash == hash && p.Status == store.ProjectionSynced {
			rep.Skipped++
		} else {
			rep.Planned++
			if !opt.DryRun {
				if orphan {
					if err := a.Delete(ctx, p.ConceptID); err != nil && !errors.Is(err, adapter.ErrUnsupported) {
						rep.Failed++
						rep.Errors = append(rep.Errors, p.ConceptID+": "+err.Error())
					} else {
						rep.Invalidated++
					}
				} else {
					rep.Invalidated++
				}
				_ = store.RecordProjection(ctx, m.Store.Meta, p.ConceptID, opt.Target, p.ContentHash, p.Fingerprint, store.ProjectionStale)
			}
		}
		cur.LastConceptID = p.ConceptID
	}
	return nil
}

func (m *Migrator) hashOf(id string) (string, error) {
	p, err := m.Store.Bundle.Path(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return okf.Hash(data), nil
}
