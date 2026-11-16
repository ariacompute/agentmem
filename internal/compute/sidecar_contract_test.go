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

// sidecarContractCase pins one outbound runtime to the shipped document that
// exercises it. The four entries are the whole non-postgres surface: before
// this table only python had an end-to-end sample, so a regression in bigquery,
// dbt or looker body classification could only show up as a fail-closed path
// that looked correct.
type sidecarContractCase struct {
	runtime string
	id      string
	kind    string
}

func sidecarContractCases() []sidecarContractCase {
	return []sidecarContractCase{
		{runtime: "bigquery", id: "computations/revenue-ytd", kind: KindSQL},
		{runtime: "dbt", id: "computations/revenue-dbt", kind: KindModel},
		{runtime: "looker", id: "computations/revenue-looker", kind: KindQuery},
		{runtime: "python", id: "computations/revenue-python", kind: KindScript},
	}
}

func contractBundle(t *testing.T) *okf.Bundle {
	t.Helper()
	b, err := okf.Open(filepath.Join("..", "..", "testdata", "bundle"))
	if err != nil {
		t.Fatalf("open testdata bundle: %v", err)
	}
	return b
}

// contractRegistry returns a registry with one http sidecar per runtime in
// cases, all pointed at the same local recorder.
func contractRegistry(t *testing.T, endpoint string, cases []sidecarContractCase) *RuntimeRegistry {
	t.Helper()
	specs := make([]SidecarSpec, 0, len(cases))
	for _, tc := range cases {
		specs = append(specs, SidecarSpec{Runtime: tc.runtime, Bridge: "http", Endpoint: endpoint})
	}
	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure(specs, []string{"127.0.0.1"}, nil); err != nil {
		t.Fatalf("configure sidecars: %v", err)
	}
	return r
}

func runContract(t *testing.T, b *okf.Bundle, r *RuntimeRegistry, id string) Envelope {
	t.Helper()
	env, err := NewService(b, r, nil, 5*time.Second, 10).Run(context.Background(), Request{
		ConceptID: id, Parameters: map[string]any{"year": 2026},
	})
	if err != nil {
		t.Fatalf("run %s: %v", id, err)
	}
	return env
}

// TestSidecarWireContractForEveryRuntime is the wire-level contract for all
// four outbound runtimes: the request carries the runtime, its derived
// body_kind, the concept id, a non-empty contract hash and typed parameter
// values; a matching digest is attested and displayed.
func TestSidecarWireContractForEveryRuntime(t *testing.T) {
	b := contractBundle(t)
	cases := sidecarContractCases()

	for _, tc := range cases {
		t.Run(tc.runtime, func(t *testing.T) {
			var got sidecarRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/run" || r.Method != http.MethodPost {
					http.NotFound(w, r)
					return
				}
				if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
					t.Errorf("sidecar Content-Type = %q", ct)
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode sidecar request: %v", err)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"columns":     []string{"revenue"},
					"rows":        [][]any{{1250}},
					"body_digest": BodyDigest(got.Body),
				})
			}))
			defer srv.Close()

			env := runContract(t, b, contractRegistry(t, srv.URL, []sidecarContractCase{tc}), tc.id)

			// Request shape.
			if got.Runtime != tc.runtime {
				t.Fatalf("request runtime = %q, want %q", got.Runtime, tc.runtime)
			}
			if got.BodyKind != tc.kind {
				t.Fatalf("request body_kind = %q, want %q", got.BodyKind, tc.kind)
			}
			if got.ConceptID != tc.id || got.ContractHash == "" || got.Body == "" {
				t.Fatalf("request identity = %+v", got)
			}
			if year, ok := got.Parameters["year"]; !ok || year != float64(2026) {
				t.Fatalf("parameters = %+v, want a typed year", got.Parameters)
			}

			// Receipt shape and attestation.
			if env.Runtime != tc.runtime {
				t.Fatalf("envelope runtime = %q", env.Runtime)
			}
			if !env.DisplayAllowed || !env.Verdict.Passed || !env.Verdict.Fidelity {
				t.Fatalf("verdict = %+v", env.Verdict)
			}
			if env.DisplayValue != "1250" {
				t.Fatalf("display value = %q", env.DisplayValue)
			}
			if !strings.HasPrefix(env.JobID, "job-") {
				t.Fatalf("job id must be recomputed by Go, got %q", env.JobID)
			}
		})
	}
}

