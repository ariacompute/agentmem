package adapter

import (
	"context"
	"sync"

	"agentmem/internal/meta"
)

// Call records one VectorStore invocation for deterministic contract tests.
type Call struct {
	Method     string
	ConceptID  string
	Dimensions int
	TopK       int
}

// RecordingVectorDB is an offline fake that also captures call order, so tests
// can assert SQL-boundary behaviour without a database.
type RecordingVectorDB struct {
	inner meta.VectorStore
	mu    sync.Mutex
	calls []Call
}

func NewRecordingVectorDB(inner meta.VectorStore) *RecordingVectorDB {
	if inner == nil {
		inner = meta.NewFakeStore()
	}
	return &RecordingVectorDB{inner: inner}
}

func (r *RecordingVectorDB) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

func (r *RecordingVectorDB) record(c Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

func (r *RecordingVectorDB) PutVector(ctx context.Context, conceptID string, v meta.Vector) error {
	r.record(Call{Method: "PutVector", ConceptID: conceptID, Dimensions: len(v)})
	return r.inner.PutVector(ctx, conceptID, v)
}

func (r *RecordingVectorDB) QueryVector(ctx context.Context, v meta.Vector, topK int) ([]meta.VectorHit, error) {
	r.record(Call{Method: "QueryVector", Dimensions: len(v), TopK: topK})
	return r.inner.QueryVector(ctx, v, topK)
}

func (r *RecordingVectorDB) DeleteVector(ctx context.Context, conceptID string) error {
	r.record(Call{Method: "DeleteVector", ConceptID: conceptID})
	return r.inner.DeleteVector(ctx, conceptID)
}
