package compute

import (
	"context"

	"agentmem/internal/meta"
)

// FakeExecutor is the deterministic offline SQLExecutor. It derives a stable
// scalar from the statement and bound arguments without executing any SQL.
type FakeExecutor struct{}

func (FakeExecutor) Query(ctx context.Context, statement string, args []any, maxRows int) (meta.SQLRows, error) {
	if err := ctx.Err(); err != nil {
		return meta.SQLRows{}, err
	}
	return meta.DeterministicSQLRows(statement, args, maxRows), nil
}

// FailingExecutor lets tests exercise executor failure paths.
type FailingExecutor struct{}

func (FailingExecutor) Query(_ context.Context, _ string, _ []any, _ int) (meta.SQLRows, error) {
	return meta.SQLRows{}, fail(ErrExecution, "offline failure injection")
}
