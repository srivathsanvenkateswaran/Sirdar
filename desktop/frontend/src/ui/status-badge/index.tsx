import './StatusBadge.css'

/** The run states the CLI's state machine can report. */
export type SdStatus =
  | 'queued'
  | 'preparing'
  | 'running'
  | 'blocked'
  | 'completed'
  | 'failed'
  | 'over_budget'

/**
 * The word each state is shown by. Status is never colour alone, so this map
 * is the component rather than a label beside it: the badge cannot be rendered
 * without its word.
 */
export const STATUS_WORDS: Record<SdStatus, string> = {
  queued: 'Queued',
  preparing: 'Preparing',
  running: 'Running',
  blocked: 'Needs input',
  completed: 'Completed',
  failed: 'Failed',
  over_budget: 'Over budget',
}

export interface StatusBadgeProps {
  status: SdStatus
  /** Overrides the word. Only for a state the CLI reports under another name. */
  children?: string
}

/**
 * A run's state, in the hue its board column uses.
 *
 * Inside a lane the badge takes the lane's hue from the column rail, so the
 * rail at the top of a column and every badge under it are one signal and the
 * rail doubles as the legend. Outside a lane it takes the hue its own state
 * names.
 */
export default function StatusBadge({ status, children }: StatusBadgeProps): JSX.Element {
  return (
    <span className="sd-badge" data-status={status}>
      {children ?? STATUS_WORDS[status] ?? status}
    </span>
  )
}

/**
 * The tracker's own priority string, shown verbatim.
 *
 * Trackers disagree about whether the top priority is `P1`, `High` or
 * `Blocker`, and inventing a mapping would hide what the ticket actually says.
 * Only the two that every tracker treats as urgent get a hue.
 */
export function PriorityBadge({ priority }: { priority: string }): JSX.Element | null {
  const text = priority?.trim()
  if (!text) return null
  return (
    <span className="sd-badge sd-badge--priority" data-priority={text.toLowerCase()}>
      {text}
    </span>
  )
}
