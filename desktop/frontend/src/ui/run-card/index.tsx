import StatusBadge, { PriorityBadge, type SdStatus } from '../status-badge'
import './RunCard.css'

export interface RunCardProps {
  /** The tracker's key, shown in the ledger face because it is an identifier. */
  runKey: string
  /** What the run is: triage, fix, rca, eval. */
  kind: string
  status: SdStatus
  /** The ticket's title. Falls back to the key when the tracker has none. */
  title?: string
  /** Why the run stopped. Shown only for a state that stopped on something. */
  reason?: string
  priority?: string
  /** Already formatted: this component does no arithmetic on a clock. */
  elapsed?: string
  /** The clock's tooltip: the exact stamp, or what it is counting. */
  elapsedTitle?: string
  cost?: string
  /** The cost's tooltip: turns and token flow, so the card stays to one number. */
  costTitle?: string
  onOpen: () => void
}

const STOPPED: SdStatus[] = ['blocked', 'failed', 'over_budget']

/**
 * A run, as the board shows it.
 *
 * The key is monospace and the title is not, which is the product's one
 * typographic rule: an identifier is a thing you match character by character,
 * a title is a thing you read. The title and the reason both carry `dir="auto"`
 * because either can be the customer's own Arabic, and a reason quoting a
 * customer inside an English board should still read from the right.
 *
 * A live run takes the accent on its leading edge. It is the only moving thing
 * on the board and the only thing wearing the accent, so a column of cards
 * answers "what is happening right now" without being read.
 */
export default function RunCard({
  runKey,
  kind,
  status,
  title,
  reason,
  priority,
  elapsed,
  elapsedTitle,
  cost,
  costTitle,
  onOpen,
}: RunCardProps): JSX.Element {
  const live = status === 'preparing' || status === 'running'
  const heading = title || runKey
  const showReason = Boolean(reason) && STOPPED.includes(status)

  return (
    <button
      type="button"
      className="sd-run-card"
      data-status={status}
      data-live={live ? 'true' : undefined}
      aria-label={`${runKey}: ${heading}`}
      onClick={onOpen}
    >
      <span className="sd-run-card__top">
        <span className="sd-run-card__key" dir="ltr">
          {runKey}
        </span>
        <span className="sd-run-card__kind" dir="ltr">
          {kind}
        </span>
        <PriorityBadge priority={priority ?? ''} />
      </span>

      {heading !== runKey && (
        <span className="sd-run-card__title" dir="auto">
          {heading}
        </span>
      )}

      {showReason && (
        <span className="sd-run-card__reason" dir="auto">
          {reason}
        </span>
      )}

      <span className="sd-run-card__foot">
        <StatusBadge status={status} />
        {elapsed && (
          <span
            className="sd-run-card__elapsed"
            data-live={live ? 'true' : undefined}
            title={elapsedTitle}
            dir="ltr"
          >
            {elapsed}
          </span>
        )}
        {cost && (
          <span className="sd-run-card__cost" title={costTitle} dir="ltr">
            {cost}
          </span>
        )}
      </span>
    </button>
  )
}
