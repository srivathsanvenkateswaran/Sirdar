import type { QueuedSteer } from '../../api/types'
import './queued-steers.css'

/** How many chips the strip shows; the oldest fall off first. */
export const MAX_QUEUED_CHIPS = 3

/**
 * What became of one queued steer, in the words the chip carries. The word
 * is the status — never a colour alone.
 */
export function queuedSteerWord(q: Pick<QueuedSteer, 'status' | 'turn' | 'reason'>): string {
  switch (q.status) {
    case 'delivered':
      return q.turn ? `delivered at turn ${q.turn}` : 'delivered'
    case 'held':
      return 'waiting for the run to finish'
    case 'applied':
      return 'applied after the run finished'
    case 'dropped':
      return q.reason ? `not delivered: ${q.reason}` : 'not delivered'
    default:
      return 'queued for the next turn'
  }
}

/**
 * Which queued steers are worth a chip right now: every one still waiting
 * (queued or held), every one that could not be delivered, and — while the
 * run works — the ones the live session has already taken, so a reader sees
 * "delivered at turn N" before the transcript reaches it. Applied steers are
 * in the transcript as a "you" card and need no chip. The newest few.
 */
export function visibleQueuedSteers(all: QueuedSteer[] | undefined, running: boolean): QueuedSteer[] {
  const shown = (all ?? []).filter(
    (q) => q.status === 'queued' || q.status === 'held' || q.status === 'dropped' || (running && q.status === 'delivered'),
  )
  return shown.slice(-MAX_QUEUED_CHIPS)
}

/**
 * The strip above the composer's box listing the steers typed while the run
 * worked. Each chip is the instruction, cut to a line, and what became of
 * it. The text is the reader's own, so it takes its direction from itself.
 */
export default function QueuedSteers({ items }: { items: QueuedSteer[] }): JSX.Element | null {
  if (items.length === 0) return null
  return (
    <ul className="queued-steers" aria-label="Queued steers">
      {items.map((q) => (
        <li key={q.id} className="queued-steer" data-status={q.status}>
          <span className="queued-steer__text" dir="auto" title={q.text}>
            {q.text}
          </span>
          <span className="queued-steer__status">{queuedSteerWord(q)}</span>
        </li>
      ))}
    </ul>
  )
}
