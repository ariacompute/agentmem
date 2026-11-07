package meta

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// SQLRows is the narrow, driver-independent result of a sanctioned read.
// Values are JSON-friendly scalars (string / int64 / float64 / bool / nil /
// time.Time); no driver types or query parameters are retained.
type SQLRows struct {
	Columns []string `json:"columns"`
	Values  [][]any  `json:"values"`
}

// SQLExecutor is the port behind Attested Computation. It lives in meta so
// the GORM implementation can stay in this package without an upward import
// into compute. Query must be read-only, parameterised and row-bounded.
type SQLExecutor interface {
	Query(ctx context.Context, statement string, args []any, maxRows int) (SQLRows, error)
}

// DeterministicSQLRows is the offline executor result. It derives a stable
// scalar from the statement and bound arguments so computation tests need no
// database, network or credentials.
func DeterministicSQLRows(statement string, args []any, maxRows int) SQLRows {
	if maxRows <= 0 || maxRows > 1 {
		maxRows = 1
	}
	h := sha256.New()
	_, _ = h.Write([]byte(statement))
	for _, a := range args {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(fmt.Sprintf("%T=%v", a, a)))
	}
	sum := h.Sum(nil)
	base := int64(binary.BigEndian.Uint64(sum[:8])%1000) + 1
	for _, a := range args {
		switch v := a.(type) {
		case int64:
			base += v * 1000
		case int:
			base += int64(v) * 1000
		}
	}
	return SQLRows{
		Columns: []string{"value"},
		Values:  [][]any{{base}},
	}
}

// ScalarString renders a driver value as a stable display string.
func ScalarString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(x)
	}
}

// DigestBytes is a short, deterministic hex digest used for job ids and
// parameter fingerprints. It never contains query values in clear text.
func DigestBytes(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
