import { memo, useCallback, useEffect, useState, useSyncExternalStore } from 'react'
import type { Quota, RunSummary, SourcesSummary, Workspace } from '../../api/types'
import { showLibrary, subscribeShowLibrary } from '../../lib/library'
import { readStoredFlag, writeStoredFlag } from '../../lib/storedFlag'
import { BELOW_COMPACT, useMediaQuery } from '../../lib/useMediaQuery'
import type { Screen } from '../../store/appStore'
import Button from '../../ui/button'
import PanelToggle from '../../ui/panel-toggle'
import SidebarFooterCard from '../../ui/sidebar-footer-card'
import SidebarNavItem from '../../ui/sidebar-nav-item'
import QuotaMeter from '../QuotaMeter'
import {
  BoardIcon,
  EvalIcon,
  LibraryIcon,
  PlusIcon,
  RegisterIcon,
  SessionsIcon,
  SettingsIcon,
  SwitcherIcon,
} from './icons'
import { usePrimaryAction } from './primaryAction'
import SessionsList, { recentRuns } from './SessionsList'
import WorkspaceSwitcher from './WorkspaceSwitcher'
import './sidebar.css'

export { CARD_CLOSE_MS, CARD_OPEN_MS, SHOWN_LIMIT, recentRuns, shortAge, splitRuns } from './SessionsList'

type NavName = 'sessions' | 'board' | 'register' | 'eval' | 'library' | 'settings'

const ROWS: { name: NavName; label: string; icon: JSX.Element }[] = [
  { name: 'sessions', label: 'Sessions', icon: <SessionsIcon /> },
  { name: 'board', label: 'Board', icon: <BoardIcon /> },
  { name: 'register', label: 'Register', icon: <RegisterIcon /> },
  { name: 'eval', label: 'Eval', icon: <EvalIcon /> },
  { name: 'library', label: 'Library', icon: <LibraryIcon /> },
  { name: 'settings', label: 'Settings', icon: <SettingsIcon /> },
]

/**
 * Below this window width the sidebar keeps its icons and drops its labels:
 * the narrow band of `styles/tokens.css`, where the sheet needs every pixel.
 */
export const RAIL_AT = BELOW_COMPACT

/**
 * The reader's own fold: the sidebar as the 56px rail at any width, on the
 * head's toggle or ⌘B, remembered in this browser. The automatic rail under
 * 1024 applies regardless.
 */
export const SIDEBAR_COLLAPSED_KEY = 'sirdar.sidebarCollapsed'

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
 * The app's left edge: the wordmark with the workspace as a badge, the six
 * screens, the sessions list (`SessionsList`), and a footer card with how
 * much of each provider's plan is gone and the one button that starts a
 * session.
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
function Sidebar(props: {
  workspaces: Workspace[]
  currentWorkspaceId: string
  quota: Quota[]
  screen: Screen
  /** The current workspace's runs, listed under the nav. */
  runs?: RunSummary[]
  /** The current workspace's tracker and helpdesk, for the marks beside each number. */
  sources?: SourcesSummary
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
    sources,
    now,
    inboundCount = 0,
    onSelectWorkspace,
    onAddWorkspace,
    onNavigate,
  } = props
  const library = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const primary = usePrimaryAction()
  const auto = useMediaQuery(RAIL_AT)
  const [folded, setFolded] = useState(() => readStoredFlag(SIDEBAR_COLLAPSED_KEY))
  const rail = auto || folded

  function toggleFold(): void {
    const next = !folded
    setFolded(next)
    writeStoredFlag(SIDEBAR_COLLAPSED_KEY, next)
  }

  // ⌘B, from anywhere in the window. Not while the width has already made
  // the choice: a chord that appears to do nothing is worse than none.
  useEffect(() => {
    if (auto) return
    function onKey(e: KeyboardEvent): void {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return
      if (e.key !== 'b' && e.key !== 'B') return
      e.preventDefault()
      const next = !folded
      setFolded(next)
      writeStoredFlag(SIDEBAR_COLLAPSED_KEY, next)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [auto, folded])

  const openRun = useCallback((runId: string) => onNavigate({ name: 'run', runId }), [onNavigate])

  const current = rowOf(screen)
  const rows = library ? ROWS : ROWS.filter((row) => row.name !== 'library')
  const currentRunId =
    screen.name === 'run' || screen.name === 'review' ? screen.runId : undefined
  const newest = recentRuns(runs, 1)[0]
  const workspaceName = workspaces.find((w) => w.id === currentWorkspaceId)?.name

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
        <PanelToggle
          side="start"
          open={!rail}
          hideLabel="Hide sidebar"
          showLabel="Show sidebar"
          shortcut="⌘B"
          disabled={auto}
          disabledReason="The sidebar is a rail at this width"
          onToggle={toggleFold}
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

      <SessionsList
        runs={runs}
        now={now}
        sources={sources}
        workspaceName={workspaceName}
        currentRunId={currentRunId}
        rail={rail}
        onOpen={openRun}
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
            // filled button, so the window never has two. In the rail the
            // words go and a plus stays, named the same.
            <Button
              variant={primary ? 'secondary' : 'primary'}
              size={rail ? 'sm' : 'md'}
              iconOnly={rail}
              icon={rail ? <PlusIcon /> : undefined}
              title={rail ? 'New session' : undefined}
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

/**
 * Memoised: the shell re-renders for every slice a screen reads, and the
 * sidebar's inputs — the runs, the quota, the screen — change far less often
 * than that. The callbacks it is given are stable, so a render of the shell
 * that changed none of them costs the sidebar nothing.
 */
export default memo(Sidebar)
