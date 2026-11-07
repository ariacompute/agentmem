---
type: Attested Computation
title: Revenue YTD
description: Sanctioned revenue year-to-date computation.
runtime: bigquery
status: stable
verified:
  - by: human:jsmith@acme
parameters:
  - name: year
    type: integer
    required: true
computation: refs/revenue.sql
executor:
  resource: skills/run-on-bq.md
  receipt: [job_id, executed_sql, result]
attester:
  resource: attesters/sql_equality.py
stale_after: 2026-12-31T00:00:00Z
---
# Computation

The agent may only fill parameter values; it must never rewrite the sanctioned
SQL. `attesters/sql_equality.py` is deterministic (no LLM) and verifies both
provenance (executed SQL equals the sanctioned SQL) and fidelity (displayed
value equals `receipt.result`). agentmem models this contract but never
executes it.
