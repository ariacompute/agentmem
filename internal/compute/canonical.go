package compute

import (
	"strings"
	"sync"
)

var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "AND": true, "OR": true,
	"AS": true, "JOIN": true, "LEFT": true, "RIGHT": true, "INNER": true,
	"OUTER": true, "ON": true, "GROUP": true, "BY": true, "ORDER": true,
	"HAVING": true, "LIMIT": true, "OFFSET": true, "WITH": true, "UNION": true,
	"ALL": true, "DISTINCT": true, "COUNT": true, "SUM": true, "AVG": true,
	"MIN": true, "MAX": true, "COALESCE": true, "CAST": true, "CASE": true,
	"WHEN": true, "THEN": true, "ELSE": true, "END": true, "IN": true,
	"NOT": true, "NULL": true, "IS": true, "LIKE": true, "BETWEEN": true,
	"ASC": true, "DESC": true, "DATE": true, "INTERVAL": true, "TRUE": true,
	"FALSE": true, "EXTRACT": true, "FILTER": true, "OVER": true,
	"PARTITION": true, "ROUND": true, "CURRENT_DATE": true, "NOW": true,
}

const maxCanonicalCache = 256

var canonicalCache = struct {
	sync.Mutex
	values map[string]string
}{values: make(map[string]string)}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdent(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '.'
}

// CanonicalSQL deterministically normalises SQL for provenance comparison:
// comments are removed outside quoted literals, whitespace and punctuation
// spacing are collapsed, SQL keywords are uppercased, and named :parameters
// become a uniform placeholder. Positional $N placeholders are preserved so a
// reordered binding can never pass as the same statement.
func CanonicalSQL(s string) string {
	canonicalCache.Lock()
	defer canonicalCache.Unlock()
	if cached, ok := canonicalCache.values[s]; ok {
		return cached
	}
	out := canonicalSQL(s)
	if len(canonicalCache.values) >= maxCanonicalCache {
		canonicalCache.values = make(map[string]string, maxCanonicalCache)
	}
	canonicalCache.values[s] = out
	return out
}

func canonicalSQL(s string) string {
	var toks []string
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			toks = append(toks, buf.String())
			buf.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			flush()
			start, quote := i, c
			i++
			for i < len(s) {
				if s[i] == quote {
					if i+1 < len(s) && s[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			toks = append(toks, s[start:i])
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			flush()
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			flush()
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			} else {
				i = len(s)
			}
		case c == ':' && i+1 < len(s) && isIdentStart(s[i+1]):
			flush()
			i++
			for i < len(s) && isIdent(s[i]) {
				i++
			}
			toks = append(toks, "?")
		case isIdent(c):
			buf.WriteByte(c)
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			flush()
			i++
		default:
			flush()
			toks = append(toks, string(c))
			i++
		}
	}
	flush()
	for i, t := range toks {
		if len(t) > 0 && isIdentStart(t[0]) {
			upper := strings.ToUpper(t)
			if sqlKeywords[upper] {
				toks[i] = upper
			}
		}
	}
	out := strings.Join(toks, " ")
	out = strings.ReplaceAll(out, "( ", "(")
	out = strings.ReplaceAll(out, " )", ")")
	out = strings.ReplaceAll(out, " ,", ",")
	out = strings.ReplaceAll(out, ", ", ",")
	out = strings.TrimSpace(out)
	out = strings.TrimSuffix(strings.TrimSpace(out), ";")
	return strings.TrimSpace(out)
}
