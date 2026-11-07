package eval

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCoverageVersusHitRateConvention(t *testing.T) {
	ranked := []string{"a", "x", "y"}
	gold := []string{"a", "b"}
	// Coverage recall: 1 of 2 gold docs found.
	if got := RecallAtK(ranked, gold, 3); got != 0.5 {
		t.Fatalf("Recall@3 = %f, want 0.5", got)
	}
	// memo-style hit rate: any relevant hit = 1.
	if got := HitRateAtK(ranked, gold, 3); got != 1 {
		t.Fatalf("HitRate@3 = %f, want 1", got)
	}
}

func TestRankingMetrics(t *testing.T) {
	ranked := []string{"a", "x", "b"}
	gold := []string{"a", "b"}
	if got := PrecisionAtK(ranked, gold, 3); got != 2.0/3 {
		t.Fatalf("Precision@3 = %f", got)
	}
	if got := MRR(ranked, gold); got != 1 {
		t.Fatalf("MRR = %f", got)
	}
	if got := RecallAtK(ranked, gold, 1); got != 0.5 {
		t.Fatalf("Recall@1 = %f", got)
	}
	if got := NDCGAtK(ranked, gold, 3); got <= 0 || got > 1 {
		t.Fatalf("NDCG@3 = %f", got)
	}
	if got := AveragePrecision(ranked, gold); !approx(got, (1.0+2.0/3.0)/2.0) {
		t.Fatalf("AP = %f", got)
	}
}

func TestPerfectAndEmpty(t *testing.T) {
	if got := NDCGAtK([]string{"a"}, []string{"a"}, 1); got != 1 {
		t.Fatalf("perfect NDCG = %f", got)
	}
	if got := RecallAtK([]string{"x"}, []string{}, 5); got != 0 {
		t.Fatalf("empty gold recall = %f", got)
	}
	rep := Evaluate(nil, 5)
	if rep.Samples != 0 || rep.Recall != 0 {
		t.Fatalf("empty report = %+v", rep)
	}
}

func TestEvaluateReportAverages(t *testing.T) {
	samples := []RankedSample{
		{Query: "q1", Ranked: []string{"a", "x"}, Gold: []string{"a"}},
		{Query: "q2", Ranked: []string{"y", "b"}, Gold: []string{"b"}},
	}
	rep := Evaluate(samples, 2)
	if rep.Samples != 2 || rep.Recall != 1 || rep.Precision != 0.5 || !approx(rep.MRR, 0.75) || !approx(rep.MAP, 0.75) {
		t.Fatalf("report = %+v", rep)
	}
}

func TestEvaluateRouting(t *testing.T) {
	samples := []RoutingSample{
		{Query: "q1", Expected: []string{"okf", "memory"}, Actual: []string{"memory", "okf"}},
		{Query: "q2", Expected: []string{"okf"}, Actual: []string{"memory"}},
	}
	rep := EvaluateRouting(samples)
	if rep.ExactMatch != 0.5 || rep.Recall != 0.5 || rep.Precision != 0.5 {
		t.Fatalf("routing report = %+v", rep)
	}
}
