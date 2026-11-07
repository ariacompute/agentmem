package compute

import (
	"context"

	"agentmem/internal/meta"
)

// postgresRuntime is the only runtime that actually executes SQL. It uses the
// injected meta.SQLExecutor port, so the GORM implementation stays inside
// internal/meta and offline tests can use the deterministic fake.
type postgresRuntime struct {
	exec    meta.SQLExecutor
	maxRows int
}

func (p *postgresRuntime) Name() string { return "postgres" }

func (p *postgresRuntime) Execute(ctx context.Context, b BoundComputation) (Receipt, error) {
	if p.exec == nil {
		return Receipt{}, fail(ErrExecution, "no SQL executor is configured")
	}
	rows, err := p.exec.Query(ctx, b.Statement, b.Args, p.maxRows)
	if err != nil {
		// Driver messages may contain statement details; never surface them.
		return Receipt{}, fail(ErrExecution, "sanctioned query could not be executed")
	}
	result := DeriveResult(rows)
	return Receipt{
		JobID:        JobIDFor(b),
		Runtime:      "postgres",
		ContractHash: b.Contract.ContractHash,
		ParamsDigest: b.ParamsDigest,
		ExecutedSQL:  b.Statement,
		Result:       result,
		DisplayValue: result,
		Rows:         rows,
		RowCount:     len(rows.Values),
	}, nil
}
