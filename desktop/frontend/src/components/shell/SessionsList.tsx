import {
  memo,
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
  type RefObject,
} from 'react'
import type { RunSummary, SearchHit, SourcesSummary } from '../../api/types'
import { pointAnchor, useAnchor, type Anchorable } from '../../lib/anchor'
import { parseTime } from '../../lib/format'
import { probeRender } from '../../lib/renderProbe'
import {
  forceGroup,
  forgetRun,
  groupRuns,
  isLiveStatus,
  isSnoozed,
  isUnread,
  markRead,
  markUnread,
  newestFirst,
  pruneSnoozes,
  renameRun,
  resetName,
  sessionPrefs,
  setArchived,
  snoozeRun,
  snoozeUntil,
  subscribeSessionPrefs,
  togglePin,
  unsnoozeRun,
  type SessionPrefs,
  type SnoozeChoice,
} from '../../lib/sessionPrefs'
import { matchRun, splitMatch, splitText, type RunMatch } from '../../lib/sessionSearch'
import Age from '../Age'
import {
  helpdeskNumber,
  sessionsShow,
  shownNumber,
  subscribeSessionsShow,
  withSource,
  type SessionsShow,
} from '../../lib/sessionsShow'
import Button from '../../ui/button'
import ContextMenu, { type MenuEntry } from '../../ui/context-menu'
import Dialog from '../../ui/dialog'
import KindChip from '../../ui/kind-chip'
import ProviderMark from '../../ui/provider-mark'
import SourceMark from '../../ui/source-mark'
import { stateWord } from '../../ui/status-badge'
import { useHoverCard, type HoverCard } from './useHoverCard'
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

/** The newest `limit` runs, by their last change. */
export function recentRuns(runs: RunSummary[], limit = Infinity): RunSummary[] {
  return newestFirst(runs).slice(0, limit)
}

/** A run still happening or waiting on a person: the two the list keeps above the fold. */
export function isLive(run: Pick<RunSummary, 'status'>): boolean {
  return isLiveStatus(run.status)
}

/**
 * The list's two halves with no record applied, each newest first: the runs
 * still live or blocked, and the settled ones under the divider.
 */
export function splitRuns(runs: RunSummary[]): { live: RunSummary[]; settled: RunSummary[] } {
  const sorted = newestFirst(runs)
  return { live: sorted.filter(isLive), settled: sorted.filter((run) => !isLive(run)) }
}

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/** How often a row's age is re-read. Its finest word is the minute. */
export const AGE_PERIOD_MS = 15_000

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

/** The day a run started, for the delete question: "16 Sept 2026". */
export function runDate(run: Pick<RunSummary, 'startedAt'>): string {
  const ms = parseTime(run.startedAt)
  if (Number.isNaN(ms)) return 'unknown date'
  return new Date(ms).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
}

/** A local instant as `<input type="datetime-local">` writes it. */
export function toLocalInput(ms: number): string {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** lucide `chevron-down`, turned on a divider when it is folded. */
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

/** lucide `ellipsis`, the dots at a row's end. */
function DotsIcon(): JSX.Element {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <circle cx="12" cy="12" r="1" />
      <circle cx="19" cy="12" r="1" />
      <circle cx="5" cy="12" r="1" />
    </svg>
  )
}

/** lucide `pin`, on the Pinned group's label. */
function PinIcon(): JSX.Element {
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
      <path d="M12 17v5" />
      <path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z" />
    </svg>
  )
}

/** The tracker and helpdesk pages of a run's ticket, read off its prompt when the menu opens. */
export interface RunLinks {
  trackerUrl?: string
  helpdeskUrl?: string
}

/**
 * What the row's menu can reach beyond the list itself. Every one is
 * optional: an item whose action is absent is left out of the menu, which
 * is how a browser served by `sirdar serve` gets no "Open the run folder".
 */
export interface SessionActions {
  /** Removes the run's directory. Rejects with the reason, which the dialog shows. */
  onDelete?: (runId: string) => Promise<void>
  /** Settings › General. */
  onOpenSettings?: () => void
  /** Opens a note the run recorded, in the desktop's Markdown editor. */
  onOpenNote?: (runId: string, path: string) => void
  /** Reveals the run's directory in the desktop's file manager. */
  onOpenRunDir?: (runId: string) => void
  /** The ticket's pages, for Copy ▸ and Open ▸; asked for when the menu opens. */
  loadLinks?: (runId: string) => Promise<RunLinks>
  /** A word to the window: "Copied ticket key." */
  onToast?: (text: string, tone?: 'info' | 'error') => void
}

