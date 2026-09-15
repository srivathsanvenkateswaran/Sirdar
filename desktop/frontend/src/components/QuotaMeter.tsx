import type { Quota } from '../api/types'
import { relativeTime } from '../lib/format'
import QuotaChip from '../ui/quota-chip'

const OVER_BUDGET = 100

function clampPercent(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, value))
}

/** "seen 3m ago" from an ISO timestamp, for the widget's tooltip; empty when the stamp is not one. */
export function formatSeenAgo(observedAt: string, now: number = Date.now()): string {
  const ago = relativeTime(observedAt, now)
  return ago ? `seen ${ago}` : ''
}

/**
 * "resets in Xh Ym" from an ISO timestamp. A countdown to the minute, not
 * the shared relative clock: the chip's spec quotes the minutes, and a
 * quota that resets in 2h 14m is a fact a reader plans an hour around.
 */
export function formatResetIn(resetsAt: string, now: number = Date.now()): string {
  const diffMs = new Date(resetsAt).getTime() - now
  if (!Number.isFinite(diffMs)) return ''
  const totalMinutes = Math.max(0, Math.round(diffMs / 60000))
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  return `resets in ${hours}h ${minutes}m`
}

/**
 * How much of each provider's limit is gone, as a column of quota chips in the
 * sidebar footer.
 *
 * Two chips (5h and 7d) for a Claude quota, one (used) for a Codex one. The
 * chip itself is the library component; what is left here is the arithmetic on
 * the timestamps, which is Sirdar's and not the chip's — the chip does no
 * arithmetic on a clock by design.
 */
export default function QuotaMeter(props: { quota: Quota[] }): JSX.Element {
  const { quota } = props
  if (quota.length === 0) return <></>

  return (
    <div className="quota-meter">
      {quota.map((q) => (
        <div key={q.provider} className="quota-meter__provider" title={formatSeenAgo(q.observedAt)}>
          {q.fiveHour && (
            <QuotaChip
              provider={q.provider}
              window="5h"
              percent={clampPercent(q.fiveHour.utilization * 100)}
              resetsIn={q.fiveHour.resetsAt ? formatResetIn(q.fiveHour.resetsAt) : undefined}
              overBudget={q.fiveHour.utilization * 100 >= OVER_BUDGET}
            />
          )}
          {q.sevenDay && (
            <QuotaChip
              provider={q.provider}
              window="7d"
              percent={clampPercent(q.sevenDay.utilization * 100)}
              resetsIn={q.sevenDay.resetsAt ? formatResetIn(q.sevenDay.resetsAt) : undefined}
              overBudget={q.sevenDay.utilization * 100 >= OVER_BUDGET}
            />
          )}
          {q.usedPercent !== undefined && (
            <QuotaChip
              provider={q.provider}
              window="used"
              percent={clampPercent(q.usedPercent)}
              resetsIn={q.resetsAt ? formatResetIn(q.resetsAt) : undefined}
              overBudget={q.usedPercent >= OVER_BUDGET}
            />
          )}
        </div>
      ))}
    </div>
  )
}
