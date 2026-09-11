import type { Usage } from '../../api/types'
import { costOrUnknown, tokenFlow } from '../../lib/format'

/**
 * What the run has spent so far. The hover title carries the turn count and
 * token flow so the card itself stays to one number. While the run is live
 * and the provider has reported no cost yet, that number is `n/a`: Claude
 * reports cost only when the session ends.
 */
export default function CostChip(props: { usage?: Usage; live?: boolean }): JSX.Element | null {
  const usage = props.usage
  if (!usage) return null
  const cost = usage.costUsd ?? 0
  const turns = usage.turns ?? 0
  if (cost === 0 && turns === 0) return null
  const live = props.live ?? false
  const tokens = tokenFlow(usage.inputTokens, usage.outputTokens)
  const detail =
    live && cost === 0
      ? `${turns} ${turns === 1 ? 'turn' : 'turns'}, ${tokens}; cost is reported when the session ends`
      : `${turns} ${turns === 1 ? 'turn' : 'turns'}, ${tokens}`
  return (
    <span className="cost" title={detail}>
      {costOrUnknown(cost, live)}
    </span>
  )
}
