import { useSyncExternalStore, type ReactNode } from 'react'
import type { RunDetail, SourcesSummary } from '../../api/types'
import { elapsed } from '../../lib/events'
import { costOrUnknown } from '../../lib/format'
import { SESSION_LAYOUT_OPTIONS, sessionLayout, setSessionLayout, subscribeSessionLayout, type SessionLayout } from '../../lib/sessionLayout'
import { shownNumber, type SessionsShow } from '../../lib/sessionsShow'
import Age from '../Age'
import KindChip from '../../ui/kind-chip'
import ProviderMark from '../../ui/provider-mark'
import { AssignedTo } from '../../ui/run-card/Avatar'
import AskedFor from './AskedFor'
import SourceMark from '../../ui/source-mark'
import StatusBadge, { type SdStatus } from '../../ui/status-badge'
import { ConversationLayoutIcon, DocumentLayoutIcon, WorkbenchLayoutIcon } from './icons'

const LIVE = new Set(['preparing', 'running'])

/** What the badge says after the state word, on this screen: `waiting on you`, `note saved`, `committed f144936`. */
export function badgeDetail(detail: RunDetail, notePath?: string): string | undefined {
  switch (detail.status) {
    case 'blocked':
      return 'waiting on you'
    case 'completed':
      if (detail.kind === 'fix') return detail.fix?.commit ? `committed ${detail.fix.commit.slice(0, 7)}` : undefined
      return notePath || detail.notes.length > 0 ? 'note saved' : undefined
    default:
      return undefined
  }
}

/** "claude · opus · 4 turns": the stats in one line, for a title when the window is too narrow to draw them. */
export function statsTitle(detail: { provider: string; model?: string; usage?: { turns?: number } }): string {
  const turns = detail.usage?.turns ?? 0
  return [detail.provider, detail.model || 'model unknown', `${turns} ${turns === 1 ? 'turn' : 'turns'}`].join(' · ')
}

const LAYOUT_ICONS: Record<SessionLayout, JSX.Element> = {
  conversation: <ConversationLayoutIcon />,
  document: <DocumentLayoutIcon />,
  workbench: <WorkbenchLayoutIcon />,
}

/** The three-icon switcher: the same setting as Settings › General › Session layout. */
export function LayoutSwitcher({
  value,
  onChange,
}: {
  value: SessionLayout
  onChange: (layout: SessionLayout) => void
}): JSX.Element {
  return (
    <div className="sn-switch" role="radiogroup" aria-label="Session layout">
      {SESSION_LAYOUT_OPTIONS.map((o) => (
        <button
          key={o.id}
          type="button"
          role="radio"
          className="sn-switch__opt"
          aria-checked={o.id === value}
          aria-label={o.label}
          title={`${o.label} layout`}
          onClick={() => onChange(o.id)}
        >
          {LAYOUT_ICONS[o.id]}
        </button>
      ))}
    </div>
  )
}

/**
 * The switcher wired to the preference itself, for a header that does not
 * plumb the layout through its props: the Conversation and Workbench
 * headers drop this in and the Document header passes `LayoutSwitcher` the
 * value the dispatcher already holds.
 */
export function SessionLayoutSwitcher(): JSX.Element {
  const value = useSyncExternalStore(subscribeSessionLayout, sessionLayout, () => 'conversation' as SessionLayout)
  return <LayoutSwitcher value={value} onChange={setSessionLayout} />
}

export interface RunHeaderProps {
  detail: RunDetail
  /** The ticket's title, when the tracker's queue lists it. */
  title?: string
  sources?: SourcesSummary
  /** Which number heads the screen, on the Sessions show preference. */
  show: SessionsShow
  /** `compact` is A's and B's topbar; `gauges` is C's, with bars against the caps. */
  variant?: 'compact' | 'gauges'
  /** Under 1200: the provider, the model and the turns fold into the stats' title. */
  narrow?: boolean
  /** The filed note, for the badge's `note saved`. */
  notePath?: string
  /** The header's action at the end: Open note, Open worktree. Stopping a live run is the composer's Stop. */
  action?: ReactNode
  switcher?: ReactNode
}

