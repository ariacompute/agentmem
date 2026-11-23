// Graph view: search declared nodes, inspect lineage and edges, expand a
// neighbourhood and check answerability. Machine data (ids, node/edge types,
// origins, warnings and verdicts) renders verbatim; only labels are localised.
import { button, el, kvRow, section } from '../lib/dom.js'
import { t } from '../lib/i18n.js'
import { DEPTHS, DIRECTIONS, coveragePercent, freshness, gapText } from '../lib/api-graph.js'

function localized(key) {
  return t('graph', key)
}

/** Badge copy for the projection freshness indicator. */
const FRESHNESS_KEY = {
  fresh: 'freshLabel',
  stale: 'staleLabel',
  rebuilding: 'rebuildingLabel',
}

const FRESHNESS_KIND = { fresh: '', stale: 'warn', rebuilding: 'muted' }

function badge(text, kind = '') {
  return el('span', `chip ${kind}`.trim(), text)
}

/**
 * Accessible table wrapper: scrollable region with a named caption.
 *
 * Each cell also carries the column name in `data-label`, which the stylesheet
 * turns into an inline label once the wrapper is too narrow for columns.
 */
function table(host, captionId, caption, columns, rows) {
  const wrap = el('div', 'table-wrap')
  wrap.setAttribute('tabindex', '0')
  wrap.setAttribute('role', 'region')
  wrap.setAttribute('aria-labelledby', captionId)
  const node = el('table', 'data-table')
  const cap = el('caption', 'mono', caption)
  cap.id = captionId
  node.appendChild(cap)
  const head = el('thead')
  const headRow = el('tr')
  for (const column of columns) {
    const th = el('th', 'mono', column)
    th.scope = 'col'
    headRow.appendChild(th)
  }
  head.appendChild(headRow)
  node.appendChild(head)
  const body = el('tbody')
  if (rows.length === 0) {
    const tr = el('tr')
    const td = el('td', 'empty', t('graph', 'noResults'))
    td.colSpan = columns.length
    tr.appendChild(td)
    body.appendChild(tr)
  }
  for (const cells of rows) {
    const tr = el('tr')
    cells.forEach((cell, index) => {
      const td = el('td')
      td.dataset.label = columns[index]
      if (cell && cell.node) td.appendChild(cell.node)
      else td.textContent = cell === undefined || cell === null ? '-' : String(cell)
      tr.appendChild(td)
    })
    body.appendChild(tr)
  }
  node.appendChild(body)
  wrap.appendChild(node)
  host.appendChild(wrap)
  return wrap
}

/** Arrow keys move between the row links, Enter activates the focused one. */
function rovingLinks(wrap) {
  const links = wrap.querySelectorAll('.link-btn')
  links.forEach((link, index) => {
    link.addEventListener('keydown', (event) => {
      if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
      if (typeof event.preventDefault === 'function') event.preventDefault()
      const delta = event.key === 'ArrowDown' ? 1 : -1
      const next = links[(index + delta + links.length) % links.length]
      if (next && typeof next.focus === 'function') next.focus()
    })
  })
  return wrap
}

function linkButton(content, onClick) {
  const node = el('button', 'link-btn mono', typeof content === 'string' ? content : null)
  if (typeof content !== 'string') node.appendChild(content)
  node.type = 'button'
  node.addEventListener('click', onClick)
  return node
}

/**
 * Marks the first occurrence of the query inside a label.
 *
 * The match is a `<mark>` for semantics and a solid underline for looks, so the
 * highlight survives a monochrome palette and forced-colors mode.
 */
function highlight(text, query) {
  const raw = text === undefined || text === null ? '' : String(text)
  const needle = String(query || '').trim()
  const span = el('span')
  const at = needle ? raw.toLowerCase().indexOf(needle.toLowerCase()) : -1
  if (at < 0) {
    span.textContent = raw
    return span
  }
  span.appendChild(el('span', null, raw.slice(0, at)))
  span.appendChild(el('mark', 'hit', raw.slice(at, at + needle.length)))
  span.appendChild(el('span', null, raw.slice(at + needle.length)))
  return span
}

/** A pending-work line that screen readers announce without stealing focus. */
function liveLine(text) {
  const node = el('p', 'status-line mono', text)
  node.setAttribute('role', 'status')
  node.setAttribute('aria-live', 'polite')
  return node
}

