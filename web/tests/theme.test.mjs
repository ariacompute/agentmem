import assert from 'node:assert/strict'
import { afterEach, beforeEach, describe, it } from 'node:test'
import {
  STORAGE_KEY,
  getTheme,
  isValidTheme,
  nextTheme,
  resolveTheme,
  setTheme,
} from '../src/lib/theme.js'

// Only the storage is swapped in. Test runners that share one process may
// already have a window/document stub from the shell smoke test; replacing
// those wholesale would leave its render listeners pointing at an incomplete
// environment.
let savedStorage
let stored
let dataset

beforeEach(() => {
  stored = new Map()
  if (typeof globalThis.window === 'undefined') globalThis.window = {}
  savedStorage = globalThis.window.localStorage
  globalThis.window.localStorage = {
    getItem: (key) => stored.get(key) ?? null,
    setItem: (key, value) => stored.set(key, value),
  }
  if (typeof globalThis.document === 'undefined') globalThis.document = {}
  if (!globalThis.document.documentElement) globalThis.document.documentElement = {}
  if (!globalThis.document.documentElement.dataset) globalThis.document.documentElement.dataset = {}
  dataset = globalThis.document.documentElement.dataset
})

afterEach(() => {
  if (savedStorage === undefined) delete globalThis.window.localStorage
  else globalThis.window.localStorage = savedStorage
})

describe('theme resolution', () => {
  it('lets a stored choice outrank the OS', () => {
    assert.equal(resolveTheme('light', false), 'light')
    assert.equal(resolveTheme('dark', true), 'dark')
  })

  it('follows the OS when nothing is stored', () => {
    assert.equal(resolveTheme(null, true), 'light')
    assert.equal(resolveTheme(null, false), 'dark')
    assert.equal(resolveTheme('unknown', true), 'light')
  })

  it('validates and toggles themes', () => {
    assert.equal(isValidTheme('light'), true)
    assert.equal(isValidTheme('dark'), true)
    assert.equal(isValidTheme('solar'), false)
    assert.equal(nextTheme('light'), 'dark')
    assert.equal(nextTheme('dark'), 'light')
  })
})

describe('theme persistence', () => {
  it('writes storage only for a deliberate choice', () => {
    setTheme('light', false)
    assert.equal(dataset.theme, 'light')
    assert.equal(stored.has(STORAGE_KEY), false, 'following the OS must not persist')

    setTheme('dark')
    assert.equal(stored.get(STORAGE_KEY), 'dark')
    assert.equal(dataset.theme, 'dark')
    assert.equal(getTheme(), 'dark')
  })
})
