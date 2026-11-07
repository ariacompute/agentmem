// API documentation view.
//
// Methods, paths, field names, status codes and JSON samples come from
// api-content.js and render verbatim in every locale. Chapter titles, endpoint
// descriptions, column heads and notes come from the api copy namespace.
import { button, codeBlock, el } from '../lib/dom.js'
import { t } from '../lib/i18n.js'
import {
  conventionBlocks,
  docsSections,
  endpointGroups,
  endpoints,
  statusGroups,
} from '../lib/api-content.js'

const READING_LINE = 160
let cleanup = null
let activeId = docsSections[0].id

/** Stops the scroll spy when the shell switches away from the docs view. */
export function cleanupDocs() {
  if (cleanup) cleanup()
  cleanup = null
}

function updateIndex(index) {
  for (const item of index.querySelectorAll('.index-item')) {
    item.classList.toggle('active', item.dataset.section === activeId)
  }
}

function buildIndex() {
  const index = el('aside', 'docs-index')
  index.appendChild(el('div', 'index-title', t('api', 'indexTitle')))
  for (const s of docsSections) {
    const item = button(t('api', s.titleKey), 'index-item', () => {
      document.getElementById(`sec-${s.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    })
    item.dataset.section = s.id
    index.appendChild(item)
  }
  return index
}

function buildOverview() {
  const card = el('section', 'card panel')
  card.id = 'sec-overview'
  card.appendChild(el('h3', null, t('api', 'sectionOverview')))
  card.appendChild(el('p', 'lede', t('api', 'overviewBody')))
  for (const block of conventionBlocks) {
    const wrap = el('div', 'block')
    wrap.appendChild(el('h4', null, t('api', block.titleKey)))
    wrap.appendChild(el('p', null, t('api', block.bodyKey)))
    wrap.appendChild(codeBlock(block.lines.join('\n')))
    card.appendChild(wrap)
  }
  card.appendChild(el('p', 'note', t('api', 'computeNote')))
  card.appendChild(el('p', 'note', t('api', 'limitNote')))
  return card
}

function buildEndpoints(open, onToggle) {
  const card = el('section', 'card panel')
  card.id = 'sec-endpoints'
  card.appendChild(el('h3', null, t('api', 'sectionEndpoints')))
  card.appendChild(el('p', 'lede', t('api', 'endpointsBody')))

  for (const group of endpointGroups) {
    const wrap = el('div', 'endpoint-group')
    wrap.appendChild(el('h4', null, t('api', group.groupKey)))
    const scroll = el('div', 'table-scroll')
    const table = el('table', 'docs-table endpoints')
    const thead = el('thead')
    const headRow = el('tr')
    for (const key of ['colMethod', 'colPath', 'colStatus', 'colDescription']) {
      headRow.appendChild(el('th', null, t('api', key)))
    }
    headRow.appendChild(el('th'))
    thead.appendChild(headRow)
    table.appendChild(thead)

    const tbody = el('tbody')
    for (const ep of endpoints.filter((entry) => entry.group === group.id)) {
      const key = `${ep.method} ${ep.path}`
      const isOpen = Boolean(open[key])
      const row = el('tr', 'endpoint-row')
      row.addEventListener('click', () => onToggle(key))
      const methodCell = el('td')
      methodCell.appendChild(el('span', 'verb mono', ep.method))
      row.appendChild(methodCell)
      row.appendChild(el('td', 'mono', ep.path))
      row.appendChild(el('td', 'mono dim', ep.statuses.join(' / ')))
      row.appendChild(el('td', null, t('api', ep.descKey)))
      const toggleCell = el('td', 'toggle-cell')
      const toggle = button(
        isOpen ? t('api', 'hideExample') : t('api', 'showExample'),
        'btn btn-ghost compact',
        (event) => {
          event.stopPropagation()
          onToggle(key)
        },
      )
      toggleCell.appendChild(toggle)
      row.appendChild(toggleCell)
      tbody.appendChild(row)

      if (isOpen) {
        const detailRow = el('tr', 'endpoint-detail')
        const detailCell = el('td')
        detailCell.colSpan = 5
        const grid = el('div', 'detail')
        const request = el('div', 'detail-block')
        request.appendChild(el('div', 'detail-label', t('api', 'colRequest')))
        request.appendChild(codeBlock(ep.request))
        const response = el('div', 'detail-block')
        response.appendChild(el('div', 'detail-label', t('api', 'colResponse')))
        response.appendChild(codeBlock(ep.response))
        grid.appendChild(request)
        grid.appendChild(response)
        detailCell.appendChild(grid)
        detailRow.appendChild(detailCell)
        tbody.appendChild(detailRow)
      }
    }
    table.appendChild(tbody)
    scroll.appendChild(table)
    wrap.appendChild(scroll)
    card.appendChild(wrap)
  }
  card.appendChild(el('p', 'note', t('api', 'endpointsNote')))
  return card
}

function buildErrors() {
  const card = el('section', 'card panel')
  card.id = 'sec-errors'
  card.appendChild(el('h3', null, t('api', 'sectionErrors')))
  card.appendChild(el('p', 'lede', t('api', 'errorsBody')))
  const scroll = el('div', 'table-scroll')
  const table = el('table', 'docs-table')
  const thead = el('thead')
  const headRow = el('tr')
  for (const key of ['colStatus', 'colMeaning', 'colCode']) {
    headRow.appendChild(el('th', null, t('api', key)))
  }
  thead.appendChild(headRow)
  table.appendChild(thead)
  const tbody = el('tbody')
  for (const group of statusGroups) {
    const row = el('tr')
    row.appendChild(el('td', 'mono', group.status))
    row.appendChild(el('td', null, t('api', group.descKey)))
    row.appendChild(el('td', 'mono dim', group.codes))
    tbody.appendChild(row)
  }
  table.appendChild(tbody)
  scroll.appendChild(table)
  card.appendChild(scroll)
  card.appendChild(el('p', 'note', t('api', 'errorBodyNote')))
  return card
}

export function renderDocs(host, state, actions) {
  cleanupDocs()
  const wrap = el('div', 'docs')
  const body = el('div', 'docs-body')
  const index = buildIndex()
  const content = el('div', 'docs-content')
  content.appendChild(buildOverview())
  content.appendChild(buildEndpoints(state.open, actions.onToggle))
  content.appendChild(buildErrors())
  body.appendChild(index)
  body.appendChild(content)
  wrap.appendChild(body)
  host.appendChild(wrap)

  function onScroll() {
    let current = docsSections[0].id
    for (const s of docsSections) {
      const node = document.getElementById(`sec-${s.id}`)
      if (node && node.getBoundingClientRect().top <= READING_LINE) current = s.id
    }
    activeId = current
    updateIndex(index)
  }
  window.addEventListener('scroll', onScroll, { passive: true })
  cleanup = () => window.removeEventListener('scroll', onScroll)
  onScroll()
}
