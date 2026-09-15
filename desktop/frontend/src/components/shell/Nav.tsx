import { useSyncExternalStore } from 'react'
import { showLibrary, subscribeShowLibrary } from '../../lib/library'
import type { Screen } from '../../store/appStore'

const TABS: { name: Screen['name']; label: string }[] = [
  { name: 'board', label: 'Board' },
  { name: 'register', label: 'Register' },
  { name: 'eval', label: 'Eval' },
  { name: 'settings', label: 'Settings' },
]

/** The design library is a tab only while Settings says it is. */
const LIBRARY_TAB: { name: Screen['name']; label: string } = { name: 'library', label: 'Library' }

/**
 * Top-level screens. Run detail is reached from a card rather than from here,
 * so it marks the Board tab as current while it is open.
 */
export default function Nav(props: {
  screen: Screen
  onNavigate: (screen: Screen) => void
}): JSX.Element {
  const { screen, onNavigate } = props
  const active = screen.name === 'run' ? 'board' : screen.name
  const library = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const tabs = library ? [...TABS, LIBRARY_TAB] : TABS

  return (
    <nav className="nav" aria-label="Screens">
      {tabs.map((tab) => (
        <button
          key={tab.name}
          type="button"
          className="nav-item"
          aria-current={tab.name === active ? 'page' : undefined}
          onClick={() => onNavigate({ name: tab.name } as Screen)}
        >
          {tab.label}
        </button>
      ))}
    </nav>
  )
}
