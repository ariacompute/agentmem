package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/auth"
	"agentmem/internal/compute"
	"agentmem/internal/config"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
	"agentmem/internal/router"
	"agentmem/internal/service"
	"agentmem/internal/store"
	"agentmem/internal/webui"
)

// Every command must reject an unknown flag instead of ignoring it.
func TestCommandsRejectUnknownFlags(t *testing.T) {
	names := []string{
		"serve", "put", "query", "migrate", "decisions",
		"concepts", "compute", "computations", "validate", "eval",
	}
	for _, name := range names {
		if err := Run([]string{name, "--definitely-not-a-flag"}, io.Discard); err == nil {
			t.Fatalf("%s must reject an unknown flag", name)
		}
	}
}

// envFile writes a minimal .env that keeps every path inside the test directory.
func envFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "AGENTMEM_GIT_ROOT=" + filepath.Join(dir, "bundle") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// failingDecisioner fails every routing decision.
type failingDecisioner struct{ err error }

func (f failingDecisioner) Decide(context.Context, router.DecisionTask) (router.RouteDecision, error) {
	return router.RouteDecision{}, f.err
}

// failingMeta fails the audit queries.
type failingMeta struct{ *meta.FakeStore }

func (failingMeta) ListDecisions(context.Context, meta.DecisionQuery) ([]meta.DecisionRecord, error) {
	return nil, errors.New("decisions unavailable")
}
func (failingMeta) ListComputations(context.Context, meta.ComputationQuery) ([]meta.ComputationRecord, error) {
	return nil, errors.New("computations unavailable")
}

// brokenService is a fully assembled but failing service.
func brokenService(t *testing.T) *service.KnowledgeService {
	t.Helper()
	fake := meta.NewFakeStore()
	badBundle := &okf.Bundle{Root: filepath.Join(t.TempDir(), "missing", "root")}
	badStore := &store.OKFStore{Bundle: badBundle, Meta: fake}
	return &service.KnowledgeService{
		Config:   config.Default(),
		Store:    badStore,
		Registry: adapter.NewRegistry(),
		Read: &router.ReadRouter{
			Store: badStore, Registry: adapter.NewRegistry(),
			Decisioner: failingDecisioner{err: errors.New("router down")},
		},
		Migrate:  router.NewMigrator(badStore, adapter.NewRegistry()),
		Evidence: router.NewEvidence(failingMeta{FakeStore: fake}),
		Compute:  &compute.Service{Audit: failingMeta{FakeStore: fake}},
	}
}

func withBrokenService(t *testing.T) {
	t.Helper()
	previous := assemble
	assemble = func(string) (*service.KnowledgeService, error) { return brokenService(t), nil }
	t.Cleanup(func() { assemble = previous })
}

func TestCommandsPropagateServiceErrors(t *testing.T) {
	withBrokenService(t)
	env := envFile(t)
	cases := map[string][]string{
		"query":        {"query", "--env", env, "--q", "revenue"},
		"migrate":      {"migrate", "--env", env, "--target", "missing"},
		"decisions":    {"decisions", "--env", env},
		"concepts":     {"concepts", "--env", env},
		"computations": {"computations", "--env", env},
	}
	for name, args := range cases {
		if err := Run(args, io.Discard); err == nil {
			t.Fatalf("%s must propagate the service error", name)
		}
	}
}

func TestPutRejectsInvalidVisibility(t *testing.T) {
	env := envFile(t)
	err := Run([]string{"put", "--env", env, "--id", "team/a", "--visibility", "world"}, io.Discard)
	if err == nil {
		t.Fatal("an invalid visibility must fail the write")
	}
}

func TestComputeRejectsInvalidConceptID(t *testing.T) {
	env := envFile(t)
	if err := Run([]string{"compute", "--env", env, "--id", "../escape"}, io.Discard); err == nil {
		t.Fatal("an escaping concept id must fail the computation")
	}
}

func TestEvalSkipsBlankLinesAndRejectsBadRows(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.jsonl")
	if err := os.WriteFile(good, []byte("\n{\"query\":\"q\",\"expected\":[]}\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run([]string{"eval", "--dataset", good, "--k", "3"}, &out); err != nil {
		t.Fatalf("blank lines must be skipped: %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("eval must report a ranking")
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"eval", "--dataset", bad}, io.Discard); err == nil {
		t.Fatal("invalid JSON must fail the evaluation")
	}

	long := filepath.Join(dir, "long.jsonl")
	if err := os.WriteFile(long, []byte(`{"query":"`+strings.Repeat("x", 70*1024)+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"eval", "--dataset", long}, io.Discard); err == nil {
		t.Fatal("an over-long line must fail the scan")
	}
}

func TestComputeParamFlagRejectsMalformedValue(t *testing.T) {
	var params paramFlags
	if err := params.Set("value-without-name"); err == nil {
		t.Fatal("--param must require name=value")
	}
	empty := &paramFlags{}
	if err := empty.Set("=1"); err == nil {
		t.Fatal("--param must require a name")
	}
	if err := empty.Set("year=2026"); err != nil {
		t.Fatal(err)
	}
	if empty.values["year"] != "2026" {
		t.Fatalf("values = %+v", empty.values)
	}
	if got := empty.String(); got != "year=2026" {
		t.Fatalf("String = %q", got)
	}
}

// blockingRunner blocks until Shutdown releases it.
type blockingRunner struct {
	shutdownErr error
	resumeErr   error
	resume      chan error
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{resume: make(chan error, 1)}
}

func (b *blockingRunner) ListenAndServe() error { return <-b.resume }
func (b *blockingRunner) Shutdown(context.Context) error {
	if b.shutdownErr != nil {
		return b.shutdownErr
	}
	b.resume <- b.resumeErr
	return nil
}

// immediateRunner fails before any signal arrives.
type immediateRunner struct{ err error }

func (r immediateRunner) ListenAndServe() error          { return r.err }
func (r immediateRunner) Shutdown(context.Context) error { return nil }

func signalChannel() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	ch <- syscall.SIGTERM
	return ch
}

