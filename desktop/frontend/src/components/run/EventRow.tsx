import type { ReactNode } from 'react'
import type { RunEvent } from '../../api/types'
import { classify, offsetLabel, truncate } from '../../lib/events'
import SdEventRow, { type EventVariant } from '../../ui/event-row'
import ToolCallRow from './ToolCallRow'
import PermissionRow from './PermissionRow'
import UsageRow from './UsageRow'

/**
 * The ledger row every event shares, drawn by the library's Event row: offset
 * in the leading gutter, then the rail with its one-character glyph, then the
 * content. Colour lives on the rail, so the variant is what a denial or an
 * error changes.
 *
 * This wrapper stays because the three row kinds that build on it —
 * `ToolCallRow`, `PermissionRow`, `UsageRow` — pass their own glyph, and
 * because the app's `classify` names one family (`system`) the library spells
 * `state`.
 */
export function Row({
  at,
  glyph,
  variant,
  children,
}: {
  at: string
  glyph: string
  variant: EventVariant
  children: ReactNode
}) {
  return (
    <SdEventRow at={at} variant={variant} glyph={glyph}>
      {children}
    </SdEventRow>
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
        <Row at={at} glyph="·" variant="state">
          <div className="ev-summary">{truncate(event.payload?.text || event.kind, 90)}</div>
        </Row>
      )
  }
}
