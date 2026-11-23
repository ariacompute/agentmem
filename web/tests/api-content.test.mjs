import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { LANG_CODES } from '../src/lib/languages.js'
import { resolve } from '../src/lib/i18n.js'
import { apiCopy } from '../src/lib/copy/api-copy.js'
import {
  ROUTE_KEYS,
  conventionBlocks,
  docsSections,
  endpointGroups,
  endpoints,
  errorLines,
  graphLines,
  headerLines,
  serverBase,
  statusGroups,
  tryMeta,
} from '../src/lib/api-content.js'

/** Mirrors internal/handlers/http.go NewHandler and routePermissions. */
const EXPECTED_ROUTES = [
  'GET /healthz',
  'POST /v1/knowledge',
  'GET /v1/knowledge',
  'POST /v1/query',
  'POST /v1/migrate',
  'GET /v1/decisions',
  'GET /v1/concepts',
  'POST /v1/computations',
  'GET /v1/computations',
  'GET /v1/graph',
  'GET /v1/graph/node',
  'PUT /v1/graph/node',
  'GET /v1/graph/neighbors',
  'GET /v1/graph/path',
  'GET /v1/graph/search',
  'GET /v1/graph/schema',
  'POST /v1/graph/answerability',
  'POST /v1/graph/retrieve',
  'POST /v1/graph/index',
  'POST /v1/graph/edges',
  'POST /mcp',
]

describe('api-content endpoints', () => {
  it('lists exactly the 21 method+path combinations of the server', () => {
    assert.deepEqual(ROUTE_KEYS, EXPECTED_ROUTES)
    assert.equal(endpoints.length, 21)
    assert.equal(new Set(ROUTE_KEYS).size, 21)
  })

  it('never documents the outbound sidecar wire protocol', () => {
    for (const path of endpoints.map((entry) => entry.path)) {
      assert.equal(path === '/put' || path === '/query' || path === '/delete', false)
      assert.ok(path === '/healthz' || path === '/mcp' || path.startsWith('/v1/'))
    }
  })

  it('keeps every pointer resolvable in all 13 languages', () => {
    const pointers = [
      ...docsSections.map((section) => section.titleKey),
      ...endpointGroups.map((group) => group.groupKey),
      ...conventionBlocks.flatMap((block) => [block.titleKey, block.bodyKey]),
      ...endpoints.map((entry) => entry.descKey),
      ...statusGroups.map((group) => group.descKey),
      'overviewBody',
      'endpointsBody',
      'endpointsNote',
      'errorsBody',
      'errorBodyNote',
      'computeNote',
      'limitNote',
      'showExample',
      'hideExample',
      'colMethod',
      'colPath',
      'colStatus',
      'colDescription',
      'colRequest',
      'colResponse',
      'colMeaning',
      'colCode',
      'pageTitle',
      'pageSub',
      'versionNote',
      'indexTitle',
    ]
    for (const code of LANG_CODES) {
      const table = resolve(apiCopy, code)
      for (const key of pointers) {
        assert.ok(typeof table[key] === 'string' && table[key].length > 0, `${code}.${key}`)
      }
    }
  })

  it('documents the graph gates as machine data, not prose', () => {
    const block = conventionBlocks.find((entry) => entry.bodyKey === 'graphBody')
    assert.ok(block, 'graph convention block')
    assert.equal(block.titleKey, 'graphTitle')
    assert.ok(graphLines.length >= 4)
    assert.equal(block.lines, graphLines)
    assert.ok(graphLines.some((line) => line.includes('AGENTMEM_GRAPH_ENABLED=false')))
    assert.ok(graphLines.some((line) => line.includes('graph:write')))
  })

  it('describes every graph route on its own', () => {
    const graph = endpoints.filter((entry) => entry.group === 'graph')
    assert.equal(graph.length, 12)
    const keys = graph.map((entry) => entry.descKey)
    assert.equal(new Set(keys).size, keys.length, 'graph routes share a description')
    for (const key of keys) {
      assert.notEqual(key, 'epGraphRead')
    }
  })

  it('keeps the schema sample in the same snake_case shape the server emits', () => {
    const schema = endpoints.find((entry) => entry.path === '/v1/graph/schema')
    const body = JSON.parse(schema.response)
    assert.deepEqual(Object.keys(body), ['node_types', 'edge_types', 'warnings'])
    assert.deepEqual(Object.keys(body.edge_types.derived_from), ['from', 'to'])
    assert.doesNotMatch(schema.response, /"(Description|From|To)"/)
  })

  it('marks every graph mutation as a confirmed write', () => {
    const writes = ['PUT /v1/graph/node', 'POST /v1/graph/edges', 'POST /v1/graph/index']
    for (const key of writes) {
      assert.equal(tryMeta[key].write, true, key)
    }
    const reads = ['GET /v1/graph', 'GET /v1/graph/node', 'POST /v1/graph/retrieve', 'POST /mcp']
    for (const key of reads) {
      assert.equal(tryMeta[key].write, false, key)
    }
  })

  it('renders valid JSON samples with placeholder values only', () => {
    for (const entry of endpoints) {
      assert.doesNotThrow(() => JSON.parse(entry.response), `${entry.method} ${entry.path} response`)
      if (entry.method === 'GET') {
        assert.ok(entry.request.startsWith('GET '), `${entry.path} request line`)
      } else {
        // POST and PUT both carry a JSON body.
        assert.doesNotThrow(() => JSON.parse(entry.request), `${entry.method} ${entry.path} request`)
      }
      assert.ok(entry.statuses.every((status) => /^\d{3}$/.test(status)))
      assert.ok(!/password|token|secret|dsn/i.test(entry.request + entry.response))
    }
    for (const line of errorLines) {
      assert.doesNotThrow(() => JSON.parse(line), line)
    }
  })

  it('places every endpoint in a declared group and every section in the index', () => {
    const groupIds = new Set(endpointGroups.map((group) => group.id))
    for (const entry of endpoints) assert.ok(groupIds.has(entry.group), entry.group)
    assert.equal(docsSections.length, 3)
    assert.equal(serverBase.addr, '127.0.0.1:8080')
    assert.equal(serverBase.addrEnv, 'AGENTMEM_ADDR')
  })
})

