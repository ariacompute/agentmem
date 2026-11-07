// Package eval provides pure ranking metrics with no LLM judge.
//
// Metric convention (explicit, because the reference projects disagree):
//   - Recall@k is COVERAGE |top-k ∩ relevant| / |relevant|, matching DeepTutor
//     `kb eval` and the track_a report.
//   - HitRate@k is memo/benches' binary "did any relevant doc appear" metric.
//
// Keeping both avoids silently mixing two incompatible definitions.
package eval

import "math"

// RankedSample is one query with a ranked result list and its gold set.
type RankedSample struct {
	Query  string   `json:"query"`
	Ranked []string `json:"ranked"`
	Gold   []string `json:"gold"`
}

// Report aggregates all metrics at one k.
type Report struct {
	K         int     `json:"k"`
	Samples   int     `json:"samples"`
	Recall    float64 `json:"recall_at_k"`
	Precision float64 `json:"precision_at_k"`
	NDCG      float64 `json:"ndcg_at_k"`
	MRR       float64 `json:"mrr"`
	MAP       float64 `json:"map"`
	HitRate   float64 `json:"hit_rate_at_k"`
}

func goldSet(gold []string) map[string]bool {
	m := make(map[string]bool, len(gold))
	for _, g := range gold {
		m[g] = true
	}
	return m
}

func atK(ranked []string, k int) []string {
	if k <= 0 || len(ranked) <= k {
		return ranked
	}
	return ranked[:k]
}

// RecallAtK measures coverage of the gold set within top-k.
func RecallAtK(ranked, gold []string, k int) float64 {
	if len(gold) == 0 {
		return 0
	}
	want := goldSet(gold)
	seen := map[string]bool{}
	hits := 0
	for _, id := range atK(ranked, k) {
		if want[id] && !seen[id] {
			seen[id] = true
			hits++
		}
	}
	return float64(hits) / float64(len(want))
}

// PrecisionAtK is the fraction of the top-k that is relevant.
func PrecisionAtK(ranked, gold []string, k int) float64 {
	top := atK(ranked, k)
	if len(top) == 0 {
		return 0
	}
	want := goldSet(gold)
	hits := 0
	for _, id := range top {
		if want[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(top))
}

// HitRateAtK is 1 when at least one relevant document appears in top-k.
func HitRateAtK(ranked, gold []string, k int) float64 {
	want := goldSet(gold)
	for _, id := range atK(ranked, k) {
		if want[id] {
			return 1
		}
	}
	return 0
}

// NDCGAtK uses binary relevance gains.
func NDCGAtK(ranked, gold []string, k int) float64 {
	want := goldSet(gold)
	dcg := 0.0
	for i, id := range atK(ranked, k) {
		if want[id] {
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	ideal := 0.0
	for i := 0; i < k && i < len(gold); i++ {
		ideal += 1 / math.Log2(float64(i+2))
	}
	if ideal == 0 {
		return 0
	}
	return dcg / ideal
}

// MRR is the reciprocal rank of the first relevant hit (0 when absent).
func MRR(ranked, gold []string) float64 {
	want := goldSet(gold)
	for i, id := range ranked {
		if want[id] {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// AveragePrecision averages precision at each relevant rank.
func AveragePrecision(ranked, gold []string) float64 {
	if len(gold) == 0 {
		return 0
	}
	want := goldSet(gold)
	seen := map[string]bool{}
	hits := 0
	sum := 0.0
	for i, id := range ranked {
		if want[id] && !seen[id] {
			seen[id] = true
			hits++
			sum += float64(hits) / float64(i+1)
		}
	}
	return sum / float64(len(gold))
}

// Evaluate computes the full report over a dataset.
func Evaluate(samples []RankedSample, k int) Report {
	if k <= 0 {
		k = 5
	}
	r := Report{K: k, Samples: len(samples)}
	if len(samples) == 0 {
		return r
	}
	for _, s := range samples {
		r.Recall += RecallAtK(s.Ranked, s.Gold, k)
		r.Precision += PrecisionAtK(s.Ranked, s.Gold, k)
		r.NDCG += NDCGAtK(s.Ranked, s.Gold, k)
		r.MRR += MRR(s.Ranked, s.Gold)
		r.MAP += AveragePrecision(s.Ranked, s.Gold)
		r.HitRate += HitRateAtK(s.Ranked, s.Gold, k)
	}
	n := float64(len(samples))
	r.Recall, r.Precision, r.NDCG = r.Recall/n, r.Precision/n, r.NDCG/n
	r.MRR, r.MAP, r.HitRate = r.MRR/n, r.MAP/n, r.HitRate/n
	return r
}
