---
type: Attested Computation
title: Revenue YTD (Looker)
description: Sanctioned Looker query reference executed by an outbound Looker sidecar.
runtime: looker
status: stable
verified:
  - by: human:jsmith@acme
parameters:
  - name: year
    type: integer
    required: true
computation: inline
executor:
  resource: runtime/looker.md
  receipt: [job_id, body_digest, result]
attester:
  resource: attesters/body_digest.go
stale_after: 2099-12-31T00:00:00Z
---
# Computation

The sanctioned body names an explore and a query id; the sidecar talks to Looker.
Parameters are values only and never become part of the reference.

```yaml
explore: orders
query_id: revenue_ytd
```
