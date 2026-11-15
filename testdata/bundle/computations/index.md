# computations

Attested Computation contracts. Bodies are canonical in OKF+Git; execution and
verification live in `internal/compute`.

* `revenue-postgres.md` — executable `postgres` runtime example (in-process)
* `revenue-python.md` — `python` sidecar example (`references/revenue.py`)
* `revenue-dbt.md` — `dbt` sidecar example (model selector body)
* `revenue-looker.md` — `looker` sidecar example (query reference body)
* `revenue-ytd.md` — `bigquery` contract left unconfigured on purpose (`Unsupported`)
