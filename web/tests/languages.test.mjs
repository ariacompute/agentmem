import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  LANGUAGES,
  LANG_CODES,
  hreflangFor,
  isRTL,
  isValidLang,
  matchLang,
  ogLocaleFor,
} from '../src/lib/languages.js'

describe('language catalogue', () => {
  it('exposes exactly the same 13 languages as agenthub', () => {
    assert.equal(LANGUAGES.length, 13)
    assert.deepEqual(LANG_CODES, [
      'en',
      'zh',
      'zh-TW',
      'es',
      'fr',
      'de',
      'it',
      'ru',
      'ja',
      'ko',
      'pt',
      'ar',
      'hi',
    ])
  })

  it('marks only Arabic as right-to-left', () => {
    assert.equal(isRTL('ar'), true)
    for (const code of LANG_CODES.filter((value) => value !== 'ar')) {
      assert.equal(isRTL(code), false, `${code} must not flip the layout`)
    }
  })

  it('validates language codes', () => {
    assert.equal(isValidLang('zh-TW'), true)
    assert.equal(isValidLang('nl'), false)
    assert.equal(isValidLang(''), false)
  })

  it('maps raw browser locales onto supported codes', () => {
    assert.equal(matchLang(''), 'en')
    assert.equal(matchLang('en-US'), 'en')
    assert.equal(matchLang('zh-CN'), 'zh')
    assert.equal(matchLang('zh-TW'), 'zh-TW')
    assert.equal(matchLang('zh-Hant-HK'), 'zh-TW')
    assert.equal(matchLang('fr-CA'), 'fr')
    assert.equal(matchLang('nl-NL'), 'en')
  })

  it('exposes hreflang and Open Graph locales', () => {
    assert.equal(hreflangFor('zh'), 'zh-Hans')
    assert.equal(hreflangFor('zh-TW'), 'zh-Hant')
    assert.equal(ogLocaleFor('pt'), 'pt_BR')
    assert.equal(ogLocaleFor('xx'), 'en_US')
  })

  it('passes an unknown code through unchanged', () => {
    assert.equal(hreflangFor('xx'), 'xx')
    assert.equal(hreflangFor(''), '')
  })
})
