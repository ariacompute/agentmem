package compute

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"agentmem/internal/okf"
)

// Contract is the immutable, sanctioned computation loaded from OKF+Git.
type Contract struct {
	ConceptID        string
	Runtime          string
	Parameters       []okf.Parameter
	SQL              string
	Source           string
	ContractHash     string
	ReceiptFields    []string
	AttesterResource string
	Verified         bool
	StaleAfter       *time.Time
}

// supportedStatement is a read-only SELECT / WITH statement only.
var forbiddenSQL = regexp.MustCompile(`\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|COPY|CALL|DO|VACUUM|SET|BEGIN|COMMIT|ROLLBACK|INTO|EXECUTE|MERGE|REPLACE)\b`)

// LoadContract resolves an Attested Computation document into an executable
// contract. The body is always read from the bundle; callers can never inject
// computation text through parameters.
func LoadContract(b *okf.Bundle, conceptID string) (Contract, error) {
	if b == nil {
		return Contract{}, fail(ErrContract, "nil bundle")
	}
	d, err := b.Get(conceptID)
	if err != nil {
		return Contract{}, fail(ErrContract, "concept %q is not an Attested Computation document", conceptID)
	}
	a := d.Attested
	if a == nil {
		return Contract{}, fail(ErrContract, "concept %q has no attested contract", conceptID)
	}
	if err := a.Validate(); err != nil {
		return Contract{}, fail(ErrContract, "invalid contract: %v", err)
	}
	sql, source, err := computationBody(b, a.Computation, d.Body)
	if err != nil {
		return Contract{}, err
	}
	sql, err = SafeStatement(sql)
	if err != nil {
		return Contract{}, err
	}
	params := make([]okf.Parameter, len(a.Parameters))
	copy(params, a.Parameters)
	for i := range params {
		params[i].Type = strings.ToLower(strings.TrimSpace(params[i].Type))
	}
	attester := ""
	if a.Attester != nil {
		attester = a.Attester.Resource
	}
	sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
	hash, err := contractHash(a.RuntimeID(), sql, params, attester)
	if err != nil {
		return Contract{}, err
	}
	return Contract{
		ConceptID: conceptID, Runtime: a.RuntimeID(), Parameters: params,
		SQL: sql, Source: source, ContractHash: hash,
		ReceiptFields: a.ReceiptFields(), AttesterResource: attester,
		Verified: len(d.Verified) > 0, StaleAfter: d.StaleAfter,
	}, nil
}

func computationBody(b *okf.Bundle, ref, body string) (string, string, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "" || strings.EqualFold(ref, "inline"):
		sql := okf.ExtractInlineSQL(body)
		if sql == "" {
			return "", "", fail(ErrContract, "no fenced sql block in document body")
		}
		return sql, "inline", nil
	case !strings.ContainsAny(ref, " \t\r\n") && strings.HasSuffix(strings.ToLower(ref), ".sql"):
		data, err := b.ReadResource(ref)
		if err != nil {
			return "", "", fail(ErrContract, "resource unavailable")
		}
		return string(data), "resource:" + ref, nil
	case strings.ContainsAny(ref, "\r\n"):
		return ref, "inline-frontmatter", nil
	default:
		return "", "", fail(ErrContract, "computation must be an inline sql block or a .sql resource path")
	}
}

// SafeStatement rejects multi-statement and mutating SQL. Parameters are bound
// separately; this check only guards the sanctioned text itself.
func SafeStatement(sql string) (string, error) {
	s := strings.TrimSpace(sql)
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if s == "" {
		return "", fail(ErrContract, "computation is empty")
	}
	if strings.Contains(s, ";") {
		return "", fail(ErrContract, "multiple statements are not allowed")
	}
	upper := strings.ToUpper(CanonicalSQL(s))
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return "", fail(ErrContract, "computation must be a read-only SELECT or WITH statement")
	}
	if forbiddenSQL.MatchString(upper) {
		return "", fail(ErrContract, "computation contains a mutating or session keyword")
	}
	return s, nil
}

func contractHash(runtime, sql string, params []okf.Parameter, attester string) (string, error) {
	type paramShape struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Required bool   `json:"required"`
	}
	shapes := make([]paramShape, 0, len(params))
	for _, p := range params {
		shapes = append(shapes, paramShape{Name: p.Name, Type: p.Type, Required: p.Required})
	}
	raw, err := json.Marshal(shapes)
	if err != nil {
		return "", fail(ErrContract, "parameter shape is not serialisable")
	}
	return okf.Hash([]byte(strings.Join([]string{
		runtime, CanonicalSQL(sql), string(raw), attester,
	}, "\x00"))), nil
}
