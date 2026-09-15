import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  MODAL_ENTER_CLASS,
  PAGE_ENTER_CLASS,
  SCRIM_ENTER_CLASS,
  prefersReducedMotion,
  ringAngles,
  subscribeReducedMotion,
} from './index'

/** A MediaQueryList that answers what the case is about. */
function stubMedia(matches: boolean, legacy = false): () => void {
  const listeners = new Set<() => void>()
  const query = {
    matches,
    media: '(prefers-reduced-motion: reduce)',
    ...(legacy
      ? {
          addListener: (l: () => void) => void listeners.add(l),
          removeListener: (l: () => void) => void listeners.delete(l),
        }
      : {
          addEventListener: (_: string, l: () => void) => void listeners.add(l),
          removeEventListener: (_: string, l: () => void) => void listeners.delete(l),
        }),
  }
  const original = window.matchMedia
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: vi.fn(() => query as unknown as MediaQueryList),
  })
  return () => {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: original,
    })
  }
}

let restore: (() => void) | null = null
afterEach(() => {
  restore?.()
  restore = null
})

describe('the reduced-motion switch', () => {
  it('is off where the preference cannot be asked for', () => {
    // jsdom ships no matchMedia, which is also the case in a locked-down
    // webview. A missing preference is not a stated preference.
    expect(prefersReducedMotion()).toBe(false)
  })

  it('reads the preference when one is stated', () => {
    restore = stubMedia(true)
    expect(prefersReducedMotion()).toBe(true)
  })

  it('unsubscribes cleanly, including from the legacy listener API', () => {
    restore = stubMedia(false, true)
    const stop = subscribeReducedMotion(() => {})
    expect(() => stop()).not.toThrow()
  })

  it('subscribing where there is no matchMedia is a no-op, not a crash', () => {
    expect(() => subscribeReducedMotion(() => {})()).not.toThrow()
  })
})

describe('the ring layout', () => {
  it('starts the sentence at nine o clock and reads clockwise', () => {
    const angles = ringAngles('abcd')
    expect(angles[0]).toBe(180)
    expect(angles[1]).toBeGreaterThan(angles[0])
    expect(angles).toHaveLength(4)
  })

  it('spreads a sentence over the whole circle', () => {
    const angles = ringAngles('abcdefgh')
    expect(angles[angles.length - 1] - angles[0]).toBeCloseTo(315, 5)
  })

  it('does not divide by zero on an empty sentence', () => {
    expect(ringAngles('')).toEqual([])
  })
})

describe('the two entrances', () => {
  const sheet = readFileSync(resolve(process.cwd(), 'src', 'ui', 'motion', 'motion.css'), 'utf8')

  it.each([PAGE_ENTER_CLASS, MODAL_ENTER_CLASS, SCRIM_ENTER_CLASS])(
    'defines .%s and turns it off under reduced motion',
    (name) => {
      expect(sheet).toMatch(new RegExp(`\\.${name}\\s*\\{[^}]*animation:`))
      const reduced = sheet.slice(sheet.indexOf('prefers-reduced-motion'))
      expect(reduced).toContain(`.${name}`)
      expect(reduced).toMatch(/animation:\s*none/)
    },
  )

  it('runs the page enter over the 320ms token and the modal over 300ms', () => {
    expect(sheet).toMatch(/\.sd-motion-page\s*\{[^}]*var\(--sd-dur-page\)/)
    expect(sheet).toMatch(/\.sd-motion-modal\s*\{[^}]*var\(--sd-dur-3\)/)
    expect(sheet).toMatch(/\.sd-motion-scrim\s*\{[^}]*var\(--sd-dur-2\)/)
  })
})
