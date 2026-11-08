// Language-neutral documentation skeleton.
//
// Everything here is machine data: HTTP methods, paths, field names, status
// codes and JSON samples render verbatim in every locale. Only the `descKey`,
// `titleKey`, `bodyKey` and `groupKey` pointers cross into api-copy.js.
//
// This table mirrors internal/handlers/http.go. When a route changes there it
// changes here too; web/tests/api-content.test.mjs pins the exact 9 method+path
// combinations so the console cannot drift from the server.
//
// The sidecar wire protocol in internal/bridges/httpbridge.go (/put, /query,
// /delete) is an outbound channel to RAG engines, not part of this API.

export const docsSections = [
  { id: 'overview', titleKey: 'sectionOverview' },
  { id: 'endpoints', titleKey: 'sectionEndpoints' },
  { id: 'errors', titleKey: 'sectionErrors' },
]

/** Default listener. Ports and environment keys are machine data. */
export const serverBase = {
  addr: '127.0.0.1:8080',
  addrEnv: 'AGENTMEM_ADDR',
  containerAddr: '0.0.0.0:8080',
}

export const serverBaseLines = [
  `http://${serverBase.addr}          ${serverBase.addrEnv} (default)`,
  `http://${serverBase.containerAddr}          container / compose`,
  '/healthz  ·  /v1/*             one listener, no version prefix on health',
]

export const headerLines = [
  'Content-Type: application/json; charset=utf-8',
  'request body limit: 4 MiB (4194304 bytes)',
  'authorization: Bearer <access-token>   ·   CORS headers: none   ·   pagination: none',
  'scopes: knowledge:read · knowledge:write · migrate:run · audit:read · compute:run',
  'rate limit: 20 req/s per client, burst 40   ·   Origin allow-list enforced',
]

export const errorLines = [
  '{"error": "authentication required"}',
  '{"error": "permission denied"}',
  '{"error": "rate limit exceeded"}',
  '{"error": "document not found or access denied"}',
  '{"error": "query is empty"}',
  '{"error": "method not allowed"}',
  '{"error": "computation rejected", "envelope": {"concept_id": "computations/example", "display_allowed": false, "verified": false}}',
]

/** Prose blocks on the overview section. Samples stay untranslated. */
export const conventionBlocks = [
  { titleKey: 'baseUrlTitle', bodyKey: 'baseUrlBody', lines: serverBaseLines },
  { titleKey: 'conventionsTitle', bodyKey: 'conventionsBody', lines: headerLines },
  { titleKey: 'errorFormatTitle', bodyKey: 'errorsBody', lines: errorLines },
]

export const endpointGroups = [
  { id: 'system', groupKey: 'groupSystem' },
  { id: 'knowledge', groupKey: 'groupKnowledge' },
  { id: 'routing', groupKey: 'groupRouting' },
  { id: 'computation', groupKey: 'groupComputation' },
]

/**
 * The 9 method+path combinations served by NewHandler.
 * `request` is a JSON body for POST routes and a request line for GET routes.
 * `response` is always a JSON sample. All values are placeholders.
 */
