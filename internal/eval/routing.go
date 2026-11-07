package eval

import "sort"

// RoutingSample is one gold routing decision (expected target set).
type RoutingSample struct {
	Query    string   `json:"query"`
	Expected []string `json:"expected"`
	Actual   []string `json:"actual"`
}

// RoutingReport summarises rule/System-One decision quality.
type RoutingReport struct {
	Samples    int     `json:"samples"`
	ExactMatch float64 `json:"exact_match"`
	Precision  float64 `json:"precision"`
	Recall     float64 `json:"recall"`
}

func normalise(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EvaluateRouting measures target-set accuracy on a gold dataset.
func EvaluateRouting(samples []RoutingSample) RoutingReport {
	rep := RoutingReport{Samples: len(samples)}
	if len(samples) == 0 {
		return rep
	}
	for _, s := range samples {
		expected, actual := normalise(s.Expected), normalise(s.Actual)
		if sameSet(expected, actual) {
			rep.ExactMatch++
		}
		want := goldSet(expected)
		hit := 0
		for _, a := range actual {
			if want[a] {
				hit++
			}
		}
		if len(actual) > 0 {
			rep.Precision += float64(hit) / float64(len(actual))
		}
		if len(expected) > 0 {
			rep.Recall += float64(hit) / float64(len(expected))
		}
	}
	n := float64(len(samples))
	rep.ExactMatch, rep.Precision, rep.Recall = rep.ExactMatch/n, rep.Precision/n, rep.Recall/n
	return rep
}
