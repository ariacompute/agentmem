import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  DEPTHS,
  DIRECTIONS,
  GRAPH_LIMIT,
  answerRequest,
  coveragePercent,
  freshness,
  gapText,
  graphInit,
  graphUrl,
  neighborsRequest,
  nodeRequest,
  pathRequest,
  putNodeRequest,
  reindexRequest,
  schemaRequest,
  searchRequest,
  statsRequest,
} from '../src/lib/api-graph.js'

describe('graph request shaping', () => {
  it('drops blank parameters instead of sending empties', () => {
    assert.equal(graphUrl('/v1/graph/search'), '/v1/graph/search')
    assert.equal(graphUrl('/v1/graph/search', { q: '  ', limit: '' }), '/v1/graph/search')
    assert.equal(graphUrl('/v1/graph/search', { q: ' margin ', limit: 20 }), '/v1/graph/search?q=margin&limit=20')
  })

  it('encodes a concept id inside the query string, never the path', () => {
    assert.equal(nodeRequest('metrics/gross margin').url, '/v1/graph/node?id=metrics%2Fgross+margin')
    assert.equal(nodeRequest('metrics/gross margin').init.method, 'GET')
  })

  it('reads the index and the declared types', () => {
    assert.equal(statsRequest().url, '/v1/graph')
    assert.equal(schemaRequest().url, '/v1/graph/schema')
    assert.equal(statsRequest().init.body, undefined)
  })

  it('bounds the search and forwards the type filter', () => {
    const { url } = searchRequest({ query: 'margin', nodeType: 'metric' })
    assert.match(url, /q=margin/)
    assert.match(url, /node_types=metric/)
    assert.match(url, new RegExp(`limit=${GRAPH_LIMIT}`))
    assert.equal(searchRequest({}).url, `/v1/graph/search?limit=${GRAPH_LIMIT}`)
  })

  it('walks a neighbourhood with a bounded depth and one direction', () => {
    const { url } = neighborsRequest(
      { from: 'metrics/margin', depth: 2, direction: 'out', edgeTypes: ['derived_from', 'computed_from'] },
    )
    assert.match(url, /from=metrics%2Fmargin/)
    assert.match(url, /depth=2/)
    assert.match(url, /direction=out/)
    assert.match(url, /edge_types=derived_from%2Ccomputed_from/)
    assert.deepEqual(DEPTHS, [1, 2, 3])
    assert.deepEqual(DIRECTIONS, ['out', 'in', 'both'])
  })

  it('asks for a shortest path under max_depth', () => {
    const { url } = pathRequest({ from: 'a', to: 'b', maxDepth: 3 })
    assert.match(url, /max_depth=3/)
    assert.match(url, /direction=both/)
  })

  it('posts a JSON question and a JSON-RPC-free body', () => {
    const answer = answerRequest('what is margin')
    assert.equal(answer.url, '/v1/graph/answerability')
    assert.equal(answer.init.method, 'POST')
    assert.equal(answer.init.headers['Content-Type'], 'application/json')
    assert.deepEqual(JSON.parse(answer.init.body), { question: 'what is margin' })

    const reindex = reindexRequest()
    assert.equal(reindex.url, '/v1/graph/index')
    assert.equal(reindex.init.method, 'POST')
    assert.deepEqual(JSON.parse(reindex.init.body), {})
  })

  it('writes a node with PUT and omits blank optional fields', () => {
    const full = putNodeRequest({ id: 'metrics/margin', nodeType: 'metric', label: 'Gross Margin' })
    assert.equal(full.init.method, 'PUT')
    assert.deepEqual(JSON.parse(full.init.body), {
      concept_id: 'metrics/margin',
      node_type: 'metric',
      label: 'Gross Margin',
    })

    const minimal = putNodeRequest({ id: 'metrics/margin', nodeType: '  ', label: '' })
    assert.deepEqual(JSON.parse(minimal.init.body), { concept_id: 'metrics/margin' })
  })

  it('keeps the bearer token in the header only', () => {
    const token = 'secret-value'
    for (const request of [
      statsRequest(token),
      schemaRequest(token),
      searchRequest({ query: 'x' }, token),
      nodeRequest('metrics/margin', token),
      neighborsRequest({ from: 'metrics/margin' }, token),
      pathRequest({ from: 'a', to: 'b' }, token),
      answerRequest('x', token),
      putNodeRequest({ id: 'metrics/margin' }, token),
      reindexRequest(token),
    ]) {
      assert.equal(request.init.headers.Authorization, `Bearer ${token}`)
      assert.equal(request.url.includes(token), false)
      assert.equal(String(request.init.body ?? '').includes(token), false)
    }
  })

  it('sends no header and no credentials when there is no token', () => {
    const { init } = statsRequest('')
    assert.equal(init.headers.Authorization, undefined)
    assert.equal(init.credentials, 'omit')
    assert.equal(init.cache, 'no-store')
    assert.equal(init.redirect, 'error')
  })

  it('never lets the caller assert a subject', () => {
    const requests = [
      searchRequest({ query: 'x' }),
      neighborsRequest({ from: 'a' }),
      pathRequest({ from: 'a', to: 'b' }),
      answerRequest('x'),
      putNodeRequest({ id: 'a' }),
    ]
    for (const request of requests) {
      assert.equal(request.url.toLowerCase().includes('subject'), false)
      assert.equal(/subject/i.test(String(request.init.body ?? '')), false)
    }
  })
})

describe('graph presentation facts', () => {
  it('prefers work in flight over a stale count', () => {
    assert.equal(freshness({ stale: 0 }, ''), 'fresh')
    assert.equal(freshness({ stale: 3 }, ''), 'stale')
    assert.equal(freshness({ stale: 3 }, 'reindex'), 'rebuilding')
    assert.equal(freshness(null, ''), 'fresh')
    assert.equal(freshness({ stale: 'not-a-number' }, ''), 'fresh')
  })

  it('renders coverage as a clamped integer percentage', () => {
    assert.equal(coveragePercent(0.5), 50)
    assert.equal(coveragePercent(0.666), 67)
    assert.equal(coveragePercent(2), 100)
    assert.equal(coveragePercent(-1), 0)
    assert.equal(coveragePercent('nope'), 0)
  })

  it('renders a gap as machine data with a placeholder for a missing node', () => {
    assert.equal(
      gapText({ kind: 'dangling_edge', concept_id: 'tables/fact_sales', detail: 'target missing' }),
      'dangling_edge: tables/fact_sales · target missing',
    )
    assert.equal(gapText({ kind: 'unverified' }), 'unverified: -')
    assert.equal(gapText(null), '')
  })
})
