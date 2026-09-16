import type { JSX } from 'react'
import type { RunDetail } from '../../../api/types'
import { useNow } from '../../../lib/useNow'
import Button from '../../../ui/button'
import ProviderMark from '../../../ui/provider-mark'
import StatusBadge, { type SdStatus } from '../../../ui/status-badge'
import { SessionLayoutSwitcher } from '../../../components/session/RunHeader'
import { PersonIcon } from './icons'
import { gauges, type Gauge as GaugeModel } from './model'

/**
 * The run header, `gauges` variant: the 60px strip the Workbench mock draws
 * — key, kind, the state word as a badge, the ticket's title, three budget
 * gauges (turns, minutes, cost against the caps in state.json), the
 * provider's mark with the model, and the assignee.
 *
 * A local adapter with the shared `RunHeader`'s name and props; when
 * `src/components/session/RunHeader` lands with its `gauges` variant, this
 * file goes and the import moves. The Cancel button is not in the mock: a
 * live run has to be stoppable from somewhere, and the header is where the
 * other layouts put it.
 */
export interface RunHeaderProps {
  variant: 'gauges'
  detail: RunDetail
  /** The number shown for the ticket, on the Sessions-show preference. */
  keyText: string
  /** The other number, as the key's tooltip. */
  keyTitle?: string
  title?: string
  live: boolean
  /** A still clock for tests; the gauges tick on the shared clock without it. */
  now?: number
  onCancel?: () => void
  cancelDisabled?: boolean
  cancelTitle?: string
}

function Gauge({ label, value, cap, pct, na, live }: GaugeModel & { live: boolean }): JSX.Element {
  return (
    <div
      className="wb-gauge"
      data-live={live ? 'true' : 'false'}
      data-na={na ? 'true' : 'false'}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={pct}
      aria-valuetext={`${value} ${cap}`.trim()}
    >
      <span className="wb-gauge__l">
        <b>{value}</b>
        <span>{cap}</span>
      </span>
      <span className="wb-gauge__bar">
        <span className="wb-gauge__fill" style={{ inlineSize: `${pct}%` }} />
      </span>
      <span className="wb-gauge__lb">{label}</span>
    </div>
  )
}

export default function RunHeader({
  detail,
  keyText,
  keyTitle,
  title,
  live,
  now,
  onCancel,
  cancelDisabled,
  cancelTitle,
}: RunHeaderProps): JSX.Element {
  // The minutes gauge is a clock while the run is live or waiting; once the
  // run has ended the figures are state.json's and nothing ticks.
  const ticking = now === undefined && (live || detail.status === 'blocked')
  const tick = useNow(1000, ticking)
  const at = now ?? tick
  return (
    <header className="wb-head">
      <h1 className="wb-key" title={keyTitle || undefined}>
        {keyText}
      </h1>
      <span className="wb-kind">{detail.kind}</span>
      <StatusBadge
        status={detail.status as SdStatus}
        detail={detail.status === 'blocked' ? 'waiting on you' : undefined}
      />
      <span className="wb-title" title={title ?? detail.title} dir="auto">
        {title ?? detail.title ?? ''}
      </span>
      <div className="wb-gauges">
        {gauges(detail, at).map((g) => (
          <Gauge key={g.label} {...g} live={live} />
        ))}
      </div>
      <span className="wb-sep" />
      <span className="wb-who">
        <ProviderMark provider={detail.provider} size="sm" />
        <span className="wb-mono" dir="ltr">
          {detail.model || 'model unknown'}
        </span>
      </span>
      {detail.assignee ? (
        <>
          <span className="wb-sep" />
          <span className="wb-who" title="assignee">
            <PersonIcon />
            <span className="wb-mono">{detail.assignee}</span>
          </span>
        </>
      ) : null}
      <SessionLayoutSwitcher />
      {onCancel ? (
        <Button variant="ghost" size="sm" onClick={onCancel} disabled={cancelDisabled} title={cancelTitle}>
          Cancel
        </Button>
      ) : null}
    </header>
  )
}
