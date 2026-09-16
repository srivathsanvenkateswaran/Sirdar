import { useId, useRef, useState, useSyncExternalStore, type RefObject } from 'react'
import type { RunSummary, SourcesSummary } from '../../api/types'
import { useAnchor } from '../../lib/anchor'
import { parseTime } from '../../lib/format'
import {
  sessionsShow,
  shownNumber,
  subscribeSessionsShow,
  withSource,
  type SessionsShow,
} from '../../lib/sessionsShow'
import KindChip from '../../ui/kind-chip'
import ProviderMark from '../../ui/provider-mark'
import SourceMark from '../../ui/source-mark'
import { stateWord } from '../../ui/status-badge'
import { useHoverCard } from './useHoverCard'
import './sessions.css'

export { CARD_CLOSE_MS, CARD_OPEN_MS } from './useHoverCard'

/** How many settled rows the list shows before "Show N more". Live rows all show. */
export const SHOWN_LIMIT = 8

const SETTLED_KEY = 'sirdar.settledCollapsed'

/** localStorage is absent in some tests and can throw in a locked-down webview. */
export function readSettledCollapsed(): boolean {
  try {
    return globalThis.localStorage?.getItem(SETTLED_KEY) === '1'
  } catch {
    return false
  }
}

function writeSettledCollapsed(value: boolean): void {
  try {
    globalThis.localStorage?.setItem(SETTLED_KEY, value ? '1' : '0')
  } catch {
    // A fold that cannot be remembered still holds this session.
  }
}

function stamp(run: RunSummary): number {
  const updated = Date.parse(run.updatedAt ?? '')
  if (!Number.isNaN(updated)) return updated
  const started = Date.parse(run.startedAt ?? '')
  return Number.isNaN(started) ? 0 : started
}

/** The newest `limit` runs, by their last change. */
export function recentRuns(runs: RunSummary[], limit = Infinity): RunSummary[] {
  return runs
    .slice()
    .sort((a, b) => stamp(b) - stamp(a))
    .slice(0, limit)
}

/** A run still happening or waiting on a person: the two the list keeps above the fold. */
export function isLive(run: Pick<RunSummary, 'status'>): boolean {
  return run.status === 'preparing' || run.status === 'running' || run.status === 'blocked'
}

/**
 * The list's two halves, each newest first: the runs still live or blocked,
 * and the settled ones under the divider.
 */
export function splitRuns(runs: RunSummary[]): { live: RunSummary[]; settled: RunSummary[] } {
  const sorted = recentRuns(runs)
  return { live: sorted.filter(isLive), settled: sorted.filter((run) => !isLive(run)) }
}

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/**
 * The age at a row's end, as short as it goes: `now`, `4m`, `18h`, `5d`.
 * Empty when the stamp is not one.
 */
export function shortAge(value: string | undefined, now = Date.now()): string {
  const ms = parseTime(value)
  if (Number.isNaN(ms)) return ''
  const delta = Math.max(0, now - ms)
  if (delta < MINUTE) return 'now'
  if (delta < HOUR) return `${Math.round(delta / MINUTE)}m`
  if (delta < DAY) return `${Math.round(delta / HOUR)}h`
  return `${Math.round(delta / DAY)}d`
}

