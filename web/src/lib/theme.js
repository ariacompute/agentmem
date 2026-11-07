// Light/dark theme resolution, mirroring agenthub's ThemeToggle.vue.
//
// A stored choice outranks the OS. With none, the page follows the OS live and
// writes nothing, so a preference that flips at dusk is not frozen at what it
// was when the page loaded. Only a deliberate choice is persisted.
export const STORAGE_KEY = 'agentmem.theme'

const VALID = ['light', 'dark']

export function isValidTheme(value) {
  return VALID.includes(value)
}

/** Pure resolver: stored choice > OS preference > dark. */
export function resolveTheme(stored, prefersLight) {
  if (isValidTheme(stored)) return stored
  return prefersLight ? 'light' : 'dark'
}

/** The next theme for a toggle. */
export function nextTheme(current) {
  return current === 'light' ? 'dark' : 'light'
}

function readStored() {
  if (typeof window === 'undefined' || !window.localStorage) return null
  try {
    return window.localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

function prefersLight() {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  try {
    return window.matchMedia('(prefers-color-scheme: light)').matches
  } catch {
    return false
  }
}

function mediaQuery() {
  if (typeof window === 'undefined' || !window.matchMedia) return null
  try {
    return window.matchMedia('(prefers-color-scheme: light)')
  } catch {
    return null
  }
}

let themeRef = 'dark'
const listeners = new Set()

function apply(next, persist) {
  themeRef = next
  if (typeof document !== 'undefined' && document.documentElement) {
    document.documentElement.dataset.theme = next
  }
  if (persist && typeof window !== 'undefined' && window.localStorage) {
    try {
      window.localStorage.setItem(STORAGE_KEY, next)
    } catch {
      // Storage may be unavailable; the theme still applies for this session.
    }
  }
  for (const fn of listeners) fn(next)
  return next
}

/** Applies an explicit choice and persists it. */
export function setTheme(next, persist = true) {
  return apply(isValidTheme(next) ? next : 'dark', persist)
}

export function toggleTheme() {
  return setTheme(nextTheme(themeRef))
}

export function getTheme() {
  return themeRef
}

export function onThemeChange(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/**
 * Called once at boot. The pre-paint script in index.html has already written
 * data-theme, so this only adopts that value and starts following the OS when
 * there is no stored choice. It deliberately does not persist.
 */
export function initTheme() {
  const fromDoc =
    typeof document !== 'undefined' && document.documentElement
      ? document.documentElement.dataset.theme
      : null
  themeRef = isValidTheme(fromDoc) ? fromDoc : resolveTheme(readStored(), prefersLight())
  apply(themeRef, false)
  const media = mediaQuery()
  if (media && typeof media.addEventListener === 'function') {
    media.addEventListener('change', () => {
      if (!readStored()) apply(resolveTheme(null, prefersLight()), false)
    })
  }
  return themeRef
}
