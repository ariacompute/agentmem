// Package service assembles the application from ports and exposes the three
// use cases: Put, Query and Migrate.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentmem/internal/adapter"
	"agentmem/internal/bridges"
	"agentmem/internal/compute"
	"agentmem/internal/config"
	"agentmem/internal/graph"
	"agentmem/internal/meta"
	"agentmem/internal/netsafe"
	"agentmem/internal/okf"
	"agentmem/internal/plugins"
	"agentmem/internal/router"
	"agentmem/internal/store"
	"agentmem/internal/vcs"
)

// openMetadata returns the structured store, its vector view and a closer.
// It is a seam so the fail-closed "not a SQLExecutor" path stays testable.
var openMetadata = func(ctx context.Context, cfg *config.Config) (meta.MetaStore, meta.VectorStore, func() error, error) {
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
			return nil, nil, nil, err
		}
		m, vectors, closer = pg, pg, pg.Close
		return m, vectors, closer, nil
	}
	// Offline fallback: persist the fake's structured state so separate CLI
	// processes share ACL metadata, projections and the decision audit.
	snapshot := filepath.Join(filepath.Dir(cfg.Git.Root), "meta.fake.json")
	fake, err := meta.NewFileFakeStore(snapshot)
	if err != nil {
		return nil, nil, nil, err
	}
	return fake, fake, fake.Close, nil
}

// configureRuntimes wires the declared sidecars. It is a seam for the same
// reason openMetadata is.
var configureRuntimes = func(rt *compute.RuntimeRegistry, specs []compute.SidecarSpec, hosts []string) error {
	return rt.Configure(specs, hosts, nil)
}