export const endpoints = [
  {
    group: 'system',
    method: 'GET',
    path: '/healthz',
    descKey: 'epHealthz',
    // Public, so no 401/403; 405 still applies to a non-GET method.
    statuses: ['200', '405', '429'],
    request: 'GET /healthz',
    response: `{
  "status": "ok",
  "postgres": "fake (offline)",
  "compute_runtimes": "postgres",
  "adapter:memory": "ok"
}`,
  },
  {
    group: 'knowledge',
    method: 'POST',
    path: '/v1/knowledge',
    descKey: 'epPut',
    statuses: ['200', '400', '401', '403', '405', '429'],
    request: `{
  "concept_id": "concepts/example",
  "document": {
    "type": "Concept",
    "title": "Example",
    "description": "placeholder",
    "body": "placeholder text",
    "tags": ["example"],
    "visibility": "private",
    "owner": "system"
  }
}`,
    response: `{
  "concept_id": "concepts/example",
  "content_hash": "sha256:placeholder",
  "commit_sha": "0abcdef",
  "decision": {
    "targets": ["okf"],
    "reason": "authoritative",
    "rule_id": "R1",
    "confidence": 1,
    "source": "rule"
  },
  "projections": [{ "target": "memory", "status": "synced" }],
  "unchanged": false
}`,
  },
  {
    group: 'knowledge',
    method: 'GET',
    path: '/v1/knowledge',
    descKey: 'epGet',
    statuses: ['200', '400', '401', '403', '404', '405', '429'],
    request: 'GET /v1/knowledge?id=concepts/example',
    response: `{
  "concept_id": "concepts/example",
  "type": "Concept",
  "title": "Example",
  "description": "placeholder",
  "body": "placeholder text",
  "status": "stable",
  "trust_tier": "unverified",
  "visibility": "private",
  "owner": "system"
}`,
  },
  {
    group: 'routing',
    method: 'POST',
    path: '/v1/query',
    descKey: 'epQuery',
    statuses: ['200', '400', '401', '403', '405', '429', '500'],
    request: `{
  "query": "definition of an example",
  "top_k": 5
}`,
    response: `{
  "query": "definition of an example",
  "decision": {
    "targets": ["okf"],
    "reason": "definition query",
    "rule_id": "R2",
    "confidence": 1,
    "source": "rule"
  },
  "hits": [
    {
      "concept_id": "concepts/example",
      "score": 0.91,
      "snippet": "placeholder text",
      "source": "okf",
      "citation": "concepts/example@0abcdef"
    }
  ],
  "conflicts": []
}`,
  },
  {
    group: 'routing',
    method: 'POST',
    path: '/v1/migrate',
    descKey: 'epMigrate',
    statuses: ['200', '400', '401', '403', '405', '429'],
    request: `{
  "job": "nightly",
  "direction": "canonical_to_rag",
  "target": "memory",
  "dry_run": true,
  "limit": 100
}`,
    response: `{
  "job": "nightly",
  "direction": "canonical_to_rag",
  "target": "memory",
  "dry_run": true,
  "scanned": 12,
  "planned": 12,
  "synced": 0,
  "skipped": 0,
  "failed": 0,
  "invalidated": 0,
  "last_concept_id": "concepts/example",
  "done": false
}`,
  },
  {
    group: 'routing',
    method: 'GET',
    path: '/v1/decisions',
    descKey: 'epDecisions',
    statuses: ['200', '400', '401', '403', '405', '429', '500'],
    request: 'GET /v1/decisions?concept_id=concepts/example&limit=20',
    response: `[
  {
    "id": 1,
    "concept_id": "concepts/example",
    "rule_id": "R1",
    "reason": "authoritative",
    "source": "rule",
    "confidence": 1,
    "targets": ["okf"],
    "created_at": "2026-01-01T00:00:00Z"
  }
]`,
  },
  {
    group: 'routing',
    method: 'GET',
    path: '/v1/concepts',
    descKey: 'epConcepts',
    statuses: ['200', '401', '403', '405', '429', '500'],
    request: 'GET /v1/concepts',
    response: `["concepts/example"]`,
  },
  {
    group: 'computation',
    method: 'POST',
    path: '/v1/computations',
    descKey: 'epComputePost',
    statuses: ['200', '400', '401', '403', '405', '422', '429', '502'],
    request: `{
  "concept_id": "computations/example",
  "parameters": { "year": 2026 }
}`,
    response: `{
  "concept_id": "computations/example",
  "runtime": "postgres",
  "contract_hash": "sha256:placeholder",
  "params_digest": "sha256:placeholder",
  "job_id": "job-placeholder",
  "display_allowed": true,
  "display_value": "0",
  "verified": true,
  "stale": false,
  "verdict": {
    "passed": true,
    "provenance": true,
    "fidelity": true,
    "stale": false,
    "reasons": [],
    "contract_hash": "sha256:placeholder",
    "job_id": "job-placeholder"
  }
}`,
  },
  {
    group: 'computation',
    method: 'GET',
    path: '/v1/computations',
    descKey: 'epComputeGet',
    statuses: ['200', '400', '401', '403', '405', '429', '500'],
    request: 'GET /v1/computations?concept_id=computations/example&limit=20',
    response: `[
  {
    "id": 1,
    "concept_id": "computations/example",
    "subject": "alice",
    "runtime": "postgres",
    "contract_hash": "sha256:placeholder",
    "params_digest": "sha256:placeholder",
    "job_id": "job-placeholder",
    "verdict": "passed",
    "display_allowed": true,
    "provenance": true,
    "fidelity": true,
    "stale": false,
    "reasons": [],
    "error_class": "",
    "created_at": "2026-01-01T00:00:00Z"
  }
]`,
  },
]

/** Status rows for the errors section. `codes` is a wire-level hint. */
export const statusGroups = [
  { status: '200', descKey: 'status200', codes: 'ok' },
  { status: '400', descKey: 'status400', codes: 'invalid JSON / required / boundary' },
  { status: '401', descKey: 'status401', codes: 'missing / unknown credentials' },
  { status: '403', descKey: 'status403', codes: 'missing scope / origin' },
  { status: '404', descKey: 'status404', codes: 'not found / refused' },
  { status: '405', descKey: 'status405', codes: 'method not allowed' },
  { status: '422', descKey: 'status422', codes: 'unsupported / attestation' },
  { status: '429', descKey: 'status429', codes: 'client rate limit' },
  { status: '500', descKey: 'status500', codes: 'store / router' },
  { status: '502', descKey: 'status502', codes: 'executor' },
]

/** Canonical list used by api-content.test.mjs to detect drift. */
export const ROUTE_KEYS = endpoints.map((e) => `${e.method} ${e.path}`)
