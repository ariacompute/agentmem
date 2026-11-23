import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

// A tiny DOM stand-in: enough to boot main.js offline and assert that both
// views, the language menu and the theme toggle render without throwing.
const ids = new Map()

class FakeElement {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase()
    this.children = []
    this.attributes = {}
    this.dataset = {}
    this.style = {}
    this.listeners = new Map()
    this._text = ''
    this._className = ''
    this._classes = new Set()
    const self = this
    this.classList = {
      add(...names) {
        for (const name of names) self._classes.add(name)
        self._syncClassName()
      },
      remove(...names) {
        for (const name of names) self._classes.delete(name)
        self._syncClassName()
      },
      toggle(name, force) {
        const wanted = force === undefined ? !self._classes.has(name) : Boolean(force)
        if (wanted) self._classes.add(name)
        else self._classes.delete(name)
        self._syncClassName()
        return wanted
      },
      contains(name) {
        return self._classes.has(name)
      },
    }
  }

  _syncClassName() {
    this._className = [...this._classes].join(' ')
  }

  get className() {
    return this._className
  }

  set className(value) {
    this._className = String(value ?? '')
    this._classes = new Set(this._className.split(/\s+/).filter(Boolean))
  }

  get textContent() {
    return this._text + this.children.map((child) => child.textContent).join('')
  }

  set textContent(value) {
    this._text = String(value ?? '')
    this.children = []
  }

  get id() {
    return this.attributes.id || ''
  }

  set id(value) {
    this.setAttribute('id', value)
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value)
    if (name === 'id') ids.set(String(value), this)
  }

  appendChild(node) {
    this.children.push(node)
    return node
  }

  replaceChildren(...nodes) {
    this.children = []
    for (const node of nodes) this.appendChild(node)
  }

  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, [])
    this.listeners.get(type).push(fn)
  }

  removeEventListener(type, fn) {
    const list = this.listeners.get(type) || []
    const index = list.indexOf(fn)
    if (index >= 0) list.splice(index, 1)
  }

  click() {
    for (const fn of this.listeners.get('click') || []) fn({ stopPropagation() {} })
  }

  querySelectorAll(selector) {
    const name = selector.startsWith('.') ? selector.slice(1) : null
    const out = []
    const visit = (node) => {
      for (const child of node.children) {
        if (name && child._classes.has(name)) out.push(child)
        visit(child)
      }
    }
    visit(this)
    return out
  }

  getBoundingClientRect() {
    return { top: 0 }
  }

  scrollIntoView() {}
}

function descendants(node) {
  const out = []
  const visit = (current) => {
    for (const child of current.children) {
      out.push(child)
      visit(child)
    }
  }
  visit(node)
  return out
}

function findByText(root, className, text) {
  return descendants(root).find(
    (node) => node._classes.has(className) && node.textContent === text,
  )
}

function findByAttr(root, name, value) {
  return descendants(root).find((node) => node.attributes[name] === value)
}

const html = new FakeElement('html')
const app = new FakeElement('div')
app.id = 'app'
const storage = new Map()

globalThis.document = {
  documentElement: html,
  createElement: (tag) => new FakeElement(tag),
  createTextNode: (text) => {
    const node = new FakeElement('#text')
    node._text = String(text)
    return node
  },
  getElementById: (id) => ids.get(id) || (id === 'app' ? app : null),
}

globalThis.window = {
  location: { search: '?lang=en' },
  localStorage: {
    getItem: (key) => storage.get(key) ?? null,
    setItem: (key, value) => storage.set(key, value),
  },
  matchMedia: () => ({ matches: false, addEventListener() {} }),
  addEventListener() {},
  removeEventListener() {},
  scrollTo() {},
  scrollY: 0,
}

const GRAPH_NODE = {
  concept_id: 'metrics/gross_margin',
  node_type: 'metric',
  label: 'Gross Margin',
  trust: 'unverified',
  stale: false,
  visibility: 'private',
  content_hash: 'sha256:placeholder',
  commit_sha: '0abcdef',
  sources: [],
  out: [
    {
      from_id: 'metrics/gross_margin',
      to_id: 'tables/fact_sales',
      edge_type: 'computed_from',
      origin: 'declared',
    },
  ],
  in: [],
  warnings: [],
}

function graphPayload(path) {
  if (path === '/v1/graph') {
    return { nodes: 1, edges: 1, node_types: { metric: 1 }, edge_types: { computed_from: 1 }, stale: 0 }
  }
  if (path === '/v1/graph/schema') {
    return {
      node_types: { metric: {} },
      edge_types: { computed_from: { From: ['metric'], To: ['table'] } },
      warnings: [],
    }
  }
  if (path === '/v1/graph/search') return [GRAPH_NODE]
  if (path === '/v1/graph/node') return GRAPH_NODE
  if (path === '/v1/graph/neighbors') {
    return {
      root: 'metrics/gross_margin',
      nodes: [GRAPH_NODE],
      edges: [],
      depths: { 'metrics/gross_margin': 1 },
      truncated: false,
    }
  }
  if (path === '/v1/graph/answerability') {
    return {
      question: 'gross margin',
      verdict: 'partial',
      coverage: 0.5,
      minimum: 0.6,
      matched: [],
      gaps: [{ kind: 'stale', concept_id: 'metrics/gross_margin', detail: 'stale body' }],
      terms: ['gross', 'margin'],
    }
  }
  return { nodes: 1, edges: 1, pruned: 0, warnings: [] }
}

