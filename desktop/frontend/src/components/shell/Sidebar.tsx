import { useState, useSyncExternalStore } from 'react'
import type { Quota, RunSummary, Workspace } from '../../api/types'
import { parseTime } from '../../lib/format'
import { showLibrary, subscribeShowLibrary } from '../../lib/library'
import { BELOW_COMPACT, useMediaQuery } from '../../lib/useMediaQuery'
import type { Screen } from '../../store/appStore'
import Button from '../../ui/button'
import ProviderMark from '../../ui/provider-mark'
import SidebarFooterCard from '../../ui/sidebar-footer-card'
import SidebarNavItem from '../../ui/sidebar-nav-item'
import { stateWord } from '../../ui/status-badge'
import QuotaMeter from '../QuotaMeter'
import {
  BoardIcon,
  EvalIcon,
  LibraryIcon,
  RegisterIcon,
  SessionsIcon,
  SettingsIcon,
  SwitcherIcon,
} from './icons'
import { usePrimaryAction } from './primaryAction'
import WorkspaceSwitcher from './WorkspaceSwitcher'
import './sidebar.css'

type NavName = 'sessions' | 'board' | 'register' | 'eval' | 'library' | 'settings'

const ROWS: { name: NavName; label: string; icon: JSX.Element }[] = [
  { name: 'sessions', label: 'Sessions', icon: <SessionsIcon /> },
  { name: 'board', label: 'Board', icon: <BoardIcon /> },
  { name: 'register', label: 'Register', icon: <RegisterIcon /> },
  { name: 'eval', label: 'Eval', icon: <EvalIcon /> },
  { name: 'library', label: 'Library', icon: <LibraryIcon /> },
  { name: 'settings', label: 'Settings', icon: <SettingsIcon /> },
]

/** How many rows the sessions list shows before "Show N more". */
export const SHOWN_LIMIT = 8

/**
 * Below this window width the sidebar keeps its icons and drops its labels:
 * the narrow band of `styles/tokens.css`, where the sheet needs every pixel.
 */
export const RAIL_AT = BELOW_COMPACT

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

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

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

