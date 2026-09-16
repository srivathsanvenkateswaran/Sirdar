import { useSyncExternalStore } from 'react'
import type { Quota, RunSummary, SourcesSummary, Workspace } from '../../api/types'
import { showLibrary, subscribeShowLibrary } from '../../lib/library'
import { BELOW_COMPACT, useMediaQuery } from '../../lib/useMediaQuery'
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
import SessionsList, { recentRuns } from './SessionsList'
import WorkspaceSwitcher from './WorkspaceSwitcher'
import './sidebar.css'

export { CARD_DELAY_MS, SHOWN_LIMIT, recentRuns, shortAge, splitRuns } from './SessionsList'

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
export default function Sidebar(props: {
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
  const rail = useMediaQuery(RAIL_AT)

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
