import type { ReactNode } from 'react'
import './ItemRow.css'

/** The tone rail's hue: the fact the item states, or none. */
export type ItemTone = 'plain' | 'live' | 'blocked' | 'done' | 'failed'

export interface ItemRowProps {
  /** A 20px icon in the box at the leading edge, `currentColor`. */
  icon?: ReactNode
  title: ReactNode
  /** The line under the title: key, source, outcome, time. */
  meta?: ReactNode
  /** One control at the inline end: a Triage button, an Open link. */
  action?: ReactNode
  tone?: ItemTone
  /** Makes the whole row the control that opens what it describes. */
  onOpen?: () => void
  /** Required with `onOpen`: what the row opens, said in full. */
  openLabel?: string
  /** `auto` lets a bilingual title lay itself out from its own first letter. */
  dir?: 'auto' | 'ltr' | 'rtl'
  /** Clamp the title to two lines rather than one. */
  wrap?: boolean
}

/**
 * One thing in a list: a tone rail, an icon in a box, a title with a line of
 * facts under it, and one control at the end. "Landed today" on the New
 * session and Board screens is a stack of these.
 *
 * The rail is the only colour on the row and it states one fact — live,
 * waiting, done, failed — which the meta line also says in words. A row with
 * nothing to state has a neutral rail rather than none, so the list keeps its
 * left edge.
 */
export default function ItemRow({
  icon,
  title,
  meta,
  action,
  tone = 'plain',
  onOpen,
  openLabel,
  dir,
  wrap = false,
}: ItemRowProps): JSX.Element {
  const body = (
    <>
      {icon && (
        <span className="sd-item__icon" aria-hidden="true">
          {icon}
        </span>
      )}
      <span className="sd-item__body">
        <span className="sd-item__title" data-wrap={wrap ? 'true' : undefined} dir={dir}>
          {title}
        </span>
        {meta && <span className="sd-item__meta">{meta}</span>}
      </span>
    </>
  )

  if (onOpen) {
    return (
      <div className="sd-item" data-tone={tone}>
        <button type="button" className="sd-item__open" aria-label={openLabel} onClick={onOpen}>
          {body}
        </button>
        {action && <span className="sd-item__action">{action}</span>}
      </div>
    )
  }
  return (
    <div className="sd-item" data-tone={tone}>
      {body}
      {action && <span className="sd-item__action">{action}</span>}
    </div>
  )
}
