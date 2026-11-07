package meta

import (
	"context"
	"sort"
)

// VectorHit is one similarity result.
type VectorHit struct {
	ConceptID string  `json:"concept_id"`
	Score     float64 `json:"score"`
}

// VectorStore is the narrow port consumed by the pgvector adapter. The real
// implementation uses parameterised native SQL because GORM has no vector type.
type VectorStore interface {
	PutVector(ctx context.Context, conceptID string, v Vector) error
	QueryVector(ctx context.Context, v Vector, topK int) ([]VectorHit, error)
	DeleteVector(ctx context.Context, conceptID string) error
}

func (s *PostgresStore) PutVector(ctx context.Context, conceptID string, v Vector) error {
	literal, err := v.Value()
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Exec(
		`INSERT INTO concept_vectors (concept_id, embedding, updated_at)
		 VALUES (?, ?::vector, now())
		 ON CONFLICT (concept_id) DO UPDATE
		 SET embedding = EXCLUDED.embedding, updated_at = now()`,
		conceptID, literal,
	).Error
}

func (s *PostgresStore) QueryVector(ctx context.Context, v Vector, topK int) ([]VectorHit, error) {
	literal, err := v.Value()
	if err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 5
	}
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT concept_id, 1 - (embedding <=> ?::vector) AS score
		 FROM concept_vectors
		 ORDER BY embedding <=> ?::vector
		 LIMIT ?`, literal, literal, topK,
	).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VectorHit
	for rows.Next() {
		var h VectorHit
		if err := rows.Scan(&h.ConceptID, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func cosine(a, b Vector) float64 {
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
	return dot / (sqrt(na) * sqrt(nb))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 32; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func (s *PostgresStore) DeleteVector(ctx context.Context, conceptID string) error {
	return s.db.WithContext(ctx).Exec(
		`DELETE FROM concept_vectors WHERE concept_id = ?`, conceptID,
	).Error
}

func rankHits(hits []VectorHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ConceptID < hits[j].ConceptID
		}
		return hits[i].Score > hits[j].Score
	})
}
