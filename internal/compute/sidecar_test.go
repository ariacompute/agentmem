package compute

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmem/internal/okf"
)

const pythonDoc = `---
type: Attested Computation
title: Python Revenue
runtime: python
parameters:
  - name: year
    type: integer
    required: true
computation: inline
attester:
  resource: attesters/revenue.py
---
# Computation

` + "```python\nimport json\nprint(json.dumps({\"revenue\": 1250}))\n```\n"

func testBundleWith(t *testing.T, rel, doc string) *okf.Bundle {
	t.Helper()
	b := testBundle(t)
	writeResource(t, b, rel, doc)
	return b
}

// sidecarServer replies with a scripted table and echoes the digest of the body
// it received, which is the only provenance a sidecar is trusted to supply.
func sidecarServer(t *testing.T, rows [][]any, digest func(body string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/run" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req sidecarRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"columns":     []string{"revenue"},
			"rows":        rows,
			"body_digest": digest(req.Body),
		})
	}))
}

func TestSidecarHTTPRuntimeExecutesAndAttests(t *testing.T) {
	b := testBundleWith(t, "computations/py.md", pythonDoc)
	srv := sidecarServer(t, [][]any{{1250}}, BodyDigest)
	defer srv.Close()

	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "http", Endpoint: srv.URL}},
		[]string{"127.0.0.1"}, nil); err != nil {
		t.Fatal(err)
	}
	svc := NewService(b, r, nil, 2*time.Second, 10)
	env, err := svc.Run(context.Background(), Request{
		ConceptID: "computations/py", Parameters: map[string]any{"year": 2026},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !env.DisplayAllowed || !env.Verdict.Passed || env.DisplayValue != "1250" {
		t.Fatalf("sidecar run not attested: %+v", env)
	}
	if env.Runtime != "python" || env.JobID == "" || !strings.HasPrefix(env.JobID, "job-") {
		t.Fatalf("runtime/job = %q / %q", env.Runtime, env.JobID)
	}
}

func TestSidecarBodyDigestMismatchRefusesDisplay(t *testing.T) {
	b := testBundleWith(t, "computations/py.md", pythonDoc)
	srv := sidecarServer(t, [][]any{{1250}}, func(string) string { return strings.Repeat("0", 64) })
	defer srv.Close()

	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "http", Endpoint: srv.URL}},
		[]string{"127.0.0.1"}, nil); err != nil {
		t.Fatal(err)
	}
	env, err := NewService(b, r, nil, 2*time.Second, 10).Run(context.Background(), Request{
		ConceptID: "computations/py", Parameters: map[string]any{"year": 2026},
	})
	if err != nil {
		t.Fatalf("attestation failure is a verdict, not a transport error: %v", err)
	}
	if env.DisplayAllowed || env.DisplayValue != "" || env.Verdict.Passed {
		t.Fatalf("unverified value must never be displayed: %+v", env)
	}
	if env.ErrorClass != "attestation_failed" {
		t.Fatalf("error class = %q", env.ErrorClass)
	}
}

func TestSidecarFaultsMapToExecution(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"5xx": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"invalid json": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		},
		"trailing data": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"rows":[]} {}`))
		},
		"nested cell": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"columns":["a"],"rows":[[{"x":1}]],"body_digest":""}`))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			b := testBundleWith(t, "computations/py.md", pythonDoc)
			srv := httptest.NewServer(handler)
			defer srv.Close()
			r := NewRuntimeRegistry(FakeExecutor{}, 10)
			if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "http", Endpoint: srv.URL}},
				[]string{"127.0.0.1"}, nil); err != nil {
				t.Fatal(err)
			}
			_, err := NewService(b, r, nil, 2*time.Second, 10).Run(context.Background(), Request{
				ConceptID: "computations/py", Parameters: map[string]any{"year": 2026},
			})
			if !errors.Is(err, ErrExecution) {
				t.Fatalf("got %v, want ErrExecution", err)
			}
			if strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), "127.0.0.1") {
				t.Fatalf("error leaks the endpoint: %v", err)
			}
		})
	}
}

func TestSidecarEndpointMustBeAllowed(t *testing.T) {
	b := testBundleWith(t, "computations/py.md", pythonDoc)
	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "http", Endpoint: "https://bq.internal"}},
		nil, nil); err != nil {
		t.Fatal(err)
	}
	rt, ok := r.Get("python")
	if !ok {
		t.Fatal("python runtime missing")
	}
	c, err := LoadContract(b, "computations/py")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := Bind(c, map[string]any{"year": 2026})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Execute(context.Background(), bound); !errors.Is(err, ErrExecution) {
		t.Fatalf("outbound host outside the allowlist must fail closed, got %v", err)
	}
}

func TestSidecarTruncatesRowsToMaxRows(t *testing.T) {
	b := testBundleWith(t, "computations/py.md", pythonDoc)
	srv := sidecarServer(t, [][]any{{1}, {2}, {3}}, BodyDigest)
	defer srv.Close()
	r := NewRuntimeRegistry(FakeExecutor{}, 2)
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "http", Endpoint: srv.URL}},
		[]string{"127.0.0.1"}, nil); err != nil {
		t.Fatal(err)
	}
	rt, _ := r.Get("python")
	c, err := LoadContract(b, "computations/py")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := Bind(c, map[string]any{"year": 2026})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := rt.Execute(context.Background(), bound)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RowCount != 2 || len(receipt.Rows.Values) != 2 {
		t.Fatalf("max_rows not enforced: %+v", receipt)
	}
}

// TestTestdataBundleContractsLoad pins the shipped bundle surface: every
// runtime name has a document, and each body is classified before execution.
func TestTestdataBundleContractsLoad(t *testing.T) {
	b, err := okf.Open(filepath.Join("..", "..", "testdata", "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"computations/revenue-postgres": KindSQL,
		"computations/revenue-ytd":      KindSQL,
		"computations/revenue-python":   KindScript,
		"computations/revenue-dbt":      KindModel,
		"computations/revenue-looker":   KindQuery,
	}
	for id, kind := range want {
		c, err := LoadContract(b, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if c.Kind != kind {
			t.Fatalf("%s kind = %q, want %q", id, c.Kind, kind)
		}
		if c.ContractHash == "" || c.SQL == "" {
			t.Fatalf("%s has no body/hash", id)
		}
	}
}

func TestConfigureRejectsInvalidSpecs(t *testing.T) {
	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure([]SidecarSpec{{Runtime: "postgres", Bridge: "http", Endpoint: "https://x"}}, nil, nil); err == nil {
		t.Fatal("postgres must never be proxied to a sidecar")
	}
	if err := r.Configure([]SidecarSpec{{Runtime: "spark", Bridge: "http", Endpoint: "https://x"}}, nil, nil); err == nil {
		t.Fatal("unknown runtime must be rejected")
	}
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "grpc"}}, nil, nil); err == nil {
		t.Fatal("unknown bridge must be rejected")
	}
	if err := r.Configure([]SidecarSpec{{Runtime: "python", Bridge: "cli", Command: "rm -rf /"}}, nil, nil); err == nil {
		t.Fatal("shell-ish command must be rejected")
	}
	if err := r.Configure([]SidecarSpec{
		{Runtime: "python", Bridge: "cli", Command: "py"},
		{Runtime: "python", Bridge: "cli", Command: "py"},
	}, nil, nil); err == nil {
		t.Fatal("duplicate runtime must be rejected")
	}
}
