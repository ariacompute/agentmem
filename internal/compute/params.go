package compute

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"agentmem/internal/meta"
	"agentmem/internal/okf"
)

// BoundComputation is a sanctioned contract with only values filled in. The
// statement is the original SQL with named holes replaced by positional
// placeholders; Args is aligned to the declared parameter order.
type BoundComputation struct {
	Contract     Contract
	Statement    string
	Args         []any
	ParamsDigest string
}

// reservedValueKeys may never be supplied by an agent: computation text,
// runtime selection and attester selection are all Git-owned.
var reservedValueKeys = map[string]bool{
	"computation": true, "sql": true, "statement": true, "runtime": true,
	"attester": true, "executor": true, "contract": true, "body": true,
}

// Bind validates parameter values and binds them to the sanctioned SQL.
// It rejects undeclared names, missing required values, wrong types and any
// attempt to pass computation text. No string concatenation of values occurs.
func Bind(c Contract, values map[string]any) (BoundComputation, error) {
	declared := make(map[string]okf.Parameter, len(c.Parameters))
	for _, p := range c.Parameters {
		declared[p.Name] = p
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if reservedValueKeys[strings.ToLower(k)] {
			return BoundComputation{}, fail(ErrComputationRewrite,
				"field %q is owned by the sanctioned contract; only declared parameter values may be supplied", k)
		}
		if _, ok := declared[k]; !ok {
			return BoundComputation{}, fail(ErrInvalidParams, "undeclared parameter %q", k)
		}
	}
	args := make([]any, 0, len(c.Parameters))
	for _, p := range c.Parameters {
		raw, present := values[p.Name]
		if !present || raw == nil {
			if p.Required {
				return BoundComputation{}, fail(ErrInvalidParams, "required parameter %q is missing", p.Name)
			}
			args = append(args, nil)
			continue
		}
		v, err := coerce(p, raw)
		if err != nil {
			return BoundComputation{}, err
		}
		args = append(args, v)
	}
	statement, err := bindStatement(c.SQL, c.Parameters)
	if err != nil {
		return BoundComputation{}, err
	}
	return BoundComputation{
		Contract: c, Statement: statement, Args: args,
		ParamsDigest: digestParams(c.Parameters, args),
	}, nil
}

func coerce(p okf.Parameter, raw any) (any, error) {
	switch strings.ToLower(strings.TrimSpace(p.Type)) {
	case "integer":
		switch v := raw.(type) {
		case int:
			return int64(v), nil
		case int32:
			return int64(v), nil
		case int64:
			return v, nil
		case float64:
			if math.Trunc(v) != v || v > math.MaxInt64 || v < math.MinInt64 {
				break
			}
			return int64(v), nil
		case json.Number:
			n, err := v.Int64()
			if err == nil {
				return n, nil
			}
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil {
				return n, nil
			}
		}
	case "number":
		switch v := raw.(type) {
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case float64:
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				return v, nil
			}
		case json.Number:
			f, err := v.Float64()
			if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				return f, nil
			}
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				return f, nil
			}
		}
	case "boolean":
		switch v := raw.(type) {
		case bool:
			return v, nil
		case string:
			if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
				return b, nil
			}
		}
	case "string":
		if v, ok := raw.(string); ok {
			return v, nil
		}
	case "date":
		switch v := raw.(type) {
		case time.Time:
			return v.UTC().Format("2006-01-02"), nil
		case string:
			t := strings.TrimSpace(v)
			if _, err := time.Parse("2006-01-02", t); err == nil {
				return t, nil
			}
			if parsed, err := time.Parse(time.RFC3339, t); err == nil {
				return parsed.UTC().Format("2006-01-02"), nil
			}
		}
	}
	return nil, fail(ErrInvalidParams, "parameter %q is not a valid %s", p.Name, p.Type)
}

// bindStatement replaces :name holes outside quoted literals and comments.
// Positions follow declaration order, so Args[i] maps to $i+1.
func bindStatement(sql string, params []okf.Parameter) (string, error) {
	positions := make(map[string]int, len(params))
	for i, p := range params {
		positions[p.Name] = i + 1
	}
	var out strings.Builder
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			start, quote := i, c
			i++
			for i < len(sql) {
				if sql[i] == quote {
					if i+1 < len(sql) && sql[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			out.WriteString(sql[start:i])
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				out.WriteByte(sql[i])
				i++
			}
		case c == ':' && i+1 < len(sql) && isIdentStart(sql[i+1]):
			start := i + 1
			i++
			for i < len(sql) && isIdent(sql[i]) {
				i++
			}
			name := sql[start:i]
			n, ok := positions[name]
			if !ok {
				return "", fail(ErrInvalidParams, "SQL references undeclared placeholder %q", name)
			}
			out.WriteString("$" + strconv.Itoa(n))
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), nil
}

func digestParams(params []okf.Parameter, args []any) string {
	type bound struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	rows := make([]bound, 0, len(params))
	for i, p := range params {
		var v any
		if i < len(args) {
			v = args[i]
		}
		rows = append(rows, bound{Name: p.Name, Type: p.Type, Value: v})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return meta.DigestBytes("unserialisable", strconv.Itoa(len(params)))
	}
	return meta.DigestBytes(string(raw))
}