function Gauge({
  label,
  value,
  cap,
  text,
  warn,
}: {
  label: string
  value: number
  cap: number
  text: string
  warn?: boolean
}): JSX.Element {
  const pct = cap > 0 ? Math.min(100, Math.round((value / cap) * 100)) : 0
  return (
    <span className="sn-gauge" data-level={warn || pct >= 80 ? 'warn' : undefined} title={`${label}: ${text}`}>
      <span className="sn-gauge__label">{label}</span>
      <span>{text}</span>
      <span className="sn-gauge__bar" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={label}>
        <span className="sn-gauge__fill" style={{ inlineSize: `${pct}%` }} />
      </span>
    </span>
  )
}

/**
 * The run's header: key under its source mark, kind, state badge, the
 * ticket's title, who it is assigned to, then the figures — provider and
 * model, the clock, turns, cost — and one action at the end. Stopping a
 * live run belongs to the composer's Stop, beside the box the reader is
 * already looking at, not to a text button in the window corner. The `gauges`
 * variant draws turns, minutes and cost as bars against the run's caps in
 * place of the flat figures, which is what the Workbench layout asks for.
 */
export default function RunHeader({
  detail,
  title,
  sources,
  show,
  variant = 'compact',
  narrow = false,
  notePath,
  action,
  switcher,
}: RunHeaderProps): JSX.Element {
  const live = LIVE.has(detail.status)
  const terminal = detail.status === 'completed' || detail.status === 'failed' || detail.status === 'over_budget'
  const shown = shownNumber(detail, show, sources)
  const turns = detail.usage?.turns ?? 0
  const budget = detail.budget
  const startedMs = Date.parse(detail.startedAt)
  const endMs = live ? Date.now() : Date.parse(detail.updatedAt)
  const minutes = Number.isNaN(startedMs) || Number.isNaN(endMs) ? 0 : Math.max(0, (endMs - startedMs) / 60_000)

  return (
    <header className={variant === 'gauges' ? 'sn-head sn-head--gauges' : 'sn-head'} data-live={live ? 'true' : undefined}>
      <SourceMark adapter={shown.source?.adapter ?? shown.role} name={shown.source?.name} size="sm" />
      <h1 className="sn-head__key" title={shown.other || undefined}>
        {shown.text}
      </h1>
      <KindChip kind={detail.kind} />
      <StatusBadge status={detail.status as SdStatus} detail={badgeDetail(detail, notePath)} />
      <span className="sn-head__title" title={title} dir="auto">
        {title ?? ''}
      </span>
      <AssignedTo name={detail.assignee ?? ''} />
      <AskedFor instruction={detail.instruction} />
      {variant === 'gauges' ? (
        <div className="sn-gauges">
          <span className="sn-head__stat sn-head__provider">
            <ProviderMark provider={detail.provider} size="sm" />
            <span className="sn-head__model" dir="ltr">
              {detail.model || 'model unknown'}
            </span>
          </span>
          <Gauge label="turns" value={turns} cap={budget.maxTurns} text={`${turns} of ${budget.maxTurns}`} />
          <Gauge
            label="minutes"
            value={minutes}
            cap={budget.maxMinutes}
            text={`${Math.floor(minutes)} of ${budget.maxMinutes}`}
          />
          <Gauge
            label="cost"
            value={detail.usage?.costUsd ?? 0}
            cap={budget.maxUsd}
            text={`${costOrUnknown(detail.usage?.costUsd, live)} of $${budget.maxUsd}`}
          />
        </div>
      ) : (
        <div className="sn-head__stats" data-compact={narrow ? 'true' : undefined} title={narrow ? statsTitle(detail) : undefined}>
          {narrow ? null : (
            <span className="sn-head__stat sn-head__provider">
              <ProviderMark provider={detail.provider} size="sm" />
              <span className="sn-head__provider-name">{detail.provider}</span>
              <span className="sn-head__model" dir="ltr">
                {detail.model || 'model unknown'}
              </span>
            </span>
          )}
          <span className="sn-head__stat">
            <b>
              <Age active={live} format={(now) => elapsed(detail, now)} />
            </b>
          </span>
          {narrow ? null : (
            <span className="sn-head__stat">
              <b>{turns}</b> {detail.status === 'blocked' || live ? `of ${budget.maxTurns} turns` : turns === 1 ? 'turn' : 'turns'}
            </span>
          )}
          {detail.status === 'blocked' && !(detail.usage?.costUsd > 0) ? null : (
            <span className="sn-head__stat">
              <b>{costOrUnknown(detail.usage?.costUsd, live)}</b>
            </span>
          )}
        </div>
      )}
      {switcher}
      {terminal ? action : null}
    </header>
  )
}
