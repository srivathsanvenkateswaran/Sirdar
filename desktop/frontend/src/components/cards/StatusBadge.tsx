import type { RunState } from '../../api/types'

const LABELS: Record<RunState, string> = {
  preparing: 'Preparing',
  running: 'Running',
  completed: 'Completed',
  failed: 'Failed',
  blocked: 'Needs input',
  over_budget: 'Over budget',
}

/**
 * The run's state, tinted with the hue its board column uses so the badge and
 * the column rail read as the same signal.
 */
export default function StatusBadge(props: { status: RunState }): JSX.Element {
  const { status } = props
  return (
    <span className="badge" data-status={status}>
      {LABELS[status] ?? status}
    </span>
  )
}

/**
 * The tracker's own priority string, shown verbatim — trackers disagree about
 * whether it is `P1`, `High` or `Blocker`, and inventing a mapping would hide
 * what the ticket actually says.
 */
export function PriorityBadge(props: { priority: string }): JSX.Element | null {
  const priority = props.priority?.trim()
  if (!priority) return null
  return (
    <span className="badge badge--priority" data-priority={priority.toLowerCase()}>
      {priority}
    </span>
  )
}
