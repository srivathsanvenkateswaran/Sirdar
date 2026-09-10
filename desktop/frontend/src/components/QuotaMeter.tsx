import type { Quota } from '../api/types'
import './panels.css'

const WARN_THRESHOLD = 80
const DANGER_THRESHOLD = 100

function clampPercent(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, value))
}

function levelClass(percent: number): string {
  if (percent >= DANGER_THRESHOLD) return 'quota-bar--danger'
  if (percent >= WARN_THRESHOLD) return 'quota-bar--warn'
  return ''
}

/** "seen 3m ago" from an ISO timestamp, for the widget's tooltip. */
export function formatSeenAgo(observedAt: string, now: number = Date.now()): string {
  const diffMs = now - new Date(observedAt).getTime()
  if (!Number.isFinite(diffMs)) return ''
  const minutes = Math.max(0, Math.round(diffMs / 60000))
  if (minutes < 1) return 'seen just now'
  if (minutes < 60) return `seen ${minutes}m ago`
  return `seen ${Math.floor(minutes / 60)}h ago`
}

/** "resets in Xh Ym" from an ISO timestamp. */
export function formatResetIn(resetsAt: string, now: number = Date.now()): string {
  const diffMs = new Date(resetsAt).getTime() - now
  if (!Number.isFinite(diffMs)) return ''
  const totalMinutes = Math.max(0, Math.round(diffMs / 60000))
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  return `resets in ${hours}h ${minutes}m`
}

function Bar({ label, percent, resetsAt }: { label: string; percent: number; resetsAt?: string }) {
  const pct = clampPercent(percent)
  return (
    <div className="quota-row">
      <span className="quota-row__label">{label}</span>
      <div className="quota-bar" role="img" aria-label={`${label} ${Math.round(pct)}%`}>
        <div className={`quota-bar__fill ${levelClass(pct)}`} style={{ width: `${pct}%` }} />
      </div>
      <span className="quota-row__pct">{Math.round(pct)}%</span>
      {resetsAt && <span className="quota-row__reset">{formatResetIn(resetsAt)}</span>}
    </div>
  )
}

/**
 * Compact header widget: two bars (5h/7d) for a Claude quota, one bar for a
 * Codex quota (usedPercent). Renders nothing when there is no quota to show.
 */
export default function QuotaMeter(props: { quota: Quota[] }): JSX.Element {
  const { quota } = props
  if (quota.length === 0) return <></>

  return (
    <div className="quota-meter">
      {quota.map((q) => (
        <div key={q.provider} className="quota-meter__provider" title={formatSeenAgo(q.observedAt)}>
          <span className="quota-meter__name">{q.provider}</span>
          {q.fiveHour && (
            <Bar label="5h" percent={q.fiveHour.utilization * 100} resetsAt={q.fiveHour.resetsAt} />
          )}
          {q.sevenDay && (
            <Bar label="7d" percent={q.sevenDay.utilization * 100} resetsAt={q.sevenDay.resetsAt} />
          )}
          {q.usedPercent !== undefined && (
            <Bar label="used" percent={q.usedPercent} resetsAt={q.resetsAt} />
          )}
        </div>
      ))}
    </div>
  )
}
