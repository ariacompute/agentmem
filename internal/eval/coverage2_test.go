package eval

import "testing"

func TestMetricsEdgeCases(t *testing.T) {
	if PrecisionAtK(nil, []string{"a"}, 5) != 0 {
		t.Fatal("empty top-k precision")
	}
	if PrecisionAtK([]string{"a", "b"}, []string{"a"}, 5) != 0.5 {
		t.Fatal("half precision")
	}
	if HitRateAtK([]string{"x"}, []string{"a"}, 5) != 0 {
		t.Fatal("hit rate miss")
	}
	if HitRateAtK([]string{"a"}, []string{"a"}, 5) != 1 {
		t.Fatal("hit rate hit")
	}
	if NDCGAtK([]string{"a"}, nil, 5) != 0 {
		t.Fatal("ndcg with empty gold")
	}
	if MRR([]string{"x"}, []string{"a"}) != 0 {
		t.Fatal("mrr miss")
	}
	if MRR([]string{"a"}, []string{"a"}) != 1 {
		t.Fatal("mrr hit")
	}
	if AveragePrecision([]string{"a"}, nil) != 0 {
		t.Fatal("ap empty gold")
	}
	if AveragePrecision([]string{"x"}, []string{"a"}) != 0 {
		t.Fatal("ap miss")
	}
	if RecallAtK([]string{"a"}, nil, 5) != 0 {
		t.Fatal("recall empty gold")
	}
}

func TestEvaluateEmptyAndDefaultK(t *testing.T) {
	r := Evaluate(nil, 0)
	if r.K != 5 || r.Samples != 0 {
		t.Fatalf("report = %+v", r)
	}
	r2 := Evaluate([]RankedSample{{Query: "q", Ranked: []string{"a"}, Gold: []string{"a"}}}, 5)
	if r2.Samples != 1 || r2.HitRate != 1 {
		t.Fatalf("report2 = %+v", r2)
	}
}
