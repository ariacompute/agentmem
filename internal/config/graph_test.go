package config

import (
	"errors"
	"strings"
	"testing"
)

func TestGraphConfigDefaults(t *testing.T) {
	g := Default().Graph
	if !g.Enabled || g.Backend != "builtin" || g.MaxDepth != 3 || g.RRFK != 60 || g.CoverageMin != 0.6 {
		t.Fatalf("defaults = %+v", g)
	}
	if g.RemoteURL != "" || g.RemoteTokenEnv != "" {
		t.Fatalf("defaults must not point at a remote engine: %+v", g)
	}
}

func TestGraphConfigValidation(t *testing.T) {
	cases := map[string]func(*GraphConfig){
		"unknown backend":  func(g *GraphConfig) { g.Backend = "neo4j" },
		"depth low":        func(g *GraphConfig) { g.MaxDepth = 0 },
		"depth high":       func(g *GraphConfig) { g.MaxDepth = 9 },
		"nodes low":        func(g *GraphConfig) { g.MaxNodes = 0 },
		"nodes high":       func(g *GraphConfig) { g.MaxNodes = 2000000 },
		"edges low":        func(g *GraphConfig) { g.MaxEdges = 0 },
		"edges high":       func(g *GraphConfig) { g.MaxEdges = 6000000 },
		"coverage low":     func(g *GraphConfig) { g.CoverageMin = -0.1 },
		"coverage high":    func(g *GraphConfig) { g.CoverageMin = 1.1 },
		"rrf low":          func(g *GraphConfig) { g.RRFK = 0 },
		"rrf high":         func(g *GraphConfig) { g.RRFK = 1001 },
		"timeout high":     func(g *GraphConfig) { g.RemoteTimeoutMS = 120001 },
		"timeout negative": func(g *GraphConfig) { g.RemoteTimeoutMS = -1 },
		"remote no url":    func(g *GraphConfig) { g.Backend = "remote" },
		"remote bad tokenenv": func(g *GraphConfig) {
			g.Backend = "remote"
			g.RemoteURL = "https://graph.example.com"
			g.RemoteTokenEnv = "1BAD-NAME"
		},
		"remote no allowlist": func(g *GraphConfig) {
			g.Backend = "remote"
			g.RemoteURL = "https://graph.example.com"
			g.RemoteTokenEnv = "GRAPH_TOKEN"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default()
			mutate(&c.Graph)
			if err := c.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}

	ok := Default()
	ok.Graph.Backend = "remote"
	ok.Graph.RemoteURL = "https://graph.example.com"
	ok.Graph.RemoteTokenEnv = "GRAPH_TOKEN"
	ok.Egress.AllowedHosts = []string{"graph.example.com"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid remote config: %v", err)
	}
	if err := ok.Validate(); err != nil && errors.Is(err, ErrUnsupported) {
		t.Fatalf("unexpected unsupported error: %v", err)
	}
}

func TestGraphRemoteFailsClosedInProduction(t *testing.T) {
	c := Default()
	c.Server.Mode = "production"
	c.Postgres.DSN = "postgres://u:p@db:5432/agentmem?sslmode=require"
	c.Auth = AuthConfig{Enabled: true, Tokens: []AuthTokenSpec{{
		TokenSHA256: strings.Repeat("a", 64), Subject: "ops", Scopes: []string{"health:read"},
	}}}
	c.Graph.Backend = "remote"
	c.Graph.RemoteURL = "https://graph.example.com"
	c.Graph.RemoteTokenEnv = "AGENTMEM_TEST_GRAPH_TOKEN"
	c.Egress.AllowedHosts = []string{"graph.example.com"}
	t.Setenv("AGENTMEM_TEST_GRAPH_TOKEN", "")
	if err := c.Validate(); err == nil {
		t.Fatal("production must require the remote token in the process environment")
	}
	t.Setenv("AGENTMEM_TEST_GRAPH_TOKEN", "secret")
	if err := c.Validate(); err != nil {
		t.Fatalf("token present: %v", err)
	}
	// A disabled graph must not force the remote checks.
	c.Graph.Enabled = false
	t.Setenv("AGENTMEM_TEST_GRAPH_TOKEN", "")
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled graph: %v", err)
	}
}

func TestGraphEnvKeysLoadAndRejectUnknown(t *testing.T) {
	p := writeEnv(t, strings.Join([]string{
		"AGENTMEM_GRAPH_ENABLED=false",
		"AGENTMEM_GRAPH_BACKEND=builtin",
		"AGENTMEM_GRAPH_REMOTE_URL=",
		"AGENTMEM_GRAPH_REMOTE_TOKEN_ENV=",
		"AGENTMEM_GRAPH_REMOTE_TIMEOUT_MS=1500",
		"AGENTMEM_GRAPH_MAX_DEPTH=4",
		"AGENTMEM_GRAPH_MAX_NODES=500",
		"AGENTMEM_GRAPH_MAX_EDGES=900",
		"AGENTMEM_GRAPH_COVERAGE_MIN=0.75",
		"AGENTMEM_GRAPH_RRF_K=10",
		"",
	}, "\n"))
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Graph.Enabled || cfg.Graph.MaxDepth != 4 || cfg.Graph.MaxNodes != 500 ||
		cfg.Graph.MaxEdges != 900 || cfg.Graph.CoverageMin != 0.75 || cfg.Graph.RRFK != 10 ||
		cfg.Graph.RemoteTimeoutMS != 1500 {
		t.Fatalf("loaded graph = %+v", cfg.Graph)
	}
	if _, err := Load(writeEnv(t, "AGENTMEM_GRAPH_TYPO=1\n")); err == nil {
		t.Fatal("unknown graph keys must fail closed")
	}
	keys := map[string]bool{}
	for _, k := range KnownEnvKeys() {
		keys[k] = true
	}
	for _, want := range []string{
		"AGENTMEM_GRAPH_ENABLED", "AGENTMEM_GRAPH_BACKEND", "AGENTMEM_GRAPH_REMOTE_URL",
		"AGENTMEM_GRAPH_REMOTE_TOKEN_ENV", "AGENTMEM_GRAPH_REMOTE_TIMEOUT_MS",
		"AGENTMEM_GRAPH_MAX_DEPTH", "AGENTMEM_GRAPH_MAX_NODES", "AGENTMEM_GRAPH_MAX_EDGES",
		"AGENTMEM_GRAPH_COVERAGE_MIN", "AGENTMEM_GRAPH_RRF_K",
	} {
		if !keys[want] {
			t.Fatalf("missing supported key %q", want)
		}
	}
}

func TestGraphPermissionIsKnown(t *testing.T) {
	c := Default()
	c.Auth = AuthConfig{Enabled: true, Tokens: []AuthTokenSpec{{
		TokenSHA256: strings.Repeat("b", 64), Subject: "ops", Scopes: []string{"graph:write"},
	}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("graph:write must be a known scope: %v", err)
	}
}
