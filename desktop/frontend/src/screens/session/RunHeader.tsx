import { useSyncExternalStore } from 'react'
import type { RunDetail, SourcesSummary } from '../../api/types'
import { elapsed } from '../../lib/events'
import { costOrUnknown } from '../../lib/format'
import { sessionsShow, shownNumber, subscribeSessionsShow, type SessionsShow } from '../../lib/sessionsShow'
import { BELOW_STANDARD, useMediaQuery } from '../../lib/useMediaQuery'
import Age from '../../components/Age'
import Button from '../../ui/button'
import KindChip from '../../ui/kind-chip'
import ProviderMark from '../../ui/provider-mark'
import { AssignedTo } from '../../ui/run-card/Avatar'
import SourceMark from '../../ui/source-mark'
import StatusBadge, { type SdStatus } from '../../ui/status-badge'
import { SessionLayoutSwitcher } from '../../components/session/RunHeader'

/**
 * The compact run header: key under its source's mark, kind, state, the
 * ticket's title, who it is assigned to, then the run's figures — provider
 * and model, the clock, turns, cost — and Cancel while the run is live.
 *
 * Under 1200 the provider, the model and the turns fold into the figures'
 * title and the clock and the cost stay drawn.
 */
export function statsTitle(detail: { provider: string; model?: string; usage?: { turns?: number } }): string {
  const turns = detail.usage?.turns ?? 0
  return [detail.provider, detail.model || 'model unknown', `${turns} ${turns === 1 ? 'turn' : 'turns'}`].join(' · ')
}

export interface RunHeaderProps {
  detail: RunDetail
  title?: string
  sources?: SourcesSummary
  live: boolean
  terminal: boolean
  /**
   * The model the provider's init line named, for a run whose record has
   * not reported one: the workspace configured no model, so state.json
   * says '' while the log says which one answered.
   */
  fallbackModel?: string
  /** Cancel is offered while the run is live; absent when this window cannot stop it. */
  onCancel?: () => void
  cancelDisabled?: boolean
  cancelTitle?: string
}

export default function RunHeader({
  detail,
  title,
  sources,
  live,
  terminal,
  fallbackModel = '',
  onCancel,
  cancelDisabled = false,
  cancelTitle,
}: RunHeaderProps): JSX.Element {
  const show = useSyncExternalStore(subscribeSessionsShow, sessionsShow, () => 'tracker' as SessionsShow)
  const compact = useMediaQuery(BELOW_STANDARD)
  const shown = shownNumber(detail, show, sources)
  const modelWord = detail.model || fallbackModel || 'model unknown'

  return (
    <header className="sc-topbar">
      <SourceMark adapter={shown.source?.adapter ?? shown.role} name={shown.source?.name} size="sm" />
      <h1 className="sc-key" title={shown.other || undefined}>
        {shown.text}
      </h1>
      <KindChip kind={detail.kind} />
      <StatusBadge status={detail.status as SdStatus} detail={detail.status === 'blocked' ? 'waiting on you' : undefined} />
      <span className="sc-title" title={title} dir="auto">
        {title ?? detail.title ?? ''}
      </span>
      <AssignedTo name={detail.assignee ?? ''} />
      <div
        className="sc-stats"
        data-compact={compact ? 'true' : undefined}
        title={compact ? statsTitle({ ...detail, model: detail.model || fallbackModel }) : undefined}
      >
        {compact ? null : (
          <span className="sc-stat sc-provider">
            <ProviderMark provider={detail.provider} size="sm" />
            <span className="sc-provider__name">{detail.provider}</span>
            <span className="sc-provider__model" dir="ltr">
              {modelWord}
            </span>
          </span>
        )}
        <span className="sc-stat">
          <b>
            <Age active={live} format={(now) => elapsed(detail, now)} />
          </b>
        </span>
        {compact ? null : (
          <span className="sc-stat">
            <b>{detail.usage?.turns ?? 0}</b> {detail.usage?.turns === 1 ? 'turn' : 'turns'}
          </span>
        )}
        <span className="sc-stat">
          <b>{costOrUnknown(detail.usage?.costUsd, live)}</b>
        </span>
      </div>
      <SessionLayoutSwitcher />
      {terminal || !onCancel ? null : (
        <Button variant="ghost" onClick={onCancel} disabled={cancelDisabled} title={cancelTitle}>
          Cancel
        </Button>
      )}
    </header>
  )
}
