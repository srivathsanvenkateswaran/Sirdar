import { useMemo, useState } from 'react'
import { conversation, offsetLabel, type IndexedEvent, type Turn } from '../../lib/events'
import SdEventRow from '../../ui/event-row'
import EventRow from './EventRow'
import AssistantMessage from './AssistantMessage'
import ToolCallRow from './ToolCallRow'

/**
 * A run of raw stream lines, standing in for all of them until it is opened.
 *
 * It keeps the ledger's three columns so the fold sits in the same grid as the
 * rows around it: the offset of the first line it covers, the system glyph,
 * and the count as the button.
 */
function StreamFold({
  items,
  startedAt,
  provider,
}: {
  items: IndexedEvent[]
  startedAt: string | undefined
  provider: string
}) {
  const [open, setOpen] = useState(false)

  return (
    <>
      <SdEventRow at={offsetLabel(items[0].event.t, startedAt)} variant="state" glyph="·">
        <button
          type="button"
          className="ev-fold"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          <span className="ev-fold-caret" aria-hidden="true">
            {open ? '▾' : '▸'}
          </span>
          {items.length} stream events
        </button>
      </SdEventRow>
      {open
        ? items.map((item) => (
            <EventRow key={item.index} event={item.event} startedAt={startedAt} provider={provider} />
          ))
        : null}
    </>
  )
}

/**
 * One turn of the run, read as a conversation: the model's messages as
 * prose blocks, each tool call as one row with its result and the policy's
 * word folded in, the answer as a card, and everything else as a ledger
 * row. The rule above is the only place a number is announced, because
 * turns really are a sequence: turn 3 came after turn 2. The offset at the
 * rule's other end is when the turn began.
 *
 * `fold` collapses the raw stream lines — the token deltas a provider writes
 * by the dozen per turn — behind one row each burst. `lastCall` is the
 * index of the run's last tool call, which opens on its own.
 */
export default function TurnGroup({
  turn,
  startedAt,
  fold = false,
  provider = '',
  lastCall = -1,
  live = false,
}: {
  turn: Turn
  startedAt: string | undefined
  fold?: boolean
  provider?: string
  lastCall?: number
  live?: boolean
}) {
  const items = useMemo(() => conversation(turn.events, fold), [fold, turn.events])
  const began = offsetLabel(turn.events[0]?.event.t, startedAt)

  return (
    <section aria-label={`turn ${turn.n}`}>
      <div className="turn-rule">
        <span>turn {turn.n}</span>
        <i className="turn-line" />
        {began ? <span>{began}</span> : null}
      </div>
      {items.map((row) => {
        switch (row.kind) {
          case 'fold':
            return (
              <StreamFold
                key={`fold-${row.index}`}
                items={row.items}
                startedAt={startedAt}
                provider={provider}
              />
            )
          case 'message':
            return (
              <AssistantMessage
                key={`msg-${row.index}`}
                text={row.text}
                provider={provider}
                at={offsetLabel(row.parts[0].event.t, startedAt)}
              />
            )
          case 'call': {
            const denied = row.call.permission?.event.payload?.decision === 'deny'
            return (
              <ToolCallRow
                key={`call-${row.index}`}
                call={row.call}
                startedAt={startedAt}
                defaultOpen={denied || row.index === lastCall}
                live={live}
              />
            )
          }
          default:
            return (
              <EventRow
                key={row.index}
                event={row.item.event}
                startedAt={startedAt}
                provider={provider}
              />
            )
        }
      })}
    </section>
  )
}
