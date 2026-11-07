package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"agentmem/internal/meta"
)

// Projection lifecycle states.
const (
	ProjectionPending = "pending"
	ProjectionSynced  = "synced"
	ProjectionStale   = "stale"
	ProjectionFailed  = "failed"
)

// Fingerprint identifies the embedding/index configuration behind a projection.
// A change means the projection must be rebuilt (DeepTutor's embedding identity).
func Fingerprint(adapter, model string, dimension int, endpoint string) string {
	parts := []string{adapter, model, itoa(dimension), endpoint}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])[:32]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// RecordProjection persists the state of one canonical→adapter projection.
func RecordProjection(ctx context.Context, m meta.MetaStore, conceptID, adapter, contentHash, fingerprint, status string) error {
	return m.SetProjectionState(ctx, meta.Projection{
		ConceptID:   conceptID,
		Adapter:     adapter,
		ContentHash: contentHash,
		Fingerprint: fingerprint,
		Status:      status,
	})
}