export type SearchMode = 'sessions' | 'notes'

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
  /** Keys the local record: pins, aliases and the rest are per workspace. */
  workspaceId?: string
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
  actions?: SessionActions
  /**
   * The list's first line, above the groups: the sidebar's search field.
   * It scrolls with nothing — sidebar.css pins it to the top of the list.
   */
  head?: ReactNode
  /** The search field's text. Non-empty, the list is the rows that answer it. */
  query?: string
  /** `notes`: the list is `hits`, one row per run with its excerpt under it. */
  mode?: SearchMode
  hits?: SearchHit[]
  hitsState?: 'idle' | 'loading' | 'error'
  hitsError?: string
}

/** A run that a query answers, with where. */
interface Matched {
  run: RunSummary
  match: RunMatch
}

/**
 * One row: the ticket number under its source's mark and the age at the end,
 * with the dots that open its menu.
 *
 * Memoised on its own inputs, so a render of the list — the card opening on
 * another row, a store emit that changed a different run — draws nothing
 * here. `hover` is one object per change of the open card, which is what
 * makes the memo hold; a row opens and closes the card through it.
 */
const SessionRow = memo(function SessionRow({
  run,
  show,
  sources,
  current,
  isOpen,
  cardId,
  now,
  hover,
  pinned,
  alias,
  unread,
  menuOpen,
  match,
  excerpts,
  onOpen,
  onMenu,
  register,
}: {
  run: RunSummary
  show: SessionsShow
  sources?: SourcesSummary
  current: boolean
  isOpen: boolean
  cardId: string
  /** A still clock for tests; the row ticks on its own without one. */
  now?: number
  hover: HoverCard
  pinned: boolean
  /** The local name, drawn in place of the number. */
  alias?: string
  unread: boolean
  /** This row's menu is up: the dots stay shown. */
  menuOpen: boolean
  /** Where the query matched, when one is active. */
  match?: RunMatch
  /** Search-notes excerpts under the row, each cut around its match. */
  excerpts?: [string, string, string][]
  onOpen: (runId: string) => void
  onMenu: (runId: string, anchor: Anchorable) => void
  register: (runId: string, el: HTMLButtonElement | null) => void
}): JSX.Element {
  probeRender('SessionRow')
  const shown = shownNumber(run, show, sources)
  const running = run.status === 'preparing' || run.status === 'running'
  const blocked = run.status === 'blocked'
  const word = running || blocked ? `, ${stateWord(run.status)}` : ''
  const label = alias ?? shown.text
  const name = `${label}, ${run.kind}${word}${unread ? ', unread' : ''}`
  const stamp = run.updatedAt || run.startedAt
  const id = run.runId

  // The match is drawn in the label when it is the label; else on a line
  // under it, so the reader sees what the query hit.
  const inLabel = match && match.text === label
  const labelParts = inLabel ? splitMatch(match) : null
  const below = match && !inLabel ? splitMatch(match) : null

  function onContextMenu(event: ReactMouseEvent): void {
    event.preventDefault()
    onMenu(id, pointAnchor(event.clientX, event.clientY))
  }

  function onKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>): void {
    if (event.key === 'ContextMenu' || (event.key === 'F10' && event.shiftKey)) {
      event.preventDefault()
      onMenu(id, event.currentTarget)
    }
  }

  return (
    <div
      className="sd-session-row"
      data-current={current ? 'true' : undefined}
      data-unread={unread ? 'true' : undefined}
      data-menu={menuOpen ? 'true' : undefined}
    >
      <button
        ref={(el) => register(id, el)}
        type="button"
        className="sd-session-row__open"
        aria-current={current ? 'page' : undefined}
        aria-label={name}
        aria-describedby={isOpen ? cardId : undefined}
        onClick={() => onOpen(id)}
        onDoubleClick={onContextMenu}
        onContextMenu={onContextMenu}
        onKeyDown={onKeyDown}
        onPointerEnter={pinned ? undefined : () => hover.enterRow(id)}
        onPointerLeave={pinned ? undefined : () => hover.leaveRow(id)}
        onPointerDown={pinned ? undefined : hover.pressRow}
        onFocus={pinned ? undefined : () => hover.focusRow(id)}
        onBlur={pinned ? undefined : () => hover.blurRow(id)}
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
        <span className="sd-session-row__text">
          <span className="sd-session-row__number" dir={alias ? 'auto' : 'ltr'}>
            {labelParts ? (
              <>
                {labelParts[0]}
                <mark className="sd-session-row__mark">{labelParts[1]}</mark>
                {labelParts[2]}
              </>
            ) : (
              label
            )}
          </span>
          {below ? (
            <span className="sd-session-row__match" dir="auto">
              {below[0]}
              <mark className="sd-session-row__mark">{below[1]}</mark>
              {below[2]}
            </span>
          ) : null}
          {excerpts?.map(([before, hit, after], i) => (
            <span key={i} className="sd-session-row__match" dir="auto">
              {before}
              {hit ? <mark className="sd-session-row__mark">{hit}</mark> : null}
              {after}
            </span>
          ))}
        </span>
        {unread ? <span className="sd-session-row__unread" aria-hidden="true" /> : null}
        <Age
          className="sd-session-row__age"
          dir="ltr"
          at={stamp}
          now={now}
          period={AGE_PERIOD_MS}
          format={(at) => shortAge(stamp, at)}
        />
      </button>
      <button
        type="button"
        className="sd-session-row__more"
        aria-label="Session menu"
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        tabIndex={-1}
        onClick={(event) => {
          event.stopPropagation()
          onMenu(id, event.currentTarget)
        }}
        onPointerDown={(event) => event.stopPropagation()}
      >
        <DotsIcon />
      </button>
    </div>
  )
})