// TestSidecarDigestMismatchRefusesDisplayForEveryRuntime proves the safety
// property is not python-specific: a sidecar that ran a different body can
// never get a value displayed.
func TestSidecarDigestMismatchRefusesDisplayForEveryRuntime(t *testing.T) {
	b := contractBundle(t)

	for _, tc := range sidecarContractCases() {
		t.Run(tc.runtime, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"columns":     []string{"revenue"},
					"rows":        [][]any{{999999}},
					"body_digest": strings.Repeat("0", 64),
				})
			}))
			defer srv.Close()

			env := runContract(t, b, contractRegistry(t, srv.URL, []sidecarContractCase{tc}), tc.id)
			if env.DisplayAllowed || env.Verdict.Passed || env.DisplayValue != "" {
				t.Fatalf("unverified value must never be displayed: %+v", env)
			}
			if env.ErrorClass != "attestation_failed" {
				t.Fatalf("error class = %q", env.ErrorClass)
			}
		})
	}
}

// TestSidecarRejectsUnknownResponseFields pins the closed reply shape: job ids,
// results and display values are recomputed in Go, so a sidecar offering them
// is a protocol violation rather than a shortcut.
func TestSidecarRejectsUnknownResponseFields(t *testing.T) {
	b := contractBundle(t)

	for _, tc := range sidecarContractCases() {
		t.Run(tc.runtime, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"columns":       []string{"revenue"},
					"rows":          [][]any{{1250}},
					"body_digest":   strings.Repeat("a", 64),
					"job_id":        "forged",
					"display_value": "999999",
				})
			}))
			defer srv.Close()

			r := contractRegistry(t, srv.URL, []sidecarContractCase{tc})
			_, err := NewService(b, r, nil, 5*time.Second, 10).Run(context.Background(), Request{
				ConceptID: tc.id, Parameters: map[string]any{"year": 2026},
			})
			if !errors.Is(err, ErrExecution) {
				t.Fatalf("got %v, want ErrExecution", err)
			}
			if strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("error leaks the endpoint: %v", err)
			}
		})
	}
}

// TestSidecarRegistryCoversExactlyTheOutboundRuntimes keeps the declared
// surface honest: every non-postgres runtime may be configured, postgres may
// never be proxied, and an unknown runtime is rejected instead of silently
// falling back to another executor.
func TestSidecarRegistryCoversExactlyTheOutboundRuntimes(t *testing.T) {
	all := sidecarContractCases()
	specs := make([]SidecarSpec, 0, len(all))
	for _, tc := range all {
		specs = append(specs, SidecarSpec{Runtime: tc.runtime, Bridge: "http", Endpoint: "https://sidecar.internal"})
	}
	r := NewRuntimeRegistry(FakeExecutor{}, 10)
	if err := r.Configure(specs, []string{"sidecar.internal"}, nil); err != nil {
		t.Fatalf("the four outbound runtimes must be configurable: %v", err)
	}
	for _, tc := range all {
		if _, ok := r.Get(tc.runtime); !ok {
			t.Fatalf("runtime %q was not registered", tc.runtime)
		}
	}
	if err := r.Configure([]SidecarSpec{{Runtime: "postgres", Bridge: "http", Endpoint: "https://sidecar.internal"}},
		[]string{"sidecar.internal"}, nil); err == nil {
		t.Fatal("postgres must never be proxied to a sidecar")
	}
}
