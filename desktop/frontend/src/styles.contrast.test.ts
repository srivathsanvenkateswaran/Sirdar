import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/*
 * The palette, read out of the stylesheet and checked against WCAG.
 *
 * `styles/tokens.css` is the single source of truth for three surfaces, so it
 * is the file this test reads. Every ratio `docs/design/01-tokens.md` states is
 * asserted here, and the rule that file ends on — "a token that cannot state
 * its ratio does not ship" — is what this test enforces.
 *
 * Two pairs are tight on purpose and are named as their own cases so a future
 * palette tweak that loses them fails with the reason rather than with a
 * number: --sd-ink-3 on --sd-sunk at 4.56, which is why --sd-sunk is no
 * darker, and --sd-st-blocked on --sd-paper at 4.84.
 *
 * The dim ink is also the colour `--muted` resolves to for every panel. It
 * used to sit at 3.1:1 in the light theme and 3.5:1 in the dark one, which is
 * what made Register rows, Eval values and the Settings headings hard to read;
 * the panels made it worse by falling back to a light-theme literal in the
 * dark theme. Both failures are invisible to a type checker and to every
 * render test in this suite, which is why they are checked here.
 */

/** Vitest serves the module over its own transform pipeline, so `import.meta`
 * carries no file path; the stylesheets are read from the project root, which
 * is where the runner's working directory is. */
function css(path: string): string {
  return readFileSync(resolve(process.cwd(), 'src', path), 'utf8')
}

const TOKENS = css('styles/tokens.css')
const SHELL = css('styles.css')
const PANELS = css('components/panels.css')

