import { useEffect, useState, useSyncExternalStore } from 'react'
import type { Quota, RunSummary, Workspace } from '../../api/types'
import { showLibrary, subscribeShowLibrary } from '../../lib/library'
import type { Screen } from '../../store/appStore'
import Button from '../../ui/button'
import SidebarFooterCard from '../../ui/sidebar-footer-card'
import SidebarNavItem from '../../ui/sidebar-nav-item'
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

/** How many runs the Recent sessions list shows. */
export const RECENT_LIMIT = 4

/** Below this window width the sidebar keeps its icons and drops its labels. */
const RAIL_AT = '(max-width: 900px)'

/**
 * Whether the sidebar is down to its icon rail.
 *
 * `matchMedia` is guarded rather than assumed: the Wails webview has it, jsdom
 * does not always, and a shell that throws on mount in a test environment is a
 * shell nobody can test.
 */
function useRail(): boolean {
  const [rail, setRail] = useState(false)
  useEffect(() => {
    const media = globalThis.matchMedia?.(RAIL_AT)
    if (!media) return
    setRail(media.matches)
    const listen = (e: MediaQueryListEvent) => setRail(e.matches)
    media.addEventListener?.('change', listen)
    return () => media.removeEventListener?.('change', listen)
  }, [])
  return rail
}

function stamp(run: RunSummary): number {
  const updated = Date.parse(run.updatedAt ?? '')
  if (!Number.isNaN(updated)) return updated
  const started = Date.parse(run.startedAt ?? '')
  return Number.isNaN(started) ? 0 : started
}

/** The newest `limit` runs, by their last change. */
export function recentRuns(runs: RunSummary[], limit = RECENT_LIMIT): RunSummary[] {
  return runs
    .slice()
    .sort((a, b) => stamp(b) - stamp(a))
    .slice(0, limit)
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
 * The four sessions that changed last, under the nav.
 *
 * A row is the key in the ledger face and the kind beside it, with a dot that
 * says only whether the run is live or waiting on a person: the two states a
 * reader would open a session for. Everything else about a run is on the
 * board and in the session itself.
 */
function RecentSessions({
  runs,
  currentRunId,
  onOpen,
}: {
  runs: RunSummary[]
  currentRunId?: string
  onOpen: (runId: string) => void
}): JSX.Element | null {
  const recent = recentRuns(runs)
  if (recent.length === 0) return null
  return (
    <nav className="sd-sidebar__recent" aria-label="Recent sessions">
      <p className="sd-sidebar__recent-label">Recent sessions</p>
      {recent.map((run) => {
        const live = run.status === 'preparing' || run.status === 'running'
        const blocked = run.status === 'blocked'
        return (
          <button
            key={run.runId}
            type="button"
            className="sd-recent-row"
            aria-current={run.runId === currentRunId ? 'page' : undefined}
            aria-label={`${run.key} ${run.kind}${live ? ', running' : blocked ? ', needs input' : ''}`}
            onClick={() => onOpen(run.runId)}
          >
            <span
              className="sd-recent-row__dot"
              data-live={live ? 'true' : undefined}
              data-blocked={blocked ? 'true' : undefined}
              aria-hidden="true"
            />
            <span className="sd-recent-row__key" dir="ltr">
              {run.key}
            </span>
            <span className="sd-recent-row__kind" dir="ltr">
              {run.kind}
            </span>
          </button>
        )
      })}
    </nav>
  )
}

/**
 * The app's left edge: the wordmark with the workspace as a badge, the six
 * screens, the sessions that changed last, and a footer card with how much of
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
  /** The current workspace's runs; the newest four are listed under the nav. */
  runs?: RunSummary[]
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
    inboundCount = 0,
    onSelectWorkspace,
    onAddWorkspace,
    onNavigate,
  } = props
  const library = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const primary = usePrimaryAction()
  const rail = useRail()

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

      <RecentSessions
        runs={runs}
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
          primary?.inline ? (
            // The screen is drawing its own filled button; this one steps
            // down so the window still has one.
            <Button onClick={() => onNavigate({ name: 'new' })}>New session</Button>
          ) : primary ? (
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
            <Button variant="primary" onClick={() => onNavigate({ name: 'new' })}>
              New session
            </Button>
          )
        }
      />
    </div>
  )
}
