package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"strings"

	"agentmem/internal/meta"
)

// ErrUnsupported is returned for a capability a channel cannot perform.
var ErrUnsupported = &unsupportedError{}

type unsupportedError struct{}

func (e *unsupportedError) Error() string { return "adapter: unsupported capability" }
func (e *unsupportedError) Is(target error) bool {
	_, ok := target.(*unsupportedError)
	return ok
}

// Embedder is the embedding port. Identity participates in projection
// fingerprints so a model change forces a rebuild.
type Embedder interface {
	Embed(ctx context.Context, text string) (meta.Vector, error)
	Dimension() int
	Identity() string
}

// HashingEmbedder is a deterministic, offline embedding used for tests and as
// the dev default. It is lexical, not semantic: production must inject a real
// embedder. TODO(real): replace with a model-backed embedder.
type HashingEmbedder struct {
	Dim int
}

func NewHashingEmbedder(dim int) *HashingEmbedder {
	if dim <= 0 {
		dim = 64
	}
	return &HashingEmbedder{Dim: dim}
}

func (h *HashingEmbedder) Dimension() int { return h.Dim }
func (h *HashingEmbedder) Identity() string {
	return "hashing-embedder/d" + itoa(h.Dim)
}

func (h *HashingEmbedder) Embed(ctx context.Context, text string) (meta.Vector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v := make(meta.Vector, h.Dim)
	for _, tok := range strings.Fields(strings.ToLower(text)) {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(tok))
		idx := int(hash.Sum32()) % h.Dim
		v[idx]++
	}
	norm := float32(0)
	for _, x := range v {
		norm += x * x
	}
	if norm > 0 {
		for i := range v {
			v[i] /= sqrt32(norm)
		}
	}
	return v, nil
}

func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 24; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// FingerprintFor identifies an adapter's indexing configuration.
func FingerprintFor(name string, e Embedder) string {
	sum := sha256.Sum256([]byte(name + "|" + e.Identity()))
	return hex.EncodeToString(sum[:])[:32]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
