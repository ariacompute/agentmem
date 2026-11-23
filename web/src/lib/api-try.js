// Pure request logic for the API documentation's try-it panel.
//
// Nothing here touches the DOM, so the URL building, header shaping, storage
// handling and response formatting are all testable offline. The one rule this
// module exists to enforce: a bearer token only ever travels in the
// Authorization header. It is never appended to a URL, never written to a log
// and never stored outside sessionStorage.

/** Session-scoped key. Closing the tab drops the token. */
export const TOKEN_KEY = 'agentmem.token'

/** Display cap so a huge response cannot freeze the main thread. */
export const MAX_BODY_CHARS = 64 * 1024

/** Header subset worth showing; everything else is noise for a caller. */
export const HEADER_WHITELIST = [
  'content-type',
  'content-length',
  'www-authenticate',
  'retry-after',
]

function storageOf(candidate) {
  if (candidate) return candidate
  try {
    if (typeof window !== 'undefined' && window.sessionStorage) return window.sessionStorage
  } catch (error) {
    // Private mode can throw on access; the panel then simply cannot persist.
    return null
  }
  return null
}

/** Reads the token. An unavailable storage degrades to "no token". */
export function readToken(storage) {
  const store = storageOf(storage)
  if (!store) return ''
  try {
    return store.getItem(TOKEN_KEY) || ''
  } catch (error) {
    return ''
  }
}

/** Stores (or clears) the token and returns the trimmed value. */
export function storeToken(value, storage) {
  const token = typeof value === 'string' ? value.trim() : ''
  const store = storageOf(storage)
  if (!store) return token
  try {
    if (token) store.setItem(TOKEN_KEY, token)
    else store.removeItem(TOKEN_KEY)
  } catch (error) {
    // Quota or a disabled store: keep the in-memory value for this panel.
  }
  return token
}

/** True when the route changes stored state and must be confirmed first. */
export function isWrite(meta) {
  return Boolean(meta && meta.write)
}

/** True while a write route is still unconfirmed. */
export function needsConfirm(meta, confirmed) {
  return isWrite(meta) && !confirmed
}

/** Builds the same-origin path for a request, query samples included. */
export function buildUrl(ep, query) {
  const params = new URLSearchParams()
  for (const [name, value] of Object.entries(query || {})) {
    const text = value === undefined || value === null ? '' : String(value).trim()
    if (text !== '') params.set(name, text)
  }
  const suffix = params.toString()
  return suffix ? `${ep.path}?${suffix}` : ep.path
}

/**
 * Shapes one fetch call. The body of a POST route defaults to the documented
 * sample, so the panel opens on a request that is already valid JSON.
 */
export function buildRequest(ep, draft = {}, token = '') {
  const method = String(ep.method || 'GET').toUpperCase()
  const headers = {}
  if (token) headers.Authorization = `Bearer ${token}`
  const init = {
    method,
    headers,
    credentials: 'omit',
    cache: 'no-store',
    redirect: 'error',
  }
  // POST and PUT both carry a JSON body; GET and DELETE do not.
  if (method === 'POST' || method === 'PUT') {
    headers['Content-Type'] = 'application/json'
    init.body = typeof draft.body === 'string' ? draft.body : ep.request || ''
  }
  return { url: buildUrl(ep, draft.query), init }
}

function parseJSON(text) {
  try {
    return { ok: true, value: JSON.parse(text) }
  } catch (error) {
    return { ok: false, value: null }
  }
}

/** Pretty-prints JSON, keeps anything else verbatim and truncates long bodies. */
export function formatBody(raw, contentType = '') {
  const text = typeof raw === 'string' ? raw : ''
  const parsed = parseJSON(text)
  const isJSON = parsed.ok || /json/i.test(contentType)
  const pretty = parsed.ok ? JSON.stringify(parsed.value, null, 2) : text
  const truncated = pretty.length > MAX_BODY_CHARS
  return {
    text: truncated ? pretty.slice(0, MAX_BODY_CHARS) : pretty,
    truncated,
    json: isJSON,
  }
}

/** Returns the whitelisted response headers, in a stable order. */
export function summarizeHeaders(headers) {
  if (!headers) return []
  let entries = []
  if (typeof headers.forEach === 'function') {
    headers.forEach((value, name) => entries.push([name, value]))
  } else {
    entries = Object.entries(headers)
  }
  const out = []
  for (const name of HEADER_WHITELIST) {
    const hit = entries.find(([key]) => String(key).toLowerCase() === name)
    if (hit) out.push([name, String(hit[1])])
  }
  return out
}

/** Coarse tone used for luminance styling: never a colour. */
export function statusTone(status) {
  if (status < 400) return 'ok'
  if (status < 500) return 'client'
  return 'server'
}

/** Copy key for the hint that belongs under a response, if any. */
export function statusHintKey(status) {
  if (status === 401) return 'tryErrAuth'
  if (status === 403) return 'tryErrForbidden'
  if (status === 429) return 'tryErrRate'
  return ''
}

export function formatDuration(ms) {
  const value = Number.isFinite(ms) ? Math.max(0, ms) : 0
  return `${Math.round(value)} ms`
}
