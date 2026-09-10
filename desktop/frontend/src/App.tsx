import { useCallback, useEffect, useRef, useState } from 'react'
import Header from './components/shell/Header'
import NewTriageDialog from './components/shell/NewTriageDialog'
import Toasts from './components/shell/Toast'
import Board from './screens/Board'
import Register from './screens/Register'
import RunDetail from './screens/RunDetail'
import Settings from './screens/Settings'
import type { Screen } from './store/appStore'
import { useAppState, useStore } from './store/useAppStore'

/** Keys typed into a field belong to that field, not to the window. */
function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName.toLowerCase()
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable
}

export default function App(): JSX.Element {
  const store = useStore()
  const state = useAppState()
  const [triageOpen, setTriageOpen] = useState(false)
  const filterRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    void store.init()
  }, [store])

  const workspaceId = state.currentWorkspaceId
  const currentWorkspace = state.workspaces.find((w) => w.id === workspaceId)
  const runs = state.runsByWorkspace[workspaceId] ?? []
  const tickets = state.ticketsByWorkspace[workspaceId] ?? []

  const navigate = useCallback((screen: Screen) => store.navigate(screen), [store])
  const dismissToast = useCallback((id: number) => store.dismissToast(id), [store])

  const openTriage = useCallback(() => {
    if (state.workspaces.length === 0) {
      store.toast('Add a workspace before starting a run.', 'error')
      return
    }
    setTriageOpen(true)
  }, [state.workspaces.length, store])

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent): void {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === 'Escape' && triageOpen) {
        setTriageOpen(false)
        return
      }
      if (triageOpen || isTyping(e.target)) return
      if (e.key === 'n') {
        e.preventDefault()
        openTriage()
        return
      }
      if (e.key === '/' && state.screen.name === 'board') {
        e.preventDefault()
        filterRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [openTriage, state.screen.name, triageOpen])

  // Both starts go through the store, which keeps the job id the run detail
  // screen's Cancel button needs.
  const startRCA = useCallback(
    (key: string, opts?: { prUrl?: string; resolution?: string }) => store.startRCA(key, opts),
    [store],
  )

  let screen
  switch (state.screen.name) {
    case 'run':
      screen = (
        <RunDetail
          transport={state.transport}
          workspaceId={workspaceId}
          runId={state.screen.runId}
          onBack={() => navigate({ name: 'board' })}
          onStartRCA={startRCA}
        />
      )
      break
    case 'register':
      screen = <Register transport={state.transport} workspaceId={workspaceId} />
      break
    case 'settings':
      screen = (
        <Settings
          transport={state.transport}
          workspaces={state.workspaces}
          onWorkspacesChanged={() => void store.refresh()}
        />
      )
      break
    default:
      screen = (
        <Board
          tickets={tickets}
          runs={runs}
          queueUnsupported={Boolean(state.queueUnsupported[workspaceId])}
          loading={state.loading}
          filterRef={filterRef}
          onOpenRun={(runId) => navigate({ name: 'run', runId })}
          onTriage={(keys) => void store.startTriage(keys)}
        />
      )
  }

  return (
    <div className="app">
      <Header
        workspaces={state.workspaces}
        currentWorkspaceId={workspaceId}
        quota={state.quota}
        screen={state.screen}
        onSelectWorkspace={(id) => store.setWorkspace(id)}
        onAddWorkspace={() => navigate({ name: 'settings' })}
        onNavigate={navigate}
        onNewTriage={openTriage}
      />
      <main className="main">
        {state.workspaces.length === 0 && !state.loading ? (
          <p className="app-empty">
            No workspace yet. Open Settings and add the path to a repository that has a{' '}
            <code>.sirdar</code> config.
          </p>
        ) : (
          screen
        )}
      </main>
      <NewTriageDialog
        open={triageOpen}
        defaultProvider={currentWorkspace?.provider}
        onClose={() => setTriageOpen(false)}
        onSubmit={(keys, opts) => void store.startTriage(keys, opts)}
      />
      <Toasts toasts={state.toasts} onDismiss={dismissToast} />
    </div>
  )
}
