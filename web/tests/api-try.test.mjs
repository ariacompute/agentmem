import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  MAX_BODY_CHARS,
  TOKEN_KEY,
  buildRequest,
  buildUrl,
  formatBody,
  formatDuration,
  isWrite,
  needsConfirm,
  readToken,
  statusHintKey,
  statusTone,
  storeToken,
  summarizeHeaders,
} from '../src/lib/api-try.js'
import { ROUTE_KEYS, endpoints, tryMeta } from '../src/lib/api-content.js'

/** Minimal sessionStorage stand-in. */
function fakeStorage(initial = {}) {
  const values = { ...initial }
  return {
    getItem: (key) => (key in values ? values[key] : null),
    setItem: (key, value) => {
      values[key] = String(value)
    },
    removeItem: (key) => {
      delete values[key]
    },
    dump: () => values,
  }
}

const POST_KNOWLEDGE = endpoints.find((entry) => entry.method === 'POST' && entry.path === '/v1/knowledge')
const PUT_GRAPH_NODE = endpoints.find((entry) => entry.method === 'PUT' && entry.path === '/v1/graph/node')
const GET_DECISIONS = endpoints.find((entry) => entry.path === '/v1/decisions')

describe('try-it request metadata', () => {
  it('covers every documented route', () => {
    assert.deepEqual(Object.keys(tryMeta).sort(), [...ROUTE_KEYS].sort())
    for (const key of ROUTE_KEYS) {
      assert.equal(typeof tryMeta[key].write, 'boolean', `${key} write flag`)
      assert.equal(typeof tryMeta[key].query, 'object', `${key} query map`)
    }
  })

  it('marks exactly the six state-changing routes as writes', () => {
    const writes = ROUTE_KEYS.filter((key) => tryMeta[key].write)
    assert.deepEqual(writes, [
      'POST /v1/knowledge',
      'POST /v1/migrate',
      'POST /v1/computations',
      'PUT /v1/graph/node',
      'POST /v1/graph/index',
      'POST /v1/graph/edges',
    ])
    assert.equal(isWrite(tryMeta['POST /v1/query']), false, 'a routed read is not a write')
    assert.equal(isWrite(tryMeta['GET /v1/graph']), false, 'a graph read is not a write')
    assert.equal(isWrite(tryMeta['POST /v1/graph/answerability']), false, 'a probe is not a write')
  })

  it('requires a confirmation before a write is sent', () => {
    assert.equal(needsConfirm(tryMeta['POST /v1/knowledge'], false), true)
    assert.equal(needsConfirm(tryMeta['POST /v1/knowledge'], true), false)
    assert.equal(needsConfirm(tryMeta['GET /healthz'], false), false)
  })

  it('keeps every query sample free of an asserted subject', () => {
    for (const key of ROUTE_KEYS) {
      assert.equal('subject' in tryMeta[key].query, false, `${key} must not send a subject`)
    }
  })
})

