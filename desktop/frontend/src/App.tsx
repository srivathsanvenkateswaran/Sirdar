import { useState } from 'react'

type Screen = 'board' | 'register' | 'settings'

const SCREENS: { id: Screen; label: string }[] = [
  { id: 'board', label: 'Board' },
  { id: 'register', label: 'Register' },
  { id: 'settings', label: 'Settings' },
]

export default function App() {
  const [screen, setScreen] = useState<Screen>('board')

  return (
    <div className="app">
      <header className="app-header">
        <h1 className="app-title">Sirdar</h1>
        <nav className="app-nav">
          {SCREENS.map((s) => (
            <button
              key={s.id}
              type="button"
              className={s.id === screen ? 'nav-item nav-item--active' : 'nav-item'}
              aria-current={s.id === screen ? 'page' : undefined}
              onClick={() => setScreen(s.id)}
            >
              {s.label}
            </button>
          ))}
        </nav>
      </header>
      <main className="app-main" data-screen={screen} />
    </div>
  )
}