function progressBar(percent, label) {
  const bar = el('progress', 'meter')
  bar.max = 100
  bar.value = percent
  bar.setAttribute('aria-label', label)
  bar.setAttribute('aria-valuetext', `${percent}%`)
  bar.textContent = `${percent}%`
  return bar
}

export function renderGraph(host, state, actions) {
  const wrap = el('div', 'graph')

  if (state.error) {
    const notice = el('div', 'notice')
    notice.appendChild(el('p', null, state.error))
    notice.appendChild(button(localized('retryAction'), 'btn compact', actions.onRetry))
    wrap.appendChild(notice)
  }

  // 1. Query and index status.
  const query = section(localized('queryTitle'))
  const bar = el('div', 'graph-bar')
  const searchLabel = el('label', 'detail-label', localized('searchPlaceholder'))
  const search = el('input', 'input mono')
  search.type = 'search'
  search.value = state.query
  search.placeholder = localized('searchPlaceholder')
  search.addEventListener('input', (event) => actions.onQuery(event.target.value))
  search.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') actions.onSearch()
  })
  searchLabel.appendChild(search)
  bar.appendChild(searchLabel)

  const typeLabel = el('label', 'detail-label', localized('allTypes'))
  const select = el('select', 'input mono')
  const all = el('option', null, localized('allTypes'))
  all.value = ''
  select.appendChild(all)
  for (const type of state.types) {
    const option = el('option', null, type)
    option.value = type
    if (type === state.nodeType) option.selected = true
    select.appendChild(option)
  }
  select.addEventListener('change', (event) => actions.onType(event.target.value))
  typeLabel.appendChild(select)
  bar.appendChild(typeLabel)

  const searchAction = button(t('shell', 'refresh'), 'btn', actions.onSearch)
  searchAction.disabled = state.busy === 'search'
  bar.appendChild(searchAction)
  const reindex = button(localized('reindexAction'), 'btn', () => {
    if (window.confirm(localized('reindexConfirm'))) actions.onReindex()
  })
  reindex.disabled = state.busy === 'reindex'
  bar.appendChild(reindex)
  query.appendChild(bar)

  const meta = el('div', 'graph-meta')
  const fresh = freshness(state.stats, state.busy)
  meta.appendChild(badge(localized(FRESHNESS_KEY[fresh]), FRESHNESS_KIND[fresh]))
  const stats = el('div', 'kv-grid')
  stats.appendChild(kvRow('nodes', String(state.stats.nodes), true))
  stats.appendChild(kvRow('edges', String(state.stats.edges), true))
  stats.appendChild(kvRow('stale', String(state.stats.stale), true))
  meta.appendChild(stats)
  query.appendChild(meta)
  query.appendChild(
    liveLine(state.busy ? `${localized('statusBusy')} · ${state.busy}` : localized('statusIdle')),
  )
  wrap.appendChild(query)

  // 2. Results and 3. node detail share one row on wide screens.
  const split = el('div', 'graph-split')

  const results = section(`${localized('resultsTitle')} · ${state.hits.length}`)
  const resultsTable = table(
    results,
    'graph-results-caption',
    localized('resultsTitle'),
    ['concept_id', 'type', 'trust'],
    state.hits.map((hit) => [
      {
        node: linkButton(highlight(hit.concept_id, state.query), () =>
          actions.onSelect(hit.concept_id),
        ),
      },
      hit.node_type,
      hit.trust || '-',
    ]),
  )
  rovingLinks(resultsTable)
  split.appendChild(results)

  const detail = section(localized('detailTitle'))
  detail.classList.add('detail-card')
  if (!state.selected) {
    detail.appendChild(el('p', 'empty', localized('selectNode')))
  } else {
    const node = state.selected
    const head = el('div', 'detail-head')
    head.appendChild(el('h3', 'detail-title mono', node.concept_id))
    head.appendChild(badge(node.node_type))
    head.appendChild(badge(node.trust, 'muted'))
    if (node.status) head.appendChild(badge(node.status, 'muted'))
    if (node.stale) head.appendChild(badge(localized('staleLabel'), 'warn'))
    detail.appendChild(head)
    if (node.label && node.label !== node.concept_id) {
      detail.appendChild(el('p', null, node.label))
    }
    if (node.description) detail.appendChild(el('p', null, node.description))
    detail.appendChild(kvRow('visibility', node.visibility, true))
    detail.appendChild(kvRow('content_hash', node.content_hash, true))
    if (node.commit_sha) detail.appendChild(kvRow('commit_sha', node.commit_sha, true))

    const sources = el('div', 'sub-block')
    sources.appendChild(el('h3', 'sub-title', localized('sourcesTitle')))
    if ((node.sources || []).length === 0) sources.appendChild(el('p', 'empty', '-'))
    for (const src of node.sources || []) {
      sources.appendChild(kvRow(src.id || src.title || 'source', src.resource, true))
    }
    detail.appendChild(sources)

    if (node.properties && Object.keys(node.properties).length > 0) {
      const properties = el('div', 'sub-block')
      properties.appendChild(el('h3', 'sub-title', localized('propertiesTitle')))
      for (const [key, value] of Object.entries(node.properties)) {
        properties.appendChild(kvRow(key, value, true))
      }
      detail.appendChild(properties)
    }

    if (node.computation) {
      const computation = el('div', 'sub-block')
      computation.appendChild(el('h3', 'sub-title', localized('computationTitle')))
      computation.appendChild(kvRow(node.computation.runtime, node.computation.computation || '-', true))
      detail.appendChild(computation)
    }

    const edges = el('div', 'sub-block')
    edges.appendChild(el('h3', 'sub-title', localized('edgesTitle')))
    const rows = []
    for (const edge of node.out || []) {
      rows.push([
        '→',
        { node: linkButton(edge.to_id, () => actions.onSelect(edge.to_id)) },
        edge.edge_type,
        edge.origin,
      ])
    }
    for (const edge of node.in || []) {
      rows.push([
        '←',
        { node: linkButton(edge.from_id, () => actions.onSelect(edge.from_id)) },
        edge.edge_type,
        edge.origin,
      ])
    }
    rovingLinks(table(edges, 'graph-edges-caption', localized('edgesTitle'), ['dir', 'target', 'type', 'origin'], rows))
    detail.appendChild(edges)

    const warnings = el('div', 'sub-block')
    warnings.appendChild(el('h3', 'sub-title', localized('warningsLabel')))
    if ((node.warnings || []).length === 0) warnings.appendChild(el('p', 'empty', localized('noGaps')))
    for (const warning of node.warnings || []) warnings.appendChild(el('p', 'mono warn-text', warning))
    detail.appendChild(warnings)
  }
  split.appendChild(detail)
  wrap.appendChild(split)

  // 4. Neighbourhood.
  const neighbours = section(localized('neighborsTitle'))
  const controls = el('div', 'graph-bar')
  const depthLabel = el('label', 'detail-label', 'depth')
  const depth = el('select', 'input mono')
  for (const value of DEPTHS) {
    const option = el('option', null, String(value))
    option.value = String(value)
    if (value === state.depth) option.selected = true
    depth.appendChild(option)
  }
  depth.addEventListener('change', (event) => actions.onDepth(Number(event.target.value)))
  depthLabel.appendChild(depth)
  controls.appendChild(depthLabel)

  const directions = el('div', 'segmented')
  directions.setAttribute('role', 'group')
  directions.setAttribute('aria-label', 'direction')
  for (const value of DIRECTIONS) {
    const active = state.direction === value
    const btn = button(value, `seg${active ? ' active' : ''}`, () => actions.onDirection(value))
    btn.setAttribute('aria-pressed', active ? 'true' : 'false')
    directions.appendChild(btn)
  }
  controls.appendChild(directions)
  const expand = button(localized('neighborsTitle'), 'btn', actions.onExpand)
  expand.disabled = !state.selected || state.busy === 'neighbors'
  controls.appendChild(expand)
  neighbours.appendChild(controls)

  if (state.edgeTypes.length > 0) {
    const filters = el('div', 'chip-row')
    filters.setAttribute('role', 'group')
    filters.setAttribute('aria-label', localized('edgesTitle'))
    for (const type of state.edgeTypes) {
      const active = state.activeEdgeTypes.includes(type)
      const chip = button(type, `chip mono${active ? ' active' : ''}`, () => actions.onEdgeType(type))
      chip.setAttribute('aria-pressed', active ? 'true' : 'false')
      filters.appendChild(chip)
    }
    neighbours.appendChild(filters)
  }

  if (state.subgraph) {
    if (state.subgraph.truncated) {
      neighbours.appendChild(el('p', 'notice', localized('truncatedLabel')))
    }
    const depths = state.subgraph.depths || {}
    neighbours.appendChild(
      table(
        neighbours,
        'graph-neighbours-caption',
        localized('neighborsTitle'),
        ['concept_id', 'type', 'depth'],
        (state.subgraph.nodes || []).map((item) => [
          { node: linkButton(item.concept_id, () => actions.onSelect(item.concept_id)) },
          item.node_type,
          String(depths[item.concept_id] ?? 0),
        ]),
      ),
    )
  }
  wrap.appendChild(neighbours)

  // 5. Answerability.
  const answer = section(localized('answerTitle'))
  const answerBar = el('div', 'graph-bar')
  const questionLabel = el('label', 'detail-label', localized('answerPlaceholder'))
  const question = el('input', 'input')
  question.type = 'search'
  question.value = state.question
  question.placeholder = localized('answerPlaceholder')
  question.addEventListener('input', (event) => actions.onQuestion(event.target.value))
  question.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') actions.onAnswer()
  })
  questionLabel.appendChild(question)
  answerBar.appendChild(questionLabel)
  const check = button(localized('answerAction'), 'btn', actions.onAnswer)
  check.disabled = state.busy === 'answer'
  answerBar.appendChild(check)
  answer.appendChild(answerBar)

  if (state.coverage) {
    const cov = state.coverage
    const percent = coveragePercent(cov.coverage)
    if (cov.verdict === 'insufficient') {
      answer.appendChild(el('p', 'notice', localized('noGaps')))
    }
    answer.appendChild(progressBar(percent, localized('coverageLabel')))
    answer.appendChild(kvRow(localized('coverageLabel'), `${percent}%`, true))
    answer.appendChild(kvRow('verdict', cov.verdict, true))
    answer.appendChild(kvRow('minimum', String(cov.minimum), true))
    const matched = el('div', 'sub-block')
    matched.appendChild(el('h3', 'sub-title', 'matched'))
    if ((cov.matched || []).length === 0) matched.appendChild(el('p', 'empty', '-'))
    for (const term of cov.matched || []) {
      matched.appendChild(kvRow(term.term, `${term.concept_id} (${term.score})`, true))
    }
    answer.appendChild(matched)
    const gaps = el('div', 'sub-block')
    gaps.appendChild(el('h3', 'sub-title', localized('gapsTitle')))
    if ((cov.gaps || []).length === 0) gaps.appendChild(el('p', 'empty', localized('noGaps')))
    for (const gap of cov.gaps || []) gaps.appendChild(el('p', 'mono warn-text', gapText(gap)))
    answer.appendChild(gaps)
  }
  wrap.appendChild(answer)

  // 6. Write.
  const write = section(localized('writeTitle'))
  const form = el('div', 'graph-bar')
  for (const field of ['id', 'node_type', 'label']) {
    const label = el('label', 'detail-label mono', field)
    const input = el('input', 'input mono')
    input.type = 'text'
    input.value = state.draft[field]
    input.addEventListener('input', (event) => actions.onDraft(field, event.target.value))
    label.appendChild(input)
    form.appendChild(label)
  }
  const save = button(localized('saveAction'), 'btn', () => {
    if (state.draft.id && window.confirm(`${localized('saveAction')}: ${state.draft.id}?`)) actions.onSave()
  })
  save.disabled = state.busy === 'save' || !state.draft.id
  form.appendChild(save)
  write.appendChild(form)
  if (state.saved) {
    write.appendChild(
      el('p', 'empty mono', `${state.saved.concept_id} · ${state.saved.content_hash}`),
    )
  }
  wrap.appendChild(write)

  host.appendChild(wrap)
}

/** State factory so main.js keeps one object across re-renders. */
export function initialGraphState() {
  return {
    query: '',
    nodeType: '',
    question: '',
    draft: { id: '', node_type: '', label: '' },
    types: [],
    edgeTypes: [],
    activeEdgeTypes: [],
    stats: { nodes: 0, edges: 0, stale: 0 },
    hits: [],
    selected: null,
    subgraph: null,
    coverage: null,
    saved: null,
    depth: 1,
    direction: 'both',
    busy: '',
    error: '',
    loaded: false,
  }
}
