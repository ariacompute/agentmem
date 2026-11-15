---
type: Attested Computation
title: Revenue YTD
description: Sanctioned revenue year-to-date computation (BigQuery sidecar runtime).
runtime: bigquery
status: stable
verified:
  - by: human:jsmith@acme
parameters:
  - name: year
    type: integer
    required: true
computation: references/revenue.sql
executor:
  resource: skills/run-on-bq.md
  receipt: [job_id, executed_sql, result]
attester:
  resource: attesters/sql_equality.go
stale_after: 2099-12-31T00:00:00Z
---
# Computation

The agent may only fill parameter values; it must never rewrite the sanctioned
SQL. `attesters/sql_equality.go` is deterministic (no LLM) and verifies both
provenance (executed SQL equals the sanctioned SQL) and fidelity (displayed
value equals `receipt.result`). The `bigquery` runtime executes through an
outbound sidecar declared in `AGENTMEM_COMPUTE_SIDECARS`; with no sidecar
configured it stays fail-closed and returns `Unsupported` rather than
executing, silently degrading, or inventing a value.
