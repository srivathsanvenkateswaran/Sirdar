import type { RunSummary } from '../../api/types'
import CostChip from './CostChip'
import ElapsedTime from './ElapsedTime'
import StatusBadge, { PriorityBadge } from './StatusBadge'

/**
 * A run in any lane past the queue. The whole card opens Run detail.
 *
 * The heading is the tracker's ticket title and the reason is whatever the run
 * stopped on, which can be a question quoting the customer, so both carry
 * `dir="auto"` and lay themselves out from their own first strong character.
 */

export default function RunCard(props: {
  run: RunSummary
  title?: string
  priority?: string
  onOpen: (runId: string) => void
}): JSX.Element {
  const { run, title, priority, onOpen } = props
  const live = run.status === 'preparing' || run.status === 'running'
  const needsReason = run.status === 'blocked' || run.status === 'failed' || run.status === 'over_budget'
  const heading = title || run.key

  return (
    <button
      type="button"
      className="card card--run"
      data-status={run.status}
      onClick={() => onOpen(run.runId)}
      aria-label={`${run.key}: ${heading}`}
    >
      <span className="card-top">
        <span className="key">{run.key}</span>
        <span className="card-kind">{run.kind}</span>
        <PriorityBadge priority={priority ?? ''} />
      </span>
      {heading !== run.key && (
        <span className="card-title" dir="auto">
          {heading}
        </span>
      )}
      {needsReason && run.reason && (
        <span className="card-reason" dir="auto">
          {run.reason}
        </span>
      )}

      <span className="card-foot">
        <StatusBadge status={run.status} />
        <ElapsedTime startedAt={run.startedAt} updatedAt={run.updatedAt} live={live} />
        <CostChip usage={run.usage} live={live} />
      </span>
    </button>
  )
}
