import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const WEB = fileURLToPath(new URL('../', import.meta.url))
const INDEX = join(WEB, 'index.html')
const html = readFileSync(INDEX, 'utf8')

/**
 * The page is served as plain static files, so the policy travels inside the
 * document as a meta element. Only the parser-blocking theme resolver is
 * inline; everything else, including the module bundle and the stylesheet,
 * must stay same-origin and dependency free.
 */
function csp() {
  const meta = html.match(
    /<meta\s+http-equiv="Content-Security-Policy"\s+content="([^"]*)"/,
  )
  assert.ok(meta, 'index.html has no Content-Security-Policy meta element')
  const directives = {}
  for (const part of meta[1].split(';')) {
    const tokens = part.trim().split(/\s+/).filter(Boolean)
    if (tokens.length > 0) directives[tokens[0]] = tokens.slice(1)
  }
  return { raw: meta[1], directives }
}

function inlineScripts() {
  const out = []
  for (const match of html.matchAll(/<script(\s[^>]*)?>([\s\S]*?)<\/script>/g)) {
    const attributes = match[1] || ''
    if (!/\bsrc\s*=/.test(attributes)) out.push(match[2])
  }
  return out
}

function sha256Token(source) {
  return `'sha256-${createHash('sha256').update(source, 'utf8').digest('base64')}'`
}

describe('content security policy', () => {
  const { directives } = csp()

  it('falls back to deny-all for anything not named', () => {
    assert.deepEqual(directives['default-src'], ["'none'"])
    assert.deepEqual(directives['object-src'], ["'none'"])
    assert.deepEqual(directives['base-uri'], ["'none'"])
    assert.deepEqual(directives['form-action'], ["'none'"])
  })

  it('loads scripts, styles and API calls from the same origin only', () => {
    assert.equal(directives['script-src'][0], "'self'")
    assert.deepEqual(directives['style-src'], ["'self'"])
    assert.deepEqual(directives['img-src'], ["'self'"])
    // The Vite dev proxy keeps /healthz and /v1 on the page origin, so the
    // console never needs a cross-origin connect-src entry.
    assert.deepEqual(directives['connect-src'], ["'self'"])
  })

  it('admits the inline theme bootstrap by its exact digest', () => {
    const scripts = inlineScripts()
    assert.equal(scripts.length, 1, 'more than one inline script needs a review')
    const token = sha256Token(scripts[0])
    assert.ok(
      directives['script-src'].includes(token),
      `CSP must allow ${token} for the inline theme script`,
    )
    assert.equal(directives['script-src'].includes("'unsafe-inline'"), false)
    assert.equal(directives['script-src'].includes("'unsafe-eval'"), false)
    assert.equal(directives['style-src'].includes("'unsafe-inline'"), false)
  })

  it('declares no external asset and no inline event handler', () => {
    assert.equal(/(src|href)="(https?:)?\/\//i.test(html), false, 'external asset in index.html')
    assert.equal(/\son[a-z]+\s*=\s*["']/i.test(html), false, 'inline event handler in index.html')
    assert.equal(html.includes('<style'), false, 'styles belong in style.css')
  })
})
