// Template-level console shell: shared top bar, two views, language and theme
// switching. Dependency-free ES module so the page also works without a bundler
// or Node runtime when served straight from web/.
import { LANGUAGES } from './lib/languages.js'
import { getLang, initLang, mirrorHtml, onLangChange, setLang, t } from './lib/i18n.js'
import { getTheme, initTheme, onThemeChange, toggleTheme } from './lib/theme.js'
import { button, chip, el } from './lib/dom.js'
import { readToken } from './lib/api-try.js'
import {
  answerRequest,
  nodeRequest,
  neighborsRequest,
  putNodeRequest,
  reindexRequest,
  schemaRequest,
  searchRequest,
  statsRequest,
} from './lib/api-graph.js'
import { renderStatus } from './views/status.js'
import { cleanupDocs, renderDocs } from './views/docs.js'
import { initialGraphState, renderGraph } from './views/graph.js'

const root = document.getElementById('app')

const statusState = { health: {}, decisions: [], error: '', loaded: false }
// `drafts` keeps unsent requests alive across the shell's full re-render; the
// token never enters this state, it stays in sessionStorage.
const docsState = { open: {}, drafts: {} }
// The graph view keeps its selection, filters and probe results across the
// shell's re-renders, exactly like the docs drafts above.
const graphState = initialGraphState()
const VIEWS = ['status', 'docs', 'graph']
const initialView =
  typeof window !== 'undefined' && window.location
    ? new URLSearchParams(window.location.search).get('view')
    : null
let view = VIEWS.includes(initialView) ? initialView : 'status'
let langMenuOpen = false

function buildLangSwitcher() {
  const wrap = el('div', 'lang')
  const current = LANGUAGES.find(([code]) => code === getLang())?.[1] ?? 'English'
  const trigger = button('', 'icon-btn')
  trigger.setAttribute('aria-label', t('shell', 'langLabel'))
  trigger.title = t('shell', 'langLabel')
  trigger.appendChild(el('span', 'theme-glyph', '◎'))
  trigger.appendChild(el('span', 'label', current))
  trigger.addEventListener('click', () => {
    langMenuOpen = !langMenuOpen
    render()
  })
  wrap.appendChild(trigger)

  if (langMenuOpen) {
    const menu = el('div', 'lang-menu')
    menu.setAttribute('role', 'listbox')
    for (const [code, name] of LANGUAGES) {
      const item = button(name, `lang-item${code === getLang() ? ' active' : ''}`, () => {
        langMenuOpen = false
        setLang(code)
      })
      item.setAttribute('role', 'option')
      item.setAttribute('aria-selected', code === getLang() ? 'true' : 'false')
      menu.appendChild(item)
    }
    wrap.appendChild(menu)
  }
  return wrap
}

function buildThemeButton() {
  const theme = getTheme()
  const toDark = theme === 'light'
  const action = t('shell', toDark ? 'themeToDark' : 'themeToLight')
  const btn = button('', 'icon-btn')
  btn.setAttribute('aria-label', action)
  btn.title = `${t('shell', 'themeLabel')} · ${action}`
  btn.appendChild(el('span', 'theme-glyph', toDark ? '☾' : '☀'))
  btn.appendChild(el('span', 'label', t('shell', toDark ? 'themeLight' : 'themeDark')))
  btn.addEventListener('click', toggleTheme)
  return btn
}

function buildTopbar() {
  const bar = el('header', 'topbar')
  const inner = el('div', 'topbar-inner')

  const brand = el('div', 'brand')
  brand.appendChild(el('span', 'brand-mark', 'a'))
  brand.appendChild(el('span', 'brand-name', 'agentmem'))
  brand.appendChild(chip(t('shell', 'footerBuild')))
  inner.appendChild(brand)

  const tabs = el('nav', 'tabs')
  for (const [id, key] of [
    ['status', 'navStatus'],
    ['docs', 'navDocs'],
    ['graph', 'navGraph'],
  ]) {
    const label = id === 'graph' ? t('graph', key) : t('shell', key)
    tabs.appendChild(
      button(label, `tab${view === id ? ' active' : ''}`, () => {
        view = id
        langMenuOpen = false
        render()
        if (id === 'graph' && !graphState.loaded) loadGraph()
      }),
    )
  }
  inner.appendChild(tabs)

  const actions = el('div', 'topbar-actions')
  if (view === 'status') {
    const refresh = button('', 'icon-btn', loadStatus)
    refresh.setAttribute('aria-label', t('shell', 'refresh'))
    refresh.title = t('shell', 'refresh')
    refresh.appendChild(el('span', 'theme-glyph', '↻'))
    refresh.appendChild(el('span', 'label', t('shell', 'refresh')))
    actions.appendChild(refresh)
  }
  actions.appendChild(buildLangSwitcher())
  actions.appendChild(buildThemeButton())
  inner.appendChild(actions)

  bar.appendChild(inner)
  return bar
}