globalThis.fetch = async (url) => {
  const target = String(url)
  const path = target.split('?')[0]
  if (path === '/healthz') {
    return { ok: true, json: async () => ({ status: 'ok', postgres: 'fake (offline)' }) }
  }
  if (path.startsWith('/v1/graph')) {
    return { ok: true, status: 200, json: async () => graphPayload(path) }
  }
  return {
    ok: true,
    status: 200,
    json: async () => [
      { concept_id: 'concepts/example', rule_id: 'R1', source: 'rule', targets: ['okf'], confidence: 1 },
    ],
  }
}

describe('console shell', () => {
  it('boots the status view with the shared shell', async () => {
    await import('../src/main.js')
    await new Promise((resolve) => setTimeout(resolve, 0))

    assert.ok(findByAttr(app, 'aria-label', 'Refresh'), 'refresh control is missing')
    assert.equal(html.lang, 'en')
    assert.equal(html.dir, 'ltr')
    assert.equal(html.dataset.theme, 'dark')
    assert.ok(descendants(app).some((node) => node.textContent === 'Service health'))
    assert.ok(descendants(app).some((node) => node.textContent.includes('concepts/example')))
    assert.equal(descendants(app).filter((node) => node._classes.has('footer')).length, 1)
  })

  it('switches to the docs view and expands an endpoint sample', () => {
    const docsTab = findByText(app, 'tab', 'API docs')
    assert.ok(docsTab, 'API docs tab is missing')
    docsTab.click()

    assert.ok(ids.get('sec-overview'), 'overview section is missing')
    assert.ok(ids.get('sec-endpoints'), 'endpoints section is missing')
    assert.ok(ids.get('sec-errors'), 'errors section is missing')
    assert.equal(
      descendants(app).filter((node) => node._classes.has('endpoint-row')).length,
      23,
    )

    const row = descendants(app).find((node) => node._classes.has('endpoint-row'))
    row.click()
    assert.ok(
      descendants(app).some((node) => node._classes.has('endpoint-detail')),
      'request/response sample did not expand',
    )
  })

  it('renders the graph view with index status, results and node detail', async () => {
    const graphTab = findByText(app, 'tab', 'Graph')
    assert.ok(graphTab, 'graph tab is missing')
    graphTab.click()
    await new Promise((resolve) => setTimeout(resolve, 0))

    assert.ok(findByText(app, 'card-title', 'Query and index'), 'the query card is missing')
    assert.ok(
      descendants(app).some((node) => node.tagName === 'H1' && node.textContent === 'Knowledge graph'),
      'the page title did not follow the view',
    )
    assert.ok(
      descendants(app).some((node) => node._classes.has('chip') && node.textContent === 'fresh'),
      'index freshness is missing',
    )
    assert.ok(
      descendants(app).some((node) => node._classes.has('status-line')),
      'the live status line is missing',
    )
    assert.ok(
      descendants(app).filter((node) => node._classes.has('data-table')).length >= 1,
      'no data table rendered',
    )

    const link = descendants(app).find(
      (node) => node._classes.has('link-btn') && node.textContent === 'metrics/gross_margin',
    )
    assert.ok(link, 'the search result is missing')
    link.click()
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.ok(
      descendants(app).some(
        (node) => node._classes.has('link-btn') && node.textContent === 'tables/fact_sales',
      ),
      'the edge target did not render',
    )
  })

  it('switches language to Arabic and mirrors RTL', () => {
    const trigger = findByAttr(app, 'aria-label', 'Language')
    assert.ok(trigger, 'language switcher is missing')
    trigger.click()
    const arabic = findByText(app, 'lang-item', 'العربية')
    assert.ok(arabic, 'Arabic option is missing')
    arabic.click()

    assert.equal(html.lang, 'ar')
    assert.equal(html.dir, 'rtl')
    assert.equal(storage.get('agentmem.lang'), 'ar')
  })

  it('toggles the theme with an explicit stored choice', async () => {
    const { shellCopy } = await import('../src/lib/copy/shell-copy.js')
    const toggle = findByAttr(app, 'aria-label', shellCopy.ar.themeToLight)
    assert.ok(toggle, 'theme toggle is missing')
    toggle.click()
    assert.equal(html.dataset.theme, 'light')
    assert.equal(storage.get('agentmem.theme'), 'light')
  })
})
