package adapter

import (
	"context"
	"errors"
	"sort"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// PGVectorAdapter projects concepts into a pgvector-backed store. The actual
// SQL lives in internal/meta (gorm stays behind the VectorStore port).
type PGVectorAdapter struct {
	vectors  meta.VectorStore
	embedder Embedder
}

func NewPGVectorAdapter(v meta.VectorStore, e Embedder) (*PGVectorAdapter, error) {
	if v == nil {
		return nil, errors.New("adapter: pgvector requires a VectorStore")
	}
	if e == nil {
		e = NewHashingEmbedder(64)
	}
	return &PGVectorAdapter{vectors: v, embedder: e}, nil
}

func (a *PGVectorAdapter) Name() string { return "pgvector" }
func (a *PGVectorAdapter) Capabilities() Capabilities {
	return Capabilities{Put: true, Query: true, Delete: true, Vector: true, Semantic: true}
}
func (a *PGVectorAdapter) Fingerprint() string { return FingerprintFor("pgvector", a.embedder) }
func (a *PGVectorAdapter) Health(ctx context.Context) error {
	return ctx.Err()
}

func (a *PGVectorAdapter) Put(ctx context.Context, conceptID string, d *okf.Document) error {
	v, err := a.embedder.Embed(ctx, docText(d))
	if err != nil {
		return err
	}
	return a.vectors.PutVector(ctx, conceptID, v)
}

func (a *PGVectorAdapter) Query(ctx context.Context, q Query) ([]Hit, error) {
	v := q.Vector
	if len(v) == 0 {
		var err error
		v, err = a.embedder.Embed(ctx, q.Text)
		if err != nil {
			return nil, err
		}
	}
	found, err := a.vectors.QueryVector(ctx, v, q.TopK)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(found))
	for _, h := range found {
		if h.Score <= 0 {
			continue
		}
		hits = append(hits, Hit{
			ConceptID: h.ConceptID, Score: h.Score, Source: "pgvector",
			Citation: "pgvector:/" + h.ConceptID,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits, nil
}

func (a *PGVectorAdapter) Delete(ctx context.Context, conceptID string) error {
	return a.vectors.DeleteVector(ctx, conceptID)
}
