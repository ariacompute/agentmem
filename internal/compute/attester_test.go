package compute

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAttesterPassesValidReceipt(t *testing.T) {
	b, r := validPair(t)
	v, err := (Attester{}).Attest(b, r)
	if err != nil || !v.Passed || !v.Provenance || !v.Fidelity || v.Stale {
		t.Fatalf("verdict = %+v, err = %v", v, err)
	}
}

func TestAttesterRejectsForgedEvidence(t *testing.T) {
	cases := map[string]func(*Receipt){
		"rewritten SQL": func(r *Receipt) { r.ExecutedSQL = "SELECT 1 FROM orders" },
		"tampered display value": func(r *Receipt) {
			r.DisplayValue = "999999999"
		},
		"tampered result": func(r *Receipt) { r.Result = "0" },
		"invented job id": func(r *Receipt) { r.JobID = "job-forged" },
		"wrong contract":  func(r *Receipt) { r.ContractHash = "deadbeef" },
		"wrong params":    func(r *Receipt) { r.ParamsDigest = "deadbeef" },
		"wrong runtime":   func(r *Receipt) { r.Runtime = "bigquery" },
		"wrong row count": func(r *Receipt) {
			r.RowCount = 7
		},
		"empty rows": func(r *Receipt) {
			r.Rows.Values = nil
			r.RowCount = 0
			r.Result = ""
			r.DisplayValue = ""
		},
	}
	b, base := validPair(t)
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := base
			mutate(&r)
			v, err := (Attester{}).Attest(b, r)
			if !errors.Is(err, ErrAttestationFailed) || v.Passed {
				t.Fatalf("forgery accepted: verdict=%+v err=%v", v, err)
			}
			if len(v.Reasons) == 0 {
				t.Fatal("rejection has no reason")
			}
		})
	}
}

func TestAttesterFailsClosedOnStaleContract(t *testing.T) {
	b, r := validPair(t)
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	b.Contract.StaleAfter = &past
	v, err := (Attester{}).Attest(b, r)
	if !errors.Is(err, ErrAttestationFailed) || !v.Stale || v.Passed {
		t.Fatalf("stale verdict = %+v, err = %v", v, err)
	}
	if !strings.Contains(strings.Join(v.Reasons, " "), "stale_after") {
		t.Fatalf("reasons = %v", v.Reasons)
	}
}

func TestDocumentVerifiedIsOrthogonalToAttestation(t *testing.T) {
	b, r := validPair(t)
	if !b.Contract.Verified {
		t.Fatal("fixture should be human-verified")
	}
	b.Contract.Verified = false
	if _, err := (Attester{}).Attest(b, r); err != nil {
		t.Fatalf("document-level verification must not gate run attestation: %v", err)
	}
}
