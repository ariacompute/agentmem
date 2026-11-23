package graph

import (
	"math"
	"sort"
)

// Ranked is one entry of one ranked list.
type Ranked struct {
	ID     string `json:"id"`
	Source string `json:"source"` // "text" | "graph" | "vector"
	Rank   int    `json:"rank"`   // 1-based; 0 means "use the slice position"
}

// Fused is one merged hit.
type Fused struct {
	ID      string   `json:"id"`
	Score   float64  `json:"score"`
	Rank    int      `json:"rank"`
	Sources []string `json:"sources"`
}

// RRF fuses ranked lists with Reciprocal Rank Fusion. The score is the sum of
// 1/(k+rank) over every list that contains the id; ties break on id so the
// output is deterministic. RRF is preferred over score normalisation because
// the participating rankers use incomparable scales.
func RRF(lists [][]Ranked, k int) []Fused {
	if k <= 0 {
		k = 60
	}
	acc := map[string]*Fused{}
	for _, list := range lists {
		seen := map[string]bool{}
		for i, item := range list {
			if item.ID == "" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			rank := item.Rank
			if rank <= 0 {
				rank = i + 1
			}
			f, ok := acc[item.ID]
			if !ok {
				f = &Fused{ID: item.ID, Sources: []string{}}
				acc[item.ID] = f
			}
			f.Score += 1 / float64(k+rank)
			if item.Source != "" && !contains(f.Sources, item.Source) {
				f.Sources = append(f.Sources, item.Source)
			}
		}
	}
	out := make([]Fused, 0, len(acc))
	for _, f := range acc {
		f.Score = math.Round(f.Score*1e6) / 1e6
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}
