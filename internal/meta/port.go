// Package meta stores agentmem's own server-side structured data.
//
// Knowledge bodies live in OKF+Git; PostgreSQL (through GORM) only holds
// metadata and state, linked by content_hash + commit_sha. The port has no
// *gorm.DB in its signatures so the in-memory fake stays dependency-free.
package meta

import "context"

// Query/result value objects shared by the port and its implementations.
type ProjectionQuery struct {
	ConceptID string
	Adapter   string
	Status    string
	Limit     int
}

type DecisionQuery struct {
	ConceptID string
	Limit     int
}

// MetaStore is the server-side structured data port.
type MetaStore interface {
	UpsertConcept(ctx context.Context, m ConceptMeta) error
	GetConcept(ctx context.Context, conceptID string) (ConceptMeta, error)
	ListConcepts(ctx context.Context, limit int) ([]ConceptMeta, error)

	ListProjections(ctx context.Context, q ProjectionQuery) ([]Projection, error)
	SetProjectionState(ctx context.Context, p Projection) error

	AppendDecision(ctx context.Context, d DecisionRecord) error
	ListDecisions(ctx context.Context, q DecisionQuery) ([]DecisionRecord, error)

	MigrationCursor(ctx context.Context, job string) (Cursor, error)
	SaveMigrationCursor(ctx context.Context, job string, c Cursor) error

	EngineFingerprint(ctx context.Context, adapter string) (string, error)
	SetEngineFingerprint(ctx context.Context, adapter, fingerprint string) error

	PutACL(ctx context.Context, e ACLEntry) error
	CheckACL(ctx context.Context, subject, conceptID string) (bool, error)

	Close() error
}
