package compute

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

func TestLoadContractRejectsInvalidAttestedContract(t *testing.T) {
	b := testBundle(t)
	writeResource(t, b, "computations/broken.md", `---
type: Attested Computation
runtime: postgres
parameters:
  - name: year
    type: float
computation: inline
---
`+"```sql\nSELECT 1\n```\n")
	if _, err := LoadContract(b, "computations/broken"); err == nil {
		t.Fatal("an invalid attested contract must be rejected")
	}
}

func TestLoadContractRejectsUnreadableResource(t *testing.T) {
	b := testBundle(t)
	writeResource(t, b, "computations/missing-ref.md", strings.Replace(
		postgresDoc, "references/revenue.sql", "references/absent.sql", 1))
	if _, err := LoadContract(b, "computations/missing-ref"); err == nil {
		t.Fatal("an unreadable computation resource must be rejected")
	}
}

func TestLoadContractModelAndQueryBodies(t *testing.T) {
	b := testBundle(t)
	writeResource(t, b, "models/revenue.yml", "name: revenue\n")
	writeResource(t, b, "models/selector.yaml", "select: revenue\n")
	writeResource(t, b, "queries/look.lkml", "view: revenue\n")
	for _, tc := range []struct{ id, doc string }{
		{"computations/dbt-model", strings.NewReplacer(
			"runtime: postgres", "runtime: dbt",
			"references/revenue.sql", "models/revenue.yml",
		).Replace(postgresDoc)},
		{"computations/dbt-selector", strings.NewReplacer(
			"runtime: postgres", "runtime: dbt",
			"references/revenue.sql", "models/selector.yaml",
		).Replace(postgresDoc)},
		{"computations/looker", strings.NewReplacer(
			"runtime: postgres", "runtime: looker",
			"references/revenue.sql", "models/selector.yaml",
		).Replace(postgresDoc)},
	} {
		writeResource(t, b, tc.id+".md", tc.doc)
		c, err := LoadContract(b, tc.id)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		if c.Kind != KindModel && c.Kind != KindQuery {
			t.Fatalf("%s kind = %q", tc.id, c.Kind)
		}
	}

	// A body reference whose suffix does not belong to the runtime's kind is
	// refused instead of being read as an opaque path.
	writeResource(t, b, "computations/dbt-unknown.md", strings.NewReplacer(
		"runtime: postgres", "runtime: dbt",
		"references/revenue.sql", "queries/look.lkml",
	).Replace(postgresDoc))
	if _, err := LoadContract(b, "computations/dbt-unknown"); err == nil {
		t.Fatal("a non-model suffix must be rejected for a dbt runtime")
	}
}

func TestLoadContractSortsParameters(t *testing.T) {
	b := testBundle(t)
	writeResource(t, b, "computations/two-params.md", `---
type: Attested Computation
runtime: postgres
parameters:
  - name: year
    type: integer
    required: true
  - name: amount
    type: number
    required: true
computation: inline
---
`+"```sql\nSELECT :amount, :year\n```\n")
	c, err := LoadContract(b, "computations/two-params")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Parameters) != 2 || c.Parameters[0].Name != "amount" {
		t.Fatalf("parameters = %+v", c.Parameters)
	}
}

func TestSafeBodyRejectsOversizedScript(t *testing.T) {
	big := strings.Repeat("x", maxCanonicalSQLSize+1)
	if _, err := SafeBody(KindScript, big); err == nil {
		t.Fatal("an oversized script body must be rejected")
	}
	if _, err := SafeBody(KindQuery, "a: 1\n---\nb: 2\n"); err == nil {
		t.Fatal("a multi-document query body must be rejected")
	}
	if _, err := SafeBody(KindScript, ""); err == nil {
		t.Fatal("an empty body must be rejected")
	}
	if _, err := SafeBody(KindScript, "a\x00b"); err == nil {
		t.Fatal("a NUL byte must be rejected")
	}
}

