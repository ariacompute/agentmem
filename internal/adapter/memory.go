package adapter

import (
	"context"
	"sort"
	"strings"
	"sync"
	"unicode"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// MemoryAdapter is an in-process deterministic index combining lexical token
// overlap with hashing embeddings. It is the offline default RAG channel.
// Complexity is O(N*d); large corpora must move to pgvector ANN.
type MemoryAdapter struct {
	mu       sync.RWMutex
	docs     map[string]memoryDoc
	embedder Embedder
}

type memoryDoc struct {
	text   string
	vector meta.Vector
}

func NewMemoryAdapter(e Embedder) *MemoryAdapter {
	if e == nil {
		e = NewHashingEmbedder(64)
	}
	return &MemoryAdapter{docs: map[string]memoryDoc{}, embedder: e}
}

func (a *MemoryAdapter) Name() string { return "memory" }
func (a *MemoryAdapter) Capabilities() Capabilities {
	return Capabilities{Put: true, Query: true, Delete: true, Vector: true, Semantic: true}
}
func (a *MemoryAdapter) Fingerprint() string { return FingerprintFor("memory", a.embedder) }
func (a *MemoryAdapter) Health(ctx context.Context) error {
	return ctx.Err()
}

func (a *MemoryAdapter) Put(ctx context.Context, conceptID string, d *okf.Document) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	text := docText(d)
	v, err := a.embedder.Embed(ctx, text)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.docs[conceptID] = memoryDoc{text: text, vector: v}
	return nil
}

func (a *MemoryAdapter) Query(ctx context.Context, q Query) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v := q.Vector
	if len(v) == 0 {
		var err error
		v, err = a.embedder.Embed(ctx, q.Text)
		if err != nil {
			return nil, err
		}
	}
	queryTokens := tokenSet(q.Text)
	a.mu.RLock()
	defer a.mu.RUnlock()
	var hits []Hit
	for id, stored := range a.docs {
		vectorScore := cosineVectors(v, stored.vector)
		lexScore := overlapScore(queryTokens, tokenSet(stored.text))
		score := 0.6*vectorScore + 0.4*lexScore
		if score <= 0 {
			continue
		}
		hits = append(hits, Hit{
			ConceptID: id, Score: score, Snippet: shortSnippet(stored.text), Source: "memory",
			Citation: "memory:/" + id,
		})
	}
	sortHits(hits)
	return truncateHits(hits, q.TopK), nil
}

func (a *MemoryAdapter) Delete(ctx context.Context, conceptID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.docs, conceptID)
	return ctx.Err()
}

func docText(d *okf.Document) string {
	if d == nil {
		return ""
	}
	return strings.TrimSpace(d.Title + " " + d.Description + " " + d.Body)
}

func shortSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(t) > 1 {
			out[t] = true
		}
	}
	return out
}

func overlapScore(q, d map[string]bool) float64 {
	if len(q) == 0 || len(d) == 0 {
		return 0
	}
	common := 0
	for t := range q {
		if d[t] {
			common++
		}
	}
	return float64(common) / float64(len(q))
}

func cosineVectors(a, b meta.Vector) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrtFloat(na) * sqrtFloat(nb))
}

func sqrtFloat(x float64) float64 {
	z := x
	for i := 0; i < 32; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ConceptID < hits[j].ConceptID
		}
		return hits[i].Score > hits[j].Score
	})
}
