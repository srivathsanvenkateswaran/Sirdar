import type { Ticket } from '../../api/types'
import { relativeTime } from '../../lib/format'
import Button from '../../ui/button'
import Card from '../../ui/card'
import { PriorityBadge } from '../../ui/status-badge'

/**
 * A tracker ticket that Sirdar has never run. Nothing to open yet, so the only
 * affordance is the one action that changes that.
 *
 * It is the library's base Card rather than the run card: a ticket has no
 * state, no clock and no cost, and giving it the run card's shape would
 * promise a run behind it.
 */
export default function TicketCard(props: {
  ticket: Ticket
  onTriage: (key: string) => void
  busy?: boolean
}): JSX.Element {
  const { ticket, onTriage, busy } = props
  return (
    <Card
      title={
        <span className="ticket-key" dir="ltr">
          {ticket.key}
        </span>
      }
      meta={<PriorityBadge priority={ticket.priority} />}
      dir="auto"
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => onTriage(ticket.key)}>
            Triage
          </Button>
          {ticket.status && <span className="ticket-status">{ticket.status}</span>}
          <time className="ticket-at" dateTime={ticket.updatedAt} title={ticket.updatedAt}>
            {relativeTime(ticket.updatedAt)}
          </time>
        </>
      }
    >
      {ticket.title || null}
    </Card>
  )
}
