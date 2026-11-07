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

globalThis.fetch = async (url) => {
  if (String(url).includes('/healthz')) {
    return { ok: true, json: async () => ({ status: 'ok', postgres: 'fake (offline)' }) }
  }
  return {
    ok: true,
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
      9,
    )

    const row = descendants(app).find((node) => node._classes.has('endpoint-row'))
    row.click()
    assert.ok(
      descendants(app).some((node) => node._classes.has('endpoint-detail')),
      'request/response sample did not expand',
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