describe('try-it request building', () => {
  it('builds a bare path when no query value is set', () => {
    assert.equal(buildUrl({ path: '/v1/concepts' }, {}), '/v1/concepts')
    assert.equal(buildUrl({ path: '/v1/concepts' }, { limit: '  ' }), '/v1/concepts')
  })

  it('encodes query samples', () => {
    assert.equal(
      buildUrl(GET_DECISIONS, { concept_id: 'concepts/example', limit: '20' }),
      '/v1/decisions?concept_id=concepts%2Fexample&limit=20',
    )
  })

  it('sends JSON for a body route and no body for a GET route', () => {
    const post = buildRequest(POST_KNOWLEDGE, { body: '{"a":1}' }, '')
    assert.equal(post.init.method, 'POST')
    assert.equal(post.init.headers['Content-Type'], 'application/json')
    assert.equal(post.init.body, '{"a":1}')
    assert.equal(post.url, '/v1/knowledge')

    const put = buildRequest(PUT_GRAPH_NODE, {}, '')
    assert.equal(put.init.method, 'PUT')
    assert.equal(put.init.headers['Content-Type'], 'application/json')
    assert.doesNotThrow(() => JSON.parse(put.init.body))

    const get = buildRequest(GET_DECISIONS, { query: { limit: '5' } }, '')
    assert.equal(get.init.method, 'GET')
    assert.equal('body' in get.init, false)
    assert.equal(get.init.headers['Content-Type'], undefined)
    assert.equal(get.url, '/v1/decisions?limit=5')
  })

  it('falls back to the documented sample body', () => {
    const request = buildRequest(POST_KNOWLEDGE, {}, '')
    assert.equal(request.init.body, POST_KNOWLEDGE.request)
    assert.doesNotThrow(() => JSON.parse(request.init.body))
  })

  it('puts the bearer token in the header only', () => {
    const token = 'secret-value'
    const request = buildRequest(GET_DECISIONS, { query: { limit: '20' } }, token)
    assert.equal(request.init.headers.Authorization, `Bearer ${token}`)
    assert.equal(request.url.includes(token), false)
    assert.equal(JSON.stringify(request.init.body ?? '').includes(token), false)
    assert.equal(
      buildRequest(GET_DECISIONS, {}, '').init.headers.Authorization,
      undefined,
      'no token means no header',
    )
  })

  it('keeps the request same-origin and non-persistent', () => {
    const { init } = buildRequest(GET_DECISIONS, {}, '')
    assert.equal(init.credentials, 'omit')
    assert.equal(init.cache, 'no-store')
    assert.equal(init.redirect, 'error')
  })
})

describe('try-it token storage', () => {
  it('round-trips through sessionStorage', () => {
    const storage = fakeStorage()
    assert.equal(storeToken('  abc  ', storage), 'abc')
    assert.equal(storage.dump()[TOKEN_KEY], 'abc')
    assert.equal(readToken(storage), 'abc')
  })

  it('clears the key when the field is emptied', () => {
    const storage = fakeStorage({ [TOKEN_KEY]: 'abc' })
    assert.equal(storeToken('   ', storage), '')
    assert.equal(TOKEN_KEY in storage.dump(), false)
    assert.equal(readToken(storage), '')
  })

  it('degrades to no token when storage is unavailable or throws', () => {
    assert.equal(readToken({ getItem: () => { throw new Error('blocked') } }), '')
    assert.equal(storeToken('abc', { setItem: () => { throw new Error('blocked') } }), 'abc')
    assert.equal(readToken(null) === '', true, 'tests have no window.sessionStorage')
  })
})

describe('try-it response formatting', () => {
  it('pretty-prints JSON', () => {
    const body = formatBody('{"a":1}', 'application/json')
    assert.equal(body.json, true)
    assert.equal(body.text, '{\n  "a": 1\n}')
    assert.equal(body.truncated, false)
  })

  it('keeps non-JSON bodies verbatim', () => {
    const body = formatBody('plain', 'text/plain')
    assert.equal(body.json, false)
    assert.equal(body.text, 'plain')
  })

  it('truncates oversized bodies', () => {
    const body = formatBody('x'.repeat(MAX_BODY_CHARS + 10), 'text/plain')
    assert.equal(body.truncated, true)
    assert.equal(body.text.length, MAX_BODY_CHARS)
  })

  it('shows only the whitelisted headers, in a stable order', () => {
    const headers = new Map([
      ['server', 'hidden'],
      ['retry-after', '5'],
      ['content-type', 'application/json'],
    ])
    headers.forEach = function forEach(fn) {
      for (const [key, value] of Map.prototype.entries.call(this)) fn(value, key)
    }
    assert.deepEqual(summarizeHeaders(headers), [
      ['content-type', 'application/json'],
      ['retry-after', '5'],
    ])
    assert.deepEqual(summarizeHeaders(null), [])
  })

  it('maps statuses to a tone and a hint without inventing new ones', () => {
    assert.equal(statusTone(200), 'ok')
    assert.equal(statusTone(404), 'client')
    assert.equal(statusTone(502), 'server')
    assert.equal(statusHintKey(401), 'tryErrAuth')
    assert.equal(statusHintKey(403), 'tryErrForbidden')
    assert.equal(statusHintKey(429), 'tryErrRate')
    assert.equal(statusHintKey(200), '')
    assert.equal(formatDuration(12.6), '13 ms')
  })
})
