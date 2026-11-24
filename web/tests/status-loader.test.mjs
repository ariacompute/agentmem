import assert from 'node:assert/strict'
import { after, before, describe, it } from 'node:test'

class Node {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase()
    this.children = []
    this.className = ''
    this._text = ''
  }

  get textContent() {
    return this._text + this.children.map((child) => child.textContent).join('')
  }

  set textContent(value) {
    this._text = String(value ?? '')
    this.children = []
  }

  appendChild(node) {
    this.children.push(node)
    return node
  }
}

const previousDocument = globalThis.document

before(() => {
  globalThis.document = {
    documentElement: new Node('html'),
    createElement: (tag) => new Node(tag),
    createTextNode: (text) => {
      const node = new Node('#text')
      node._text = String(text)
      return node
    },
  }
})

after(() => {
  globalThis.document = previousDocument
})

const payload = (body) => ({ ok: true, status: 200, json: async () => body })
const refusal = (status) => ({ ok: false, status, json: async () => ({}) })

describe('status snapshot', () => {
  it('keeps health visible when only the private decisions probe needs a token', async () => {
    const { loadStatusSnapshot } = await import('../src/views/status-loader.js')
    const { t } = await import('../src/lib/i18n.js')
    const seen = []
    const fakeFetch = async (url, init) => {
      seen.push([url, init])
      if (url === '/healthz') return payload({ status: 'ok', postgres: 'configured' })
      return refusal(401)
    }

    const snapshot = await loadStatusSnapshot(fakeFetch, '')
    assert.equal(snapshot.health.status, 'ok')
    assert.equal(snapshot.healthError, '')
    assert.deepEqual(snapshot.decisions, [])
    assert.equal(snapshot.decisionsError, t('shell', 'decisionsAuth'))
    assert.equal(seen[1][1], undefined, 'an empty token must not be sent')
  })

  it('keeps decisions when health fails and sends a present token only to the API', async () => {
    const { loadStatusSnapshot } = await import('../src/views/status-loader.js')
    const { t } = await import('../src/lib/i18n.js')
    const seen = []
    const fakeFetch = async (url, init) => {
      seen.push([url, init])
      if (url === '/healthz') return refusal(500)
      return payload([{ concept_id: 'concepts/example' }])
    }

    const snapshot = await loadStatusSnapshot(fakeFetch, 'probe-token')
    assert.deepEqual(snapshot.health, {})
    assert.equal(snapshot.healthError, t('shell', 'apiError') + t('shell', 'badStatus'))
    assert.equal(snapshot.decisions[0].concept_id, 'concepts/example')
    assert.equal(snapshot.decisionsError, '')
    assert.equal(seen[0][1], undefined, 'the public health probe stays anonymous')
    assert.equal(seen[1][1].headers.Authorization, 'Bearer probe-token')
  })

  it('renders the token hint beside the decisions, never across the health card', async () => {
    const { renderStatus } = await import('../src/views/status.js')
    const { t } = await import('../src/lib/i18n.js')
    const host = new Node('div')

    renderStatus(host, {
      health: { status: 'ok' },
      decisions: [],
      healthError: '',
      decisionsError: t('shell', 'decisionsAuth'),
      loaded: true,
    })

    assert.equal(host.children.length, 2)
    assert.ok(host.children[0].textContent.includes('status'))
    assert.ok(host.children[0].textContent.includes('ok'))
    assert.ok(!host.children[0].textContent.includes(t('shell', 'apiError')))
    assert.ok(host.children[1].textContent.includes(t('shell', 'decisionsAuth')))
    assert.ok(!host.children[1].textContent.includes(t('shell', 'noDecisions')))
  })
})
