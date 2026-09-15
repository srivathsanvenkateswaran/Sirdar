import { useMemo, useState } from 'react'
import { foldSystem, offsetLabel, type IndexedEvent, type Turn } from '../../lib/events'
import SdEventRow from '../../ui/event-row'
import EventRow from './EventRow'

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
}: {
  items: IndexedEvent[]
  startedAt: string | undefined
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
            <EventRow key={item.index} event={item.event} startedAt={startedAt} />
          ))
        : null}
    </>
  )
}

/**
 * One turn of the run. The rule above the rows is the only place a number is
 * announced, because turns really are a sequence: turn 3 came after turn 2.
 * The offset at the rule's other end is when the turn began.
 *
 * `fold` collapses the raw stream lines — the token deltas a provider writes
 * by the dozen per turn — behind one row each burst.
 */
export default function TurnGroup({
  turn,
  startedAt,
  fold = false,
}: {
  turn: Turn
  startedAt: string | undefined
  fold?: boolean
}) {
  const items = useMemo(
    () =>
      fold
        ? foldSystem(turn.events)
        : turn.events.map((item) => ({ kind: 'event' as const, index: item.index, item })),
    [fold, turn.events],
  )
  const began = offsetLabel(turn.events[0]?.event.t, startedAt)

  return (
    <section aria-label={`turn ${turn.n}`}>
      <div className="turn-rule">
        <span>turn {turn.n}</span>
        <i className="turn-line" />
        {began ? <span>{began}</span> : null}
      </div>
      {items.map((row) =>
        row.kind === 'fold' ? (
          <StreamFold key={`fold-${row.index}`} items={row.items} startedAt={startedAt} />
        ) : (
          <EventRow key={row.index} event={row.item.event} startedAt={startedAt} />
        ),
      )}
    </section>
  )
}