/** A row being renamed: the tile, then the field where the number was. */
function RenameRow({
  run,
  show,
  sources,
  alias,
  onDone,
}: {
  run: RunSummary
  show: SessionsShow
  sources?: SourcesSummary
  alias?: string
  onDone: (alias: string | null) => void
}): JSX.Element {
  const shown = shownNumber(run, show, sources)
  const [value, setValue] = useState(alias ?? '')
  const input = useRef<HTMLInputElement | null>(null)
  // Enter commits and the field unmounts, which blurs it: one answer only.
  const answered = useRef(false)
  const finish = (next: string | null) => {
    if (answered.current) return
    answered.current = true
    onDone(next)
  }
  useEffect(() => {
    input.current?.focus()
    input.current?.select()
  }, [])
  return (
    <div className="sd-session-row" data-editing="true">
      <span className="sd-session-row__tile">
        <SourceMark adapter={shown.source?.adapter ?? shown.role} name={shown.source?.name} size="xs" />
      </span>
      <input
        ref={input}
        className="sd-session-row__rename"
        aria-label={`Rename ${shown.text}`}
        placeholder={shown.text}
        value={value}
        dir="auto"
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            finish(value)
          } else if (e.key === 'Escape') {
            e.preventDefault()
            e.stopPropagation()
            finish(null)
          }
        }}
        onBlur={() => finish(value)}
      />
    </div>
  )
}

/** A group's head: the Pinned label, or a divider that folds its rows away. */
function GroupHead({
  label,
  count,
  icon,
  expanded,
  controls,
  onToggle,
}: {
  label: string
  count?: number
  icon?: JSX.Element
  expanded?: boolean
  controls?: string
  onToggle?: () => void
}): JSX.Element {
  const text = count === undefined ? label : `${label} (${count})`
  if (!onToggle) {
    return (
      <div className="sd-sessions__settled" data-static="true">
        <span>{text}</span>
        <span className="sd-sessions__rule" aria-hidden="true" />
        {icon}
      </div>
    )
  }
  return (
    <button
      type="button"
      className="sd-sessions__settled"
      aria-expanded={expanded}
      aria-controls={controls}
      onClick={onToggle}
    >
      <span>{text}</span>
      <span className="sd-sessions__rule" aria-hidden="true" />
      <Chevron />
    </button>
  )
}

