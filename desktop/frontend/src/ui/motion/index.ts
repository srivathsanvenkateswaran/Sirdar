import { useSyncExternalStore } from 'react'
import './motion.css'

/**
 * The two ambient motions, and the one switch that turns them off.
 *
 * Ring text and the marquee are the only things in Sirdar that move without
 * being asked to. They are decorative by definition — neither carries a fact a
 * reader needs — so each has a static pose rather than a faster version, and
 * `docs/design/00-design-language.md` section 7 fixes what that pose is: the
 * ring freezes with its sentence starting at nine o'clock and reading
 * clockwise, and the marquee becomes a wrapped row with its edge mask removed.
 *
 * They share this module because they share that contract and the direction
 * flip under RTL: a sentence that travels against its own reading direction is
 * unreadable in either script, so both reverse when the page does. The flip
 * itself is a CSS rule in `motion.css`, where `[dir="rtl"]` can be read from
 * whichever ancestor carries it; nothing here has to be told the direction.
 *
 * At most one component uses each motion: `ui/ambient` renders RingText and
 * Marquee, and nothing else in the library animates a position.
 */

const QUERY = '(prefers-reduced-motion: reduce)'

function media(): MediaQueryList | null {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return null
  return window.matchMedia(QUERY)
}

/** Whether this reader has asked for less motion. False where it cannot be asked. */
export function prefersReducedMotion(): boolean {
  return media()?.matches ?? false
}

/** Notifies on every change of the preference; the return value unsubscribes. */
export function subscribeReducedMotion(listener: () => void): () => void {
  const query = media()
  if (!query) return () => {}
  // Safari before 14 has no addEventListener on a MediaQueryList, and the
  // desktop app runs inside whatever WebKit the machine ships.
  if (typeof query.addEventListener === 'function') {
    query.addEventListener('change', listener)
    return () => query.removeEventListener('change', listener)
  }
  query.addListener(listener)
  return () => query.removeListener(listener)
}

/**
 * The preference, as state. A component reads this to choose its static pose
 * rather than to speed an animation up, because a shortened animation is still
 * motion.
 */
export function useReducedMotion(): boolean {
  return useSyncExternalStore(subscribeReducedMotion, prefersReducedMotion, () => true)
}

/** The ambient durations, so a spec and a stylesheet cannot disagree. */
export const RING_DURATION_TOKEN = 'var(--sd-dur-ring)'
export const MARQUEE_DURATION_TOKEN = 'var(--sd-dur-marquee)'

/**
 * Where each character of a sentence sits on the ring.
 *
 * The sentence starts at nine o'clock and reads clockwise, which is the angle
 * the reduced-motion pose freezes at, so the still and the moving ring are the
 * same drawing. Characters are laid out by index rather than by measured width
 * because the ring is set in the UI face at one size and a half-degree of
 * kerning drift is invisible at 10.5px.
 */
export function ringAngles(text: string, arc = 360): number[] {
  const count = Math.max(text.length, 1)
  const step = arc / count
  return [...text].map((_, i) => 180 + i * step)
}
