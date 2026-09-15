import type { ReactNode } from 'react'
import './KanbanColumn.css'
import './Note.css'

/** The board's columns, in the order the state machine moves a ticket through. */
export type LaneId = 'queue' | 'gathering' | 'blocked' | 'triaged' | 'done' | 'failed'

export interface KanbanColumnProps {
  lane: LaneId
  title: string
  /** Shown beside the title in the lane's hue. Zero is shown, not hidden. */
  count: number
  /**
   * A few words after the count saying what narrows this lane — "assigned to
   * you" on a queue that lists only the reader's tickets. It is in the head's
   * own sentence rather than the empty prose because it is true of the lane
   * whether or not the lane has anything in it.
   */
  note?: ReactNode
  /** Prose, not an icon: what an empty column means and what to do about it. */
  empty: ReactNode
  children?: ReactNode
}

/**
 * One column of the board.
 *
 * The 3px rail across the top is the lane's hue and the state glyph on every
 * card inside the column repeats it, so the rail is the board's legend and no
 * key is needed anywhere else. The heading says the same thing in words, which
 * is what keeps the column readable when the hue is not.
 *
 * An empty column says what its emptiness means. An icon of an empty box says
 * only that somebody thought about the empty case.
 */
export default function KanbanColumn({
  lane,
  title,
  count,
  note,
  empty,
  children,
}: KanbanColumnProps): JSX.Element {
  const isEmpty = !children || (Array.isArray(children) && children.length === 0)
  return (
    <section className="sd-lane" data-lane={lane} aria-label={`${title} (${count})`}>
      <span className="sd-lane__rail" aria-hidden="true" />
      <h2 className="sd-lane__head">
        <span>{title}</span>
        <span className="sd-lane__count">{count}</span>
        {note ? <span className="sd-lane__note">· {note}</span> : null}
      </h2>
      <div className="sd-lane__body">
        {isEmpty ? <p className="sd-lane__empty">{empty}</p> : children}
      </div>
    </section>
  )
}