/** Midnight at the start of the local day `ms` falls in. */
function dayStart(ms: number): number {
  const d = new Date(ms)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

/**
 * Which day a run last changed on, as the list heads it: "Today",
 * "Yesterday", then the date — "14 Sep", with the year once it is not this
 * one. Local time, since the reader's day is the one that matters.
 */
export function dayLabel(ms: number, now = Date.now()): string {
  const today = dayStart(now)
  const day = dayStart(ms)
  if (day === today) return 'Today'
  if (day === dayStart(today - DAY)) return 'Yesterday'
  const d = new Date(ms)
  const date = `${d.getDate()} ${MONTHS[d.getMonth()]}`
  return d.getFullYear() === new Date(now).getFullYear() ? date : `${date} ${d.getFullYear()}`
}

export interface RunGroup {
  label: string
  runs: RunSummary[]
}

/**
 * The runs newest first, grouped by the day they last changed, in the order
 * the days come. A run with no usable stamp goes last under "Earlier".
 */
export function groupRuns(runs: RunSummary[], now = Date.now()): RunGroup[] {
  const groups: RunGroup[] = []
  for (const run of recentRuns(runs)) {
    const ms = stamp(run)
    const label = ms === 0 ? 'Earlier' : dayLabel(ms, now)
    const last = groups[groups.length - 1]
    if (last && last.label === label) last.runs.push(run)
    else groups.push({ label, runs: [run] })
  }
  return groups
}

/** The nav row a screen belongs to. A run is reached from Sessions, a review from its run. */
function rowOf(screen: Screen): NavName {
  switch (screen.name) {
    case 'new':
    case 'run':
    case 'review':
      return 'sessions'
    default:
      return screen.name
  }
}

/**
 * The workspace's sessions, under the nav, grouped by the day they last
 * changed, and the sidebar's one scroll region.
 *
 * A row is the ticket's title — or its key, when the run knows no title —
 * ellipsised, with the key in the ledger face under it when a title is
 * shown, a 14px provider mark before it and the age at the end. A dot says
 * only whether the run is live or waiting on a person: the two states a
 * reader would open a session for. The accessible name says the same in
 * the badge's own words. The first eight rows show; "Show N more" is the
 * rest. Everything else about a run is on the board and in the session.
 */
function Sessions({
  runs,
  currentRunId,
  onOpen,
  now,
}: {
  runs: RunSummary[]
  currentRunId?: string
  onOpen: (runId: string) => void
  /** The clock, for the ages and the day heads; tests hold it still. */
  now?: number
}): JSX.Element | null {
  const [showAll, setShowAll] = useState(false)
  if (runs.length === 0) return null
  const at = now ?? Date.now()
  const shown = showAll ? runs : recentRuns(runs, SHOWN_LIMIT)
  const hidden = runs.length - shown.length
  const groups = groupRuns(shown, at)
  return (
    <nav className="sd-sidebar__sessions" aria-label="Sessions">
      {groups.map((group) => (
        <div key={group.label} className="sd-sidebar__day" role="group" aria-label={group.label}>
          <p className="sd-sidebar__day-label">{group.label}</p>
          {group.runs.map((run) => {
            const live = run.status === 'preparing' || run.status === 'running'
            const blocked = run.status === 'blocked'
            const word = live || blocked ? `, ${stateWord(run.status)}` : ''
            const title = run.title || ''
            const name = `${title ? `${title}, ` : ''}${run.key} ${run.kind}${word}`
            const age = shortAge(run.updatedAt || run.startedAt, at)
            return (
              <button
                key={run.runId}
                type="button"
                className="sd-session-row"
                aria-current={run.runId === currentRunId ? 'page' : undefined}
                aria-label={name}
                title={title || undefined}
                onClick={() => onOpen(run.runId)}
              >
                <ProviderMark provider={run.provider} size="sm" />
                <span className="sd-session-row__body">
                  <span className="sd-session-row__title" dir="auto">
                    {title || run.key}
                  </span>
                  {title ? (
                    <span className="sd-session-row__key" dir="ltr">
                      {run.key}
                      <span className="sd-session-row__kind"> · {run.kind}</span>
                    </span>
                  ) : null}
                </span>
                {live || blocked ? (
                  <span
                    className="sd-session-row__dot"
                    data-live={live ? 'true' : undefined}
                    data-blocked={blocked ? 'true' : undefined}
                    aria-hidden="true"
                  />
                ) : null}
                <span className="sd-session-row__age" dir="ltr">
                  {age}
                </span>
              </button>
            )
          })}
        </div>
      ))}
      {hidden > 0 ? (
        <button type="button" className="sd-sidebar__more" onClick={() => setShowAll(true)}>
          Show {hidden} more
        </button>
      ) : null}
    </nav>
  )
}

/**
 * The app's left edge: the wordmark with the workspace as a badge, the six
 * screens, the sessions by day, and a footer card with how much of
 * each provider's plan is gone and the one button that starts a session.
 *
 * It replaces the top header. A board that scrolls horizontally has no room to
 * spare above it, and a nav that does not move is one less thing that can
 * cover a lane — which is the reason `docs/design/03-desktop-app.md` section 5
 * gives, and it is the same reason the reference it is read from did it. The
 * 2026-09-15 screens round re-scaled it to the reference's own register:
 * 248 wide, 44-tall rows, 20px icons.
 *
 * Settings is a row here but not a screen: it opens a modal over whatever is
 * behind it, because settings is a place you leave and the board staying
 * painted is what says you are coming back.
 */
export default function Sidebar(props: {
  workspaces: Workspace[]
  currentWorkspaceId: string
  quota: Quota[]
  screen: Screen
  /** The current workspace's runs, listed under the nav by day. */
  runs?: RunSummary[]
  /** The clock the ages are read against; tests hold it still. */
  now?: number
  /** Inbound deliveries waiting to be read, badged on the Board row. */
  inboundCount?: number
  onSelectWorkspace: (id: string) => void
  onAddWorkspace: () => void
  onNavigate: (screen: Screen) => void
}): JSX.Element {
  const {
    workspaces,
    currentWorkspaceId,
    quota,
    screen,
    runs = [],
    now,
    inboundCount = 0,
    onSelectWorkspace,
    onAddWorkspace,
    onNavigate,
  } = props
  const library = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const primary = usePrimaryAction()
  const rail = useMediaQuery(RAIL_AT)

  const current = rowOf(screen)
  const rows = library ? ROWS : ROWS.filter((row) => row.name !== 'library')
  const currentRunId =
    screen.name === 'run' || screen.name === 'review' ? screen.runId : undefined
  const newest = recentRuns(runs, 1)[0]

  function open(name: NavName): void {
    if (name === 'sessions') {
      // Sessions is the session that changed last, or a new one when the
      // workspace has none yet.
      onNavigate(newest ? { name: 'run', runId: newest.runId } : { name: 'new' })
      return
    }
    onNavigate({ name })
  }

  return (
    <div className="sd-sidebar" data-collapsed={rail ? 'true' : undefined}>
      <div className="sd-sidebar__brand">
        <span className="brand">Sirdar</span>
        <WorkspaceSwitcher
          workspaces={workspaces}
          currentId={currentWorkspaceId}
          onSelect={onSelectWorkspace}
          onAdd={onAddWorkspace}
        />
      </div>

      <nav className="sd-sidebar__nav" aria-label="Screens">
        {rows.map((row) => (
          <SidebarNavItem
            key={row.name}
            icon={row.icon}
            label={row.label}
            current={row.name === current}
            count={row.name === 'board' ? inboundCount : undefined}
            countLabel={row.name === 'board' ? 'inbound deliveries' : undefined}
            title={rail ? row.label : undefined}
            onSelect={() => open(row.name)}
          />
        ))}
      </nav>

      <Sessions
        runs={runs}
        now={now}
        currentRunId={currentRunId}
        onOpen={(runId) => onNavigate({ name: 'run', runId })}
      />

      <SidebarFooterCard
        label="Plan usage"
        title={
          <>
            <span>Plan usage</span>
            <SwitcherIcon />
          </>
        }
        quotas={quota.length > 0 ? <QuotaMeter quota={quota} /> : undefined}
        action={
          primary && primary.placement !== 'screen' ? (
            <Button
              variant="primary"
              busy={primary.busy}
              shortcut={primary.shortcut}
              disabled={primary.disabled}
              title={primary.title}
              onClick={primary.onRun}
            >
              {primary.label}
            </Button>
          ) : (
            // Demoted to the bordered style while a screen draws its own
            // filled button, so the window never has two.
            <Button
              variant={primary ? 'secondary' : 'primary'}
              onClick={() => onNavigate({ name: 'new' })}
            >
              New session
            </Button>
          )
        }
      />
    </div>
  )
}
