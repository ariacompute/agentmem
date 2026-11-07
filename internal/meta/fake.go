package meta

import (
	"context"
	"sort"
	"sync"
	"time"
)

// FakeStore is an in-memory MetaStore + VectorStore for offline tests.
// It mirrors the PostgreSQL implementation's contract exactly.
type FakeStore struct {
	mu           sync.Mutex
	concepts     map[string]ConceptMeta
	projections  map[string]Projection // conceptID + "\x00" + adapter
	decisions    []DecisionRecord
	computations []ComputationRecord
	runKeys      map[string]bool
	nextID       uint
	cursors      map[string]Cursor
	fingerprint  map[string]string
	acl          map[string]ACLEntry
	vectors      map[string]Vector
	onClose      func() error
}

// NewFakeStore returns an empty in-memory store.
func NewFakeStore() *FakeStore {
	return &FakeStore{
		concepts:     map[string]ConceptMeta{},
		projections:  map[string]Projection{},
		cursors:      map[string]Cursor{},
		fingerprint:  map[string]string{},
		acl:          map[string]ACLEntry{},
		vectors:      map[string]Vector{},
		computations: []ComputationRecord{},
		runKeys:      map[string]bool{},
	}
}

func projectionKey(conceptID, adapter string) string { return conceptID + "\x00" + adapter }
func aclKey(subject, conceptID string) string        { return subject + "\x00" + conceptID }

func (f *FakeStore) UpsertConcept(ctx context.Context, m ConceptMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	if old, ok := f.concepts[m.ConceptID]; ok {
		if m.CreatedAt.IsZero() {
			m.CreatedAt = old.CreatedAt
		}
	} else if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	f.concepts[m.ConceptID] = m
	return ctx.Err()
}

func (f *FakeStore) GetConcept(ctx context.Context, id string) (ConceptMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.concepts[id]
	if !ok {
		return ConceptMeta{}, errNotFound
	}
	return m, nil
}

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (e *notFoundError) Error() string { return "meta: not found" }

func (f *FakeStore) ListConcepts(ctx context.Context, limit int) ([]ConceptMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ConceptMeta, 0, len(f.concepts))
	for _, m := range f.concepts {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConceptID < out[j].ConceptID })
	return truncateConcepts(out, limit), nil
}

func truncateConcepts(in []ConceptMeta, limit int) []ConceptMeta {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}

