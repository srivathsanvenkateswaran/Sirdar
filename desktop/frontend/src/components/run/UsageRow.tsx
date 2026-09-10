import type { RunEvent } from '../../api/types'
import { formatCost } from '../../lib/events'
import { Row } from './EventRow'

/**
 * The provider's per-turn tick. The turn rule above it already says which turn
 * ended, so this row only carries what the rule does not: the running totals.
 */
export default function UsageRow({ event, at }: { event: RunEvent; at: string }) {
  const turns = event.payload?.turns ?? 0
  const cost = event.payload?.costUsd
  const parts = [turns === 1 ? '1 turn' : `${turns} turns`]
  if (cost !== undefined) parts.push(formatCost(cost))

  return (
    <Row at={at} glyph="=" variant="usage">
      <div className="ev-summary">{parts.join('   ')}</div>
    </Row>
  )
}
