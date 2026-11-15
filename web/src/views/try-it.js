// Inline trial workbench for one documented endpoint.
//
// The panel is built inside the expanded table row and updates only its own
// subtree: a send never re-renders the page, so the request text and the caret
// survive. Drafts are handed back through `onDraft` and replayed on the next
// build, which is what keeps them alive across the shell's full re-render.
//
// The token lives in sessionStorage (see api-try.js) and is read at send time;
// it is never copied into the draft, the URL or a rendered text node.
import { button, codeBlock, el } from '../lib/dom.js'
import { t } from '../lib/i18n.js'
import {
  buildRequest,
  formatBody,
  formatDuration,
  isWrite,
  needsConfirm,
  readToken,
  statusHintKey,
  statusTone,
  storeToken,
  summarizeHeaders,
} from '../lib/api-try.js'

/** Live token fields, so typing in one panel updates the others. */
const tokenFields = new Set()

/** Drops the token-field registry when the docs view is torn down. */
export function cleanupTryIt() {
  tokenFields.clear()
}

function syncToken(value, origin) {
  const label = value ? t('tryit', 'tryTokenSet') : t('tryit', 'tryTokenUnset')
  for (const field of tokenFields) {
    if (field.input === origin) continue
    if (field.input.value !== value) field.input.value = value
    field.badge.textContent = label
  }
}

function now() {
  if (typeof performance !== 'undefined' && typeof performance.now === 'function') {
    return performance.now()
  }
  return Date.now()
}

