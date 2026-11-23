package graph

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"agentmem/internal/meta"
)

// maxCoverageTerms bounds how many question tokens are resolved, keeping the
// check_answerable call a fixed number of index lookups.
const maxCoverageTerms = 8

// Tokenize splits a question into deterministic search terms. Latin words are
// kept whole; CJK runs are kept as a run so substring matching works without a
// segmenter dependency.
func Tokenize(question string) []string {
	var out []string
	seen := map[string]bool{}
	var cur []rune
	flush := func() {
		if len(cur) == 0 {
			return
		}
		tok := strings.ToLower(string(cur))
		cur = cur[:0]
		if len([]rune(tok)) < 2 || stopWords[tok] || seen[tok] {
			return
		}
		seen[tok] = true
		out = append(out, tok)
	}
	for _, r := range question {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	if len(out) > maxCoverageTerms {
		out = out[:maxCoverageTerms]
	}
	return out
}

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "do": true, "does": true, "for": true, "from": true,
	"how": true, "in": true, "is": true, "it": true, "of": true, "on": true,
	"or": true, "the": true, "to": true, "was": true, "were": true, "what": true,
	"when": true, "where": true, "which": true, "who": true, "why": true, "with": true,
}

// Answerability scores how much of a question the graph can currently support.
// It never invents an answer: a low coverage returns explicit gaps instead.
func (s *Searcher) Answerability(ctx context.Context, subject, question string, minimum float64) (Coverage, error) {
	if minimum <= 0 {
		minimum = 0.6
	}
	if minimum > 1 {
		minimum = 1
	}
	out := Coverage{
		Question: question, Minimum: minimum, Verdict: VerdictInsufficient,
		Matched: []TermMatch{}, Gaps: []Gap{},
	}
	out.Terms = Tokenize(question)
	stats, err := s.Index.GraphStats(ctx, subject)
	if err != nil {
		return Coverage{}, fmt.Errorf("%w: %v", ErrUnhealthy, err)
	}
	if stats.Nodes == 0 {
		out.Gaps = append(out.Gaps, Gap{Kind: GapEmptyIndex, Detail: "the graph index has no readable node"})
		return out, nil
	}
	if len(out.Terms) == 0 {
		out.Gaps = append(out.Gaps, Gap{Kind: GapMissingTerm, Detail: "the question has no searchable term"})
		return out, nil
	}
	memo := map[string]bool{}
	matched := map[string]meta.GraphNodeRecord{}
	for _, term := range out.Terms {
		hits, err := s.Index.SearchGraphNodes(ctx, meta.GraphSearch{Text: term, Limit: 5})
		if err != nil {
			return Coverage{}, fmt.Errorf("%w: %v", ErrUnhealthy, err)
		}
		found := false
		for i, h := range hits {
			ok, err := s.allowed(ctx, subject, h.ConceptID, memo)
			if err != nil {
				return Coverage{}, err
			}
			if !ok {
				continue
			}
			out.Matched = append(out.Matched, TermMatch{
				Term: term, ConceptID: h.ConceptID, Label: h.Label,
				NodeType: h.NodeType, Score: round3(1 / float64(i+1)),
			})
			matched[h.ConceptID] = h
			found = true
			break
		}
		if !found {
			out.Gaps = append(out.Gaps, Gap{Kind: GapMissingTerm, Detail: term})
		}
	}
	tokenCoverage := float64(len(out.Terms)-countKind(out.Gaps, GapMissingTerm)) / float64(len(out.Terms))

	ids := make([]string, 0, len(matched))
	for id := range matched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	stale, _ := s.Index.StaleGraphNodes(ctx, ids)
	verified, sourced, fresh := 0, 0, 0
	checks := 0
	schema, _, _ := s.Schema(ctx)
	for _, id := range ids {
		n := matched[id]
		if n.Trust != "unverified" {
			verified++
		}
		if n.SourceCount > 0 {
			sourced++
		}
		if !stale[id] {
			fresh++
		} else {
			out.Gaps = append(out.Gaps, Gap{Kind: GapStale, ConceptID: id, Detail: "the body changed after the last index run"})
		}
		if n.Trust == "unverified" {
			out.Gaps = append(out.Gaps, Gap{Kind: GapUnverified, ConceptID: id, Detail: "the concept has no verified signal"})
		}
		if n.SourceCount == 0 {
			out.Gaps = append(out.Gaps, Gap{Kind: GapNoSource, ConceptID: id, Detail: "the concept cites no source"})
		}
		if checks >= 200 {
			continue
		}
		edges, err := s.Index.GraphEdges(ctx, []string{id}, "out")
		if err != nil {
			return Coverage{}, fmt.Errorf("%w: %v", ErrUnhealthy, err)
		}
		for _, e := range edges {
			checks++
			if checks > 200 {
				break
			}
			if _, ok := schema.EdgeTypes[e.EdgeType]; !ok && !schema.Empty() {
				out.Gaps = append(out.Gaps, Gap{Kind: GapUnknownEdgeType, ConceptID: id, Detail: e.EdgeType})
			}
			if _, err := s.Index.GetGraphNode(ctx, e.ToID); err != nil {
				out.Gaps = append(out.Gaps, Gap{Kind: GapDanglingEdge, ConceptID: id, Detail: e.EdgeType + " -> " + e.ToID})
			}
		}
	}
	total := float64(len(ids))
	if total == 0 {
		total = 1
	}
	quality := 0.6*tokenCoverage +
		0.15*float64(verified)/total +
		0.15*float64(sourced)/total +
		0.1*float64(fresh)/total
	out.Coverage = round3(quality)
	switch {
	case out.Coverage >= minimum:
		out.Verdict = VerdictAnswerable
	case out.Coverage >= round3(minimum*0.6):
		out.Verdict = VerdictPartial
	default:
		out.Verdict = VerdictInsufficient
	}
	// A purely unverified, unsourced match is never certified as answerable,
	// however many question terms it covers.
	if out.Verdict == VerdictAnswerable && (verified == 0 || sourced == 0) {
		out.Verdict = VerdictPartial
	}
	out.Gaps = capGaps(out.Gaps, 50)
	return out, nil
}

func countKind(gaps []Gap, kind string) int {
	n := 0
	for _, g := range gaps {
		if g.Kind == kind {
			n++
		}
	}
	return n
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func capGaps(gaps []Gap, max int) []Gap {
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].Kind != gaps[j].Kind {
			return gaps[i].Kind < gaps[j].Kind
		}
		if gaps[i].ConceptID != gaps[j].ConceptID {
			return gaps[i].ConceptID < gaps[j].ConceptID
		}
		return gaps[i].Detail < gaps[j].Detail
	})
	seen := map[string]bool{}
	out := make([]Gap, 0, len(gaps))
	for _, g := range gaps {
		key := g.Kind + "\x00" + g.ConceptID + "\x00" + g.Detail
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(out) >= max {
			break
		}
		out = append(out, g)
	}
	return out
}
