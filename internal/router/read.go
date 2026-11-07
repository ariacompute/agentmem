package router

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/store"
)

// Conflict records a disagreement between the canonical body and a projection.
// The canonical answer always wins; the conflict is surfaced, never hidden.
type Conflict struct {
	ConceptID string `json:"concept_id"`
	Canonical string `json:"canonical"`
	External  string `json:"external"`
	Source    string `json:"source"`
}

// QueryResult is the merged, auditable read outcome.
type QueryResult struct {
	Query     string            `json:"query"`
	Decision  RouteDecision     `json:"decision"`
	Hits      []adapter.Hit     `json:"hits"`
	Conflicts []Conflict        `json:"conflicts"`
	Errors    map[string]string `json:"errors,omitempty"`
}

// ReadRouter performs read-time routing, concurrent dual-path retrieval and
// canonical-first merging.
type ReadRouter struct {
	Store        *store.OKFStore
	Registry     *adapter.Registry
	Decisioner   Decisioner
	Evidence     *Evidence
	QueryTimeout time.Duration
}

func NewReadRouter(s *store.OKFStore, r *adapter.Registry, d Decisioner) *ReadRouter {
	if d == nil {
		d = NewComposite(NewRuleEngine(), NewStubDecisioner())
	}
	return &ReadRouter{
		Store: s, Registry: r, Decisioner: d,
		Evidence: NewEvidence(s.Meta), QueryTimeout: 5 * time.Second,
	}
}

func (r *ReadRouter) eligibleTargets() []string {
	var out []string
	for _, name := range r.Registry.Names() {
		out = append(out, name)
	}
	if len(out) == 0 {
		out = []string{CanonicalTarget}
	}
	return out
}

func (r *ReadRouter) canonicalHits(text string) int {
	ids, err := r.Store.Bundle.List()
	if err != nil {
		return 0
	}
	needle := strings.ToLower(strings.TrimSpace(text))
	if needle == "" {
		return 0
	}
	count := 0
	for _, id := range ids {
		if strings.Contains(strings.ToLower(id), needle) {
			count++
		}
	}
	return count
}

// Query routes a read, runs all selected channels concurrently (max latency,
// not sum), merges with OKF priority and filters by ACL.
func (r *ReadRouter) Query(ctx context.Context, subject, text string, topK int) (QueryResult, error) {
	if err := ctx.Err(); err != nil {
		return QueryResult{}, err
	}
	if topK <= 0 {
		topK = 5
	}
	task := DecisionTask{
		Kind: "read", Text: text,
		Features: ExtractQueryFeatures(text, r.canonicalHits(text)),
		Eligible: r.eligibleTargets(),
	}
	decision, err := r.Decisioner.Decide(ctx, task)
	if err != nil {
		return QueryResult{}, fmt.Errorf("router: decide query: %w", err)
	}
	if err := r.Evidence.Record(ctx, task, decision); err != nil {
		return QueryResult{}, err
	}
	result := QueryResult{Query: text, Decision: decision, Errors: map[string]string{}}

	queryCtx, cancel := context.WithTimeout(ctx, r.QueryTimeout)
	defer cancel()
	type outcome struct {
		target string
		hits   []adapter.Hit
		err    error
	}
	ch := make(chan outcome, len(decision.Targets))
	var wg sync.WaitGroup
	for _, target := range decision.Targets {
		a, ok := r.Registry.Get(target)
		if !ok {
			result.Errors[target] = "adapter not registered"
			continue
		}
		wg.Add(1)
		go func(target string, a adapter.Adapter) {
			defer wg.Done()
			hits, err := a.Query(queryCtx, adapter.Query{Text: text, TopK: topK})
			ch <- outcome{target: target, hits: hits, err: err}
		}(target, a)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()
	byTarget := map[string][]adapter.Hit{}
	for o := range ch {
		if o.err != nil {
			result.Errors[o.target] = o.err.Error()
			continue
		}
		byTarget[o.target] = o.hits
	}

	hits, conflicts := mergeHits(byTarget, topK)
	filtered := make([]adapter.Hit, 0, len(hits))
	for _, h := range hits {
		ok, err := r.Store.Meta.CheckACL(ctx, subject, h.ConceptID)
		if err != nil || !ok {
			continue
		}
		filtered = append(filtered, h)
	}
	result.Hits = filtered
	result.Conflicts = conflicts
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	return result, nil
}

// mergeHits applies canonical-first precedence and de-duplication.
func mergeHits(byTarget map[string][]adapter.Hit, topK int) ([]adapter.Hit, []Conflict) {
	merged := map[string]adapter.Hit{}
	var conflicts []Conflict
	// Canonical first so its entry wins any id collision.
	targets := make([]string, 0, len(byTarget))
	for t := range byTarget {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i] == CanonicalTarget && targets[j] != CanonicalTarget
	})
	for _, target := range targets {
		for _, h := range byTarget[target] {
			existing, ok := merged[h.ConceptID]
			if !ok {
				merged[h.ConceptID] = h
				continue
			}
			if existing.Source == CanonicalTarget && target != CanonicalTarget {
				if h.Snippet != "" && existing.Snippet != "" && h.Snippet != existing.Snippet {
					conflicts = append(conflicts, Conflict{
						ConceptID: h.ConceptID, Canonical: existing.Snippet,
						External: h.Snippet, Source: target,
					})
				}
				continue // canonical wins
			}
			if h.Score > existing.Score {
				merged[h.ConceptID] = h
			}
		}
	}
	out := make([]adapter.Hit, 0, len(merged))
	for _, h := range merged {
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := out[i].Source == CanonicalTarget, out[j].Source == CanonicalTarget
		if ci != cj {
			return ci
		}
		if out[i].Score == out[j].Score {
			return out[i].ConceptID < out[j].ConceptID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > topK {
		out = out[:topK]
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].ConceptID < conflicts[j].ConceptID })
	return out, conflicts
}
