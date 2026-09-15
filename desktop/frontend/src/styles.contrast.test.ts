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
/** The desktop app's own three planes: window ground, content sheet, card. */
const APP_GROUNDS = ['--sd-shell', '--sd-sheet', '--sd-card-row'] as const
const HEAT = [
  '--sd-heat-0',
  '--sd-heat-1',
  '--sd-heat-2',
  '--sd-heat-3',
  '--sd-heat-4',
] as const
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

    /*
     * The app shell: a window ground with one content sheet on it. Every ratio
     * `docs/design/01-tokens.md` section 2 states for these tokens is asserted
     * here, in both themes, because a plane pair is the one kind of token that
     * can be wrong in only one theme and still look right in the other.
     */
    it(`${theme}: the ink ramp clears 4.5:1 on the shell, the sheet and a card row`, () => {
      for (const token of INKS) {
        for (const ground of APP_GROUNDS) {
          const ratio = contrast(tokens[token], tokens[ground])
          expect(
            ratio,
            `${token} ${tokens[token]} on ${ground} ${tokens[ground]} is ${ratio.toFixed(2)}:1`,
          ).toBeGreaterThanOrEqual(4.5)
        }
      }
    })

    it(`${theme}: the sheet lifts off the shell without becoming a second window`, () => {
      const step = contrast(tokens['--sd-sheet'], tokens['--sd-shell'])
      expect(step, `sheet on shell is ${step.toFixed(2)}:1`).toBeGreaterThanOrEqual(1.08)
      expect(step).toBeLessThanOrEqual(1.25)
    })

    it(`${theme}: a card row is one step off the sheet, in whichever direction reads as raised`, () => {
      const step = contrast(tokens['--sd-card-row'], tokens['--sd-sheet'])
      expect(step, `card row on sheet is ${step.toFixed(2)}:1`).toBeGreaterThanOrEqual(1.08)
      expect(step).toBeLessThanOrEqual(1.25)
      // Light: darker than the sheet. Dark: lighter. That inversion is why
      // --sd-card-row cannot be --sd-sunk, which stays recessed in both.
      const raised = luminance(tokens['--sd-card-row']) - luminance(tokens['--sd-sheet'])
      const lightTheme = luminance(tokens['--sd-sheet']) > 0.5
      expect(lightTheme ? raised < 0 : raised > 0).toBe(true)
    })

    it(`${theme}: the nav fills are neutral, and never wear the accent`, () => {
      for (const fill of ['--sd-nav-hover', '--sd-nav-active'] as const) {
        expect(tokens[fill]).not.toBe(tokens['--sd-accent'])
        expect(tokens[fill]).not.toBe(tokens['--sd-shell'])
      }
      // Rest and hover carry --sd-ink-2; the current row carries --sd-ink.
      expect(contrast(tokens['--sd-ink-2'], tokens['--sd-nav-hover'])).toBeGreaterThanOrEqual(4.5)
      expect(contrast(tokens['--sd-ink-2'], tokens['--sd-nav-active'])).toBeGreaterThanOrEqual(4.5)
      expect(contrast(tokens['--sd-ink'], tokens['--sd-nav-active'])).toBeGreaterThanOrEqual(4.5)
    })

    it(`${theme}: the primary button is the ink inverted, and its label clears 4.5:1`, () => {
      expect(tokens['--sd-primary']).toBe(tokens['--sd-ink'])
      expect(contrast(tokens['--sd-primary-ink'], tokens['--sd-primary'])).toBeGreaterThanOrEqual(
        4.5,
      )
      expect(
        contrast(tokens['--sd-primary-ink'], tokens['--sd-primary-hover']),
      ).toBeGreaterThanOrEqual(4.5)
      // A black button on a #0C0D11 shell is invisible, which is the whole
      // reason the fill inverts rather than staying one value.
      expect(contrast(tokens['--sd-primary'], tokens['--sd-shell'])).toBeGreaterThanOrEqual(4.5)
    })

    it(`${theme}: a badge's label clears 4.5:1 on its fill`, () => {
      const bg = hex(tokens, '--sd-badge-bg')
      const fg = hex(tokens, '--sd-badge-ink')
      const ratio = contrast(fg, bg)
      expect(ratio, `--sd-badge-ink ${fg} on ${bg} is ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(
        4.5,
      )
    })

    it(`${theme}: the heat ramp climbs in one direction, a step at a time`, () => {
      const steps = HEAT.map((token) => luminance(hex(tokens, token)))
      // Light runs bright to dark, dark runs dark to bright; either way "more
      // runs" is always further from the sheet, never back towards it.
      const down = steps[0] > steps[steps.length - 1]
      for (let i = 1; i < steps.length; i += 1) {
        expect(
          down ? steps[i] < steps[i - 1] : steps[i] > steps[i - 1],
          `--sd-heat-${i} is not a step on from --sd-heat-${i - 1}`,
        ).toBe(true)
        const ratio = contrast(hex(tokens, HEAT[i]), hex(tokens, HEAT[i - 1]))
        expect(
          ratio,
          `--sd-heat-${i - 1} to --sd-heat-${i} is ${ratio.toFixed(2)}:1`,
        ).toBeGreaterThanOrEqual(1.2)
      }
      // The empty cell has to be visible on the sheet it sits on.
      const empty = contrast(tokens['--sd-heat-0'], tokens['--sd-sheet'])
      expect(empty, `--sd-heat-0 on the sheet is ${empty.toFixed(2)}:1`).toBeGreaterThanOrEqual(1.1)
      // The busiest day and a live run are the same colour, which is true:
      // both mean the machine is working.
      expect(hex(tokens, '--sd-heat-4')).toBe(tokens['--sd-accent'])
    })

    it(`${theme}: the faint rule is a hairline and not a text colour`, () => {
      expect(tokens['--sd-rule-faint']).not.toBe(tokens['--sd-card-row'])
      expect(tokens['--sd-rule-faint']).not.toBe(tokens['--sd-ink-3'])
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

  /*
   * The app-shell addendum, pair by pair. `docs/design/01-tokens.md` section 6
   * names four tight ones and two steps; every other number the addendum
   * prints is here too, because the rule it ends on — a token that cannot
   * state its ratio does not ship — only means anything if the ratios are
   * read back rather than believed.
   */
  it('states every app-shell ratio the tokens doc claims', () => {
    const light = block(TOKENS, ':root {')
    const dark = { ...light, ...block(TOKENS, ':root[data-theme="dark"]') }

    const pairs: [string, string, number, number][] = [
      // fg, bg, light, dark
      ['--sd-ink-3', '--sd-shell', 4.6, 6.14],
      ['--sd-ink', '--sd-sheet', 17.44, 14.02],
      ['--sd-ink', '--sd-card-row', 15.7, 12.51],
      ['--sd-ink-2', '--sd-card-row', 7.37, 6.6],
      ['--sd-ink-3', '--sd-card-row', 4.73, 4.9],
      ['--sd-ink-2', '--sd-nav-hover', 6.79, 7.16],
      ['--sd-ink', '--sd-nav-active', 13.68, 12.19],
      ['--sd-ink-2', '--sd-nav-active', 6.42, 6.42],
      ['--sd-primary-ink', '--sd-primary', 17.44, 15.69],
      ['--sd-primary-ink', '--sd-primary-hover', 13.53, 12.82],
    ]
    for (const [fg, bg, inLight, inDark] of pairs) {
      expect(contrast(light[fg], light[bg]), `light ${fg} on ${bg}`).toBeCloseTo(inLight, 1)
      expect(contrast(dark[fg], dark[bg]), `dark ${fg} on ${bg}`).toBeCloseTo(inDark, 1)
    }

    const badge = (t: Record<string, string>) =>
      contrast(hex(t, '--sd-badge-ink'), hex(t, '--sd-badge-bg'))
    expect(badge(light)).toBeCloseTo(13.86, 1)
    expect(badge(dark)).toBeCloseTo(9.72, 1)

    // The two plane steps, each an upper bound as well as a lower one.
    expect(contrast(light['--sd-sheet'], light['--sd-shell'])).toBeCloseTo(1.14, 2)
    expect(contrast(dark['--sd-sheet'], dark['--sd-shell'])).toBeCloseTo(1.12, 2)
    expect(contrast(light['--sd-card-row'], light['--sd-sheet'])).toBeCloseTo(1.11, 2)
    expect(contrast(dark['--sd-card-row'], dark['--sd-sheet'])).toBeCloseTo(1.12, 2)
    expect(contrast(light['--sd-nav-hover'], light['--sd-shell'])).toBeCloseTo(1.06, 2)
    expect(contrast(dark['--sd-nav-hover'], dark['--sd-shell'])).toBeCloseTo(1.15, 2)
    expect(contrast(light['--sd-nav-active'], light['--sd-shell'])).toBeCloseTo(1.12, 2)
    expect(contrast(dark['--sd-nav-active'], dark['--sd-shell'])).toBeCloseTo(1.29, 2)
    expect(contrast(light['--sd-heat-0'], light['--sd-sheet'])).toBeCloseTo(1.18, 2)
    expect(contrast(dark['--sd-heat-0'], dark['--sd-sheet'])).toBeCloseTo(1.15, 2)

    // The consecutive heat steps, as the doc prints them.
    const gaps = (t: Record<string, string>) =>
      HEAT.slice(1).map((token, i) => Number(contrast(hex(t, token), hex(t, HEAT[i])).toFixed(2)))
    expect(gaps(light)).toEqual([1.3, 1.51, 1.98, 1.91])
    expect(gaps(dark)).toEqual([1.26, 1.52, 1.83, 1.9])
  })

  /*
   * One claim in `01-tokens.md` does not survive being measured, and it is
   * recorded rather than quietly corrected or quietly broken.
   *
   * The doc gives --sd-rule-faint as #EAE6D6 / #24262C and says the pair is
   * "1.11:1 light, 1.12:1 dark" against --sd-card-row. Light holds. Dark is
   * 1.02 — #24262C and the card's #22242B are two shades apart — so the
   * divider is all but invisible in the dark theme. 1.12 in dark is what the
   * ordinary --sd-rule already gives on a card row, and 1.15 is what
   * --sd-rule-faint gives against --sd-sheet, which is the likelier source of
   * the number. The values ship as the doc fixes them; whether dark's divider
   * wants a new value is a decision for whoever owns the palette.
   */
  it('records the one ratio the tokens doc claims that its own values do not give', () => {
    const light = block(TOKENS, ':root {')
    const dark = { ...light, ...block(TOKENS, ':root[data-theme="dark"]') }
    expect(contrast(light['--sd-rule-faint'], light['--sd-card-row'])).toBeCloseTo(1.11, 2)
    expect(contrast(dark['--sd-rule-faint'], dark['--sd-card-row'])).toBeCloseTo(1.02, 2)
    expect(contrast(dark['--sd-rule'], dark['--sd-card-row'])).toBeCloseTo(1.12, 2)
  })

  /*
   * A pair the app does not use, kept visible so nobody reaches for it: the
   * dim ink on a current nav pill is 4.12:1 in the light theme. The sidebar
   * spec says a nav row carries --sd-ink-2 at rest and --sd-ink when current,
   * and this is the number behind that rule.
   */
  it('records --sd-ink-3 on a nav pill as the pair the app does not use', () => {
    const light = block(TOKENS, ':root {')
    expect(contrast(light['--sd-ink-3'], light['--sd-nav-active'])).toBeCloseTo(4.12, 1)
    expect(contrast(light['--sd-ink-3'], light['--sd-nav-active'])).toBeLessThan(4.5)
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
 * The migration, finished.
 *
 * `panels.css` (since deleted: nothing imported it once the screens moved to
 * `src/ui`) and `run.css` were written against a shorter set of names —
 * --bg, --fg, --muted, --border, --line — and carried a light-theme literal as
 * each one's fallback. Nothing defined those names, so every rule in those
 * files used its fallback: in the dark theme that painted Register rows, Eval
 * values and the Settings headings in #16181d on a #131418 paper, a ratio of
 * 1.04:1. `01-tokens.md` section 5 landed the new names first and kept the old
 * ones as one-line references so the palette change and the rename were two
 * commits rather than one; this branch is the second pass it names, and these
 * cases are what say the pass is done rather than half done.
 *
 * The rule that replaced them is stronger and lives in
 * `styles.library.test.ts`: every stylesheet the app ships may read only names
 * the tokens file declares, which no fallback can slip past.
 */
describe('the token migration', () => {
  const LEGACY = [
    'paper',
    'surface',
    'lane',
    'ink',
    'ink-2',
    'ink-3',
    'rule',
    'rule-strong',
    'accent',
    'accent-ink',
    'accent-soft',
    'st-queue',
    'st-live',
    'st-blocked',
    'st-triaged',
    'st-done',
    'st-failed',
    'sans',
    'mono',
    'bg',
    'fg',
    'muted',
    'border',
    'line',
    'radius',
    'gutter',
  ] as const

  it('declares no unprefixed name any more: every token is --sd-', () => {
    const root = roots(TOKENS)
    const unprefixed = Object.keys(root).filter((name) => !name.startsWith('--sd-'))
    expect(unprefixed, `tokens.css still declares ${unprefixed.join(', ')}`).toEqual([])
  })

  it.each(LEGACY)('no stylesheet reads --%s any more', (name) => {
    for (const [file, source] of [['styles.css', SHELL]] as const) {
      // A fallback is what made the old names dangerous, so both spellings of
      // a read are refused: `var(--x)` and `var(--x, #literal)`.
      const reads = new RegExp(`var\\(\\s*--${name}\\s*[,)]`)
      expect(reads.test(source), `${file} still reads --${name}`).toBe(false)
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
