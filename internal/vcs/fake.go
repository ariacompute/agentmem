package vcs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// FakeRepository is an in-memory, deterministic repository for offline tests.
type FakeRepository struct {
	mu      sync.Mutex
	commits []fakeCommit
}

type fakeCommit struct {
	hash    string
	message string
	when    time.Time
	changed []string
	files   map[string][]byte
}

// NewFake returns an empty in-memory repository.
func NewFake() *FakeRepository { return &FakeRepository{} }

func (f *FakeRepository) Commit(ctx context.Context, message string, files map[string][]byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", len(f.commits), message)))
	h := hex.EncodeToString(sum[:])
	snapshot := map[string][]byte{}
	if len(f.commits) > 0 {
		for k, v := range f.commits[len(f.commits)-1].files {
			snapshot[k] = append([]byte(nil), v...)
		}
	}
	changed := make([]string, 0, len(files))
	for k, v := range files {
		snapshot[k] = append([]byte(nil), v...)
		changed = append(changed, k)
	}
	sort.Strings(changed)
	f.commits = append(f.commits, fakeCommit{
		hash: h, message: message, when: time.Now().UTC(), changed: changed, files: snapshot,
	})
	return h, nil
}

func (f *FakeRepository) Head(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commits) == 0 {
		return "", nil
	}
	return f.commits[len(f.commits)-1].hash, nil
}

func (f *FakeRepository) Log(ctx context.Context, limit int) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		limit = 10
	}
	out := make([]Entry, 0, limit)
	for i := len(f.commits) - 1; i >= 0 && len(out) < limit; i-- {
		c := f.commits[i]
		out = append(out, Entry{Hash: c.hash, Message: c.message, When: c.when})
	}
	return out, nil
}

func (f *FakeRepository) Show(ctx context.Context, ref, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.find(ref)
	if err != nil {
		return nil, err
	}
	data, ok := c.files[path]
	if !ok {
		return nil, fmt.Errorf("vcs: show %s: not found", path)
	}
	return append([]byte(nil), data...), nil
}

func (f *FakeRepository) Diff(ctx context.Context, from, to string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.find(to)
	if err != nil {
		return "", err
	}
	var sb stringBuilder
	for _, name := range c.changed {
		sb.WriteString("--- " + name + "\n+++ " + name + "\n")
	}
	return sb.String(), nil
}

func (f *FakeRepository) find(ref string) (fakeCommit, error) {
	if len(f.commits) == 0 {
		return fakeCommit{}, fmt.Errorf("vcs: empty repository")
	}
	if ref == "" {
		return f.commits[len(f.commits)-1], nil
	}
	for _, c := range f.commits {
		if c.hash == ref {
			return c, nil
		}
	}
	return fakeCommit{}, fmt.Errorf("vcs: unknown ref %q", ref)
}

type stringBuilder struct{ s string }

func (b *stringBuilder) WriteString(s string) { b.s += s }
func (b *stringBuilder) String() string       { return b.s }
