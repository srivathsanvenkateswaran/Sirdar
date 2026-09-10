import type { ReactNode } from 'react'
import type { RunEvent } from '../../api/types'
import { classify, offsetLabel, truncate } from '../../lib/events'
import ToolCallRow from './ToolCallRow'
import PermissionRow from './PermissionRow'
import UsageRow from './UsageRow'

/**
 * The ledger row every event shares: offset in the left gutter, then the rail
 * with its one-character glyph, then the content. Colour lives on the rail, so
 * the variant is what a denial or an error changes.
 */
export function Row({
  at,
  glyph,
  variant,
  children,
}: {
  at: string
  glyph: string
  variant: string
  children: ReactNode
}) {
  return (
    <div className={`ev ev--${variant}`}>
      <span className="ev-at">{at}</span>
      <span className="ev-mark" aria-hidden="true">
        {glyph}
      </span>
      <div className="ev-main">{children}</div>
    </div>
  )
}

/** Picks the row for one event. `startedAt` sets the zero of the offset gutter. */
export default function EventRow({
  event,
  startedAt,
}: {
  event: RunEvent
  startedAt: string | undefined
}) {
  const at = offsetLabel(event.t, startedAt)
  const family = classify(event)

  switch (family) {
    case 'tool':
      return <ToolCallRow event={event} at={at} />
    case 'permission':
      return <PermissionRow event={event} at={at} />
    case 'usage':
      return <UsageRow event={event} at={at} />
    case 'text':
      return (
        <Row at={at} glyph="▪" variant="text">
          <div className="ev-text">{event.payload?.text ?? ''}</div>
        </Row>
      )
    case 'final':
      return (
        <Row at={at} glyph="●" variant="final">
          <div className="ev-note">note produced</div>
        </Row>
      )
    case 'error':
      return (
        <Row at={at} glyph="!" variant="error">
          <div className="ev-text">{event.payload?.text || 'run failed'}</div>
        </Row>
      )
    case 'callout': {
      const rateLimited = event.kind === 'rate_limited'
      return (
        <Row at={at} glyph={rateLimited ? '~' : '?'} variant="callout">
          <div className="ev-callout">
            <div className="ev-callout-head">
              {rateLimited ? 'rate limited' : 'agent asked a question'}
            </div>
            {event.payload?.text ? (
              <div className="ev-callout-body">{event.payload.text}</div>
            ) : null}
          </div>
        </Row>
      )
    }
    default:
      return (
        <Row at={at} glyph="·" variant="system">
          <div className="ev-summary">{truncate(event.payload?.text || event.kind, 90)}</div>
        </Row>
      )
  }
}