/**
 * The workspace's sessions, one line each, newest first: the runs still
 * running or waiting on a person, then a "Settled" divider that folds the
 * finished ones away, remembered in this browser. Above them a Pinned group
 * when this person has pinned any, and below them "Snoozed (n)" and
 * "Archived (n)", folded, when there are any — all of it from the record
 * in `lib/sessionPrefs`, per workspace.
 *
 * A row is the ticket number under its source's mark — the tracker's key or
 * the helpdesk's number, on the Settings › General preference — or the local
 * name this person gave it, and the age at the end. Nothing else: no title,
 * no kind. Those are on the card a row opens after a moment's hover or focus,
 * beside the sidebar: the title (over the real one, when the row is renamed),
 * the other number with its product's name, the kind and state, the provider
 * and model, and the workspace. A dot over the tile's corner says a run is
 * live or blocked, a dot before the age says a finished run is unread, and
 * the row's accessible name says both in words. The row itself opens the run.
 *
 * A right-click, a double-click, the dots at the row's end or the
 * context-menu key opens the row's menu (`ui/context-menu`): Pin, Mark
 * settled, Snooze ▸, Rename, Mark unread, Copy ▸, Open ▸, Workspace settings,
 * Archive, Delete…. Delete asks first, in a dialog, and the answer goes
 * through `actions.onDelete`.
 *
 * With a query the list is the rows that answer it, flat and unfolded, the
 * match marked in the label or on a line under it; in the notes mode it is
 * the runs the service found the query in, each with its excerpt.
 *
 * There is one card element, drawn after the list as the sidebar's child
 * rather than inside the scrolling list, and `useHoverCard` says which row
 * it is for and when: 120ms of rest opens it, the next row takes it over
 * with no wait, and it stays 150ms after the pointer leaves so the pointer
 * can reach it.
 */
