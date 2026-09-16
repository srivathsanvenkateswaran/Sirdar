import { readFileSync, readdirSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

/*
 * The library's cross-cutting rules, pointed at the app's own stylesheets.
 *
 * `src/ui/library.test.ts` checks that no component declares a colour and that
 * every component uses logical properties. Those rules were never only about
 * the library: a hex slipped into `board.css` looks right in the theme it was
 * written in and wrong in the other one, and a `margin-left` in `run.css` puts
 * the quote rail on the wrong side of an Arabic note. Both failures are
 * invisible to a type checker and to every render test in this suite, which is
 * why they are read off the source here.
 *
 * `styles/tokens.css` is the exemption and the reason the rule can hold: it is
 * the one file a value is declared in.
 */

const SRC = resolve(process.cwd(), 'src')

/** Every stylesheet the app ships, apart from the library's and the tokens. */
function stylesheets(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      // The library polices itself in src/ui/library.test.ts.
      if (entry.name === 'ui') continue
      stylesheets(path, out)
      continue
    }
    if (!entry.name.endsWith('.css')) continue
    // The single source of truth for every value, and the only file allowed
    // to spell one out.
    if (path.endsWith(join('styles', 'tokens.css'))) continue
    out.push(path)
  }
  return out
}

const SHEETS = stylesheets(SRC).sort()

describe("the app's own stylesheets", () => {
  it('finds the sheets it is meant to be checking', () => {
    const names = SHEETS.map((path) => relative(SRC, path))
    expect(names).toContain('styles.css')
    expect(names).toContain('components/session/session.css')
    expect(names).toContain('components/composer/composer.css')
    expect(names).toContain('components/shell/shell.css')
    expect(names).toContain('components/shell/sidebar.css')
    expect(names).toContain('screens/board.css')
    expect(names).toContain('screens/register.css')
  })

  describe.each(SHEETS.map((path) => [relative(SRC, path), path] as const))('%s', (name, path) => {
    const sheet = readFileSync(path, 'utf8')

    it('declares no colour of its own', () => {
      const rules = sheet
        .split('\n')
        // A comment may name a hex when it is explaining one that used to be
        // there; what must not appear is a declaration.
        .filter((line) => !/^\s*(\*|\/\*|\/\/)/.test(line))
        .join('\n')
      const hexes = rules.match(/#[0-9a-f]{3,8}\b/gi) ?? []
      expect(hexes, `${name} declares ${hexes.join(', ')}`).toEqual([])
      expect(rules, `${name} declares a colour function`).not.toMatch(/\b(rgb|hsl)a?\(/)
    })

    it('reads only tokens, never a name the tokens file does not define', () => {
      const tokens = readFileSync(join(SRC, 'styles', 'tokens.css'), 'utf8')
      const declared = new Set(
        [...tokens.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gim)].map((m) => m[1].toLowerCase()),
      )
      // What a sheet declares for itself: a local shade, never a hue.
      const local = new Set(
        [...sheet.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gim)].map((m) => m[1].toLowerCase()),
      )
      for (const match of sheet.matchAll(/var\(\s*(--[a-z0-9-]+)/gi)) {
        const read = match[1].toLowerCase()
        if (local.has(read)) continue
        expect(declared.has(read), `${name} reads ${read}, which no token declares`).toBe(true)
      }
    })

    it('uses logical properties, so it is correct in an Arabic pane', () => {
      const physical = sheet.match(
        /^\s*(margin|padding|border)-(left|right)\s*:|^\s*(left|right|top|bottom)\s*:/gm,
      )
      expect(physical, `${name} uses ${physical?.join(', ')}`).toBeNull()
    })

    it('answers for its motion, if it has any', () => {
      if (!/transition|animation/.test(sheet)) return
      expect(
        /prefers-reduced-motion/.test(sheet),
        `${name} animates with no reduced-motion answer`,
      ).toBe(true)
    })
  })
})
