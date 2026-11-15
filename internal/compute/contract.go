package compute

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"agentmem/internal/okf"
)

// Body kinds classify what the sanctioned body actually is. Only SQL runs
// through a statement parser; the other kinds are opaque text handed to a
// sidecar as data.
const (
	KindSQL    = "sql"
	KindScript = "script"
	KindModel  = "model"
	KindQuery  = "query"
)

// bodyKindFor maps a runtime to the shape of its sanctioned body.
func bodyKindFor(runtime string) string {
	switch runtime {
	case "python":
		return KindScript
	case "dbt":
		return KindModel
	case "looker":
		return KindQuery
	default:
		return KindSQL
	}
}

// Contract is the immutable, sanctioned computation loaded from OKF+Git.
// SQL is the sanctioned body text: SQL for the postgres/bigquery runtimes, a
// script, model selector or query reference for the others (see Kind).
type Contract struct {
	ConceptID        string
	Runtime          string
	Kind             string
	Parameters       []okf.Parameter
	SQL              string
	Source           string
	ContractHash     string
	ReceiptFields    []string
	AttesterResource string
	Verified         bool
	StaleAfter       *time.Time
}

// supportedStatement is a read-only SELECT / WITH statement only. In addition
// to mutating keywords, PostgreSQL functions that read files, reach the
// network, mutate session state, sleep or export large objects are refused:
// a SELECT wrapper alone is not enough to make them safe.
var forbiddenSQL = regexp.MustCompile(`\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|COPY|CALL|DO|VACUUM|SET|BEGIN|COMMIT|ROLLBACK|INTO|EXECUTE|MERGE|REPLACE|PG_READ_FILE|PG_READ_BINARY_FILE|PG_LS_DIR|PG_STAT_FILE|PG_SLEEP|PG_NOTIFY|PG_LISTEN|PG_TERMINATE_BACKEND|PG_CANCEL_BACKEND|DBLINK|DBLINK_EXEC|LO_IMPORT|LO_EXPORT|LO_UNLINK|SET_CONFIG|QUERY_TO_XML|DATABASE_TO_XML)\b`)

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
	kind := bodyKindFor(a.RuntimeID())
	sql, source, err := computationBody(b, a.Computation, d.Body, kind)
	if err != nil {
		return Contract{}, err
	}
	sql, err = SafeBody(kind, sql)
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
	hash, err := contractHash(a.RuntimeID(), kind, sql, params, attester)
	if err != nil {
		return Contract{}, err
	}
	return Contract{
		ConceptID: conceptID, Runtime: a.RuntimeID(), Kind: kind, Parameters: params,
		SQL: sql, Source: source, ContractHash: hash,
		ReceiptFields: a.ReceiptFields(), AttesterResource: attester,
		Verified: len(d.Verified) > 0, StaleAfter: d.StaleAfter,
	}, nil
}

// computationBody resolves the sanctioned text. The accepted fence languages
// and resource suffixes follow the runtime's body kind, so a python runtime
// reads a ```python block and a .py resource instead of demanding SQL.
func computationBody(b *okf.Bundle, ref, body, kind string) (string, string, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "" || strings.EqualFold(ref, "inline"):
		text := okf.ExtractInlineBlock(body, fenceLangs(kind)...)
		if text == "" {
			return "", "", fail(ErrContract, "no fenced %s block in document body", kind)
		}
		return text, "inline", nil
	case isResourceRef(ref, kind):
		data, err := b.ReadResource(ref)
		if err != nil {
			return "", "", fail(ErrContract, "resource unavailable")
		}
		return string(data), "resource:" + ref, nil
	case strings.ContainsAny(ref, "\r\n"):
		return ref, "inline-frontmatter", nil
	default:
		return "", "", fail(ErrContract, "computation must be an inline %s block or a %s resource path",
			kind, strings.Join(resourceExts(kind), "/"))
	}
}

func fenceLangs(kind string) []string {
	switch kind {
	case KindScript:
		return []string{"python", "py"}
	case KindModel:
		return []string{"yaml", "yml"}
	case KindQuery:
		return []string{"yaml", "yml", "json"}
	default:
		return []string{"sql"}
	}
}

func resourceExts(kind string) []string {
	switch kind {
	case KindScript:
		return []string{".py"}
	case KindModel:
		return []string{".yaml", ".yml"}
	case KindQuery:
		return []string{".yaml", ".yml", ".json"}
	default:
		return []string{".sql"}
	}
}

func isResourceRef(ref, kind string) bool {
	if strings.ContainsAny(ref, " \t\r\n") {
		return false
	}
	lower := strings.ToLower(ref)
	for _, ext := range resourceExts(kind) {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
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

// SafeBody validates the sanctioned text for its kind. SQL keeps the strict
// read-only SELECT/WITH guard. Scripts, model selectors and query references
// are only ever handed to a sidecar as data (stdin or a JSON field, never a
// command line or a shell), so their guard is structural: bounded size, no NUL,
// and exactly one YAML document when the body is a selector or reference.
func SafeBody(kind, text string) (string, error) {
	if kind == KindSQL {
		return SafeStatement(text)
	}
	s := strings.TrimSpace(text)
	if s == "" {
		return "", fail(ErrContract, "computation is empty")
	}
	if len(s) > maxCanonicalSQLSize {
		return "", fail(ErrContract, "computation exceeds the 1 MiB size limit")
	}
	if strings.IndexByte(s, 0) >= 0 {
		return "", fail(ErrContract, "computation contains a NUL byte")
	}
	if kind == KindModel || kind == KindQuery {
		body := strings.TrimPrefix(s, "---")
		if strings.Contains(body, "\n---") || strings.Contains(body, "\n...") {
			return "", fail(ErrContract, "computation must be a single %s document", kind)
		}
	}
	return s, nil
}

func contractHash(runtime, kind, sql string, params []okf.Parameter, attester string) (string, error) {
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
		runtime, kind, CanonicalBody(kind, sql), string(raw), attester,
	}, "\x00"))), nil
}
