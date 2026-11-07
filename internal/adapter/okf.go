package adapter

import (
	"context"
	"sort"
	"strings"

	"agentmem/internal/okf"
)

// OKFAdapter exposes the canonical bundle as a retrieval channel. It is
// deterministic path/title/description lookup, not a probabilistic index.
type OKFAdapter struct {
	bundle *okf.Bundle
}

func NewOKFAdapter(b *okf.Bundle) *OKFAdapter { return &OKFAdapter{bundle: b} }

func (a *OKFAdapter) Name() string { return "okf" }
func (a *OKFAdapter) Capabilities() Capabilities {
	return Capabilities{Put: false, Query: true, Delete: false}
}
func (a *OKFAdapter) Fingerprint() string { return "okf/canonical-v1" }
func (a *OKFAdapter) Health(ctx context.Context) error {
	if a.bundle == nil {
		return ErrUnsupported
	}
	return ctx.Err()
}

// Put is intentionally unsupported: only OKFStore writes the canonical body.
func (a *OKFAdapter) Put(ctx context.Context, conceptID string, doc *okf.Document) error {
	return ErrUnsupported
}

func (a *OKFAdapter) Delete(ctx context.Context, conceptID string) error {
	return ErrUnsupported
}

func (a *OKFAdapter) Query(ctx context.Context, q Query) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := a.bundle.List()
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	queryTokens := tokenSet(q.Text)
	var hits []Hit
	for _, id := range ids {
		d, err := a.bundle.Get(id)
		if err != nil {
			continue // tolerate unreadable concepts
		}
		score := 0.0
		lowerID := strings.ToLower(id)
		if needle != "" && lowerID == needle {
			score = 1.0
		} else if len(queryTokens) == 0 {
			score = 0.5
		} else {
			haystack := tokenSet(id + " " + d.Title + " " + d.Description)
			ratio := overlapScore(queryTokens, haystack)
			if ratio > 0 {
				score = 0.5 + 0.4*ratio
				if strings.Contains(lowerID, needle) {
					score = 0.95
				}
			}
		}
		if score == 0 {
			continue
		}
		snippet := d.Description
		if snippet == "" {
			snippet = d.Title
		}
		hits = append(hits, Hit{
			ConceptID: id, Score: score, Snippet: snippet, Source: "okf",
			Citation: "okf:/" + id + ".md",
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ConceptID < hits[j].ConceptID
		}
		return hits[i].Score > hits[j].Score
	})
	return truncateHits(hits, q.TopK), nil
}

func truncateHits(in []Hit, topK int) []Hit {
	if topK > 0 && len(in) > topK {
		return in[:topK]
	}
	return in
}
