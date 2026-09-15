import type { ReactNode } from 'react'
import { ringAngles, useReducedMotion } from '../motion'
import './Ambient.css'

export interface RingTextProps {
  /** A short sentence. Long enough to circle, short enough to read in one pass. */
  text: string
  /** The ring's outer size in pixels. */
  size?: number
}

/**
 * A sentence set on a circle, turning once every 48 seconds.
 *
 * It carries no information: the same sentence is in the headline beside it, or
 * it is a phrase about the product that nothing depends on. That is what makes
 * it safe to stop, and it stops completely under `prefers-reduced-motion` at
 * the angle it starts from — nine o'clock, reading clockwise — rather than
 * spinning faster or fading.
 *
 * The sentence is hidden from screen readers. Read character by character it
 * is noise, and every word of it is already on the page in a heading.
 */
export function RingText({ text, size = 180 }: RingTextProps): JSX.Element {
  const still = useReducedMotion()
  const angles = ringAngles(text)
  const radius = size / 2

  return (
    <div
      className={`sd-ring${still ? '' : ' sd-motion-ring'}`}
      style={{ inlineSize: size, blockSize: size }}
      aria-hidden="true"
      data-still={still ? 'true' : undefined}
    >
      {[...text].map((character, i) => (
        <span
          key={`${character}-${i}`}
          className="sd-ring__glyph"
          style={{
            transform: `rotate(${angles[i]}deg) translate(${radius}px) rotate(90deg)`,
          }}
        >
          {character === ' ' ? ' ' : character}
        </span>
      ))}
    </div>
  )
}

export interface MarqueeProps {
  /** The row's items. Type, never third-party logos. */
  children: ReactNode
  /** Says what the row lists, for a reader who cannot see it scroll. */
  label: string
}

/**
 * One row of short items travelling one track width every 34 seconds.
 *
 * The track is rendered twice so the loop has no seam, and the duplicate is
 * hidden from screen readers — otherwise every provider name is announced
 * twice. Under `prefers-reduced-motion` the row stops, wraps onto as many
 * lines as it needs, and drops its edge mask, because a mask with nothing
 * moving under it is a gradient for its own sake.
 */
export function Marquee({ children, label }: MarqueeProps): JSX.Element {
  const still = useReducedMotion()
  return (
    <div className="sd-marquee" data-still={still ? 'true' : undefined} aria-label={label} role="group">
      <div className={`sd-marquee__track${still ? '' : ' sd-motion-marquee'}`}>
        <div className="sd-marquee__run">{children}</div>
        {!still && (
          <div className="sd-marquee__run" aria-hidden="true">
            {children}
          </div>
        )}
      </div>
    </div>
  )
}

export interface MarqueeItemProps {
  children: ReactNode
}

/** One item in the row: a provider, a tracker, a helpdesk, set as type. */
export function MarqueeItem({ children }: MarqueeItemProps): JSX.Element {
  return <span className="sd-marquee__item">{children}</span>
}

export default Marquee
