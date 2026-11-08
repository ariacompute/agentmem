package router

import (
	"fmt"
	"log/slog"
)

// redactError turns an internal failure into a stable, client-safe class string.
// Route reports and query results are returned to the caller, and adapter or
// driver errors can embed SQL, DSNs, absolute paths or document fragments, so
// only the class travels back; the error type is logged for operators.
func redactError(class string, err error) string {
	if err == nil {
		return ""
	}
	slog.Warn("routing error", "class", class, "error_type", fmt.Sprintf("%T", err))
	return class
}
