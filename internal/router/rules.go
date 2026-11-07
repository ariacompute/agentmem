package router

import "fmt"

// LargeDocBytes is the threshold above which long-tail RAG projection is added.
const LargeDocBytes = 4096

// Rule is one deterministic, ordered, explainable routing rule.
type Rule struct {
	ID    string
	Match func(DecisionTask) (RouteDecision, bool)
}

// RuleEngine short-circuits on the first matching rule.
type RuleEngine struct{ rules []Rule }

func NewRuleEngine(rules ...[]Rule) *RuleEngine {
	var all []Rule
	for _, group := range rules {
		all = append(all, group...)
	}
	if len(all) == 0 {
		all = DefaultRules()
	}
	return &RuleEngine{rules: all}
}

func (e *RuleEngine) Decide(task DecisionTask) (RouteDecision, bool) {
	for _, r := range e.rules {
		if d, ok := r.Match(task); ok {
			d.RuleID = r.ID
			d.Source = SourceRule
			return d, true
		}
	}
	return RouteDecision{}, false
}

func (e *RuleEngine) RuleIDs() []string {
	out := make([]string, 0, len(e.rules))
	for _, r := range e.rules {
		out = append(out, r.ID)
	}
	return out
}

// DefaultRules implements the ordered rule table. Anything unmatched is
// ambiguous and falls through to System One.
func DefaultRules() []Rule {
	decide := func(id string, fn func(DecisionTask) (RouteDecision, bool)) Rule {
		return Rule{ID: id, Match: fn}
	}
	return []Rule{
		decide("R1_authoritative_write", func(t DecisionTask) (RouteDecision, bool) {
			if t.Kind != "write" || !t.Features.Authoritative {
				return RouteDecision{}, false
			}
			return RouteDecision{
				Targets: []string{CanonicalTarget}, Confidence: 1.0,
				Reason: "authoritative definition belongs to the OKF+Git canonical body",
			}, true
		}),
		decide("R2_sensitive_write", func(t DecisionTask) (RouteDecision, bool) {
			if t.Kind != "write" || !t.Features.Sensitive {
				return RouteDecision{}, false
			}
			return RouteDecision{
				Targets: []string{CanonicalTarget}, Confidence: 0.95,
				Reason: "sensitive/private knowledge is not projected to third-party RAG",
			}, true
		}),
		decide("R3_definition_query", func(t DecisionTask) (RouteDecision, bool) {
			if t.Kind != "read" || t.Features.QueryKind != "definition" {
				return RouteDecision{}, false
			}
			return RouteDecision{
				Targets: []string{CanonicalTarget}, Confidence: 1.0,
				Reason: "exact/definition query requires the canonical, auditable answer",
			}, true
		}),
		decide("R4_canonical_update", func(t DecisionTask) (RouteDecision, bool) {
			if t.Kind != "write" || !t.Features.HasCanonical {
				return RouteDecision{}, false
			}
			targets := []string{CanonicalTarget}
			if rag := firstNonCanonical(t.Eligible); rag != "" {
				targets = append(targets, rag)
			}
			return RouteDecision{
				Targets: targets, Confidence: 0.9,
				Reason: "updating an existing canonical concept, refresh its primary projection",
			}, true
		}),
		decide("R5_large_document", func(t DecisionTask) (RouteDecision, bool) {
			if t.Kind != "write" || t.Features.ByteSize < LargeDocBytes {
				return RouteDecision{}, false
			}
			targets := append([]string{CanonicalTarget}, nonCanonical(t.Eligible)...)
			return RouteDecision{
				Targets: targets, Confidence: 0.85,
				Reason: fmt.Sprintf("large unstructured body (%d bytes) needs long-tail retrieval", t.Features.ByteSize),
			}, true
		}),
		decide("R6_exploratory_query", func(t DecisionTask) (RouteDecision, bool) {
			if t.Kind != "read" || t.Features.QueryKind != "exploratory" {
				return RouteDecision{}, false
			}
			targets := nonCanonical(t.Eligible)
			if len(targets) == 0 {
				targets = []string{CanonicalTarget}
			}
			return RouteDecision{
				Targets: targets, Confidence: 0.8,
				Reason: "exploratory query benefits from broad probabilistic retrieval",
			}, true
		}),
		decide("R7_aggregate_dual", func(t DecisionTask) (RouteDecision, bool) {
			if t.Kind != "read" || t.Features.QueryKind != "aggregate" {
				return RouteDecision{}, false
			}
			targets := append([]string{CanonicalTarget}, nonCanonical(t.Eligible)...)
			return RouteDecision{
				Targets: targets, Confidence: 0.75,
				Reason: "aggregate query uses canonical facts plus long-tail evidence",
			}, true
		}),
	}
}
