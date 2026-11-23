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
  '/                        console page, compiled into the binary',
]

export const headerLines = [
  'Content-Type: application/json; charset=utf-8',
  'request body limit: 4 MiB (4194304 bytes)',
  'authorization: Bearer <access-token>   ·   CORS headers: none   ·   pagination: none',
  'scopes: knowledge:read · knowledge:write · migrate:run · audit:read · compute:run · graph:write',
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
  { id: 'graph', groupKey: 'groupGraph' },
]

/**
 * The 21 method+path combinations served by NewHandler.
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
  {
    group: 'graph',
    method: 'GET',
    path: '/v1/graph',
    descKey: 'epGraphStats',
    statuses: ['200', '401', '403', '405', '429', '500'],
    request: 'GET /v1/graph',
    response: `{
  "nodes": 12,
  "edges": 18,
  "node_types": { "metric": 4, "table": 3, "concept": 5 },
  "edge_types": { "derived_from": 6, "computed_from": 12 },
  "stale": 0
}`,
  },
  {
    group: 'graph',
    method: 'GET',
    path: '/v1/graph/node',
    descKey: 'epGraphRead',
    statuses: ['200', '400', '401', '403', '404', '405', '429', '500'],
    request: 'GET /v1/graph/node?id=metrics/example',
    response: `{
  "concept_id": "metrics/example",
  "node_type": "metric",
  "label": "Example",
  "trust": "unverified",
  "stale": false,
  "visibility": "private",
  "sources": [],
  "out": [],
  "in": [],
  "warnings": []
}`,
  },
  {
    group: 'graph',
    method: 'PUT',
    path: '/v1/graph/node',
    descKey: 'epGraphWrite',
    statuses: ['200', '400', '401', '403', '405', '422', '429', '500'],
    request: `{
  "concept_id": "metrics/example",
  "node_type": "metric",
  "label": "Example",
  "edges": [{ "to": "tables/example", "type": "computed_from" }],
  "replace_edges": true
}`,
    response: `{
  "concept_id": "metrics/example",
  "content_hash": "sha256:placeholder",
  "commit_sha": "0abcdef",
  "unchanged": false,
  "warnings": ["dangling_edge: computed_from -> tables/example"]
}`,
  },
  {
    group: 'graph',
    method: 'GET',
    path: '/v1/graph/neighbors',
    descKey: 'epGraphRead',
    statuses: ['200', '400', '401', '403', '404', '405', '429', '500'],
    request: 'GET /v1/graph/neighbors?from=metrics/example&depth=1&direction=both',
    response: `{
  "root": "metrics/example",
  "nodes": [],
  "edges": [],
  "depths": {},
  "truncated": false
}`,
  },
  {
    group: 'graph',
    method: 'GET',
    path: '/v1/graph/path',
    descKey: 'epGraphRead',
    statuses: ['200', '400', '401', '403', '404', '405', '429', '500'],
    request: 'GET /v1/graph/path?from=metrics/example&to=tables/example',
    response: `{
  "from": "metrics/example",
  "to": "tables/example",
  "found": false,
  "hops": 0,
  "nodes": [],
  "edges": []
}`,
  },
  {
    group: 'graph',
    method: 'GET',
    path: '/v1/graph/search',
    descKey: 'epGraphRead',
    statuses: ['200', '400', '401', '403', '405', '429', '500'],
    request: 'GET /v1/graph/search?q=example&limit=20',
    response: `[
  {
    "concept_id": "metrics/example",
    "node_type": "metric",
    "label": "Example",
    "trust": "unverified",
    "stale": false
  }
]`,
  },
  {
    group: 'graph',
    method: 'GET',
    path: '/v1/graph/schema',
    descKey: 'epGraphRead',
    statuses: ['200', '401', '403', '405', '429', '500'],
    request: 'GET /v1/graph/schema',
    response: `{
  "node_types": { "metric": { "Description": "" } },
  "edge_types": { "derived_from": { "From": ["metric"], "To": ["metric"] } },
  "warnings": []
}`,
  },
  {
    group: 'graph',
    method: 'POST',
    path: '/v1/graph/answerability',
    descKey: 'epGraphAnswer',
    statuses: ['200', '400', '401', '403', '405', '429', '500'],
    request: `{
  "question": "what is the example metric"
}`,
    response: `{
  "question": "what is the example metric",
  "verdict": "partial",
  "coverage": 0.7,
  "minimum": 0.6,
  "matched": [],
  "gaps": [{ "kind": "unverified", "concept_id": "metrics/example", "detail": "placeholder" }],
  "terms": ["example", "metric"]
}`,
  },
  {
    group: 'graph',
    method: 'POST',
    path: '/v1/graph/retrieve',
    descKey: 'epGraphAnswer',
    statuses: ['200', '400', '401', '403', '405', '429', '500'],
    request: `{
  "query": "example metric",
  "top_k": 5
}`,
    response: `{
  "query": "example metric",
  "hits": [{ "id": "metrics/example", "score": 0.032, "rank": 1, "sources": ["text", "graph"] }],
  "nodes": [],
  "warnings": []
}`,
  },
  {
    group: 'graph',
    method: 'POST',
    path: '/v1/graph/index',
    descKey: 'epGraphIndex',
    statuses: ['200', '401', '403', '405', '429', '500'],
    request: `{}`,
    response: `{
  "nodes": 12,
  "edges": 18,
  "pruned": 0,
  "warnings": []
}`,
  },
  {
    group: 'graph',
    method: 'POST',
    path: '/v1/graph/edges',
    descKey: 'epGraphWrite',
    statuses: ['200', '400', '401', '403', '405', '422', '429', '500'],
    request: `{
  "concept_id": "metrics/example",
  "edges": [{ "to": "tables/example", "type": "computed_from" }]
}`,
    response: `{
  "concept_id": "metrics/example",
  "content_hash": "sha256:placeholder",
  "commit_sha": "0abcdef",
  "unchanged": false,
  "warnings": []
}`,
  },
  {
    group: 'graph',
    method: 'POST',
    path: '/mcp',
    descKey: 'epMCP',
    statuses: ['200', '202', '401', '403', '405', '413', '429'],
    request: `{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list"
}`,
    response: `{
  "jsonrpc": "2.0",
  "id": 1,
  "result": { "tools": [{ "name": "search_terms" }] }
}`,
  },
]

/**
 * Interactive metadata for the try-it workbench, keyed by `METHOD path`.
 *
 * Machine data again: `query` holds the sample value of every query parameter
 * the route accepts (empty for routes without one), and `write` marks the
 * routes that change stored state, which the panel asks the operator to
 * confirm before sending. Every key in ROUTE_KEYS must appear here;
 * api-content.test.mjs enforces that, so a new endpoint cannot ship without a
 * request form.
 */
