---
type: Attested Computation
title: Revenue YTD (PostgreSQL)
description: Executable sanctioned revenue year-to-date computation.
runtime: postgres
status: stable
verified:
  - by: human:jsmith@acme
parameters:
  - name: year
    type: integer
    required: true
computation: references/revenue.sql
executor:
  resource: runtime/postgres.md
  receipt: [job_id, executed_sql, result]
attester:
  resource: attesters/sql_equality.go
stale_after: 2099-12-31T00:00:00Z
---
# Computation

The agent may only fill the declared `year` value; the sanctioned SQL lives in
`references/revenue.sql` and is never rewritten. The deterministic Go attester
checks provenance (executed SQL equals the sanctioned SQL) and fidelity (the
displayed value equals `receipt.result`). A failed attestation never displays a
number and always leaves an audit record.
