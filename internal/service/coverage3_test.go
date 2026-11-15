package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentmem/internal/adapter"
	"agentmem/internal/compute"
	"agentmem/internal/config"
	"agentmem/internal/meta"
)

func TestAssembleRejectsUnopenableBundle(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "bundle")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Git.Root = blocker
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("a bundle root that is a regular file must fail")
	}
}

func TestAssembleRejectsBrokenRepository(t *testing.T) {
	root := t.TempDir()
	// A .git file that does not point at a git directory is not "not exists".
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not-a-gitdir-link\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Git.Root = root
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("a broken repository must fail assembly")
	}
}

func TestAssembleRejectsUnwritableOfflineSnapshot(t *testing.T) {
	dir := t.TempDir()
	// The offline snapshot lives next to the bundle; a directory there cannot
	// be read as a JSON snapshot.
	if err := os.Mkdir(filepath.Join(dir, "meta.fake.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Git.Root = filepath.Join(dir, "bundle")
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("an unreadable offline snapshot must fail assembly")
	}
}

func TestAssembleRejectsUnbuildableAdapter(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	cfg.Adapters = append(cfg.Adapters, config.AdapterConfig{Name: "engine", Kind: "http", Enabled: true})
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("an adapter without an engine declaration must fail assembly")
	}
}

func TestAssembleRejectsFailingAdapterStart(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	cfg.Adapters = append(cfg.Adapters, config.AdapterConfig{
		Name: "lightrag", Kind: "lightrag", Enabled: true, Endpoint: "http://127.0.0.1:1/engine",
	})
	cfg.Egress.AllowedHosts = []string{"127.0.0.1"}
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("an unreachable engine must fail the adapter start")
	}
}

func TestAssembleAppliesComputeDefaults(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	cfg.Compute = config.ComputeConfig{}
	svc, err := Assemble(context.Background(), cfg)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	defer svc.Close()
	if svc.Compute == nil || svc.Compute.MaxRows != config.DefaultCompute().MaxRows {
		t.Fatalf("compute defaults not applied: %+v", svc.Compute)
	}
}

func TestAssembleReportsRegistrationFailures(t *testing.T) {
	cases := map[string]func(*testing.T){
		"bridges": func(t *testing.T) {
			previous := registerBridges
			registerBridges = func(*adapter.Registry) error { return errors.New("bridge table broken") }
			t.Cleanup(func() { registerBridges = previous })
		},
		"plugins": func(t *testing.T) {
			previous := registerPlugins
			registerPlugins = func(*adapter.Registry) error { return errors.New("plugin table broken") }
			t.Cleanup(func() { registerPlugins = previous })
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			setup(t)
			cfg := config.Default()
			cfg.Git.Root = t.TempDir()
			if _, err := Assemble(context.Background(), cfg); err == nil {
				t.Fatalf("%s registration failure must abort assembly", name)
			}
		})
	}
}

// plainMeta forwards the metadata port without implementing SQLExecutor.
type plainMeta struct{ meta.MetaStore }

func withOpenMetadata(t *testing.T, store meta.MetaStore) {
	t.Helper()
	previous := openMetadata
	openMetadata = func(context.Context, *config.Config) (meta.MetaStore, meta.VectorStore, func() error, error) {
		return store, meta.NewFakeStore(), nil, nil
	}
	t.Cleanup(func() { openMetadata = previous })
}

func TestAssembleRequiresSQLExecutor(t *testing.T) {
	withOpenMetadata(t, plainMeta{MetaStore: meta.NewFakeStore()})
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("a store without SQL execution must fail assembly")
	}
}

func TestAssembleReportsSidecarConfigurationFailure(t *testing.T) {
	previous := configureRuntimes
	configureRuntimes = func(*compute.RuntimeRegistry, []compute.SidecarSpec, []string) error {
		return errors.New("sidecar refused")
	}
	t.Cleanup(func() { configureRuntimes = previous })
	cfg := config.Default()
	cfg.Git.Root = t.TempDir()
	if _, err := Assemble(context.Background(), cfg); err == nil {
		t.Fatal("a failing sidecar configuration must abort assembly")
	}
}
