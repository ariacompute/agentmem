// Status view: health and recent routing decisions.
// The machine-shaped values (ids, rule ids, targets, confidence) stay verbatim;
// only labels, empty states and error text are localised.
import { el, kvRow, section } from '../lib/dom.js'
import { t } from '../lib/i18n.js'

export function renderStatus(host, state) {
  const health = section(t('shell', 'healthTitle'))
  if (state.error) health.appendChild(el('p', 'notice', state.error))
  const entries = Object.entries(state.health || {})
  if (!state.loaded) {
    health.appendChild(el('p', 'empty', t('shell', 'loading')))
  } else if (entries.length === 0) {
    health.appendChild(el('p', 'empty', t('shell', 'notLoaded')))
  } else {
    for (const [key, value] of entries) health.appendChild(kvRow(key, value, true))
  }
  host.appendChild(health)

  const decisions = el('section', 'card')
  const heading = el('h2', 'card-title', t('shell', 'decisionsTitle'))
  heading.appendChild(el('span', 'count', String((state.decisions || []).length)))
  decisions.appendChild(heading)
  if ((state.decisions || []).length === 0) {
    decisions.appendChild(el('p', 'empty', t('shell', 'noDecisions')))
  } else {
    for (const d of state.decisions) {
      const row = el('div', 'kv')
      row.appendChild(el('b', null, d.concept_id || '(query)'))
      const detail = el('span')
      for (const part of [
        `${d.rule_id || '-'} · ${d.source || '-'} · `,
        (d.targets || []).join(', '),
        ` · confidence=${d.confidence ?? '-'}`,
      ]) {
        detail.appendChild(document.createTextNode(part))
      }
      row.appendChild(detail)
      decisions.appendChild(row)
    }
  }
  host.appendChild(decisions)
}