// The two plugin tables are seams so the fail-closed registration paths stay
// testable; production always uses the compiled-in tables.
var (
	registerBridges = bridges.RegisterKinds
	registerPlugins = plugins.RegisterAll
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
	// Graph is the read surface (built-in searcher or the optional remote
	// engine); the writer and projector are only assembled for the built-in
	// backend because Git remains the authority for every write.
	Graph          graph.Query
	GraphWriter    *graph.Writer
	GraphProjector *graph.Projector
	closer         func() error
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
	// Fail closed at assembly time on outbound endpoints: no adapter should
	// ever be able to reach a metadata service, a private network or a host
	// outside the operator's allowlist.
	if strings.TrimSpace(cfg.Router.SystemOne.Endpoint) != "" {
		if err := netsafe.ValidateEndpoint(cfg.Router.SystemOne.Endpoint, cfg.Egress.AllowedHosts); err != nil {
			return nil, fmt.Errorf("service: system one endpoint: %w", err)
		}
	}
	for _, a := range cfg.Adapters {
		if a.Enabled && a.Kind != "cli" && strings.TrimSpace(a.Endpoint) != "" {
			if err := netsafe.ValidateEndpoint(a.Endpoint, cfg.Egress.AllowedHosts); err != nil {
				return nil, fmt.Errorf("service: adapter %q endpoint: %w", a.Name, err)
			}
		}
	}
	// Same rule for outbound compute sidecars: an http executor outside the
	// allowlist must stop the process at start, not at the first calculation.
	for _, sc := range cfg.Compute.Sidecars {
		if strings.EqualFold(strings.TrimSpace(sc.Bridge), "http") {
			if err := netsafe.ValidateEndpoint(sc.Endpoint, cfg.Egress.AllowedHosts); err != nil {
				return nil, fmt.Errorf("service: compute sidecar %q endpoint: %w", sc.Runtime, err)
			}
		}
	}
	bundle, err := okf.Open(cfg.Git.Root)
	if err != nil {
		return nil, err
	}
	repo, err := vcs.NewGit(cfg.Git.Root, cfg.Git.AuthorName, cfg.Git.AuthorEmail)
	if err != nil {
		return nil, err
	}
	m, vectors, closer, err := openMetadata(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if closer == nil {
		closer = func() error { return nil }
	}
	registry := adapter.NewRegistry()
	if err := registerBridges(registry); err != nil {
		_ = closer()
		return nil, err
	}
	if err := registerPlugins(registry); err != nil {
		_ = closer()
		return nil, err
	}
	if err := registry.Build(cfg, adapter.Deps{
		Bundle: bundle, Vector: vectors, Meta: m, Embedder: adapter.NewHashingEmbedder(64),
		AllowedHosts: cfg.Egress.AllowedHosts,
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
	so.AllowedHosts = cfg.Egress.AllowedHosts
	// FailClosed stays unconditional: DefaultSystemOneConfig sets it to true
	// and the Composite hard-codes it. There is deliberately no .env switch.
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
	runtimes := compute.NewRuntimeRegistry(sqlExec, computeCfg.MaxRows)
	specs := make([]compute.SidecarSpec, 0, len(computeCfg.Sidecars))
	for _, sc := range computeCfg.Sidecars {
		specs = append(specs, compute.SidecarSpec{
			Runtime: sc.Runtime, Bridge: sc.Bridge, Endpoint: sc.Endpoint,
			Command: sc.Command, TimeoutMS: sc.TimeoutMS,
		})
	}
	if err := configureRuntimes(runtimes, specs, cfg.Egress.AllowedHosts); err != nil {
		_ = registry.Close(context.Background())
		_ = closer()
		return nil, err
	}
	computeSvc := compute.NewService(bundle, runtimes, m,
		time.Duration(computeCfg.TimeoutMS)*time.Millisecond, computeCfg.MaxRows)
	svc := &KnowledgeService{
		Config: cfg, Store: s, Registry: registry,
		Write:    router.NewWriteRouter(s, registry, decisioner),
		Read:     router.NewReadRouter(s, registry, decisioner),
		Migrate:  router.NewMigrator(s, registry),
		Decision: decisioner, Evidence: router.NewEvidence(m),
		Compute: computeSvc, closer: closer,
	}
	if err := svc.assembleGraph(ctx, cfg); err != nil {
		_ = registry.Close(context.Background())
		_ = closer()
		return nil, err
	}
	return svc, nil
}

// assembleGraph wires the graph read/write surfaces. The index port is
// mandatory when the feature is on: silently disabling the graph would make an
// enabled feature look empty rather than broken.
func (s *KnowledgeService) assembleGraph(ctx context.Context, cfg *config.Config) error {
	if !cfg.Graph.Enabled {
		return nil
	}
	idx, ok := s.Store.Meta.(meta.GraphIndex)
	if !ok {
		return fmt.Errorf("service: metadata store does not implement GraphIndex")
	}
	projector := &graph.Projector{Bodies: s.Store.Bundle, Meta: s.Store.Meta, Index: idx}
	searcher := &graph.Searcher{
		Meta: s.Store.Meta, Index: idx, Projector: projector, Bodies: s.Store.Bundle,
	}
	s.GraphProjector = projector
	s.GraphWriter = &graph.Writer{
		Meta: s.Store.Meta, Store: storeDocuments{s.Store}, Projector: projector, Searcher: searcher,
	}
	s.Graph = searcher
	if strings.EqualFold(cfg.Graph.Backend, "remote") {
		remote, err := graph.NewRemote(graph.RemoteConfig{
			Endpoint:     cfg.Graph.RemoteURL,
			Token:        os.Getenv(cfg.Graph.RemoteTokenEnv),
			Timeout:      time.Duration(cfg.Graph.RemoteTimeoutMS) * time.Millisecond,
			AllowedHosts: cfg.Egress.AllowedHosts,
		})
		if err != nil {
			return fmt.Errorf("service: graph backend: %w", err)
		}
		s.Graph = remote
		return nil
	}
	// Heal an empty index on first boot. A non-empty index is left alone: reads
	// repair individual rows lazily, which keeps startup independent of bundle
	// size.
	if stats, err := idx.GraphStats(ctx, "system"); err == nil && stats.Nodes == 0 {
		if report, err := projector.Rebuild(ctx); err != nil {
			slog.Warn("graph index bootstrap skipped", "error_type", fmt.Sprintf("%T", err))
		} else {
			slog.Info("graph index bootstrapped", "nodes", report.Nodes, "edges", report.Edges)
		}
	}
	return nil
}

// storeDocuments adapts store.OKFStore to the graph package's narrow ports so
// graph never imports the store (which would invert the layering).
type storeDocuments struct{ s *store.OKFStore }

func (d storeDocuments) Get(ctx context.Context, subject, id string) (*okf.Document, error) {
	return d.s.Get(ctx, subject, id)
}

func (d storeDocuments) PutAs(ctx context.Context, subject, id string, doc *okf.Document) (graph.WriteResult, error) {
	r, err := d.s.PutAs(ctx, subject, id, doc)
	if err != nil {
		return graph.WriteResult{}, err
	}
	return graph.WriteResult{
		ConceptID: r.ConceptID, ContentHash: r.ContentHash,
		CommitSHA: r.CommitSHA, Unchanged: r.Unchanged,
	}, nil
}

func (s *KnowledgeService) Put(ctx context.Context, subject, id string, d *okf.Document) (router.WriteResult, error) {
	res, err := s.Write.Put(ctx, subject, id, d)
	if err != nil {
		return res, err
	}
	// The canonical body is already committed, so a projection failure is a
	// warning (repaired by reindex), never a failed write.
	s.projectGraph(ctx, res.ConceptID)
	return res, nil
}

func (s *KnowledgeService) projectGraph(ctx context.Context, id string) {
	if s.GraphProjector == nil || strings.TrimSpace(id) == "" {
		return
	}
	_, _, warnings, err := s.GraphProjector.Sync(ctx, id)
	if err != nil {
		slog.Warn("graph projection failed", "concept_id", id, "error_type", fmt.Sprintf("%T", err))
		return
	}
	for _, w := range warnings {
		slog.Warn("graph projection warning", "concept_id", id, "warning", w)
	}
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
	if s.Config != nil {
		switch {
		case !s.Config.Graph.Enabled:
			out["graph"] = "disabled"
		case s.Graph == nil:
			out["graph"] = "unavailable"
		default:
			out["graph"] = s.Config.Graph.Backend
		}
	}
	for name, err := range s.Registry.HealthAll(ctx) {
		if err != nil {
			// Adapter errors can contain URLs, DSNs or external process stderr.
			// Only the error type reaches the log; the response stays generic.
			slog.Warn("adapter unhealthy", "adapter", name, "error_type", fmt.Sprintf("%T", err))
			out["adapter:"+name] = "unhealthy"
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
