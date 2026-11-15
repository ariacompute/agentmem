---
type: Attested Computation
title: Revenue YTD (dbt)
description: Sanctioned dbt model selector executed by an outbound dbt sidecar.
runtime: dbt
status: stable
verified:
  - by: human:jsmith@acme
parameters:
  - name: year
    type: integer
    required: true
computation: inline
executor:
  resource: runtime/dbt.md
  receipt: [job_id, body_digest, result]
attester:
  resource: attesters/body_digest.go
stale_after: 2099-12-31T00:00:00Z
---
# Computation

The sanctioned body is a model selector, not SQL: the sidecar owns compilation
and warehouse access. The agent may only fill declared parameter values.

```yaml
model: revenue_ytd
selector: year
```
