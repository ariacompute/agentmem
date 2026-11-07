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
  serverBase,
  statusGroups,
} from '../src/lib/api-content.js'

/** Mirrors internal/handlers/http.go NewHandler. */
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
]

describe('api-content endpoints', () => {
  it('lists exactly the 9 method+path combinations of the server', () => {
    assert.deepEqual(ROUTE_KEYS, EXPECTED_ROUTES)
    assert.equal(endpoints.length, 9)
    assert.equal(new Set(ROUTE_KEYS).size, 9)
  })

  it('never documents the outbound sidecar wire protocol', () => {
    for (const path of endpoints.map((entry) => entry.path)) {
      assert.equal(path === '/put' || path === '/query' || path === '/delete', false)
      assert.ok(path === '/healthz' || path.startsWith('/v1/'))
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

  it('renders valid JSON samples with placeholder values only', () => {
    for (const entry of endpoints) {
      assert.doesNotThrow(() => JSON.parse(entry.response), `${entry.method} ${entry.path} response`)
      if (entry.method === 'POST') {
        assert.doesNotThrow(() => JSON.parse(entry.request), `${entry.method} ${entry.path} request`)
      } else {
        assert.ok(entry.request.startsWith('GET '), `${entry.path} request line`)
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
