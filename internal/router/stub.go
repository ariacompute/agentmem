package router

import (
	"context"
	"hash/fnv"
)

// StubDecisioner is an offline, deterministic System One replacement used by
// CI. It never calls the network and always returns a stable decision.
type StubDecisioner struct{}

func NewStubDecisioner() *StubDecisioner { return &StubDecisioner{} }

func (s *StubDecisioner) Decide(ctx context.Context, task DecisionTask) (RouteDecision, error) {
	if err := ctx.Err(); err != nil {
		return RouteDecision{}, err
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(task.Text))
	seed := hash.Sum32()
	rag := firstNonCanonical(task.Eligible)
	if task.Kind == "read" {
		if task.Features.QueryKind == "definition" || rag == "" {
			return RouteDecision{
				Targets: []string{CanonicalTarget}, Confidence: 0.5,
				Reason: "stub: canonical-first", RuleID: "stub_read", Source: SourceSystemOne,
			}, nil
		}
		return RouteDecision{
			Targets: []string{rag}, Confidence: 0.5,
			Reason: "stub: long-tail retrieval", RuleID: "stub_read", Source: SourceSystemOne,
		}, nil
	}
	targets := []string{CanonicalTarget}
	if rag != "" && seed%2 == 0 {
		targets = append(targets, rag)
	}
	return RouteDecision{
		Targets: targets, Confidence: 0.5,
		Reason: "stub: deterministic write routing", RuleID: "stub_write", Source: SourceSystemOne,
	}, nil
}
