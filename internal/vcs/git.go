package vcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// GitRepository is a pure-Go Git backend (no external git process).
type GitRepository struct {
	root   string
	repo   *git.Repository
	author object.Signature
}

// NewGit opens or initializes a repository rooted at the OKF bundle.
func NewGit(root, authorName, authorEmail string) (*GitRepository, error) {
	if root == "" {
		return nil, errors.New("vcs: empty root")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	repo, err := git.PlainOpen(root)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		repo, err = git.PlainInit(root, false)
	}
	if err != nil {
		return nil, fmt.Errorf("vcs: open repository: %w", err)
	}
	if authorName == "" {
		authorName = "agentmem"
	}
	if authorEmail == "" {
		authorEmail = "agentmem@localhost"
	}
	return &GitRepository{root: root, repo: repo, author: object.Signature{
		Name: authorName, Email: authorEmail, When: time.Now().UTC(),
	}}, nil
}

func (g *GitRepository) worktree() (*git.Worktree, error) {
	return g.repo.Worktree()
}

// Commit writes files, stages them and creates one commit atomically.
func (g *GitRepository) Commit(ctx context.Context, message string, files map[string][]byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	w, err := g.worktree()
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		rel := filepath.FromSlash(name)
		if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return "", fmt.Errorf("vcs: path escapes repository: %q", name)
		}
		p := filepath.Join(g.root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, files[name], 0o644); err != nil {
			return "", err
		}
		if _, err := w.Add(name); err != nil {
			return "", fmt.Errorf("vcs: stage %s: %w", name, err)
		}
	}
	sig := g.author
	sig.When = time.Now().UTC()
	hash, err := w.Commit(message, &git.CommitOptions{Author: &sig})
	if err != nil {
		return "", fmt.Errorf("vcs: commit: %w", err)
	}
	return hash.String(), nil
}

// Head returns the current HEAD commit hash (empty when unborn).
func (g *GitRepository) Head(ctx context.Context) (string, error) {
	ref, err := g.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ref.Hash().String(), nil
}

// Log returns newest-first commits, bounded by limit (<=0 means 10).
func (g *GitRepository) Log(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 10
	}
	ref, err := g.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	iter, err := g.repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []Entry
	err = iter.ForEach(func(c *object.Commit) error {
		if len(out) >= limit {
			return storerStop
		}
		out = append(out, Entry{Hash: c.Hash.String(), Message: c.Message, When: c.Author.When})
		return nil
	})
	if err != nil && !errors.Is(err, storerStop) {
		return nil, err
	}
	return out, nil
}

var storerStop = errors.New("vcs: stop iteration")

// Show reads a file at a commit (empty ref means HEAD).
func (g *GitRepository) Show(ctx context.Context, ref, path string) ([]byte, error) {
	h, err := g.resolve(ref)
	if err != nil {
		return nil, err
	}
	c, err := g.repo.CommitObject(h)
	if err != nil {
		return nil, err
	}
	f, err := c.File(path)
	if err != nil {
		return nil, fmt.Errorf("vcs: show %s@%s: %w", path, ref, err)
	}
	s, err := f.Contents()
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// Diff produces a textual patch between two commits. Empty refs mean HEAD and
// its parent; an unborn or root commit yields an empty patch.
func (g *GitRepository) Diff(ctx context.Context, from, to string) (string, error) {
	toH, err := g.resolve(to)
	if err != nil {
		return "", err
	}
	if from == "" {
		c, err := g.repo.CommitObject(toH)
		if err != nil {
			return "", err
		}
		if c.NumParents() == 0 {
			return "", nil
		}
		from = c.ParentHashes[0].String()
	}
	fromH, err := g.resolve(from)
	if err != nil {
		return "", err
	}
	cFrom, err := g.repo.CommitObject(fromH)
	if err != nil {
		return "", err
	}
	cTo, err := g.repo.CommitObject(toH)
	if err != nil {
		return "", err
	}
	patch, err := cFrom.Patch(cTo)
	if err != nil {
		return "", err
	}
	return patch.String(), nil
}

func (g *GitRepository) resolve(ref string) (plumbing.Hash, error) {
	if ref == "" {
		head, err := g.repo.Head()
		if err != nil {
			return plumbing.ZeroHash, err
		}
		return head.Hash(), nil
	}
	return plumbing.NewHash(ref), nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