function buildPageHead() {
  const head = el('div', 'page-head')
  const row = el('div', 'page-title-row')
  const title =
    view === 'status'
      ? t('shell', 'statusHeadTitle')
      : view === 'graph'
        ? t('graph', 'headTitle')
        : t('api', 'pageTitle')
  const note = view === 'status' ? t('shell', 'footerBuild') : t('api', 'versionNote')
  const sub =
    view === 'status'
      ? t('shell', 'statusHeadSub')
      : view === 'graph'
        ? t('graph', 'headSub')
        : t('api', 'pageSub')
  row.appendChild(el('h1', null, title))
  row.appendChild(chip(note))
  head.appendChild(row)
  head.appendChild(el('p', 'page-sub', sub))
  return head
}

function buildFooter() {
  const theme = getTheme()
  const footer = el('footer', 'footer')
  const inner = el('div', 'footer-inner')
  inner.appendChild(el('span', null, t('shell', 'subtitle')))
  const meta = el('div', 'footer-meta')
  meta.appendChild(el('span', null, t('shell', 'footerSource')))
  meta.appendChild(chip(getLang()))
  meta.appendChild(chip(t('shell', theme === 'light' ? 'themeLight' : 'themeDark')))
  meta.appendChild(chip(t('shell', 'footerBuild')))
  inner.appendChild(meta)
  footer.appendChild(inner)
  return footer
}

function render() {
  const scrollY = window.scrollY
  cleanupDocs()
  root.replaceChildren()
  root.appendChild(buildTopbar())
  const main = el('main', 'main')
  main.appendChild(buildPageHead())
  const host = el('div', 'view')
  host.id = 'view'
  main.appendChild(host)
  root.appendChild(main)
  root.appendChild(buildFooter())

  if (view === 'status') {
    renderStatus(host, statusState)
  } else if (view === 'graph') {
    renderGraph(host, graphState, graphActions)
  } else {
    renderDocs(host, docsState, {
      onToggle: (key) => {
        docsState.open[key] = !docsState.open[key]
        render()
      },
      onDraft: (key, draft) => {
        docsState.drafts[key] = draft
      },
    })
  }
  window.scrollTo(0, scrollY)
}

async function loadStatus() {
  try {
    // /healthz is public; the audit trail is not. When the operator has entered
    // a token in the docs workbench, the status view reuses it so an
    // authenticated deployment does not show a permanent 401.
    const token = readToken()
    const authorized = token ? { headers: { Authorization: `Bearer ${token}` } } : undefined
    const [healthRes, decisionsRes] = await Promise.all([
      fetch('/healthz'),
      fetch('/v1/decisions?limit=20', authorized),
    ])
    if (!healthRes.ok || !decisionsRes.ok) throw new Error(t('shell', 'badStatus'))
    statusState.health = await healthRes.json()
    statusState.decisions = (await decisionsRes.json()) || []
    statusState.error = ''
  } catch (err) {
    statusState.health = {}
    statusState.decisions = []
    statusState.error = t('shell', 'apiError') + err.message
  }
  statusState.loaded = true
  if (view === 'status') render()
}

/** One authenticated JSON call against a graph endpoint. */
async function callGraph(request) {
  const response = await fetch(request.url, request.init)
  let data = null
  try {
    data = await response.json()
  } catch (error) {
    // A 204 or a proxy error page has no JSON body; the status code still rules.
    data = null
  }
  if (!response.ok) {
    const detail = data && typeof data.error === 'string' ? data.error : `HTTP ${response.status}`
    throw new Error(detail)
  }
  return data
}

function graphFail(err) {
  graphState.error = t('shell', 'apiError') + (err && err.message ? err.message : '')
}

/** Loads the index size, the declared types and the first result page. */
async function loadGraph() {
  graphState.busy = 'load'
  if (view === 'graph') render()
  try {
    const token = readToken()
    const [stats, schema] = await Promise.all([
      callGraph(statsRequest(token)),
      callGraph(schemaRequest(token)),
    ])
    if (!stats || typeof stats !== 'object' || Array.isArray(stats)) {
      throw new Error(t('shell', 'badStatus'))
    }
    graphState.stats = {
      nodes: Number(stats.nodes) || 0,
      edges: Number(stats.edges) || 0,
      stale: Number(stats.stale) || 0,
    }
    const edgeTypes = schema && schema.edge_types ? Object.keys(schema.edge_types) : []
    graphState.types = schema && schema.node_types ? Object.keys(schema.node_types) : []
    graphState.edgeTypes = edgeTypes
    graphState.activeEdgeTypes = graphState.activeEdgeTypes.filter((type) => edgeTypes.includes(type))
    // Opens on the first page so the list is never empty for no reason.
    const hits = await callGraph(
      searchRequest({ query: graphState.query, nodeType: graphState.nodeType }, token),
    )
    graphState.hits = Array.isArray(hits) ? hits : []
    graphState.error = ''
  } catch (err) {
    graphFail(err)
  }
  graphState.busy = ''
  graphState.loaded = true
  if (view === 'graph') render()
}