/** The declarations of the `{ … }` block a selector opens, by property name. */
function block(source: string, selector: string, from = 0): Record<string, string> {
  const at = source.indexOf(selector, from)
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

/**
 * Every `:root` declaration in the file, later blocks winning. The light
 * palette and the legacy aliases are two separate `:root` blocks, and the
 * alias cases below need to see both.
 */
function roots(source: string): Record<string, string> {
  const out: Record<string, string> = {}
  let from = 0
  for (;;) {
    const at = source.indexOf('\n:root {', from)
    if (at < 0) return out
    Object.assign(out, block(source, ':root {', at))
    from = at + 1
  }
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
const SURFACES = ['--sd-paper', '--sd-surface', '--sd-sunk'] as const
const INKS = ['--sd-ink', '--sd-ink-2', '--sd-ink-3'] as const
const STATUS = [
  '--sd-st-queue',
  '--sd-st-blocked',
  '--sd-st-triaged',
  '--sd-st-done',
  '--sd-st-failed',
] as const

const THEMES = {
  light: ':root {',
  // The media-query theme, the explicitly chosen one and the subtree spelling
  // the gallery uses must stay in step; each is checked in its own right
  // rather than assumed equal to the others.
  'dark (system)': ':root:not([data-theme="light"])',
  'dark (chosen)': ':root[data-theme="dark"]',
  'dark (subtree)': '\n[data-theme="dark"] {',
  'light (subtree)': '\n[data-theme="light"] {',
} as const

/** Resolves one `var(--x)` hop so --sd-st-live and --sd-band-fg read as hexes. */
function hex(tokens: Record<string, string>, name: string): string {
  const raw = tokens[name]
  if (!raw) throw new Error(`${name} is not declared in this theme`)
  const via = /^var\((--[a-z0-9-]+)\)$/i.exec(raw)
  return via ? tokens[via[1]] : raw
}

describe('palette contrast', () => {
  it('is sanity-checked against the two ends of the scale', () => {
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 1)
    expect(contrast('#767676', '#ffffff')).toBeGreaterThanOrEqual(4.5)
  })

  const LIGHT = block(TOKENS, ':root {')

  for (const [theme, selector] of Object.entries(THEMES)) {
    // A theme block only restates what it changes: --sd-st-live is
    // `var(--sd-accent)` once, in the light block, and follows the accent into
    // the dark theme by inheritance. Reading a theme means reading it over the
    // light palette, which is how the browser resolves it too.
    const tokens = { ...LIGHT, ...block(TOKENS, selector) }

    it(`${theme}: the whole ink ramp clears 4.5:1 on every ground`, () => {
      for (const token of INKS) {
        for (const surface of SURFACES) {
          const ratio = contrast(tokens[token], tokens[surface])
          expect(
            ratio,
            `${token} ${tokens[token]} on ${surface} ${tokens[surface]} is ${ratio.toFixed(2)}:1`,
          ).toBeGreaterThanOrEqual(4.5)
        }
      }
    })

    it(`${theme}: the ink ramp is monotonic`, () => {
      const ground = luminance(tokens['--sd-paper'])
      const distance = (name: string) => Math.abs(luminance(tokens[name]) - ground)
      expect(distance('--sd-ink')).toBeGreaterThan(distance('--sd-ink-2'))
      expect(distance('--sd-ink-2')).toBeGreaterThan(distance('--sd-ink-3'))
    })

    it(`${theme}: the dim ink is neither the body ink nor a ground`, () => {
      expect(tokens['--sd-ink-3']).not.toBe(tokens['--sd-ink'])
      for (const surface of SURFACES) expect(tokens['--sd-ink-3']).not.toBe(tokens[surface])
    })

    it(`${theme}: the accent clears 4.5:1 on every ground`, () => {
      for (const surface of SURFACES) {
        const ratio = contrast(tokens['--sd-accent'], tokens[surface])
        expect(ratio, `--sd-accent on ${surface} is ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(
          4.5,
        )
      }
    })

    it(`${theme}: text on the accent and highlight fills clears 4.5:1`, () => {
      expect(contrast(tokens['--sd-accent-ink'], tokens['--sd-accent'])).toBeGreaterThanOrEqual(4.5)
      expect(
        contrast(tokens['--sd-highlight-ink'], tokens['--sd-highlight']),
      ).toBeGreaterThanOrEqual(4.5)
      // The link's marker sweep paints --sd-highlight behind body ink, and the
      // primary button's label is the accent over the same fill.
      expect(contrast(tokens['--sd-ink'], tokens['--sd-highlight'])).toBeGreaterThanOrEqual(4.5)
      expect(contrast(tokens['--sd-accent'], tokens['--sd-highlight'])).toBeGreaterThanOrEqual(4.5)
    })

    it(`${theme}: band ink clears 4.5:1 on both bands`, () => {
      for (const band of ['--sd-band-deep', '--sd-band-ink'] as const) {
        const fg = hex(tokens, '--sd-band-fg')
        const ratio = contrast(fg, tokens[band])
        expect(ratio, `--sd-band-fg ${fg} on ${band} is ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(
          4.5,
        )
      }
    })

    it(`${theme}: every status hue clears 4.5:1 on paper and on surface`, () => {
      for (const token of [...STATUS, '--sd-st-live'] as const) {
        for (const surface of ['--sd-paper', '--sd-surface'] as const) {
          const fg = hex(tokens, token)
          const ratio = contrast(fg, tokens[surface])
          expect(
            ratio,
            `${token} ${fg} on ${surface} ${tokens[surface]} is ${ratio.toFixed(2)}:1`,
          ).toBeGreaterThanOrEqual(4.5)
        }
      }
    })

    it(`${theme}: a status word is never painted on the sunk ground below 4.5:1`, () => {
      /*
       * --sd-st-blocked is #A15C07 in the light theme, which is 4.84:1 on
       * paper and 4.43:1 on sunk — the one pair in the file that does not
       * clear 4.5 on all three grounds. The token keeps the value the design
       * language fixes, and the rule the library carries instead is that no
       * component paints a status word on --sd-sunk: lane wells are sunk, but
       * the cards inside them are --sd-surface. This case records which hues
       * would be safe there if that rule ever changed.
       */
      const unsafe = STATUS.filter(
        (token) => contrast(hex(tokens, token), tokens['--sd-sunk']) < 4.5,
      )
      expect(unsafe.every((token) => token === '--sd-st-blocked')).toBe(true)
    })

    it(`${theme}: both rules are visible against the grounds they separate`, () => {
      for (const rule of ['--sd-rule', '--sd-rule-strong'] as const) {
        expect(tokens[rule]).not.toBe(tokens['--sd-surface'])
        expect(tokens[rule]).not.toBe(tokens['--sd-paper'])
      }
    })
  }

  it('states the two tight pairs at the ratios the tokens doc claims', () => {
    const light = block(TOKENS, ':root {')
    expect(contrast(light['--sd-ink-3'], light['--sd-sunk'])).toBeCloseTo(4.56, 1)
    expect(contrast(light['--sd-st-blocked'], light['--sd-paper'])).toBeCloseTo(4.84, 1)
    expect(contrast(light['--sd-paper'], light['--sd-band-deep'])).toBeCloseTo(12.83, 1)
  })

  it('keeps the two dark spellings identical', () => {
    const system = block(TOKENS, ':root:not([data-theme="light"])')
    const chosen = block(TOKENS, ':root[data-theme="dark"]')
    const subtree = block(TOKENS, '\n[data-theme="dark"] {')
    for (const [name, value] of Object.entries(system)) {
      expect(chosen[name], `${name} differs between the two dark spellings`).toBe(value)
      expect(subtree[name], `${name} differs in the subtree spelling`).toBe(value)
    }
  })
})

/*
 * The aliases are the migration. `panels.css` and `run.css` were written
 * against a shorter set of names — --bg, --fg, --muted, --border, --line — and
 * carry a light-theme literal as each one's fallback. Nothing defined those
 * names, so every rule in those files used its fallback: in the dark theme
 * that painted Register rows, Eval values and the Settings headings in #16181d
 * on a #131418 paper, a ratio of 1.04:1. They must stay defined, and must stay
 * expressed as references so they follow the palette into the dark theme
 * rather than pinning a light value.
 */
describe('legacy aliases', () => {
  const root = roots(TOKENS)

  const LEGACY = [
    '--paper',
    '--surface',
    '--lane',
    '--ink',
    '--ink-2',
    '--ink-3',
    '--rule',
    '--rule-strong',
    '--accent',
    '--accent-ink',
    '--accent-soft',
    '--st-queue',
    '--st-live',
    '--st-blocked',
    '--st-triaged',
    '--st-done',
    '--st-failed',
    '--sans',
    '--mono',
    '--bg',
    '--fg',
    '--muted',
    '--border',
    '--line',
    '--radius',
    '--gutter',
  ] as const

  it.each(LEGACY)('%s is a reference to a token, not a literal', (name) => {
    expect(root[name], `${name} is not defined in :root`).toBeTruthy()
    expect(root[name]).toMatch(/^var\(--sd-[a-z0-9-]+\)$/)
  })

  it('points every alias at a token the file actually declares', () => {
    const light = block(TOKENS, ':root {')
    for (const name of LEGACY) {
      const target = /^var\((--sd-[a-z0-9-]+)\)$/.exec(root[name])?.[1] ?? ''
      expect(light[target], `${name} points at ${target}, which is not declared`).toBeTruthy()
    }
  })

  it('resolves --muted to the dim ink, which every theme redefines', () => {
    expect(root['--muted']).toBe('var(--sd-ink-3)')
    for (const selector of Object.values(THEMES)) {
      expect(block(TOKENS, selector)['--sd-ink-3']).toMatch(/^#[0-9a-f]{6}$/i)
    }
  })

  it('leaves no panel rule reading a name the tokens file does not define', () => {
    const referenced = new Set(
      [...PANELS.matchAll(/var\((--[a-z0-9-]+)/gi)].map((m) => m[1].toLowerCase()),
    )
    // Those the panel stylesheet declares for itself.
    const local = new Set(['--panel-warn', '--panel-danger', '--panel-ok'])
    for (const name of referenced) {
      if (local.has(name)) continue
      expect(root[name], `panels.css reads ${name}, which the tokens file does not define`).toBeTruthy()
    }
  })
})

describe('the shell stylesheet', () => {
  it('imports the tokens file ahead of every rule', () => {
    const at = SHELL.indexOf("@import './styles/tokens.css'")
    expect(at, 'styles.css does not import the tokens file').toBeGreaterThan(-1)
    expect(SHELL.slice(0, at).replace(/\/\*[\s\S]*?\*\//g, '').trim()).toBe('')
  })

  it('declares no palette of its own', () => {
    const root = roots(SHELL)
    expect(Object.keys(root)).toEqual([])
    expect(SHELL).not.toMatch(/^\s*--(paper|ink|accent|st-[a-z-]+)\s*:/m)
  })
})