func TestBindStatementKeepsEscapedQuotes(t *testing.T) {
	out, err := bindStatement(`SELECT 'it''s :label', :year FROM t`, []okf.Parameter{
		{Name: "year", Type: "integer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "'it''s :label'") || !strings.Contains(out, "$1") {
		t.Fatalf("bound = %q", out)
	}
}

func TestDigestParamsHandlesUnserialisableValue(t *testing.T) {
	got := digestParams([]okf.Parameter{{Name: "year", Type: "integer"}}, []any{make(chan int)})
	if got == "" {
		t.Fatal("an unserialisable parameter must still produce a stable digest")
	}
	if got != digestParams([]okf.Parameter{{Name: "year", Type: "integer"}}, []any{make(chan int)}) {
		t.Fatal("the fallback digest must be deterministic")
	}
}

func TestDeriveResultRejectsUnserialisableRows(t *testing.T) {
	rows := meta.SQLRows{Values: [][]any{{make(chan int), make(chan int)}}}
	if DeriveResult(rows) != "" {
		t.Fatal("a non-serialisable table must not invent a display value")
	}
}

// nilListAudit returns an empty, nil slice from the audit query.
type nilListAudit struct{ *meta.FakeStore }

func (nilListAudit) ListComputations(context.Context, meta.ComputationQuery) ([]meta.ComputationRecord, error) {
	return nil, nil
}

func TestServiceListNormalisesNilRecords(t *testing.T) {
	svc := &Service{Audit: nilListAudit{FakeStore: meta.NewFakeStore()}, Timeout: time.Second}
	out, err := svc.List(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || len(out) != 0 {
		t.Fatalf("records = %+v", out)
	}
}

func TestServiceRejectsRuntimeWithoutImplementation(t *testing.T) {
	b := testBundle(t)
	writeResource(t, b, "computations/unknown-runtime.md", `---
type: Attested Computation
runtime: snowflake
parameters:
  - name: year
    type: integer
    required: true
computation: inline
---
`+"```sql\nSELECT :year\n```\n")
	svc := NewService(b, NewRuntimeRegistry(FakeExecutor{}, 10), meta.NewFakeStore(), time.Second, 10)
	_, err := svc.Run(context.Background(), Request{
		ConceptID: "computations/unknown-runtime", Parameters: map[string]any{"year": 2026},
	})
	if err == nil {
		t.Fatal("an unregistered runtime must fail closed")
	}
}

func TestSidecarHTTPRequiresEndpoint(t *testing.T) {
	if _, err := newSidecarRuntime(SidecarSpec{Runtime: "python", Bridge: "http"}, nil, 10, nil); err == nil {
		t.Fatal("an http sidecar without an endpoint must be rejected")
	}
}

func TestSidecarRequestMarshalFailure(t *testing.T) {
	rt, err := newSidecarRuntime(SidecarSpec{Runtime: "python", Bridge: "cli", Command: "agentmem-py"},
		nil, 10, func(context.Context, string, []string, []byte) ([]byte, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	bound := BoundComputation{
		Contract: Contract{
			ConceptID: "computations/py", ContractHash: "hash", Kind: KindScript,
			Parameters: []okf.Parameter{{Name: "year", Type: "integer"}},
		},
		Args: []any{make(chan int)}, Statement: "print(1)",
	}
	if _, err := rt.Execute(context.Background(), bound); err == nil {
		t.Fatal("an unserialisable parameter must fail the sidecar request")
	}
}

func TestSidecarToRowsDerivesColumnsAndValidatesWidth(t *testing.T) {
	rt := &sidecarRuntime{name: "python", maxRows: 10}
	rows, err := rt.toRows(sidecarResponse{Rows: [][]json.RawMessage{
		{json.RawMessage(`1`), json.RawMessage(`"two"`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Columns) != 2 || rows.Columns[0] != "col1" {
		t.Fatalf("derived columns = %+v", rows.Columns)
	}
	if _, err := rt.toRows(sidecarResponse{
		Columns: []string{"only"},
		Rows:    [][]json.RawMessage{{json.RawMessage(`1`), json.RawMessage(`2`)}},
	}); err == nil {
		t.Fatal("a row wider than its columns must be rejected")
	}
}

func TestScalarCellClassifiesValues(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want any
	}{
		{`null`, nil},
		{`true`, true},
		{`"text"`, "text"},
		{`42`, int64(42)},
		{`1.5`, 1.5},
	} {
		got, err := scalarCell(json.RawMessage(tc.raw))
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("%s = %#v", tc.raw, got)
		}
	}
	if _, err := scalarCell(json.RawMessage(`1e999`)); err == nil {
		t.Fatal("an out-of-range number must be rejected")
	}
	if _, err := scalarCell(json.RawMessage(`{"a":1}`)); err == nil {
		t.Fatal("a nested object must be rejected")
	}
	if _, err := scalarCell(json.RawMessage(`{`)); err == nil {
		t.Fatal("an unreadable cell must be rejected")
	}
}

func TestSidecarExecuteTrimsRowsToMax(t *testing.T) {
	rt, err := newSidecarRuntime(SidecarSpec{Runtime: "python", Bridge: "cli", Command: "agentmem-py"},
		nil, 1, func(context.Context, string, []string, []byte) ([]byte, error) {
			return json.Marshal(map[string]any{
				"columns":     []string{"n"},
				"rows":        [][]any{{1}, {2}},
				"body_digest": strings.Repeat("a", 64),
			})
		})
	if err != nil {
		t.Fatal(err)
	}
	b := BoundComputation{
		Contract:  Contract{ConceptID: "c", ContractHash: "h", Kind: KindScript},
		Statement: "print(1)",
	}
	receipt, err := rt.Execute(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RowCount != 1 {
		t.Fatalf("row count = %d", receipt.RowCount)
	}
}
