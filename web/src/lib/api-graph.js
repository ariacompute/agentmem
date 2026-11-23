// Request shaping and derived facts for the graph console view.
//
// Pure functions only: no DOM, no fetch. The view renders, the shell owns the
// network, and every rule here stays testable offline. The one invariant this
// module enforces: a bearer token only ever travels in the Authorization
// header, never in a URL and never in a log line.

/** Server-side ceiling for graph list reads (internal/handlers/graph.go). */
export const GRAPH_LIMIT = 200

/** Depths the console offers; the server caps at AGENTMEM_GRAPH_MAX_DEPTH. */
export const DEPTHS = [1, 2, 3]

/** Walk directions accepted by GET /v1/graph/neighbors. */
export const DIRECTIONS = ['out', 'in', 'both']

function blank(value) {
  return value === undefined || value === null || String(value).trim() === ''
}

/** Builds a graph URL, dropping blank parameters instead of sending empties. */
export function graphUrl(path, params = {}) {
  const search = new URLSearchParams()
  for (const [name, value] of Object.entries(params)) {
    if (!blank(value)) search.set(name, String(value).trim())
  }
  const suffix = search.toString()
  return suffix ? `${path}?${suffix}` : path
}

/** Fetch init shared by every graph call; the token only rides the header. */
export function graphInit(method, token, body) {
  const headers = {}
  if (token) headers.Authorization = `Bearer ${token}`
  const init = { method, headers, credentials: 'omit', cache: 'no-store', redirect: 'error' }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  return init
}

export function statsRequest(token) {
  return { url: graphUrl('/v1/graph'), init: graphInit('GET', token) }
}

export function schemaRequest(token) {
  return { url: graphUrl('/v1/graph/schema'), init: graphInit('GET', token) }
}

export function searchRequest({ query = '', nodeType = '', limit = GRAPH_LIMIT } = {}, token) {
  return {
    url: graphUrl('/v1/graph/search', { q: query, node_types: nodeType, limit }),
    init: graphInit('GET', token),
  }
}

export function nodeRequest(id, token) {
  return { url: graphUrl('/v1/graph/node', { id }), init: graphInit('GET', token) }
}

export function neighborsRequest(
  { from, depth = 1, direction = 'both', edgeTypes = [], limit = GRAPH_LIMIT } = {},
  token,
) {
  return {
    url: graphUrl('/v1/graph/neighbors', {
      from,
      depth,
      direction,
      edge_types: (edgeTypes || []).join(','),
      limit,
    }),
    init: graphInit('GET', token),
  }
}

export function pathRequest({ from, to, maxDepth = 3, direction = 'both', edgeTypes = [] } = {}, token) {
  return {
    url: graphUrl('/v1/graph/path', {
      from,
      to,
      max_depth: maxDepth,
      direction,
      edge_types: (edgeTypes || []).join(','),
    }),
    init: graphInit('GET', token),
  }
}

export function answerRequest(question, token) {
  return {
    url: graphUrl('/v1/graph/answerability'),
    init: graphInit('POST', token, { question }),
  }
}

/** A node write: the declaration is committed to OKF before it is projected. */
export function putNodeRequest({ id, nodeType = '', label = '' } = {}, token) {
  const body = { concept_id: id }
  if (!blank(nodeType)) body.node_type = nodeType
  if (!blank(label)) body.label = label
  return { url: graphUrl('/v1/graph/node'), init: graphInit('PUT', token, body) }
}

export function reindexRequest(token) {
  return { url: graphUrl('/v1/graph/index'), init: graphInit('POST', token, {}) }
}

/** Projection freshness, in the order the badge prefers: work wins over facts. */
export function freshness(stats, busy) {
  if (busy === 'reindex') return 'rebuilding'
  const stale = stats ? Number(stats.stale) : 0
  return Number.isFinite(stale) && stale > 0 ? 'stale' : 'fresh'
}

/** Coverage as a 0-100 integer, clamped so a bad sample cannot break the bar. */
export function coveragePercent(value) {
  const ratio = Number(value)
  if (!Number.isFinite(ratio)) return 0
  return Math.min(100, Math.max(0, Math.round(ratio * 100)))
}

/** One gap rendered as machine data: kind, subject and detail stay verbatim. */
export function gapText(gap) {
  if (!gap || typeof gap !== 'object') return String(gap ?? '')
  const kind = blank(gap.kind) ? 'gap' : String(gap.kind)
  const id = blank(gap.concept_id) ? '-' : String(gap.concept_id)
  const detail = blank(gap.detail) ? '' : ` · ${String(gap.detail)}`
  return `${kind}: ${id}${detail}`
}