/** lucide `chevron-down`, turned on the Settled divider when it is folded. */
function Chevron(): JSX.Element {
  return (
    <svg
      className="sd-sessions__chevron"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

/** lucide `folder`, for the card's workspace row. */
function FolderIcon(): JSX.Element {
  return (
    <svg
      className="sd-session-card__icon"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13c0 1.1.9 2 2 2Z" />
    </svg>
  )
}

export interface SessionsListProps {
  runs: RunSummary[]
  currentRunId?: string
  onOpen: (runId: string) => void
  /** The clock the ages are read against; tests hold it still. */
  now?: number
  /** The workspace's tracker and helpdesk, so each number sits under its own mark. */
  sources?: SourcesSummary
  /** Named on the card. */
  workspaceName?: string
  /**
   * The sidebar is the 56px rail: a row is its tile alone, so the card
   * carries the row's own number as well as the other one.
   */
  rail?: boolean
  /**
   * The run whose card is drawn open, in the flow under the list rather than
   * pinned beside its row — for the gallery, which has no pointer to rest.
   */
  pinnedCard?: string
}

/**
 * The workspace's sessions, one line each, newest first: the runs still
 * running or waiting on a person, then a "Settled" divider that folds the
 * finished ones away, remembered in this browser.
 *
 * A row is the ticket number under its source's mark — the tracker's key or
 * the helpdesk's number, on the Settings › General preference — and the age
 * at the end. Nothing else: no title, no kind. Those are on the card a row
 * opens after a moment's hover or focus, beside the sidebar: the title, the
 * other number with its product's name, the kind and state, the provider
 * and model, and the workspace. A dot over the tile's corner says a run is
 * live or blocked, and the row's accessible name says the same in words.
 * The row itself opens the run.
 *
 * There is one card element, drawn after the list as the sidebar's child
 * rather than inside the scrolling list, and `useHoverCard` says which row
 * it is for and when: 120ms of rest opens it, the next row takes it over
 * with no wait, and it stays 150ms after the pointer leaves so the pointer
 * can reach it.
 */
export default function SessionsList({
  runs,
  currentRunId,
  onOpen,
  now,
  sources,
  workspaceName,
  rail = false,
  pinnedCard,
}: SessionsListProps): JSX.Element | null {
  const [showAll, setShowAll] = useState(false)
  const [collapsed, setCollapsed] = useState(readSettledCollapsed)
  const show = useSyncExternalStore(
    subscribeSessionsShow,
    sessionsShow,
    () => 'tracker' as SessionsShow,
  )
  const hover = useHoverCard()
  const rows = useRef(new Map<string, HTMLButtonElement>())
  const cardEl = useRef<HTMLDivElement | null>(null)
  const id = useId()
  const cardId = `${id}-card`
  const settledId = `${id}-settled`

  const pinned = pinnedCard !== undefined
  const card = pinned ? pinnedCard : hover.open
  // The anchor is whichever row the card is for, read when the card is
  // measured rather than copied when it opened: a row that is re-drawn
  // (a run settling moves its row under the divider) is still found.
  const cardNow = useRef(card)
  cardNow.current = card
  const anchor = useRef<RefObject<HTMLElement | null>>({
    get current() {
      return cardNow.current ? (rows.current.get(cardNow.current) ?? null) : null
    },
  })
  useAnchor(card !== null && !pinned, anchor.current, cardEl, { beside: true, track: card })

  function toggleSettled(): void {
    const next = !collapsed
    setCollapsed(next)
    writeSettledCollapsed(next)
  }

  if (runs.length === 0) return null
  const at = now ?? Date.now()
  const { live, settled } = splitRuns(runs)
  const settledShown = showAll ? settled : settled.slice(0, SHOWN_LIMIT)
  const hidden = settled.length - settledShown.length
  const open = card ? runs.find((run) => run.runId === card) : undefined

  function renderRow(run: RunSummary): JSX.Element {
    const shown = shownNumber(run, show, sources)
    const running = run.status === 'preparing' || run.status === 'running'
    const blocked = run.status === 'blocked'
    const word = running || blocked ? `, ${stateWord(run.status)}` : ''
    const name = `${shown.text}, ${run.kind}${word}`
    const age = shortAge(run.updatedAt || run.startedAt, at)
    const isOpen = card === run.runId
    return (
      <button
        key={run.runId}
        ref={(el) => {
          if (el) rows.current.set(run.runId, el)
          else rows.current.delete(run.runId)
        }}
        type="button"
        className="sd-session-row"
        aria-current={run.runId === currentRunId ? 'page' : undefined}
        aria-label={name}
        aria-describedby={isOpen ? cardId : undefined}
        onClick={() => onOpen(run.runId)}
        onPointerEnter={pinned ? undefined : () => hover.enterRow(run.runId)}
        onPointerLeave={pinned ? undefined : () => hover.leaveRow(run.runId)}
        onFocus={pinned ? undefined : () => hover.focusRow(run.runId)}
        onBlur={pinned ? undefined : () => hover.blurRow(run.runId)}
      >
        <span className="sd-session-row__tile">
          <SourceMark adapter={shown.source?.adapter ?? shown.role} name={shown.source?.name} size="xs" />
          {running || blocked ? (
            <span
              className="sd-session-row__dot"
              data-live={running ? 'true' : undefined}
              data-blocked={blocked ? 'true' : undefined}
              aria-hidden="true"
            />
          ) : null}
        </span>
        <span className="sd-session-row__number" dir="ltr">
          {shown.text}
        </span>
        <span className="sd-session-row__age" dir="ltr">
          {age}
        </span>
      </button>
    )
  }

  function renderCard(run: RunSummary): JSX.Element {
    const shown = shownNumber(run, show, sources)
    // The other number with its product's name; a run with one number names
    // that one, so the card always says where the ticket lives. In the rail
    // the row shows no number at all, so the card carries the row's own too.
    const otherRole: SessionsShow = shown.role === 'tracker' ? 'helpdesk' : 'tracker'
    const hasOther = shown.other !== ''
    const numbers: { role: SessionsShow; text: string }[] = []
    if (rail || !hasOther) numbers.push({ role: shown.role, text: withSource(shown.text, shown.source) })
    if (hasOther) numbers.push({ role: otherRole, text: shown.other })
    return (
      <div
        id={cardId}
        ref={cardEl}
        className="sd-session-card"
        role="tooltip"
        data-static={pinned ? 'true' : undefined}
        onPointerEnter={pinned ? undefined : hover.enterCard}
        onPointerLeave={pinned ? undefined : hover.leaveCard}
      >
        <p className="sd-session-card__title" dir="auto">
          {run.title || shown.text}
        </p>
        <ul className="sd-session-card__rows">
          {numbers.map((number) => (
            <li key={number.role} className="sd-session-card__row">
              <SourceMark
                adapter={sources?.[number.role]?.adapter ?? number.role}
                name={sources?.[number.role]?.name}
                size="xs"
              />
              <span className="sd-session-card__mono" dir="ltr">
                {number.text}
              </span>
            </li>
          ))}
          <li className="sd-session-card__row">
            <KindChip kind={run.kind} />
            <span>{stateWord(run.status)}</span>
          </li>
          <li className="sd-session-card__row">
            <ProviderMark provider={run.provider} size="sm" />
            <span className="sd-session-card__mono" dir="ltr">
              {run.model || 'model unknown'}
            </span>
          </li>
          {workspaceName ? (
            <li className="sd-session-card__row">
              <FolderIcon />
              <span>{workspaceName}</span>
            </li>
          ) : null}
        </ul>
      </div>
    )
  }

  return (
    <>
      <nav className="sd-sidebar__sessions" aria-label="Sessions">
        {live.map(renderRow)}
        {settled.length > 0 ? (
          <button
            type="button"
            className="sd-sessions__settled"
            aria-expanded={!collapsed}
            aria-controls={settledId}
            onClick={toggleSettled}
          >
            <span>Settled</span>
            <span className="sd-sessions__rule" aria-hidden="true" />
            <Chevron />
          </button>
        ) : null}
        {settled.length > 0 && !collapsed ? (
          <div id={settledId} className="sd-sessions__settled-rows">
            {settledShown.map(renderRow)}
            {hidden > 0 ? (
              <button
                type="button"
                className="sd-sessions__more"
                aria-label={rail ? `Show ${hidden} more` : undefined}
                onClick={() => setShowAll(true)}
              >
                {rail ? `+${hidden}` : `Show ${hidden} more`}
              </button>
            ) : null}
          </div>
        ) : null}
      </nav>
      {open ? renderCard(open) : null}
    </>
  )
}
