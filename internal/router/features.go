package router

import (
	"strings"

	"agentmem/internal/okf"
)

// Features are the deterministic signals the rule table and System One see.
// They deliberately exclude document bodies.
type Features struct {
	ByteSize      int    `json:"byte_size"`
	Structured    bool   `json:"structured"`
	Authoritative bool   `json:"authoritative"`
	Sensitive     bool   `json:"sensitive"`
	Verified      bool   `json:"verified"`
	HasCanonical  bool   `json:"has_canonical"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	QueryKind     string `json:"query_kind"`
}

// authoritativeTypes are canonical-definition concepts: OKF is authoritative.
var authoritativeTypes = []string{
	"metric", "definition", "policy", "contract", "schema", "attested", "runbook", "glossary",
}

func isAuthoritativeType(t string) bool {
	lower := strings.ToLower(t)
	for _, a := range authoritativeTypes {
		if strings.Contains(lower, a) {
			return true
		}
	}
	return false
}

// ExtractWriteFeatures derives routing signals from a document.
func ExtractWriteFeatures(d *okf.Document, canonicalExists bool) Features {
	f := Features{HasCanonical: canonicalExists, QueryKind: "write"}
	if d == nil {
		return f
	}
	f.Type = d.Type
	f.Status = d.EffectiveStatus()
	f.ByteSize = len(d.Body) + len(d.Title) + len(d.Description)
	f.Structured = d.Attested != nil || strings.Contains(strings.ToLower(d.Body), "| --- |")
	f.Verified = len(d.Verified) > 0
	f.Authoritative = isAuthoritativeType(d.Type) || d.Attested != nil ||
		(d.Verified != nil && d.TrustTier() == okf.TrustHumanReviewed)
	f.Sensitive = d.Visibility() == "private" || d.Status == "deprecated"
	return f
}

// ExtractQueryFeatures classifies a query by shape, not content.
func ExtractQueryFeatures(text string, canonicalHits int) Features {
	lower := strings.ToLower(text)
	kind := "general"
	switch {
	case containsAny(lower, "define", "definition", "exact", "official", "how is", "calculated", "policy"):
		kind = "definition"
	case containsAny(lower, "similar", "related", "any examples", "discuss", "why", "summar", "long tail", "background"):
		kind = "exploratory"
	case containsAny(lower, "compare", "across", "all documents", "overview"):
		kind = "aggregate"
	}
	return Features{
		ByteSize: len(text), QueryKind: kind, HasCanonical: canonicalHits > 0,
		Authoritative: kind == "definition",
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// Digest builds the short summary sent to System One. It never includes the
// document body, credentials or PII.
func Digest(d *okf.Document) string {
	if d == nil {
		return ""
	}
	s := strings.Join(strings.Fields(d.Type+" "+d.Title+" "+d.Description), " ")
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
