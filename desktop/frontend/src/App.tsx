import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import Header from './components/shell/Header'
import NewTriageDialog from './components/shell/NewTriageDialog'
import Toasts from './components/shell/Toast'
import Board from './screens/Board'
import Eval from './screens/Eval'
import Library from './screens/Library'
import Register from './screens/Register'
import RunDetail from './screens/RunDetail'
import Settings from './screens/Settings'
import { showLibrary, subscribeShowLibrary } from './lib/library'
import { parseRoute, routeHash, sameScreen } from './lib/routes'
import type { AppState, AppStore, EvalOptions, FixOptions, RCAOptions, Screen } from './store/appStore'
import { useAppState, useStore } from './store/useAppStore'

/**
 * A rejection the store has already toasted. The forms show the reason beside
 * their own button; a start with no form behind it has nothing else to do with
 * it, and an unhandled rejection would only reach the console.
 */
function reported(): void {}

/**
 * Keeps the address bar and the store's screen in step, both ways.
 *
 * Reading: whatever the hash names is opened — on load, on Back, and on a
 * pasted link. Nothing is applied until `init()` has answered, because a run
 * link names a workspace and there are none to match it against before then.
 *
 * Writing: every move the window makes — the header's buttons, a board card,
 * the store's own navigation — leaves an address that can be linked to. A hash
 * that names nothing opens nothing and is then overwritten by the screen the
 * window is really on, so the address never describes a screen that is not up.
 */
function useHashRoute(store: AppStore, state: AppState): void {
  const { screen, currentWorkspaceId, workspaces, loading } = state

  useEffect(() => {
    function apply(): void {
      const route = parseRoute(window.location.hash)
      if (!route) return
      const now = store.getState()
      if (now.loading) return
      const wants = route.workspaceId
      if (
        sameScreen(now.screen, route.screen) &&
        (!wants || wants === now.currentWorkspaceId || !workspaces.some((w) => w.id === wants))
      ) {
        return
      }
      store.openRoute(route.screen, wants)
    }
    apply()
    window.addEventListener('hashchange', apply)
    return () => window.removeEventListener('hashchange', apply)
  }, [store, loading, workspaces])

  useEffect(() => {
    if (loading) return
    const want = routeHash(screen, currentWorkspaceId)
    if (window.location.hash === want) return
    // A window that opened with no hash at all gets one without a history
    // entry, so Back still leads out of the app rather than to the board.
    if (window.location.hash === '') {
      window.history.replaceState(null, '', want)
      return
    }
    window.location.hash = want
  }, [screen, currentWorkspaceId, loading])
}

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
  const libraryOn = useSyncExternalStore(subscribeShowLibrary, showLibrary, () => false)
  const [triageOpen, setTriageOpen] = useState(false)
  const filterRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    void store.init()
  }, [store])

  useHashRoute(store, state)

  useEffect(() => {
    if (state.screen.name === 'library' && !libraryOn) store.navigate({ name: 'board' })
  }, [state.screen, libraryOn, store])

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

  // Every start goes through the store, which keeps the job id the run detail
  // screen's Cancel button needs. A start that fails rejects as well as
  // toasting, so the form that asked can show the reason beside its button;
  // the two call sites with no form behind them swallow it here instead, and
  // the toast is what the reader sees.
  const startRCA = useCallback((key: string, opts?: RCAOptions) => store.startRCA(key, opts), [
    store,
  ])
  const startFix = useCallback((key: string, opts?: FixOptions) => store.startFix(key, opts), [
    store,
  ])
  const startEval = useCallback(
    (keys?: string[], opts?: EvalOptions) => store.startEval(keys, opts),
    [store],
  )
  const cancelJob = useCallback((jobId: string) => store.cancelJob(jobId), [store])

  let screen
  switch (state.screen.name) {
    case 'run':
      screen = (
        <RunDetail
          transport={state.transport}
          workspaceId={workspaceId}
          runId={state.screen.runId}
          defaultProvider={currentWorkspace?.provider}
          onBack={() => navigate({ name: 'board' })}
          onStartRCA={startRCA}
          onStartFix={startFix}
        />
      )
      break
    case 'register':
      screen = <Register transport={state.transport} workspaceId={workspaceId} />
      break
    case 'eval':
      screen = (
        <Eval
          transport={state.transport}
          workspaceId={workspaceId}
          defaultProvider={currentWorkspace?.provider}
          jobs={state.keylessJobs.filter((j) => j.workspaceId === workspaceId)}
          onStartEval={startEval}
          onCancelJob={cancelJob}
        />
      )
      break
    case 'library':
      // The switch can be turned off while the gallery is open, and a link to
      // it can be pasted into a window that has it off. Either way the window
      // shows the board and the address catches up, rather than showing a
      // screen the reader has said they do not want.
      screen = libraryOn ? <Library /> : null
      break
    case 'settings':
      screen = (
        <Settings
          transport={state.transport}
          workspaces={state.workspaces}
          currentWorkspaceId={workspaceId}
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
          inbound={state.inbound}
          filterRef={filterRef}
          onOpenRun={(runId) => navigate({ name: 'run', runId })}
          onTriage={(keys) => void store.startTriage(keys).catch(reported)}
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
        {state.workspaces.length === 0 && !state.loading && state.screen.name !== 'library' ? (
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
        onSubmit={(keys, opts) => void store.startTriage(keys, opts).catch(reported)}
      />
      <Toasts toasts={state.toasts} onDismiss={dismissToast} />
    </div>
  )
}
