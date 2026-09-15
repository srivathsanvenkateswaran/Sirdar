import { readFileSync, readdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/*
 * The rules that hold across every component in the library, checked rather
 * than asked for.
 *
 * `docs/design/library/README.md` states five of them — no component declares
 * a colour, logical properties only, every focusable part keeps the focus
 * ring, status is never colour alone, every animation has a reduced-motion
 * answer. Three of those can be read off the source, and they are the three
 * that break silently: a hex slipped into a component stylesheet looks right
 * in the theme it was written in, a `margin-left` looks right in the direction
 * it was written in, and a missing spec is invisible until somebody needs it.
 */

const UI = resolve(process.cwd(), 'src', 'ui')

/** Every component folder. `motion/` is a shared module, not a component. */
const COMPONENTS = readdirSync(UI, { withFileTypes: true })
  .filter((entry) => entry.isDirectory() && entry.name !== 'motion')
  .map((entry) => entry.name)
  .sort()

function files(component: string): string[] {
  return readdirSync(join(UI, component))
}

function css(component: string): string {
  const name = files(component).find((file) => file.endsWith('.css'))
  if (!name) throw new Error(`${component} has no stylesheet`)
  return readFileSync(join(UI, component, name), 'utf8')
}

describe('the component library', () => {
  it('holds the fifteen components the library README lists', () => {
    expect(COMPONENTS).toEqual([
      'ambient',
      'button',
      'card',
      'data-table',
      'dialog',
      'event-row',
      'hero-band',
      'kanban-column',
      'note-pane',
      'pill-nav',
      'quota-chip',
      'run-card',
      'segmented-control',
      'status-badge',
      'toast',
    ])
  })

  describe.each(COMPONENTS)('%s', (component) => {
    it('is a folder with a component, a stylesheet, a test and a spec', () => {
      const here = files(component)
      expect(here).toContain('index.tsx')
      expect(here).toContain('SPEC.md')
      expect(here.some((file) => file.endsWith('.css'))).toBe(true)
      expect(here.some((file) => file.endsWith('.test.tsx'))).toBe(true)
    })

    it('declares no colour of its own', () => {
      // The one exemption: a mask gradient needs an opaque stop, and opacity
      // is not something the palette has a token for. It paints nothing.
      const rules = css(component)
        .split('\n')
        .filter((line) => !/mask-image|^\s+(to right|transparent|#000|\))/.test(line))
        .join('\n')
      const hexes = rules.match(/#[0-9a-f]{3,8}\b/gi) ?? []
      expect(hexes, `${component} declares ${hexes.join(', ')}`).toEqual([])
      expect(rules).not.toMatch(/\b(rgb|hsl)a?\(/)
    })

    it('uses logical properties, so it is correct in an Arabic pane', () => {
      const physical = css(component).match(
        /^\s*(margin|padding|border)-(left|right)\s*:|^\s*(left|right|top|bottom)\s*:/gm,
      )
      expect(physical, `${component} uses ${physical?.join(', ')}`).toBeNull()
    })

    it('has a spec with all six headings and a changelog', () => {
      const spec = readFileSync(join(UI, component, 'SPEC.md'), 'utf8')
      for (const heading of [
        '## What it is',
        '## Anatomy',
        '## States',
        '## Tokens used',
        '## Do / Don',
        '## Accessibility',
        '## Changelog',
      ]) {
        expect(spec, `${component}'s spec has no "${heading}"`).toContain(heading)
      }
    })

    it('names an RTL row in its states table', () => {
      const spec = readFileSync(join(UI, component, 'SPEC.md'), 'utf8')
      expect(spec).toMatch(/\| ?RTL ?\|/)
    })

    it('answers for its motion, if it has any', () => {
      const sheet = css(component)
      if (!/transition|animation/.test(sheet)) return
      const answered =
        /prefers-reduced-motion/.test(sheet) ||
        /prefers-reduced-motion/.test(readFileSync(join(UI, 'motion', 'motion.css'), 'utf8'))
      expect(answered, `${component} animates with no reduced-motion answer`).toBe(true)
    })
  })
})
