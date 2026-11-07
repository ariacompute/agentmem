import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const WEB = fileURLToPath(new URL('../', import.meta.url))
const SRC = join(WEB, 'src')
const INDEX = join(WEB, 'index.html')

// Any hue word here would mean the monochrome design has started to drift.
const HUE_WORDS = [
  'blue',
  'purple',
  'violet',
  'cyan',
  'teal',
  'green',
  'yellow',
  'red',
  'orange',
  'pink',
  'magenta',
  'indigo',
  'lime',
  'amber',
  'turquoise',
  'navy',
  'maroon',
  'olive',
  'crimson',
  'salmon',
  'gold',
  'bronze',
]

function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) out.push(...walk(path))
    else if (/\.(js|css)$/.test(entry)) out.push(path)
  }
  return out
}

const FILES = [...walk(SRC), INDEX]

function hexSamples(source) {
  const samples = []
  const re = /#([0-9a-fA-F]{3,8})\b/g
  let match
  while ((match = re.exec(source))) samples.push(match[1])
  return samples
}

function channelsOf(hex) {
  if (hex.length === 3 || hex.length === 4) {
    return hex.slice(0, 3).split('').map((char) => parseInt(char + char, 16))
  }
  return [hex.slice(0, 2), hex.slice(2, 4), hex.slice(4, 6)].map((part) => parseInt(part, 16))
}

describe('monochrome guard', () => {
  it('scans the whole source tree and the document', () => {
    assert.ok(FILES.length > 8, `only ${FILES.length} files scanned`)
  })

  it('uses only grey hex colours (R === G === B)', () => {
    for (const file of FILES) {
      for (const hex of hexSamples(readFileSync(file, 'utf8'))) {
        const [r, g, b] = channelsOf(hex)
        assert.equal(r, g, `${file}: #${hex} is not grey`)
        assert.equal(g, b, `${file}: #${hex} is not grey`)
      }
    }
  })

  it('uses only neutral rgb()/rgba() channels', () => {
    const re = /(?:rgba?|hsla?)\(([^)]*)\)/g
    for (const file of FILES) {
      const source = readFileSync(file, 'utf8')
      let match
      while ((match = re.exec(source))) {
        const parts = match[1].split(',').map((part) => parseFloat(part))
        if (parts.length < 3 || parts.slice(0, 3).some(Number.isNaN)) continue
        const [a, b, c] = parts.slice(0, 3)
        const neutral = a === b && b === c
        const ends = (a === 0 || a === 255) && (b === 0 || b === 255) && (c === 0 || c === 255)
        assert.ok(neutral || ends, `${file}: ${match[0]} is not neutral`)
      }
    }
  })

  it('never mentions a hue word', () => {
    for (const file of FILES) {
      const source = readFileSync(file, 'utf8').toLowerCase()
      for (const word of HUE_WORDS) {
        assert.equal(new RegExp(`\\b${word}\\b`).test(source), false, `${file} mentions "${word}"`)
      }
    }
  })
})

describe('light and dark themes', () => {
  const css = readFileSync(join(SRC, 'style.css'), 'utf8')

  it('defines both themes on the same grey token set', () => {
    assert.match(css, /\[data-theme='light'\]/)
    assert.match(css, /color-scheme:\s*dark/)
    assert.match(css, /color-scheme:\s*light/)
    for (const token of ['--bg-base', '--bg-surface', '--text-1', '--accent', '--code-bg']) {
      assert.ok(css.includes(token), `${token} is missing`)
    }
  })

  it('resolves the theme before the first paint', () => {
    const html = readFileSync(INDEX, 'utf8')
    assert.match(html, /dataset\.theme/)
    assert.match(html, /prefers-color-scheme/)
    assert.match(html, /agentmem\.theme/)
    assert.match(html, /rel="stylesheet" href="\.\/src\/style\.css"/)
    assert.equal(html.includes('<style'), false, 'styles must live in style.css, not inline')
  })
})
