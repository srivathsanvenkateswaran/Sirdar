import { useEffect, useState, useSyncExternalStore } from 'react'
import type { Quota, Workspace } from '../../api/types'
import { showLibrary, subscribeShowLibrary } from '../../lib/library'
import type { Screen } from '../../store/appStore'
import Button from '../../ui/button'
import SidebarFooterCard from '../../ui/sidebar-footer-card'
import SidebarNavItem from '../../ui/sidebar-nav-item'
import QuotaMeter from '../QuotaMeter'
import { BoardIcon, EvalIcon, LibraryIcon, RegisterIcon, SettingsIcon } from './icons'
import { usePrimaryAction } from './primaryAction'
import WorkspaceSwitcher from './WorkspaceSwitcher'
import './sidebar.css'

type NavName = Extract<Screen['name'], 'board' | 'register' | 'eval' | 'library' | 'settings'>

const ROWS: { name: NavName; label: string; icon: JSX.Element }[] = [
  { name: 'board', label: 'Board', icon: <BoardIcon /> },
  { name: 'register', label: 'Register', icon: <RegisterIcon /> },
  { name: 'eval', label: 'Eval', icon: <EvalIcon /> },
  { name: 'settings', label: 'Settings', icon: <SettingsIcon /> },
]

/** The design library is a row only while Settings says it is. */
const LIBRARY_ROW: { name: NavName; label: string; icon: JSX.Element } = {
  name: 'library',
  label: 'Library',
  icon: <LibraryIcon />,
}

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

/**
 * The app's left edge: where you are, which repository you are looking at,
 * what is left of each provider's limit, and the one action this screen can
 * commit.
 *
 * It replaces the top header. A board that scrolls horizontally has no room to
 * spare above it, and a nav that does not move is one less thing that can
 * cover a lane — which is the reason `docs/design/03-desktop-app.md` section 5
 * gives, and it is the same reason the reference it is read from did it.
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
    inboundCount = 0,
    onSelectWorkspace,
    onAddWorkspace,
    onNavigate,
  } = props
  const library = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const primary = usePrimaryAction()
  const rail = useRail()

  // Run detail is reached from a card rather than from here, so it keeps the
  // Board row current while it is open.
  const current = screen.name === 'run' ? 'board' : screen.name
  const rows = library ? [...ROWS.slice(0, 3), LIBRARY_ROW, ROWS[3]] : ROWS

  return (
    <div className="sd-sidebar" data-collapsed={rail ? 'true' : undefined}>
      <div className="sd-sidebar__brand">
        <span className="brand">Sirdar</span>
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
            onSelect={() => onNavigate({ name: row.name } as Screen)}
          />
        ))}
      </nav>

      <SidebarFooterCard
        switcher={
          <WorkspaceSwitcher
            workspaces={workspaces}
            currentId={currentWorkspaceId}
            onSelect={onSelectWorkspace}
            onAdd={onAddWorkspace}
          />
        }
        quotas={quota.length > 0 ? <QuotaMeter quota={quota} /> : undefined}
        action={
          primary ? (
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
          ) : undefined
        }
      />
    </div>
  )
}
