package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/okf"
)

type errDecisioner struct{}

func (errDecisioner) Decide(context.Context, DecisionTask) (RouteDecision, error) {
	return RouteDecision{}, errors.New("boom")
}

func TestSystemOneConfigDefaults(t *testing.T) {
	s := NewSystemOneDecisioner(SystemOneConfig{})
	if s.cfg.TimeoutMS != DefaultTimeoutMS || s.cfg.CacheCapacity != DefaultCacheCapacity ||
		s.cfg.MaxChoices != DefaultMaxChoices || s.cfg.MinConfidence != DefaultMinConfidence ||
		s.cfg.Fallback != CanonicalTarget {
		t.Fatalf("defaults = %+v", s.cfg)
	}
	// An endpoint without an allowlist pins the host.
	s2 := NewSystemOneDecisioner(SystemOneConfig{Endpoint: "https://api.example.com"})
	if len(s2.hosts) != 1 || s2.hosts[0] != "api.example.com" {
		t.Fatalf("hosts = %v", s2.hosts)
	}
}

func TestSystemOneDecideNoEligibleAndTruncation(t *testing.T) {
	s := NewSystemOneDecisioner(SystemOneConfig{})
	if _, err := s.Decide(context.Background(), DecisionTask{Kind: "read"}); !errors.Is(err, ErrNoEligibleTargets) {
		t.Fatalf("no eligible = %v", err)
	}
	s2 := NewSystemOneDecisioner(SystemOneConfig{MaxChoices: 1})
	if _, err := s2.Decide(context.Background(), DecisionTask{Kind: "read", Eligible: []string{"okf", "memory"}}); err != nil {
		t.Fatalf("truncated decide: %v", err)
	}
}

