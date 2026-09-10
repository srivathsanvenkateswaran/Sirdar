import type { Ticket } from '../../api/types'
import { relativeTime } from '../../lib/format'
import { PriorityBadge } from './StatusBadge'

/**
 * A tracker ticket that Sirdar has never run. Nothing to open yet, so the only
 * affordance is the one action that changes that.
 */
export default function TicketCard(props: {
  ticket: Ticket
  onTriage: (key: string) => void
  busy?: boolean
}): JSX.Element {
  const { ticket, onTriage, busy } = props
  return (
    <article className="card card--ticket">
      <span className="card-top">
        <span className="key">{ticket.key}</span>
        <PriorityBadge priority={ticket.priority} />
      </span>
      {ticket.title && <span className="card-title">{ticket.title}</span>}
      <span className="card-foot">
        <button
          type="button"
          className="button button--quiet"
          disabled={busy}
          onClick={() => onTriage(ticket.key)}
        >
          Triage
        </button>
        {ticket.status && <span className="card-kind">{ticket.status}</span>}
        <time className="elapsed" dateTime={ticket.updatedAt} title={ticket.updatedAt}>
          {relativeTime(ticket.updatedAt)}
        </time>
      </span>
    </article>
  )
}