function SessionsList({
  runs,
  currentRunId,
  onOpen,
  now,
  sources,
  workspaceName,
  workspaceId = '',
  rail = false,
  pinnedCard,
  actions,
  head,
  query = '',
  mode = 'sessions',
  hits,
  hitsState = 'idle',
  hitsError,
}: SessionsListProps): JSX.Element | null {
  const [showAll, setShowAll] = useState(false)
  const [collapsed, setCollapsed] = useState(readSettledCollapsed)
  const [snoozedOpen, setSnoozedOpen] = useState(false)
  const [archivedOpen, setArchivedOpen] = useState(false)
  const show = useSyncExternalStore(
    subscribeSessionsShow,
    sessionsShow,
    () => 'tracker' as SessionsShow,
  )
  const prefs: SessionPrefs = useSyncExternalStore(
    subscribeSessionPrefs,
    () => sessionPrefs(workspaceId),
    () => sessionPrefs(workspaceId),
  )
  const hover = useHoverCard()
  const rows = useRef(new Map<string, HTMLButtonElement>())
  const cardEl = useRef<HTMLDivElement | null>(null)
  const id = useId()
  const cardId = `${id}-card`
  const settledId = `${id}-settled`
  const snoozedId = `${id}-snoozed`
  const archivedId = `${id}-archived`

  // The clock the groups are cut against. A snoozed row comes back when
  // its time is up, so the list re-renders once at the earliest such time;
  // `tick` carries no value of its own, it is the re-render.
  const [tick, setTick] = useState(0)
  const clock = now ?? Date.now()
  useEffect(() => {
    if (now !== undefined) return
    const soonest = Math.min(
      ...Object.values(prefs.snoozed).map((s) => s.until).filter((t) => t > Date.now()),
    )
    if (!Number.isFinite(soonest)) return
    const timer = setTimeout(() => setTick((n) => n + 1), Math.max(1000, soonest - Date.now()))
    return () => clearTimeout(timer)
  }, [prefs.snoozed, now, tick])

  // The menu, the rename field, and the two dialogs.
  const [menuFor, setMenuFor] = useState<string | null>(null)
  const menuAnchor = useRef<Anchorable | null>(null)
  const [links, setLinks] = useState<RunLinks>({})
  const [renaming, setRenaming] = useState<string | null>(null)
  // The row to focus once the rename field has given way to it again.
  const focusAfterRename = useRef<string | null>(null)
  useEffect(() => {
    const id = focusAfterRename.current
    if (!id || renaming === id) return
    focusAfterRename.current = null
    rows.current.get(id)?.focus()
  }, [renaming])
  const [deleting, setDeleting] = useState<RunSummary | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const [picking, setPicking] = useState<RunSummary | null>(null)
  const [pickValue, setPickValue] = useState('')
  const [pickError, setPickError] = useState('')

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

  const register = useCallback((runId: string, el: HTMLButtonElement | null) => {
    if (el) rows.current.set(runId, el)
    else rows.current.delete(runId)
  }, [])

  // Opening a run reads it: the row stops being unread, and stays read
  // through a change that lands while it is open.
  const currentRun = currentRunId ? runs.find((r) => r.runId === currentRunId) : undefined
  const currentStamp = currentRun ? currentRun.updatedAt || currentRun.startedAt : ''
  useEffect(() => {
    if (!workspaceId || !currentRunId || !currentRun) return
    markRead(workspaceId, currentRunId, now ?? Date.now())
  }, [workspaceId, currentRunId, currentRun, currentStamp, now])

  // A snooze that no longer holds is forgotten, so the record stays small.
  useEffect(() => {
    if (workspaceId) pruneSnoozes(workspaceId, runs, now ?? Date.now())
  }, [workspaceId, runs, now])

  const openMenu = useCallback(
    (runId: string, at: Anchorable) => {
      menuAnchor.current = at
      hover.close()
      setLinks({})
      setMenuFor(runId)
      const load = actions?.loadLinks
      if (load) {
        void load(runId).then(
          (got) => setLinks((prev) => (menuAnchor.current === at ? got : prev)),
          () => {},
        )
      }
    },
    [hover, actions?.loadLinks],
  )
  const closeMenu = useCallback(() => setMenuFor(null), [])

  function toggleSettled(): void {
    const next = !collapsed
    setCollapsed(next)
    writeSettledCollapsed(next)
  }

  const toast = (text: string, tone?: 'info' | 'error') =>
    tone ? actions?.onToast?.(text, tone) : actions?.onToast?.(text)

  async function copy(what: string, text: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(text)
      toast(`Copied ${what}.`)
    } catch {
      toast(`Could not copy ${what}.`, 'error')
    }
  }

  function askSnoozeTime(run: RunSummary): void {
    setPickValue(toLocalInput(snoozeUntil('tomorrow', now ?? Date.now())))
    setPickError('')
    setPicking(run)
  }

  function snoozePicked(): void {
    if (!picking) return
    const at = new Date(pickValue).getTime()
    if (Number.isNaN(at) || at <= (now ?? Date.now())) {
      setPickError('Pick a time later than now.')
      return
    }
    snoozeRun(workspaceId, picking.runId, at, picking.status)
    setPicking(null)
  }

  async function confirmDelete(): Promise<void> {
    if (!deleting || !actions?.onDelete) return
    setDeleteBusy(true)
    setDeleteError('')
    try {
      await actions.onDelete(deleting.runId)
      forgetRun(workspaceId, deleting.runId)
      setDeleting(null)
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : String(err))
    } finally {
      setDeleteBusy(false)
    }
  }

  /** The menu for one row, as the record and the actions stand right now. */
  function buildMenu(run: RunSummary): MenuEntry[] {
    const runId = run.runId
    const forced = prefs.group[runId]
    const naturalLive = isLiveStatus(run.status)
    const currentLive = forced ? forced === 'live' : naturalLive
    const snoozed = isSnoozed(prefs, run, clock)
    const alias = prefs.aliases[runId]
    const note = run.notes[0]
    const items: MenuEntry[] = [
      {
        id: 'pin',
        label: runId in prefs.pinned ? 'Unpin' : 'Pin',
        onSelect: () => togglePin(workspaceId, runId, now ?? Date.now()),
      },
      {
        id: 'settle',
        label: currentLive ? 'Mark settled' : 'Un-settle',
        onSelect: () =>
          forceGroup(
            workspaceId,
            runId,
            currentLive ? (naturalLive ? 'settled' : null) : naturalLive ? null : 'live',
          ),
      },
      snoozed
        ? { id: 'snooze', label: 'Unsnooze', onSelect: () => unsnoozeRun(workspaceId, runId) }
        : {
            kind: 'submenu',
            id: 'snooze',
            label: 'Snooze',
            items: (
              [
                ['hour', '1 hour'],
                ['tomorrow', 'Until tomorrow 9:00'],
                ['nextWeek', 'Next week'],
              ] as [SnoozeChoice, string][]
            )
              .map<MenuEntry>(([choice, label]) => ({
                id: choice,
                label,
                onSelect: () =>
                  snoozeRun(workspaceId, runId, snoozeUntil(choice, now ?? Date.now()), run.status),
              }))
              .concat([{ id: 'pick', label: 'Pick a time…', onSelect: () => askSnoozeTime(run) }]),
          },
      { id: 'rename', label: 'Rename', onSelect: () => setRenaming(runId) },
    ]
    if (alias) {
      items.push({ id: 'reset-name', label: 'Reset name', onSelect: () => resetName(workspaceId, runId) })
    }
    const unread = isUnread(prefs, run)
    items.push({
      id: 'unread',
      label: unread ? 'Mark read' : 'Mark unread',
      disabled: naturalLive,
      onSelect: () =>
        unread ? markRead(workspaceId, runId, now ?? Date.now()) : markUnread(workspaceId, runId),
    })
    items.push({ kind: 'separator', id: 's1' })

    const copies: MenuEntry[] = [
      { id: 'key', label: 'Ticket key', detail: run.key, onSelect: () => void copy('ticket key', run.key) },
    ]
    const helpdesk = run.helpdeskKey?.trim() ?? ''
    if (helpdesk) {
      copies.push({
        id: 'helpdesk',
        label: 'Helpdesk number',
        detail: helpdeskNumber(helpdesk),
        onSelect: () => void copy('helpdesk number', helpdesk),
      })
    }
    copies.push({ id: 'run-id', label: 'Run id', detail: runId, onSelect: () => void copy('run id', runId) })
    if (note) copies.push({ id: 'note', label: 'Note path', onSelect: () => void copy('note path', note) })
    if (links.trackerUrl) {
      copies.push({ id: 'tracker-url', label: 'Tracker URL', onSelect: () => void copy('tracker URL', links.trackerUrl!) })
    }
    if (links.helpdeskUrl) {
      copies.push({ id: 'helpdesk-url', label: 'Helpdesk URL', onSelect: () => void copy('helpdesk URL', links.helpdeskUrl!) })
    }
    items.push({ kind: 'submenu', id: 'copy', label: 'Copy', items: copies })

    const opens: MenuEntry[] = []
    if (links.trackerUrl) {
      opens.push({
        id: 'in-tracker',
        label: `In ${sources?.tracker?.name || 'the tracker'}`,
        onSelect: () => window.open(links.trackerUrl, '_blank', 'noreferrer'),
      })
    }
    if (links.helpdeskUrl) {
      opens.push({
        id: 'in-helpdesk',
        label: `In ${sources?.helpdesk?.name || 'the helpdesk'}`,
        onSelect: () => window.open(links.helpdeskUrl, '_blank', 'noreferrer'),
      })
    }
    if (actions?.onOpenNote && note) {
      opens.push({ id: 'note-file', label: 'The note file', onSelect: () => actions.onOpenNote!(runId, note) })
    }
    if (actions?.onOpenRunDir) {
      opens.push({ id: 'run-dir', label: 'The run folder', onSelect: () => actions.onOpenRunDir!(runId) })
    }
    if (opens.length > 0) items.push({ kind: 'submenu', id: 'open', label: 'Open', items: opens })
    if (actions?.onOpenSettings) {
      items.push({ id: 'settings', label: 'Workspace settings', onSelect: actions.onOpenSettings })
    }
    items.push({ kind: 'separator', id: 's2' })
    items.push({
      id: 'archive',
      label: prefs.archived[runId] ? 'Unarchive' : 'Archive',
      onSelect: () => setArchived(workspaceId, runId, !prefs.archived[runId]),
    })
    if (actions?.onDelete) {
      items.push({
        id: 'delete',
        label: 'Delete…',
        tone: 'danger',
        onSelect: () => {
          setDeleteError('')
          setDeleting(run)
        },
      })
    }
    return items
  }

  const groups = useMemo(() => groupRuns(runs, prefs, clock), [runs, prefs, clock])

  // The filter, over every run whatever its group: a query is a question
  // about the whole workspace, not about what is unfolded.
  const filtering = mode === 'sessions' && query.trim() !== ''
  const matched: Matched[] = useMemo(() => {
    if (!filtering) return []
    const out: Matched[] = []
    for (const run of newestFirst(runs)) {
      const match = matchRun(run, query, { alias: prefs.aliases[run.runId], show, sources })
      if (match) out.push({ run, match })
    }
    return out
  }, [filtering, runs, query, prefs.aliases, show, sources])

  // Notes mode: one row per run the service found the query in, in the
  // order the hits came (newest run first), with its excerpts under it.
  const noteRows = useMemo(() => {
    if (mode !== 'notes') return []
    const byRun = new Map<string, string[]>()
    for (const hit of hits ?? []) {
      const list = byRun.get(hit.runId) ?? []
      if (list.length < 2) list.push(hit.excerpt)
      byRun.set(hit.runId, list)
    }
    const out: { run: RunSummary; excerpts: string[] }[] = []
    for (const [runId, excerpts] of byRun) {
      const run = runs.find((r) => r.runId === runId)
      if (run) out.push({ run, excerpts })
    }
    return out
  }, [mode, hits, runs])

  if (runs.length === 0) return null
  const open = card ? runs.find((run) => run.runId === card) : undefined
  const menuRun = menuFor ? runs.find((run) => run.runId === menuFor) : undefined

  function renderRow(
    run: RunSummary,
    extra: { match?: RunMatch; excerpts?: [string, string, string][] } = {},
  ): JSX.Element {
    if (renaming === run.runId) {
      return (
        <RenameRow
          key={run.runId}
          run={run}
          show={show}
          sources={sources}
          alias={prefs.aliases[run.runId]}
          onDone={(alias) => {
            if (alias !== null) renameRun(workspaceId, run.runId, alias)
            focusAfterRename.current = run.runId
            setRenaming(null)
          }}
        />
      )
    }
    return (
      <SessionRow
        key={run.runId}
        run={run}
        show={show}
        sources={sources}
        current={run.runId === currentRunId}
        isOpen={card === run.runId}
        cardId={cardId}
        now={now}
        hover={hover}
        pinned={pinned}
        alias={prefs.aliases[run.runId]}
        unread={isUnread(prefs, run)}
        menuOpen={menuFor === run.runId}
        match={extra.match}
        excerpts={extra.excerpts}
        onOpen={onOpen}
        onMenu={openMenu}
        register={register}
      />
    )
  }

  function renderCard(run: RunSummary): JSX.Element {
    const shown = shownNumber(run, show, sources)
    const alias = prefs.aliases[run.runId]
    // The other number with its product's name; a run with one number names
    // that one, so the card always says where the ticket lives. In the rail
    // the row shows no number at all, so the card carries the row's own too.
    const otherRole: SessionsShow = shown.role === 'tracker' ? 'helpdesk' : 'tracker'
    const hasOther = shown.other !== ''
    const numbers: { role: SessionsShow; text: string }[] = []
    if (rail || !hasOther || alias) numbers.push({ role: shown.role, text: withSource(shown.text, shown.source) })
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
          {alias ?? (run.title || shown.text)}
        </p>
        {alias ? (
          <p className="sd-session-card__subtitle" dir="auto">
            {run.title || shown.text}
          </p>
        ) : null}
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

  function renderBody(): JSX.Element {
    if (mode === 'notes') {
      const q = query.trim()
      if (!q) return <p className="sd-sessions__empty">Type to search the notes.</p>
      if (hitsState === 'loading') return <p className="sd-sessions__empty">Searching notes…</p>
      if (hitsState === 'error') {
        return <p className="sd-sessions__empty">Could not search notes. {hitsError}</p>
      }
      if (noteRows.length === 0) {
        return <p className="sd-sessions__empty">No notes match “{q}”.</p>
      }
      return (
        <>
          {noteRows.map(({ run, excerpts }) =>
            renderRow(run, { excerpts: excerpts.map((line) => splitText(line, q)) }),
          )}
        </>
      )
    }
    if (filtering) {
      if (matched.length === 0) {
        return <p className="sd-sessions__empty">No sessions match “{query.trim()}”.</p>
      }
      return <>{matched.map(({ run, match }) => renderRow(run, { match }))}</>
    }
    const { pinned: pinnedRows, live, settled, snoozed, archived } = groups
    const settledShown = showAll ? settled : settled.slice(0, SHOWN_LIMIT)
    const hidden = settled.length - settledShown.length
    return (
      <>
        {pinnedRows.length > 0 ? <GroupHead label="Pinned" icon={<PinIcon />} /> : null}
        {pinnedRows.map((run) => renderRow(run))}
        {live.map((run) => renderRow(run))}
        {settled.length > 0 ? (
          <GroupHead label="Settled" expanded={!collapsed} controls={settledId} onToggle={toggleSettled} />
        ) : null}
        {settled.length > 0 && !collapsed ? (
          <div id={settledId} className="sd-sessions__settled-rows">
            {settledShown.map((run) => renderRow(run))}
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
        {snoozed.length > 0 ? (
          <GroupHead
            label="Snoozed"
            count={snoozed.length}
            expanded={snoozedOpen}
            controls={snoozedId}
            onToggle={() => setSnoozedOpen((v) => !v)}
          />
        ) : null}
        {snoozed.length > 0 && snoozedOpen ? (
          <div id={snoozedId} className="sd-sessions__settled-rows">
            {snoozed.map((run) => renderRow(run))}
          </div>
        ) : null}
        {archived.length > 0 ? (
          <GroupHead
            label="Archived"
            count={archived.length}
            expanded={archivedOpen}
            controls={archivedId}
            onToggle={() => setArchivedOpen((v) => !v)}
          />
        ) : null}
        {archived.length > 0 && archivedOpen ? (
          <div id={archivedId} className="sd-sessions__settled-rows">
            {archived.map((run) => renderRow(run))}
          </div>
        ) : null}
      </>
    )
  }

  return (
    <>
      <nav className="sd-sidebar__sessions" aria-label="Sessions">
        {head}
        {renderBody()}
      </nav>
      {open ? renderCard(open) : null}
      {menuRun ? (
        <ContextMenu
          open
          anchor={menuAnchor}
          items={buildMenu(menuRun)}
          label={`Session menu for ${prefs.aliases[menuRun.runId] ?? shownNumber(menuRun, show, sources).text}`}
          onClose={closeMenu}
        />
      ) : null}
      <Dialog
        open={deleting !== null}
        title={deleting ? `Delete run ${deleting.key} · ${deleting.kind} · ${runDate(deleting)}?` : ''}
        onClose={() => {
          if (!deleteBusy) setDeleting(null)
        }}
        actions={
          <>
            <Button onClick={() => setDeleting(null)} disabled={deleteBusy}>
              Cancel
            </Button>
            <Button variant="primary" busy={deleteBusy} onClick={() => void confirmDelete()}>
              Delete
            </Button>
          </>
        }
      >
        <p>This removes its folder under .sirdar/runs. The register row and any filed note stay.</p>
        {deleteError ? (
          <p className="sd-sessions__dialog-error" role="alert">
            {deleteError}
          </p>
        ) : null}
      </Dialog>
      <Dialog
        open={picking !== null}
        title={picking ? `Snooze ${prefs.aliases[picking.runId] ?? picking.key} until` : ''}
        onClose={() => setPicking(null)}
        actions={
          <>
            <Button onClick={() => setPicking(null)}>Cancel</Button>
            <Button variant="primary" onClick={snoozePicked}>
              Snooze
            </Button>
          </>
        }
      >
        <input
          type="datetime-local"
          className="sd-sessions__when"
          aria-label="Snooze until"
          value={pickValue}
          onChange={(e) => setPickValue(e.target.value)}
        />
        <p className="sd-sessions__dialog-note">The row comes back at that time, or sooner if the run changes state.</p>
        {pickError ? (
          <p className="sd-sessions__dialog-error" role="alert">
            {pickError}
          </p>
        ) : null}
      </Dialog>
    </>
  )
}

export default memo(SessionsList)
