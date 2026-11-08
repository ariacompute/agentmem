// Template-level console shell: shared top bar, two views, language and theme
// switching. Dependency-free ES module so the page also works without a bundler
// or Node runtime when served straight from web/.
import { LANGUAGES } from './lib/languages.js'
import { getLang, mirrorHtml, onLangChange, setLang, t } from './lib/i18n.js'
import { getTheme, initTheme, onThemeChange, toggleTheme } from './lib/theme.js'
import { button, chip, el } from './lib/dom.js'
import { renderStatus } from './views/status.js'
import { cleanupDocs, renderDocs } from './views/docs.js'

const root = document.getElementById('app')

const statusState = { health: {}, decisions: [], error: '', loaded: false }
const docsState = { open: {} }
const initialView =
  typeof window !== 'undefined' && window.location
    ? new URLSearchParams(window.location.search).get('view')
    : null
let view = initialView === 'docs' ? 'docs' : 'status'
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
  ]) {
    tabs.appendChild(
      button(t('shell', key), `tab${view === id ? ' active' : ''}`, () => {
        view = id
        langMenuOpen = false
        render()
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
  row.appendChild(el('h1', null, view === 'status' ? t('shell', 'statusHeadTitle') : t('api', 'pageTitle')))
  row.appendChild(chip(view === 'status' ? t('shell', 'footerBuild') : t('api', 'versionNote')))
  head.appendChild(row)
  head.appendChild(
    el('p', 'page-sub', view === 'status' ? t('shell', 'statusHeadSub') : t('api', 'pageSub')),
  )
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
  } else {
    renderDocs(host, docsState, {
      onToggle: (key) => {
        docsState.open[key] = !docsState.open[key]
        render()
      },
    })
  }
  window.scrollTo(0, scrollY)
}

async function loadStatus() {
  try {
    const [healthRes, decisionsRes] = await Promise.all([
      fetch('/healthz'),
      fetch('/v1/decisions?limit=20'),
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

initTheme()
mirrorHtml(getLang())
onLangChange(() => render())
onThemeChange(() => render())
render()
loadStatus()
