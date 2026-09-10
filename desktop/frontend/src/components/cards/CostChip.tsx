import type { Usage } from '../../api/types'
import { tokenFlow, usd } from '../../lib/format'

/**
 * What the run has spent so far. The hover title carries the turn count and
 * token flow so the card itself stays to one number.
 */
export default function CostChip(props: { usage?: Usage }): JSX.Element | null {
  const usage = props.usage
  if (!usage) return null
  const cost = usage.costUsd ?? 0
  const turns = usage.turns ?? 0
  if (cost === 0 && turns === 0) return null
  const detail = `${turns} ${turns === 1 ? 'turn' : 'turns'}, ${tokenFlow(usage.inputTokens, usage.outputTokens)}`
  return (
    <span className="cost" title={detail}>
      {usd(cost)}
    </span>
  )
}
