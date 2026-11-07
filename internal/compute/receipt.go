package compute

import (
	"encoding/json"

	"agentmem/internal/meta"
)

// Receipt is the evidence returned by a runtime. ExecutedSQL keeps positional
// placeholders (never inlined values) so provenance can be compared without
// leaking parameter values into logs or the audit trail.
type Receipt struct {
	JobID        string
	Runtime      string
	ContractHash string
	ParamsDigest string
	ExecutedSQL  string
	Result       string
	DisplayValue string
	Rows         meta.SQLRows
	RowCount     int
}

// JobIDFor derives a stable run id from the contract and bound statement.
// It is recomputed by the attester, so a runtime cannot invent a job id.
func JobIDFor(b BoundComputation) string {
	return "job-" + meta.DigestBytes(
		b.Contract.ContractHash, b.ParamsDigest, CanonicalSQL(b.Statement),
	)[:24]
}

// DeriveResult renders rows as the single canonical value a caller may see.
// The attester recomputes it from the receipt's own rows, so a display value
// that does not match the executed result is rejected.
func DeriveResult(rows meta.SQLRows) string {
	if len(rows.Values) == 0 || len(rows.Values[0]) == 0 {
		return ""
	}
	if len(rows.Values) == 1 && len(rows.Values[0]) == 1 {
		return meta.ScalarString(rows.Values[0][0])
	}
	raw, err := json.Marshal(rows.Values)
	if err != nil {
		return ""
	}
	return string(raw)
}
