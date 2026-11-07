---
type: Metric
title: Gross Margin
description: Gross margin definition.
tags: [finance, margin]
status: stable
verified:
  - by: human:alice
    at: 2026-06-25T09:00:00Z
generated:
  by: reference_agent/gemini
  at: 2026-06-20T22:53:05Z
stale_after: 2026-12-31T00:00:00Z
visibility: public
owner: finance
sources:
  - id: bq
    resource: bigquery://acme/warehouse/finance
    usage_count: 1823
---
# Gross Margin

$$ \text{Gross Margin} = \frac{\text{Revenue} - \text{COGS}}{\text{Revenue}} $$

See [churn](churn.md) and [revenue-ytd](/computations/revenue-ytd.md).
