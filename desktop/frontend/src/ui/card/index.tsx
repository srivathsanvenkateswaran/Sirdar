import type { ReactNode } from 'react'
import './Card.css'

export type CardTone = 'plain' | 'live' | 'blocked' | 'failed'

export interface CardProps {
  /** The heading. Omit it and the card is a box with content in it. */
  title?: ReactNode
  /** Sits beside the title, in the ledger face: a key, a count, a clock. */
  meta?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  /** Paints the leading edge. Only a card whose state is a fact uses one. */
  tone?: CardTone
  /** Makes the whole card the control that opens what it describes. */
  onOpen?: () => void
  /** Required with `onOpen`: what the card opens, said in full. */
  openLabel?: string
  /** `auto` lets a bilingual title lay itself out from its own first letter. */
  dir?: 'auto' | 'ltr' | 'rtl'
}

/**
 * The base box. Everything on the board that looks like a box is this
 * component with different content in it.
 *
 * It has no shadow. Cards on a board separate with a hairline and a gap,
 * because twenty cards each casting the same soft grey shadow is a texture
 * rather than a hierarchy, and the board has to be readable at a glance from
 * across a desk. The only elevation in Sirdar is the hard offset under a
 * primary button and the soft ambient one under a dialog.
 */
export default function Card({
  title,
  meta,
  children,
  footer,
  tone = 'plain',
  onOpen,
  openLabel,
  dir,
}: CardProps): JSX.Element {
  const body = (
    <>
      {(title || meta) && (
        <span className="sd-card__head">
          {title && (
            <span className="sd-card__title" dir={dir}>
              {title}
            </span>
          )}
          {meta && <span className="sd-card__meta">{meta}</span>}
        </span>
      )}
      {children && (
        <span className="sd-card__body" dir={dir}>
          {children}
        </span>
      )}
      {footer && <span className="sd-card__foot">{footer}</span>}
    </>
  )

  if (onOpen) {
    return (
      <button type="button" className="sd-card" data-tone={tone} aria-label={openLabel} onClick={onOpen}>
        {body}
      </button>
    )
  }
  return (
    <div className="sd-card" data-tone={tone}>
      {body}
    </div>
  )
}
