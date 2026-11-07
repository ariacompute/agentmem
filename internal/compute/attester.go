package compute

import (
	"strings"
	"time"
)

// Verdict is the deterministic, LLM-free attestation outcome.
type Verdict struct {
	Passed       bool     `json:"passed"`
	Provenance   bool     `json:"provenance"`
	Fidelity     bool     `json:"fidelity"`
	Stale        bool     `json:"stale"`
	Reasons      []string `json:"reasons"`
	ContractHash string   `json:"contract_hash"`
	JobID        string   `json:"job_id"`
}

// Attester verifies provenance (the executed SQL is the sanctioned SQL bound
// to the same parameters and contract) and fidelity (the displayed value is
// exactly the value derived from the executor's rows). It is pure Go: no LLM,
// no network, no model calls, so the same receipt always yields the same verdict.
type Attester struct {
	Now func() time.Time
}

func (a Attester) clock() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Attest returns ErrAttestationFailed whenever any check fails. Callers must
// not display a value from a failed verdict, and must still write the audit.
func (a Attester) Attest(b BoundComputation, r Receipt) (Verdict, error) {
	v := Verdict{
		ContractHash: b.Contract.ContractHash,
		JobID:        r.JobID,
		Reasons:      []string{},
	}
	provenance := true
	check := func(ok bool, reason string) {
		if !ok {
			provenance = false
			v.Reasons = append(v.Reasons, reason)
		}
	}
	check(r.ContractHash == b.Contract.ContractHash, "contract hash mismatch")
	check(r.ParamsDigest == b.ParamsDigest, "parameter digest mismatch")
	check(r.Runtime == b.Contract.Runtime, "runtime mismatch")
	check(r.JobID == JobIDFor(b), "job id mismatch")
	check(CanonicalSQL(r.ExecutedSQL) == CanonicalSQL(b.Statement), "executed SQL differs from sanctioned SQL")

	fidelity := true
	derived := DeriveResult(r.Rows)
	if derived == "" {
		fidelity = false
		v.Reasons = append(v.Reasons, "executor returned no result")
	}
	if r.Result != derived {
		fidelity = false
		v.Reasons = append(v.Reasons, "receipt result differs from executed rows")
	}
	if r.DisplayValue != derived {
		fidelity = false
		v.Reasons = append(v.Reasons, "display value differs from receipt result")
	}
	if r.RowCount != len(r.Rows.Values) {
		fidelity = false
		v.Reasons = append(v.Reasons, "row count mismatch")
	}

	stale := false
	if b.Contract.StaleAfter != nil && !a.clock().Before(*b.Contract.StaleAfter) {
		stale = true
		v.Reasons = append(v.Reasons, "contract stale_after elapsed; human review required")
	}

	v.Provenance = provenance
	v.Fidelity = fidelity
	v.Stale = stale
	v.Passed = provenance && fidelity && !stale
	if !v.Passed {
		return v, fail(ErrAttestationFailed, "%s", strings.Join(v.Reasons, "; "))
	}
	return v, nil
}
