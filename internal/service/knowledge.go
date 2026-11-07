// Package service assembles the application from ports and exposes the three
// use cases: Put, Query and Migrate.
package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/bridges"
	"agentmem/internal/compute"
	"agentmem/internal/config"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/plugins"
	"agentmem/internal/router"
	"agentmem/internal/store"
	"agentmem/internal/vcs"
)

// KnowledgeService is the single orchestration entry point.
type KnowledgeService struct {
	Config   *config.Config
	Store    *store.OKFStore
	Registry *adapter.Registry
	Write    *router.WriteRouter
	Read     *router.ReadRouter
	Migrate  *router.Migrator
	Decision router.Decisioner
	Evidence *router.Evidence
	Compute  *compute.Service
	closer   func() error
}

// Assemble wires configuration to implementations. With no DSN it uses the
// in-memory PostgreSQL fake, so the whole service runs offline.
func Assemble(ctx context.Context, cfg *config.Config) (*KnowledgeService, error) {
	if cfg == nil {
		return nil, fmt.Errorf("service: nil config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	bundle, err := okf.Open(cfg.Git.Root)
	if err != nil {
		return nil, err
	}
	repo, err := vcs.NewGit(cfg.Git.Root, cfg.Git.AuthorName, cfg.Git.AuthorEmail)
	if err != nil {
		return nil, err
	}
	var m meta.MetaStore
	var vectors meta.VectorStore
	var closer func() error
	if cfg.Postgres.DSN != "" {
		lifetime, _ := time.ParseDuration(cfg.Postgres.ConnMaxLifetime)
		pg, err := meta.OpenPostgres(ctx, meta.Options{
			DSN: cfg.Postgres.DSN, MaxOpenConns: cfg.Postgres.MaxOpenConns,
			MaxIdleConns: cfg.Postgres.MaxIdleConns, ConnMaxLifetime: lifetime,
			LogLevel: cfg.Postgres.LogLevel,
		})
		if err != nil {
			return nil, err
		}
		m, vectors, closer = pg, pg, pg.Close
	} else {
		// Offline fallback: persist the fake's structured state so separate CLI
		// processes share ACL metadata, projections and the decision audit.
		snapshot := filepath.Join(filepath.Dir(cfg.Git.Root), "meta.fake.json")
		fake, err := meta.NewFileFakeStore(snapshot)
		if err != nil {
			return nil, err
		}
		m, vectors, closer = fake, fake, fake.Close
	}
	registry := adapter.NewRegistry()
	if err := bridges.RegisterKinds(registry); err != nil {
		_ = closer()
		return nil, err
	}
	if err := plugins.RegisterAll(registry); err != nil {
		_ = closer()
		return nil, err
	}
	if err := registry.Build(cfg, adapter.Deps{
		Bundle: bundle, Vector: vectors, Meta: m, Embedder: adapter.NewHashingEmbedder(64),
	}); err != nil {
		_ = closer()
		return nil, err
	}
	if err := registry.StartAll(ctx); err != nil {
		_ = registry.Close(context.Background())
		_ = closer()
		return nil, err
	}
	s := store.NewOKFStore(bundle, repo, m, store.ACLDefaults{
		Visibility: cfg.ACL.DefaultVisibility, Owner: cfg.ACL.DefaultOwner,
	})
	so := router.DefaultSystemOneConfig()
	so.Endpoint = cfg.Router.SystemOne.Endpoint
	so.TimeoutMS = cfg.Router.SystemOne.TimeoutMS
	so.CacheCapacity = cfg.Router.SystemOne.CacheCapacity
	so.MaxChoices = cfg.Router.SystemOne.MaxChoices
	so.MinConfidence = cfg.Router.SystemOne.MinConfidence
	so.Fallback = cfg.Router.SystemOne.Fallback
	so.FailClosed = cfg.Router.SystemOne.FailClosed
	decisioner := router.NewComposite(router.NewRuleEngine(), router.NewSystemOneDecisioner(so))
	sqlExec, ok := m.(meta.SQLExecutor)
	if !ok {
		_ = registry.Close(context.Background())
		_ = closer()
		return nil, fmt.Errorf("service: metadata store does not implement SQLExecutor")
	}
	computeCfg := cfg.Compute
	if computeCfg.TimeoutMS <= 0 || computeCfg.MaxRows <= 0 {
		def := config.DefaultCompute()
		if computeCfg.TimeoutMS <= 0 {
			computeCfg.TimeoutMS = def.TimeoutMS
		}
		if computeCfg.MaxRows <= 0 {
			computeCfg.MaxRows = def.MaxRows
		}
	}
	computeSvc := compute.NewService(bundle,
		compute.NewRuntimeRegistry(sqlExec, computeCfg.MaxRows), m,
		time.Duration(computeCfg.TimeoutMS)*time.Millisecond, computeCfg.MaxRows)
	return &KnowledgeService{
		Config: cfg, Store: s, Registry: registry,
		Write:    router.NewWriteRouter(s, registry, decisioner),
		Read:     router.NewReadRouter(s, registry, decisioner),
		Migrate:  router.NewMigrator(s, registry),
		Decision: decisioner, Evidence: router.NewEvidence(m),
		Compute: computeSvc, closer: closer,
	}, nil
}

func (s *KnowledgeService) Put(ctx context.Context, subject, id string, d *okf.Document) (router.WriteResult, error) {
	return s.Write.Put(ctx, subject, id, d)
}

func (s *KnowledgeService) Query(ctx context.Context, subject, text string, topK int) (router.QueryResult, error) {
	return s.Read.Query(ctx, subject, text, topK)
}

func (s *KnowledgeService) RunMigration(ctx context.Context, opt router.MigrationOptions) (router.MigrationReport, error) {
	return s.Migrate.Run(ctx, opt)
}

func (s *KnowledgeService) Decisions(ctx context.Context, conceptID string, limit int) ([]meta.DecisionRecord, error) {
	return s.Evidence.List(ctx, conceptID, limit)
}

func (s *KnowledgeService) Concepts(ctx context.Context) ([]string, error) {
	return s.Store.List(ctx)
}

// RunComputation executes an Attested Computation through the full
// load -> bind -> execute -> attest -> gate -> audit chain.
func (s *KnowledgeService) RunComputation(ctx context.Context, req compute.Request) (compute.Envelope, error) {
	if s.Compute == nil {
		return compute.Envelope{}, fmt.Errorf("service: computation service is not assembled")
	}
	return s.Compute.Run(ctx, req)
}

// ComputationAudit returns the newest verdicts for a concept.
func (s *KnowledgeService) ComputationAudit(ctx context.Context, conceptID string, limit int) ([]meta.ComputationRecord, error) {
	if s.Compute == nil {
		return []meta.ComputationRecord{}, nil
	}
	return s.Compute.List(ctx, conceptID, limit)
}

// Health reports the state of all dependencies.
func (s *KnowledgeService) Health(ctx context.Context) map[string]string {
	out := map[string]string{"status": "ok"}
	if s.Config.Postgres.DSN == "" {
		out["postgres"] = "fake (offline)"
	} else {
		out["postgres"] = "configured"
	}
	if s.Compute != nil {
		out["compute_runtimes"] = strings.Join(s.Compute.Runtimes.Names(), ",")
	}
	for name, err := range s.Registry.HealthAll(ctx) {
		if err != nil {
			out["adapter:"+name] = "error: " + err.Error()
			out["status"] = "degraded"
		} else {
			out["adapter:"+name] = "ok"
		}
	}
	return out
}

// Close releases plugin resources before the metadata connection so a plugin
// can never outlive the store it depends on.
func (s *KnowledgeService) Close() error {
	var errs []error
	if s.Registry != nil {
		if err := s.Registry.Close(context.Background()); err != nil {
			errs = append(errs, err)
		}
	}
	if s.closer != nil {
		if err := s.closer(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
