// Locale resolution for agentmem, mirroring agenthub's use-copy.js.
//
// Priority: ?lang= > localStorage > matchLang(navigator.language) > en.
// Resolution is a shallow merge over the English block plus English fallback,
// so a language with a missing key degrades to readable copy instead of a blank
// page. Resolved dictionaries are memoized per namespace and language.
import { isRTL, isValidLang, matchLang } from './languages.js'
import { shellCopy } from './copy/shell-copy.js'
import { apiCopy } from './copy/api-copy.js'

export const STORAGE_KEY = 'agentmem.lang'

const COPIES = {
  shell: shellCopy,
  api: apiCopy,
}

/** Shallow merge with English fallback. Exported for copy-fallback tests. */
export function resolve(copy, lang) {
  return { ...copy.en, ...(copy[lang] || {}) }
}

/** Reads the requested language from the URL, storage or the browser. */
export function detectLang(search, storage) {
  const fromQuery = search ? new URLSearchParams(search).get('lang') : null
  if (fromQuery && isValidLang(fromQuery)) return fromQuery
  if (storage) {
    const stored = storage.getItem(STORAGE_KEY)
    if (stored && isValidLang(stored)) return stored
  }
  if (typeof navigator !== 'undefined' && navigator.language) {
    return matchLang(navigator.language)
  }
  return 'en'
}

let langRef = detectLang(
  typeof window !== 'undefined' ? window.location.search : '',
  typeof window !== 'undefined' ? window.localStorage : null,
)

const listeners = new Set()
const cache = new Map()

function mirrorHtml(lang) {
  if (typeof document === 'undefined') return
  document.documentElement.lang = lang
  document.documentElement.dir = isRTL(lang) ? 'rtl' : 'ltr'
}

export function getLang() {
  return langRef
}

/** Applies a language: persists it, mirrors <html lang|dir>, notifies views. */
export function setLang(next) {
  const lang = isValidLang(next) ? next : 'en'
  langRef = lang
  cache.clear()
  if (typeof window !== 'undefined') {
    window.localStorage.setItem(STORAGE_KEY, lang)
  }
  mirrorHtml(lang)
  for (const fn of listeners) fn(lang)
  return lang
}

export function onLangChange(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** Translation helper bound to the current language. */
export function t(namespace, key) {
  const key0 = `${namespace}:${langRef}`
  if (!cache.has(key0)) cache.set(key0, resolve(COPIES[namespace] || {}, langRef))
  return cache.get(key0)[key]
}

mirrorHtml(langRef)