func TestServeLoopBranches(t *testing.T) {
	if err := serveLoop(immediateRunner{err: errors.New("bind refused")}, make(chan os.Signal)); err == nil {
		t.Fatal("a listen failure must be reported")
	}
	if err := serveLoop(immediateRunner{err: http.ErrServerClosed}, make(chan os.Signal)); err != nil {
		t.Fatalf("a closed server is a clean stop: %v", err)
	}

	// Shutdown failure is reported instead of being swallowed.
	failing := newBlockingRunner()
	failing.shutdownErr = errors.New("shutdown refused")
	if err := serveLoop(failing, signalChannel()); err == nil {
		t.Fatal("a shutdown failure must be reported")
	}

	// A clean shutdown returns nil.
	clean := newBlockingRunner()
	clean.resumeErr = http.ErrServerClosed
	if err := serveLoop(clean, signalChannel()); err != nil {
		t.Fatalf("clean shutdown: %v", err)
	}

	// A listen error racing the signal is still reported.
	racy := newBlockingRunner()
	racy.resumeErr = errors.New("accept failed")
	if err := serveLoop(racy, signalChannel()); err == nil {
		t.Fatal("a listen error after the signal must be reported")
	}
}

func TestServeFailsClosedOnStartupErrors(t *testing.T) {
	env := envFile(t)
	cases := map[string]func(t *testing.T){
		"auth": func(t *testing.T) {
			previous := buildAuth
			buildAuth = func(*config.Config) (*auth.Authenticator, error) {
				return nil, errors.New("no authenticator")
			}
			t.Cleanup(func() { buildAuth = previous })
		},
		"assemble": func(t *testing.T) {
			previous := assembleService
			assembleService = func(context.Context, *config.Config) (*service.KnowledgeService, error) {
				return nil, errors.New("no service")
			}
			t.Cleanup(func() { assembleService = previous })
		},
		"console": func(t *testing.T) {
			previous := newConsole
			newConsole = func() (*webui.Handler, error) { return nil, errors.New("no console") }
			t.Cleanup(func() { newConsole = previous })
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			setup(t)
			if err := Run([]string{"serve", "--env", env}, io.Discard); err == nil {
				t.Fatalf("%s failure must abort startup", name)
			}
		})
	}
}

// TestServerOptionsHonourTheConsoleSwitch covers the API-only posture: with the
// switch off the console constructor must not run at all and the Options
// interface must be a real nil (a typed-nil *webui.Handler would pass an
// `opts.Console != nil` check and panic on the first request).
func TestServerOptionsHonourTheConsoleSwitch(t *testing.T) {
	previous := newConsole
	calls := 0
	newConsole = func() (*webui.Handler, error) {
		calls++
		return nil, errors.New("the console must not be constructed")
	}
	t.Cleanup(func() { newConsole = previous })

	cfg := config.Default()
	cfg.Console.Enabled = false
	opts, err := serverOptions(cfg, nil)
	if err != nil {
		t.Fatalf("an API-only deployment must start cleanly: %v", err)
	}
	if opts.Console != nil {
		t.Fatalf("disabled console leaked into Options: %#v", opts.Console)
	}
	if calls != 0 {
		t.Fatalf("newConsole called %d times with the switch off", calls)
	}

	// Enabled still fails closed when the embed is unusable.
	cfg.Console.Enabled = true
	if _, err := serverOptions(cfg, nil); err == nil {
		t.Fatal("a broken embed must abort startup when the console is enabled")
	}
}

func TestBuildAuthenticatorRejectsUnknownScope(t *testing.T) {
	_, err := buildAuthenticator(&config.Config{Auth: config.AuthConfig{
		Tokens: []config.AuthTokenSpec{{
			TokenSHA256: strings.Repeat("a", 64), Subject: "alice", Scopes: []string{"bogus"},
		}},
	}})
	if err == nil {
		t.Fatal("an unknown scope must be rejected")
	}
}
