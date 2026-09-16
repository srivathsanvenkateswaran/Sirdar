import type { MouseEvent } from 'react'
import './Marker.css'

export type MarkerSize = 'sm' | 'md'

export interface MarkerProps {
  /** `E1`, `C3`: the letter says what it marks, the number which one. */
  id: string
  /** The marker whose twin is in view: the accent fill. */
  hot?: boolean
  /** 20px tall in a step or a table cell; 16px beside a `file:line` in prose. */
  size?: MarkerSize
  /** Makes it a button: clicking finds the twin. Without it the chip only labels. */
  onClick?: (id: string, event: MouseEvent<HTMLElement>) => void
  /** The tooltip: the evidence item's query, a hunk's header. */
  title?: string
}

/** The family a marker id belongs to: `E` for evidence, `C` for a change. */
export function markerKind(id: string): 'E' | 'C' | 'other' {
  const first = id.charAt(0).toUpperCase()
  return first === 'E' || first === 'C' ? first : 'other'
}

/**
 * The small chip that ties a claim to the call that produced it.
 *
 * E1…En sit on the answer's evidence items, on the `file:line` references
 * in its prose, on the tool calls that read or searched those files and in
 * the Tools table's last column; C1…Cn sit on a change's hunks and on the
 * edits that wrote them. The same id on both sides is the whole idea: a
 * reader auditing a claim looks for the chip with the same letters, and
 * clicking one scrolls to and lights the other. The chip is a button only
 * when it goes somewhere.
 */
export default function Marker({ id, hot = false, size = 'md', onClick, title }: MarkerProps): JSX.Element {
  const shared = {
    className: 'sd-marker',
    'data-kind': markerKind(id),
    'data-hot': hot ? 'true' : undefined,
    'data-size': size === 'md' ? undefined : size,
    title,
  }
  if (onClick) {
    return (
      <button
        {...shared}
        type="button"
        aria-pressed={hot}
        aria-label={`Marker ${id}`}
        onClick={(e) => onClick(id, e)}
      >
        {id}
      </button>
    )
  }
  return <span {...shared}>{id}</span>
}
