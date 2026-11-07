// Package vcs provides the Git-backed version history of the OKF bundle.
package vcs

import (
	"context"
	"time"
)

// Entry is one commit in the log.
type Entry struct {
	Hash    string
	Message string
	When    time.Time
}

// Repository is the version-control port. Implementations must write the given
// files before committing so the fake and go-git backends share one contract.
type Repository interface {
	Commit(ctx context.Context, message string, files map[string][]byte) (string, error)
	Log(ctx context.Context, limit int) ([]Entry, error)
	Show(ctx context.Context, ref, path string) ([]byte, error)
	Diff(ctx context.Context, from, to string) (string, error)
	Head(ctx context.Context) (string, error)
}
