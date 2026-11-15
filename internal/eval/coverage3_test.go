package eval

import "testing"

func TestEvaluateRoutingEmptyDataset(t *testing.T) {
	rep := EvaluateRouting(nil)
	if rep.Samples != 0 || rep.ExactMatch != 0 || rep.Precision != 0 || rep.Recall != 0 {
		t.Fatalf("empty report = %+v", rep)
	}
}

func TestEvaluateRoutingMixedSamples(t *testing.T) {
	rep := EvaluateRouting([]RoutingSample{
		{Query: "q1", Expected: []string{"okf"}, Actual: []string{"okf", "memory"}},
		{Query: "q2", Expected: []string{"okf", "memory"}, Actual: []string{"memory", "okf"}},
		{Query: "q3", Expected: []string{"okf"}, Actual: []string{"memory"}},
	})
	if rep.Samples != 3 {
		t.Fatalf("samples = %d", rep.Samples)
	}
	if rep.ExactMatch != 1.0/3.0 {
		t.Fatalf("exact match = %f", rep.ExactMatch)
	}
	if rep.Precision <= 0 || rep.Recall <= 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestSameSetComparesLengthAndContent(t *testing.T) {
	if sameSet([]string{"a"}, []string{"a", "b"}) {
		t.Fatal("different lengths must not match")
	}
	if sameSet([]string{"a", "b"}, []string{"a", "c"}) {
		t.Fatal("different content must not match")
	}
	if !sameSet(nil, nil) {
		t.Fatal("two empty sets must match")
	}
}