export const tryMeta = {
  'GET /healthz': { write: false, query: {} },
  'POST /v1/knowledge': { write: true, query: {} },
  'GET /v1/knowledge': { write: false, query: { id: 'concepts/example' } },
  'POST /v1/query': { write: false, query: {} },
  'POST /v1/migrate': { write: true, query: {} },
  'GET /v1/decisions': { write: false, query: { concept_id: 'concepts/example', limit: '20' } },
  'GET /v1/concepts': { write: false, query: {} },
  'POST /v1/computations': { write: true, query: {} },
  'GET /v1/computations': {
    write: false,
    query: { concept_id: 'computations/example', limit: '20' },
  },
  'GET /v1/graph': { write: false, query: {} },
  'GET /v1/graph/node': { write: false, query: { id: 'metrics/example' } },
  'PUT /v1/graph/node': { write: true, query: {} },
  'GET /v1/graph/neighbors': {
    write: false,
    query: { from: 'metrics/example', depth: '1', direction: 'both', limit: '20' },
  },
  'GET /v1/graph/path': {
    write: false,
    query: { from: 'metrics/example', to: 'tables/example', max_depth: '3' },
  },
  'GET /v1/graph/search': { write: false, query: { q: 'example', limit: '20' } },
  'GET /v1/graph/schema': { write: false, query: {} },
  'POST /v1/graph/answerability': { write: false, query: {} },
  'POST /v1/graph/retrieve': { write: false, query: {} },
  'POST /v1/graph/index': { write: true, query: {} },
  'POST /v1/graph/edges': { write: true, query: {} },
  'POST /mcp': { write: false, query: {} },
}

/** Status rows for the errors section. `codes` is a wire-level hint. */
export const statusGroups = [
  { status: '200', descKey: 'status200', codes: 'ok' },
  // JSON-RPC notifications have no id and therefore no result body.
  { status: '202', descKey: 'status202', codes: 'accepted / notification' },
  { status: '400', descKey: 'status400', codes: 'invalid JSON / required / boundary' },
  { status: '401', descKey: 'status401', codes: 'missing / unknown credentials' },
  { status: '403', descKey: 'status403', codes: 'missing scope / origin' },
  { status: '404', descKey: 'status404', codes: 'not found / refused' },
  { status: '405', descKey: 'status405', codes: 'method not allowed' },
  { status: '413', descKey: 'status413', codes: 'body too large' },
  { status: '422', descKey: 'status422', codes: 'unsupported / attestation' },
  { status: '429', descKey: 'status429', codes: 'client rate limit' },
  { status: '500', descKey: 'status500', codes: 'store / router' },
  { status: '502', descKey: 'status502', codes: 'executor' },
]

/** Canonical list used by api-content.test.mjs to detect drift. */
export const ROUTE_KEYS = endpoints.map((e) => `${e.method} ${e.path}`)