async function searchGraph() {
  graphState.busy = 'search'
  if (view === 'graph') render()
  try {
    const hits = await callGraph(
      searchRequest({ query: graphState.query, nodeType: graphState.nodeType }, readToken()),
    )
    graphState.hits = Array.isArray(hits) ? hits : []
    graphState.error = ''
  } catch (err) {
    graphState.hits = []
    graphFail(err)
  }
  graphState.busy = ''
  if (view === 'graph') render()
}

async function selectGraphNode(id) {
  if (!id) return
  graphState.busy = 'node'
  if (view === 'graph') render()
  try {
    graphState.selected = await callGraph(nodeRequest(id, readToken()))
    graphState.subgraph = null
    graphState.error = ''
  } catch (err) {
    graphState.selected = null
    graphFail(err)
  }
  graphState.busy = ''
  if (view === 'graph') render()
}

async function expandGraph() {
  if (!graphState.selected) return
  graphState.busy = 'neighbors'
  if (view === 'graph') render()
  try {
    graphState.subgraph = await callGraph(
      neighborsRequest(
        {
          from: graphState.selected.concept_id,
          depth: graphState.depth,
          direction: graphState.direction,
          edgeTypes: graphState.activeEdgeTypes,
        },
        readToken(),
      ),
    )
    graphState.error = ''
  } catch (err) {
    graphState.subgraph = null
    graphFail(err)
  }
  graphState.busy = ''
  if (view === 'graph') render()
}

async function checkAnswerability() {
  const question = graphState.question.trim()
  if (!question) {
    graphState.coverage = null
    if (view === 'graph') render()
    return
  }
  graphState.busy = 'answer'
  if (view === 'graph') render()
  try {
    graphState.coverage = await callGraph(answerRequest(question, readToken()))
    graphState.error = ''
  } catch (err) {
    graphState.coverage = null
    graphFail(err)
  }
  graphState.busy = ''
  if (view === 'graph') render()
}

async function reindexGraph() {
  graphState.busy = 'reindex'
  if (view === 'graph') render()
  try {
    await callGraph(reindexRequest(readToken()))
    graphState.error = ''
  } catch (err) {
    graphFail(err)
  }
  graphState.busy = ''
  await loadGraph()
}

async function saveGraphNode() {
  if (!graphState.draft.id) return
  graphState.busy = 'save'
  if (view === 'graph') render()
  try {
    graphState.saved = await callGraph(putNodeRequest(graphState.draft, readToken()))
    graphState.error = ''
    graphState.busy = ''
    await searchGraph()
    await selectGraphNode(graphState.saved.concept_id)
    return
  } catch (err) {
    graphState.saved = null
    graphFail(err)
  }
  graphState.busy = ''
  if (view === 'graph') render()
}

// Mutating the state without re-rendering keeps focus inside a field that the
// operator is still typing in; every fetch below re-renders on completion.
const graphActions = {
  onQuery: (value) => {
    graphState.query = value
  },
  onQuestion: (value) => {
    graphState.question = value
  },
  onType: (value) => {
    graphState.nodeType = value
    searchGraph()
  },
  onDepth: (value) => {
    graphState.depth = value
    if (graphState.subgraph) expandGraph()
    else if (view === 'graph') render()
  },
  onDirection: (value) => {
    graphState.direction = value
    if (graphState.subgraph) expandGraph()
    else if (view === 'graph') render()
  },
  onEdgeType: (type) => {
    const active = graphState.activeEdgeTypes
    graphState.activeEdgeTypes = active.includes(type)
      ? active.filter((item) => item !== type)
      : [...active, type]
    if (graphState.subgraph) expandGraph()
    else if (view === 'graph') render()
  },
  onSearch: searchGraph,
  onSelect: selectGraphNode,
  onExpand: expandGraph,
  onAnswer: checkAnswerability,
  onReindex: reindexGraph,
  onRetry: loadGraph,
  onDraft: (field, value) => {
    graphState.draft[field] = value
  },
  onSave: saveGraphNode,
}

initTheme()
initLang()
onLangChange(() => render())
onThemeChange(() => render())
render()
loadStatus()
if (view === 'graph') loadGraph()
