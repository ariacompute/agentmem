package graph

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRemoteFailsClosed(t *testing.T) {
	if _, err := NewRemote(RemoteConfig{}); err == nil {
		t.Fatal("empty endpoint must fail")
	}
	if _, err := NewRemote(RemoteConfig{Endpoint: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("missing token must fail")
	}
	if _, err := NewRemote(RemoteConfig{Endpoint: "http://203.0.113.9:1", Token: "t", AllowedHosts: []string{"127.0.0.1"}}); err == nil {
		t.Fatal("host outside the egress allowlist must fail")
	}
	if _, err := NewRemote(RemoteConfig{Endpoint: "http://127.0.0.1:1", Token: "t", AllowedHosts: []string{"127.0.0.1"}}); err != nil {
		t.Fatalf("valid config: %v", err)
	}
}

func TestRemoteReadsTheContract(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing bearer on %s", r.URL.Path)
		}
		if r.URL.Path == "/v1/graph/schema" {
			if r.Header.Get("X-Agentmem-Subject") != "" {
				t.Errorf("schema call must not carry a subject")
			}
		} else if r.Header.Get("X-Agentmem-Subject") != "alice" {
			t.Errorf("subject header = %q on %s", r.Header.Get("X-Agentmem-Subject"), r.URL.Path)
		}
		if strings.Contains(r.URL.RawQuery, "subject=") {
			t.Errorf("subject leaked into the query string: %s", r.URL.RawQuery)
		}
		switch r.URL.Path {
		case "/v1/graph/node":
			if r.URL.Query().Get("id") != "metrics/a" {
				t.Errorf("node id = %q", r.URL.Query().Get("id"))
			}
			_, _ = w.Write([]byte(`{"concept_id":"metrics/a","node_type":"metric","label":"A"}`))
		case "/v1/graph/neighbors":
			if r.URL.Query().Get("edge_types") != "derived_from" {
				t.Errorf("edge types = %q", r.URL.Query().Get("edge_types"))
			}
			_, _ = w.Write([]byte(`{"root":"metrics/a","nodes":[{"concept_id":"metrics/a"}],"edges":[],"depths":{"metrics/a":0}}`))
		case "/v1/graph/path":
			_, _ = w.Write([]byte(`{"from":"metrics/a","to":"metrics/b","found":true,"hops":1}`))
		case "/v1/graph/search":
			if r.URL.Query().Get("node_types") != "metric" {
				t.Errorf("node types = %q", r.URL.Query().Get("node_types"))
			}
			_, _ = w.Write([]byte(`[{"concept_id":"metrics/a","label":"A"}]`))
		case "/v1/graph/answerability":
			_, _ = w.Write([]byte(`{"verdict":"insufficient","coverage":0.2,"gaps":[{"kind":"missing_term","detail":"x"}]}`))
		case "/v1/graph/stats":
			_, _ = w.Write([]byte(`{"nodes":3,"edges":2,"node_types":{"metric":3}}`))
		case "/v1/graph/schema":
			_, _ = w.Write([]byte(`{"node_types":{"metric":{"description":"A measure"}},"edge_types":{"derived_from":{"from":["metric"],"to":["metric"]}}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r, err := NewRemote(RemoteConfig{Endpoint: srv.URL, Token: "secret", AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if n, err := r.Node(ctx, "alice", "metrics/a"); err != nil || n.Label != "A" {
		t.Fatalf("node = %+v (%v)", n, err)
	}
	if sub, err := r.Neighbors(ctx, "alice", WalkQuery{From: "metrics/a", Depth: 1, EdgeTypes: []string{"derived_from"}}); err != nil || len(sub.Nodes) != 1 {
		t.Fatalf("neighbors = %+v (%v)", sub, err)
	}
	if p, err := r.Path(ctx, "alice", PathQuery{From: "metrics/a", To: "metrics/b"}); err != nil || !p.Found {
		t.Fatalf("path = %+v (%v)", p, err)
	}
	if hits, err := r.Search(ctx, "alice", SearchQuery{Text: "a", NodeTypes: []string{"metric"}}); err != nil || len(hits) != 1 {
		t.Fatalf("search = %+v (%v)", hits, err)
	}
	if cov, err := r.Answerability(ctx, "alice", "how much", 0.6); err != nil || cov.Verdict != VerdictInsufficient {
		t.Fatalf("answerability = %+v (%v)", cov, err)
	}
	if stats, err := r.Stats(ctx, "alice"); err != nil || stats.Nodes != 3 {
		t.Fatalf("stats = %+v (%v)", stats, err)
	}
	schema, warnings, err := r.Schema(ctx)
	if err != nil || warnings != nil || len(schema.EdgeTypes) != 1 || schema.NodeTypes["metric"].Description != "A measure" {
		t.Fatalf("schema = %+v (%v)", schema, err)
	}
	if len(paths) != 7 {
		t.Fatalf("calls = %v", paths)
	}
}

func TestRemoteRedactsFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/graph/node":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal detail with DSN postgres://u:p@db/x"))
		default:
			_, _ = w.Write([]byte("not json"))
		}
	}))
	defer srv.Close()
	r, err := NewRemote(RemoteConfig{Endpoint: srv.URL, Token: "secret", AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = r.Node(ctx, "alice", "metrics/a")
	if !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("error class = %v", err)
	}
	if msg := err.Error(); strings.Contains(msg, "DSN") || strings.Contains(msg, "postgres") || strings.Contains(msg, "127.0.0.1") {
		t.Fatalf("error leaked engine detail: %s", msg)
	}
	if _, err := r.Search(ctx, "alice", SearchQuery{Text: "x"}); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("bad JSON = %v", err)
	}

	// An unreachable endpoint is a class error too.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := dead.URL
	dead.Close()
	closed, err := NewRemote(RemoteConfig{Endpoint: endpoint, Token: "secret", AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Stats(ctx, "alice"); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("unreachable = %v", err)
	}
}
