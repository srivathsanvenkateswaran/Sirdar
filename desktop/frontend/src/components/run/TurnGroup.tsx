import type { Turn } from '../../lib/events'
import EventRow from './EventRow'

/**
 * One turn of the run. The rule above the rows is the only place a number is
 * announced, because turns really are a sequence: turn 3 came after turn 2.
 */
export default function TurnGroup({
  turn,
  startedAt,
}: {
  turn: Turn
  startedAt: string | undefined
}) {
  return (
    <section aria-label={`turn ${turn.n}`}>
      <div className="turn-rule">
        <span>turn {turn.n}</span>
        <i className="turn-line" />
      </div>
      {turn.events.map((item) => (
        <EventRow key={item.index} event={item.event} startedAt={startedAt} />
      ))}
    </section>
  )
}
