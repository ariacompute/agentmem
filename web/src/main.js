// Template-level status page: health + routing decision list.
// Dependency-free ES module so it works without a bundler or Node runtime.

const root = document.getElementById('app')

function el(tag, className, text) {
  const node = document.createElement(tag)
  if (className) node.className = className
  if (text !== undefined) node.textContent = String(text)
  return node
}

function section(title) {
  const box = el('section')
  box.appendChild(el('h2', null, title))
  return box
}

function render(state) {
  root.replaceChildren()
  const main = el('main')
  main.appendChild(el('h1', null, 'agentmem'))
  main.appendChild(el('p', 'sub', 'OKF+Git 权威正文 · PostgreSQL/GORM 结构化数据 · 智能路由'))
  if (state.error) main.appendChild(el('p', 'err', state.error))

  const health = section('服务健康')
  const entries = Object.entries(state.health)
  if (entries.length === 0) {
    health.appendChild(el('p', 'empty', '尚未加载'))
  } else {
    for (const [key, value] of entries) {
      const row = el('div', 'kv')
      row.appendChild(el('b', null, key))
      row.appendChild(el('span', null, value))
      health.appendChild(row)
    }
  }
  main.appendChild(health)

  const decisions = section(`最近路由决策 (${state.decisions.length})`)
  if (state.decisions.length === 0) {
    decisions.appendChild(el('p', 'empty', '暂无决策记录'))
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
  const refresh = el('button', null, '刷新')
  refresh.style.marginTop = '12px'
  refresh.style.padding = '6px 12px'
  refresh.style.cursor = 'pointer'
  refresh.addEventListener('click', load)
  decisions.appendChild(refresh)
  main.appendChild(decisions)
  root.appendChild(main)
}

async function load() {
  const state = { health: {}, decisions: [], error: '' }
  try {
    const [healthRes, decisionsRes] = await Promise.all([
      fetch('/healthz'),
      fetch('/v1/decisions?limit=20'),
    ])
    if (!healthRes.ok || !decisionsRes.ok) throw new Error('API 返回非 2xx')
    state.health = await healthRes.json()
    state.decisions = (await decisionsRes.json()) || []
  } catch (err) {
    state.error = '无法连接 agentmem API：' + err.message
  }
  render(state)
}

render({ health: {}, decisions: [], error: '' })
load()
