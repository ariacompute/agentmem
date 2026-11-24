// Status snapshot loader. The two probes are independent: /healthz is public
// and defines service health, while /v1/decisions needs a bearer token. A
// missing or rejected token must not turn a healthy service into a failed card.
import { t } from '../lib/i18n.js'

const HEALTH_PATH = '/healthz'
const DECISIONS_PATH = '/v1/decisions?limit=20'
const TOKEN_STATUSES = new Set([401, 403])

async function readJson(fetchImpl, path, init) {
  const response = await fetchImpl(path, init)
  if (!response.ok) {
    const error = new Error(t('shell', 'badStatus'))
    error.status = response.status
    throw error
  }
  return (await response.json()) || {}
}

function messageOf(reason) {
  return t('shell', 'apiError') + (reason && reason.message ? reason.message : '')
}

/**
 * Reads health and recent decisions with allSettled so one failure cannot hide
 * the other result. The returned strings are already localised; the caller
 * only decides which card displays them.
 */
export async function loadStatusSnapshot(fetchImpl = globalThis.fetch, token = '') {
  const authorized = token ? { headers: { Authorization: `Bearer ${token}` } } : undefined
  const [healthResult, decisionsResult] = await Promise.allSettled([
    readJson(fetchImpl, HEALTH_PATH),
    readJson(fetchImpl, DECISIONS_PATH, authorized),
  ])

  const health = healthResult.status === 'fulfilled' ? healthResult.value : {}
  const decisions =
    decisionsResult.status === 'fulfilled' && Array.isArray(decisionsResult.value)
      ? decisionsResult.value
      : []

  const healthError = healthResult.status === 'rejected' ? messageOf(healthResult.reason) : ''
  let decisionsError = ''
  if (decisionsResult.status === 'rejected') {
    const reason = decisionsResult.reason
    decisionsError = TOKEN_STATUSES.has(reason && reason.status)
      ? t('shell', 'decisionsAuth')
      : messageOf(reason)
  }

  return { health, decisions, healthError, decisionsError, loaded: true }
}