func (f *FakeStore) ListProjections(ctx context.Context, q ProjectionQuery) ([]Projection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Projection
	for _, p := range f.projections {
		if q.ConceptID != "" && p.ConceptID != q.ConceptID {
			continue
		}
		if q.Adapter != "" && p.Adapter != q.Adapter {
			continue
		}
		if q.Status != "" && p.Status != q.Status {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ConceptID == out[j].ConceptID {
			return out[i].Adapter < out[j].Adapter
		}
		return out[i].ConceptID < out[j].ConceptID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (f *FakeStore) SetProjectionState(ctx context.Context, p Projection) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p.UpdatedAt = time.Now().UTC()
	if old, ok := f.projections[projectionKey(p.ConceptID, p.Adapter)]; ok {
		p.ID = old.ID
	} else {
		f.nextID++
		p.ID = f.nextID
	}
	f.projections[projectionKey(p.ConceptID, p.Adapter)] = p
	return ctx.Err()
}

func (f *FakeStore) AppendDecision(ctx context.Context, d DecisionRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	d.ID = f.nextID
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.Targets == nil {
		d.Targets = StringList{}
	}
	f.decisions = append(f.decisions, d)
	return ctx.Err()
}

func (f *FakeStore) ListDecisions(ctx context.Context, q DecisionQuery) ([]DecisionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []DecisionRecord
	for i := len(f.decisions) - 1; i >= 0; i-- {
		d := f.decisions[i]
		if q.ConceptID != "" && d.ConceptID != q.ConceptID {
			continue
		}
		out = append(out, d)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (f *FakeStore) AppendComputation(ctx context.Context, r ComputationRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runKeys == nil {
		f.runKeys = map[string]bool{}
	}
	key := r.JobID + "\x00" + r.Verdict
	if f.runKeys[key] {
		return ctx.Err()
	}
	f.runKeys[key] = true
	f.nextID++
	r.ID = f.nextID
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.Reasons == nil {
		r.Reasons = StringList{}
	}
	f.computations = append(f.computations, r)
	return ctx.Err()
}

func (f *FakeStore) ListComputations(ctx context.Context, q ComputationQuery) ([]ComputationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ComputationRecord, 0, len(f.computations))
	for i := len(f.computations) - 1; i >= 0; i-- {
		r := f.computations[i]
		if q.ConceptID != "" && r.ConceptID != q.ConceptID {
			continue
		}
		out = append(out, r)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

// Query is the offline SQLExecutor used by the postgres computation runtime
// when no DSN is configured. It never executes SQL: the result is derived
// deterministically from the statement and bound arguments.
func (f *FakeStore) Query(ctx context.Context, statement string, args []any, maxRows int) (SQLRows, error) {
	if err := ctx.Err(); err != nil {
		return SQLRows{}, err
	}
	return DeterministicSQLRows(statement, args, maxRows), nil
}

func (f *FakeStore) MigrationCursor(ctx context.Context, job string) (Cursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.cursors[job]; ok {
		return c, nil
	}
	return Cursor{Job: job}, nil
}

func (f *FakeStore) SaveMigrationCursor(ctx context.Context, job string, c Cursor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.Job = job
	c.UpdatedAt = time.Now().UTC()
	f.cursors[job] = c
	return ctx.Err()
}

func (f *FakeStore) EngineFingerprint(ctx context.Context, adapter string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fingerprint[adapter], nil
}

func (f *FakeStore) SetEngineFingerprint(ctx context.Context, adapter, fingerprint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fingerprint[adapter] = fingerprint
	return ctx.Err()
}

func (f *FakeStore) PutACL(ctx context.Context, e ACLEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acl[aclKey(e.Subject, e.ConceptID)] = e
	return ctx.Err()
}

func (f *FakeStore) CheckACL(ctx context.Context, subject, conceptID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.acl[aclKey(subject, conceptID)]; ok {
		return e.Allow, nil
	}
	m, ok := f.concepts[conceptID]
	if !ok {
		return false, nil
	}
	return aclFromVisibility(m, subject), nil
}

func (f *FakeStore) PutVector(ctx context.Context, conceptID string, v Vector) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := append(Vector(nil), v...)
	f.vectors[conceptID] = cp
	return ctx.Err()
}

func (f *FakeStore) QueryVector(ctx context.Context, v Vector, topK int) ([]VectorHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if topK <= 0 {
		topK = 5
	}
	hits := make([]VectorHit, 0, len(f.vectors))
	for id, stored := range f.vectors {
		hits = append(hits, VectorHit{ConceptID: id, Score: cosine(v, stored)})
	}
	rankHits(hits)
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, ctx.Err()
}

func (f *FakeStore) DeleteVector(ctx context.Context, conceptID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.vectors, conceptID)
	return ctx.Err()
}

func (f *FakeStore) Close() error {
	if f.onClose != nil {
		return f.onClose()
	}
	return nil
}

// Snapshot is the serialisable state of the in-memory store.
type Snapshot struct {
	Concepts     map[string]ConceptMeta `json:"concepts"`
	Projections  map[string]Projection  `json:"projections"`
	Decisions    []DecisionRecord       `json:"decisions"`
	Computations []ComputationRecord    `json:"computations"`
	NextID       uint                   `json:"next_id"`
	Cursors      map[string]Cursor      `json:"cursors"`
	Fingerprints map[string]string      `json:"fingerprints"`
	ACL          map[string]ACLEntry    `json:"acl"`
	Vectors      map[string]Vector      `json:"vectors"`
}

// Export returns a deep-enough copy of the current state.
func (f *FakeStore) Export() Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := Snapshot{
		Concepts:     make(map[string]ConceptMeta, len(f.concepts)),
		Projections:  make(map[string]Projection, len(f.projections)),
		Decisions:    append([]DecisionRecord(nil), f.decisions...),
		Computations: append([]ComputationRecord(nil), f.computations...),
		NextID:       f.nextID,
		Cursors:      make(map[string]Cursor, len(f.cursors)),
		Fingerprints: make(map[string]string, len(f.fingerprint)),
		ACL:          make(map[string]ACLEntry, len(f.acl)),
		Vectors:      make(map[string]Vector, len(f.vectors)),
	}
	for k, v := range f.concepts {
		s.Concepts[k] = v
	}
	for k, v := range f.projections {
		s.Projections[k] = v
	}
	for k, v := range f.cursors {
		s.Cursors[k] = v
	}
	for k, v := range f.fingerprint {
		s.Fingerprints[k] = v
	}
	for k, v := range f.acl {
		s.ACL[k] = v
	}
	for k, v := range f.vectors {
		s.Vectors[k] = append(Vector(nil), v...)
	}
	return s
}

// Import replaces the current state with a snapshot.
func (f *FakeStore) Import(s Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.concepts = s.Concepts
	if f.concepts == nil {
		f.concepts = map[string]ConceptMeta{}
	}
	f.projections = s.Projections
	if f.projections == nil {
		f.projections = map[string]Projection{}
	}
	f.decisions = s.Decisions
	if f.decisions == nil {
		f.decisions = []DecisionRecord{}
	}
	f.computations = s.Computations
	if f.computations == nil {
		f.computations = []ComputationRecord{}
	}
	f.runKeys = make(map[string]bool, len(f.computations))
	for _, r := range f.computations {
		f.runKeys[r.JobID+"\x00"+r.Verdict] = true
	}
	f.nextID = s.NextID
	f.cursors = s.Cursors
	if f.cursors == nil {
		f.cursors = map[string]Cursor{}
	}
	f.fingerprint = s.Fingerprints
	if f.fingerprint == nil {
		f.fingerprint = map[string]string{}
	}
	f.acl = s.ACL
	if f.acl == nil {
		f.acl = map[string]ACLEntry{}
	}
	f.vectors = s.Vectors
	if f.vectors == nil {
		f.vectors = map[string]Vector{}
	}
}
