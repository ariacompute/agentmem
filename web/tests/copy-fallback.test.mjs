import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { LANGUAGES } from '../src/lib/languages.js'
import { detectLang, resolve, STORAGE_KEY } from '../src/lib/i18n.js'
import { shellCopy } from '../src/lib/copy/shell-copy.js'
import { apiCopy } from '../src/lib/copy/api-copy.js'

const PROSE = { shell: shellCopy, api: apiCopy }

describe('copy fallback', () => {
  it('keeps English complete for every key translated elsewhere', () => {
    for (const [name, copy] of Object.entries(PROSE)) {
      for (const [code] of LANGUAGES) {
        for (const key of Object.keys(copy[code] || {})) {
          assert.notEqual(copy.en[key], undefined, `${name}.${code}.${key} lacks an English fallback`)
        }
      }
    }
  })

  it('gives all 13 languages their own translation of every key', () => {
    for (const [name, copy] of Object.entries(PROSE)) {
      for (const [code] of LANGUAGES) {
        if (code === 'en') continue
        const table = resolve(copy, code)
        for (const key of Object.keys(copy.en)) {
          assert.notEqual(table[key], undefined, `${name}.${code}.${key} is missing`)
          assert.notEqual(table[key], copy.en[key], `${name}.${code}.${key} is not translated`)
        }
      }
    }
  })

  it('falls back to English for an unsupported language', () => {
    for (const [name, copy] of Object.entries(PROSE)) {
      const table = resolve(copy, 'nl')
      for (const key of Object.keys(copy.en)) {
        assert.equal(table[key], copy.en[key], `${name}.nl.${key}`)
      }
    }
  })

  it('returns undefined for a missing key instead of throwing', () => {
    for (const [code] of LANGUAGES) {
      assert.equal(resolve(shellCopy, code).thisKeyDoesNotExist, undefined)
    }
  })
})

describe('language detection priority', () => {
  const storage = (values) => ({ getItem: (key) => values[key] ?? null })

  it('prefers a valid query parameter', () => {
    assert.equal(detectLang('?lang=ja', storage({ [STORAGE_KEY]: 'fr' })), 'ja')
  })

  it('ignores an invalid query parameter', () => {
    assert.equal(detectLang('?lang=nl', storage({ [STORAGE_KEY]: 'ko' })), 'ko')
  })

  it('reads the stored choice next', () => {
    assert.equal(detectLang('', storage({ [STORAGE_KEY]: 'ar' })), 'ar')
  })

  it('falls back to English when nothing is known', () => {
    assert.equal(detectLang('', storage({})), 'en')
  })
})