/** Builds the workbench for `ep`; `meta` comes from api-content.tryMeta. */
export function buildTryIt(ep, meta, draft = {}, onDraft = () => {}) {
  const state = {
    query: { ...((meta && meta.query) || {}), ...(draft.query || {}) },
    body: typeof draft.body === 'string' ? draft.body : ep.method === 'POST' ? ep.request : '',
    confirmed: Boolean(draft.confirmed),
  }
  const write = isWrite(meta)
  const inputs = []
  let pending = false

  const section = el('section', 'try')
  const head = el('div', 'try-head')
  head.appendChild(el('span', 'try-title', t('tryit', 'tryTitle')))

  const cred = el('div', 'try-cred')
  const tokenInput = document.createElement('input')
  tokenInput.type = 'password'
  tokenInput.className = 'input mono try-token'
  tokenInput.autocomplete = 'off'
  tokenInput.spellcheck = false
  tokenInput.placeholder = t('tryit', 'tryToken')
  tokenInput.setAttribute('aria-label', t('tryit', 'tryToken'))
  tokenInput.value = readToken()
  const badge = el(
    'span',
    'chip try-badge',
    tokenInput.value ? t('tryit', 'tryTokenSet') : t('tryit', 'tryTokenUnset'),
  )
  tokenInput.addEventListener('input', () => {
    const value = storeToken(tokenInput.value)
    syncToken(value, tokenInput)
    badge.textContent = value ? t('tryit', 'tryTokenSet') : t('tryit', 'tryTokenUnset')
  })
  tokenFields.add({ input: tokenInput, badge })
  cred.appendChild(tokenInput)
  cred.appendChild(badge)
  head.appendChild(cred)
  head.appendChild(el('p', 'note try-hint', t('tryit', 'tryTokenHint')))
  section.appendChild(head)

  const grid = el('div', 'try-grid')

  // --- request column ------------------------------------------------------
  const request = el('div', 'try-req')
  if (write) {
    const warn = el('div', 'try-write')
    warn.appendChild(el('span', 'try-write-prefix', 'WRITE'))
    warn.appendChild(el('span', null, t('tryit', 'tryWriteWarn')))
    request.appendChild(warn)
  }

  const queryKeys = Object.keys((meta && meta.query) || {})
  if (queryKeys.length > 0) {
    request.appendChild(el('div', 'detail-label', t('tryit', 'tryQuery')))
    const fields = el('div', 'try-fields')
    for (const name of queryKeys) {
      const row = el('label', 'try-field')
      row.appendChild(el('span', 'try-key mono', name))
      const input = document.createElement('input')
      input.className = 'input mono'
      input.dataset.name = name
      input.value = state.query[name] === undefined ? '' : String(state.query[name])
      input.addEventListener('input', () => {
        state.query[name] = input.value
        push()
      })
      inputs.push(input)
      row.appendChild(input)
      fields.appendChild(row)
    }
    request.appendChild(fields)
  }

  if (ep.method === 'POST') {
    request.appendChild(el('div', 'detail-label', t('tryit', 'tryBody')))
    const area = document.createElement('textarea')
    area.className = 'input mono try-area'
    area.spellcheck = false
    area.rows = 8
    area.value = state.body
    area.setAttribute('aria-label', t('tryit', 'tryBody'))
    area.addEventListener('input', () => {
      state.body = area.value
      push()
    })
    area.addEventListener('keydown', (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
        event.preventDefault()
        run()
      }
    })
    inputs.push(area)
    request.appendChild(area)
  }

  const actions = el('div', 'try-actions')
  const send = button(t('tryit', 'trySend'), 'btn try-send', run)
  const reset = button(t('tryit', 'tryReset'), 'btn btn-ghost compact', restore)
  actions.appendChild(send)
  actions.appendChild(reset)

  let confirmBox = null
  if (write) {
    const confirm = el('label', 'try-confirm')
    confirmBox = document.createElement('input')
    confirmBox.type = 'checkbox'
    confirmBox.checked = state.confirmed
    confirmBox.addEventListener('change', () => {
      state.confirmed = confirmBox.checked
      push()
      refreshSend()
    })
    confirm.appendChild(confirmBox)
    confirm.appendChild(el('span', null, t('tryit', 'tryConfirmWrite')))
    actions.appendChild(confirm)
  }
  request.appendChild(actions)
  grid.appendChild(request)
  // A GET without query parameters has nothing to edit: one column keeps the
  // response wide instead of leaving half the row empty.
  if (queryKeys.length === 0 && ep.method !== 'POST') grid.classList.add('is-simple')

  // --- result column -------------------------------------------------------
  const result = el('div', 'try-res')
  const out = el('div', 'try-out')
  out.setAttribute('aria-live', 'polite')
  result.appendChild(out)
  grid.appendChild(result)
  section.appendChild(grid)

  clearOut()
  refreshSend()

  function push() {
    onDraft({ query: { ...state.query }, body: state.body, confirmed: state.confirmed })
  }

  function refreshSend() {
    const blocked = pending || needsConfirm(meta, state.confirmed)
    send.disabled = blocked
    send.setAttribute('title', blocked && !pending ? t('tryit', 'tryConfirmWrite') : '')
  }

  function clearOut() {
    out.classList.remove('has-result')
    out.replaceChildren(el('p', 'note try-empty', t('tryit', 'tryEmpty')))
  }

  function restore() {
    state.query = { ...((meta && meta.query) || {}) }
    state.body = ep.method === 'POST' ? ep.request : ''
    state.confirmed = false
    if (confirmBox) confirmBox.checked = false
    for (const input of inputs) {
      if (input.tagName === 'TEXTAREA') input.value = state.body
      else {
        const name = input.dataset.name
        input.value = state.query[name] === undefined ? '' : String(state.query[name])
      }
    }
    clearOut()
    push()
    refreshSend()
  }

  function renderNetworkError() {
    out.classList.add('has-result')
    out.replaceChildren(el('p', 'try-error', t('tryit', 'tryErrNetwork')))
  }

  function renderResult(request, response, raw, ms) {
    const contentType = response.headers.get('content-type') || ''
    const body = formatBody(raw, contentType)
    const summary = el('div', 'try-sum')
    summary.appendChild(el('span', `try-status is-${statusTone(response.status)}`, response.status))
    summary.appendChild(el('span', 'mono dim', `${request.init.method} ${request.url}`))
    summary.appendChild(el('span', 'note', formatDuration(ms)))
    if (contentType) summary.appendChild(el('span', 'mono dim', contentType))

    const nodes = [summary]
    const headers = summarizeHeaders(response.headers)
    if (headers.length > 0) {
      nodes.push(el('div', 'detail-label', t('tryit', 'tryHeaders')))
      const table = el('div', 'try-headers')
      for (const [name, value] of headers) {
        const row = el('div', 'kv')
        row.appendChild(el('b', null, name))
        row.appendChild(el('span', 'mono', value))
        table.appendChild(row)
      }
      nodes.push(table)
    }
    nodes.push(el('div', 'detail-label', t('tryit', 'tryResponse')))
    nodes.push(codeBlock(body.text))
    if (body.truncated) nodes.push(el('p', 'note', t('tryit', 'tryTruncated')))
    const hintKey = statusHintKey(response.status)
    if (hintKey) nodes.push(el('p', 'try-error', t('tryit', hintKey)))

    out.classList.add('has-result')
    out.replaceChildren(...nodes)
  }

  async function run() {
    if (pending || needsConfirm(meta, state.confirmed)) return
    push()
    pending = true
    send.textContent = t('tryit', 'trySending')
    send.setAttribute('aria-busy', 'true')
    refreshSend()
    const started = now()
    const request = buildRequest(ep, { query: state.query, body: state.body }, readToken())
    try {
      const response = await fetch(request.url, request.init)
      const raw = await response.text()
      renderResult(request, response, raw, now() - started)
    } catch (error) {
      // The error object can carry the request URL; only the fixed class shows.
      renderNetworkError()
    } finally {
      pending = false
      send.textContent = t('tryit', 'trySend')
      send.setAttribute('aria-busy', 'false')
      refreshSend()
    }
  }

  return section
}
