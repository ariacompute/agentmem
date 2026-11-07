// Package router decides where knowledge is written and where queries are read.
package router

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Decision sources.
const (
	SourceRule      = "rule"
	SourceSystemOne = "systemone"
	SourceCache     = "cache"
	SourceFallback  = "fallback"
)

// CanonicalTarget is the OKF+Git authoritative body.
const CanonicalTarget = "okf"

// RouteDecision is the typed outcome of routing, aligned with AFM-D's
// RouteDecision shape (model/choice + reason + confidence).
type RouteDecision struct {
	Targets    []string `json:"targets"`
	Reason     string   `json:"reason"`
	RuleID     string   `json:"rule_id"`
	Confidence float64  `json:"confidence"`
	Source     string   `json:"source"`
}

// DecisionTask carries features and a text digest, never the document body.
type DecisionTask struct {
	Kind      string   `json:"kind"` // "write" | "read"
	ConceptID string   `json:"concept_id"`
	Text      string   `json:"text"` // query text or short summary
	Features  Features `json:"features"`
	Eligible  []string `json:"eligible"`
}

// Decisioner is the second-level (fallback) decision port.
type Decisioner interface {
	Decide(ctx context.Context, task DecisionTask) (RouteDecision, error)
}

// Composite runs deterministic rules first; only ambiguous tasks reach the
// fallback decisioner. Hard pruning happens before any decision (fail closed).
type Composite struct {
	Rules      *RuleEngine
	Fallback   Decisioner
	AlwaysOn   []string // targets that must always be included on write
	FailClosed bool
}

func NewComposite(rules *RuleEngine, fallback Decisioner) *Composite {
	return &Composite{Rules: rules, Fallback: fallback, AlwaysOn: []string{CanonicalTarget}, FailClosed: true}
}

func (c *Composite) Decide(ctx context.Context, task DecisionTask) (RouteDecision, error) {
	task.Eligible = pruneEligible(task.Eligible)
	if len(task.Eligible) == 0 && c.FailClosed {
		return failClosedDecision("no eligible targets"), nil
	}
	if d, ok := c.Rules.Decide(task); ok {
		return sanitizeDecision(d, task.Eligible), nil
	}
	if c.Fallback == nil {
		return failClosedDecision("no fallback decisioner"), nil
	}
	d, err := c.Fallback.Decide(ctx, task)
	if err != nil {
		return failClosedDecision("fallback error: " + err.Error()), nil
	}
	return sanitizeDecision(d, task.Eligible), nil
}

func failClosedDecision(reason string) RouteDecision {
	return RouteDecision{
		Targets: []string{CanonicalTarget}, Reason: "fail-closed: " + reason,
		RuleID: "fail_closed", Confidence: 0, Source: SourceFallback,
	}
}

// sanitizeDecision enforces eligible-set membership, canonical-first ordering
// and (for writes) the always-on canonical body.
func sanitizeDecision(d RouteDecision, eligible []string) RouteDecision {
	allowed := map[string]bool{}
	for _, e := range eligible {
		allowed[e] = true
	}
	seen := map[string]bool{}
	var targets []string
	for _, t := range d.Targets {
		if allowed[t] && !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return failClosedDecision("decision had no eligible targets")
	}
	// Canonical always leads when present: OKF wins on conflicts.
	sort.SliceStable(targets, func(i, j int) bool {
		return targets[i] == CanonicalTarget && targets[j] != CanonicalTarget
	})
	d.Targets = targets
	if d.Source == "" {
		d.Source = SourceRule
	}
	return d
}

func pruneEligible(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// ErrNoEligibleTargets is returned by helpers that cannot route at all.
var ErrNoEligibleTargets = errors.New("router: no eligible targets")

func contains(in []string, want string) bool {
	for _, x := range in {
		if x == want {
			return true
		}
	}
	return false
}

func firstNonCanonical(in []string) string {
	for _, x := range in {
		if x != CanonicalTarget {
			return x
		}
	}
	return ""
}

func nonCanonical(in []string) []string {
	var out []string
	for _, x := range in {
		if x != CanonicalTarget {
			out = append(out, x)
		}
	}
	return out
}

func validateTarget(t string, eligible []string) error {
	if !contains(eligible, t) {
		return fmt.Errorf("%w: %q", ErrNoEligibleTargets, t)
	}
	return nil
}
