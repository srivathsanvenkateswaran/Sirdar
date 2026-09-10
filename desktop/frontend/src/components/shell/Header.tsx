import type { Quota, Workspace } from '../../api/types'
import QuotaMeter from '../QuotaMeter'
import Nav from './Nav'
import WorkspaceSwitcher from './WorkspaceSwitcher'
import type { Screen } from '../../store/appStore'

/**
 * One bar for everything that is true of the whole window: where you are, which
 * repository you are looking at, what is left of the provider's rate limit, and
 * the one action that starts work.
 */
export default function Header(props: {
  workspaces: Workspace[]
  currentWorkspaceId: string
  quota: Quota[]
  screen: Screen
  onSelectWorkspace: (id: string) => void
  onAddWorkspace: () => void
  onNavigate: (screen: Screen) => void
  onNewTriage: () => void
}): JSX.Element {
  const {
    workspaces,
    currentWorkspaceId,
    quota,
    screen,
    onSelectWorkspace,
    onAddWorkspace,
    onNavigate,
    onNewTriage,
  } = props

  return (
    <header className="header">
      <span className="brand">Sirdar</span>
      <Nav screen={screen} onNavigate={onNavigate} />
      <span className="header-gap" />
      <WorkspaceSwitcher
        workspaces={workspaces}
        currentId={currentWorkspaceId}
        onSelect={onSelectWorkspace}
        onAdd={onAddWorkspace}
      />
      <QuotaMeter quota={quota} />
      <button
        type="button"
        className="button button--accent"
        onClick={onNewTriage}
        disabled={workspaces.length === 0}
        title="New triage (n)"
      >
        New triage
        <kbd className="kbd">n</kbd>
      </button>
    </header>
  )
}
