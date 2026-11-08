// Package store owns the canonical knowledge body (OKF+Git) and its metadata.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/vcs"
)

// OKFStore is the canonical store. Git holds bodies; MetaStore holds structured
// state. The two are linked by content_hash + commit_sha, never by duplicated
// bodies.
type OKFStore struct {
	Bundle *okf.Bundle
	Repo   vcs.Repository
	Meta   meta.MetaStore
	ACL    ACLDefaults
}

func NewOKFStore(b *okf.Bundle, r vcs.Repository, m meta.MetaStore, acl ACLDefaults) *OKFStore {
	return &OKFStore{Bundle: b, Repo: r, Meta: m, ACL: acl}
}

// PutResult is the outcome of a canonical write.
type PutResult struct {
	ConceptID   string `json:"concept_id"`
	ContentHash string `json:"content_hash"`
	CommitSHA   string `json:"commit_sha"`
	Unchanged   bool   `json:"unchanged"`
}

// PutAs is the authenticated write path used by the HTTP handler. It binds the
// ACL owner to the trusted subject and refuses to overwrite a body owned by a
// different subject. Put remains available for local CLI operations, which are
// trusted by virtue of running on the host.
func (s *OKFStore) PutAs(ctx context.Context, subject, id string, d *okf.Document) (PutResult, error) {
	if err := TrustedACL(d, s.ACL, subject); err != nil {
		return PutResult{}, err
	}
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return PutResult{}, err
	}
	if existing, err := s.Bundle.Get(clean); err == nil {
		if err := AuthorizeUpdate(existing, subject); err != nil {
			return PutResult{}, err
		}
	}
	return s.Put(ctx, clean, d)
}

// DeleteAs applies the same ownership rule to canonical deletions.
func (s *OKFStore) DeleteAs(ctx context.Context, subject, id string) error {
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("store: anonymous delete rejected")
	}
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return err
	}
	existing, err := s.Bundle.Get(clean)
	if err != nil {
		return err
	}
	if err := AuthorizeUpdate(existing, subject); err != nil {
		return err
	}
	return s.Delete(ctx, clean)
}

// Put writes the body first, commits it to Git, then records metadata. A failed
// metadata write never rolls back the body; migration reconciles the gap.
func (s *OKFStore) Put(ctx context.Context, id string, d *okf.Document) (PutResult, error) {
	if err := ctx.Err(); err != nil {
		return PutResult{}, err
	}
	if err := ApplyACLDefaults(d, s.ACL); err != nil {
		return PutResult{}, err
	}
	data, err := d.Marshal()
	if err != nil {
		return PutResult{}, err
	}
	hash := okf.Hash(data)
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return PutResult{}, err
	}
	if existing, err := s.Meta.GetConcept(ctx, clean); err == nil && existing.ContentHash == hash && existing.Status != "deprecated" {
		return PutResult{ConceptID: clean, ContentHash: hash, CommitSHA: existing.CommitSHA, Unchanged: true}, nil
	}
	if _, err := s.Bundle.Put(clean, d); err != nil {
		return PutResult{}, err
	}
	files, err := s.commitFiles(clean)
	if err != nil {
		return PutResult{}, err
	}
	commitSHA, err := s.Repo.Commit(ctx, "put: "+clean, files)
	if err != nil {
		return PutResult{}, fmt.Errorf("store: commit body: %w", err)
	}
	if err := s.Meta.UpsertConcept(ctx, meta.ConceptMeta{
		ConceptID:   clean,
		ContentHash: hash,
		CommitSHA:   commitSHA,
		Visibility:  d.Visibility(),
		Owner:       d.Owner(),
		Status:      d.EffectiveStatus(),
	}); err != nil {
		return PutResult{}, fmt.Errorf("store: record metadata: %w", err)
	}
	return PutResult{ConceptID: clean, ContentHash: hash, CommitSHA: commitSHA}, nil
}

// commitFiles collects the concept plus the incrementally rebuilt index/log.
func (s *OKFStore) commitFiles(clean string) (map[string][]byte, error) {
	p, err := s.Bundle.Path(clean)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(p)
	candidates := []string{p, filepath.Join(dir, "index.md"), filepath.Join(dir, "log.md")}
	out := map[string][]byte{}
	for _, c := range candidates {
		data, err := os.ReadFile(c)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		rel, err := filepath.Rel(s.Bundle.Root, c)
		if err != nil {
			return nil, err
		}
		out[filepath.ToSlash(rel)] = data
	}
	return out, nil
}

// Get enforces ACL before returning the canonical body.
func (s *OKFStore) Get(ctx context.Context, subject, id string) (*okf.Document, error) {
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return nil, err
	}
	ok, err := s.Meta.CheckACL(ctx, subject, clean)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("store: access denied for %q", clean)
	}
	return s.Bundle.Get(clean)
}

// Delete removes the body and marks metadata deprecated (keeping the audit row).
func (s *OKFStore) Delete(ctx context.Context, id string) error {
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return err
	}
	if err := s.Bundle.Delete(clean); err != nil {
		return err
	}
	files, err := s.commitFiles(clean)
	if err != nil {
		return err
	}
	if _, err := s.Repo.Commit(ctx, "delete: "+clean, files); err != nil {
		return err
	}
	m, err := s.Meta.GetConcept(ctx, clean)
	if err == nil {
		m.Status = "deprecated"
		return s.Meta.UpsertConcept(ctx, m)
	}
	return nil
}

// List returns canonical concept ids.
func (s *OKFStore) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Bundle.List()
}

// History exposes the Git log of the canonical body.
func (s *OKFStore) History(ctx context.Context, limit int) ([]vcs.Entry, error) {
	return s.Repo.Log(ctx, limit)
}

// Diff exposes a textual patch between two commits.
func (s *OKFStore) Diff(ctx context.Context, from, to string) (string, error) {
	return s.Repo.Diff(ctx, from, to)
}

// Show reads a historical body from Git.
func (s *OKFStore) Show(ctx context.Context, ref, id string) ([]byte, error) {
	clean, err := okf.ValidateConceptID(id)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(ref, "/") {
		ref = strings.TrimSuffix(ref, "/")
	}
	return s.Repo.Show(ctx, ref, clean+".md")
}
