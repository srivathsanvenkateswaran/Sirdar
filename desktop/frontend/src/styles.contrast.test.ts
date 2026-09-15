import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/*
 * The palette, read out of the stylesheet and checked against WCAG.
 *
 * The dim ink is the one text colour that is set apart from the body colour on
 * purpose, and it is the colour `--muted` resolves to for every panel. It used
 * to sit at 3.1:1 in the light theme and 3.5:1 in the dark one, which is what
 * made Register rows, Eval values and the Settings headings hard to read; the
 * panels made it worse by falling back to a light-theme literal in the dark
 * theme. Both are checked here, against every surface a muted line is painted
 * on, because the failure is invisible to a type checker and to every render
 * test in this suite.
 */

/** Vitest serves the module over its own transform pipeline, so `import.meta`
 * carries no file path; the stylesheets are read from the project root, which
 * is where the runner's working directory is. */
function css(path: string): string {
  return readFileSync(resolve(process.cwd(), 'src', path), 'utf8')
}

const CSS = css('styles.css')
const PANELS = css('components/panels.css')

/** The declarations of one `{ … }` block, by custom-property name. */
function block(source: string, selector: string): Record<string, string> {
  const at = source.indexOf(selector)
  if (at < 0) throw new Error(`no block for ${selector}`)
  const open = source.indexOf('{', at)
  let depth = 0
  let end = open
  for (let i = open; i < source.length; i += 1) {
    if (source[i] === '{') depth += 1
    if (source[i] === '}') {
      depth -= 1
      if (depth === 0) {
        end = i
        break
      }
    }
  }
  const out: Record<string, string> = {}
  for (const line of source.slice(open + 1, end).split(';')) {
    const match = /(--[a-z0-9-]+)\s*:\s*([^;]+)/i.exec(line)
    if (match) out[match[1]] = match[2].trim()
  }
  return out
}

function channel(v: number): number {
  const s = v / 255
  return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}

export function luminance(hex: string): number {
  const m = /^#([0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) throw new Error(`not a six-digit hex colour: ${hex}`)
  const n = parseInt(m[1], 16)
  return (
    0.2126 * channel((n >> 16) & 255) +
    0.7152 * channel((n >> 8) & 255) +
    0.0722 * channel(n & 255)
  )
}

export function contrast(fg: string, bg: string): number {
  const a = luminance(fg)
  const b = luminance(bg)
  const [hi, lo] = a > b ? [a, b] : [b, a]
  return (hi + 0.05) / (lo + 0.05)
}

/** Every background a line of body or muted text is painted on. */
const SURFACES = ['--paper', '--surface', '--lane'] as const

const THEMES = {
  light: ':root {',
  // The media-query theme and the explicitly chosen one must stay in step;
  // each is checked in its own right rather than assumed equal.
  'dark (system)': ':root:not([data-theme="light"])',
  'dark (chosen)': ':root[data-theme="dark"]',
} as const

describe('palette contrast', () => {
  it('is sanity-checked against the two ends of the scale', () => {
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 1)
    expect(contrast('#767676', '#ffffff')).toBeGreaterThanOrEqual(4.5)
  })

  for (const [theme, selector] of Object.entries(THEMES)) {
    const tokens = block(CSS, selector)

    it(`${theme}: the dim ink clears 4.5:1 on every surface`, () => {
      for (const surface of SURFACES) {
        const ratio = contrast(tokens['--ink-3'], tokens[surface])
        expect(
          ratio,
          `--ink-3 ${tokens['--ink-3']} on ${surface} ${tokens[surface]} is ${ratio.toFixed(2)}:1`,
        ).toBeGreaterThanOrEqual(4.5)
      }
    })

    it(`${theme}: body and secondary ink clear 4.5:1 on every surface`, () => {
      for (const token of ['--ink', '--ink-2'] as const) {
        for (const surface of SURFACES) {
          expect(
            contrast(tokens[token], tokens[surface]),
            `${token} on ${surface}`,
          ).toBeGreaterThanOrEqual(4.5)
        }
      }
    })

    it(`${theme}: the dim ink is neither the body ink nor a background`, () => {
      expect(tokens['--ink-3']).not.toBe(tokens['--ink'])
      for (const surface of SURFACES) expect(tokens['--ink-3']).not.toBe(tokens[surface])
    })
  }
})

/*
 * The panels were the visible half of the bug: they read --bg/--fg/--muted/
 * --border, nothing defined those names, and so every one of them used the
 * light-theme literal written as its fallback — in the dark theme, #16181d
 * text on #131418 paper. The aliases must stay defined, and must stay
 * expressed as references so they follow the palette into the dark theme
 * rather than pinning a light value.
 */
describe('panel aliases', () => {
  const root = block(CSS, ':root {')

  it.each(['--bg', '--fg', '--muted', '--border', '--line'])('defines %s', (name) => {
    expect(root[name], `${name} is not defined in :root`).toBeTruthy()
    expect(root[name]).toMatch(/^var\(--[a-z0-9-]+\)$/)
  })

  it('resolves --muted to the dim ink, which every theme redefines', () => {
    expect(root['--muted']).toBe('var(--ink-3)')
    for (const selector of Object.values(THEMES)) {
      expect(block(CSS, selector)['--ink-3']).toMatch(/^#[0-9a-f]{6}$/i)
    }
  })

  it('leaves no panel rule reading a name the shell does not define', () => {
    const referenced = new Set(
      [...PANELS.matchAll(/var\((--[a-z0-9-]+)/gi)].map((m) => m[1].toLowerCase()),
    )
    // Those the panel stylesheet declares for itself.
    const local = new Set(['--panel-warn', '--panel-danger', '--panel-ok'])
    for (const name of referenced) {
      if (local.has(name)) continue
      expect(root[name], `panels.css reads ${name}, which :root does not define`).toBeTruthy()
    }
  })
})
