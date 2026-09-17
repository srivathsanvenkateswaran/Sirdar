import { memo, useEffect, useId, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import type { RunDetail, SourcesSummary, Transport } from '../../api/types'
import { useAnchor } from '../../lib/anchor'
import { probeRender } from '../../lib/renderProbe'
import { parseBundle } from '../../lib/bundle'
import { duration, parseTime, usd } from '../../lib/format'
import { SESSION_LAYOUT_OPTIONS, sessionLayout, setSessionLayout, subscribeSessionLayout, type SessionLayout } from '../../lib/sessionLayout'
import { helpdeskNumber, sessionsShow, shownNumber, subscribeSessionsShow, withSource, type SessionsShow } from '../../lib/sessionsShow'
import { useNow } from '../../lib/useNow'
import KindChip from '../../ui/kind-chip'
import ProviderMark from '../../ui/provider-mark'
import SourceMark from '../../ui/source-mark'
import StatusBadge, { stateWord, type SdStatus } from '../../ui/status-badge'
import { ConversationLayoutIcon, CopyIcon, DocumentLayoutIcon, InfoIcon, OpenIcon, WorkbenchLayoutIcon } from './icons'

const LIVE = new Set(['preparing', 'running'])

/** How long `Copied` stands before the button says `Copy` again. */
const COPIED_MS = 1200

/** What the badge says after the state word, as its title: `waiting on you`, `note saved`, `committed f144936`. */
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

/** The switcher wired to the preference itself, for a header given no `switcher` of its own. */
export function SessionLayoutSwitcher(): JSX.Element {
  const value = useSyncExternalStore(subscribeSessionLayout, sessionLayout, () => 'conversation' as SessionLayout)
  return <LayoutSwitcher value={value} onChange={setSessionLayout} />
}

export interface Gauge {
  label: string
  /** What has been spent: `17`, `03:15`, `$1.02`. */
  value: string
  /** What the run may spend: `of 60`, `of 20:00`, `of $5.00`. */
  cap: string
  /** 0..100, for the bar and its meter. */
  pct: number
  /** The figure is not known yet: the bar stays empty and the value is a dash. */
  na?: boolean
}

/**
 * How long the run has been going: to now while it is live or waiting, to
 * the last update once it has stopped.
 */
export function elapsedOf(detail: RunDetail, now = Date.now()): number {
  const start = parseTime(detail.startedAt)
  if (Number.isNaN(start)) return 0
  const end = LIVE.has(detail.status) || detail.status === 'blocked' ? now : parseTime(detail.updatedAt) || now
  return Math.max(0, end - start)
}

/**
 * Turns, minutes and cost against `state.json`'s caps — the three bars the
 * Workbench used to draw in its header, which now live in the popover with
 * the rest of the run's figures.
 */
export function budgetGauges(detail: RunDetail, now = Date.now()): Gauge[] {
  const live = LIVE.has(detail.status)
  const turns = detail.usage?.turns ?? 0
  const maxTurns = detail.budget?.maxTurns ?? 0
  const elapsedMs = elapsedOf(detail, now)
  const maxMinutes = detail.budget?.maxMinutes ?? 0
  const cost = detail.usage?.costUsd ?? 0
  const maxUsd = detail.budget?.maxUsd ?? 0
  const pct = (v: number, cap: number) => (cap > 0 ? Math.min(100, Math.round((v / cap) * 100)) : 0)
  // A live run's cost is only settled when the provider reports it.
  const costKnown = cost > 0 || (!live && detail.status !== 'blocked')
  return [
    { label: 'turns', value: String(turns), cap: maxTurns ? `of ${maxTurns}` : '', pct: pct(turns, maxTurns) },
    {
      label: 'minutes',
      value: duration(elapsedMs),
      cap: maxMinutes ? `of ${duration(maxMinutes * 60_000)}` : '',
      pct: pct(elapsedMs, maxMinutes * 60_000),
    },
    {
      label: 'cost',
      value: costKnown ? usd(cost) : '—',
      cap: maxUsd ? `of ${usd(maxUsd)}` : '',
      pct: costKnown ? pct(cost, maxUsd) : 0,
      na: costKnown ? undefined : true,
    },
  ]
}

/** `09:05:31`, the wall clock a run started at. */
function clockOf(t: string | undefined): string {
  const ms = parseTime(t)
  if (Number.isNaN(ms)) return ''
  return new Date(ms).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function GaugeRow({ gauge }: { gauge: Gauge }): JSX.Element {
  return (
    <div
      className="sn-gauge"
      data-na={gauge.na ? 'true' : undefined}
      data-level={gauge.pct >= 80 ? 'warn' : undefined}
      title={gauge.na ? 'Known when the run ends' : undefined}
    >
      <span className="sn-gauge__label">{gauge.label}</span>
      <span className="sn-gauge__fig" dir="ltr">
        <b>{gauge.value}</b> {gauge.cap}
      </span>
      <span
        className="sn-gauge__bar"
        role="meter"
        aria-label={gauge.label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={gauge.pct}
        aria-valuetext={`${gauge.value} ${gauge.cap}`.trim()}
      >
        <span className="sn-gauge__fill" style={{ inlineSize: `${gauge.pct}%` }} />
      </span>
    </div>
  )
}

interface AboutProps {
  detail: RunDetail
  title?: string
  model: string
  onOpenRunDir?: () => void
}

/**
 * Everything the header no longer says: the ticket's title, who it is
 * assigned to, the provider and the model it ran on (with the stretches,
 * when it changed model partway), when it started and how long it took, the
 * three budget bars, and the run's own id.
 */
function About({ detail, title, model, onOpenRunDir }: AboutProps): JSX.Element {
  const live = LIVE.has(detail.status)
  const [copied, setCopied] = useState(false)
  const [copyFailed, setCopyFailed] = useState('')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  // The minutes bar ticks while the run is live or waiting; a finished run's
  // figures are state.json's and nothing moves.
  const now = useNow(1000, live || detail.status === 'blocked')
  const gauges = budgetGauges(detail, now)
  const segments = detail.modelSegments ?? []

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  async function copyRunId(): Promise<void> {
    setCopyFailed('')
    try {
      await navigator.clipboard?.writeText(detail.runId)
      setCopied(true)
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), COPIED_MS)
    } catch {
      setCopyFailed('The run id could not be copied')
    }
  }

  return (
    <>
      <p className="sn-about__title" dir="auto">
        {title || detail.title || 'No title'}
      </p>
      <dl className="sn-about__facts">
        <dt>Assignee</dt>
        <dd dir="auto">{detail.assignee || 'Nobody'}</dd>
        <dt>Model</dt>
        <dd>
          <span className="sn-about__model">
            <ProviderMark provider={detail.provider} size="sm" />
            <span dir="ltr">
              {detail.provider} · {model}
            </span>
          </span>
          {segments.length > 1 ? (
            <ul className="sn-about__segments">
              {segments.map((segment, i) => (
                <li key={`${segment.at}-${i}`}>
                  <span dir="ltr">{clockOf(segment.at)}</span> <b dir="ltr">{segment.model}</b>
                  {segment.why ? ` · ${segment.why}` : ''}
                </li>
              ))}
            </ul>
          ) : null}
        </dd>
        <dt>Started</dt>
        <dd>
          <span dir="ltr">{clockOf(detail.startedAt)}</span> · {duration(elapsedOf(detail, now))}
        </dd>
      </dl>
      <div className="sn-about__gauges">
        {gauges.map((gauge) => (
          <GaugeRow key={gauge.label} gauge={gauge} />
        ))}
      </div>
      <div className="sn-about__run">
        <span className="sn-about__runid" dir="ltr">
          {detail.runId}
        </span>
        <button type="button" className="sn-about__act" onClick={() => void copyRunId()}>
          <CopyIcon />
          {copied ? 'Copied' : 'Copy'}
        </button>
        {onOpenRunDir ? (
          <button type="button" className="sn-about__act" onClick={onOpenRunDir}>
            <OpenIcon />
            Open run folder
          </button>
        ) : null}
      </div>
      {copyFailed ? (
        <p className="sn-about__failed" role="alert">
          {copyFailed}
        </p>
      ) : null}
    </>
  )
}

export interface RunHeaderProps {
  detail: RunDetail
  /** The ticket's title, when the tracker's queue lists it. It shows in the popover, never in the row. */
  title?: string
  /** The workspace's tracker and helpdesk, for the mark beside the number. */
  sources?: SourcesSummary
  /** The Workbench's mono register; the content and its order are the same either way. */
  variant?: 'default' | 'workbench'
  /** The filed note, for the state chip's `note saved`. */
  notePath?: string
  /**
   * The model the provider's init line named, for a run whose record has
   * none: the workspace configured no model, so state.json says '' while
   * the log says which one answered.
   */
  fallbackModel?: string
  /** The switcher, when the layout already holds the preference; else the header wires its own. */
  switcher?: ReactNode
  /** For the ticket's page and for Open run folder; both are skipped without it. */
  transport?: Transport
  workspaceId?: string
}

/**
 * The run's header, the same row in all three layouts: which ticket, what
 * kind of run, what state it is in, and the ticket's number in the other
 * system. Four things, 56px, one line.
 *
 * Everything else a run has — the title, the assignee, the provider and
 * model, the clock, the budget bars, the run id — is behind the `i` button
 * at the end of the row, because the owner reads the header on every screen
 * and needs it to say what run this is, not to recite the run. Stopping a
 * live run belongs to the composer's Stop, beside the box the reader is
 * already looking at.
 *
 * The Session renders it once, above the layout, and it is memoised on its
 * props: a layout that redraws for a streamed line — or is swapped for
 * another layout entirely — leaves the header's own DOM nodes alone, so the
 * popover stays open and the row does not blink. Everything it takes is a
 * value or an identity the dispatcher holds still; pass no `switcher` and
 * it wires its own, which keeps the prop stable across a switch.
 */
function RunHeaderRow({
  detail,
  title,
  sources,
  variant = 'default',
  notePath,
  fallbackModel = '',
  switcher,
  transport,
  workspaceId,
}: RunHeaderProps): JSX.Element {
  probeRender('RunHeader')
  const show = useSyncExternalStore(subscribeSessionsShow, sessionsShow, () => 'tracker' as SessionsShow)
  const [open, setOpen] = useState(false)
  const [links, setLinks] = useState<{ trackerUrl?: string; helpdeskUrl?: string }>({})
  const root = useRef<HTMLSpanElement | null>(null)
  const button = useRef<HTMLButtonElement | null>(null)
  const popover = useRef<HTMLDivElement | null>(null)
  const id = useId()
  const popoverId = `${id}-about`

  const live = LIVE.has(detail.status)
  const shown = shownNumber(detail, show, sources)
  const other = otherNumber(detail, shown.role, sources)
  const word = stateWord(detail.status)
  const said = badgeDetail(detail, notePath)
  const model = detail.model || fallbackModel || 'model unknown'

  useAnchor(open, root, popover, { align: 'end' })

  // The ticket's two pages are named in the prompt the run was given, which
  // is the one place the service records them. Read once, and only when
  // there is a second number to hang a link on.
  useEffect(() => {
    if (!other.text || !transport?.prompt || !workspaceId) return
    let alive = true
    transport
      .prompt(workspaceId, detail.runId)
      .then((prompt) => {
        if (!alive) return
        const { ticket } = parseBundle(prompt)
        setLinks({ trackerUrl: ticket['Tracker URL'] || undefined, helpdeskUrl: ticket['Helpdesk URL'] || undefined })
      })
      .catch(() => {
        // A number without a page is still the number; it stays plain text.
      })
    return () => {
      alive = false
    }
  }, [transport, workspaceId, detail.runId, other.text])

  useEffect(() => {
    if (!open) return
    function onDown(event: globalThis.MouseEvent): void {
      if (root.current && !root.current.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  useEffect(() => {
    if (open) popover.current?.focus()
  }, [open])

  const href = other.role === 'helpdesk' ? links.helpdeskUrl : links.trackerUrl
  const openRunDir =
    transport?.openRunDir && workspaceId
      ? () => void transport.openRunDir!(workspaceId, detail.runId)
      : undefined

  return (
    <header className="sn-head" data-variant={variant} data-live={live ? 'true' : undefined}>
      <SourceMark adapter={shown.source?.adapter ?? shown.role} name={shown.source?.name} size="sm" />
      <h1 className="sn-head__key" title={shown.other || undefined} dir="ltr">
        {shown.text}
      </h1>
      <KindChip kind={detail.kind} />
      {/* The state is the word; what the word means here is its title, so the
          row says the same thing on every run instead of a sentence that
          grows. Under 1024 the chip is a dot and the title is all there is. */}
      <span className="sn-head__state" title={said ? `${word} · ${said}` : word}>
        <StatusBadge status={detail.status as SdStatus} />
      </span>
      {other.text ? (
        href ? (
          <a
            className="sn-head__other"
            href={href}
            target="_blank"
            rel="noreferrer noopener"
            dir="ltr"
            title={`Open ${other.long}`}
          >
            {other.text}
            <OpenIcon />
          </a>
        ) : (
          <span className="sn-head__other" dir="ltr" title={other.long}>
            {other.text}
          </span>
        )
      ) : null}
      <span className="sn-head__rest" />
      {switcher ?? <SessionLayoutSwitcher />}
      <span className="sn-head__about" ref={root}>
        <button
          ref={button}
          type="button"
          className="sn-head__info"
          aria-label="About this run"
          title="About this run"
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-controls={open ? popoverId : undefined}
          onClick={() => setOpen((was) => !was)}
        >
          <InfoIcon />
        </button>
        {open ? (
          <div
            id={popoverId}
            ref={popover}
            className="sn-about"
            role="dialog"
            aria-label="About this run"
            tabIndex={-1}
            onKeyDown={(event) => {
              if (event.key !== 'Escape') return
              event.preventDefault()
              event.stopPropagation()
              setOpen(false)
              button.current?.focus()
            }}
          >
            <About detail={detail} title={title} model={model} onOpenRunDir={openRunDir} />
          </div>
        ) : null}
      </span>
    </header>
  )
}

/**
 * The header as the Session mounts it: the same row, redrawn only when one
 * of its own props changes.
 */
const RunHeader = memo(RunHeaderRow)
RunHeader.displayName = 'RunHeader'
export default RunHeader

/**
 * The number the row does not head with: the helpdesk's when the reader is
 * shown the tracker's key, the tracker's key when they are shown the
 * helpdesk's. `long` names its system, for the title.
 */
function otherNumber(
  detail: RunDetail,
  shownRole: SessionsShow,
  sources?: SourcesSummary,
): { text: string; long: string; role: SessionsShow } {
  if (shownRole === 'helpdesk') {
    return { text: detail.key, long: withSource(detail.key, sources?.tracker), role: 'tracker' }
  }
  const helpdesk = detail.helpdeskKey?.trim() ?? ''
  if (!helpdesk) return { text: '', long: '', role: 'helpdesk' }
  const text = helpdeskNumber(helpdesk)
  return { text, long: withSource(text, sources?.helpdesk), role: 'helpdesk' }
}
