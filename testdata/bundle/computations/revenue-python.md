---
type: Attested Computation
title: Revenue YTD (Python)
description: Sanctioned revenue computation executed by an outbound python sidecar.
runtime: python
status: stable
verified:
  - by: human:jsmith@acme
parameters:
  - name: year
    type: integer
    required: true
computation: references/revenue.py
executor:
  resource: runtime/python.md
  receipt: [job_id, body_digest, result]
attester:
  resource: attesters/body_digest.go
stale_after: 2099-12-31T00:00:00Z
---
# Computation

The agent may only fill the declared `year` value; the sanctioned script lives in
`references/revenue.py` and is never rewritten. Nothing runs inside this process:
the body travels to the `python` sidecar configured in
`AGENTMEM_COMPUTE_SIDECARS`, which echoes the SHA-256 of the body it executed.
The deterministic Go attester refuses to display a value when that digest does
not match, and an unconfigured runtime stays fail-closed.
