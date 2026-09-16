import type { ReactNode } from 'react'
import type { RunEvent } from '../../api/types'
import { classify, offsetLabel, outputText, truncate } from '../../lib/events'
import SdEventRow, { type EventVariant } from '../../ui/event-row'
import AnswerCard from './AnswerCard'
import AssistantMessage from './AssistantMessage'
import CappedBlock from './CappedBlock'
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

/**
 * The operator's own words in the transcript: a steer the run recorded, or
 * an answer this window posted. Not a ledger row — it is the one thing in
 * the stream a person wrote, and it reads as a message rather than a record.
 * The original prompt is never one of these; it is the run's brief, under
 * Bundle.
 */
export function YouBubble({ text, continuation }: { text: string; continuation?: string }) {
  return (
    <div className="you" data-testid="you-bubble">
      <span className="you-who">you</span>
      <div className="you-say" dir="auto">
        {text}
        {continuation === 'primed' ? (
          <span className="you-note">continued in a new session</span>
        ) : null}
      </div>
    </div>
  )
}

/**
 * A tool result that paired with no call — the log lost the start, or a
 * provider wrote a result the transcript had no call for. It is shown in
 * full rather than dropped, since it is still what a tool returned.
 */
function LoneResult({ event, at }: { event: RunEvent; at: string }) {
  const text = outputText(event)
  return (
    <Row at={at} glyph="‹" variant="tool">
      <div className="call-body call-body--lone">
        <div className="call-label">Result</div>
        {text ? <CappedBlock text={text} label="tool result" table /> : <p className="call-none">The tool returned nothing.</p>}
      </div>
    </Row>
  )
}

/**
 * Picks the row for one event. `startedAt` sets the zero of the offset
 * gutter; `provider` heads a message block. A tool call, its result and
 * its permission are one row when `TurnGroup` pairs them; what reaches
 * here on its own is the unpaired remainder.
 */
export default function EventRow({
  event,
  startedAt,
  provider = '',
}: {
  event: RunEvent
  startedAt: string | undefined
  provider?: string
}) {
  const at = offsetLabel(event.t, startedAt)
  const family = classify(event)

  switch (family) {
    case 'tool':
      return event.kind === 'tool_started' ? (
        <ToolCallRow call={{ started: { index: 0, event } }} startedAt={startedAt} />
      ) : (
        <LoneResult event={event} at={at} />
      )
    case 'permission':
      return <PermissionRow event={event} at={at} />
    case 'usage':
      return <UsageRow event={event} at={at} />
    case 'you':
      return <YouBubble text={event.payload?.text ?? ''} continuation={event.payload?.continuation} />
    case 'text':
      return <AssistantMessage text={event.payload?.text ?? ''} provider={provider} at={at} />
    case 'final':
      return <AnswerCard event={event} at={at} />
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
              {rateLimited ? 'Rate limited' : 'The agent is asking'}
            </div>
            {event.payload?.text ? (
              <div className="ev-callout-body" dir="auto">
                {event.payload.text}
              </div>
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
