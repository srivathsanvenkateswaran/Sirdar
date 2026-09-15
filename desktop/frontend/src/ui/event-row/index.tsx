import type { ReactNode } from 'react'
import './EventRow.css'

/** What the row is. The variant paints the rail and picks the glyph. */
export type EventVariant =
  | 'text'
  | 'tool'
  | 'allow'
  | 'deny'
  | 'usage'
  | 'state'
  | 'final'
  | 'error'
  | 'callout'

/** The one character each variant marks its rail with. */
export const EVENT_GLYPHS: Record<EventVariant, string> = {
  text: '▪',
  tool: '›',
  allow: '✓',
  deny: '✕',
  usage: '∑',
  state: '•',
  final: '■',
  error: '!',
  callout: '?',
}

export interface EventRowProps {
  /** The offset from the start of the run, already formatted: "1m 04s". */
  at: string
  variant: EventVariant
  /** Overrides the variant's glyph. Rarely wanted. */
  glyph?: string
  children: ReactNode
}

/**
 * One line of the run's ledger.
 *
 * This is the densest thing in Sirdar: a fixed monospace gutter for the
 * offset, a rail with a one-character glyph, then the content. Colour lives on
 * the rail and nowhere else, so a denial or an error is visible down the
 * column without eight coloured words competing with the text they describe.
 *
 * The whole row is `dir="ltr"`, including inside a note pane laid out right to
 * left. It is tool names, file paths and JSON, and those are unreadable
 * mirrored — `lib/rtl.ts` states the same rule for the same reason.
 */
export default function EventRow({ at, variant, glyph, children }: EventRowProps): JSX.Element {
  return (
    <div className="sd-event" data-variant={variant} dir="ltr">
      <span className="sd-event__at">{at}</span>
      <span className="sd-event__mark" aria-hidden="true">
        {glyph ?? EVENT_GLYPHS[variant]}
      </span>
      <div className="sd-event__main">{children}</div>
    </div>
  )
}
