package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"agentmem/internal/bridges"
	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// SidecarSpec describes one outbound executor for a runtime that cannot run
// in-process. Bridge is "http" (Endpoint is a URL, egress-checked) or "cli"
// (Command is a bare executable name, never a shell). Zero values stay
// fail-closed: the runtime keeps its Unsupported skeleton.
type SidecarSpec struct {
	Runtime   string
	Bridge    string
	Endpoint  string
	Command   string
	TimeoutMS int
}

// sidecarRequest is the v1 wire payload. It carries the sanctioned body as
// data; the sidecar never receives an instruction it can reinterpret as a
// command, and it never learns anything it could use to forge a receipt.
type sidecarRequest struct {
	Runtime      string         `json:"runtime"`
	ConceptID    string         `json:"concept_id"`
	ContractHash string         `json:"contract_hash"`
	BodyKind     string         `json:"body_kind"`
	Body         string         `json:"body"`
	Parameters   map[string]any `json:"parameters"`
}

// sidecarResponse is the only accepted reply shape: a table plus the digest of
// the body it actually executed. Job ids, results and display values are
// always recomputed in Go.
type sidecarResponse struct {
	Columns    []string            `json:"columns"`
	Rows       [][]json.RawMessage `json:"rows"`
	BodyDigest string              `json:"body_digest"`
}

type sidecarRuntime struct {
	name     string
	bridge   string
	endpoint string
	command  string
	timeout  time.Duration
	maxRows  int
	hosts    []string
	runner   bridges.Runner
}

func newSidecarRuntime(spec SidecarSpec, allowHosts []string, maxRows int, runner bridges.Runner) (*sidecarRuntime, error) {
	name := strings.ToLower(strings.TrimSpace(spec.Runtime))
	if name == "" || name == "postgres" {
		return nil, fmt.Errorf("compute: runtime %q cannot use a sidecar", spec.Runtime)
	}
	bridge := strings.ToLower(strings.TrimSpace(spec.Bridge))
	if bridge != "http" && bridge != "cli" {
		return nil, fmt.Errorf("compute: runtime %q has bridge %q; use http or cli", name, spec.Bridge)
	}
	timeout := time.Duration(spec.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	s := &sidecarRuntime{
		name: name, bridge: bridge, timeout: timeout, maxRows: maxRows,
		hosts: allowHosts, runner: runner,
	}
	switch bridge {
	case "http":
		s.endpoint = strings.TrimSuffix(strings.TrimSpace(spec.Endpoint), "/")
		if s.endpoint == "" {
			return nil, fmt.Errorf("compute: runtime %q bridge http requires an endpoint", name)
		}
	case "cli":
		s.command = strings.TrimSpace(spec.Command)
		if err := bridges.ValidateCommand(s.command); err != nil {
			return nil, fmt.Errorf("compute: runtime %q: %w", name, err)
		}
		if s.runner == nil {
			s.runner = bridges.NewRunner(timeout)
		}
	}
	return s, nil
}

func (s *sidecarRuntime) Name() string { return s.name }

// Execute calls the sidecar once and converts its table into a receipt. A
// digest of a different body is surfaced to the attester, which refuses to
// display any value; transport and payload faults map to ErrExecution.
func (s *sidecarRuntime) Execute(ctx context.Context, b BoundComputation) (Receipt, error) {
	req := sidecarRequest{
		Runtime: s.name, ConceptID: b.Contract.ConceptID,
		ContractHash: b.Contract.ContractHash, BodyKind: b.Contract.Kind,
		Body: b.Statement, Parameters: namedValues(b),
	}
	raw, err := s.call(ctx, req)
	if err != nil {
		return Receipt{}, err
	}
	var resp sidecarResponse
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&resp); err != nil {
		return Receipt{}, fail(ErrExecution, "sidecar response could not be decoded")
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Receipt{}, fail(ErrExecution, "sidecar response carries trailing data")
	}
	rows, err := s.toRows(resp)
	if err != nil {
		return Receipt{}, err
	}
	result := DeriveResult(rows)
	return Receipt{
		JobID: JobIDFor(b), Runtime: s.name, ContractHash: b.Contract.ContractHash,
		ParamsDigest: b.ParamsDigest, ExecutedSQL: b.Statement,
		BodyDigest: strings.ToLower(strings.TrimSpace(resp.BodyDigest)),
		Result:     result, DisplayValue: result, Rows: rows, RowCount: len(rows.Values),
	}, nil
}

func (s *sidecarRuntime) call(ctx context.Context, req sidecarRequest) ([]byte, error) {
	switch s.bridge {
	case "http":
		raw, err := bridges.PostJSON(ctx, s.endpoint, "/run", s.hosts, s.timeout, req)
		if err != nil {
			// Never surface the wrapped error: it may name the host.
			return nil, fail(ErrExecution, "sidecar call failed")
		}
		return raw, nil
	default:
		payload, err := json.Marshal(req)
		if err != nil {
			return nil, fail(ErrExecution, "sidecar request is not serialisable")
		}
		out, err := s.runner(ctx, s.command, []string{"run"}, payload)
		if err != nil {
			// The runner strips stderr and host paths on purpose.
			return nil, fail(ErrExecution, "sidecar process failed")
		}
		return out, nil
	}
}

// toRows normalises the sidecar table into meta.SQLRows and enforces the
// scalar contract DeriveResult relies on. Nested objects are refused rather
// than stringified: a receipt must not invent a value shape.
func (s *sidecarRuntime) toRows(resp sidecarResponse) (meta.SQLRows, error) {
	if len(resp.Rows) > s.maxRows && s.maxRows > 0 {
		resp.Rows = resp.Rows[:s.maxRows]
	}
	rows := meta.SQLRows{Columns: append([]string(nil), resp.Columns...)}
	for _, raw := range resp.Rows {
		row := make([]any, 0, len(raw))
		for _, cell := range raw {
			v, err := scalarCell(cell)
			if err != nil {
				return meta.SQLRows{}, err
			}
			row = append(row, v)
		}
		if len(rows.Columns) == 0 && len(row) > 0 {
			for i := range row {
				rows.Columns = append(rows.Columns, fmt.Sprintf("col%d", i+1))
			}
		}
		rows.Values = append(rows.Values, row)
	}
	if len(rows.Columns) > 0 && len(rows.Values) > 0 && len(rows.Values[0]) != len(rows.Columns) {
		return meta.SQLRows{}, fail(ErrExecution, "sidecar returned a row that does not match the column count")
	}
	return rows, nil
}

func scalarCell(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fail(ErrExecution, "sidecar returned an unreadable cell")
	}
	switch x := v.(type) {
	case nil, bool, string:
		return x, nil
	case json.Number:
		// Keep integers as int64 so ScalarString never renders 2 as 2.0.
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		f, err := x.Float64()
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fail(ErrExecution, "sidecar returned a number out of range")
		}
		return f, nil
	default:
		return nil, fail(ErrExecution, "sidecar returned a non-scalar cell")
	}
}

// namedValues re-pairs declared parameter names with their bound values; the
// positional Args slice is for SQL drivers only.
func namedValues(b BoundComputation) map[string]any {
	out := make(map[string]any, len(b.Contract.Parameters))
	for i, p := range b.Contract.Parameters {
		if i < len(b.Args) {
			out[p.Name] = b.Args[i]
		}
	}
	return out
}

// BodyDigest is the digest a sidecar must echo to prove which body it ran.
func BodyDigest(body string) string { return okf.Hash([]byte(body)) }