func TestSystemOneCacheEvictionAndExistingKey(t *testing.T) {
	s := NewSystemOneDecisioner(SystemOneConfig{CacheCapacity: 1})
	ctx := context.Background()
	if _, err := s.Decide(ctx, DecisionTask{Kind: "read", Text: "a", Eligible: []string{"okf", "memory"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, DecisionTask{Kind: "read", Text: "b", Eligible: []string{"okf", "memory"}}); err != nil {
		t.Fatal(err)
	}
	if s.CacheSize() != 1 {
		t.Fatalf("cache size = %d", s.CacheSize())
	}
	// cachePut with an existing key is a no-op.
	s.cachePut(cacheKey(DecisionTask{Kind: "read", Text: "b"}, []string{"memory", "okf"}), RouteDecision{})
}

func systemOneServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSystemOneCallBranches(t *testing.T) {
	ctx := context.Background()
	task := DecisionTask{Kind: "read", Text: "q", Eligible: []string{"okf", "memory"}}

	// High confidence: the chosen target is returned.
	okSrv := systemOneServer(t, 200, `{"choice":"memory","confidence":0.9,"reason":"r"}`)
	okD := NewSystemOneDecisioner(SystemOneConfig{Endpoint: okSrv.URL, AllowedHosts: []string{"127.0.0.1"}})
	d, err := okD.Decide(ctx, task)
	if err != nil || len(d.Targets) != 1 || d.Targets[0] != "memory" {
		t.Fatalf("high confidence = %+v (%v)", d, err)
	}

	// Low confidence uses the in-pool fallback.
	lowSrv := systemOneServer(t, 200, `{"choice":"memory","confidence":0.1,"reason":"meh"}`)
	lowD := NewSystemOneDecisioner(SystemOneConfig{Endpoint: lowSrv.URL, AllowedHosts: []string{"127.0.0.1"}})
	if d, err := lowD.Decide(ctx, task); err != nil || d.RuleID != "systemone_min_confidence" {
		t.Fatalf("low confidence = %+v (%v)", d, err)
	}

	// Low confidence with an out-of-pool fallback fails closed.
	lowBad := NewSystemOneDecisioner(SystemOneConfig{Endpoint: lowSrv.URL, AllowedHosts: []string{"127.0.0.1"}, Fallback: "ghost"})
	if _, err := lowBad.Decide(ctx, task); err == nil {
		t.Fatal("out-of-pool fallback must error")
	}

	// Empty choice, non-2xx, bad JSON and an out-of-pool choice all error.
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"empty choice":   {200, `{"choice":""}`},
		"server error":   {500, `{}`},
		"bad json":       {200, `not json`},
		"invalid target": {200, `{"choice":"ghost"}`},
	} {
		srv := systemOneServer(t, tc.status, tc.body)
		d := NewSystemOneDecisioner(SystemOneConfig{Endpoint: srv.URL, AllowedHosts: []string{"127.0.0.1"}})
		if _, err := d.Decide(ctx, task); err == nil {
			t.Fatalf("%s must error", name)
		}
	}
}

func TestSystemOneCallValidateEndpointError(t *testing.T) {
	// The endpoint host is not in the injected allowlist.
	d := NewSystemOneDecisioner(SystemOneConfig{Endpoint: "http://evil.example.com", AllowedHosts: []string{"good.example.com"}})
	if _, err := d.Decide(context.Background(), DecisionTask{Kind: "read", Eligible: []string{"okf", "memory"}}); err == nil {
		t.Fatal("disallowed endpoint must error")
	}
}

func TestMigratorRunEdgeCases(t *testing.T) {
	_, s, r := newWriteFixture(t)
	m := NewMigrator(s, r)
	ctx := context.Background()

	cancel, done := context.WithCancel(context.Background())
	done()
	if _, err := m.Run(cancel, MigrationOptions{}); err == nil {
		t.Fatal("cancelled run")
	}
	if _, err := m.Run(ctx, MigrationOptions{Job: strings.Repeat("x", 200)}); err == nil {
		t.Fatal("long job name")
	}
	if _, err := m.Run(ctx, MigrationOptions{Target: CanonicalTarget}); err == nil {
		t.Fatal("canonical target")
	}
	if _, err := m.Run(ctx, MigrationOptions{Target: "ghost"}); err == nil {
		t.Fatal("unregistered target")
	}
	if _, err := m.Run(ctx, MigrationOptions{Target: "memory", Direction: "weird"}); err == nil {
		t.Fatal("unknown direction")
	}
	if _, err := m.Run(ctx, MigrationOptions{Target: "memory", Direction: DirectionToRAG, Limit: 5000}); err != nil {
		t.Fatalf("clamped limit: %v", err)
	}
}

func TestMigratorHashOfErrors(t *testing.T) {
	_, s, r := newWriteFixture(t)
	m := NewMigrator(s, r)
	if _, err := m.hashOf(""); err == nil {
		t.Fatal("empty id")
	}
	if _, err := m.hashOf("missing/concept"); err == nil {
		t.Fatal("missing file")
	}
	if _, err := s.Put(context.Background(), "concepts/x", simpleDoc("Note", "b")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.hashOf("concepts/x"); err != nil {
		t.Fatalf("existing: %v", err)
	}
}

func TestReadRouterTopKClamp(t *testing.T) {
	_, s, r := newWriteFixture(t)
	rr := NewReadRouter(s, r, nil)
	ctx := context.Background()
	if _, err := rr.Query(ctx, "alice", "x", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := rr.Query(ctx, "alice", "x", 200); err != nil {
		t.Fatal(err)
	}
	// An empty needle yields no canonical hits.
	if rr.canonicalHits("") != 0 {
		t.Fatal("empty needle")
	}
}

func TestMergeHitsReplaceHigherScore(t *testing.T) {
	byTarget := map[string][]adapter.Hit{
		"memory": {
			{ConceptID: "c", Source: "memory", Score: 0.4},
			{ConceptID: "c", Source: "memory", Score: 0.9},
		},
	}
	out, _ := mergeHits(byTarget, 5)
	if len(out) != 1 || out[0].Score != 0.9 {
		t.Fatalf("replace = %+v", out)
	}
}

func TestNewWriteRouterDefault(t *testing.T) {
	_, s, r := newWriteFixture(t)
	w := NewWriteRouter(s, r, nil)
	if w.Decisioner == nil {
		t.Fatal("nil decisioner must default")
	}
}

type noPutAdapter struct{}

func (noPutAdapter) Name() string                                     { return "noput" }
func (noPutAdapter) Capabilities() adapter.Capabilities               { return adapter.Capabilities{Query: true} }
func (noPutAdapter) Put(context.Context, string, *okf.Document) error { return nil }
func (noPutAdapter) Query(context.Context, adapter.Query) ([]adapter.Hit, error) {
	return nil, nil
}
func (noPutAdapter) Delete(context.Context, string) error { return nil }
func (noPutAdapter) Fingerprint() string                  { return "noput-v1" }
func (noPutAdapter) Health(context.Context) error         { return nil }

func TestWriteRouterAuthorizationAndUnsupportedProjection(t *testing.T) {
	w, _, r := newWriteFixture(t)
	ctx := context.Background()
	if _, err := w.Put(ctx, "alice", "notes/a", simpleDoc("Note", "v1")); err != nil {
		t.Fatal(err)
	}
	// A different subject may not overwrite Alice's body.
	if _, err := w.Put(ctx, "bob", "notes/a", simpleDoc("Note", "v2")); err == nil {
		t.Fatal("cross-owner update must fail")
	}

	// A projection-only adapter lacking Put is recorded as failed.
	_ = r.RegisterAdapter(noPutAdapter{})
	w.Decisioner = &fixedDecisioner{d: RouteDecision{Targets: []string{CanonicalTarget, "noput"}, RuleID: "t", Source: SourceRule}}
	res, err := w.Put(ctx, "alice", "notes/n", simpleDoc("Note", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Projections) != 1 || res.Projections[0].Status != "failed" {
		t.Fatalf("projections = %+v", res.Projections)
	}
}

func TestCompositeFallbackError(t *testing.T) {
	c := NewComposite(NewRuleEngine(), errDecisioner{})
	d, err := c.Decide(context.Background(), DecisionTask{
		Kind: "write", Text: "general note", Features: Features{ByteSize: 100},
		Eligible: []string{"okf", "memory"},
	})
	if err != nil {
		t.Fatalf("composite must not return an error: %v", err)
	}
	if !strings.HasPrefix(d.Reason, "fail-closed") {
		t.Fatalf("fallback error must fail closed: %+v", d)
	}
}