describe('api-content authentication', () => {
  it('documents a bearer token instead of anonymous access', () => {
    const conventions = headerLines.join('\n')
    assert.match(conventions, /authorization: Bearer/)
    assert.doesNotMatch(conventions, /authorization: none/i)
    assert.match(conventions, /knowledge:read/)
    assert.match(conventions, /compute:run/)
    assert.match(conventions, /graph:write/)
    assert.match(conventions, /rate limit/i)
  })

  it('never lets the caller assert a subject', () => {
    for (const entry of endpoints) {
      assert.equal(/"subject"/.test(entry.request), false, `${entry.method} ${entry.path} request`)
      assert.equal(/subject=/.test(entry.request), false, `${entry.method} ${entry.path} query`)
    }
  })

  it('advertises authentication and authorization failures on protected routes', () => {
    for (const entry of endpoints) {
      const route = `${entry.method} ${entry.path}`
      if (route === 'GET /healthz') {
        // Public route: no 401/403, but a wrong method still answers 405.
        assert.deepEqual(entry.statuses, ['200', '405', '429'])
        continue
      }
      assert.ok(entry.statuses.includes('401'), `${route} lacks 401`)
      assert.ok(entry.statuses.includes('403'), `${route} lacks 403`)
      assert.ok(entry.statuses.includes('429'), `${route} lacks 429`)
    }
  })

  it('uses only statuses explained in the error table', () => {
    const explained = new Set(statusGroups.map((group) => group.status))
    assert.deepEqual([...explained], [
      '200',
      '202',
      '400',
      '401',
      '403',
      '404',
      '405',
      '413',
      '422',
      '429',
      '500',
      '502',
    ])
    for (const entry of endpoints) {
      for (const status of entry.statuses) {
        assert.ok(explained.has(status), `${entry.method} ${entry.path} answers undocumented ${status}`)
      }
    }
  })

  it('ships an error sample for every documented authentication failure', () => {
    const samples = errorLines.join('\n')
    for (const message of ['authentication required', 'permission denied', 'rate limit exceeded']) {
      assert.ok(samples.includes(message), `${message} has no sample`)
    }
  })
})
